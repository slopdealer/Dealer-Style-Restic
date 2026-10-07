package repository

// WITNESS (readside review, cross-area: internal/repository/stream_pack_coalesced.go,
// reached by restore, repack and copy through loadBlobsFromPack,
// repository.go:1513-1570).
//
// Stock reads a pack range into memory inside the backend Load callback and
// decodes it AFTER Load has returned. The candidate's multi-window path
// (streamPackResponse) decodes every window but the last INSIDE the Load
// callback and hands the blobs to the caller from there. Two consequences:
//
//  1. TestWitnessCoalescedStreamStructuralErrorIsRetried: an error that is not
//     a transport error (here: an index entry whose length is not larger than
//     the nonce, "invalid blob length") is now returned from inside the Load
//     callback. The retry backend treats it as a failed download: it downloads
//     the same range again and again until its time budget is used up (15
//     minutes in the real binary, global.go:626) and then opens the circuit
//     breaker for that pack file for an hour (backend_retry.go:205-226), so
//     every later read of that pack fails too. Stock makes one request and
//     reports the error at once. Needs a damaged index entry to trigger.
//
//  2. TestWitnessCoalescedStreamFailOpen: if a Load implementation returns
//     nil without having called the consumer, the multi-window path returns
//     nil and delivers NO blob and NO error (stream_pack_coalesced.go:152-203).
//     The stock path (and the candidate's own single-window path) fails closed
//     in that case: the buffer does not authenticate, every blob is reported
//     as an error. No backend in the tree does this today (all go through
//     util.DefaultLoad); this is a lost safety margin, not a live bug.
//
// The candidate keeps the stock algorithm as streamPack (frozen test API,
// repository.go:1608), so both arms run in one binary.
//
// OS: any. Run in the candidate tree:
//   go test -run 'TestWitnessCoalescedStream' -count=1 -v ./internal/repository
// Expected on the candidate: both tests FAIL (they report the divergence).

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
)

type witnessCountingBackend struct {
	backend.Backend
	loads atomic.Int64
}

func (b *witnessCountingBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	b.loads.Add(1)
	return b.Backend.Load(ctx, h, length, offset, fn)
}

