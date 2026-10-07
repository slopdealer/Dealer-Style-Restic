package main

import (
	"context"

	"github.com/restic/restic/internal/checker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// repositoryIndexLoad prepares the repository index during the lock's
// required settle delay. Progress and the result are exposed only after the
// second lock check succeeds.
type repositoryIndexLoad struct {
	repo      *repository.Repository
	cancel    context.CancelFunc
	result    chan error
	progress  *delayedTerminalCounterFactory
	snapshots *preparedSnapshots
	err       error
	done      bool
	used      bool
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
		load.result <- repo.LoadIndex(loadCtx, load.progress)
	}()
	return load
}

// snapshotLister returns the snapshot list prepared before the index load, or
// fallback if none was prepared.
func (load *repositoryIndexLoad) snapshotLister(fallback restic.Lister) restic.Lister {
	if load == nil {
		return fallback
	}
	return load.snapshots.get(fallback)
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

func (load *repositoryIndexLoad) settle(confirmed bool) {
	if load == nil || load.done {
		return
	}
	if !confirmed {
		load.discard()
	}
}

func (load *repositoryIndexLoad) consume(printer restic.TerminalCounterFactory) error {
	load.progress.activate(printer)
	if !load.done {
		load.err = <-load.result
		load.done = true
	}
	load.cancel()
	if load.err != nil {
		load.repo.DiscardIndex()
	}
	load.used = true
	return load.err
}

// discard releases speculative index state if the command reaches a path
// that never needs the prepared index (for example, forget removes no
// snapshots and consequently skips prune).
func (load *repositoryIndexLoad) discard() {
	if load == nil {
		return
	}
	if !load.done {
		load.cancel()
		<-load.result
		load.done = true
	}
	load.cancel()
	load.repo.DiscardIndex()
}

type checkerIndexLoadResult struct {
	hints []error
	errs  []error
	packs *repository.PreparedPackSizes
}

// checkerIndexLoad prepares check's checker-specific index pass during the
// settle delay. The snapshot listing (LoadSnapshots) runs first, keeping the
// stock listing order; its error is reported at the existing command position.
// Progress remains buffered until the command consumes the prepared result.
type checkerIndexLoad struct {
	repo         *repository.Repository
	checker      *checker.Checker
	cancel       context.CancelFunc
	result       chan checkerIndexLoadResult
	progress     *delayedTerminalCounterFactory
	snapshotsErr chan error
	packs        *repository.PreparedPackSizes
	done         bool
	used         bool
}

func startCheckerIndexLoad(ctx context.Context, repo *repository.Repository, trackUnused bool, snapshotFilter *data.SnapshotFilter, args []string) *checkerIndexLoad {
	loadCtx, cancel := context.WithCancel(ctx)
	chkr := checker.New(repo, trackUnused)
	load := &checkerIndexLoad{
		repo:         repo,
		checker:      chkr,
		cancel:       cancel,
		result:       make(chan checkerIndexLoadResult, 1),
		progress:     &delayedTerminalCounterFactory{},
		snapshotsErr: make(chan error, 1),
	}
	go func() {
		err := chkr.LoadSnapshots(loadCtx, snapshotFilter, args)
		load.snapshotsErr <- err
		if err != nil {
			load.result <- checkerIndexLoadResult{}
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

// loadSnapshots returns the result of the prepared LoadSnapshots call.
func (load *checkerIndexLoad) loadSnapshots() error {
	return <-load.snapshotsErr
}

func (load *checkerIndexLoad) settle(confirmed bool) {
	if load == nil || load.done {
		return
	}
	if !confirmed {
		load.discard()
	}
}

func (load *checkerIndexLoad) checkerForSnapshots() *checker.Checker {
	return load.checker
}

func (load *checkerIndexLoad) consume(printer restic.TerminalCounterFactory) ([]error, []error) {
	load.progress.activate(printer)
	value := <-load.result
	load.packs = value.packs
	load.done = true
	load.cancel()
	load.used = true
	return value.hints, value.errs
}

func (load *checkerIndexLoad) discard() {
	if load == nil {
		return
	}
	if !load.done {
		load.cancel()
		<-load.result
		load.done = true
	}
	load.cancel()
	load.packs = nil
	load.repo.DiscardIndex()
}
