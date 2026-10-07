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
	rtest "github.com/restic/restic/internal/test"
)

// Keep a real index-load call alive after it observes cancellation. This
// distinguishes cancellation delivery from joining the load pipeline.
type packSizesBlockedIndex struct {
	backend.Backend
	entered, canceled, release chan struct{}
	enterOnce, cancelOnce      sync.Once
}

func (b *packSizesBlockedIndex) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	if h.Type != backend.IndexFile {
		return b.Backend.Load(ctx, h, length, offset, fn)
	}
	b.enterOnce.Do(func() { close(b.entered) })
	<-ctx.Done()
	b.cancelOnce.Do(func() { close(b.canceled) })
	<-b.release
	return ctx.Err()
}

func TestCheckerWaitCancellationJoinsBlockedIndexBeforeUnlock(t *testing.T) {
	original, _, be := repository.TestRepositoryWithVersion(t, 0)
	data.TestCreateSnapshot(t, original, time.Unix(1470492820, 207401672), 1)
	blocked := &packSizesBlockedIndex{Backend: be, entered: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	gate := &packSizesConfirmationGate{Backend: blocked, confirmation: make(chan struct{}), resume: make(chan struct{})}
	repo := repository.TestOpenBackend(t, gate)
	repository.TestSetLockTimeout(t, 5*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var release sync.Once
	unblock := func() { release.Do(func() { close(blocked.release) }) }
	defer unblock()
	done := make(chan error, 1)
	started := make(chan *checkerIndexLoad, 1)
	go func() {
		unlock, _, err := repository.LockRepoWithWaitWork(ctx, repo, true, 0, func(string) {}, func(string, ...any) {}, func(ctx context.Context) func(bool) {
			load := startCheckerIndexLoad(ctx, repo, false, &data.SnapshotFilter{}, nil)
			started <- load
			return load.settle
		})
		if unlock != nil {
			unlock()
		}
		done <- err
	}()
	wait := func(ch <-chan struct{}, stage string) {
		t.Helper()
		select {
		case <-ch:
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout at %s", stage)
		}
	}
	var load *checkerIndexLoad
	select {
	case load = <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("preparation did not start")
	}
	wait(blocked.entered, "index load")
	wait(gate.confirmation, "confirmation")
	cancel()
	wait(blocked.canceled, "index cancellation")
	select {
	case err := <-done:
		t.Fatalf("returned before index load joined: %v", err)
	default:
	}
	locks := 0
	rtest.OK(t, be.List(context.Background(), backend.LockFile, func(backend.FileInfo) error { locks++; return nil }))
	rtest.Equals(t, 1, locks)
	unblock()
	select {
	case err := <-done:
		rtest.Assert(t, err != nil, "canceled attempt succeeded")
	case <-time.After(5 * time.Second):
		t.Fatal("index pipeline did not join")
	}
	rtest.Assert(t, load.done && !load.used && load.packs == nil, "canceled result escaped")
	locks = 0
	rtest.OK(t, be.List(context.Background(), backend.LockFile, func(backend.FileInfo) error { locks++; return nil }))
	rtest.Equals(t, 0, locks)
}
