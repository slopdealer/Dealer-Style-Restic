package repository

import (
	"context"
	"errors"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestPrunePackRunRevisitAndSettlement(t *testing.T) {
	// Mixed types, lengths and duplicate decisions update the same record in
	// order. Include the zero ID, revisited packs, and a second walk.
	ids := []restic.ID{{}, {}, {1}, {1}, {}, {2}, {1}, {2}}
	want := map[restic.ID]packInfo{{}: {usedSize: 41}, {1}: {usedSize: 53}}
	got := make(map[restic.ID]packInfo)
	for id, info := range want {
		got[id] = info
	}
	run := prunePackRun{packs: got}
	for pass := range 2 {
		for i, id := range ids {
			update := func(p *packInfo) {
				if p.usedBlobs > 0 || pass == 0 {
					p.usedBlobs++
					p.usedSize += uint64(i + 1)
				} else {
					p.unusedBlobs++
				}
				p.tpe = restic.BlobType(i%2 + 1)
				p.uncompressed = i%3 == 0
			}
			p := want[id]
			update(&p)
			want[id] = p
			update(run.get(id))
		}
		run.flush()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("pass %d: got %v, want %v", pass, got, want)
		}
		// Settlement is idempotent; a later walk begins from committed records.
		run.flush()
		if run.active {
			t.Fatal("retained active record after settlement")
		}
	}
}

func TestPrunePackRunInterruptedWalkPublishesNoPlan(t *testing.T) {
	for _, failWalk := range []int{2, 3} {
		mi, handles := countValidationIndexForRun(t)
		used := index.NewAssociatedSet[uint8](mi)
		used.Insert(handles[0])
		failure := errors.New("interrupted index walk")
		calls := 0
		lister := headerOverlapLister{list: func(ctx context.Context, fn func(restic.PackBlob)) error {
			calls++
			for i, id := range []restic.ID{{10}, {10}, {11}} {
				fn(&pack.PackedBlob{Pack: id, Blob: pack.Blob{BlobHandle: handles[0], Length: 100, Offset: uint(i * 100)}})
				if calls == failWalk {
					return failure
				}
			}
			return nil
		}}
		keep, packs, err := packInfoFromIndexWithHeaders(context.Background(), lister, used, &PruneStats{}, restic.NewNoopPrinter(), func() (map[restic.ID]int64, error) { return map[restic.ID]int64{{10}: 32, {11}: 32}, nil })
		if !errors.Is(err, failure) || keep != nil || packs != nil {
			t.Fatalf("walk %d published partial plan: %v %v %v", failWalk, keep, packs, err)
		}
	}
}

func countValidationIndexForRun(t *testing.T) (*index.MasterIndex, []restic.BlobHandle) {
	t.Helper()
	h := restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{1}}
	idx := index.NewIndex()
	idx.StorePack(restic.ID{10}, pack.Blobs{{BlobHandle: h, Length: 100}, {BlobHandle: h, Length: 100, Offset: 100}})
	idx.StorePack(restic.ID{11}, pack.Blobs{{BlobHandle: h, Length: 100}})
	idx.Finalize()
	if err := idx.SetID(restic.ID{99}); err != nil {
		t.Fatal(err)
	}
	mi := index.NewMasterIndex()
	mi.Insert(idx)
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	return mi, []restic.BlobHandle{h}
}
