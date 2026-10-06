package index

import (
	"context"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func mergeTestIndex(rnd *rand.Rand, id byte, ids []restic.ID, blobsPerPack, packs int) *Index {
	idx := NewIndex()
	for p := 0; p < packs; p++ {
		var packID restic.ID
		rnd.Read(packID[:])
		var blobs pack.Blobs
		for b := 0; b < blobsPerPack; b++ {
			t := restic.DataBlob
			if rnd.Intn(4) == 0 {
				t = restic.TreeBlob
			}
			blobs = append(blobs, pack.Blob{
				BlobHandle: restic.BlobHandle{ID: ids[rnd.Intn(len(ids))], Type: t},
				Offset:     uint(b * 100), Length: uint(50 + rnd.Intn(50)),
			})
		}
		idx.StorePack(packID, blobs)
	}
	idx.Finalize()
	if err := idx.SetID(restic.ID{id}); err != nil {
		panic(err)
	}
	return idx
}

// TestEachSlotMatchesLookup: every entry of a merged index with many repeated
// IDs is reported in the order of Values with the slot blobIndex returns, also
// for the entries of an index that is not merged.
func TestEachSlotMatchesLookup(t *testing.T) {
	rnd := rand.New(rand.NewSource(23))
	ids := make([]restic.ID, 3000)
	for i := range ids {
		rnd.Read(ids[i][:])
		ids[i][0] = byte(i % 5) // crowd the bloom bits
	}
	mi := NewMasterIndex()
	for f := 0; f < 6; f++ {
		mi.Insert(mergeTestIndex(rnd, byte(f+1), ids, 40, 30)) // 7200 entries over 3000 IDs: most IDs repeat
	}
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	// one more index that stays outside the merged one
	extra := NewIndex()
	extra.StorePack(restic.ID{9, 9}, pack.Blobs{{BlobHandle: restic.BlobHandle{ID: ids[0], Type: restic.DataBlob}, Length: 60},
		{BlobHandle: restic.BlobHandle{ID: restic.ID{7, 7, 7}, Type: restic.DataBlob}, Offset: 60, Length: 60}})
	mi.Insert(extra)

	var want []pack.PackedBlob
	for pb := range mi.Values() {
		want = append(want, *pb)
	}
	n, repeated := 0, 0
	mi.EachSlot(func(pb *pack.PackedBlob, slot Slot) bool {
		if n >= len(want) || *pb != want[n] {
			t.Fatalf("entry %d differs from Values", n)
		}
		n++
		look := mi.blobIndex(pb.Blob.BlobHandle)
		if slot == SlotUnknown {
			if pb.Pack != (restic.ID{9, 9}) {
				t.Fatalf("unknown slot for a merged entry")
			}
			return true
		}
		if int(slot) != look {
			t.Fatalf("entry %d: slot %d, lookup %d", n, slot, look)
		}
		return true
	})
	if n != len(want) {
		t.Fatalf("walk reported %d of %d entries", n, len(want))
	}
	for typ := range mi.idx[0].byType {
		m := &mi.idx[0].byType[typ]
		for pos := uint(1); pos < m.blockList.Size(); pos++ {
			if m.firstPosition(m.blockList.Ref(pos), pos) != pos {
				repeated++
			}
		}
	}
	if repeated < 1000 {
		t.Fatalf("only %d repeated entries: the test does not cover them", repeated)
	}

	// the *At methods and All agree with Get/Set/Delete
	a, b := NewAssociatedSet[uint8](mi), NewAssociatedSet[uint8](mi)
	i := 0
	mi.EachSlot(func(pb *pack.PackedBlob, slot Slot) bool {
		bh := pb.Blob.BlobHandle
		switch i % 3 {
		case 0:
			a.Set(bh, uint8(i))
			b.SetAt(bh, slot, uint8(i))
		case 1:
			a.Delete(bh)
			b.DeleteAt(bh, slot)
		default:
			a.Insert(bh)
			b.SetAt(bh, slot, 0)
			IncrementAssociatedCount(a, bh)
			IncrementAssociatedCountAt(b, bh, slot)
		}
		i++
		return true
	})
	a.Set(restic.BlobHandle{ID: restic.ID{1, 2, 3}, Type: restic.TreeBlob}, 5)
	b.Set(restic.BlobHandle{ID: restic.ID{1, 2, 3}, Type: restic.TreeBlob}, 5)
	mi.EachSlot(func(pb *pack.PackedBlob, slot Slot) bool {
		bh := pb.Blob.BlobHandle
		av, aok := a.Get(bh)
		bv, bok := b.GetAt(bh, slot)
		if av != bv || aok != bok {
			t.Fatalf("%v: Get %v %v, GetAt %v %v", bh, av, aok, bv, bok)
		}
		return true
	})
	type kv struct {
		bh restic.BlobHandle
		v  uint8
	}
	var got []kv
	for bh, v := range b.All() {
		got = append(got, kv{bh, v})
	}
	var ref []kv
	for k, v := range b.overflow {
		ref = append(ref, kv{k, v})
	}
	for pb := range mi.Values() {
		bh := pb.Handle()
		if _, ok := b.overflow[bh]; ok {
			continue
		}
		if v, ok := b.Get(bh); ok {
			ref = append(ref, kv{bh, v})
		}
	}
	if len(got) != len(ref) {
		t.Fatalf("All yields %d, reference %d", len(got), len(ref))
	}
	for i := range got {
		if got[i] != ref[i] {
			t.Fatalf("All differs at %d", i)
		}
	}

	// concurrent walks and readers share nothing
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			mi.EachSlot(func(pb *pack.PackedBlob, slot Slot) bool {
				av, aok := a.Get(pb.Blob.BlobHandle)
				bv, bok := b.GetAt(pb.Blob.BlobHandle, slot)
				if av != bv || aok != bok {
					t.Errorf("concurrent read differs")
					return false
				}
				return true
			})
		}()
	}
	wg.Wait()
}

