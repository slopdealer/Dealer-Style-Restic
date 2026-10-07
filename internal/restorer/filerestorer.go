package restorer

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"golang.org/x/sync/errgroup"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/feature"
	"github.com/restic/restic/internal/restic"
)

const (
	largeFileBlobCount = 25
)

// information about regular file being restored
type fileInfo struct {
	lock       sync.Mutex
	inProgress bool
	sparse     bool
	size       int64
	location   string // file on local filesystem relative to restorer basedir
	blobs      any    // blobs of the file
	state      *fileState
}

type fileBlobInfo struct {
	id     restic.ID // the blob id
	offset int64     // blob offset in the file
}

// information about a data pack required to restore one or more files
type packInfo struct {
	id              restic.ID              // the pack id
	files           map[*fileInfo]struct{} // set of files that use blobs from this pack
	largeNeededBlob bool
	// set by restoreFiles only: files holds exactly the files with an
	// unmatched blob whose first index entry is this pack
	associationsPlanned bool
}

type blobsLoaderFn func(ctx context.Context, packID restic.ID, blobs []restic.BlobHandle, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error) error
type startWarmupFn func(context.Context, restic.IDSet) (restic.WarmupJob, error)

// fileRestorer restores set of files
type fileRestorer struct {
	idx         func(restic.BlobHandle) []restic.PackBlob
	blobsLoader blobsLoaderFn

	startWarmup startWarmupFn

	workerCount int
	// one ordinary worker more than configured may run (ordinary_extra_worker.go)
	additionalWorker bool
	filesWriter      *filesWriter
	zeroChunk        restic.ID
	sparse           bool
	progress         ProgressReporter

	allowRecursiveDelete bool

	dst     string
	files   []*fileInfo
	errorMu sync.Mutex
	Error   func(string, error) error
	Info    func(string)
}

func newFileRestorer(dst string,
	blobsLoader blobsLoaderFn,
	idx func(restic.BlobHandle) []restic.PackBlob,
	connections uint,
	sparse bool,
	allowRecursiveDelete bool,
	startWarmup startWarmupFn,
	progress ProgressReporter,
	zeroChunk restic.ID) *fileRestorer {

	// as packs are streamed the concurrency is limited by IO
	workerCount := int(connections)

	return &fileRestorer{
		idx:                  idx,
		blobsLoader:          blobsLoader,
		startWarmup:          startWarmup,
		filesWriter:          newFilesWriter(workerCount, allowRecursiveDelete),
		zeroChunk:            zeroChunk,
		sparse:               sparse,
		progress:             progressOrNoop(progress),
		allowRecursiveDelete: allowRecursiveDelete,
		workerCount:          workerCount,
		dst:                  dst,
		Error:                restorerAbortOnAllErrors,
		Info:                 func(_ string) {},
	}
}

func (r *fileRestorer) addFile(location string, content restic.IDs, size int64, state *fileState) {
	r.files = append(r.files, &fileInfo{location: location, blobs: content, size: size, state: state})
}

func (r *fileRestorer) targetPath(location string) string {
	return filepath.Join(r.dst, location)
}

func (r *fileRestorer) forEachBlob(blobIDs []restic.ID, fn func(blob restic.PackBlob, idx int, fileOffset int64)) error {
	if len(blobIDs) == 0 {
		return nil
	}

	fileOffset := int64(0)
	for i, blobID := range blobIDs {
		packs := r.idx(restic.BlobHandle{Type: restic.DataBlob, ID: blobID})
		if len(packs) == 0 {
			return errors.Errorf("Unknown blob %s", blobID.String())
		}
		pb := packs[0]
		fn(pb, i, fileOffset)
		fileOffset += int64(pb.PlaintextLength())
	}

	return nil
}

