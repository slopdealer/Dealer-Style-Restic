package main

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// Model a repair that restores an indexed pack before lock confirmation. This
// controls backend visibility; it is not a stress test of two CLI processes.
func TestCheckPreparedSizesCompletingPackRepair(t *testing.T) {
	ctx := context.Background()
	original, _, be := repository.TestRepositoryWithVersion(t, 0)
	data.TestCreateSnapshot(t, original, time.Unix(1470492820, 207401672), 1)
	var h backend.Handle
	if err := be.List(ctx, backend.PackFile, func(fi backend.FileInfo) error {
		if h.Name == "" {
			h = backend.Handle{Type: backend.PackFile, Name: fi.Name}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if h.Name == "" {
		t.Fatal("fixture contains no pack")
	}
	var payload []byte
	if err := be.Load(ctx, h, 0, 0, func(r io.Reader) error {
		var err error
		payload, err = io.ReadAll(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := be.Remove(ctx, h); err != nil {
		t.Fatal(err)
	}
	gate := &packSizesConfirmationGate{Backend: be, confirmation: make(chan struct{}), resume: make(chan struct{})}
	repo := repository.TestOpenBackend(t, gate)
	repository.TestSetLockTimeout(t, 5*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)
	started := make(chan *checkerIndexLoad, 1)
	type result struct {
		unlock func()
		ctx    context.Context
		err    error
	}
	finished := make(chan result, 1)
	go func() {
		unlock, lockCtx, err := repository.LockRepoWithWaitWork(ctx, repo, true, 0, func(string) {}, func(string, ...any) {}, func(ctx context.Context) func(bool) {
			load := startCheckerIndexLoad(ctx, repo, false, &data.SnapshotFilter{}, nil)
			started <- load
			return load.settle
		})
		finished <- result{unlock, lockCtx, err}
	}()
	load := <-started
	<-gate.confirmation
	value := <-load.result
	load.result <- value
	if value.packs == nil || len(value.errs) != 0 {
		t.Fatal("index-only preparation failed while pack unavailable")
	}
	gate.mu.Lock()
	packLists := gate.packLists
	gate.mu.Unlock()
	if packLists != 0 {
		t.Fatal("speculative preparation observed pack availability")
	}
	// Repair completes and then confirmation proceeds. Subsequent metadata
	// checking must observe the restored pack, rather than an early absence.
	if err := be.Save(ctx, h, backend.NewByteReader(payload, be.Hasher())); err != nil {
		t.Fatal(err)
	}
	close(gate.resume)
	got := <-finished
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer got.unlock()
	defer load.discard()
	if err := load.loadSnapshots(); err != nil {
		t.Fatal(err)
	}
	_, errs := load.consume(restic.NoopTerminalCounterFactory)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	errsCh := make(chan error)
	go load.checker.PacksWithPreparedSizes(got.ctx, load.packs, errsCh)
	for err := range errsCh {
		t.Error(err)
	}
}
