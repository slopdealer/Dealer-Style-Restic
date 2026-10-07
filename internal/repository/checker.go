package repository

import (
	"bufio"
	"context"
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/feature"
	"github.com/restic/restic/internal/repository/hashing"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
	"golang.org/x/sync/errgroup"
)

const (
	maxStreamBufferSize        = 1 * 1024 * 1024
	smallCheckStreamBufferSize = 256 * 1024
	smallCheckBlobLimit        = 1 * 1024 * 1024
)

// ErrIncompletePackEntry is returned when indexes contain different data for a pack.
type ErrIncompletePackEntry struct {
	PackID  restic.ID
	Indexes restic.IDSet
}

func (e *ErrIncompletePackEntry) Error() string {
	return fmt.Sprintf("pack %v has different data in indexes: %v", e.PackID, e.Indexes)
}

// ErrDuplicatePacks is returned when a pack is found in more than one index.
type ErrDuplicatePacks struct {
	PackID  restic.ID
	Indexes restic.IDSet
}

func (e *ErrDuplicatePacks) Error() string {
	return fmt.Sprintf("pack %v contained in several indexes: %v", e.PackID, e.Indexes)
}

// ErrMixedPack is returned when a pack is found that contains both tree and data blobs.
type ErrMixedPack struct {
	PackID restic.ID
}

func (e *ErrMixedPack) Error() string {
	return fmt.Sprintf("pack %v contains a mix of tree and data blobs", e.PackID.Str())
}

// ErrPackMetadata describes an error with a specific pack. It is used for missing, truncated or orphaned packs.
// Errors of the actual pack data are returned as ErrPackData.
type ErrPackMetadata struct {
	ID        restic.ID
	Orphaned  bool
	Truncated bool
	Missing   bool
	Err       error
}

func (e *ErrPackMetadata) Error() string {
	return "pack " + e.ID.String() + ": " + e.Err.Error()
}

// ErrPackData is returned if errors are discovered while verifying a packfile
type ErrPackData struct {
	PackID restic.ID
	errs   []error
}

func (e *ErrPackData) Error() string {
	return fmt.Sprintf("pack %v contains %v errors: %v", e.PackID, len(e.errs), e.errs)
}

// Checker handles index-related operations for repository checking.
type Checker struct {
	repo *Repository
}

// newChecker creates a new Checker.
func newChecker(repo *Repository) *Checker {
	return &Checker{
		repo: repo,
	}
}
func computePackTypes(ctx context.Context, idx restic.ListBlobser) (map[restic.ID]restic.BlobType, error) {
	packs := make(map[restic.ID]restic.BlobType)
	err := idx.ListBlobs(ctx, func(pb restic.PackBlob) {
		packID := pb.PackID()
		h := pb.Handle()
		tpe, exists := packs[packID]
		if exists {
			if h.Type != tpe {
				tpe = restic.InvalidBlob
			}
		} else {
			tpe = h.Type
		}
		packs[packID] = tpe
	})
	return packs, err
}

