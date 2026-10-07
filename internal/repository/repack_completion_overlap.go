package repository

import (
	"context"
	"sync"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/restic"

	"golang.org/x/sync/semaphore"
)

// A repack worker reads a pack and hands the blobs it keeps to the uploader's
// asynchronous save. It used to wait for every one of those saves before it
// read the next pack. It now reads ONE further pack while the saves of the
// pack before it finish, and never more than one:
//
//   - a pack is settled (all its saves joined, its result examined) before
//     the pack after its successor is read;
//   - the function returns, with or without an error, only after every save
//     it admitted has called back, so a caller that goes on to save the index
//     and delete packs after a nil return has every blob stored;
//   - the first error wins in the order read error, save error, cancellation,
//     for each pack and in pack order, as before;
//   - the plaintext copied for pending saves stays bounded by the shared
//     maxAsyncRepackCopyBytes budget, and the worker count is unchanged.
//
// maxRepackOverlapDescriptors bounds the result records of the two packs that
// can be open at once: a pack is not read beside its predecessor when the two
// together hold more blobs than that.
const maxRepackOverlapDescriptors = 512

// repackPackCompletion is what is left of a pack after it was read.
type repackPackCompletion struct {
	// readErr is the result of reading the pack (nil, a handler error, or a
	// read error).
	readErr error
	// descriptors is the number of blobs requested from the pack.
	descriptors int
	// finish waits for the pack's saves and returns the pack's result.
	finish func() error
	// drain waits for the pack's saves and discards their results.
	drain func()
}

// runRepackCompletionOverlap is the loop of one repack worker.
func runRepackCompletionOverlap(queue <-chan index.PackBlobs, start func(index.PackBlobs) *repackPackCompletion, progress restic.Counter) error {
	var previous *repackPackCompletion
	settle := func() error {
		if previous == nil {
			return nil
		}
		err := previous.finish()
		previous = nil
		if err == nil {
			progress.Add(1)
		}
		return err
	}
	for task := range queue {
		// Settle first when the two packs together would exceed the bound. A
		// single pack above the bound is settled right after it was read.
		if previous != nil && previous.descriptors+len(task.Blobs) > maxRepackOverlapDescriptors {
			if err := settle(); err != nil {
				return err
			}
		}
		current := start(task)
		task.Blobs = nil
		if err := settle(); err != nil {
			// the predecessor failed: join what the successor admitted
			current.drain()
			return err
		}
		previous = current
		if current.readErr != nil || current.descriptors > maxRepackOverlapDescriptors {
			if err := settle(); err != nil {
				return err
			}
		}
	}
	return settle()
}

// startRepackPackCompletion reads one pack and admits the saves of the blobs
// to keep. It returns when the pack has been read; the saves may still run.
// The pack is read on wgCtx and the saves are submitted on saveCtx; repack
// passes the context of its workers for both.
//
// The handler saves blobs, so the pack is read through
// loadBlobsFromPackReleased: the handler never runs inside the backend's
// Load callback, and a worker that waits for the save budget holds no backend
// connection.
func startRepackPackCompletion(saveCtx, wgCtx context.Context, repo *Repository, uploader restic.BlobSaverWithAsync, keepBlobs repackBlobSet, keepMutex *sync.Mutex, saveBudget *semaphore.Weighted, t index.PackBlobs) *repackPackCompletion {
	type saveResult struct {
		blob restic.BlobHandle
		err  error
		done bool
	}
	var pending sync.WaitGroup
	var resultMu sync.Mutex
	var results []saveResult

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
			_, _, _, saveErr := uploader.SaveBlob(saveCtx, blob.Type, buf, blob.ID, true)
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
		// The save runs on the context of the repack's workers, as upstream's
		// does: an error of another worker gives up a save that waits for
		// an uploader. No worker returns while a save it admitted is pending.
		uploader.SaveBlobAsync(saveCtx, blob.Type, owned, blob.ID, true, func(_ restic.ID, _ bool, _ int, saveErr error) {
			resultMu.Lock()
			results[resultID].err = saveErr
			results[resultID].done = true
			resultMu.Unlock()
			saveBudget.Release(weight)
			pending.Done()
		})
		return nil
	})
	descriptors := len(t.Blobs)

	finish := func() error {
		pending.Wait()
		// Every callback has stored its result before Done, and the pack is
		// not read any more: results is no longer written.
		var saveErr error
		for _, result := range results {
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
		return nil
	}
	return &repackPackCompletion{readErr: err, descriptors: descriptors, finish: finish, drain: pending.Wait}
}
