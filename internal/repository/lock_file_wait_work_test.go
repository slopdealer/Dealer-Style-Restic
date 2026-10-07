package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

func TestNewLockSettlesWaitWorkBeforeRemovingFailedAttempt(t *testing.T) {
	be := newSecondScanLockBackend(mem.New())
	repo, _ := TestRepositoryWithBackend(t, be, 0, Options{})
	TestSetLockTimeout(t, 5*time.Millisecond)
	be.arm()

	type lockResult struct {
		lock *lockHandle
		err  error
	}
	result := make(chan lockResult, 1)
	started := make(chan struct{})
	workDone := make(chan struct{})
	settleEntered := make(chan bool, 1)
	releaseSettle := make(chan struct{})
	continueClosed := false
	releaseClosed := false
	defer func() {
		if !continueClosed {
			close(be.continueScan)
		}
		if !releaseClosed {
			close(releaseSettle)
		}
	}()

	go func() {
		lock, err := newLockWithWaitWork(context.Background(), &internalRepository{repo}, true, func(ctx context.Context) func(bool) {
			workCtx, cancel := context.WithCancel(ctx)
			go func() {
				close(started)
				<-workCtx.Done()
				close(workDone)
			}()
			return func(confirmed bool) {
				if !confirmed {
					cancel()
					<-workDone
				}
				settleEntered <- confirmed
				if !confirmed {
					<-releaseSettle
				}
			}
		})
		result <- lockResult{lock: lock, err: err}
	}()

	var ownLock backend.Handle
	select {
	case ownLock = <-be.savedLockFiles:
	case <-time.After(time.Second):
		t.Fatal("new lock was not saved")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("wait work did not start after lock creation")
	}
	select {
	case <-be.secondScan:
	case <-time.After(time.Second):
		t.Fatal("newLock did not reach its confirmation scan")
	}

	hostname, err := os.Hostname()
	rtest.OK(t, err)
	competitor := Lock{Time: time.Now(), PID: os.Getpid(), Hostname: hostname, Exclusive: true}
	competitorID, saveErr := restic.SaveJSONUnpacked(context.Background(), &internalRepository{repo}, restic.LockFile, &competitor)
	rtest.OK(t, saveErr)
	close(be.continueScan)
	continueClosed = true

	select {
	case confirmed := <-settleEntered:
		rtest.Assert(t, !confirmed, "failed confirmation settled wait work as confirmed")
	case <-time.After(time.Second):
		t.Fatal("failed confirmation did not settle wait work")
	}
	select {
	case <-workDone:
	default:
		t.Fatal("wait work was not joined before settlement")
	}
	select {
	case got := <-result:
		t.Fatalf("newLock returned before wait work settlement completed: lock %v, error %v", got.lock, got.err)
	default:
	}

	// While settlement is still in progress, the failed attempt's lock must
	// remain present. Removing it earlier could let a new writer begin while
	// the speculative read still owns resources from this attempt.
	var ownPresent, competitorPresent bool
	err = repo.List(context.Background(), restic.LockFile, func(id restic.ID, _ int64) error {
		ownPresent = ownPresent || id.String() == ownLock.Name
		competitorPresent = competitorPresent || id.Equal(competitorID)
		return nil
	})
	rtest.OK(t, err)
	rtest.Assert(t, ownPresent && competitorPresent,
		"expected both locks to remain until wait work settles, own=%v competitor=%v", ownPresent, competitorPresent)

	close(releaseSettle)
	releaseClosed = true
	got := <-result
	rtest.Assert(t, got.lock == nil && IsAlreadyLocked(got.err),
		"expected competing lock to reject confirmation, got lock %v and error %v", got.lock, got.err)
	rtest.Equals(t, competitorID, checkSingleLock(t, repo))
}

func TestLockRepoWaitWorkUsesUnlockContext(t *testing.T) {
	repo := TestRepository(t)
	TestSetLockTimeout(t, 5*time.Millisecond)
	parentCtx, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()

	started := make(chan struct{})
	workDone := make(chan struct{})
	settled := make(chan bool, 1)
	var workContext context.Context
	unlock, lockCtx, err := LockRepoWithWaitWork(parentCtx, repo, false, time.Second, func(string) {}, func(string, ...any) {}, func(ctx context.Context) func(bool) {
		workContext = ctx
		go func() {
			close(started)
			<-ctx.Done()
			close(workDone)
		}()
		return func(confirmed bool) {
			settled <- confirmed
		}
	})
	rtest.OK(t, err)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("wait work did not start")
	}
	select {
	case confirmed := <-settled:
		rtest.Assert(t, confirmed, "wait work was not confirmed after lock validation")
	case <-time.After(time.Second):
		t.Fatal("wait work was not settled")
	}
	if workContext != lockCtx {
		t.Fatal("wait work did not receive the lock-lifetime context")
	}

	unlock()
	select {
	case <-workDone:
	case <-time.After(time.Second):
		t.Fatal("unlock did not cancel wait work")
	}
	if lockCtx.Err() != context.Canceled {
		t.Fatalf("lock context error = %v, want context.Canceled", lockCtx.Err())
	}
}
