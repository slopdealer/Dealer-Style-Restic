package repository

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"testing/synctest"

	"github.com/restic/restic/internal/restic"
)

// Exercise the real uploader registration, group capacity, worker publication,
// fn teardown, and inner pack-uploader join together. A helper-only test cannot
// establish that the production reservation leaves a publisher slot available.
func TestB23IntegratedUploaderSaturatedChildCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		repo := TestRepository(t)
		var settled atomic.Int64
		err := repo.WithBlobUploader(t.Context(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
			// All four fixed jobs have registered: pack waiter, command, completion
			// worker, publisher. Fill every remaining production slot with work that
			// cannot return until the publisher cancels the uploader context.
			for range runtime.GOMAXPROCS(0) {
				repo.mainWg.Go(func() error { <-ctx.Done(); return ctx.Err() })
			}
			child, cancel := context.WithCancel(ctx)
			cancel()
			saver := u.(*blobSaverRepo)
			for range 40 {
				saver.SaveKnownBlobAsync(child, restic.ID{3}, func(id restic.ID, known bool, size int, err error) {
					if !id.IsNull() || known || size != 0 || !errors.Is(err, context.Canceled) {
						t.Errorf("cancelled completion: %v %v %d %v", id, known, size, err)
					}
					settled.Add(1)
				})
			}
			<-ctx.Done() // No external cancellation or payload-release escape.
			return nil
		})
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("uploader error: %v", err)
		}
		if settled.Load() != 40 {
			t.Fatalf("returned with %d of 40 callbacks settled", settled.Load())
		}
	})
}
