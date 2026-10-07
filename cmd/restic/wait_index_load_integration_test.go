package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
	"github.com/restic/restic/internal/ui"
)

// waitWorkGateBackend holds the speculative index listing until its caller is
// canceled. The optional lock-list gate lets a test control the second lock
// confirmation independently of the index worker.
type waitWorkGateBackend struct {
	backend.Backend

	indexStarted  chan struct{}
	indexCanceled chan struct{}
	releaseIndex  chan struct{}
	indexStart    sync.Once
	indexCancel   sync.Once
	releaseOnce   sync.Once

	competitorLock string
	lockLists      int
	lockMu         sync.Mutex
	secondLockList chan struct{}
	continueLock   chan struct{}
	lockStart      sync.Once
	continueOnce   sync.Once
}

func newWaitWorkGateBackend(be backend.Backend) *waitWorkGateBackend {
	return &waitWorkGateBackend{
		Backend:        be,
		indexStarted:   make(chan struct{}),
		indexCanceled:  make(chan struct{}),
		releaseIndex:   make(chan struct{}),
		secondLockList: make(chan struct{}),
		continueLock:   make(chan struct{}),
	}
}

func (be *waitWorkGateBackend) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	if t == backend.IndexFile {
		be.indexStart.Do(func() { close(be.indexStarted) })
		<-ctx.Done()
		be.indexCancel.Do(func() { close(be.indexCanceled) })
		<-be.releaseIndex
		return ctx.Err()
	}

	if t == backend.LockFile {
		be.lockMu.Lock()
		be.lockLists++
		lockList := be.lockLists
		be.lockMu.Unlock()

		if lockList == 1 && be.competitorLock != "" {
			// Model a lock that becomes visible between the initial check and
			// confirmation while retaining a real, valid encrypted lock file.
			return be.Backend.List(ctx, t, func(fi backend.FileInfo) error {
				if fi.Name == be.competitorLock {
					return nil
				}
				return fn(fi)
			})
		}

		if lockList == 2 {
			be.lockStart.Do(func() { close(be.secondLockList) })
			select {
			case <-be.continueLock:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return be.Backend.List(ctx, t, fn)
}

func (be *waitWorkGateBackend) releaseHeldIndex() {
	be.releaseOnce.Do(func() { close(be.releaseIndex) })
}

func (be *waitWorkGateBackend) continueConfirmation() {
	be.continueOnce.Do(func() { close(be.continueLock) })
}

func (be *waitWorkGateBackend) unblock() {
	be.continueConfirmation()
	be.releaseHeldIndex()
}

func TestRestoreFailedLockConfirmationDrainsPreparedIndexBeforeUnlock(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	testSetupBackupData(t, env)
	testRunBackup(t, "", []string{env.testdata}, BackupOptions{}, env.gopts)
	snapshotIDs := testListSnapshots(t, env.gopts, 1)
	// testRunInit adds a junk lock filename for listing tests. This test counts
	// the two real lock owners, so remove that unrelated fixture entry.
	if err := os.Remove(filepath.Join(env.repo, "locks", "tmp12345")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	before := hashRepositoryFiles(t, env.repo)

	repository.TestSetLockTimeout(t, 50*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)

	// Hold a valid exclusive lock through an independent repository handle.
	// The test backend hides it from the first check, then exposes it to the
	// second check after the candidate has created its own lock and started
	// speculative index loading.
	competitorOptions := env.gopts
	competitorOptions.BackendTestHook = nil
	competitorOptions.NoCache = true
	// OpenRepository is normally called through withTermStatus. The independent
	// competitor also needs a terminal even with a supplied password.
	competitorOptions.Term = &ui.MockTerminal{}
	competitorRepo, err := global.OpenRepository(context.Background(), competitorOptions, restic.NewNoopPrinter())
	rtest.OK(t, err)
	unlockCompetitor, _, err := repository.LockRepo(context.Background(), competitorRepo, true, 0, func(string) {}, func(string, ...any) {})
	rtest.OK(t, err)
	defer func() {
		unlockCompetitor()
		rtest.OK(t, competitorRepo.Close())
	}()
	var competitorID restic.ID
	err = competitorRepo.List(context.Background(), restic.LockFile, func(id restic.ID, _ int64) error {
		competitorID = id
		return nil
	})
	rtest.OK(t, err)
	rtest.Assert(t, !competitorID.IsNull(), "expected the competing lock to be present")

	gate := newWaitWorkGateBackend(nil)
	gate.competitorLock = competitorID.String()
	env.gopts.BackendTestHook = func(be backend.Backend) (backend.Backend, error) {
		gate.Backend = be
		return gate, nil
	}
	defer gate.unblock()

	target := filepath.Join(env.base, "restore-competing-lock")
	stdout, stderr, err := withCaptureStdoutStderr(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
		runCtx, cancelCommand := context.WithCancel(ctx)
		defer cancelCommand()
		commandDone := make(chan error, 1)
		go func() {
			commandDone <- runRestore(runCtx, RestoreOptions{Target: target}, gopts, gopts.Term, []string{snapshotIDs[0].String()})
		}()
		fail := func(err error) error {
			cancelCommand()
			gate.unblock()
			select {
			case <-commandDone:
			case <-time.After(5 * time.Second):
			}
			return err
		}

		select {
		case <-gate.indexStarted:
		case <-time.After(5 * time.Second):
			return fail(context.DeadlineExceeded)
		}
		select {
		case <-gate.secondLockList:
		case <-time.After(5 * time.Second):
			return fail(context.DeadlineExceeded)
		}

		gate.continueConfirmation()
		select {
		case <-gate.indexCanceled:
		case <-time.After(5 * time.Second):
			return fail(context.DeadlineExceeded)
		}
		select {
		case commandErr := <-commandDone:
			return commandErr
		default:
		}

		locks, readErr := os.ReadDir(filepath.Join(env.repo, "locks"))
		if readErr != nil {
			return readErr
		}
		if len(locks) != 2 {
			return fmt.Errorf("lock files during index drain = %d, want candidate and competitor locks", len(locks))
		}
		if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
			return fmt.Errorf("restore target changed before lock confirmation: stat error = %v", statErr)
		}

		gate.releaseHeldIndex()
		return <-commandDone
	})
	if !repository.IsAlreadyLocked(err) {
		t.Fatalf("restore error = %v, want competing-lock error", err)
	}
	if strings.Contains(stdout.String()+stderr.String(), "index files loaded") || strings.Contains(stdout.String()+stderr.String(), "restoring ") {
		t.Fatalf("unconfirmed restore exposed progress or started restore output: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("restore target after failed confirmation: stat error = %v, want not-exist", statErr)
	}
	locks, err := os.ReadDir(filepath.Join(env.repo, "locks"))
	rtest.OK(t, err)
	if len(locks) != 1 || locks[0].Name() != competitorID.String() {
		t.Fatalf("locks after failed confirmation = %v, want only competitor %s", lockNames(locks), competitorID)
	}
	if got := hashRepositoryFiles(t, env.repo); !sameRepositoryFiles(before, got) {
		t.Fatal("repository objects changed during failed lock confirmation")
	}
}

func TestRestoreSnapshotLookupErrorDrainsPreparedIndexBeforeUnlock(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	testSetupBackupData(t, env)
	testRunBackup(t, "", []string{env.testdata}, BackupOptions{}, env.gopts)
	if err := os.Remove(filepath.Join(env.repo, "locks", "tmp12345")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	before := hashRepositoryFiles(t, env.repo)

	repository.TestSetLockTimeout(t, 50*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)
	gate := newWaitWorkGateBackend(nil)
	// This case gates index retirement, not successful lock confirmation.
	gate.continueConfirmation()
	env.gopts.BackendTestHook = func(be backend.Backend) (backend.Backend, error) {
		gate.Backend = be
		return gate, nil
	}
	defer gate.unblock()

	target := filepath.Join(env.base, "restore-missing-snapshot")
	stdout, stderr, err := withCaptureStdoutStderr(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
		runCtx, cancelCommand := context.WithCancel(ctx)
		defer cancelCommand()
		commandDone := make(chan error, 1)
		go func() {
			commandDone <- runRestore(runCtx, RestoreOptions{Target: target}, gopts, gopts.Term, []string{strings.Repeat("f", 64)})
		}()
		fail := func(err error) error {
			cancelCommand()
			gate.unblock()
			select {
			case <-commandDone:
			case <-time.After(5 * time.Second):
			}
			return err
		}

		select {
		case <-gate.indexStarted:
		case <-time.After(5 * time.Second):
			return fail(context.DeadlineExceeded)
		}
		select {
		case <-gate.indexCanceled:
		case <-commandDone:
			gate.releaseHeldIndex()
			return errors.New("restore returned before its prepared index observed cancellation")
		case <-time.After(5 * time.Second):
			return fail(context.DeadlineExceeded)
		}
		select {
		case commandErr := <-commandDone:
			return commandErr
		default:
		}

		locks, readErr := os.ReadDir(filepath.Join(env.repo, "locks"))
		if readErr != nil {
			return readErr
		}
		if len(locks) != 1 {
			return fmt.Errorf("lock files during index drain = %d, want the command lock", len(locks))
		}
		if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
			return fmt.Errorf("restore target changed before snapshot lookup succeeded: stat error = %v", statErr)
		}

		gate.releaseHeldIndex()
		return <-commandDone
	})
	if err == nil || repository.IsAlreadyLocked(err) || !strings.Contains(err.Error(), "failed to find snapshot") {
		t.Fatalf("restore error = %v, want missing-snapshot lookup error", err)
	}
	if strings.Contains(stdout.String()+stderr.String(), "index files loaded") || strings.Contains(stdout.String()+stderr.String(), "restoring ") {
		t.Fatalf("failed restore exposed prepared-index progress: stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if _, statErr := os.Stat(target); !os.IsNotExist(statErr) {
		t.Fatalf("restore target after missing snapshot: stat error = %v, want not-exist", statErr)
	}
	locks, err := os.ReadDir(filepath.Join(env.repo, "locks"))
	rtest.OK(t, err)
	if len(locks) != 0 {
		t.Fatalf("locks after missing-snapshot failure = %v, want none", lockNames(locks))
	}
	if got := hashRepositoryFiles(t, env.repo); !sameRepositoryFiles(before, got) {
		t.Fatal("repository objects changed while the missing-snapshot path discarded prepared index state")
	}
}

func hashRepositoryFiles(t *testing.T, root string) map[string][sha256.Size]byte {
	t.Helper()
	files := make(map[string][sha256.Size]byte)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if rel == "locks" || strings.HasPrefix(rel, "locks"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			return nil
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		h := sha256.New()
		_, copyErr := io.Copy(h, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		var sum [sha256.Size]byte
		copy(sum[:], h.Sum(nil))
		files[rel] = sum
		return nil
	})
	rtest.OK(t, err)
	return files
}

func sameRepositoryFiles(a, b map[string][sha256.Size]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for path, sum := range a {
		if b[path] != sum {
			return false
		}
	}
	return true
}

func lockNames(entries []os.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}
