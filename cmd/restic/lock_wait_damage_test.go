package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// damageAtConfirmationBackend damages the repository when the lock code lists
// the lock files for the second time, that is, at the end of the settle
// delay. It waits a moment for an index file to have been read first, so that
// a command which reads the index during the delay has read it undamaged.
type damageAtConfirmationBackend struct {
	backend.Backend
	mu        sync.Mutex
	lockLists int
	indexRead chan struct{}
	readOnce  sync.Once
	damage    func() error
}

func (be *damageAtConfirmationBackend) Load(ctx context.Context, h backend.Handle, n int, offset int64, fn func(io.Reader) error) error {
	err := be.Backend.Load(ctx, h, n, offset, fn)
	if h.Type == backend.IndexFile && err == nil {
		be.readOnce.Do(func() { close(be.indexRead) })
	}
	return err
}

func (be *damageAtConfirmationBackend) List(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
	if typ == backend.LockFile {
		be.mu.Lock()
		be.lockLists++
		second := be.lockLists == 2
		be.mu.Unlock()
		if second {
			select {
			case <-be.indexRead:
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(500 * time.Millisecond):
				// nothing reads the index during the delay
			}
			if err := be.damage(); err != nil {
				return err
			}
		}
	}
	return be.Backend.List(ctx, typ, fn)
}

// TestCheckReportsIndexDamagedBeforeLockConfirmation is the red team's test
// (seat 1 R1, seat 2 finding 2) at the level of the command, with the
// production settle delay: an index file that is cut short, or changed in
// place, before the exclusive lock is confirmed must be reported by check, as
// it is by stock, which reads the index after confirmation.
func TestCheckReportsIndexDamagedBeforeLockConfirmation(t *testing.T) {
	for _, cache := range []bool{false, true} {
		for _, damage := range []string{"truncate", "same-size bit flip"} {
			t.Run(fmt.Sprintf("cache-%v/%s", cache, damage), func(t *testing.T) {
				checkReportsIndexDamagedBeforeLockConfirmation(t, cache, damage)
			})
		}
	}
}

func checkReportsIndexDamagedBeforeLockConfirmation(t *testing.T, cache bool, damage string) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()
	env.gopts.NoCache = true
	env.gopts.BackendTestHook = nil
	testRunInit(t, env.gopts)
	rtest.OK(t, withTermStatus(t, env.gopts, func(ctx context.Context, opts global.Options) error {
		writer, err := global.OpenRepository(ctx, opts, restic.NewNoopPrinter())
		if err != nil {
			return err
		}
		_, err = schedStoreSnapshot(ctx, writer, 44, []int{0}, time.Now())
		return err
	}))
	entries, err := os.ReadDir(filepath.Join(env.repo, "index"))
	rtest.OK(t, err)
	var indexPath string
	for _, entry := range entries {
		if _, err := restic.ParseID(entry.Name()); err == nil {
			indexPath = filepath.Join(env.repo, "index", entry.Name())
		}
	}
	if indexPath == "" {
		t.Fatal("backup wrote no index file")
	}
	gate := &damageAtConfirmationBackend{indexRead: make(chan struct{}), damage: func() error {
		if err := os.Chmod(indexPath, 0o600); err != nil {
			return err
		}
		if damage == "truncate" {
			return os.Truncate(indexPath, 1)
		}
		raw, err := os.ReadFile(indexPath)
		if err != nil {
			return err
		}
		raw[len(raw)/2] ^= 1
		return os.WriteFile(indexPath, raw, 0o600)
	}}
	env.gopts.BackendTestHook = func(be backend.Backend) (backend.Backend, error) {
		gate.Backend = be
		return gate, nil
	}
	env.gopts.Quiet = false
	env.gopts.Verbosity = 1
	env.gopts.NoCache = !cache
	repository.TestSetLockTimeout(t, 200*time.Millisecond)
	defer repository.TestSetLockTimeout(t, 0)
	stdout, stderr, checkError := testRunCheckOutput(t, env.gopts, false)

	// the same command once more, on the repository as it is now
	env.gopts.BackendTestHook = nil
	repository.TestSetLockTimeout(t, 0)
	_, _, freshError := testRunCheckOutput(t, env.gopts, false)
	if freshError == nil {
		t.Fatal("control accepted the damaged index")
	}
	if checkError == nil {
		t.Fatalf("check --read-data returned success for an index damaged before lock confirmation; stdout=%q, stderr=%q", stdout, stderr)
	}
	if checkError.Error() != freshError.Error() {
		t.Fatalf("check returned %q, a check after the damage returns %q", checkError, freshError)
	}
}