// LoadIndex loads all index files.
func (c *Checker) LoadIndex(ctx context.Context, p restic.TerminalCounterFactory) (hints []error, errs []error) {
	debug.Log("Start")
	packToIndex := make(map[restic.ID]restic.IDSet)
	packTypes := freshCheckPackTypes(c.repo.idx)
	// in restic < 0.10.0, the blobs of a pack could be split over multiple indexes.
	// by now this is considered as repository damage.
	packToPackBlobHash := make(map[restic.ID]restic.IDSet)

	// Use the repository's internal loadIndexWithCallback to handle per-index errors
	err := c.repo.loadIndexWithCallback(ctx, p, func(id restic.ID, idx *index.Index, err error) error {
		debug.Log("process index %v, err %v", id, err)
		err = errors.Wrapf(err, "error loading index %v", id)

		if err != nil {
			errs = append(errs, err)
			return nil
		}

		debug.Log("process blobs")
		cnt := 0
		// EachByPack already builds the pack relation for the integrity hash.
		// Record index membership once per group instead of once per blob.
		for pbs := range idx.EachByPackForCheck(ctx, restic.NewIDSet()) {
			cnt += len(pbs.Blobs)
			if packTypes != nil {
				collectCheckPackType(packTypes, pbs)
			}
			if _, ok := packToIndex[pbs.PackID]; !ok {
				packToIndex[pbs.PackID] = restic.NewIDSet()
			}
			packToIndex[pbs.PackID].Insert(id)
			// The group owns this scratch and no longer needs its original order.
			packBlobHash := index.PackBlobsHashOwned(pbs)
			if _, ok := packToPackBlobHash[pbs.PackID]; !ok {
				packToPackBlobHash[pbs.PackID] = restic.NewIDSet()
			}
			packToPackBlobHash[pbs.PackID].Insert(packBlobHash)
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		debug.Log("%d blobs processed", cnt)
		return nil
	})
	if err != nil {
		// failed to load the index
		return hints, append(errs, err)
	}

	if packTypes == nil {
		// Previously loaded or unsaved blobs do not appear in load callbacks.
		packTypes, err = computePackTypes(ctx, c.repo)
		if err != nil {
			return hints, append(errs, err)
		}
	}

	debug.Log("checking for duplicate packs")
	for packID := range packTypes {
		debug.Log("  check pack %v: contained in %d indexes", packID, len(packToIndex[packID]))
		if len(packToPackBlobHash[packID]) > 1 {
			hints = append(hints, &ErrIncompletePackEntry{
				PackID:  packID,
				Indexes: packToIndex[packID],
			})
		} else if len(packToIndex[packID]) > 1 {
			hints = append(hints, &ErrDuplicatePacks{
				PackID:  packID,
				Indexes: packToIndex[packID],
			})
		}
		if packTypes[packID] == restic.InvalidBlob {
			hints = append(hints, &ErrMixedPack{
				PackID: packID,
			})
		}
	}

	return hints, errs
}

// Packs checks that all packs referenced in the index are still available and
// there are no packs that aren't in an index. errChan is closed after all
// packs have been checked.
func (c *Checker) Packs(ctx context.Context, errChan chan<- error) {
	c.PacksWithPreparedSizes(ctx, nil, errChan)
}

// PacksWithPreparedSizes checks current backend contents against sizes derived
// from the already-loaded index. Only that computation may precede confirmation;
// the backend listing and diagnostic settlement remain here.
func (c *Checker) PacksWithPreparedSizes(ctx context.Context, prepared *PreparedPackSizes, errChan chan<- error) {
	defer close(errChan)

	// compute pack size using index entries
	packs, err := c.takePackSizes(ctx, prepared)
	if err != nil {
		errChan <- err
		return
	}

	debug.Log("checking for %d packs", len(packs))

	debug.Log("listing repository packs")
	repoPacks := make(map[restic.ID]int64)

	err = c.repo.List(ctx, restic.PackFile, func(id restic.ID, size int64) error {
		repoPacks[id] = size
		return nil
	})

	if err != nil {
		errChan <- err
	}

	for id, size := range packs {
		reposize, ok := repoPacks[id]
		// remove from repoPacks so we can find orphaned packs
		delete(repoPacks, id)

		// missing: present in c.packs but not in the repo
		if !ok {
			select {
			case <-ctx.Done():
				return
			case errChan <- &ErrPackMetadata{ID: id, Missing: true, Err: errors.New("does not exist")}:
			}
			continue
		}

		// size not matching: present in c.packs and in the repo, but sizes do not match
		if size != reposize {
			select {
			case <-ctx.Done():
				return
			case errChan <- &ErrPackMetadata{ID: id, Truncated: true, Err: errors.Errorf("unexpected file size: got %d, expected %d", reposize, size)}:
			}
		}
	}

	// orphaned: present in the repo but not in c.packs
	for orphanID := range repoPacks {
		select {
		case <-ctx.Done():
			return
		case errChan <- &ErrPackMetadata{ID: orphanID, Orphaned: true, Err: errors.New("not referenced in any index")}:
		}
	}
}

// ReadPacks loads data from specified packs and checks the integrity.
func (c *Checker) ReadPacks(ctx context.Context, filter func(packs map[restic.ID]int64) map[restic.ID]int64, printer restic.Printer, errChan chan<- error) {
	c.ReadPacksWithPreparedSizes(ctx, nil, filter, printer, errChan)
}

// ReadPacksWithPreparedSizes reuses an index-derived size result after the
// metadata check has finished. Normal callers retain the synchronous path.
func (c *Checker) ReadPacksWithPreparedSizes(ctx context.Context, prepared *PreparedPackSizes, filter func(packs map[restic.ID]int64) map[restic.ID]int64, printer restic.Printer, errChan chan<- error) {
	defer close(errChan)

	// compute pack size using index entries
	packs, err := c.takePackSizes(ctx, prepared)
	if err != nil {
		errChan <- err
		return
	}
	packs = filter(packs)

	p := printer.NewCounter("packs")
	p.SetMax(uint64(len(packs)))
	defer p.Done()

	packSet := restic.NewIDSet()
	for pack := range packs {
		packSet.Insert(pack)
	}

	if feature.Flag.Enabled(feature.S3Restore) {
		job, err := c.repo.StartWarmup(ctx, packSet)
		if err != nil {
			errChan <- err
			return
		}
		if job.HandleCount() != 0 {
			printer.P("warming up %d packs from cold storage, this may take a while...", job.HandleCount())
			if err := job.Wait(ctx); err != nil {
				errChan <- err
				return
			}
		}
	}

	g, ctx := errgroup.WithContext(ctx)
	type checkTask struct {
		id    restic.ID
		size  int64
		blobs pack.Blobs
		prior error
	}
	ch := make(chan checkTask)
	extraCh := make(chan checkTask, 1)
	extraDone := make(chan struct{})

	// as packs are streamed the concurrency is limited by IO
	workerCount := int(c.repo.Connections())
	// run workers
	for range workerCount {
		g.Go(func() error {
			bufRd := bufio.NewReaderSize(nil, maxStreamBufferSize)
			dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
			if err != nil {
				panic(err)
			}
			defer dec.Close()
			for {
				var ps checkTask
				var ok bool

				select {
				case <-ctx.Done():
					return nil
				case ps, ok = <-ch:
					if !ok {
						return nil
					}
				}

				err := checkPack(ctx, c.repo, ps.id, ps.blobs, ps.size, bufRd, dec)
				if err == nil && ps.prior != nil {
					err = fmt.Errorf("check successful on second attempt, original error %w", ps.prior)
				}
				p.Add(1)
				if err == nil {
					continue
				}

				select {
				case <-ctx.Done():
					return nil
				case errChan <- err:
				}
			}
		})
	}

	// One extra worker retains the installed small-pack route and can also
	// stream mixed packs within explicit ciphertext and decoder limits. The
	// backend's scoped extra slot is released before promoting unfinished work.
	g.Go(func() error {
		defer close(extraDone)
		var bufRd *bufio.Reader
		var dec *zstd.Decoder
		var capacityDec *zstd.Decoder
		var capacityState checkCapacityState
		defer func() {
			if dec != nil {
				dec.Close()
			}
			if capacityDec != nil {
				capacityDec.Close()
			}
		}()
		checkCtx := sema.WithAdditionalLoadSlot(ctx)
		for {
			var ps checkTask
			var ok bool
			select {
			case <-ctx.Done():
				return nil
			case ps, ok = <-extraCh:
				if !ok {
					return nil
				}
			}
			if bufRd == nil {
				bufRd = bufio.NewReaderSize(nil, smallCheckStreamBufferSize)
			}

			var err error
			if smallCheckTask(ps.blobs) {
				// The ordinary decoder belongs only to the inherited small-pack
				// path. Mixed packs must not eagerly acquire its retained buffers.
				if dec == nil {
					dec, err = zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
					if err != nil {
						return err
					}
				}
				err = checkPack(checkCtx, c.repo, ps.id, ps.blobs, ps.size, bufRd, dec)
			} else {
				if capacityDec == nil {
					capacityDec, err = newCapacityCheckDecoder()
					if err != nil {
						return err
					}
				}
				err = checkPackCapacity(checkCtx, c.repo, ps.id, ps.blobs, ps.size, bufRd, capacityDec, &capacityState)
				var refused *checkCapacityRefusal
				if errors.As(err, &refused) {
					ps.prior = refused.prior
					select {
					case ch <- ps:
					case <-ctx.Done():
						return nil
					}
					continue
				}
			}
			p.Add(1)
			if err == nil {
				continue
			}
			select {
			case <-ctx.Done():
				return nil
			case errChan <- err:
			}
		}
	})

	// push packs to ch
	for pbs := range c.repo.listPacksFromIndex(ctx, packSet) {
		size := packs[pbs.PackID]
		debug.Log("listed %v", pbs.PackID)
		task := checkTask{id: pbs.PackID, size: size, blobs: pbs.Blobs}
		if smallCheckTask(task.blobs) || capacityCheckTask(task.blobs, size) {
			// Never let a busy extra worker stall pack enumeration; the
			// configured workers can always take this task instead.
			select {
			case extraCh <- task:
				continue
			default:
			}
		}
		select {
		case ch <- task:
		case <-ctx.Done():
			close(extraCh)
			<-extraDone
			close(ch)
			_ = g.Wait()
			return
		}
	}
	close(extraCh)
	// The extra worker can return unfinished tasks to ordinary workers. Keep
	// their input open until that producer has relinquished all of its tasks.
	<-extraDone
	close(ch)

	err = g.Wait()
	if err != nil {
		select {
		case <-ctx.Done():
			return
		case errChan <- err:
		}
	}
}

func smallCheckTask(blobs pack.Blobs) bool {
	for _, blob := range blobs {
		if blob.DataLength() > smallCheckBlobLimit {
			return false
		}
	}
	return len(blobs) != 0
}

// checkPack reads a pack and checks the integrity of all blobs.
func checkPack(ctx context.Context, r *Repository, id restic.ID, blobs pack.Blobs, size int64, bufRd *bufio.Reader, dec *zstd.Decoder) error {
	err := checkPackInner(ctx, r, id, blobs, size, bufRd, dec)
	if err != nil {
		if r.cache != nil {
			// ignore error as there's not much we can do here
			_ = r.cache.Forget(backend.Handle{Type: backend.PackFile, Name: id.String()})
		}

		// retry pack verification to detect transient errors
		err2 := checkPackInner(ctx, r, id, blobs, size, bufRd, dec)
		if err2 != nil {
			err = err2
		} else {
			err = fmt.Errorf("check successful on second attempt, original error %w", err)
		}
	}
	return err
}

func checkPackInner(ctx context.Context, r *Repository, id restic.ID, blobs pack.Blobs, size int64, bufRd *bufio.Reader, dec *zstd.Decoder) error {
	return checkPackInnerWithCapacity(ctx, r, id, blobs, size, bufRd, dec, nil)
}

func checkPackInnerWithCapacity(ctx context.Context, r *Repository, id restic.ID, blobs pack.Blobs, size int64, bufRd *bufio.Reader, dec *zstd.Decoder, capacity *checkCapacityState) error {
	bounded := capacity != nil

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
	refused := false
	h := backend.Handle{Type: backend.PackFile, Name: id.String()}
	err := r.be.Load(ctx, h, int(size), 0, func(rd io.Reader) error {
		refused = false
		// The worker reuses the decoder and buffered reader across packs.
		// Detach this callback's readers before verifying the captured header
		// or waiting for another pack, including on failed/retried reads.
		defer func() {
			_ = dec.Reset(nil)
			bufRd.Reset(nil)
		}()
		hrd := hashing.NewReader(rd, sha256.New())
		bufRd.Reset(hrd)
		// reset blob errors for each retry
		blobErrors = nil

		blobReader := newBufReader(bufRd)
		if !bounded {
			blobReader.ordinary = true
			blobReader.reserve = ordinaryCheckCipherReserve(blobs, size)
		}
		if bounded {
			blobReader.max = checkCipherCapacity
			if capacity.cipher == nil {
				capacity.cipher = make([]byte, checkCipherCapacity)
			}
			blobReader.buf = capacity.cipher
		}
		it := newPackBlobIterator(id, blobReader, 0, blobs, r.Key(), dec)
		for {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			val, err := it.NextHash()
			if err == errPackEOF {
				break
			} else if err != nil {
				if bounded && errors.Is(err, errCheckCapacity) {
					refused = true
					return nil
				}
				return &partialReadError{err}
			}
			debug.Log("  check blob %v: %v", val.Handle.ID, val.Handle)
			if val.Err != nil {
				if bounded && (errors.Is(val.Err, zstd.ErrWindowSizeExceeded) || errors.Is(val.Err, zstd.ErrDecoderSizeExceeded)) {
					refused = true
					return nil
				}
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
		if bounded && (size-int64(curPos) < 0 || size-int64(curPos) > checkHeaderCapacity) {
			refused = true
			return nil
		}
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
	if refused {
		var prior error
		if len(errs) != 0 {
			prior = &ErrPackData{PackID: id, errs: errs}
		}
		return &checkCapacityRefusal{prior: prior}
	}
	if !hash.Equal(id) {
		debug.Log("pack ID does not match, want %v, got %v", id, hash)
		return &ErrPackData{PackID: id, errs: append(errs, errors.Errorf("unexpected pack id %v", hash))}
	}

	if len(hdrBuf) >= largeCheckHeaderThreshold {
		return checkLargePackHeader(r, id, idxHdrSize, hdrBuf, errs)
	}
	blobs, hdrSize, err := pack.ListOwnedTail(r.Key(), hdrBuf)
	if err != nil {
		return &ErrPackData{PackID: id, errs: append(errs, err)}
	}

	if uint32(idxHdrSize) != hdrSize {
		debug.Log("Pack header size does not match, want %v, got %v", idxHdrSize, hdrSize)
		errs = append(errs, errors.Errorf("pack header size does not match, want %v, got %v", idxHdrSize, hdrSize))
	}

	for _, blob := range blobs {
		// Check if blob is contained in index and position is correct
		idxHas := r.idx.ContainsPackBlob(id, blob)
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

type bufReader struct {
	rd       *bufio.Reader
	buf      []byte
	max      int
	reserve  int
	ordinary bool
}

func newBufReader(rd *bufio.Reader) *bufReader {
	return &bufReader{
		rd: rd,
	}
}

func (b *bufReader) Discard(n int) (discarded int, err error) {
	return b.rd.Discard(n)
}

func (b *bufReader) ReadFull(n int) (buf []byte, err error) {
	if b.ordinary && b.max == 0 {
		return b.readFullOrdinary(n)
	}
	if b.max != 0 && n > b.max {
		return nil, errCheckCapacity
	}
	if cap(b.buf) < n {
		b.buf = make([]byte, n)
	}
	b.buf = b.buf[:n]

	_, err = io.ReadFull(b.rd, b.buf)
	if err != nil {
		return nil, err
	}
	return b.buf, nil
}
