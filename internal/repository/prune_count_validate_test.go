package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestPruneCountValidationInterruptedCopy(t *testing.T) {
	// Two original packs have unused data; an interrupted prune wrote a pack
	// containing only the two reachable duplicates. Prefer that rewritten copy.
	a := restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{1}}
	b := restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{2}}
	u := restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{3}}
	v := restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{4}}
	idx := index.NewIndex()
	for i, handles := range [][]restic.BlobHandle{{a, u}, {b, v}, {a, b}} {
		var blobs pack.Blobs
		for offset, h := range handles {
			blobs = append(blobs, pack.Blob{BlobHandle: h, Length: 100, Offset: uint(offset * 100)})
		}
		idx.StorePack(restic.ID{byte(i + 10)}, blobs)
	}
	idx.Finalize()
	if err := idx.SetID(restic.ID{99}); err != nil {
		t.Fatal(err)
	}
	mi := index.NewMasterIndex()
	mi.Insert(idx)
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	repo := &Repository{idx: mi}
	used := index.NewAssociatedSet[uint8](mi)
	used.Insert(a)
	used.Insert(b)
	stats := &PruneStats{}
	_, packs, err := packInfoFromIndex(context.Background(), repo, used, stats, restic.NewNoopPrinter())
	if err != nil {
		t.Fatal(err)
	}
	if packs[restic.ID{12}].usedBlobs != 2 || packs[restic.ID{10}].usedBlobs != 0 || packs[restic.ID{11}].usedBlobs != 0 {
		t.Fatalf("rewritten duplicate-only pack not preferred: %v", packs)
	}
	if stats.Blobs.Used != 2 || stats.Blobs.Duplicate != 2 || stats.Blobs.Unused != 2 {
		t.Fatalf("changed occurrence accounting: %+v", stats.Blobs)
	}
	// The missing-handle diagnostic still precedes settlement of a header error.
	missing := restic.BlobHandle{Type: restic.TreeBlob, ID: restic.ID{77}}
	used.Insert(missing)
	called := false
	_, _, err = packInfoFromIndexWithHeaders(context.Background(), repo, used, &PruneStats{}, restic.NewNoopPrinter(), func() (map[restic.ID]int64, error) {
		called = true
		return nil, errors.New("header failed")
	})
	if err != ErrIndexIncomplete || called {
		t.Fatalf("missing-blob refusal displaced: %v, headers settled=%v", err, called)
	}
}
