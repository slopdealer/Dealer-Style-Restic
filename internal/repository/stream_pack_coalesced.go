package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

const maxStreamPackRequestSize = 32 * 1024 * 1024

var errStreamPackYield = fmt.Errorf("release pack response before recovery")

// packRangeEnd preserves the window and gap rules. Request spans use the same
// rule with a larger limit; an oversized individual blob still travels alone.
func packRangeEnd(blobs pack.Blobs, limit uint) int {
	end := blobs[0].Offset + blobs[0].Length
	for i := 1; i < len(blobs); i++ {
		if blobs[i].Offset-end > maxUnusedRange ||
			blobs[i].Offset+blobs[i].Length-blobs[0].Offset >= limit {
			return i
		}
		end = blobs[i].Offset + blobs[i].Length
	}
	return len(blobs)
}

func streamPackCoalesced(ctx context.Context, load backendLoadFn, recoverBlob loadBlobFn, dec *zstd.Decoder, key *crypto.Key, packID restic.ID, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error, window uint, pooled bool) error {
	if len(blobs) == 0 {
		return nil
	}
	blobs.Sort()
	end := blobs[0].Offset
	for _, blob := range blobs {
		if blob.Offset < end {
			return fmt.Errorf("overlapping blobs in pack %v", packID)
		}
		end = blob.Offset + blob.Length
	}
	var data []byte
	if pooled {
		data = takeStreamPackBuffer()
		defer func() { putStreamPackBuffer(data) }()
	}
	for len(blobs) > 0 {
		n := packRangeEnd(blobs, maxStreamPackRequestSize)
		span := blobs[:n]
		if packRangeEnd(span, window) == len(span) {
			// Preserve the existing path when there is no request to eliminate.
			if err := streamPackPartWithBuffer(ctx, load, recoverBlob, dec, key, packID, span, &data, handle); err != nil {
				return err
			}
		} else if err := streamPackResponse(ctx, load, recoverBlob, dec, key, packID, span, &data, handle, window); err != nil {
			return err
		}
		blobs = blobs[n:]
	}
	return nil
}

// Delivery is committed only after the handler returns nil. A replay consumes
// that prefix without decoding or delivering it again. This bounds memory but
// does not avoid retransferring the prefix: retry.Load owns fixed arguments.
func streamPackResponse(ctx context.Context, load backendLoadFn, recoverBlob loadBlobFn, dec *zstd.Decoder, key *crypto.Key, packID restic.ID, blobs pack.Blobs, data *[]byte, handle func(restic.BlobHandle, []byte, error) error, window uint) error {
	requestCtx, stop := context.WithCancel(ctx)
	defer stop()
	if policy, ok := ctx.Value(smallRestoreDecodeKey{}).(smallRestoreDecodePolicy); ok {
		// Admission promotion must happen after Load releases its connection.
		policy.ensureOrdinary = func(context.Context) error { return errStreamPackYield }
		requestCtx = context.WithValue(requestCtx, smallRestoreDecodeKey{}, policy)
	}
	committed := 0
	var handlerErr error
	yield := false
	var finalPart pack.Blobs
	// The final window is read here and decoded after Load returns: its bytes
	// are taken here and handed to that decode.
	var finalHold *restoreWindowHold
	defer func() { finalHold.release(nil) }()
	start := blobs[0].Offset
	end := blobs[len(blobs)-1].Offset + blobs[len(blobs)-1].Length
	h := backend.Handle{Type: backend.PackFile, Name: packID.String(), IsMetadata: blobs[0].Type.IsMetadata()}
	err := load(requestCtx, h, int(end-start), int64(start), func(rd io.Reader) error {
		finalPart = nil
		position := start
		for committed < len(blobs) {
			remaining := blobs[committed:]
			if _, err := io.CopyN(io.Discard, rd, int64(remaining[0].Offset-position)); err != nil {
				return err
			}
			n := packRangeEnd(remaining, window)
			part := remaining[:n]
			if n == len(remaining) {
				// Keep the final window uncommitted until terminal read/close
				// succeeds. It can then recover outside the connection as usual.
				size := int(end - part[0].Offset)
				// a replayed response may end in a different final window
				finalHold.release(nil)
				finalHold = newRestoreWindowHold(ctx, part)
				if herr := finalHold.admit(requestCtx); herr != nil {
					return herr
				}
				if cap(*data) < size {
					*data = make([]byte, size)
				} else {
					*data = (*data)[:size]
				}
				if _, err := io.ReadFull(rd, *data); err != nil {
					return err
				}
				finalPart = part
				return nil
			}
			// Reuse the established window decoder. Its load reads from this
			// response; alternate-copy recovery is deliberately disabled here.
			readWindow := func(_ context.Context, _ backend.Handle, _ int, _ int64, consume func(io.Reader) error) error {
				return consume(rd)
			}
			err := streamPackPartWithBuffer(requestCtx, readWindow, nil, dec, key, packID, part, data,
				func(h restic.BlobHandle, plaintext []byte, blobErr error) error {
					if blobErr != nil && recoverBlob != nil {
						yield = true
						stop()
						return errStreamPackYield
					}
					handlerErr = handle(h, plaintext, blobErr)
					if handlerErr != nil {
						stop() // suppress transport retry of a rejected callback
						return handlerErr
					}
					committed++
					return nil
				})
			if err == errStreamPackYield {
				yield = true
				stop()
			}
			if err != nil {
				return err
			}
			position = part[len(part)-1].Offset + part[len(part)-1].Length
		}
		return nil
	})
	if handlerErr != nil {
		return handlerErr
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if yield {
		// No live response remains here. The original context and policy allow
		// ordinary admission and alternate loads without a nested-load cycle.
		for remaining := blobs[committed:]; len(remaining) > 0; {
			n := packRangeEnd(remaining, window)
			if err := streamPackPartWithBuffer(ctx, load, recoverBlob, dec, key, packID, remaining[:n], data, handle); err != nil {
				return err
			}
			remaining = remaining[n:]
		}
		return nil
	}
	if err != nil {
		// Transport retry has finished. Recover directly, as the original
		// loader does; do not start a fresh retry budget for the same span.
		if recoverBlob != nil {
			for _, entry := range blobs[committed:] {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				plaintext, blobErr := recoverBlob(ctx, entry.BlobHandle, nil)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				err = handle(entry.BlobHandle, plaintext, blobErr)
				if err != nil {
					break
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
		}
		return errors.Wrap(err, "StreamPack")
	}
	if len(finalPart) > 0 {
		readFinal := func(_ context.Context, _ backend.Handle, _ int, _ int64, consume func(io.Reader) error) error {
			return consume(bytes.NewReader(*data))
		}
		finalCtx := ctx
		if finalHold != nil {
			finalCtx = context.WithValue(ctx, restoreHoldTakenKey{}, finalHold)
		}
		return streamPackPartWithBuffer(finalCtx, readFinal, recoverBlob, dec, key, packID, finalPart, data, handle)
	}
	return nil
}
