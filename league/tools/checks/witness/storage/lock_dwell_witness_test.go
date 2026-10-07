package repository

// WITNESS (storage review, mechanism "lock-dwell wait work": index preloaded
// between lock creation and the competing-lock check).
// OS: any. Timing based (production 200 ms lock wait, margins about 180 ms): run it
// alone on an idle machine, `go test -run TestWitnessLockDwell -count=1 ./internal/repository`.
//
// Two "processes" share one backend, each with its own *Repository handle:
//
//	B = a backup (non-exclusive lock). Its lock-file upload is slow (600 ms; a WAN
//	    SFTP/S3/B2 client). As cmd/restic/cmd_backup.go does, it starts
//	    repo.LoadIndex as wait work right after its lock file is written.
//	P = a prune (exclusive lock) next to the storage. It starts while B's lock file
//	    is still in flight, so neither of P's two lock scans can see B. P removes
//	    one pack and its index file and unlocks before B's confirmation scan.
//
// B's confirmation scan finds no lock, so B is confirmed and keeps the index it
// loaded BEFORE P deleted the pack. A backup would now treat the blob as stored
// (known=true, nothing uploaded) and write a snapshot that references a deleted
// pack, reporting success.
//
// TestWitnessLockDwellStockOrder is the same schedule with stock's order (index
// loaded after the lock call returns): the index is fresh, the blob is unknown and
// would be uploaded again. Expected: PASS.
// TestWitnessLockDwellPreloadedIndexIsStale is the candidate's order.
// Expected on the candidate: FAIL (= hazard reproduced at API level).
//
// Not shown here: an end-to-end broken snapshot with the real binaries. That needs
// a backend with > 200 ms lock-save latency for one client and a prune that
// finishes inside that latency; see REVIEW-storage.md, NEEDS-RUN.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/restic"
)

type witnessSlowLockSaver struct {
	restic.Unpacked[restic.FileType]
	delay     time.Duration
	inFlight  chan struct{}
	announce  sync.Once
	lockSaved chan struct{}
}

func (s *witnessSlowLockSaver) SaveUnpacked(ctx context.Context, t restic.FileType, buf []byte) (restic.ID, error) {
	if t != restic.LockFile {
		return s.Unpacked.SaveUnpacked(ctx, t, buf)
	}
	// B has finished its first lock scan; its lock file is now "in flight".
	s.announce.Do(func() { close(s.inFlight) })
	time.Sleep(s.delay)
	id, err := s.Unpacked.SaveUnpacked(ctx, t, buf)
	close(s.lockSaved)
	return id, err
}

type witnessDwellSetup struct {
	be     backend.Backend
	repoP  *Repository
	repoB  *Repository
	blob   restic.ID
	packID restic.ID
	idxIDs restic.IDs
}

