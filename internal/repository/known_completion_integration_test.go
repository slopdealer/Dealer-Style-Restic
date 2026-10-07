package repository

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/restic"
)

// Deliberately stalled callbacks fill the actual uploader's payload admission,
// rather than reproducing the lane in a separate errgroup fixture.
func TestIntegratedKnownCompletionSaturatedUploaderCancellation(t *testing.T) {
	repo := TestRepository(t)
	outer, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	var callbacks atomic.Int64
	go func() {
		result <- repo.WithBlobUploader(outer, func(ctx context.Context, u restic.BlobSaverWithAsync) error {
			entered := make(chan struct{}, runtime.GOMAXPROCS(0))
			for i := range runtime.GOMAXPROCS(0) {
				payload := []byte{byte(i), 17, 29}
				u.SaveBlobAsync(ctx, restic.DataBlob, payload, restic.Hash(payload), false, func(_ restic.ID, _ bool, _ int, err error) {
					if err != nil {
						t.Errorf("payload save: %v", err)
					}
					entered <- struct{}{}
					<-ctx.Done()
					callbacks.Add(1)
				})
			}
			for range runtime.GOMAXPROCS(0) {
				<-entered
			}
			child, stop := context.WithCancel(ctx)
			stop()
			u.(*blobSaverRepo).SaveKnownBlobAsync(child, restic.ID{7}, func(id restic.ID, known bool, size int, err error) {
				if !id.IsNull() || known || size != 0 || !errors.Is(err, context.Canceled) {
					t.Errorf("canceled known callback: %v %v %d %v", id, known, size, err)
				}
				callbacks.Add(1)
			})
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("uploader error: %v", err)
		}
	case <-time.After(5 * time.Second):
		cancel() // Release deliberately stalled callbacks before reporting a failure.
		<-result
		t.Fatal("known completion could not cancel a saturated actual uploader")
	}
	if want := int64(runtime.GOMAXPROCS(0) + 1); callbacks.Load() != want {
		t.Fatalf("settled %d callbacks, want %d", callbacks.Load(), want)
	}
}

func TestIntegratedKnownCompletionAsyncAndSettlement(t *testing.T) {
	repo := TestRepository(t)
	submitted := make(chan struct{})
	entered := make(chan struct{})
	release := make(chan struct{})
	commandReturned := make(chan struct{})
	result := make(chan error, 1)
	var calls atomic.Int64
	var flushes atomic.Int64
	repo.be = &completionFlushObserver{Backend: repo.be, settled: &calls, flushes: &flushes, t: t}
	go func() {
		result <- repo.WithBlobUploader(t.Context(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
			payload := []byte("force pack and index flush after completion")
			if _, _, _, err := u.SaveBlob(ctx, restic.DataBlob, payload, restic.Hash(payload), false); err != nil {
				return err
			}
			u.(*blobSaverRepo).SaveKnownBlobAsync(ctx, restic.ID{9}, func(id restic.ID, known bool, size int, err error) {
				<-submitted // Inline execution cannot reach the close below.
				close(entered)
				<-release
				if id != (restic.ID{9}) || !known || size != 0 || err != nil {
					t.Error("known result changed")
				}
				calls.Add(1)
			})
			close(submitted)
			close(commandReturned)
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("known callback was not asynchronous")
	}
	<-commandReturned
	select {
	case err := <-result:
		t.Fatalf("uploader returned before accepted callback settled: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("uploader failed to join completion")
	}
	if flushes.Load() == 0 {
		t.Fatal("test did not exercise index flush")
	}
	if calls.Load() != 1 {
		t.Fatalf("callback count = %d", calls.Load())
	}
	if repo.mainWg != nil || repo.packerWg != nil || repo.blobSaver != nil {
		t.Fatal("uploader returned before flushing its state")
	}
}

// Index persistence is part of Flush; it must not begin before accepted known
// completions have settled. The wrapper observes the real backend boundary.
type completionFlushObserver struct {
	backend.Backend
	settled, flushes *atomic.Int64
	t                *testing.T
}

func (b *completionFlushObserver) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if h.Type == backend.IndexFile {
		b.flushes.Add(1)
		if b.settled.Load() != 1 {
			b.t.Error("index flush started before known callback settlement")
		}
	}
	return b.Backend.Save(ctx, h, rd)
}
