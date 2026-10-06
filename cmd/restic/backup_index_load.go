package main

import (
	"context"
	"sync"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// backupIndexLoad prepares the backup index while the append lock's required
// settle delay is running. It is the repository index load of the other
// commands: its result is used only after the lock was confirmed and the
// prepared state was compared with the repository as it is then.
type backupIndexLoad = repositoryIndexLoad

// startBackupIndexLoadAfterSnapshots lists the snapshot files for the parent
// snapshot search before the index files when listSnapshots is set, keeping
// the stock listing order (snapshots, then index).
func startBackupIndexLoadAfterSnapshots(ctx context.Context, repo *repository.Repository, listSnapshots bool) *backupIndexLoad {
	return startRepositoryIndexLoadAfterSnapshots(ctx, repo, listSnapshots)
}

// backupIndexWaitWork returns the wait work of backup. The snapshot files are
// listed only when backup will search for a parent snapshot.
func backupIndexWaitWork(target **backupIndexLoad, listSnapshots bool) func(context.Context, *repository.Repository) func(bool) {
	return repositoryIndexWaitWork(target, listSnapshots)
}

// wait returns the index for backup once the lock is confirmed.
func (load *repositoryIndexLoad) wait(printer restic.TerminalCounterFactory) error {
	return load.consume(printer)
}

// parentSearchRepo returns repo with snapshot listings served from the
// listing taken after the lock was confirmed.
func (load *repositoryIndexLoad) parentSearchRepo(repo restic.ListerLoaderUnpacked) restic.ListerLoaderUnpacked {
	if load == nil || load.snapshots == nil {
		return repo
	}
	return snapshotListerRepo{ListerLoaderUnpacked: repo, snapshots: load.snapshotLister(repo)}
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
