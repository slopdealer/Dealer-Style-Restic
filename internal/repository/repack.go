package repository

import (
	"context"
	"sync"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/feature"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"

	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

const maxAsyncRepackCopyBytes = 8 << 20

type repackBlobSet interface {
	Has(bh restic.BlobHandle) bool
	Delete(bh restic.BlobHandle)
	Len() int
}

type LogFunc func(msg string, args ...any)

// CopyBlobs takes a list of packs together with a list of blobs contained in
// these packs. Each pack is loaded and the blobs listed in keepBlobs is saved
// into a new pack. Returned is the list of obsolete packs which can then
// be removed.
//
// The map keepBlobs is modified by CopyBlobs, it is used to keep track of which
// blobs have been processed.
func CopyBlobs(
	ctx context.Context,
	repo *Repository,
	dstRepo restic.Repository,
	dstUploader restic.BlobSaverWithAsync,
	packs restic.IDSet,
	keepBlobs repackBlobSet,
	p restic.Counter,
	logf LogFunc,
) error {
	debug.Log("repacking %d packs while keeping %d blobs", len(packs), keepBlobs.Len())

	if logf == nil {
		logf = func(_ string, _ ...any) {}
	}
	p.SetMax(uint64(len(packs)))
	defer p.Done()

	if repo == dstRepo && dstRepo.Connections() < 2 {
		return errors.New("repack step requires a backend connection limit of at least two")
	}

	return repack(ctx, repo, dstRepo, dstUploader, packs, keepBlobs, p, logf)
}

func repack(
	ctx context.Context,
	repo *Repository,
	dstRepo restic.Repository,
	uploader restic.BlobSaverWithAsync,
	packs restic.IDSet,
	keepBlobs repackBlobSet,
	p restic.Counter,
	logf LogFunc,
) error {

	wg, wgCtx := errgroup.WithContext(ctx)

	if feature.Flag.Enabled(feature.S3Restore) {
		job, err := repo.StartWarmup(ctx, packs)
		if err != nil {
			return err
		}
		if job.HandleCount() != 0 {
			logf("warming up %d packs from cold storage, this may take a while...", job.HandleCount())
			if err := job.Wait(ctx); err != nil {
				return err
			}
		}
	}

	var keepMutex sync.Mutex
	downloadQueue := make(chan index.PackBlobs)
	wg.Go(func() error {
		defer close(downloadQueue)
		for pbs := range repo.idx.ListPacksForRepack(wgCtx, packs) {
			var packBlobs pack.Blobs
			keepMutex.Lock()
			// filter out unnecessary blobs
			for _, entry := range pbs.Blobs {
				h := restic.BlobHandle{ID: entry.ID, Type: entry.Type}
				if keepBlobs.Has(h) {
					packBlobs = append(packBlobs, entry)
				}
			}
			keepMutex.Unlock()

			select {
			case downloadQueue <- index.PackBlobs{PackID: pbs.PackID, Blobs: packBlobs}:
			case <-wgCtx.Done():
				return wgCtx.Err()
			}
		}
		return wgCtx.Err()
	})
	saveBudget := semaphore.NewWeighted(maxAsyncRepackCopyBytes)

	worker := func() error {
		for t := range downloadQueue {
			type saveResult struct {
				blob restic.BlobHandle
				err  error
				done bool
			}
			var pending sync.WaitGroup
			var resultMu sync.Mutex
			var results []saveResult

			// the handler saves blobs: it must not run inside the backend Load callback
			err := repo.loadBlobsFromPackReleased(wgCtx, t.PackID, t.Blobs, func(blob restic.BlobHandle, buf []byte, err error) error {
				if err != nil {
					// a required blob couldn't be retrieved
					return err
				}

				keepMutex.Lock()
				// recheck whether some other worker was faster
				shouldKeep := keepBlobs.Has(blob)
				if shouldKeep {
					keepBlobs.Delete(blob)
				}
				keepMutex.Unlock()

				if !shouldKeep {
					return nil
				}

				resultMu.Lock()
				resultID := len(results)
				results = append(results, saveResult{blob: blob})
				resultMu.Unlock()

				// Keep unusually large blobs on the synchronous path so the byte
				// budget remains a strict bound on copied plaintext.
				if int64(len(buf)) > maxAsyncRepackCopyBytes {
					_, _, _, saveErr := uploader.SaveBlob(ctx, blob.Type, buf, blob.ID, true)
					resultMu.Lock()
					results[resultID].err = saveErr
					results[resultID].done = true
					resultMu.Unlock()
					return saveErr
				}

				weight := int64(len(buf))
				if err := saveBudget.Acquire(wgCtx, weight); err != nil {
					resultMu.Lock()
					results[resultID].err = err
					results[resultID].done = true
					resultMu.Unlock()
					return err
				}
				owned := append([]byte(nil), buf...)
				pending.Add(1)
				// The uploader context owns save lifetime. wgCtx is only for
				// stopping pack reads and can be canceled as workers finish.
				uploader.SaveBlobAsync(ctx, blob.Type, owned, blob.ID, true, func(_ restic.ID, _ bool, _ int, saveErr error) {
					resultMu.Lock()
					results[resultID].err = saveErr
					results[resultID].done = true
					resultMu.Unlock()
					saveBudget.Release(weight)
					pending.Done()
				})
				return nil
			})
			pending.Wait()
			resultMu.Lock()
			completed := append([]saveResult(nil), results...)
			resultMu.Unlock()
			var saveErr error
			for _, result := range completed {
				if result.err != nil && saveErr == nil {
					saveErr = result.err
				}
				if result.done && result.err == nil {
					debug.Log("  saved blob %v", result.blob.ID)
				}
			}
			if err != nil && err != context.Canceled && err != context.DeadlineExceeded {
				return err
			}
			if saveErr != nil {
				return saveErr
			}
			if err != nil {
				return err
			}
			p.Add(1)
		}
		return nil
	}

	// as packs are streamed the concurrency is limited by IO
	// reduce by one to ensure that uploading is always possible
	repackWorkerCount := int(repo.Connections() - 1)
	if repo != dstRepo {
		// no need to share the upload and download connections for different repositories
		repackWorkerCount = int(repo.Connections())
	}
	for i := 0; i < repackWorkerCount; i++ {
		wg.Go(worker)
	}

	return wg.Wait()
}
