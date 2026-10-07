package repository

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func b18ID(n int) (id restic.ID) { binary.LittleEndian.PutUint64(id[:], uint64(n)); return }
func b18Blob(n int, typ restic.BlobType) pack.Blob {
	return pack.Blob{BlobHandle: restic.BlobHandle{ID: b18ID(n), Type: typ}, Offset: uint(n * 100), Length: 100, UncompressedLength: uint(n%2) * 200}
}

func TestCheckPackTypesDifferential(t *testing.T) {
	ctx := context.Background()
	inputs := []*index.Index{index.NewIndex(), index.NewIndex(), index.NewIndex()}
	// Revisited packs, exact duplicates, split locations, mixed types within and
	// across indexes, the same blob in different packs, and empty pack records.
	inputs[0].StorePack(b18ID(1), pack.Blobs{b18Blob(1, restic.DataBlob), b18Blob(1, restic.DataBlob)})
	inputs[0].StorePack(b18ID(2), pack.Blobs{b18Blob(2, restic.TreeBlob), b18Blob(3, restic.DataBlob)})
	inputs[0].StorePack(b18ID(3), nil)
	inputs[1].StorePack(b18ID(1), pack.Blobs{b18Blob(1, restic.DataBlob), b18Blob(4, restic.TreeBlob)})
	inputs[1].StorePack(b18ID(4), pack.Blobs{b18Blob(1, restic.DataBlob)})
	inputs[2].StorePack(b18ID(1), pack.Blobs{b18Blob(1, restic.DataBlob)})
	got := make(map[restic.ID]restic.BlobType)
	mi := index.NewMasterIndex()
	for n, idx := range inputs {
		for pbs := range idx.EachByPackForCheck(ctx, restic.NewIDSet()) {
			collectCheckPackType(got, pbs)
		}
		idx.Finalize()
		if err := idx.SetID(b18ID(100 + n)); err != nil {
			t.Fatal(err)
		}
		mi.Insert(idx)
	}
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	want, err := computePackTypes(ctx, &Repository{idx: mi})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("grouped=%v merged=%v", got, want)
	}
	if got[b18ID(1)] != restic.InvalidBlob || got[b18ID(2)] != restic.InvalidBlob {
		t.Fatal("mixed summaries lost")
	}
	if _, ok := got[b18ID(3)]; ok {
		t.Fatal("empty pack manufactured")
	}
}

func TestCheckPackTypesRandomized(t *testing.T) {
	rng := rand.New(rand.NewSource(18))
	for trial := 0; trial < 30; trial++ {
		types := make(map[restic.ID]restic.BlobType)
		mi := index.NewMasterIndex()
		for file := 0; file < 5; file++ {
			idx := index.NewIndex()
			for entry := 0; entry < 200; entry++ {
				id := b18ID(rng.Intn(40) + 1)
				blob := b18Blob(rng.Intn(25), restic.BlobType(1+rng.Intn(2)))
				idx.StorePack(id, pack.Blobs{blob})
			}
			for pbs := range idx.EachByPackForCheck(context.Background(), restic.NewIDSet()) {
				collectCheckPackType(types, pbs)
			}
			idx.Finalize()
			if err := idx.SetID(b18ID(100 + file)); err != nil {
				t.Fatal(err)
			}
			mi.Insert(idx)
		}
		if err := mi.MergeFinalIndexes(); err != nil {
			t.Fatal(err)
		}
		want, err := computePackTypes(context.Background(), &Repository{idx: mi})
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(types, want) {
			t.Fatalf("trial %d differs: grouped %v merged %v", trial, types, want)
		}
	}
}

func TestCheckPackTypesFreshAndPreloaded(t *testing.T) {
	mi := index.NewMasterIndex()
	if freshCheckPackTypes(mi) == nil {
		t.Fatal("fresh index ineligible")
	}
	mi.AddPending(restic.BlobHandle{ID: b18ID(1), Type: restic.DataBlob}, 100)
	if freshCheckPackTypes(mi) == nil {
		t.Fatal("pending blob treated as stored")
	}
	idx := index.NewIndex()
	mi.Insert(idx)
	idx.StorePack(b18ID(2), pack.Blobs{b18Blob(1, restic.DataBlob)})
	if freshCheckPackTypes(mi) != nil {
		t.Fatal("unsaved index eligible")
	}
	// Prove that the short eligibility traversal released its locks.
	idx.StorePack(b18ID(3), pack.Blobs{b18Blob(2, restic.TreeBlob)})
}

