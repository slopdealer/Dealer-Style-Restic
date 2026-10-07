package main

import (
	"context"

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
// confirmed, the snapshot files and then the index files are listed again.
// Each of the two listings is taken where the command takes it in stock and
// no earlier: the snapshot files when the command selects its snapshots, the
// index files when it loads the index, which for restore, backup and forget
// is after it has read the snapshots it selected. With any backend, also one
// whose listings lag behind what was written, this build therefore knows at
// each of these points what stock knows there; what was read during the wait
// only saves the downloads.
//
//   - The command always works with the snapshot listing taken after
//     confirmation.
//   - The index loaded during the wait is kept only if every index file it was
//     built from is still listed, with the size it was listed with when it was
//     loaded. Index files added in the meantime are loaded in addition.
//   - If a snapshot file or an index file read during the wait is gone or has
//     another size, or if any read during the wait failed, everything read
//     during the wait is discarded and read again as stock does: snapshots,
//     then the index.
//
// A name is the hash of the content that was read during the wait. It does
// not say what is stored under that name now: a file that was damaged in
// between keeps its name. The size catches a file that was cut short or
// replaced by one of another length; it does not catch damage that leaves the
// length alone, which only reading the file again does. Commands that work
// with the index accept that for the index files: stock reads them through
// the cache as well and does not see such damage either unless the cache is
// off. check does not: its purpose is to read what is stored, so check starts
// no reads during the wait and reads the snapshots and the index after the
// lock is confirmed, as stock does.
type lockWaitVerdict int

const (
	// rereadLockWaitReads is the zero value, so every early return rereads.
	rereadLockWaitReads lockWaitVerdict = iota
	keepLockWaitReads
	extendLockWaitReads
)

var errLockWaitSnapshotListing = errors.New("the snapshot listing taken during the lock wait failed")

// validateLockWaitReads compares what was read during the wait with the
// repository as it is now. It lists the index files, so it must be called
// where the command loads the index, and only once the lock is confirmed.
// waited and confirmed are the snapshot listings of the wait and of the
// command (nil when the command prepares none), waitErr is the error of the
// reads of the wait. With extendLockWaitReads the second result holds the
// index files (ID to size) that are listed now and were not when the index
// was loaded.
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
	for id, size := range repo.LoadedIndexFiles() {
		// kept only when it is listed now as it was listed when it was loaded
		if now, ok := listed[id]; !ok || size < 0 || now != size {
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
	ctx       context.Context // the lock's context
	loadCtx   context.Context
	repo      *repository.Repository
	cancel    context.CancelFunc
	result    chan error
	progress  *delayedTerminalCounterFactory
	snapshots *preparedSnapshots // listed during the wait; only compared
	// confirmed is the listing the command took after the lock was confirmed
	confirmed *preparedSnapshots
	verdict   lockWaitVerdict
	waitErr   error
	err       error
	joined    bool
	done      bool
	used      bool
	reread    bool
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

// snapshotLister lists the snapshot files and returns the listing, or
// fallback if the load prepares none or the listing failed. In the latter
// case the command lists again and reports the error as stock does. The
// command calls it where it lists the snapshot files in stock; it must not be
// called before the lock is confirmed.
func (load *repositoryIndexLoad) snapshotLister(fallback restic.Lister) restic.Lister {
	if load == nil || load.snapshots == nil {
		return fallback
	}
	listed := newPreparedSnapshots()
	listed.list(load.ctx, load.repo)
	load.confirmed = listed
	return listed.get(fallback)
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
	}
	// A confirmed lock starts nothing here: the listings that decide about
	// what was read are taken where the command takes them.
}

// join waits for the reads of the wait.
func (load *repositoryIndexLoad) join() {
	if load.joined {
		return
	}
	load.waitErr = <-load.result
	load.joined = true
}

// consume returns the index to the command. The command calls it where it
// loads the index in stock: the index files are listed here.
func (load *repositoryIndexLoad) consume(printer restic.TerminalCounterFactory) error {
	load.used = true
	if load.done {
		return load.err
	}
	load.join()
	load.done = true
	confirmed := load.confirmed
	if load.snapshots != nil && confirmed == nil {
		// The command has not listed the snapshot files. They are listed for
		// the comparison only, before the index files as always.
		confirmed = newPreparedSnapshots()
		confirmed.list(load.ctx, load.repo)
	}
	var added map[restic.ID]int64
	load.verdict, added = validateLockWaitReads(load.ctx, load.repo, load.snapshots, confirmed, load.waitErr)
	switch load.verdict {
	case keepLockWaitReads:
		load.progress.activate(printer)
	case extendLockWaitReads:
		load.progress.activate(printer)
		// only the added index files are read
		load.err = load.repo.LoadAddedIndexFiles(load.ctx, added)
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
	load.done = true
	load.repo.DiscardIndex()
}