type mergeTestRepo struct {
	restic.Unpacked[restic.FileType]
	files  map[restic.ID][]byte
	loaded map[restic.ID]int
	saved  restic.IDSet
	m      sync.Mutex
}

func (r *mergeTestRepo) Connections() uint { return 2 }
func (r *mergeTestRepo) List(_ context.Context, _ restic.FileType, fn func(restic.ID, int64) error) error {
	r.m.Lock()
	ids := make([]restic.ID, 0, len(r.files))
	for id := range r.files {
		ids = append(ids, id)
	}
	r.m.Unlock()
	for _, id := range ids {
		if err := fn(id, 1); err != nil {
			return err
		}
	}
	return nil
}
func (r *mergeTestRepo) LoadUnpacked(_ context.Context, _ restic.FileType, id restic.ID) ([]byte, error) {
	r.m.Lock()
	defer r.m.Unlock()
	r.loaded[id]++
	return r.files[id], nil
}
func (r *mergeTestRepo) SaveUnpacked(_ context.Context, _ restic.FileType, buf []byte) (restic.ID, error) {
	r.m.Lock()
	defer r.m.Unlock()
	id := restic.Hash(buf)
	r.files[id] = append([]byte(nil), buf...)
	if r.saved != nil {
		r.saved.Insert(id)
	}
	return id, nil
}
func (r *mergeTestRepo) RemoveUnpacked(_ context.Context, _ restic.FileType, id restic.ID) error {
	r.m.Lock()
	defer r.m.Unlock()
	delete(r.files, id)
	return nil
}