func TestCheckPackTypesLoadErrorsAndCancellation(t *testing.T) {
	repo, _ := TestRepositoryWithBackend(t, nil, 0, Options{})
	ctx := context.Background()
	idx := index.NewIndex()
	idx.StorePack(b18ID(1), pack.Blobs{b18Blob(1, restic.DataBlob), b18Blob(2, restic.TreeBlob)})
	var buf bytes.Buffer
	if err := idx.Encode(&buf); err != nil {
		t.Fatal(err)
	}
	if _, err := (&internalRepository{repo}).SaveUnpacked(ctx, restic.IndexFile, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	hints, errs := newChecker(repo).LoadIndex(ctx, restic.NoopTerminalCounterFactory)
	if len(errs) != 0 || len(hints) != 1 {
		t.Fatalf("mixed pack hints %v errors %v", hints, errs)
	}
	if _, ok := hints[0].(*ErrMixedPack); !ok {
		t.Fatalf("unexpected hint %T", hints[0])
	}
	// A second load has no callbacks for known indexes but must retain mixedness.
	hints, errs = newChecker(repo).LoadIndex(ctx, restic.NoopTerminalCounterFactory)
	if len(errs) != 0 || len(hints) != 1 {
		t.Fatalf("preloaded hints %v errors %v", hints, errs)
	}
	if _, err := (&internalRepository{repo}).SaveUnpacked(ctx, restic.IndexFile, []byte(`{"packs":[`)); err != nil {
		t.Fatal(err)
	}
	repo.DiscardIndex()
	_, errs = newChecker(repo).LoadIndex(ctx, restic.NoopTerminalCounterFactory)
	if len(errs) == 0 {
		t.Fatal("malformed index accepted")
	}
	repo.DiscardIndex()
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, errs = newChecker(repo).LoadIndex(cancelled, restic.NoopTerminalCounterFactory)
	if len(errs) == 0 {
		t.Fatal("cancelled load accepted")
	}
}

// Both arms include construction of integrity groups, membership and content
// hashes. The baseline adds its ordinary complete-index type traversal; the
// fused arm charges collection while these same groups are alive.
func BenchmarkB18CheckGrouping(b *testing.B) {
	for _, n := range []int{10000, 100000, 1500000} {
		idx := index.NewIndex()
		for i := 0; i < n; i += 1000 {
			blobs := make(pack.Blobs, 0, 1000)
			for j := i; j < i+1000 && j < n; j++ {
				blobs = append(blobs, b18Blob(j, restic.DataBlob))
			}
			idx.StorePack(b18ID(i/1000+1), blobs)
		}
		mi := index.NewMasterIndex()
		mi.Insert(idx)
		repo := &Repository{idx: mi}
		for _, fused := range []bool{false, true} {
			b.Run(fmt.Sprintf("blobs%d/fused%v", n, fused), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					membership := make(map[restic.ID]restic.IDSet)
					hashes := make(map[restic.ID]restic.IDSet)
					types := make(map[restic.ID]restic.BlobType)
					for pbs := range idx.EachByPackForCheck(context.Background(), restic.NewIDSet()) {
						if fused {
							collectCheckPackType(types, pbs)
						}
						membership[pbs.PackID] = restic.NewIDSet(b18ID(99))
						hashes[pbs.PackID] = restic.NewIDSet(index.PackBlobsHashOwned(pbs))
					}
					if !fused {
						var err error
						types, err = computePackTypes(context.Background(), repo)
						if err != nil {
							b.Fatal(err)
						}
					}
					if len(types) != len(membership) || len(hashes) != len(membership) {
						b.Fatal("summary lost packs")
					}
				}
			})
		}
	}
}

// Eligibility is checked before incremental loading may rebuild the index.
// Exercise both changes in that state without assuming that callbacks include
// previously loaded or still-unsaved entries.
func TestCheckPackTypesIndexTransitions(t *testing.T) {
	ctx := context.Background()
	save := func(t *testing.T, repo *Repository, idx *index.Index) restic.ID {
		t.Helper()
		var buf bytes.Buffer
		if err := idx.Encode(&buf); err != nil {
			t.Fatal(err)
		}
		id, err := (&internalRepository{repo}).SaveUnpacked(ctx, restic.IndexFile, buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(t *testing.T, repo *Repository, mixed int) {
		t.Helper()
		hints, errs := newChecker(repo).LoadIndex(ctx, restic.NoopTerminalCounterFactory)
		if len(errs) != 0 || len(hints) != mixed {
			t.Fatalf("hints %v errors %v, want %d mixed", hints, errs, mixed)
		}
		for _, h := range hints {
			if _, ok := h.(*ErrMixedPack); !ok {
				t.Fatalf("unexpected hint %T", h)
			}
		}
	}
	t.Run("empty-known-then-replacement", func(t *testing.T) {
		repo, _ := TestRepositoryWithBackend(t, nil, 0, Options{})
		empty := save(t, repo, index.NewIndex())
		check(t, repo, 0)
		mixed := index.NewIndex()
		mixed.StorePack(b18ID(1), pack.Blobs{b18Blob(1, restic.DataBlob), b18Blob(2, restic.TreeBlob)})
		old := save(t, repo, mixed)
		check(t, repo, 1)
		for _, id := range []restic.ID{empty, old} {
			if err := (&internalRepository{repo}).RemoveUnpacked(ctx, restic.IndexFile, id); err != nil {
				t.Fatal(err)
			}
		}
		replacement := index.NewIndex()
		replacement.StorePack(b18ID(1), pack.Blobs{b18Blob(1, restic.DataBlob)})
		save(t, repo, replacement)
		check(t, repo, 0)
		types, err := computePackTypes(ctx, repo)
		if err != nil || len(types) != 1 || types[b18ID(1)] != restic.DataBlob {
			t.Fatalf("stale mixed type: %v/%v", types, err)
		}
	})
	t.Run("unsaved-plus-new-file", func(t *testing.T) {
		repo, _ := TestRepositoryWithBackend(t, nil, 0, Options{})
		unsaved := index.NewIndex()
		unsaved.StorePack(b18ID(2), pack.Blobs{b18Blob(3, restic.DataBlob), b18Blob(4, restic.TreeBlob)})
		repo.idx.Insert(unsaved)
		stored := index.NewIndex()
		stored.StorePack(b18ID(3), pack.Blobs{b18Blob(5, restic.DataBlob)})
		save(t, repo, stored)
		check(t, repo, 1)
		types, err := computePackTypes(ctx, repo)
		if err != nil || len(types) != 2 || types[b18ID(2)] != restic.InvalidBlob || types[b18ID(3)] != restic.DataBlob {
			t.Fatalf("missing unsaved/stored type: %v/%v", types, err)
		}
	})
}
