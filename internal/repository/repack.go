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

	// Each worker reads one pack beside the pending saves of the pack before
	// it (repack_completion_overlap.go) and returns only after every save it
	// admitted has completed.
	worker := func() error {
		return runRepackCompletionOverlap(downloadQueue, func(t index.PackBlobs) *repackPackCompletion {
			return startRepackPackCompletion(ctx, wgCtx, repo, uploader, keepBlobs, &keepMutex, saveBudget, t)
		}, p)
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
