package restorer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// A deliberately undelivered later blob leaves an earlier ordinary file fully
// written. This distinguishes successful writes from successful file-stage
// settlement without introducing an early metadata operation in production.
type completionSettlementRepository struct {
	restic.Repository
	late    restic.ID
	fatal   error
	earlier atomic.Bool
}

func (r *completionSettlementRepository) LoadBlobsFromPack(ctx context.Context, packID restic.ID, blobs []restic.BlobHandle, handle func(restic.BlobHandle, []byte, error) error) error {
	missing := false
	err := r.Repository.LoadBlobsFromPack(ctx, packID, blobs, func(h restic.BlobHandle, buf []byte, err error) error {
		if h.ID == r.late {
			missing = true
			return nil
		}
		err = handle(h, buf, err)
		if err == nil {
			r.earlier.Store(true)
		}
		return err
	})
	if err == nil && missing {
		return r.fatal
	}
	return err
}

func TestRestoreCompletionFatalSettlementPreservesMetadataGate(t *testing.T) {
	repo := repository.TestRepository(t)
	stamp := time.Unix(1234567890, 0)
	sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: map[string]Node{
		"earlier": File{Data: "completed content", Mode: 0400, ModTime: stamp},
		"later":   File{Data: "missing content", Mode: 0400, ModTime: stamp},
	}}, noopGetGenericAttributes)
	fatal := errors.New("later pack content failed")
	failing := &completionSettlementRepository{Repository: repo, late: restic.Hash([]byte("missing content")), fatal: fatal}
	res := NewRestorer(failing, sn, Options{})
	res.SetConcurrentXattrSelectFilter(func(string) bool { return true })
	dst := t.TempDir()
	_, err := res.RestoreTo(context.Background(), dst)
	if !errors.Is(err, fatal) {
		t.Fatalf("settlement error = %v", err)
	}
	if !failing.earlier.Load() {
		t.Fatal("test did not write the earlier file before failing")
	}
	fi, err := os.Stat(filepath.Join(dst, "earlier"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() == 0400 || fi.ModTime().Equal(stamp) {
		t.Fatal("file metadata escaped the successful content-settlement gate")
	}
}
