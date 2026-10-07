package repository

import (
	"context"
	"io"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// slotProbeBackend reports a connection count and whether it grants the
// additional load slot, and records how many pack loads run at the same time.
// It sits directly under the repository (no semaphore layer), so the number
// of loads in flight is the number of check workers that are reading a pack.
type slotProbeBackend struct {
	backend.Backend
	connections uint
	slot        bool
	inFlight    atomic.Int32
	maxInFlight atomic.Int32
	loads       atomic.Int32
}

func (b *slotProbeBackend) Properties() backend.Properties {
	p := b.Backend.Properties()
	p.Connections = b.connections
	return p
}

func (b *slotProbeBackend) AllowAdditionalLoadSlot() bool { return b.slot }

func (b *slotProbeBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	if h.Type != backend.PackFile {
		return b.Backend.Load(ctx, h, length, offset, fn)
	}
	n := b.inFlight.Add(1)
	defer b.inFlight.Add(-1)
	for {
		seen := b.maxInFlight.Load()
		if n <= seen || b.maxInFlight.CompareAndSwap(seen, n) {
			break
		}
	}
	b.loads.Add(1)
	// long enough for every idle worker to pick up a pack as well
	time.Sleep(20 * time.Millisecond)
	return b.Backend.Load(ctx, h, length, offset, fn)
}

func checkWorkersInFlight(t *testing.T, connections uint, slot bool) (maxInFlight, loads int32) {
	t.Helper()
	repo, _, be := TestRepositoryWithVersion(t, 2)
	random := rand.New(rand.NewSource(20261007))
	// 16 packs of small blobs: every one of them is a task the extra worker accepts
	for range 16 {
		// one upload session per pack
		rtest.OK(t, repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
			for range 3 {
				buf := make([]byte, 2048+random.Intn(4096))
				random.Read(buf)
				if _, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, buf, restic.ID{}, false); err != nil {
					return err
				}
			}
			return nil
		}))
	}

	probe := &slotProbeBackend{Backend: be, connections: connections, slot: slot}
	opened := TestOpenBackend(t, probe)
	rtest.OK(t, opened.LoadIndex(context.TODO(), restic.NoopTerminalCounterFactory))
	rtest.Equals(t, slot, opened.extraCheckWorker())

	errs := make(chan error)
	go func() {
		// ReadPacks closes errs
		newChecker(opened).ReadPacks(context.TODO(), func(p map[restic.ID]int64) map[restic.ID]int64 { return p }, restic.NewNoopPrinter(), errs)
	}()
	for err := range errs {
		t.Errorf("check: %v", err)
	}
	return probe.maxInFlight.Load(), probe.loads.Load()
}

// On a backend that grants no additional load slot check must not read more
// packs at a time than the backend has connections: the extra worker would
// only queue behind the configured ones for a connection.
func TestCheckWorkersFollowConnectionsWithoutExtraSlot(t *testing.T) {
	for _, connections := range []uint{1, 2, 5} {
		maxInFlight, loads := checkWorkersInFlight(t, connections, false)
		if loads < 16 {
			t.Fatalf("connections=%d: only %d pack loads, the test repository has too few packs", connections, loads)
		}
		if maxInFlight > int32(connections) {
			t.Errorf("connections=%d, no additional load slot: %d packs were read at the same time, stock reads at most %d",
				connections, maxInFlight, connections)
		}
	}
}

// Control: where the slot is granted (the local backend at its default
// connection count) the extra worker still runs beside the configured ones.
func TestCheckExtraWorkerRunsWithExtraSlot(t *testing.T) {
	maxInFlight, _ := checkWorkersInFlight(t, 2, true)
	if maxInFlight != 3 {
		t.Errorf("connections=2 with the additional load slot: %d packs were read at the same time, want 3", maxInFlight)
	}
}

func TestExtraCheckTaskNeedsSlot(t *testing.T) {
	repo, _, _ := TestRepositoryWithVersion(t, 2)
	// the in-memory backend grants no slot
	rtest.Equals(t, false, repo.extraCheckWorker())
	rtest.Equals(t, false, extraCheckTask(false, nil, 0))
	rtest.Equals(t, false, extraCheckTask(true, nil, 0))
}
