package main

import (
	"context"
	"sync"
	"testing"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

type waitIndexRecordingCounterFactory struct {
	mu       sync.Mutex
	counters []*waitIndexRecordingCounter
}

func (factory *waitIndexRecordingCounterFactory) NewCounterTerminalOnly(description string) restic.Counter {
	factory.mu.Lock()
	defer factory.mu.Unlock()

	counter := &waitIndexRecordingCounter{description: description}
	factory.counters = append(factory.counters, counter)
	return counter
}

func (factory *waitIndexRecordingCounterFactory) count() int {
	factory.mu.Lock()
	defer factory.mu.Unlock()
	return len(factory.counters)
}

type waitIndexRecordingCounter struct {
	mu          sync.Mutex
	description string
	current     uint64
	maximum     uint64
	done        bool
}

func (counter *waitIndexRecordingCounter) Add(amount uint64) {
	counter.mu.Lock()
	counter.current += amount
	counter.mu.Unlock()
}

func (counter *waitIndexRecordingCounter) SetMax(maximum uint64) {
	counter.mu.Lock()
	counter.maximum = maximum
	counter.mu.Unlock()
}

func (counter *waitIndexRecordingCounter) Get() (uint64, uint64) {
	counter.mu.Lock()
	defer counter.mu.Unlock()
	return counter.current, counter.maximum
}

func (counter *waitIndexRecordingCounter) Done() {
	counter.mu.Lock()
	counter.done = true
	counter.mu.Unlock()
}

func TestDelayedTerminalCounterFactoryBuffersAndReplaysProgress(t *testing.T) {
	const updates = 1000

	progress := &delayedTerminalCounterFactory{}
	counter := progress.NewCounterTerminalOnly("index")
	counter.SetMax(updates)

	if progress.target != nil {
		t.Fatal("progress factory attached before activation")
	}

	target := &waitIndexRecordingCounterFactory{}
	var wg sync.WaitGroup
	wg.Add(1)
	started := make(chan struct{})
	go func() {
		defer wg.Done()
		close(started)
		for range updates {
			counter.Add(1)
		}
	}()
	<-started

	progress.activate(target)
	wg.Wait()
	counter.Done()
	progress.activate(target)

	if got := target.count(); got != 1 {
		t.Fatalf("target received %d counters, want 1", got)
	}
	target.mu.Lock()
	recorded := target.counters[0]
	target.mu.Unlock()
	if recorded.description != "index" {
		t.Fatalf("counter description = %q, want %q", recorded.description, "index")
	}
	if current, maximum := recorded.Get(); current != updates || maximum != updates {
		t.Fatalf("counter progress = (%d, %d), want (%d, %d)", current, maximum, updates, updates)
	}
	recorded.mu.Lock()
	done := recorded.done
	recorded.mu.Unlock()
	if !done {
		t.Fatal("counter completion was not replayed")
	}
}

func TestRepositoryIndexLoadCancelsAndDrainsOnFailedLockConfirmation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	workDone := make(chan struct{})
	go func() {
		<-ctx.Done()
		close(workDone)
		result <- ctx.Err()
	}()

	load := &repositoryIndexLoad{
		repo:     &repository.Repository{},
		cancel:   cancel,
		result:   result,
		progress: &delayedTerminalCounterFactory{},
	}
	load.settle(false)

	if !load.done || load.used {
		t.Fatalf("failed lock settlement state = done:%v used:%v, want done and unused", load.done, load.used)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("wait-work context error = %v, want context.Canceled", ctx.Err())
	}
	select {
	case <-workDone:
	default:
		t.Fatal("failed lock settlement returned before repository index work drained")
	}
	select {
	case err := <-result:
		t.Fatalf("repository index result was not discarded: %v", err)
	default:
	}
}
