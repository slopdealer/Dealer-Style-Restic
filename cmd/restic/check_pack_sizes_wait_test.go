package main

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// Unlike a nominal worker-start signal, result publication proves that both
// checker-index loading and the subsequent size calculation have returned.
type packSizesConfirmationGate struct {
	backend.Backend
	mu           sync.Mutex
	lockLists    int
	packLists    int
	order        []backend.FileType
	confirmation chan struct{}
	resume       chan struct{}
}

func (g *packSizesConfirmationGate) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	g.mu.Lock()
	g.order = append(g.order, t)
	if t == backend.PackFile {
		g.packLists++
	}
	if t == backend.LockFile {
		g.lockLists++
	}
	confirming := t == backend.LockFile && g.lockLists == 2
	g.mu.Unlock()
	if confirming {
		close(g.confirmation)
		select {
		case <-g.resume:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return g.Backend.List(ctx, t, fn)
}

func TestPackSizesCompletePrivatelyBeforeConfirmation(t *testing.T) {
	for _, outcome := range []string{"confirmed", "competing-lock", "canceled"} {
		t.Run(outcome, func(t *testing.T) {
			original, unpacked, be := repository.TestRepositoryWithVersion(t, 0)
			data.TestCreateSnapshot(t, original, time.Unix(1470492820, 207401672), 1)
			gate := &packSizesConfirmationGate{Backend: be, confirmation: make(chan struct{}), resume: make(chan struct{})}
			repo := repository.TestOpenBackend(t, gate)
			repository.TestSetLockTimeout(t, 5*time.Millisecond)
			defer repository.TestSetLockTimeout(t, 0)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var resume sync.Once
			unblock := func() { resume.Do(func() { close(gate.resume) }) }
			defer unblock()
			started := make(chan *checkerIndexLoad, 1)
			type lockResult struct {
				unlock func()
				ctx    context.Context
				err    error
			}
			finished := make(chan lockResult, 1)
			go func() {
				unlock, lockCtx, err := repository.LockRepoWithWaitWork(ctx, repo, true, 0, func(string) {}, func(string, ...any) {}, func(ctx context.Context) func(bool) {
					load := startCheckerIndexLoad(ctx, repo, false, &data.SnapshotFilter{}, nil)
					started <- load
					return load.settle
				})
				finished <- lockResult{unlock, lockCtx, err}
			}()
			var load *checkerIndexLoad
			select {
			case load = <-started:
			case <-time.After(5 * time.Second):
				t.Fatal("preparation did not start")
			}
			select {
			case <-gate.confirmation:
			case <-time.After(5 * time.Second):
				t.Fatal("confirmation was not gated")
			}
			select {
			case value := <-load.result:
				rtest.Assert(t, value.packs != nil && len(value.errs) == 0, "sizes did not complete: %+v", value)
				load.result <- value
			case <-time.After(5 * time.Second):
				t.Fatal("sizes did not complete while confirmation was held")
			}
			rtest.Assert(t, load.progress.target == nil && !load.used, "speculative progress/result was published")
			gate.mu.Lock()
			packLists := gate.packLists
			var sawSnapshots, sawIndex bool
			for _, kind := range gate.order {
				if kind == backend.SnapshotFile {
					sawSnapshots = true
				}
				if kind == backend.IndexFile {
					rtest.Assert(t, sawSnapshots, "index listed before snapshots")
					sawIndex = true
				}
			}
			gate.mu.Unlock()
			rtest.Assert(t, sawIndex && packLists == 0, "preparation listed backend packs")
			var competitor restic.ID
			if outcome == "competing-lock" {
				hostname, _ := os.Hostname()
				var err error
				competitor, err = restic.SaveJSONUnpacked(context.Background(), unpacked, restic.LockFile, &repository.Lock{Time: time.Now(), PID: os.Getpid(), Hostname: hostname, Exclusive: true})
				rtest.OK(t, err)
			} else if outcome == "canceled" {
				cancel()
			}
			unblock()
			var got lockResult
			select {
			case got = <-finished:
			case <-time.After(5 * time.Second):
				t.Fatal("lock result did not settle")
			}
			if outcome == "confirmed" {
				rtest.OK(t, got.err)
				defer got.unlock()
				rtest.OK(t, load.loadSnapshots())
				_, errs := load.consume(restic.NoopTerminalCounterFactory)
				rtest.Equals(t, 0, len(errs))
				errChan := make(chan error)
				go load.checker.PacksWithPreparedSizes(got.ctx, load.packs, errChan)
				for err := range errChan {
					rtest.OK(t, err)
				}
				gate.mu.Lock()
				packLists = gate.packLists
				gate.mu.Unlock()
				rtest.Equals(t, 1, packLists)
			} else {
				if outcome == "competing-lock" {
					rtest.Assert(t, repository.IsAlreadyLocked(got.err), "wrong rejection: %v", got.err)
				} else {
					rtest.Assert(t, got.err != nil, "cancellation accepted")
				}
				rtest.Assert(t, load.done && !load.used && load.packs == nil, "rejected attempt retained sizes")
				select {
				case <-load.result:
					t.Fatal("rejected result not drained")
				default:
				}
				var locks []string
				rtest.OK(t, be.List(context.Background(), backend.LockFile, func(fi backend.FileInfo) error { locks = append(locks, fi.Name); return nil }))
				if outcome == "competing-lock" {
					rtest.Equals(t, []string{competitor.String()}, locks)
				} else {
					rtest.Equals(t, 0, len(locks))
				}
			}
		})
	}
}

func TestCheckerPackSizesDiscardJoinsPendingResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan checkerIndexLoadResult, 1)
	canceled := make(chan struct{})
	release := make(chan struct{})
	load := &checkerIndexLoad{repo: &repository.Repository{}, cancel: cancel, result: result, progress: &delayedTerminalCounterFactory{}}
	go func() {
		// Model size computation that has observed cancellation but still owns
		// its state. The final result is not published until it has returned.
		<-ctx.Done()
		close(canceled)
		<-release
		result <- checkerIndexLoadResult{packs: &repository.PreparedPackSizes{}}
	}()
	settled := make(chan struct{})
	go func() { load.settle(false); close(settled) }()
	<-canceled
	select {
	case <-settled:
		t.Fatal("discard returned before pending size work was joined")
	default:
	}
	close(release)
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("discard did not join released work")
	}
	rtest.Assert(t, load.done && !load.used && load.packs == nil && load.progress.target == nil, "discard published/retained speculative state")
}