func newWitnessDwellSetup(t *testing.T) *witnessDwellSetup {
	be := mem.New()
	repoP, _ := TestRepositoryWithBackend(t, be, 2, Options{})
	TestSetLockTimeout(t, 200*time.Millisecond) // the production value

	data := []byte("data that a later backup wants to deduplicate against")
	var blob restic.ID
	err := repoP.WithBlobUploader(context.Background(), func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		var err error
		blob, _, _, err = up.SaveBlob(ctx, restic.DataBlob, data, restic.ID{}, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	s := &witnessDwellSetup{be: be, repoP: repoP, blob: blob}
	pbs := repoP.LookupBlob(restic.BlobHandle{Type: restic.DataBlob, ID: blob})
	if len(pbs) != 1 {
		t.Fatalf("setup: blob is in %d packs", len(pbs))
	}
	s.packID = pbs[0].PackID()
	err = repoP.List(context.Background(), restic.IndexFile, func(id restic.ID, _ int64) error {
		s.idxIDs = append(s.idxIDs, id)
		return nil
	})
	if err != nil || len(s.idxIDs) == 0 {
		t.Fatalf("setup: index files %v, err %v", s.idxIDs, err)
	}
	s.repoB = TestOpenBackend(t, be) // B's own handle, empty in-memory index
	return s
}

// prune is what P does once it holds its exclusive lock: delete the pack and
// the index file that lists it (the blob was unused), then unlock.
func (s *witnessDwellSetup) prune(t *testing.T, lockP *lockHandle) {
	ctx := context.Background()
	p := &internalRepository{s.repoP}
	for _, id := range s.idxIDs {
		if err := p.RemoveUnpacked(ctx, restic.IndexFile, id); err != nil {
			t.Errorf("prune: remove index: %v", err)
		}
	}
	if err := p.RemoveUnpacked(ctx, restic.PackFile, s.packID); err != nil {
		t.Errorf("prune: remove pack: %v", err)
	}
	if err := lockP.unlock(ctx); err != nil {
		t.Errorf("prune: unlock: %v", err)
	}
}

func runWitnessDwell(t *testing.T, preload bool) (known bool) {
	s := newWitnessDwellSetup(t)
	ctx := context.Background()
	slowB := &witnessSlowLockSaver{
		Unpacked:  &internalRepository{s.repoB},
		delay:     600 * time.Millisecond,
		inFlight:  make(chan struct{}),
		lockSaved: make(chan struct{}),
	}

	loaded := make(chan error, 1)
	confirmedCh := make(chan bool, 1)
	var startWaitWork func(context.Context) func(bool)
	if preload {
		// The shape of cmd/restic/backup_index_load.go: load in the background,
		// keep the result if the lock is confirmed.
		startWaitWork = func(workCtx context.Context) func(bool) {
			done := make(chan struct{})
			go func() {
				loaded <- s.repoB.LoadIndex(workCtx, restic.NoopTerminalCounterFactory)
				close(done)
			}()
			return func(confirmed bool) {
				<-done
				if !confirmed {
					s.repoB.DiscardIndex()
				}
				confirmedCh <- confirmed
			}
		}
	}

	type lockResult struct {
		lock *lockHandle
		err  error
	}
	resB := make(chan lockResult, 1)
	go func() {
		l, err := newLockWithWaitWork(ctx, slowB, false, startWaitWork)
		resB <- lockResult{l, err}
	}()

	// P starts while B's lock file is in flight.
	<-slowB.inFlight
	lockP, err := newLock(ctx, &internalRepository{s.repoP}, true)
	if err != nil {
		t.Fatalf("P could not take its exclusive lock: %v", err)
	}
	select {
	case <-slowB.lockSaved:
		t.Fatal("schedule broken: B's lock became visible before P was confirmed")
	default:
	}
	// P works until B's lock file exists and (candidate order) B has read the index.
	<-slowB.lockSaved
	if preload {
		if err := <-loaded; err != nil {
			t.Fatalf("B's preload failed: %v", err)
		}
	}
	s.prune(t, lockP)

	got := <-resB
	if got.err != nil || got.lock == nil {
		t.Fatalf("schedule broken: B was not confirmed: %v", got.err)
	}
	defer func() { _ = got.lock.unlock(ctx) }()
	if preload {
		if !<-confirmedCh {
			t.Fatal("schedule broken: wait work was not confirmed")
		}
	} else {
		// stock order: the index is loaded after the lock is confirmed
		if err := s.repoB.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
			t.Fatal(err)
		}
	}

	_, known = s.repoB.LookupBlobSize(restic.BlobHandle{Type: restic.DataBlob, ID: s.blob})
	_, loadErr := s.repoB.LoadBlob(ctx, restic.BlobHandle{Type: restic.DataBlob, ID: s.blob}, nil)
	t.Logf("B confirmed its lock. B's index says blob known=%v; loading the blob: err=%v", known, loadErr)
	return known
}

func TestWitnessLockDwellStockOrder(t *testing.T) {
	if runWitnessDwell(t, false) {
		t.Fatal("stock order: the index loaded after confirmation still lists the pruned blob")
	}
}

func TestWitnessLockDwellPreloadedIndexIsStale(t *testing.T) {
	if runWitnessDwell(t, true) {
		t.Fatal("HAZARD REPRODUCED: the lock was confirmed, but the index preloaded during the lock wait " +
			"still lists a blob whose pack a concurrent prune deleted; a backup would deduplicate against it")
	}
}
