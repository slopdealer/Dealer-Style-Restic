package main

import (
	"context"
	"sync"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// backupIndexLoad prepares the backup index while the append lock's required
// settle delay is running. Its result is consumed only after lock validation.
type backupIndexLoad struct {
	repo      *repository.Repository
	cancel    context.CancelFunc
	result    chan error
	progress  *delayedTerminalCounterFactory
	snapshots *preparedSnapshots
	done      bool
}

func startBackupIndexLoad(ctx context.Context, repo *repository.Repository) *backupIndexLoad {
	return startBackupIndexLoadAfterSnapshots(ctx, repo, false)
}

// startBackupIndexLoadAfterSnapshots lists the snapshot files for the parent
// snapshot search before the index files when listSnapshots is set, keeping
// the stock listing order (snapshots, then index).
func startBackupIndexLoadAfterSnapshots(ctx context.Context, repo *repository.Repository, listSnapshots bool) *backupIndexLoad {
	loadCtx, cancel := context.WithCancel(ctx)
	load := &backupIndexLoad{
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

// parentSearchRepo returns repo with snapshot listings served from the
// listing prepared before the index load.
func (load *backupIndexLoad) parentSearchRepo(repo restic.ListerLoaderUnpacked) restic.ListerLoaderUnpacked {
	if load == nil || load.snapshots == nil {
		return repo
	}
	return snapshotListerRepo{ListerLoaderUnpacked: repo, snapshots: load.snapshots.get(repo)}
}

func (load *backupIndexLoad) wait(printer restic.TerminalCounterFactory) error {
	load.progress.activate(printer)
	err := <-load.result
	load.cancel()
	load.done = true
	if err != nil {
		load.repo.DiscardIndex()
	}
	return err
}

func (load *backupIndexLoad) discard() {
	if load == nil || load.done {
		return
	}
	load.cancel()
	<-load.result
	load.repo.DiscardIndex()
	load.done = true
}

// delayedTerminalCounterFactory records progress until the lock has been
// validated and backup reaches its ordinary LoadIndex reporting point.
type delayedTerminalCounterFactory struct {
	mu       sync.Mutex
	target   restic.TerminalCounterFactory
	counters []*delayedTerminalCounter
}

func (factory *delayedTerminalCounterFactory) NewCounterTerminalOnly(description string) restic.Counter {
	factory.mu.Lock()
	defer factory.mu.Unlock()

	counter := &delayedTerminalCounter{description: description}
	factory.counters = append(factory.counters, counter)
	if factory.target != nil {
		counter.attach(factory.target.NewCounterTerminalOnly(description))
	}
	return counter
}

func (factory *delayedTerminalCounterFactory) activate(target restic.TerminalCounterFactory) {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	if factory.target != nil {
		return
	}
	factory.target = target
	for _, counter := range factory.counters {
		counter.attach(target.NewCounterTerminalOnly(counter.description))
	}
}

type delayedTerminalCounter struct {
	mu          sync.Mutex
	description string
	target      restic.Counter
	current     uint64
	maximum     uint64
	done        bool
}

func (counter *delayedTerminalCounter) Add(amount uint64) {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.current += amount
	if counter.target != nil {
		counter.target.Add(amount)
	}
}

func (counter *delayedTerminalCounter) SetMax(maximum uint64) {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.maximum = maximum
	if counter.target != nil {
		counter.target.SetMax(maximum)
	}
}

func (counter *delayedTerminalCounter) Get() (uint64, uint64) {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	return counter.current, counter.maximum
}

func (counter *delayedTerminalCounter) Done() {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.done = true
	if counter.target != nil {
		counter.target.Done()
	}
}

func (counter *delayedTerminalCounter) attach(target restic.Counter) {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	counter.target = target
	target.SetMax(counter.maximum)
	target.Add(counter.current)
	if counter.done {
		target.Done()
	}
}
