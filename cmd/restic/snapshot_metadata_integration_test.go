package main

import (
	"context"
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

func (b *metadataCommandGate) Unwrap() backend.Backend { return b.Backend }

type metadataCommandGate struct {
	*waitWorkGateBackend
	snapshotStarted chan struct{}
	snapshotStart   sync.Once
}

func (b *metadataCommandGate) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	err := b.Backend.Load(ctx, h, length, offset, fn)
	if h.Type == backend.SnapshotFile {
		b.snapshotStart.Do(func() { close(b.snapshotStarted) })
	}
	return err
}

type metadataSnapshotObserver struct {
	backend.Backend
	gate *metadataCommandGate
}

func (b *metadataSnapshotObserver) Load(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
	err := b.Backend.Load(ctx, h, n, off, fn)
	if h.Type == backend.SnapshotFile {
		b.gate.snapshotStart.Do(func() { close(b.gate.snapshotStarted) })
	}
	return err
}

func TestLsFailedLockConfirmationDiscardsPreparedSnapshotBeforeUnlock(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	env.gopts.NoCache = true
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

	gate := &metadataCommandGate{waitWorkGateBackend: newWaitWorkGateBackend(nil), snapshotStarted: make(chan struct{})}
	gate.competitorLock = competitorID.String()
	env.gopts.BackendInnerTestHook = func(be backend.Backend) (backend.Backend, error) {
		return &metadataSnapshotObserver{Backend: be, gate: gate}, nil
	}
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
			commandDone <- runLs(runCtx, LsOptions{}, gopts, []string{snapshotIDs[0].String()}, gopts.Term)
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

		select {
		case <-gate.snapshotStarted:
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
	if strings.Contains(stdout.String()+stderr.String(), "index files loaded") || strings.Contains(stdout.String()+stderr.String(), "snapshot ") {
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
