package repository

// WITNESS (storage review, mechanism "one request per pack range, read in
// windows": streamPackResponse calls the blob handler INSIDE the backend Load
// callback, i.e. while the reader still holds a backend connection).
// OS: any (pure Go, in-memory backend). The file uses only APIs that exist in
// stock as well, so it can be dropped into the stock tree as the control.
//
// Scenario: `restic repair packs <id>` on a repository whose backend is limited
// to one connection (`-o local.connections=1`, `-o sftp.connections=1`, ...). The
// pack was written with the default 16 MiB pack size; the repairing run uses the
// 4 MiB minimum, so the salvaged blobs fill more than one new pack.
//
//   - Stock: each range is read into memory, the connection is released, then the
//     handler re-uploads the blobs. Uploads get the connection. Completes.
//   - Candidate: the handler runs while Load holds the only connection. The first
//     full pack is taken by the uploader, which waits for a connection. The second
//     full pack waits for the uploader. Nothing can proceed: the command hangs.
//
// Expected: TestWitnessRepairPacksTwoConnections PASS;
// TestWitnessRepairPacksOneConnection on the candidate FAIL after 30 s (deadlock),
// on stock PASS.

import (
	"context"
	"math/rand"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/test"
)

type witnessConnBackend struct {
	backend.Backend
	connections uint
}

func (b witnessConnBackend) Properties() backend.Properties {
	p := b.Backend.Properties()
	p.Connections = b.connections
	return p
}

func (b witnessConnBackend) Unwrap() backend.Backend { return b.Backend }

func runWitnessRepairPacks(t *testing.T, connections uint) {
	be := sema.NewBackend(witnessConnBackend{Backend: mem.New(), connections: connections})
	// the pack is written with the default 16 MiB pack size
	src, _ := TestRepositoryWithBackend(t, be, 2, Options{})

	rnd := rand.New(rand.NewSource(5))
	var ids restic.IDs
	err := src.WithBlobUploader(context.Background(), func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		for i := 0; i < 13; i++ {
			buf := make([]byte, 1<<20) // incompressible
			rnd.Read(buf)
			id, _, _, err := up.SaveBlob(ctx, restic.DataBlob, buf, restic.ID{}, false)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	packs := restic.NewIDSet()
	for _, id := range ids {
		for _, pb := range src.LookupBlob(restic.BlobHandle{Type: restic.DataBlob, ID: id}) {
			packs.Insert(pb.PackID())
		}
	}
	t.Logf("%d blobs of 1 MiB in %d pack(s); backend connections: %d", len(ids), len(packs), connections)

	// the repairing run: same repository, minimum pack size
	repo, err := New(be, Options{Compression: CompressionFastest, PackSize: MinPackSize})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.SearchKey(context.Background(), test.TestPassword, 10, ""); err != nil {
		t.Fatal(err)
	}
	if err := repo.LoadIndex(context.Background(), restic.NoopTerminalCounterFactory); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RepairPacks(ctx, repo, packs, restic.NewNoopPrinter()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("repair packs failed: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatalf("DEADLOCK REPRODUCED: repair packs did not finish within 30 s with %d backend connection(s)", connections)
	}

	// every blob must still be readable afterwards
	check := TestOpenBackend(t, be)
	if err := check.LoadIndex(context.Background(), restic.NoopTerminalCounterFactory); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := check.LoadBlob(context.Background(), restic.BlobHandle{Type: restic.DataBlob, ID: id}, nil); err != nil {
			t.Fatalf("blob %v lost by repair packs: %v", id, err)
		}
	}
}

func TestWitnessRepairPacksTwoConnections(t *testing.T) { runWitnessRepairPacks(t, 2) }
func TestWitnessRepairPacksOneConnection(t *testing.T)  { runWitnessRepairPacks(t, 1) }
