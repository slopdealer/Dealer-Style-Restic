package repository

import (
	"context"
	"encoding/binary"
	"io"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// TestRewriteRejectsDamagedKeptIndex is the red team's test (seat 2, finding
// 3): an index file which the rewrite keeps as it is must still be the file
// that was loaded. Upstream reads every index file again and fails on one
// whose content no longer hashes to its name.
func TestRewriteRejectsDamagedKeptIndex(t *testing.T) {
	ctx := context.Background()
	repo, be := TestRepositoryWithBackend(t, nil, 2, Options{})
	idx := index.NewIndex()
	blobs := make(pack.Blobs, 50000)
	for i := range blobs {
		var seed [8]byte
		binary.LittleEndian.PutUint64(seed[:], uint64(i))
		blobs[i] = pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash(seed[:])}, Offset: uint(i * 64), Length: 64}
	}
	idx.StorePack(restic.Hash([]byte("kept pack")), blobs)
	idx.Finalize()
	id, err := idx.SaveIndex(ctx, &internalRepository{repo})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
		t.Fatal(err)
	}
	h := backend.Handle{Type: backend.IndexFile, Name: id.String()}
	var encrypted []byte
	if err := be.Load(ctx, h, 0, 0, func(rd io.Reader) error {
		var err error
		encrypted, err = io.ReadAll(rd)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	encrypted[len(encrypted)/2] ^= 1
	if err := be.Remove(ctx, h); err != nil {
		t.Fatal(err)
	}
	if err := be.Save(ctx, h, backend.NewByteReader(encrypted, be.Hasher())); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LoadUnpacked(ctx, restic.IndexFile, id); err == nil {
		t.Fatal("control did not reject damaged index")
	} else {
		t.Logf("upstream's required reload rejects index: %v", err)
	}
	if err := repo.idx.Rewrite(ctx, &internalRepository{repo}, nil, nil, nil, index.MasterIndexRewriteOpts{}); err == nil {
		t.Fatal("rewrite skipped the damaged full index and returned success")
	}
}