// TestRewriteKeepsUnread: a full index file with packs of its own is kept
// without a reload; files that share a pack ID, hold an excluded pack or are
// not full go through the ordinary loop, and the result equals a rewrite that
// reloads everything.
func TestRewriteKeepsUnread(t *testing.T) {
	oldFull := Full
	defer func() { Full = oldFull }()
	Full = func(idx *Index) bool {
		idx.m.RLock()
		defer idx.m.RUnlock()
		var blobs uint
		for typ := range idx.byType {
			blobs += idx.byType[typ].len()
		}
		return blobs >= 40 || time.Since(idx.created) >= indexMaxAge
	}

	build := func() (*mergeTestRepo, restic.ID, restic.ID) {
		rnd := rand.New(rand.NewSource(5))
		repo := &mergeTestRepo{files: map[restic.ID][]byte{}, loaded: map[restic.ID]int{}}
		ids := make([]restic.ID, 4000)
		for i := range ids {
			rnd.Read(ids[i][:])
		}
		var shared, excluded restic.ID
		for f := 0; f < 6; f++ {
			idx := NewIndex()
			for p := 0; p < 5; p++ {
				var packID restic.ID
				rnd.Read(packID[:])
				if f == 2 && p == 0 {
					shared = packID
				}
				if f == 4 && p == 0 {
					excluded = packID
				}
				var blobs pack.Blobs
				for b := 0; b < 10; b++ {
					blobs = append(blobs, pack.Blob{BlobHandle: restic.BlobHandle{ID: ids[rnd.Intn(len(ids))], Type: restic.DataBlob}, Offset: uint(b * 100), Length: 60})
				}
				idx.StorePack(packID, blobs)
				if f == 3 && p == 0 {
					// the same pack entry again in another index file
					idx.StorePack(shared, blobs)
				}
			}
			if f == 5 {
				// a small file: not full
				idx = NewIndex()
				idx.StorePack(restic.ID{5}, pack.Blobs{{BlobHandle: restic.BlobHandle{ID: ids[1], Type: restic.DataBlob}, Length: 60}})
			}
			idx.Finalize()
			if _, err := idx.SaveIndex(context.TODO(), repo); err != nil {
				t.Fatal(err)
			}
		}
		return repo, shared, excluded
	}

	run := func(skip bool) (map[restic.ID]int, map[restic.ID]int, int) {
		repo, _, excluded := build()
		before := len(repo.files)
		mi := NewMasterIndex()
		if err := mi.Load(context.TODO(), repo, restic.NoopCounter, nil); err != nil {
			t.Fatal(err)
		}
		if !skip {
			mi.fileNotes = nil
		}
		kept := map[restic.ID]int{}
		for id := range repo.files {
			kept[id] = 0
		}
		repo.loaded = map[restic.ID]int{}
		repo.saved = restic.NewIDSet()
		if err := mi.Rewrite(context.TODO(), repo, restic.NewIDSet(excluded), nil, nil, MasterIndexRewriteOpts{}); err != nil {
			t.Fatal(err)
		}
		reread := map[restic.ID]int{}
		for id, n := range repo.loaded {
			reread[id] = n
		}
		survivors := 0
		for id := range kept {
			if _, ok := repo.files[id]; ok {
				// A newly written output may hash to an old ID. Output grouping
				// is unordered, so compare only inputs kept without rewriting.
				if !repo.saved.Has(id) {
					survivors++
				}
			}
		}
		// packs and blob counts after the rewrite
		after := NewMasterIndex()
		if err := after.Load(context.TODO(), repo, restic.NoopCounter, nil); err != nil {
			t.Fatal(err)
		}
		packs := map[restic.ID]int{}
		for pb := range after.Values() {
			packs[pb.Pack]++
		}
		if _, ok := packs[excluded]; ok {
			t.Fatalf("excluded pack still listed")
		}
		_ = before
		return packs, reread, survivors
	}

	wantPacks, allLoaded, wantSurvivors := run(false)
	gotPacks, loaded, survivors := run(true)
	if len(allLoaded) != 6 {
		t.Fatalf("reference rewrite loaded %d files", len(allLoaded))
	}
	// files 0 and 1 are full with packs of their own: not read again
	if len(loaded) != 4 {
		t.Fatalf("rewrite read %d index files again, want 4", len(loaded))
	}
	if survivors != wantSurvivors {
		t.Fatalf("kept %d old index files, reference kept %d", survivors, wantSurvivors)
	}
	if len(gotPacks) != len(wantPacks) {
		t.Fatalf("%d packs listed, reference %d", len(gotPacks), len(wantPacks))
	}
	for id, n := range wantPacks {
		if gotPacks[id] != n {
			t.Fatalf("pack %v: %d blobs, reference %d", id.Str(), gotPacks[id], n)
		}
	}
}