func (r *fileRestorer) restoreFiles(ctx context.Context) error {

	packs := make(map[restic.ID]*packInfo) // all packs
	// Process packs in order of first access. While this cannot guarantee
	// that file chunks are restored sequentially, it offers a good enough
	// approximation to shorten restore times by up to 19% in some test.
	var packOrder restic.IDs

	// create packInfo from fileInfo
	for _, file := range r.files {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		fileBlobs := file.blobs.(restic.IDs)
		largeFile := len(fileBlobs) > largeFileBlobCount
		var packsMap map[restic.ID][]fileBlobInfo
		if largeFile {
			packsMap = make(map[restic.ID][]fileBlobInfo)
			file.blobs = packsMap
		}
		restoredBlobs := false
		err := r.forEachBlob(fileBlobs, func(blob restic.PackBlob, idx int, fileOffset int64) {
			packID := blob.PackID()
			if !file.state.HasMatchingBlob(idx) {
				if largeFile {
					packsMap[packID] = append(packsMap[packID], fileBlobInfo{id: blob.Handle().ID, offset: fileOffset})
				}
				restoredBlobs = true
			} else {
				r.reportBlobProgress(file, uint64(blob.PlaintextLength()))
				// completely ignore blob
				return
			}
			pack, ok := packs[packID]
			if !ok {
				pack = &packInfo{
					id:                  packID,
					files:               make(map[*fileInfo]struct{}),
					associationsPlanned: true,
				}
				packs[packID] = pack
				packOrder = append(packOrder, packID)
			}
			pack.files[file] = struct{}{}
			if !restic.BoundedRestoreBlob(blob.PlaintextLength(), blob.CiphertextLength(), blob.IsCompressed()) {
				pack.largeNeededBlob = true
			}
			if blob.Handle().ID.Equal(r.zeroChunk) {
				file.sparse = r.sparse
			}
		})
		if err != nil {
			// repository index is messed up, can't do anything
			return err
		}

		if len(fileBlobs) == 1 {
			// no need to preallocate files with a single block, thus we can always consider them to be sparse
			// in addition, a short chunk will never match r.zeroChunk which would prevent sparseness for short files
			file.sparse = r.sparse
		}
		if file.state != nil {
			// The restorer currently cannot punch new holes into an existing files.
			// Thus sections that contained data but should be sparse after restoring
			// the snapshot would still contain the old data resulting in a corrupt restore.
			file.sparse = false
		}

		// empty file or one with already up-to-date content. Make sure that the file size is correct
		if !restoredBlobs {
			err := r.truncateFileToSize(file.location, file.size)
			if errFile := r.sanitizeError(file, err); errFile != nil {
				return errFile
			}

			// the progress events were already sent for non-zero size files
			if file.size == 0 {
				r.reportBlobProgress(file, 0)
			}
		}
	}
	// drop no longer necessary file list
	r.files = nil

	if feature.Flag.Enabled(feature.S3Restore) {
		warmupJob, err := r.startWarmup(ctx, restic.NewIDSet(packOrder...))
		if err != nil {
			return err
		}
		if warmupJob.HandleCount() != 0 {
			r.Info(fmt.Sprintf("warming up %d packs from cold storage, this may take a while...", warmupJob.HandleCount()))
			if err := warmupJob.Wait(ctx); err != nil {
				return err
			}
		}
	}

	wg, ctx := errgroup.WithContext(ctx)
	downloadCh := make(chan *packInfo)

	// close all files when finished
	defer r.filesWriter.flush()
	extraCh := make(chan *packInfo)
	// Ordinary allocation permits also cover exceptional duplicate recovery by
	// the bounded worker. Acquisition observes cancellation.
	ordinaryWorkers := r.workerCount
	if r.additionalWorker {
		ordinaryWorkers += extraOrdinaryRestoreWorkers(r.workerCount)
	}
	permits := make(chan struct{}, ordinaryWorkers)
	// Without an additional worker there is no budget and the pack streams are
	// exactly the configured workers' streams as before.
	var holdBudget restic.RestoreHoldBudget
	if ordinaryWorkers > r.workerCount {
		holdBudget = newOrdinaryRestoreHoldBudget(r.workerCount)
	}
	acquire := func(ctx context.Context) (func(), error) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		select {
		case permits <- struct{}{}:
			if ctx.Err() != nil {
				<-permits
				return nil, ctx.Err()
			}
			return func() { <-permits }, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	worker := func(ch <-chan *packInfo, bounded, additional bool) func() error {
		return func() error {
			for pack := range ch {
				workCtx := ctx
				release := func() {}
				if bounded {
					workCtx = restic.WithSmallRestoreLoad(ctx, acquire)
				} else {
					var err error
					if holdBudget != nil {
						// The repository puts these streams on the backend's additional load slot.
						workCtx = context.WithValue(ctx, restic.RestoreHoldKey{}, restic.RestoreHoldUse{Budget: holdBudget, Wait: additional})
					}
					release, err = acquire(ctx)
					if err != nil {
						return err
					}
				}
				err := r.downloadPack(workCtx, pack)
				release()
				if err != nil {
					return err
				}
			}
			return nil
		}
	}
	for i := 0; i < ordinaryWorkers; i++ {
		wg.Go(worker(downloadCh, false, i >= r.workerCount))
	}
	wg.Go(worker(extraCh, true, false))

	// the main restore loop
	wg.Go(func() error {
		defer close(downloadCh)
		defer close(extraCh)
		for _, id := range packOrder {
			pack := packs[id]
			// allow garbage collection of packInfo
			delete(packs, id)
			var boundedCh chan *packInfo
			if !pack.largeNeededBlob {
				boundedCh = extraCh
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case boundedCh <- pack:
				debug.Log("Scheduled bounded download pack %s", pack.id.Str())
			case downloadCh <- pack:
				debug.Log("Scheduled download pack %s", pack.id.Str())
			}
		}
		return nil
	})

	return wg.Wait()
}

func (r *fileRestorer) truncateFileToSize(location string, size int64) error {
	f, err := createFile(r.targetPath(location), size, false, r.allowRecursiveDelete)
	if err != nil {
		return err
	}
	return f.Close()
}

type blobToFileOffsetsMapping map[restic.ID]struct {
	files map[*fileInfo][]int64 // file -> offsets (plural!) of the blob in the file
	blob  restic.BlobHandle
}

func (r *fileRestorer) downloadPack(ctx context.Context, pack *packInfo) error {
	// calculate blob->[]files->[]offsets mappings
	blobs := make(blobToFileOffsetsMapping)
	for file := range pack.files {
		addBlob := func(blob restic.BlobHandle, fileOffset int64) {
			blobInfo, ok := blobs[blob.ID]
			if !ok {
				blobInfo.files = make(map[*fileInfo][]int64)
				blobInfo.blob = blob
				blobs[blob.ID] = blobInfo
			}
			blobInfo.files[file] = appendRestoreOffset(blobInfo.files[file], fileOffset)
		}
		if fileBlobs, ok := file.blobs.(restic.IDs); ok {
			if blob, ok := plannedSingleton(pack, fileBlobs); ok {
				addBlob(blob, 0)
				continue
			}
			err := r.forEachBlob(fileBlobs, func(blob restic.PackBlob, idx int, fileOffset int64) {
				if blob.PackID().Equal(pack.id) && !file.state.HasMatchingBlob(idx) {
					addBlob(blob.Handle(), fileOffset)
				}
			})
			if err != nil {
				// restoreFiles should have caught this error before
				panic(err)
			}
		} else if packsMap, ok := file.blobs.(map[restic.ID][]fileBlobInfo); ok {
			for _, blob := range packsMap[pack.id] {
				idxPacks := r.idx(restic.BlobHandle{Type: restic.DataBlob, ID: blob.id})
				for _, idxPack := range idxPacks {
					if idxPack.PackID().Equal(pack.id) {
						addBlob(idxPack.Handle(), blob.offset)
						break
					}
				}
			}
		}
	}

	// The mapping now owns every association needed by callbacks and error
	// reporting. The planning set has no further consumer in this worker.
	pack.files = nil

	// track already processed blobs for precise error reporting
	processedBlobs := restic.NewBlobSet()
	err := r.downloadBlobs(ctx, pack.id, blobs, processedBlobs)
	return r.reportError(blobs, processedBlobs, err)
}

func (r *fileRestorer) sanitizeError(file *fileInfo, err error) error {
	switch err {
	case nil, context.Canceled, context.DeadlineExceeded:
		// Context errors are permanent.
		return err
	default:
		// Command error callbacks include unsynchronized counters. Data workers
		// must serialize callback ownership, including the bounded worker.
		r.errorMu.Lock()
		defer r.errorMu.Unlock()
		return r.Error(file.location, err)
	}
}

func (r *fileRestorer) reportError(blobs blobToFileOffsetsMapping, processedBlobs restic.BlobSet, err error) error {
	if err == nil {
		return nil
	}

	// only report error for not yet processed blobs
	affectedFiles := make(map[*fileInfo]struct{})
	for _, entry := range blobs {
		if processedBlobs.Has(entry.blob) {
			continue
		}
		for file := range entry.files {
			affectedFiles[file] = struct{}{}
		}
	}

	for file := range affectedFiles {
		if errFile := r.sanitizeError(file, err); errFile != nil {
			return errFile
		}
	}
	return nil
}

func (r *fileRestorer) downloadBlobs(ctx context.Context, packID restic.ID,
	blobs blobToFileOffsetsMapping, processedBlobs restic.BlobSet) error {

	blobList := make([]restic.BlobHandle, 0, len(blobs))
	for _, entry := range blobs {
		blobList = append(blobList, entry.blob)
	}
	return r.blobsLoader(ctx, packID, blobList,
		func(h restic.BlobHandle, blobData []byte, err error) error {
			processedBlobs.Insert(h)
			blob := blobs[h.ID]
			// The loader calls once per blob. Settle writes, diagnostics and
			// progress before retiring its associations, including error returns.
			// reportError already excludes every callback-settled blob.
			defer delete(blobs, h.ID)
			if err != nil {
				for file := range blob.files {
					if errFile := r.sanitizeError(file, err); errFile != nil {
						return errFile
					}
				}
				return nil
			}
			for file, offsets := range blob.files {
				for _, offset := range offsets {
					// avoid long cancellation delays for frequently used blobs
					if ctx.Err() != nil {
						return ctx.Err()
					}

					writeToFile := func() error {
						// this looks overly complicated and needs explanation
						// two competing requirements:
						// - must create the file once and only once
						// - should allow concurrent writes to the file
						// so write the first blob while holding file lock
						// write other blobs after releasing the lock
						createSize := int64(-1)
						file.lock.Lock()
						if file.inProgress {
							file.lock.Unlock()
						} else {
							defer file.lock.Unlock()
							file.inProgress = true
							createSize = file.size
						}
						writeErr := r.filesWriter.writeToFile(r.targetPath(file.location), blobData, offset, createSize, file.sparse)
						r.reportBlobProgress(file, uint64(len(blobData)))
						return writeErr
					}
					err := r.sanitizeError(file, writeToFile())
					if err != nil {
						return err
					}
				}
			}
			return nil
		})
}

func (r *fileRestorer) reportBlobProgress(file *fileInfo, blobSize uint64) {
	action := ActionFileUpdated
	if file.state == nil {
		action = ActionFileRestored
	}
	r.progress.AddProgress(file.location, action, blobSize, uint64(file.size))
}
