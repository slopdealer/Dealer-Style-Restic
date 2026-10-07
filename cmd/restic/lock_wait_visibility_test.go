package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// indexVisibilityBackend models the visibility window of a completed
// concurrent backup: its snapshot and packs are visible, but its new index
// file is absent from early listings. The index listing catches up while the
// command reads the content of the snapshot it selected. Stock lists the
// index files after that read and sees the new one.
//
// With validated set, the snapshot read waits for the second index listing,
// so that a build which lists the index files before the snapshot was read
// gets to do so; when none comes for a while, the read goes on.
type indexVisibilityBackend struct {
	backend.Backend
	mu            sync.Mutex
	hidden        restic.IDSet
	visible       bool
	indexListings int
	validated     chan struct{}
	validatedOnce sync.Once
}

func (be *indexVisibilityBackend) List(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
	be.mu.Lock()
	visible := be.visible
	if typ == backend.IndexFile {
		be.indexListings++
	}
	secondIndexList := typ == backend.IndexFile && be.indexListings == 2
	be.mu.Unlock()
	err := be.Backend.List(ctx, typ, func(fi backend.FileInfo) error {
		if typ == backend.IndexFile && !visible {
			id, err := restic.ParseID(fi.Name)
			if err == nil && be.hidden.Has(id) {
				return nil
			}
		}
		return fn(fi)
	})
	if secondIndexList && be.validated != nil {
		be.validatedOnce.Do(func() { close(be.validated) })
	}
	return err
}

func (be *indexVisibilityBackend) Load(ctx context.Context, h backend.Handle, n int, offset int64, fn func(io.Reader) error) error {
	if h.Type == backend.SnapshotFile && be.validated != nil {
		select {
		case <-be.validated:
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
			// no index listing comes before the snapshot is read
		}
	}
	err := be.Backend.Load(ctx, h, n, offset, fn)
	if h.Type == backend.SnapshotFile && err == nil {
		be.mu.Lock()
		be.visible = true
		be.mu.Unlock()
	}
	return err
}

// TestRestoreListsIndexAfterSnapshotRead is the red team's test (seat 1 R2)
// with the restore command and the production settle delay: the index file
// of the newest snapshot becomes visible while restore reads that snapshot.
// Stock lists the index files afterwards and restores; so must this build.
func TestRestoreListsIndexAfterSnapshotRead(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	env.gopts.NoCache = true
	env.gopts.BackendTestHook = nil
	testRunInit(t, env.gopts)
	hidden := restic.NewIDSet()
	rtest.OK(t, withTermStatus(t, env.gopts, func(ctx context.Context, opts global.Options) error {
		writer, err := global.OpenRepository(ctx, opts, restic.NewNoopPrinter())
		if err != nil {
			return err
		}
		_, err = schedStoreSnapshot(ctx, writer, 45, []int{0}, time.Now().Add(-time.Hour))
		if err != nil {
			return err
		}
		previous := restic.NewIDSet()
		if err := writer.List(ctx, restic.IndexFile, func(id restic.ID, _ int64) error {
			previous.Insert(id)
			return nil
		}); err != nil {
			return err
		}
		_, err = schedStoreSnapshot(ctx, writer, 45, []int{1}, time.Now())
		if err != nil {
			return err
		}
		return writer.List(ctx, restic.IndexFile, func(id restic.ID, _ int64) error {
			if !previous.Has(id) {
				hidden.Insert(id)
			}
			return nil
		})
	}))
	if len(hidden) == 0 {
		t.Fatal("fixture did not add an index")
	}
	env.gopts.BackendTestHook = func(be backend.Backend) (backend.Backend, error) {
		return &indexVisibilityBackend{Backend: be, hidden: hidden, validated: make(chan struct{})}, nil
	}
	repository.TestSetLockTimeout(t, 200*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)
	target := filepath.Join(env.base, "restore")
	restoreError := testRunRestoreAssumeFailure(t, "latest", RestoreOptions{Target: target}, env.gopts)

	// the same visibility without a settle delay, as the control
	env.gopts.BackendTestHook = func(be backend.Backend) (backend.Backend, error) {
		return &indexVisibilityBackend{Backend: be, hidden: hidden}, nil
	}
	repository.TestSetLockTimeout(t, 0)
	controlTarget := filepath.Join(env.base, "control-restore")
	rtest.OK(t, testRunRestoreAssumeFailure(t, "latest", RestoreOptions{Target: controlTarget}, env.gopts))
	want, err := os.ReadFile(filepath.Join(controlTarget, "file-00"))
	rtest.OK(t, err)
	rtest.Equals(t, schedBlob(45, 1), want)

	if restoreError != nil {
		t.Fatalf("restore failed although the snapshot and all its data are present: %v", restoreError)
	}
	got, err := os.ReadFile(filepath.Join(target, "file-00"))
	rtest.OK(t, err)
	rtest.Equals(t, want, got)
}