// For the commands that keep the index read during the settle delay, an index
// file that is listed with another size once the lock is confirmed is not the
// file that was read: everything is read again, and the command fails as a
// load after confirmation fails.
func TestLockWaitReadsRejectResizedIndexFile(t *testing.T) {
	for _, version := range []uint{1, 2} {
		t.Run(fmt.Sprintf("version-%d", version), func(t *testing.T) {
			ctx := context.Background()
			writer, shared := repository.TestRepositoryWithBackend(t, nil, version, repository.Options{})
			_, err := schedStoreSnapshot(ctx, writer, 42, []int{0}, time.Now())
			rtest.OK(t, err)
			repo, err := schedOpenRepository(ctx, shared)
			rtest.OK(t, err)

			load := startRepositoryIndexLoadAfterSnapshots(ctx, repo, true)
			defer load.discard()
			deadline := time.After(5 * time.Second)
			for len(load.result) == 0 {
				select {
				case <-deadline:
					t.Fatal("the index load of the wait did not finish")
				default:
					time.Sleep(time.Millisecond)
				}
			}

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
			rtest.OK(t, shared.Save(ctx, target, backend.NewByteReader(raw[:len(raw)/2], shared.Hasher())))

			load.settle(true)
			_ = load.snapshotLister(repo)
			got := load.consume(restic.NoopTerminalCounterFactory)

			fresh, err := schedOpenRepository(ctx, shared)
			rtest.OK(t, err)
			want := fresh.LoadIndex(ctx, restic.NoopTerminalCounterFactory)
			if want == nil {
				t.Fatal("control loaded the index file that was cut short")
			}
			if got == nil || got.Error() != want.Error() {
				t.Fatalf("the command gets %v, a load after confirmation gets %v", got, want)
			}
			if !load.rereadAfterConfirmation() {
				t.Fatal("the index read during the wait was not discarded")
			}
		})
	}
}

type fixedSnapshotList map[restic.ID]int64

func (l fixedSnapshotList) List(_ context.Context, t restic.FileType, fn func(restic.ID, int64) error) error {
	if t != restic.SnapshotFile {
		return nil
	}
	for id, size := range l {
		if err := fn(id, size); err != nil {
			return err
		}
	}
	return nil
}

type namedLoader struct {
	restic.LoaderUnpacked
	name string
}

// The snapshot file ls reads during the settle delay is served only when the
// listing taken after confirmation lists it with the size it was read with.
func TestPreparedSnapshotKeptOnlyWithListedSize(t *testing.T) {
	id := restic.Hash([]byte("snapshot"))
	other := restic.Hash([]byte("other"))
	prepared := &namedLoader{name: "prepared"}
	fallback := &namedLoader{name: "fallback"}
	for _, test := range []struct {
		name   string
		listed fixedSnapshotList
		want   *namedLoader
	}{
		{"same size", fixedSnapshotList{id: 100, other: 7}, prepared},
		{"shorter", fixedSnapshotList{id: 99}, fallback},
		{"longer", fixedSnapshotList{id: 101}, fallback},
		{"gone", fixedSnapshotList{other: 100}, fallback},
	} {
		done := make(chan struct{})
		close(done)
		p := &snapshotMetadataLoad{cancel: func() {}, done: done, loader: prepared, id: id, size: 100, prepared: true}
		if got := p.get(context.Background(), fallback, test.listed); got != restic.LoaderUnpacked(test.want) {
			t.Errorf("%s: got the %s loader, want the %s loader", test.name, got.(*namedLoader).name, test.want.name)
		}
	}
}
