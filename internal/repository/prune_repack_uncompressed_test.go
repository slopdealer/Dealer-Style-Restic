package repository_test

import (
	"bytes"
	"context"
	"math"
	"math/rand"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// `prune --repack-uncompressed` converts a repository once: after it has run,
// a second run must find nothing to repack, as in stock. That fails when a
// blob that does not compress is stored uncompressed again by the repack: it
// stays "uncompressed" in the index and every later run rewrites it.
func TestPruneRepackUncompressedComesToRest(t *testing.T) {
	ctx := context.Background()
	be := repository.TestBackend(t)
	// written without compression: every data blob is uncompressed
	repo, _ := repository.TestRepositoryWithBackend(t, be, 2, repository.Options{Compression: repository.CompressionOff})

	rnd := rand.New(rand.NewSource(20261007))
	random := func(n int) []byte {
		buf := make([]byte, n)
		rnd.Read(buf)
		return buf
	}
	text := func(n int) []byte {
		return bytes.Repeat([]byte("restic repack uncompressed "), n/27+1)[:n]
	}
	var blobs [][]byte
	for range 6 {
		blobs = append(blobs, random(1<<20)) // does not compress, 1 MiB
	}
	for range 3 {
		blobs = append(blobs, random(512*1024+rnd.Intn(200*1024))) // does not compress, just above 512 KiB
	}
	for range 5 {
		blobs = append(blobs, random(100*1024)) // does not compress, small
	}
	for i := range 4 {
		blobs = append(blobs, text(700*1024+i)) // compresses
	}

	keep := restic.NewBlobSet()
	content := map[restic.ID][]byte{}
	rtest.OK(t, repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		for _, buf := range blobs {
			id, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, buf, restic.ID{}, false)
			if err != nil {
				return err
			}
			keep.Insert(restic.BlobHandle{Type: restic.DataBlob, ID: id})
			content[id] = buf
		}
		return nil
	}))
	rtest.OK(t, repo.Close())

	// every later step runs with the production default, CompressionAuto
	// (the test helpers open with CompressionFastest)
	open := func(be backend.Backend) *repository.Repository {
		repo, err := repository.New(be, repository.Options{})
		rtest.OK(t, err)
		rtest.OK(t, repo.SearchKey(ctx, rtest.TestPassword, 10, ""))
		rtest.OK(t, repo.LoadIndex(ctx, restic.NoopTerminalCounterFactory))
		return repo
	}
	pruneRepackUncompressed := func() repository.PruneStats {
		repo := open(be)
		plan, err := repository.PlanPrune(ctx, repository.PruneOptions{
			MaxRepackBytes:     math.MaxUint64,
			MaxUnusedBytes:     func(uint64) uint64 { return 0 },
			RepackUncompressed: true,
		}, repo, func(_ context.Context, _ restic.Repository, usedBlobs restic.FindBlobSet) error {
			for blob := range keep {
				usedBlobs.Insert(blob)
			}
			return nil
		}, restic.NewNoopPrinter())
		rtest.OK(t, err)
		stats := plan.Stats()
		rtest.OK(t, plan.Execute(ctx, restic.NewNoopPrinter()))
		rtest.OK(t, repo.Close())
		return stats
	}

	first := pruneRepackUncompressed()
	if len(keep) != len(blobs) || first.Blobs.Repack != uint(len(blobs)) {
		t.Fatalf("first run repacks %d blobs, the repository holds %d uncompressed blobs (%d distinct)", first.Blobs.Repack, len(blobs), len(keep))
	}

	second := pruneRepackUncompressed()
	if second.Blobs.Repack != 0 || second.Size.Uncompressed != 0 {
		t.Errorf("second run still wants to repack %d blobs (%d uncompressed bytes); stock: 0",
			second.Blobs.Repack, second.Size.Uncompressed)
	}

	repo = open(be)
	repository.TestCheckRepo(t, repo)
	for blob := range keep {
		pbs := repo.LookupBlob(blob)
		if len(pbs) != 1 || !pbs[0].IsCompressed() {
			t.Errorf("blob %v (%d bytes) is not stored in compressed form after the conversion", blob.ID.Str(), len(content[blob.ID]))
		}
		got, err := repo.LoadBlob(ctx, blob, nil)
		rtest.OK(t, err)
		if !bytes.Equal(got, content[blob.ID]) {
			t.Errorf("blob %v does not read back", blob.ID.Str())
		}
	}
}
