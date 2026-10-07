package repository

// WITNESS (storage review, mechanism "CompressionAuto sampling: large data blobs
// whose 16 x 2 KiB sample does not shrink by 1% are stored uncompressed").
// OS: any (pure Go).
//
// The sample only sees local redundancy inside 2 KiB windows. A blob whose
// redundancy is long range (here: one incompressible 64 KiB block sixteen times,
// the shape of an uncompressed tar/zip-store/disk image that holds several copies
// of an already compressed file smaller than the 512 KiB minimum chunk size) is
// classified as incompressible and stored raw. Stock compresses every data blob
// under the default `--compression auto` and stores this one at about 1/16 of
// its size.
//
// Expected on the candidate: FAIL (= behaviour change reproduced: the repository
// is larger than stock's for such data). The stored blob is valid and stock reads
// it; this is a size regression, not an integrity problem.

import (
	"bytes"
	"context"
	"math/rand"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestWitnessAutoCompressionMissesLongRangeRedundancy(t *testing.T) {
	repo, _, _ := TestRepositoryWithVersion(t, 2)
	// the production default; the test helper silently replaces Auto by Fastest
	repo.opts.Compression = CompressionAuto

	block := make([]byte, 64<<10)
	rand.New(rand.NewSource(3)).Read(block)
	data := bytes.Repeat(block, 16) // 1 MiB

	stockStored := len(repo.getZstdEncoder().EncodeAll(data, nil))
	t.Logf("sample verdict: compressible=%v", repo.autoDataBlobLooksCompressible(data))

	var id restic.ID
	err := repo.WithBlobUploader(context.Background(), func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		var err error
		id, _, _, err = up.SaveBlob(ctx, restic.DataBlob, data, restic.ID{}, false)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	pbs := repo.LookupBlob(restic.BlobHandle{Type: restic.DataBlob, ID: id})
	if len(pbs) != 1 {
		t.Fatalf("blob stored %d times", len(pbs))
	}
	got, err := repo.LoadBlob(context.Background(), restic.BlobHandle{Type: restic.DataBlob, ID: id}, nil)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("blob does not read back: %v", err)
	}
	t.Logf("candidate stores %d bytes (compressed=%v); stock's EncodeAll of the same blob is %d bytes",
		pbs[0].CiphertextLength(), pbs[0].IsCompressed(), stockStored)
	if !pbs[0].IsCompressed() {
		t.Fatalf("SIZE REGRESSION REPRODUCED: blob stored raw at %d bytes; stock stores about %d bytes (%.1fx smaller)",
			pbs[0].CiphertextLength(), stockStored, float64(pbs[0].CiphertextLength())/float64(stockStored))
	}
}
