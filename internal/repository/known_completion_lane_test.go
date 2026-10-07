package repository

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/restic/restic/internal/restic"
	"golang.org/x/sync/errgroup"
)

func TestKnownCompletionBoundAndCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var wg errgroup.Group
		lane := newKnownCompletionLane(&wg)
		entered, release := make(chan struct{}), make(chan struct{})
		lane.submit(t.Context(), restic.ID{}, func(restic.ID, bool, int, error) {
			close(entered)
			<-release // Hold the worker only to test the admission bound.
		})
		<-entered
		ctx, cancel := context.WithCancel(t.Context())
		var calls atomic.Int64
		cb := func(id restic.ID, known bool, size int, err error) {
			if id != (restic.ID{}) || known || size != 0 || !errors.Is(err, context.Canceled) {
				t.Errorf("cancelled callback = %v %v %d %v", id, known, size, err)
			}
			calls.Add(1)
		}
		for range knownCompletionQueueSize {
			lane.submit(ctx, restic.ID{1}, cb)
		}
		submitted := make(chan struct{})
		go func() { lane.submit(ctx, restic.ID{2}, cb); close(submitted) }()
		synctest.Wait()
		select {
		case <-submitted:
			t.Fatal("full descriptor queue admitted more work")
		default:
		}
		cancel()
		close(release)
		<-submitted
		lane.finish()
		lane.finish() // Shutdown joins safely even when used by a defer as well.
		if err := wg.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatalf("worker error: %v", err)
		}
		if calls.Load() != knownCompletionQueueSize+1 {
			t.Fatalf("settled %d callbacks", calls.Load())
		}
	})
}

func TestKnownCompletionIndependentOfPayloadAdmission(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var wg errgroup.Group
		wg.SetLimit(3) // Completion worker, publisher and one stalled CPU saver.
		lane := newKnownCompletionLane(&wg)
		release := make(chan struct{})
		wg.Go(func() error { <-release; return nil })
		id := restic.ID{17}
		complete := make(chan struct{})
		returned := false
		lane.submit(t.Context(), id, func(got restic.ID, known bool, size int, err error) {
			if got != id || !known || size != 0 || err != nil {
				t.Errorf("known result changed")
			}
			close(complete)
		})
		returned = true
		<-complete
		if !returned {
			t.Fatal("submission failed to return")
		}
		lane.finish()
		close(release)
		if err := wg.Wait(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestKnownCompletionAsynchronous(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var wg errgroup.Group
		lane := newKnownCompletionLane(&wg)
		returned := make(chan struct{})
		complete := make(chan struct{})
		lane.submit(t.Context(), restic.ID{7}, func(restic.ID, bool, int, error) {
			<-returned // Inline dispatch would deadlock this test.
			close(complete)
		})
		close(returned)
		<-complete
		lane.finish()
		if err := wg.Wait(); err != nil {
			t.Fatal(err)
		}
	})
}

func TestKnownCompletionCancelsBeforeShutdown(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wg, ctx := errgroup.WithContext(t.Context())
		lane := newKnownCompletionLane(wg)
		child, cancel := context.WithCancel(ctx)
		cancel()
		lane.submit(child, restic.ID{3}, func(restic.ID, bool, int, error) {})
		<-ctx.Done() // Must settle without requiring fn to return first.
		lane.finish()
		if err := wg.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestKnownCompletionSaturatedCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		wg, ctx := errgroup.WithContext(t.Context())
		wg.SetLimit(3) // Lane + its reserved publisher + a blocked payload saver.
		lane := newKnownCompletionLane(wg)
		wg.Go(func() error { <-ctx.Done(); return ctx.Err() })
		child, cancel := context.WithCancel(ctx)
		cancel()
		lane.submit(child, restic.ID{11}, func(restic.ID, bool, int, error) {})
		<-ctx.Done() // Publication must work even with no vacant group slot.
		lane.finish()
		if err := wg.Wait(); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	})
}

func TestKnownCompletionRepositoryLifecycle(t *testing.T) {
	for _, cancelled := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "cancelled-child"}[cancelled], func(t *testing.T) {
			repo := TestRepository(t)
			data := []byte("indexed completion payload")
			id := restic.Hash(data)
			if err := repo.WithBlobUploader(t.Context(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
				_, _, _, err := u.SaveBlob(ctx, restic.DataBlob, data, id, false)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int64
			err := repo.WithBlobUploader(t.Context(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
				saver := u.(*blobSaverRepo)
				if _, known := saver.LookupStoredBlobSize(restic.BlobHandle{ID: id, Type: restic.DataBlob}); !known {
					t.Fatal("test blob was not indexed")
				}
				jobCtx := ctx
				if cancelled {
					var cancel context.CancelFunc
					jobCtx, cancel = context.WithCancel(ctx)
					cancel()
				}
				for range 40 {
					saver.SaveKnownBlobAsync(jobCtx, id, func(got restic.ID, known bool, size int, err error) {
						if cancelled {
							if !errors.Is(err, context.Canceled) || known || !got.IsNull() {
								t.Error("cancelled result changed")
							}
						} else if got != id || !known || size != 0 || err != nil {
							t.Error("known result changed")
						}
						calls.Add(1)
					})
				}
				if cancelled {
					<-ctx.Done()
				}
				return nil
			})
			if cancelled {
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("uploader error: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if calls.Load() != 40 {
				t.Fatalf("uploader returned with %d settled jobs", calls.Load())
			}
		})
	}
}