// TestLockWaitIndexListedAfterSnapshotRead is the same schedule on the wait
// work itself (seat 1 R2, lifecycle form).
func TestLockWaitIndexListedAfterSnapshotRead(t *testing.T) {
	ctx := context.Background()
	writer, shared := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	_, err := schedStoreSnapshot(ctx, writer, 43, []int{0}, time.Now().Add(-time.Hour))
	rtest.OK(t, err)
	previousIndexes := restic.NewIDSet()
	rtest.OK(t, writer.List(ctx, restic.IndexFile, func(id restic.ID, _ int64) error {
		previousIndexes.Insert(id)
		return nil
	}))
	_, err = schedStoreSnapshot(ctx, writer, 43, []int{1}, time.Now())
	rtest.OK(t, err)
	newIndexes := restic.NewIDSet()
	rtest.OK(t, writer.List(ctx, restic.IndexFile, func(id restic.ID, _ int64) error {
		if !previousIndexes.Has(id) {
			newIndexes.Insert(id)
		}
		return nil
	}))
	if len(newIndexes) == 0 {
		t.Fatal("new snapshot did not add an index file")
	}

	be := &indexVisibilityBackend{Backend: shared, hidden: newIndexes, validated: make(chan struct{})}
	repo, err := schedOpenRepository(ctx, be)
	rtest.OK(t, err)
	load := startRepositoryIndexLoadAfterSnapshots(ctx, repo, true)
	defer load.discard()
	// the wait is over when the index of the wait has been listed and loaded
	deadline := time.After(5 * time.Second)
	for len(load.result) == 0 {
		select {
		case <-deadline:
			t.Fatal("the index load of the wait did not finish")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	load.settle(true)
	sn, _, err := (&data.SnapshotFilter{}).FindLatest(ctx, load.snapshotLister(repo), repo, "latest")
	rtest.OK(t, err)
	rtest.OK(t, load.consume(restic.NoopTerminalCounterFactory))
	_, treeError := data.LoadTree(ctx, repo, *sn.Tree)

	fresh, err := schedOpenRepository(ctx, &indexVisibilityBackend{Backend: shared, hidden: newIndexes})
	rtest.OK(t, err)
	// stock's order in restore: select and read the snapshot, then list and
	// load the index, then read the selected tree
	freshSnapshot, _, err := (&data.SnapshotFilter{}).FindLatest(ctx, fresh, fresh, "latest")
	rtest.OK(t, err)
	rtest.OK(t, fresh.LoadIndex(ctx, restic.NoopTerminalCounterFactory))
	_, err = data.LoadTree(ctx, fresh, *freshSnapshot.Tree)
	rtest.OK(t, err)
	rtest.Equals(t, *freshSnapshot.ID(), *sn.ID())
	if treeError != nil {
		t.Fatalf("the selected snapshot cannot be read on a healthy repository: %v (index files %v, stock's order loads %v)",
			treeError, repo.LoadedIndexIDs(), fresh.LoadedIndexIDs())
	}
	rtest.Equals(t, fresh.LoadedIndexIDs(), repo.LoadedIndexIDs())
}
