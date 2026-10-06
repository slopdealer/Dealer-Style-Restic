package main

import (
	"context"

	"github.com/restic/restic/internal/checker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// Reads started while the lock's settle delay runs are taken BEFORE the second
// lock check. That check only proves that no conflicting lock exists at the
// moment it runs. A process whose lock was confirmed before ours became
// visible may have changed the repository and unlocked in between, so what was
// read during the wait can describe a repository that no longer exists. Stock
// reads nothing before the check.
//
// Nothing read during the wait is therefore used as it is. Once the lock is
// confirmed, the snapshot files and then the index files are listed again:
//
//   - The command always works with the snapshot listing taken after
//     confirmation.
//   - The index loaded during the wait is kept only if every index file it was
//     built from is still listed. Index files are named by the hash of their
//     content, so the same names mean the same content, and the index in memory
//     is then exactly what a load after confirmation would produce. Index files
//     added in the meantime are loaded in addition.
//   - If a snapshot file or an index file read during the wait is gone, or if
//     any read during the wait failed, everything read during the wait is
//     discarded and read again as stock does: snapshots, then the index.
type lockWaitVerdict int

const (
	// rereadLockWaitReads is the zero value, so every early return rereads.
	rereadLockWaitReads lockWaitVerdict = iota
	keepLockWaitReads
	extendLockWaitReads
)

var errLockWaitSnapshotListing = errors.New("the snapshot listing taken during the lock wait failed")

// lockWaitValidation is the comparison made after the lock is confirmed.
type lockWaitValidation struct {
	done chan struct{}
	// snapshots is the listing taken after confirmation. It is nil when the
	// command prepares no snapshot listing.
	snapshots *preparedSnapshots
	verdict   lockWaitVerdict
	// added: with extendLockWaitReads, the index files (ID to size) that are
	// listed now and were not when the index was loaded
	added map[restic.ID]int64
}

// startLockWaitValidation must only be called once the lock is confirmed.
// join waits for the reads started during the wait and returns their error.
func startLockWaitValidation(ctx context.Context, repo *repository.Repository, waited *preparedSnapshots, join func() error) *lockWaitValidation {
	v := &lockWaitValidation{done: make(chan struct{})}
	if waited != nil {
		v.snapshots = newPreparedSnapshots()
	}
	go func() {
		defer close(v.done)
		// Snapshots are listed before the index here too.
		v.snapshots.list(ctx, repo)
		waitErr := join()
		v.verdict, v.added = validateLockWaitReads(ctx, repo, waited, v.snapshots, waitErr)
	}()
	return v
}

func validateLockWaitReads(ctx context.Context, repo *repository.Repository, waited, confirmed *preparedSnapshots, waitErr error) (lockWaitVerdict, map[restic.ID]int64) {
	if waitErr != nil {
		return rereadLockWaitReads, nil
	}
	if waited != nil {
		// After a failed listing the command lists the snapshots itself. The
		// index must not be listed before that, so stop here.
		if waited.err != nil || confirmed.err != nil {
			return rereadLockWaitReads, nil
		}
		removed, err := snapshotsRemoved(ctx, waited.lister, confirmed.lister)
		if err != nil || removed {
			return rereadLockWaitReads, nil
		}
	}

	listed := make(map[restic.ID]int64)
	err := repo.List(ctx, restic.IndexFile, func(id restic.ID, size int64) error {
		listed[id] = size
		return nil
	})
	if err != nil {
		return rereadLockWaitReads, nil
	}
	loaded := repo.LoadedIndexIDs()
	for id := range loaded {
		if _, ok := listed[id]; !ok {
			return rereadLockWaitReads, nil
		}
		delete(listed, id)
	}
	if len(listed) > 0 {
		// what is left is listed and not loaded
		return extendLockWaitReads, listed
	}
	return keepLockWaitReads, nil
}

// snapshotsRemoved reports whether a snapshot file listed in before is missing
// from after.
func snapshotsRemoved(ctx context.Context, before, after restic.Lister) (bool, error) {
	current := restic.NewIDSet()
	err := after.List(ctx, restic.SnapshotFile, func(id restic.ID, _ int64) error {
		current.Insert(id)
		return nil
	})
	if err != nil {
		return true, err
	}
	removed := false
	err = before.List(ctx, restic.SnapshotFile, func(id restic.ID, _ int64) error {
		if !current.Has(id) {
			removed = true
		}
		return nil
	})
	return removed, err
}

// repositoryIndexLoad prepares the repository index during the lock's
// required settle delay. Progress and the result are exposed only after the
// second lock check succeeded and the prepared state was validated against
// the repository as it is then.
type repositoryIndexLoad struct {
	ctx        context.Context // the lock's context
	loadCtx    context.Context
	repo       *repository.Repository
	cancel     context.CancelFunc
	result     chan error
	progress   *delayedTerminalCounterFactory
	snapshots  *preparedSnapshots  // listed during the wait; only compared
	validation *lockWaitValidation // exists once the lock is confirmed
	err        error
	done       bool
	used       bool
	reread     bool
}

func startRepositoryIndexLoad(ctx context.Context, repo *repository.Repository) *repositoryIndexLoad {
	return startRepositoryIndexLoadAfterSnapshots(ctx, repo, false)
}

// startRepositoryIndexLoadAfterSnapshots lists the snapshot files before the
// index files when listSnapshots is set, keeping the stock listing order
// (snapshots, then index) for commands that select snapshots under a
// non-exclusive lock: every listed snapshot's index is in the loaded index.
func startRepositoryIndexLoadAfterSnapshots(ctx context.Context, repo *repository.Repository, listSnapshots bool) *repositoryIndexLoad {
	loadCtx, cancel := context.WithCancel(ctx)
	load := &repositoryIndexLoad{
		ctx:      ctx,
		loadCtx:  loadCtx,
		repo:     repo,
		cancel:   cancel,
		result:   make(chan error, 1),
		progress: &delayedTerminalCounterFactory{},
	}
	if listSnapshots {
		load.snapshots = newPreparedSnapshots()
	}
	go func() {
		load.snapshots.list(loadCtx, repo)
		if load.snapshots != nil && load.snapshots.err != nil {
			// The index is not listed after a failed snapshot listing: the
			// snapshots are listed again later, and the index must follow.
			load.result <- errLockWaitSnapshotListing
			return
		}
		load.result <- repo.LoadIndex(loadCtx, load.progress)
	}()
	return load
}

// snapshotLister returns the snapshot listing taken after the lock was
// confirmed, or fallback if none was prepared or the listing failed. In the
// latter case the command lists again and reports the error as stock does.
func (load *repositoryIndexLoad) snapshotLister(fallback restic.Lister) restic.Lister {
	if load == nil || load.snapshots == nil {
		return fallback
	}
	load.confirm()
	return load.validation.snapshots.get(fallback)
}

// preparedSnapshots holds a snapshot file listing taken before the index
// listing.
type preparedSnapshots struct {
	ready  chan struct{}
	lister restic.Lister
	err    error
}

func newPreparedSnapshots() *preparedSnapshots {
	return &preparedSnapshots{ready: make(chan struct{})}
}

func (p *preparedSnapshots) list(ctx context.Context, repo restic.Lister) {
	if p == nil {
		return
	}
	p.lister, p.err = restic.MemorizeList(ctx, repo, restic.SnapshotFile)
	close(p.ready)
}

// get waits for the prepared listing. On a listing error it returns fallback,
// so the command lists again and reports the error exactly as before.
func (p *preparedSnapshots) get(fallback restic.Lister) restic.Lister {
	if p == nil {
		return fallback
	}
	<-p.ready
	if p.err != nil {
		return fallback
	}
	return p.lister
}

// snapshotListerRepo serves snapshot listings from a prepared listing and
// everything else from the repository.
type snapshotListerRepo struct {
	restic.ListerLoaderUnpacked
	snapshots restic.Lister
}

func (r snapshotListerRepo) List(ctx context.Context, t restic.FileType, fn func(restic.ID, int64) error) error {
	if t == restic.SnapshotFile {
		return r.snapshots.List(ctx, t, fn)
	}
	return r.ListerLoaderUnpacked.List(ctx, t, fn)
}

// repositoryIndexWaitWork returns the wait work of a command that needs the
// index and, with listSnapshots, a snapshot listing. The lock code starts it
// once per lock attempt. The load of the attempt is published in target; a
// failed attempt leaves nothing behind there.
func repositoryIndexWaitWork(target **repositoryIndexLoad, listSnapshots bool) func(context.Context, *repository.Repository) func(bool) {
	return func(ctx context.Context, repo *repository.Repository) func(bool) {
		load := startRepositoryIndexLoadAfterSnapshots(ctx, repo, listSnapshots)
		*target = load
		return func(confirmed bool) {
			load.settle(confirmed)
			if !confirmed && *target == load {
				*target = nil
			}
		}
	}
}

// settle is called by the lock code with the outcome of the second lock
// check, before the lock call returns.
func (load *repositoryIndexLoad) settle(confirmed bool) {
	if load == nil || load.done {
		return
	}
	if !confirmed {
		load.discard()
		return
	}
	load.confirm()
}

// confirm starts the comparison with the repository as it is now. It must not
// be called before the lock is confirmed.
func (load *repositoryIndexLoad) confirm() {
	if load.validation != nil {
		return
	}
	load.validation = startLockWaitValidation(load.loadCtx, load.repo, load.snapshots, func() error {
		return <-load.result
	})
}

// join waits for every goroutine of this load.
func (load *repositoryIndexLoad) join() {
	if load.done {
		return
	}
	if load.validation != nil {
		<-load.validation.done
	} else {
		<-load.result
	}
	load.done = true
}

func (load *repositoryIndexLoad) consume(printer restic.TerminalCounterFactory) error {
	load.used = true
	if load.done {
		return load.err
	}
	load.confirm()
	load.join()
	switch load.validation.verdict {
	case keepLockWaitReads:
		load.progress.activate(printer)
	case extendLockWaitReads:
		load.progress.activate(printer)
		// only the added index files are read
		load.err = load.repo.LoadAddedIndexFiles(load.ctx, load.validation.added)
	default:
		load.reread = true
		load.repo.DiscardIndex()
		load.err = load.repo.LoadIndex(load.ctx, printer)
	}
	load.cancel()
	if load.err != nil {
		load.repo.DiscardIndex()
	}
	return load.err
}

// rereadAfterConfirmation reports whether everything read during the wait was
// discarded. It is meaningful after consume.
func (load *repositoryIndexLoad) rereadAfterConfirmation() bool {
	return load != nil && load.reread
}

// discard releases speculative index state if the command reaches a path
// that never needs the prepared index (for example, forget removes no
// snapshots and consequently skips prune).
func (load *repositoryIndexLoad) discard() {
	if load == nil {
		return
	}
	load.cancel()
	load.join()
	load.repo.DiscardIndex()
}

type checkerIndexLoadResult struct {
	hints []error
	errs  []error
	packs *repository.PreparedPackSizes
	// failed is set when the snapshot listing taken during the wait failed
	failed bool
}

// checkerIndexLoad prepares check's checker-specific index pass during the
// settle delay. The snapshot files are listed first, keeping the stock
// listing order. After the lock is confirmed the prepared pass is kept only
// if the snapshot and index files are still the ones read during the wait;
// otherwise check loads everything again exactly as stock does. Progress
// remains buffered until the command consumes the prepared result.
type checkerIndexLoad struct {
	ctx         context.Context // the lock's context
	loadCtx     context.Context
	repo        *repository.Repository
	checker     *checker.Checker
	trackUnused bool
	filter      *data.SnapshotFilter
	args        []string
	cancel      context.CancelFunc
	result      chan checkerIndexLoadResult
	progress    *delayedTerminalCounterFactory
	snapshots   *preparedSnapshots  // listed during the wait; only compared
	validation  *lockWaitValidation // exists once the lock is confirmed
	value       checkerIndexLoadResult
	packs       *repository.PreparedPackSizes
	done        bool
	used        bool
	reread      bool
}

func startCheckerIndexLoad(ctx context.Context, repo *repository.Repository, trackUnused bool, snapshotFilter *data.SnapshotFilter, args []string) *checkerIndexLoad {
	loadCtx, cancel := context.WithCancel(ctx)
	chkr := checker.New(repo, trackUnused)
	load := &checkerIndexLoad{
		ctx:         ctx,
		loadCtx:     loadCtx,
		repo:        repo,
		checker:     chkr,
		trackUnused: trackUnused,
		filter:      snapshotFilter,
		args:        args,
		cancel:      cancel,
		result:      make(chan checkerIndexLoadResult, 1),
		progress:    &delayedTerminalCounterFactory{},
		snapshots:   newPreparedSnapshots(),
	}
	go func() {
		load.snapshots.list(loadCtx, repo)
		if load.snapshots.err != nil {
			load.result <- checkerIndexLoadResult{failed: true}
			return
		}
		hints, errs := chkr.LoadIndex(loadCtx, load.progress)
		var packs *repository.PreparedPackSizes
		if len(errs) == 0 && loadCtx.Err() == nil {
			packs = chkr.PreparePackSizes(loadCtx)
		}
		load.result <- checkerIndexLoadResult{hints: hints, errs: errs, packs: packs}
	}()
	return load
}

// checkerIndexWaitWork returns the wait work of check. See
// repositoryIndexWaitWork for target.
func checkerIndexWaitWork(target **checkerIndexLoad, trackUnused bool, snapshotFilter *data.SnapshotFilter, args []string) func(context.Context, *repository.Repository) func(bool) {
	return func(ctx context.Context, repo *repository.Repository) func(bool) {
		load := startCheckerIndexLoad(ctx, repo, trackUnused, snapshotFilter, args)
		*target = load
		return func(confirmed bool) {
			load.settle(confirmed)
			if !confirmed && *target == load {
				*target = nil
			}
		}
	}
}

func (load *checkerIndexLoad) settle(confirmed bool) {
	if load == nil || load.done {
		return
	}
	if !confirmed {
		load.discard()
		return
	}
	load.confirm()
}

// confirm starts the comparison with the repository as it is now. It must not
// be called before the lock is confirmed.
func (load *checkerIndexLoad) confirm() {
	if load.validation != nil {
		return
	}
	load.validation = startLockWaitValidation(load.loadCtx, load.repo, load.snapshots, func() error {
		load.value = <-load.result
		if load.value.failed || len(load.value.errs) > 0 || load.value.packs == nil {
			// Errors met during the wait are not reported: the index is
			// read again after confirmation and reported from there.
			return errLockWaitSnapshotListing
		}
		return nil
	})
}

func (load *checkerIndexLoad) join() {
	if load.done {
		return
	}
	if load.validation != nil {
		<-load.validation.done
	} else {
		<-load.result
	}
	load.done = true
}

// decide waits for the comparison made after the lock was confirmed and
// discards the prepared index pass unless it is still exact.
func (load *checkerIndexLoad) decide() {
	if load.done {
		return
	}
	load.confirm()
	load.join()
	// An added index file would need the checker's per-file bookkeeping to
	// be extended; check simply runs its index pass again in that case.
	if load.validation.verdict != keepLockWaitReads {
		load.reread = true
		load.value = checkerIndexLoadResult{}
		load.repo.DiscardIndex()
		// the blob set of a checker is bound to the index it was created on
		load.checker = checker.New(load.repo, load.trackUnused)
	}
}

// loadSnapshots hands the snapshot listing taken after the lock was confirmed
// to the checker and returns the error of that listing at the position of
// stock's LoadSnapshots call.
func (load *checkerIndexLoad) loadSnapshots() error {
	load.decide()
	listed := load.validation.snapshots
	if listed.err != nil {
		// list again and report the error as stock does
		return load.checker.LoadSnapshots(load.ctx, load.filter, load.args)
	}
	return load.checker.LoadSnapshotsFromList(load.ctx, listed.lister, load.filter, load.args)
}

// checkerForSnapshots returns the checker to continue with. It is valid after
// loadSnapshots.
func (load *checkerIndexLoad) checkerForSnapshots() *checker.Checker {
	return load.checker
}

func (load *checkerIndexLoad) consume(printer restic.TerminalCounterFactory) ([]error, []error) {
	load.decide()
	load.used = true
	defer load.cancel()
	if load.reread {
		return load.checker.LoadIndex(load.ctx, printer)
	}
	load.progress.activate(printer)
	value := load.value
	load.value = checkerIndexLoadResult{}
	load.packs = value.packs
	return value.hints, value.errs
}

func (load *checkerIndexLoad) discard() {
	if load == nil {
		return
	}
	load.cancel()
	load.join()
	load.packs = nil
	load.value = checkerIndexLoadResult{}
	load.repo.DiscardIndex()
}
