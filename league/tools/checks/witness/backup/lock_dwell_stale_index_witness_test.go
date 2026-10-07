package repository

// WITNESS (review of S019-merge, area backup / start-up): index loaded DURING the lock
// settle wait and used AFTER it (cmd/restic/cmd_backup.go:541-556 and :598-602,
// cmd/restic/backup_index_load.go:29-45, internal/repository/lock_file.go:136-158).
//
// Stock order for `restic backup`:  create lock, wait 200 ms, check for competing locks,
// THEN list snapshots and load the index. Whatever an exclusive-lock holder (prune,
// repair index, forget) did before that check is therefore always visible to the backup.
//
// Candidate order: create lock, START listing snapshots and loading the index, wait 200 ms,
// check for competing locks, KEEP the index that was read during the wait.
//
// This test forces the one schedule in which that matters. "B" is a backup, "P" is a
// prune. Nothing in the schedule is forbidden by restic's lock protocol:
//   1. B checks for locks (none) and starts saving its own lock file. That save is slow
//      (slow link, stalled fsync): the file is not visible yet.
//   2. P checks for locks (none), creates its exclusive lock, waits, checks again: B's lock
//      is still not visible, so P is confirmed and starts working.
//   3. B's lock file lands. B starts its settle wait; the candidate loads the index now.
//   4. P finishes: removes the old index file and the pack it considers garbage, unlocks.
//   5. B's settle wait ends, B sees no competing lock, the lock is CONFIRMED.
// With stock B now loads the index: blob X is gone, B uploads it again. With the candidate
// B keeps the index from step 3: blob X is "known", is NOT uploaded, and the new snapshot
// would reference a blob that no longer exists (silent data loss in that snapshot).
//
// The test FAILS on the candidate (stale blob reported known after a confirmed lock).
// It proves the schedule is reachable, not how likely it is: it needs the backup's lock
// file to become visible later than the peer's 200 ms settle, and the peer to finish
// inside the backup's own 200 ms wait. Deciding experiment with real processes: see
// REVIEW-backup.md, NEEDS-RUN.
//
// OS: any. Run from the candidate tree:
//   go test -run 'TestWitnessLockDwellStaleIndex' -count=1 -p 2 ./internal/repository

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/restic"
)

// slowLockSaveBackend delays the moment the caller's lock file becomes visible.
type slowLockSaveBackend struct {
	backend.Backend
	once        sync.Once
	saveStarted chan struct{}
	release     chan struct{}
}

func (be *slowLockSaveBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if h.Type == backend.LockFile {
		be.once.Do(func() { close(be.saveStarted) })
		<-be.release
	}
	return be.Backend.Save(ctx, h, rd)
}

