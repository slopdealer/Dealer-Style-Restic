package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// Observe the second lock listing without holding it open. This intentionally
// does not assert completion: host scheduling determines how much finishes.
type ordinaryWaitObserver struct {
	backend.Backend
	mu             sync.Mutex
	load           *checkerIndexLoad
	start          time.Time
	lockLists      int
	atConfirmation time.Duration
	completed      bool
}

func (o *ordinaryWaitObserver) List(ctx context.Context, kind backend.FileType, fn func(backend.FileInfo) error) error {
	o.mu.Lock()
	if kind == backend.LockFile {
		o.lockLists++
		if o.lockLists == 2 && o.load != nil {
			o.atConfirmation = time.Since(o.start)
			select {
			case value := <-o.load.result:
				o.completed = value.packs != nil && len(value.errs) == 0
				o.load.result <- value
			default:
			}
		}
	}
	o.mu.Unlock()
	return o.Backend.List(ctx, kind, fn)
}

func TestCheckPreparationOrdinaryWaitCoverage(t *testing.T) {
	original, _, be := repository.TestRepositoryWithVersion(t, 0)
	data.TestCreateSnapshot(t, original, time.Unix(1470492820, 207401672), 1)
	o := &ordinaryWaitObserver{Backend: be}
	repo := repository.TestOpenBackend(t, o)
	repository.TestSetLockTimeout(t, 200*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)
	var load *checkerIndexLoad
	unlock, _, err := repository.LockRepoWithWaitWork(context.Background(), repo, true, 0, func(string) {}, func(string, ...any) {}, func(ctx context.Context) func(bool) {
		load = startCheckerIndexLoad(ctx, repo, false, &data.SnapshotFilter{}, nil)
		o.mu.Lock()
		o.load, o.start = load, time.Now()
		o.mu.Unlock()
		return load.settle
	})
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()
	defer load.discard()
	o.mu.Lock()
	completed, elapsed := o.completed, o.atConfirmation
	o.mu.Unlock()
	tailStart := time.Now()
	if err := load.loadSnapshots(); err != nil {
		t.Fatal(err)
	}
	_, errs := load.consume(restic.NoopTerminalCounterFactory)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	t.Logf("ordinary wait: elapsed=%v prepared-result-complete=%v post-confirmation-consume=%v (generated test repository only)", elapsed, completed, time.Since(tailStart))
}
