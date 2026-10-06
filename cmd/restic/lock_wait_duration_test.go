package main

// The lock's settle delay is never shortened, and nothing outlives the command
// (repair R1).
//
// For the wait work of backup, restore, prune and check, and for a number of
// ways the lock attempt or the command can end, the test checks on the
// in-memory backend, in virtual time (testing/synctest):
//
//   - the lock call does not return before the settle delay has passed since
//     the lock file was saved, and a listing of the lock files starts at or
//     after that moment and before the call returns: the confirmation scan
//     comes after the full delay, whatever the wait work does (finishes early,
//     is still running, fails, hangs) and also when the context is cancelled;
//   - the confirmation scan is not postponed by the wait work either;
//   - a conflicting lock that appears one millisecond before the delay ends
//     still refuses the lock;
//   - after the command has unlocked, no goroutine it started is left, on
//     success, on error and on cancellation; and a refused or cancelled
//     attempt leaves no lock file behind.
//
// Run:
//
//	go test -run 'TestLockWaitIsNeverShortened' -count=1 -v ./cmd/restic

import (
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

// waitProbeBackend records when lock files are saved and listed, and can make
// the reads of the wait work slow, failing or hanging.
type waitProbeBackend struct {
	backend.Backend
	t0 time.Time

	mu        sync.Mutex
	lockSaved []time.Duration // when a save of a lock file returned
	lockLists []time.Duration // when a listing of the lock files started
	returned  bool            // the lock call has returned

	readDelay time.Duration // every snapshot and index operation takes this long
	failReads bool          // snapshot and index listings fail until the lock call has returned
	hangReads bool          // index operations block until their context is cancelled
}

func (be *waitProbeBackend) now() time.Duration { return time.Since(be.t0) }

func (be *waitProbeBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	err := be.Backend.Save(ctx, h, rd)
	if h.Type == backend.LockFile && err == nil {
		be.mu.Lock()
		be.lockSaved = append(be.lockSaved, be.now())
		be.mu.Unlock()
	}
	return err
}

func (be *waitProbeBackend) read(ctx context.Context, t backend.FileType, list bool) error {
	if t != backend.SnapshotFile && t != backend.IndexFile {
		return nil
	}
	be.mu.Lock()
	returned := be.returned
	be.mu.Unlock()
	if be.hangReads && t == backend.IndexFile {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := schedSleep(ctx, be.readDelay); err != nil {
		return err
	}
	if be.failReads && list && !returned {
		return errors.New("injected listing failure during the lock wait")
	}
	return nil
}

func (be *waitProbeBackend) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	if t == backend.LockFile {
		be.mu.Lock()
		be.lockLists = append(be.lockLists, be.now())
		be.mu.Unlock()
	}
	if err := be.read(ctx, t, true); err != nil {
		return err
	}
	return be.Backend.List(ctx, t, fn)
}

func (be *waitProbeBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	if err := be.read(ctx, h.Type, false); err != nil {
		return err
	}
	return be.Backend.Load(ctx, h, length, offset, fn)
}

func TestLockWaitIsNeverShortened(t *testing.T) {
	repository.TestSetLockTimeout(t, schedSettle)
	defer repository.TestSetLockTimeout(t, 0)

	type scenario struct {
		name      string
		readDelay time.Duration
		failReads bool
		hangReads bool
		// cancelAt: cancel the command's context this long after the lock
		// file was saved (0: never)
		cancelAt time.Duration
		// competitorAt: another process's exclusive lock appears this long
		// after the lock file was saved (0: never)
		competitorAt time.Duration
		// use: after a confirmed lock the command reads on, as it does; else
		// it returns at once, as on an early error
		use bool
		// cancelBeforeUse: cancel the context once the lock is confirmed
		cancelBeforeUse bool
		wantLocked      bool
	}
	scenarios := []scenario{
		{name: "wait work finishes early", use: true, wantLocked: true},
		{name: "wait work still running at confirmation", readDelay: 150 * time.Millisecond, use: true, wantLocked: true},
		{name: "wait work fails", failReads: true, use: true, wantLocked: true},
		{name: "wait work hangs, command gives up", hangReads: true, wantLocked: true},
		{name: "command returns early", readDelay: 150 * time.Millisecond, wantLocked: true},
		{name: "cancelled after confirmation", readDelay: 150 * time.Millisecond, cancelBeforeUse: true, use: true, wantLocked: true},
		{name: "cancelled during the wait", readDelay: 30 * time.Millisecond, cancelAt: 50 * time.Millisecond},
		{name: "cancelled during the wait, wait work hangs", hangReads: true, cancelAt: 50 * time.Millisecond},
		{name: "conflicting lock early in the wait", competitorAt: time.Millisecond},
		{name: "conflicting lock 1 ms before the wait ends", readDelay: 30 * time.Millisecond, competitorAt: schedSettle - time.Millisecond},
		{name: "conflicting lock 1 ms before the wait ends, wait work hangs", hangReads: true, competitorAt: schedSettle - time.Millisecond},
	}
	kinds := []schedKind{schedBackup, schedRestore, schedPrune, schedCheck}

	for _, kind := range kinds {
		for _, sc := range scenarios {
			t.Run(fmt.Sprintf("%v/%s", kind, sc.name), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()

					// a repository with two snapshots and an unused blob, and
					// the bytes of another process's exclusive lock
					spec := &schedSpec{seed: 7, live: [][]int{{0, 1}, {2}}, forgotten: [][]int{{3}}}
					shared := schedTemplate(t, schedEmptyRepository(t), spec)
					other, err := schedOpenRepository(ctx, shared)
					rtest.OK(t, err)
					unlockOther, _, err := repository.LockRepo(ctx, other, true, 0, func(string) {}, func(string, ...any) {})
					rtest.OK(t, err)
					var competitor backend.Handle
					var competitorLock []byte
					rtest.OK(t, shared.List(ctx, backend.LockFile, func(fi backend.FileInfo) error {
						competitor = backend.Handle{Type: backend.LockFile, Name: fi.Name}
						return nil
					}))
					rtest.OK(t, shared.Load(ctx, competitor, 0, 0, func(rd io.Reader) (err error) {
						competitorLock, err = io.ReadAll(rd)
						return err
					}))
					unlockOther()

					probe := &waitProbeBackend{Backend: shared, readDelay: sc.readDelay, failReads: sc.failReads, hangReads: sc.hangReads}
					repo, err := schedOpenRepository(ctx, probe)
					rtest.OK(t, err)

					synctest.Wait()
					if n, stacks := schedOtherGoroutines(); n != 0 {
						t.Fatalf("test defect: %d goroutine(s) before the command starts:\n%s", n, stacks)
					}
					probe.t0 = time.Now()

					// what happens beside the lock call
					var beside sync.WaitGroup
					after := func(d time.Duration, fn func()) {
						if d == 0 {
							return
						}
						beside.Add(1)
						go func() {
							defer beside.Done()
							// wait for the lock file, then d
							for {
								probe.mu.Lock()
								saved := len(probe.lockSaved) > 0
								probe.mu.Unlock()
								if saved {
									break
								}
								time.Sleep(100 * time.Microsecond)
							}
							time.Sleep(d)
							fn()
						}()
					}
					after(sc.cancelAt, cancel)
					after(sc.competitorAt, func() {
						rtest.OK(t, shared.Save(context.Background(), competitor, backend.NewByteReader(competitorLock, shared.Hasher())))
					})

					p := &schedProc{spec: &schedProcSpec{kind: kind}, run: &schedRun{spec: spec}}
					c := &schedCommand{p: p, repo: repo}
					work := c.waitWork()
					unlock, lockCtx, lockErr := repository.LockRepoWithWaitWork(ctx, repo, kind.exclusive(), schedRetryLock, func(string) {}, func(string, ...any) {},
						func(lockCtx context.Context) func(bool) { return work(lockCtx, repo) })
					returnedAt := probe.now()
					probe.mu.Lock()
					probe.returned = true
					saved := append([]time.Duration(nil), probe.lockSaved...)
					lists := append([]time.Duration(nil), probe.lockLists...)
					probe.mu.Unlock()

					if len(saved) == 0 {
						t.Fatal("no lock file was saved")
					}
					// every attempt that saved a lock file waited the full delay
					for i, at := range saved {
						end := returnedAt
						if i+1 < len(saved) {
							end = saved[i+1]
						}
						confirmation := time.Duration(-1)
						for _, l := range lists {
							if l >= at+schedSettle && l <= end {
								confirmation = l
								break
							}
						}
						if confirmation < 0 {
							t.Errorf("attempt %d: lock file saved at %v, but no listing of the lock files started between %v and %v (lock listings at %v)",
								i+1, at, at+schedSettle, end, lists)
						} else if confirmation != at+schedSettle {
							t.Errorf("attempt %d: the confirmation scan started %v after the lock file was saved, want exactly %v", i+1, confirmation-at, schedSettle)
						}
					}
					if returnedAt < saved[len(saved)-1]+schedSettle && lockErr == nil {
						t.Errorf("the lock call returned %v after the lock file was saved, before the settle delay of %v had passed", returnedAt-saved[len(saved)-1], schedSettle)
					}
					if returnedAt < saved[0]+schedSettle {
						t.Errorf("the lock call returned at %v, less than %v after the first lock file was saved at %v", returnedAt, schedSettle, saved[0])
					}

					if sc.wantLocked {
						if lockErr != nil {
							t.Fatalf("lock refused: %v", lockErr)
						}
						if returnedAt != saved[0]+schedSettle {
							t.Errorf("the lock call returned %v after the lock file was saved: the wait work postponed the confirmation (want %v)", returnedAt-saved[0], schedSettle)
						}
						if sc.cancelBeforeUse {
							cancel()
						}
						if sc.use {
							err := c.read(lockCtx)
							switch {
							case sc.cancelBeforeUse:
								// either outcome is fine; it must return
							case sc.failReads && err != nil:
								// a tree may report the failed read or read again
								t.Logf("the command reports the read failure of the wait: %v", err)
							case err != nil:
								t.Errorf("reading after a confirmed lock failed: %v", err)
							}
						}
						c.discard()
						unlock()
					} else if lockErr == nil {
						t.Errorf("the lock was granted")
						c.discard()
						unlock()
					} else {
						if sc.competitorAt != 0 && !repository.IsAlreadyLocked(lockErr) {
							t.Errorf("lock refused with %v, want an already-locked error", lockErr)
						}
						if c.backupLoad != nil || c.indexLoad != nil || c.checkLoad != nil {
							t.Errorf("a refused lock attempt left prepared state behind in the command")
						}
					}
					cancel()
					beside.Wait()

					// nothing is left running, and no lock file of ours remains
					synctest.Wait()
					if left, stacks := schedOtherGoroutines(); left != 0 {
						t.Errorf("%d goroutine(s) outlived the command:\n%s", left, stacks)
					}
					locks := 0
					rtest.OK(t, shared.List(context.Background(), backend.LockFile, func(fi backend.FileInfo) error {
						if fi.Name != competitor.Name {
							locks++
						}
						return nil
					}))
					if locks != 0 {
						t.Errorf("%d lock file(s) of the command remain", locks)
					}
				})
			})
		}
	}
}
