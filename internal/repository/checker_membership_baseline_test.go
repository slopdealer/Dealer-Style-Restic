package repository

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/hashing"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
	"io"
)

// checkPack reads a pack and checks the integrity of all blobs.
func b11BaselineCheckPack(ctx context.Context, r *Repository, id restic.ID, blobs pack.Blobs, size int64, bufRd *bufio.Reader, dec *zstd.Decoder) error {
	err := b11BaselineCheckPackInner(ctx, r, id, blobs, size, bufRd, dec)
	if err != nil {
		if r.cache != nil {
			// ignore error as there's not much we can do here
			_ = r.cache.Forget(backend.Handle{Type: backend.PackFile, Name: id.String()})
		}

		// retry pack verification to detect transient errors
		err2 := b11BaselineCheckPackInner(ctx, r, id, blobs, size, bufRd, dec)
		if err2 != nil {
			err = err2
		} else {
			err = fmt.Errorf("check successful on second attempt, original error %w", err)
		}
	}
	return err
}

func b11BaselineCheckPackInner(ctx context.Context, r *Repository, id restic.ID, blobs pack.Blobs, size int64, bufRd *bufio.Reader, dec *zstd.Decoder) error {

	type partialReadError struct {
		error
	}

	debug.Log("checking pack %v", id.String())

	if len(blobs) == 0 {
		return &ErrPackData{PackID: id, errs: []error{errors.New("pack is empty or not indexed")}}
	}

	// sanity check blobs in index
	blobs.Sort()
	idxHdrSize := pack.CalculateHeaderSize(blobs)
	lastBlobEnd := 0
	nonContinuousPack := false
	for _, blob := range blobs {
		if lastBlobEnd != int(blob.Offset) {
			nonContinuousPack = true
		}
		lastBlobEnd = int(blob.Offset + blob.Length)
	}
	// size was calculated by masterindex.PackSize, thus there's no need to recalculate it here

	var errs []error
	if nonContinuousPack {
		debug.Log("Index for pack contains gaps / overlaps, blobs: %v", blobs)
		errs = append(errs, errors.New("index for pack contains gaps / overlapping blobs"))
	}

	// calculate hash on-the-fly while reading the pack and capture pack header
	var hash restic.ID
	var hdrBuf []byte
	// must use a separate slice from `errs` here as we're only interested in the last retry
	var blobErrors []error
	h := backend.Handle{Type: backend.PackFile, Name: id.String()}
	err := r.be.Load(ctx, h, int(size), 0, func(rd io.Reader) error {
		hrd := hashing.NewReader(rd, sha256.New())
		bufRd.Reset(hrd)
		// reset blob errors for each retry
		blobErrors = nil

		it := newPackBlobIterator(id, newBufReader(bufRd), 0, blobs, r.Key(), dec)
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			val, err := it.NextHash()
			if err == errPackEOF {
				break
			} else if err != nil {
				return &partialReadError{err}
			}
			debug.Log("  check blob %v: %v", val.Handle.ID, val.Handle)
			if val.Err != nil {
				debug.Log("  error verifying blob %v: %v", val.Handle.ID, val.Err)
				blobErrors = append(blobErrors, errors.Errorf("blob %v: %v", val.Handle.ID, val.Err))
			}
		}

		// skip enough bytes until we reach the possible header start
		curPos := lastBlobEnd
		minHdrStart := int(size) - pack.MaxHeaderSize
		if minHdrStart > curPos {
			_, err := bufRd.Discard(minHdrStart - curPos)
			if err != nil {
				return &partialReadError{err}
			}
			curPos += minHdrStart - curPos
		}

		// read remainder, which should be the pack header
		var err error
		hdrBuf = make([]byte, int(size-int64(curPos)))
		_, err = io.ReadFull(bufRd, hdrBuf)
		if err != nil {
			return &partialReadError{err}
		}

		hash = restic.IDFromHash(hrd.Sum(nil))
		return nil
	})
	errs = append(errs, blobErrors...)
	if err != nil {
		var e *partialReadError
		isPartialReadError := errors.As(err, &e)
		// failed to load the pack file, return as further checks cannot succeed anyways
		debug.Log("  error streaming pack (partial %v): %v", isPartialReadError, err)
		if isPartialReadError {
			return &ErrPackData{PackID: id, errs: append(errs, fmt.Errorf("partial download error: %w", err))}
		}

		// The check command suggests to repair files for which a `ErrPackData` is returned. However, this file
		// completely failed to download such that there's no point in repairing anything.
		return fmt.Errorf("download error: %w", err)
	}
	if !hash.Equal(id) {
		debug.Log("pack ID does not match, want %v, got %v", id, hash)
		return &ErrPackData{PackID: id, errs: append(errs, errors.Errorf("unexpected pack id %v", hash))}
	}

	blobs, hdrSize, err := pack.List(r.Key(), bytes.NewReader(hdrBuf), int64(len(hdrBuf)))
	if err != nil {
		return &ErrPackData{PackID: id, errs: append(errs, err)}
	}

	if uint32(idxHdrSize) != hdrSize {
		debug.Log("Pack header size does not match, want %v, got %v", idxHdrSize, hdrSize)
		errs = append(errs, errors.Errorf("pack header size does not match, want %v, got %v", idxHdrSize, hdrSize))
	}

	for _, blob := range blobs {
		// Check if blob is contained in index and position is correct
		idxHas := false
		for _, pb := range r.idx.Lookup(blob.BlobHandle) {
			if pb.PackID().Equal(id) && pb.Blob == blob {
				idxHas = true
				break
			}
		}
		if !idxHas {
			errs = append(errs, errors.Errorf("blob %v is not contained in index or position is incorrect", blob.ID))
			continue
		}
	}

	if len(errs) > 0 {
		return &ErrPackData{PackID: id, errs: errs}
	}

	return nil
}