func TestWitnessCoalescedStreamStructuralErrorIsRetried(t *testing.T) {
	retry.TestFastRetries(t)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	key := testKey(t)

	// blobs of 1.2 MiB, 0.6 MiB, 1.2 MiB, 1.2 MiB: the range needs more than one
	// 2 MiB window, and the gaps stay below the 1 MiB "unused range" limit
	good, packfile := buildPackfileWithoutHeader([]int{1200000, 600000, 1200000, 1200000}, &key, false)
	// a damaged index entry in place of blob 1: it claims 10 bytes (not more
	// than the 16 byte nonce). It lands in the first, non-final window.
	bad := pack.Blob{
		BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("damaged index entry"))},
		Offset:     good[1].Offset,
		Length:     10,
	}
	blobs := func() pack.Blobs { return pack.Blobs{good[0], bad, good[2], good[3]} }

	packID := restic.Hash(packfile)
	h := backend.Handle{Type: backend.PackFile, Name: packID.String()}

	run := func(name string, stream func(ctx context.Context, load backendLoadFn, handle func(restic.BlobHandle, []byte, error) error) error) (loads int64, streamErr error, breaker error, took time.Duration) {
		inner := &witnessCountingBackend{Backend: mem.New()}
		if err := inner.Save(context.TODO(), h, backend.NewByteReader(packfile, inner.Hasher())); err != nil {
			t.Fatal(err)
		}
		be := retry.New(inner, 15*time.Minute, nil, nil)
		delivered := 0
		start := time.Now()
		streamErr = stream(context.TODO(), be.Load, func(_ restic.BlobHandle, _ []byte, _ error) error {
			delivered++
			return nil
		})
		took = time.Since(start)
		loads = inner.loads.Load()
		// is the pack still readable afterwards?
		breaker = be.Load(context.TODO(), h, 16, 0, func(rd io.Reader) error {
			_, err := io.Copy(io.Discard, rd)
			return err
		})
		t.Logf("%-9s backend loads=%d, blobs delivered=%d, stream error=%v, took %v, next read of the pack: %v",
			name, loads, delivered, streamErr, took.Round(time.Millisecond), breaker)
		return loads, streamErr, breaker, took
	}

	stockLoads, stockErr, stockBreaker, _ := run("stock", func(ctx context.Context, load backendLoadFn, handle func(restic.BlobHandle, []byte, error) error) error {
		return streamPack(ctx, load, nil, dec, &key, packID, blobs(), handle)
	})
	candLoads, candErr, candBreaker, _ := run("candidate", func(ctx context.Context, load backendLoadFn, handle func(restic.BlobHandle, []byte, error) error) error {
		return streamPackWithPolicy(ctx, load, nil, dec, &key, packID, blobs(), handle, restoreStreamWindow, true)
	})

	if stockErr == nil || candErr == nil {
		t.Fatalf("both arms must report the damaged entry: stock=%v candidate=%v", stockErr, candErr)
	}
	if candLoads != stockLoads {
		t.Errorf("DIVERGENCE: stock made %d backend request(s) for the range, the candidate made %d (a non-transport error is retried as a failed download)", stockLoads, candLoads)
	}
	if (stockBreaker == nil) != (candBreaker == nil) {
		t.Errorf("DIVERGENCE: after the error the pack is readable in stock (%v) but not in the candidate: %v", stockBreaker, candBreaker)
	}
	if candBreaker != nil && !strings.Contains(candBreaker.Error(), "circuit breaker") {
		t.Logf("note: unexpected follow-up error %v", candBreaker)
	}
}

func TestWitnessCoalescedStreamFailOpen(t *testing.T) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	key := testKey(t)

	blobList, _ := buildPackfileWithoutHeader([]int{1200000, 1200000, 1200000, 1200000}, &key, false)
	packID := restic.Hash([]byte("witness pack"))

	// a Load that reports success without ever calling the consumer
	silentLoad := func(_ context.Context, _ backend.Handle, _ int, _ int64, _ func(rd io.Reader) error) error {
		return nil
	}

	run := func(name string, stream func(handle func(restic.BlobHandle, []byte, error) error) error) (calls, withErr int, streamErr error) {
		streamErr = stream(func(_ restic.BlobHandle, buf []byte, err error) error {
			calls++
			if err != nil {
				withErr++
			}
			return nil
		})
		t.Logf("%-9s callbacks=%d (with error: %d), stream error=%v", name, calls, withErr, streamErr)
		return calls, withErr, streamErr
	}

	clone := func() pack.Blobs { return append(pack.Blobs(nil), blobList...) }

	stockCalls, stockWithErr, stockErr := run("stock", func(handle func(restic.BlobHandle, []byte, error) error) error {
		return streamPack(context.TODO(), silentLoad, nil, dec, &key, packID, clone(), handle)
	})
	candCalls, candWithErr, candErr := run("candidate", func(handle func(restic.BlobHandle, []byte, error) error) error {
		return streamPackWithPolicy(context.TODO(), silentLoad, nil, dec, &key, packID, clone(), handle, restoreStreamWindow, true)
	})

	stockReported := stockErr != nil || stockWithErr == len(blobList)
	candReported := candErr != nil || candWithErr == len(blobList)
	if !stockReported {
		t.Fatalf("stock path was expected to fail closed: calls=%d withErr=%d err=%v", stockCalls, stockWithErr, stockErr)
	}
	if !candReported {
		t.Errorf("FAIL-OPEN: the candidate returned err=%v after %d callbacks (%d with error) for %d requested blobs; stock reported every blob as failed",
			candErr, candCalls, candWithErr, len(blobList))
	}
}
