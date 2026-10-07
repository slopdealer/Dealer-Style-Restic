package main

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

type indexListErrorBackend struct {
	backend.Backend
	err error
}

func (be *indexListErrorBackend) List(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
	if typ == backend.IndexFile {
		return be.err
	}
	return be.Backend.List(ctx, typ, fn)
}

func (be *indexListErrorBackend) IsPermanentError(err error) bool {
	return err == be.err || be.Backend.IsPermanentError(err)
}

// firstLockListHiddenBackend hides the lock files from the first listing: a
// competing lock becomes visible only in the confirmation scan.
type firstLockListHiddenBackend struct {
	backend.Backend
	mu    sync.Mutex
	lists int
}

func (be *firstLockListHiddenBackend) List(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
	if typ == backend.LockFile {
		be.mu.Lock()
		be.lists++
		first := be.lists == 1
		be.mu.Unlock()
		if first {
			return nil
		}
	}
	return be.Backend.List(ctx, typ, fn)
}

// TestRefusedLockShowsNoWaitDiagnostics is the red team's test (seat 1 R3):
// the index listing of the wait fails, and the lock is then refused. Stock
// makes no such listing and prints the lock error only.
func TestRefusedLockShowsNoWaitDiagnostics(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	env.gopts.NoCache = true
	env.gopts.BackendTestHook = nil
	testRunInit(t, env.gopts)
	var competitor *repository.Repository
	rtest.OK(t, withTermStatus(t, env.gopts, func(ctx context.Context, opts global.Options) error {
		var err error
		competitor, err = global.OpenRepository(ctx, opts, restic.NewNoopPrinter())
		return err
	}))
	unlock, _, err := repository.LockRepo(context.Background(), competitor, true, 0, func(string) {}, func(string, ...any) {})
	rtest.OK(t, err)
	defer unlock()
	failure := fmt.Errorf("injected unconfirmed index listing failure")
	env.gopts.BackendInnerTestHook = func(be backend.Backend) (backend.Backend, error) {
		return &indexListErrorBackend{Backend: be, err: failure}, nil
	}
	env.gopts.BackendTestHook = func(be backend.Backend) (backend.Backend, error) {
		return &firstLockListHiddenBackend{Backend: be}, nil
	}
	run := func() (string, error) {
		_, stderr, err := withCaptureStdoutStderr(t, env.gopts, func(ctx context.Context, opts global.Options) error {
			return runRestore(ctx, RestoreOptions{Target: filepath.Join(env.base, "unused-restore")}, opts, opts.Term, []string{"latest"})
		})
		return stderr.String(), err
	}
	repository.TestSetLockTimeout(t, 200*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)
	gotStderr, gotError := run()
	rtest.Assert(t, repository.IsAlreadyLocked(gotError), "the competing lock must be refused: %v", gotError)
	repository.TestSetLockTimeout(t, 0)
	wantStderr, wantError := run()
	rtest.Assert(t, repository.IsAlreadyLocked(wantError), "the control must refuse the competing lock: %v", wantError)
	if gotStderr != wantStderr {
		t.Fatalf("a read made before the lock was confirmed was reported although the lock was refused: stderr %q, without wait work %q", gotStderr, wantStderr)
	}
}

// flakyLoadBackend fails the first load of every index file and of nothing
// else, with an error that is retried.
type flakyLoadBackend struct {
	backend.Backend
	mu     sync.Mutex
	failed map[string]bool
}

func (be *flakyLoadBackend) Load(ctx context.Context, h backend.Handle, n int, offset int64, fn func(io.Reader) error) error {
	if h.Type == backend.IndexFile {
		be.mu.Lock()
		first := !be.failed[h.Name]
		be.failed[h.Name] = true
		be.mu.Unlock()
		if first {
			return fmt.Errorf("injected transient failure")
		}
	}
	return be.Backend.Load(ctx, h, n, offset, fn)
}

