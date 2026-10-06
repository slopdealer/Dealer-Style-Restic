package main

// Regression tests for the lock-wait defect (repair R1): reads taken during the
// lock's settle delay were used after the lock was confirmed, although another
// process had completed exclusive work in between.
//
// These are the two API-level witnesses of the soundness reviews
// (review/witness-storage/lock_dwell_witness_test.go and
// review/witness-backup/lock_dwell_stale_index_witness_test.go), ported from
// internal/repository to this package so that they drive the wait work the
// commands really use (backupIndexWaitWork) instead of a copy of it.
//
// Two "processes" share one in-memory backend, each with its own repository
// handle. Nothing in the schedule is forbidden by restic's lock protocol:
//
//  1. B (a backup, non-exclusive lock) checks for locks (none) and starts
//     saving its lock file. The save is slow: the file is not visible yet.
//  2. P (a prune, exclusive lock) checks for locks (none), creates its lock,
//     waits, checks again. B's lock is still not visible, so P is confirmed.
//  3. B's lock file lands. B starts its settle wait. The defective order reads
//     the snapshot list and the index now.
//  4. P finishes: it removes the index file and the pack that hold the unused
//     blob X, and unlocks.
//  5. B's settle wait ends. B sees no competing lock: its lock is confirmed.
//
// Stock loads the index after step 5: X is gone and a backup uploads it again.
// The defective order kept the index of step 3: X is "known", is not uploaded,
// and the new snapshot references a blob that no longer exists.
//
// TestWitnessLockDwellStockOrder is the control: the same schedule with the
// index loaded after the lock call returns. It passes in every tree.
// The other two tests fail on a tree with the defect and pass once it is
// repaired, whichever way: either B reads nothing during its wait because it
// saw P's lock, or B notices after confirmation that what it read is gone.
//
// The settle delay is scaled to 1 s (production: 200 ms) so that the order of
// the steps holds on a loaded machine. Run:
//
//	go test -run 'TestWitnessLockDwell' -count=1 -v ./cmd/restic

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

const dwellSettleDelay = 1 * time.Second

// dwellSlowLockBackend is B's view of the shared backend. Its first lock-file
// save is held back until the test releases it, and it reports when B has
// listed the index and loaded an index file.
type dwellSlowLockBackend struct {
	backend.Backend
	saveOnce    sync.Once
	saveStarted chan struct{}
	release     chan struct{}
	saved       chan struct{}

	mu          sync.Mutex
	indexListed bool
	readOnce    sync.Once
	indexRead   chan struct{}
}

func newDwellSlowLockBackend(be backend.Backend) *dwellSlowLockBackend {
	return &dwellSlowLockBackend{
		Backend:     be,
		saveStarted: make(chan struct{}),
		release:     make(chan struct{}),
		saved:       make(chan struct{}),
		indexRead:   make(chan struct{}),
	}
}

func (be *dwellSlowLockBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if h.Type != backend.LockFile {
		return be.Backend.Save(ctx, h, rd)
	}
	first := false
	be.saveOnce.Do(func() {
		first = true
		close(be.saveStarted)
	})
	if !first {
		return be.Backend.Save(ctx, h, rd)
	}
	<-be.release
	err := be.Backend.Save(ctx, h, rd)
	close(be.saved)
	return err
}

func (be *dwellSlowLockBackend) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	err := be.Backend.List(ctx, t, fn)
	if t == backend.IndexFile {
		be.mu.Lock()
		be.indexListed = true
		be.mu.Unlock()
	}
	return err
}

func (be *dwellSlowLockBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	err := be.Backend.Load(ctx, h, length, offset, fn)
	if h.Type == backend.IndexFile {
		be.mu.Lock()
		listed := be.indexListed
		be.mu.Unlock()
		if listed {
			be.readOnce.Do(func() { close(be.indexRead) })
		}
	}
	return err
}

type dwellResult struct {
	// known: B's index, as B uses it after its lock was confirmed, lists X
	known bool
	// loadErr: reading X through B's index
	loadErr error
	// readDuringWait: B listed and loaded the index before its lock was confirmed
	readDuringWait bool
	// savedKnown: a backup of X through B reported the blob as already stored
	savedKnown bool
	// readBackErr: reading X from a fresh repository handle after B's backup
	readBackErr error
}

