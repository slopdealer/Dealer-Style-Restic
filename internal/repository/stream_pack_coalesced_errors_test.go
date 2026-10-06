package repository

import (
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/test"
)

type loadCountingBackend struct {
	backend.Backend
	loads atomic.Int64
}

func (b *loadCountingBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	b.loads.Add(1)
	return b.Backend.Load(ctx, h, length, offset, fn)
}

// An error of the decoder that is not a transfer error (here an index entry
// whose length is not larger than the nonce) inside a window that is decoded
// within the Load callback must be reported once. It must not be retried as a
// failed download, and it must not open the retry backend's circuit breaker
// for the pack.
func TestStreamPackCoalescedDecodeErrorIsNotRetried(t *testing.T) {
	retry.TestFastRetries(t)
	dec, err := zstd.NewReader(nil)
	test.OK(t, err)
	defer dec.Close()
	key := testKey(t)

	// 1.2 MiB, 0.6 MiB, 1.2 MiB, 1.2 MiB: more than one 2 MiB window, no gap
	good, packfile := buildPackfileWithoutHeader([]int{1200000, 600000, 1200000, 1200000}, &key, false)
	bad := pack.Blob{
		BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("damaged index entry"))},
		Offset:     good[1].Offset,
		Length:     10,
	}
	packID := restic.Hash(packfile)
	h := backend.Handle{Type: backend.PackFile, Name: packID.String()}

	for _, tc := range []struct {
		name   string
		stream func(ctx context.Context, load backendLoadFn, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error
	}{
		{"read then decode", func(ctx context.Context, load backendLoadFn, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error {
			return streamPack(ctx, load, nil, dec, &key, packID, blobs, handle)
		}},
		{"decode inside Load", func(ctx context.Context, load backendLoadFn, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error {
			return streamPackWithPolicy(ctx, load, nil, dec, &key, packID, blobs, handle, restoreStreamWindow, true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			inner := &loadCountingBackend{Backend: mem.New()}
			test.OK(t, inner.Save(context.TODO(), h, backend.NewByteReader(packfile, inner.Hasher())))
			be := retry.New(inner, 15*time.Minute, nil, nil)

			delivered := 0
			err := tc.stream(context.TODO(), be.Load, pack.Blobs{good[0], bad, good[2], good[3]}, func(_ restic.BlobHandle, _ []byte, err error) error {
				test.OK(t, err)
				delivered++
				return nil
			})
			if err == nil || !strings.Contains(err.Error(), "invalid blob length") {
				t.Fatalf("expected the invalid blob length error, got %v", err)
			}
			test.Equals(t, 1, delivered)
			test.Equals(t, int64(1), inner.loads.Load())
			// the pack must still be readable through the same retry backend
			test.OK(t, be.Load(context.TODO(), h, 16, 0, func(rd io.Reader) error {
				_, err := io.Copy(io.Discard, rd)
				return err
			}))
		})
	}
}

// A Load that returns nil without calling the consumer has delivered nothing.
// Every requested blob must then reach the handler with an error (or come
// from another copy), exactly as when the range is decoded after Load.
func TestStreamPackCoalescedFailsClosedWithoutConsumer(t *testing.T) {
	dec, err := zstd.NewReader(nil)
	test.OK(t, err)
	defer dec.Close()
	key := testKey(t)

	blobList, _ := buildPackfileWithoutHeader([]int{1200000, 1200000, 1200000, 1200000}, &key, false)
	packID := restic.Hash([]byte("pack"))
	silentLoad := func(_ context.Context, _ backend.Handle, _ int, _ int64, _ func(rd io.Reader) error) error {
		return nil
	}
	recovered := []byte("another copy")

	for _, tc := range []struct {
		name   string
		stream func(recoverBlob loadBlobFn, handle func(restic.BlobHandle, []byte, error) error) error
	}{
		{"read then decode", func(recoverBlob loadBlobFn, handle func(restic.BlobHandle, []byte, error) error) error {
			return streamPack(context.TODO(), silentLoad, recoverBlob, dec, &key, packID, append(pack.Blobs(nil), blobList...), handle)
		}},
		{"decode inside Load", func(recoverBlob loadBlobFn, handle func(restic.BlobHandle, []byte, error) error) error {
			return streamPackWithPolicy(context.TODO(), silentLoad, recoverBlob, dec, &key, packID, append(pack.Blobs(nil), blobList...), handle, restoreStreamWindow, true)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var failed []restic.BlobHandle
			test.OK(t, tc.stream(nil, func(h restic.BlobHandle, _ []byte, err error) error {
				if err == nil {
					t.Errorf("blob %v was delivered although nothing was read", h)
				} else if !strings.Contains(err.Error(), "decrypting blob") {
					t.Errorf("unexpected error for blob %v: %v", h, err)
				}
				failed = append(failed, h)
				return nil
			}))
			test.Equals(t, len(blobList), len(failed))
			for i, h := range failed {
				test.Equals(t, blobList[i].BlobHandle, h)
			}

			// with another copy available every blob is taken from there
			calls := 0
			test.OK(t, tc.stream(func(_ context.Context, _ restic.BlobHandle, _ []byte) ([]byte, error) {
				return recovered, nil
			}, func(h restic.BlobHandle, buf []byte, err error) error {
				test.OK(t, err)
				test.Equals(t, string(recovered), string(buf))
				calls++
				return nil
			}))
			test.Equals(t, len(blobList), calls)
		})
	}
}