type reportLog struct {
	mu    sync.Mutex
	lines []string
}

func (l *reportLog) add(format string, args ...any) {
	l.mu.Lock()
	l.lines = append(l.lines, fmt.Sprintf(format, args...))
	l.mu.Unlock()
}

func (l *reportLog) text() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Join(l.lines, "\n")
}

// The reports of the retries made during the wait are shown when the index of
// the wait is used, at the point where the command loads the index, and never
// when it is not used.
func TestWaitDiagnosticsFollowTheUseOfTheReads(t *testing.T) {
	retry.TestFastRetries(t)
	for _, test := range []string{"kept", "lock refused", "discarded after confirmation"} {
		t.Run(test, func(t *testing.T) {
			ctx := context.Background()
			writer, shared := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
			_, err := schedStoreSnapshot(ctx, writer, 46, []int{0}, time.Now())
			rtest.OK(t, err)

			log := &reportLog{}
			be := retry.New(&flakyLoadBackend{Backend: shared, failed: map[string]bool{}}, time.Minute,
				func(msg string, err error, d time.Duration) { log.add("%v failed: %v", msg, err) },
				func(msg string, retries int) { log.add("%v successful after %d retries", msg, retries) })
			repo, err := schedOpenRepository(ctx, be)
			rtest.OK(t, err)

			load := startRepositoryIndexLoadAfterSnapshots(ctx, repo, true)
			defer load.discard()
			deadline := time.After(10 * time.Second)
			for len(load.result) == 0 {
				select {
				case <-deadline:
					t.Fatal("the index load of the wait did not finish")
				default:
					time.Sleep(time.Millisecond)
				}
			}
			if got := log.text(); got != "" {
				t.Fatalf("reported before the lock was confirmed:\n%s", got)
			}

			switch test {
			case "lock refused":
				load.settle(false)
				if got := log.text(); got != "" {
					t.Fatalf("reported although the lock was refused:\n%s", got)
				}
			case "kept":
				load.settle(true)
				_ = load.snapshotLister(repo)
				if got := log.text(); got != "" {
					t.Fatalf("reported before the command loads the index:\n%s", got)
				}
				rtest.OK(t, load.consume(restic.NoopTerminalCounterFactory))
				got := log.text()
				if !strings.Contains(got, "injected transient failure") || !strings.Contains(got, "successful after 1 retries") {
					t.Fatalf("the retries of the index that is used were not reported:\n%s", got)
				}
			case "discarded after confirmation":
				// another index file appears to be gone: the reads are discarded
				var target backend.Handle
				rtest.OK(t, shared.List(ctx, backend.IndexFile, func(fi backend.FileInfo) error {
					target = backend.Handle{Type: backend.IndexFile, Name: fi.Name}
					return nil
				}))
				var raw []byte
				rtest.OK(t, shared.Load(ctx, target, 0, 0, func(rd io.Reader) (err error) {
					raw, err = io.ReadAll(rd)
					return err
				}))
				rtest.OK(t, shared.Remove(ctx, target))
				other := backend.Handle{Type: backend.IndexFile, Name: restic.Hash(raw[:len(raw)-1]).String()}
				rtest.OK(t, shared.Save(ctx, other, backend.NewByteReader(raw[:len(raw)-1], shared.Hasher())))

				load.settle(true)
				_ = load.snapshotLister(repo)
				err := load.consume(restic.NoopTerminalCounterFactory)
				if !load.rereadAfterConfirmation() {
					t.Fatalf("the reads of the wait were not discarded (error %v)", err)
				}
				// what is reported now belongs to the load after confirmation,
				// which meets the new file for the first time
				for _, line := range strings.Split(log.text(), "\n") {
					if line != "" && !strings.Contains(line, other.Name[:10]) && !strings.Contains(line, other.Name) {
						t.Fatalf("a report of the discarded reads was shown: %q", line)
					}
				}
			}
		})
	}
}