func TestWitnessLockDwellStaleIndex(t *testing.T) {
	ctx := context.Background()
	shared := mem.New()
	TestSetLockTimeout(t, 400*time.Millisecond) // the settle wait, shortened only to keep the test quick

	// A repository holding one data blob X in one pack, described by one index file.
	setup, _ := TestRepositoryWithBackend(t, shared, 0, Options{})
	blobX := []byte("contents of a file that an interrupted backup already uploaded")
	var idX restic.ID
	if err := setup.WithBlobUploader(ctx, func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		var err error
		idX, _, _, err = up.SaveBlob(ctx, restic.DataBlob, blobX, restic.ID{}, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var indexFiles, packFiles []backend.Handle
	_ = shared.List(ctx, backend.IndexFile, func(fi backend.FileInfo) error {
		indexFiles = append(indexFiles, backend.Handle{Type: backend.IndexFile, Name: fi.Name})
		return nil
	})
	_ = shared.List(ctx, backend.PackFile, func(fi backend.FileInfo) error {
		packFiles = append(packFiles, backend.Handle{Type: backend.PackFile, Name: fi.Name})
		return nil
	})
	if len(indexFiles) != 1 || len(packFiles) != 1 {
		t.Fatalf("setup: want 1 index and 1 pack, got %d and %d", len(indexFiles), len(packFiles))
	}

	slow := &slowLockSaveBackend{Backend: shared, saveStarted: make(chan struct{}), release: make(chan struct{})}
	repoB := TestOpenBackend(t, slow)   // the backup process
	repoP := TestOpenBackend(t, shared) // the prune process

	// B: lock with the candidate's wait work (what runBackup passes: load the index).
	indexLoaded := make(chan error, 1)
	confirmed := make(chan bool, 1)
	type lockResult struct {
		unlock func()
		err    error
	}
	bLocked := make(chan lockResult, 1)
	go func() {
		unlock, _, err := LockRepoWithWaitWork(ctx, repoB, false, 0, func(string) {}, t.Logf, func(lockCtx context.Context) func(bool) {
			go func() { indexLoaded <- repoB.LoadIndex(lockCtx, restic.NoopTerminalCounterFactory) }()
			return func(ok bool) { confirmed <- ok }
		})
		bLocked <- lockResult{unlock, err}
	}()

	// 1. B has checked for locks and is saving its lock file (not visible yet).
	select {
	case <-slow.saveStarted:
	case <-time.After(10 * time.Second):
		t.Fatal("B did not start saving its lock")
	}

	// 2. P takes the exclusive lock; B's lock is not visible, so P is confirmed.
	unlockP, _, err := LockRepo(ctx, repoP, true, 0, func(string) {}, t.Logf)
	if err != nil {
		t.Fatalf("P could not lock exclusively: %v", err)
	}

	// 3. B's lock file lands; B starts its settle wait and (candidate) loads the index.
	close(slow.release)
	select {
	case err := <-indexLoaded:
		if err != nil {
			t.Fatalf("B's early index load failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("B did not load the index during its settle wait")
	}

	// 4. P finishes pruning the unreferenced blob X and unlocks, inside B's settle wait.
	for _, h := range append(indexFiles, packFiles...) {
		if err := shared.Remove(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	unlockP()

	// 5. B's check finds no competing lock.
	res := <-bLocked
	if res.err != nil {
		t.Skipf("schedule not reached on this run (B was refused the lock: %v); rerun", res.err)
	}
	defer res.unlock()
	if ok := <-confirmed; !ok {
		t.Skip("schedule not reached on this run (wait work was discarded); rerun")
	}

	// What stock does at this point: load the index now.
	fresh := TestOpenBackend(t, shared)
	if err := fresh.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
		t.Fatal(err)
	}
	_, stockKnows := fresh.LookupBlobSize(restic.BlobHandle{Type: restic.DataBlob, ID: idX})
	_, candidateKnows := repoB.LookupBlobSize(restic.BlobHandle{Type: restic.DataBlob, ID: idX})
	t.Logf("after the lock was confirmed: index loaded now (stock order) knows blob X = %v ; index kept from the settle wait (candidate order) knows blob X = %v", stockKnows, candidateKnows)
	if stockKnows {
		t.Fatal("test assumption broken: the pruned blob is still indexed")
	}

	// The backup now saves a file containing X.
	var known bool
	if err := repoB.WithBlobUploader(ctx, func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		var err error
		_, known, _, err = up.SaveBlob(ctx, restic.DataBlob, blobX, restic.ID{}, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	verify := TestOpenBackend(t, shared)
	if err := verify.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
		t.Fatal(err)
	}
	_, loadErr := verify.LoadBlob(ctx, restic.BlobHandle{Type: restic.DataBlob, ID: idX}, nil)
	t.Logf("backup saved blob X: reported known = %v ; reading X back from the repository afterwards: err = %v", known, loadErr)
	if known || loadErr != nil {
		t.Errorf("STALE INDEX USED AFTER A CONFIRMED LOCK: blob X was treated as already stored (known=%v) although prune had removed it; it is not in the repository (%v). A snapshot written now references missing data.", known, loadErr)
	}
}
