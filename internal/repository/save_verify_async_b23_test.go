package repository

import (
	"bytes"
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/restic/restic/internal/restic"
)

// A rejected asynchronous save must settle its accepted callback and join the
// uploader without publishing ciphertext or an indexed blob. This uses the
// complete production pipeline, including its reserved error publisher.
func TestB23VerifyAsyncRejectedSaveSettlement(t *testing.T) {
	r, _, _ := TestRepositoryWithVersion(t, 2)
	data := bytes.Repeat([]byte("complete authenticated compressed verification\n"), 100000)
	wrong := restic.Hash([]byte("a different complete blob"))
	want := fmt.Sprintf("Detected data corruption while saving blob %v: hash mismatch\nCorrupted blobs are either caused by hardware issues or software bugs. Please open an issue at https://github.com/restic/restic/issues/new/choose for further troubleshooting.", wrong)
	var settled atomic.Int64
	err := r.WithBlobUploader(t.Context(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
		result := make(chan error, 1)
		u.SaveBlobAsync(ctx, restic.DataBlob, data, wrong, true, func(id restic.ID, known bool, size int, err error) {
			if id != wrong || known || size != 0 || err == nil || err.Error() != want {
				t.Errorf("callback: %v %v %d %v", id, known, size, err)
			}
			settled.Add(1)
			result <- err
		})
		return <-result
	})
	if err == nil || err.Error() != want || settled.Load() != 1 {
		t.Fatalf("return before exact settlement: %v count=%d", err, settled.Load())
	}
	if len(r.idx.Lookup(restic.BlobHandle{Type: restic.DataBlob, ID: wrong})) != 0 {
		t.Fatal("rejected blob was indexed")
	}
	packs := 0
	if err := r.List(t.Context(), restic.PackFile, func(restic.ID, int64) error { packs++; return nil }); err != nil {
		t.Fatal(err)
	}
	if packs != 0 {
		t.Fatal("rejected asynchronous save submitted a pack")
	}
}
