package repository

import (
	"context"
	"io"
	"math/rand"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/test"
)

// A handler that uses the backend must not run inside the backend's Load
// callback: with one backend connection the upload it starts waits for the
// connection that its own pack read holds.

type fixedConnectionsBackend struct {
	backend.Backend
	connections uint
}

func (b fixedConnectionsBackend) Properties() backend.Properties {
	p := b.Backend.Properties()
	p.Connections = b.connections
	return p
}

func (b fixedConnectionsBackend) Unwrap() backend.Backend { return b.Backend }

// saveIncompressibleBlobs stores count data blobs of 1 MiB and returns their
// IDs and the packs that hold them.
func saveIncompressibleBlobs(t *testing.T, repo *Repository, count int) (restic.IDs, restic.IDSet) {
	rnd := rand.New(rand.NewSource(5))
	var ids restic.IDs
	test.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		for i := 0; i < count; i++ {
			buf := make([]byte, 1<<20)
			rnd.Read(buf)
			id, _, _, err := up.SaveBlob(ctx, restic.DataBlob, buf, restic.ID{}, false)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	}))
	packs := restic.NewIDSet()
	for _, id := range ids {
		for _, pb := range repo.LookupBlob(restic.BlobHandle{Type: restic.DataBlob, ID: id}) {
			packs.Insert(pb.PackID())
		}
	}
	return ids, packs
}

// `restic repair packs` on a backend limited to one connection. The pack was
// written with the default pack size; the repairing run uses the minimum, so
// the salvaged blobs fill more than one new pack while the pack is read.
func testRepairPacksConnections(t *testing.T, connections uint) {
	be := sema.NewBackend(fixedConnectionsBackend{Backend: mem.New(), connections: connections})
	src, _ := TestRepositoryWithBackend(t, be, 2, Options{})
	ids, packs := saveIncompressibleBlobs(t, src, 13)

	repo, err := New(be, Options{Compression: CompressionFastest, PackSize: MinPackSize})
	test.OK(t, err)
	test.OK(t, repo.SearchKey(context.Background(), test.TestPassword, 10, ""))
	test.OK(t, repo.LoadIndex(context.Background(), restic.NoopTerminalCounterFactory))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- RepairPacks(ctx, repo, packs, restic.NewNoopPrinter()) }()
	select {
	case err := <-done:
		test.OK(t, err)
	case <-time.After(30 * time.Second):
		t.Fatalf("repair packs did not finish within 30 s with %d backend connection(s): deadlock", connections)
	}

	check := TestOpenBackend(t, be)
	test.OK(t, check.LoadIndex(context.Background(), restic.NoopTerminalCounterFactory))
	for _, id := range ids {
		_, err := check.LoadBlob(context.Background(), restic.BlobHandle{Type: restic.DataBlob, ID: id}, nil)
		test.OK(t, err)
	}
}

func TestRepairPacksOneConnection(t *testing.T)  { testRepairPacksConnections(t, 1) }
func TestRepairPacksTwoConnections(t *testing.T) { testRepairPacksConnections(t, 2) }

// packLoadDepthBackend counts the pack reads whose consumer is running.
type packLoadDepthBackend struct {
	backend.Backend
	inLoad atomic.Int32
	loads  atomic.Int32
}

func (b *packLoadDepthBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	if h.Type != backend.PackFile {
		return b.Backend.Load(ctx, h, length, offset, fn)
	}
	b.loads.Add(1)
	return b.Backend.Load(ctx, h, length, offset, func(rd io.Reader) error {
		b.inLoad.Add(1)
		defer b.inLoad.Add(-1)
		return fn(rd)
	})
}

func (b *packLoadDepthBackend) Unwrap() backend.Backend { return b.Backend }

// insideLoadUploader records saves that are requested while a pack read is
// inside its Load callback.
type insideLoadUploader struct {
	restic.BlobSaverWithAsync
	be         *packLoadDepthBackend
	saves      atomic.Int32
	insideLoad atomic.Int32
}

func (u *insideLoadUploader) note() {
	u.saves.Add(1)
	if u.be.inLoad.Load() != 0 {
		u.insideLoad.Add(1)
	}
}

func (u *insideLoadUploader) SaveBlob(ctx context.Context, tpe restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool) (restic.ID, bool, int, error) {
	u.note()
	return u.BlobSaverWithAsync.SaveBlob(ctx, tpe, buf, id, storeDuplicate)
}

func (u *insideLoadUploader) SaveBlobAsync(ctx context.Context, tpe restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool, cb func(restic.ID, bool, int, error)) {
	u.note()
	u.BlobSaverWithAsync.SaveBlobAsync(ctx, tpe, buf, id, storeDuplicate, cb)
}

// The repack handler (prune and copy) saves blobs. It must be called after
// the pack read has returned its connection, for every window of the pack.
func TestRepackHandlerRunsOutsideLoad(t *testing.T) {
	probe := &packLoadDepthBackend{Backend: mem.New()}
	// two connections: one repack worker, so no other pack read is in flight
	be := sema.NewBackend(fixedConnectionsBackend{Backend: probe, connections: 2})
	repo, _ := TestRepositoryWithBackend(t, be, 2, Options{})
	ids, packs := saveIncompressibleBlobs(t, repo, 6)
	test.Equals(t, 1, len(packs))

	keep := restic.NewBlobSet()
	for _, id := range ids {
		keep.Insert(restic.BlobHandle{Type: restic.DataBlob, ID: id})
	}
	probe.loads.Store(0)
	var uploader *insideLoadUploader
	test.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		uploader = &insideLoadUploader{BlobSaverWithAsync: up, be: probe}
		return CopyBlobs(ctx, repo, repo, uploader, packs, keep, restic.NoopCounter, nil)
	}))

	test.Equals(t, int32(len(ids)), uploader.saves.Load())
	if n := uploader.insideLoad.Load(); n != 0 {
		t.Fatalf("%d of %d saves were requested inside the backend Load callback of the pack read", n, len(ids))
	}
	// 6 blobs of 1 MiB in ranges below 4 MiB: three blobs per request
	test.Equals(t, int32(2), probe.loads.Load())
	test.Equals(t, 0, keep.Len())
}