// runLockDwell drives the schedule. With stockOrder B loads the index after
// the lock call returned; otherwise it uses backup's wait work. With save, B
// then stores X as a backup would.
func runLockDwell(t *testing.T, stockOrder bool, save bool) dwellResult {
	ctx := context.Background()
	shared := mem.New()
	repository.TestSetLockTimeout(t, dwellSettleDelay)
	defer repository.TestSetLockTimeout(t, 0)

	// A repository holding one unused data blob X in one pack, described by
	// one index file.
	setup, _ := repository.TestRepositoryWithBackend(t, shared, 0, repository.Options{})
	blobX := []byte("contents of a file that an interrupted backup already uploaded")
	var idX restic.ID
	if err := setup.WithBlobUploader(ctx, func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		var err error
		idX, _, _, err = up.SaveBlob(ctx, restic.DataBlob, blobX, restic.ID{}, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	handleX := restic.BlobHandle{Type: restic.DataBlob, ID: idX}
	var obsolete []backend.Handle
	for _, tpe := range []backend.FileType{backend.IndexFile, backend.PackFile} {
		if err := shared.List(ctx, tpe, func(fi backend.FileInfo) error {
			obsolete = append(obsolete, backend.Handle{Type: tpe, Name: fi.Name})
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(obsolete) != 2 {
		t.Fatalf("setup: want one index file and one pack, got %v", obsolete)
	}

	slow := newDwellSlowLockBackend(shared)
	repoB := repository.TestOpenBackend(t, slow)   // the backup process
	repoP := repository.TestOpenBackend(t, shared) // the prune process

	var indexLoad *backupIndexLoad
	type lockResult struct {
		unlock func()
		err    error
	}
	lockedB := make(chan lockResult, 1)
	noRetryMessage := func(string) {}
	go func() {
		var unlock func()
		var err error
		if stockOrder {
			unlock, _, err = repository.LockRepo(ctx, repoB, false, 0, noRetryMessage, t.Logf)
		} else {
			work := backupIndexWaitWork(&indexLoad, true)
			unlock, _, err = repository.LockRepoWithWaitWork(ctx, repoB, false, 0, noRetryMessage, t.Logf, func(lockCtx context.Context) func(bool) {
				return work(lockCtx, repoB)
			})
		}
		lockedB <- lockResult{unlock, err}
	}()

	// 1. B has checked for locks and is saving its lock file (not visible yet).
	select {
	case <-slow.saveStarted:
	case <-time.After(30 * time.Second):
		t.Fatal("B did not start saving its lock")
	}

	// 2. P takes the exclusive lock; B's lock is not visible, so P is confirmed.
	unlockP, _, err := repository.LockRepo(ctx, repoP, true, 0, noRetryMessage, t.Logf)
	if err != nil {
		t.Fatalf("P could not lock exclusively: %v", err)
	}

	// 3. B's lock file lands; B starts its settle wait.
	close(slow.release)
	<-slow.saved
	var res dwellResult
	select {
	case <-slow.indexRead:
		res.readDuringWait = true
	case <-time.After(dwellSettleDelay * 3 / 10):
		// B reads nothing during its wait (stock order, or it saw P's lock)
	}

	// 4. P removes the index file and the pack with X and unlocks, inside B's
	// settle wait.
	for _, h := range obsolete {
		if err := shared.Remove(ctx, h); err != nil {
			t.Fatal(err)
		}
	}
	unlockP()

	// 5. B's second check finds no competing lock.
	locked := <-lockedB
	if locked.err != nil {
		t.Fatalf("schedule not reached (B was refused the lock, the machine may be overloaded): %v", locked.err)
	}
	defer locked.unlock()
	if indexLoad != nil {
		defer indexLoad.discard()
	}

	// B continues as backup does: parent snapshot search, then the index.
	if indexLoad != nil {
		if err := indexLoad.parentSearchRepo(repoB).List(ctx, restic.SnapshotFile, func(restic.ID, int64) error { return nil }); err != nil {
			t.Fatal(err)
		}
		err = indexLoad.wait(restic.NoopTerminalCounterFactory)
	} else {
		err = repoB.LoadIndex(ctx, restic.NoopTerminalCounterFactory)
	}
	if err != nil {
		t.Fatalf("B could not load the index after its lock was confirmed: %v", err)
	}

	_, res.known = repoB.LookupBlobSize(handleX)
	_, res.loadErr = repoB.LoadBlob(ctx, handleX, nil)
	t.Logf("B's lock is confirmed. B read the index during its wait: %v. B's index lists X: %v; loading X through it: err=%v",
		res.readDuringWait, res.known, res.loadErr)

	if save {
		if err := repoB.WithBlobUploader(ctx, func(ctx context.Context, up restic.BlobSaverWithAsync) error {
			var err error
			_, res.savedKnown, _, err = up.SaveBlob(ctx, restic.DataBlob, blobX, restic.ID{}, false)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		verify := repository.TestOpenBackend(t, shared)
		if err := verify.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
			t.Fatal(err)
		}
		_, res.readBackErr = verify.LoadBlob(ctx, handleX, nil)
		t.Logf("B saved X: reported as already stored = %v; reading X back from the repository: err=%v", res.savedKnown, res.readBackErr)
	}
	return res
}

// TestWitnessLockDwellStockOrder is the control: stock's order on the same
// schedule.
func TestWitnessLockDwellStockOrder(t *testing.T) {
	res := runLockDwell(t, true, true)
	if res.readDuringWait {
		t.Fatal("stock order read the index during the lock wait")
	}
	if res.known || res.savedKnown || res.readBackErr != nil {
		t.Fatalf("stock order: the index loaded after confirmation still lists the pruned blob (known=%v, saved as known=%v, read back: %v)",
			res.known, res.savedKnown, res.readBackErr)
	}
}

// TestWitnessLockDwellPreloadedIndexIsStale is the storage review's witness.
func TestWitnessLockDwellPreloadedIndexIsStale(t *testing.T) {
	res := runLockDwell(t, false, false)
	if res.known {
		t.Fatal("the lock was confirmed, but the index B uses still lists a blob whose pack a concurrent prune deleted; " +
			"a backup would deduplicate against it")
	}
}

// TestWitnessLockDwellStaleIndex is the backup review's witness.
func TestWitnessLockDwellStaleIndex(t *testing.T) {
	res := runLockDwell(t, false, true)
	if res.known || res.savedKnown || res.readBackErr != nil {
		t.Fatalf("stale index used after a confirmed lock: blob X was treated as already stored (index lists it: %v, saved as known: %v) "+
			"although prune had removed it; reading it back: %v. A snapshot written now references missing data.",
			res.known, res.savedKnown, res.readBackErr)
	}
}
