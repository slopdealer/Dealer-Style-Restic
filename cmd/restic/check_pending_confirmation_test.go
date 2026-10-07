package main

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

type checkIndexReadGate struct {
	backend.Backend
	once    sync.Once
	entered chan struct{}
	resume  chan struct{}
}

func (g *checkIndexReadGate) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	if h.Type == backend.IndexFile {
		g.once.Do(func() { close(g.entered) })
		select {
		case <-g.resume:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g.Backend.Load(ctx, h, length, offset, fn)
}

func TestCheckConfirmationWithPendingPreparation(t *testing.T) {
	original, _, be := repository.TestRepositoryWithVersion(t, 0)
	data.TestCreateSnapshot(t, original, time.Unix(1470492820, 207401672), 1)
	gate := &checkIndexReadGate{Backend: be, entered: make(chan struct{}), resume: make(chan struct{})}
	repo := repository.TestOpenBackend(t, gate)
	repository.TestSetLockTimeout(t, 200*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var resume sync.Once
	unblock := func() { resume.Do(func() { close(gate.resume) }) }
	defer unblock()
	started := make(chan *checkerIndexLoad, 1)
	type lockResult struct {
		unlock func()
		err    error
	}
	finished := make(chan lockResult, 1)
	go func() {
		unlock, _, err := repository.LockRepoWithWaitWork(ctx, repo, true, 0, func(string) {}, func(string, ...any) {}, func(ctx context.Context) func(bool) {
			load := startCheckerIndexLoad(ctx, repo, false, &data.SnapshotFilter{}, nil)
			started <- load
			return load.settle
		})
		finished <- lockResult{unlock, err}
	}()
	var load *checkerIndexLoad
	select {
	case load = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not start")
	}
	select {
	case <-gate.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("index read did not reach gate")
	}
	var got lockResult
	select {
	case got = <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("confirmation waited for preparation")
	}
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.unlock()
	defer load.discard()
	select {
	case value := <-load.result:
		load.result <- value
		t.Fatal("blocked index read published a result")
	default:
	}
	if load.done || load.used || load.progress.target != nil {
		t.Fatal("pending preparation published effects")
	}
	unblock()
	if err := load.loadSnapshots(); err != nil {
		t.Fatal(err)
	}
	_, errs := load.consume(restic.NoopTerminalCounterFactory)
	if len(errs) != 0 || load.packs == nil {
		t.Fatalf("pending preparation did not settle: %v", errs)
	}
}
