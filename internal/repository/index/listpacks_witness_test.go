package index

// WITNESS (S020 review, mechanism 4: prune and index).
//
// What it shows: on randomized master-index states the S020 pack listings
// (ListPacks = repack_fallback.go with the ordinal bitmap of repack_ordinals.go,
// and ListPacksForRepack = bounded collect, then repack_routes.go, then the
// fallback) return exactly what stock's ListPacks loop returns: the same packs,
// each exactly once, the same blob locations in the same within-pack order,
// requested packs without entries included. Also: the same holds while a
// consumer stores new packs and merges indexes between receives (what repack
// does), and AssociatedCountsInRange equals a loop over All().
//
// States covered: one merged final index; several final indexes; a non-final
// (unsaved) index next to the merged one; duplicate blobs across packs and
// inside a pack; the same pack listed in two index files (pack ID twice in
// Index.packs after the merge); mixed packs; selections below the bounded
// collector's limits, inside the route limit and above it.
//
// OS: any (pure in-memory). Run from the tree root:
//   go test -run 'TestWitnessS020' -count=1 -p 2 ./internal/repository/index

import (
	"context"
	"encoding/binary"
	"fmt"
	"math/rand"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// stockListPacksReference is stock's MasterIndex.ListPacks loop (ba802d42),
// collected into a map. The order of blobs inside a pack is the order of
// Values, as in stock.
func stockListPacksReference(mi *MasterIndex, packs restic.IDSet) map[restic.ID]pack.Blobs {
	out := make(map[restic.ID]pack.Blobs)
	for i := range byte(16) {
		packBlob := make(map[restic.ID]pack.Blobs)
		for p := range packs {
			if p[0]&0xf == i {
				packBlob[p] = nil
			}
		}
		if len(packBlob) == 0 {
			continue
		}
		for pb := range mi.Values() {
			packID := pb.PackID()
			if packs.Has(packID) && packID[0]&0xf == i {
				packBlob[packID] = append(packBlob[packID], pb.Blob)
			}
		}
		for id, blobs := range packBlob {
			out[id] = blobs
		}
	}
	return out
}

func witnessID(rng *rand.Rand) (id restic.ID) {
	binary.LittleEndian.PutUint64(id[:], rng.Uint64())
	binary.LittleEndian.PutUint64(id[8:], rng.Uint64())
	return id
}

type witnessState struct {
	mi      *MasterIndex
	packIDs []restic.ID
	handles []restic.BlobHandle
	layout  string
}

// buildWitnessState makes a random master index. blobsPerPack and nPacks decide
// which listing path a selection of all packs takes.
func buildWitnessState(rng *rand.Rand, nPacks, blobsPerPack int) *witnessState {
	st := &witnessState{mi: NewMasterIndex()}
	pool := make([]restic.BlobHandle, 0, nPacks*blobsPerPack)
	nIndexes := 1 + rng.Intn(4)
	indexes := make([]*Index, nIndexes)
	for i := range indexes {
		indexes[i] = NewIndex()
	}
	type stored struct {
		id    restic.ID
		blobs pack.Blobs
	}
	var all []stored
	for p := 0; p < nPacks; p++ {
		id := witnessID(rng)
		st.packIDs = append(st.packIDs, id)
		packType := restic.BlobType(1 + rng.Intn(2))
		mixed := rng.Intn(20) == 0
		n := 1 + rng.Intn(2*blobsPerPack)
		blobs := make(pack.Blobs, 0, n)
		offset := uint(0)
		for b := 0; b < n; b++ {
			var h restic.BlobHandle
			switch {
			case len(pool) > 0 && rng.Intn(6) == 0:
				h = pool[rng.Intn(len(pool))] // duplicate of an earlier blob
			default:
				h = restic.BlobHandle{ID: witnessID(rng), Type: packType}
				if mixed && rng.Intn(2) == 0 {
					h.Type = restic.BlobType(1 + rng.Intn(2))
				}
				pool = append(pool, h)
			}
			length := uint(40 + rng.Intn(400))
			ul := uint(0)
			if rng.Intn(2) == 0 {
				ul = length * 2
			}
			blobs = append(blobs, pack.Blob{BlobHandle: h, Offset: offset, Length: length, UncompressedLength: ul})
			offset += length
		}
		all = append(all, stored{id, blobs})
		target := indexes[rng.Intn(nIndexes)]
		switch rng.Intn(12) {
		case 0: // same pack listed completely in two index files
			target.StorePack(id, blobs)
			indexes[rng.Intn(nIndexes)].StorePack(id, blobs)
		case 1: // pack split over two index files (old repositories)
			cut := rng.Intn(len(blobs) + 1)
			target.StorePack(id, blobs[:cut])
			indexes[rng.Intn(nIndexes)].StorePack(id, blobs[cut:])
		default:
			target.StorePack(id, blobs)
		}
	}
	st.handles = pool
	st.layout = []string{"merged", "merged+unsaved", "unmerged-final", "merged+unsaved+dup"}[rng.Intn(4)]
	for i, idx := range indexes {
		idx.Finalize()
		if st.layout != "unmerged-final" || i == 0 {
			if err := idx.SetID(witnessID(rng)); err != nil {
				panic(err)
			}
		}
		st.mi.Insert(idx)
	}
	if err := st.mi.MergeFinalIndexes(); err != nil {
		panic(err)
	}
	if st.layout == "merged+unsaved" || st.layout == "merged+unsaved+dup" {
		// an unsaved index: new packs, and for the dup layout also further
		// entries of packs that are already indexed
		for k := 0; k < 3; k++ {
			id := witnessID(rng)
			st.packIDs = append(st.packIDs, id)
			h := restic.BlobHandle{ID: witnessID(rng), Type: restic.DataBlob}
			st.handles = append(st.handles, h)
			st.mi.storePack(id, pack.Blobs{{BlobHandle: h, Length: 77}, {BlobHandle: pool[rng.Intn(len(pool))], Offset: 77, Length: 99}})
		}
		if st.layout == "merged+unsaved+dup" {
			s := all[rng.Intn(len(all))]
			st.mi.storePack(s.id, s.blobs[:1+rng.Intn(len(s.blobs))])
		}
	}
	return st
}

func randomSelection(rng *rand.Rand, st *witnessState, fraction float64) restic.IDSet {
	packs := restic.NewIDSet()
	for _, id := range st.packIDs {
		if rng.Float64() < fraction {
			packs.Insert(id)
		}
	}
	// requested packs that are not in the index must still be emitted
	packs.Insert(witnessID(rng))
	packs.Insert(witnessID(rng))
	return packs
}

func collectWitness(t *testing.T, ch <-chan PackBlobs, between func()) map[restic.ID]pack.Blobs {
	t.Helper()
	out := make(map[restic.ID]pack.Blobs)
	last := byte(0)
	for pb := range ch {
		if _, exists := out[pb.PackID]; exists {
			t.Fatalf("pack %v emitted twice", pb.PackID.Str())
		}
		group := pb.PackID[0] & 0xf
		if group < last {
			t.Fatalf("group progression went back: %d after %d", group, last)
		}
		last = group
		out[pb.PackID] = pb.Blobs
		if between != nil {
			between()
		}
	}
	return out
}

func equalListing(want, got map[restic.ID]pack.Blobs) error {
	if len(want) != len(got) {
		return fmt.Errorf("pack count: want %d, got %d", len(want), len(got))
	}
	for id, w := range want {
		g, ok := got[id]
		if !ok {
			return fmt.Errorf("pack %v missing", id.Str())
		}
		if len(w) == 0 && len(g) == 0 {
			continue
		}
		if !reflect.DeepEqual(w, g) {
			return fmt.Errorf("pack %v: %d blobs wanted, %d listed, or order/content differs", id.Str(), len(w), len(g))
		}
	}
	return nil
}

func TestWitnessS020ListPacksDifferential(t *testing.T) {
	shapes := []struct {
		name                 string
		nPacks, blobsPerPack int
		rounds               int
	}{
		{"small-collect", 40, 20, 60},      // below 512 packs and 8192 blobs
		{"many-packs", 900, 4, 25},         // above 512 packs, inside the route limit
		{"many-blobs", 120, 120, 25},       // above 8192 blobs, inside the route limit
		{"above-route-limit", 300, 200, 8}, // above 65536 slots: fallback
	}
	paths := map[string]int{}
	for _, shape := range shapes {
		for round := 0; round < shape.rounds; round++ {
			seed := int64(round*7919 + shape.nPacks)
			rng := rand.New(rand.NewSource(seed))
			st := buildWitnessState(rng, shape.nPacks, shape.blobsPerPack)
			for _, fraction := range []float64{0, 0.03, 0.5, 1} {
				packs := randomSelection(rng, st, fraction)
				want := stockListPacksReference(st.mi, packs)
				ctx := context.Background()

				got := collectWitness(t, st.mi.ListPacks(ctx, packs), nil)
				if err := equalListing(want, got); err != nil {
					t.Fatalf("%s seed %d layout %s fraction %v: ListPacks differs from stock: %v", shape.name, seed, st.layout, fraction, err)
				}
				got = collectWitness(t, st.mi.ListPacksForRepack(ctx, packs), nil)
				if err := equalListing(want, got); err != nil {
					t.Fatalf("%s seed %d layout %s fraction %v: ListPacksForRepack differs from stock: %v", shape.name, seed, st.layout, fraction, err)
				}

				// which path did ListPacksForRepack take? (bookkeeping only)
				if _, complete := st.mi.collectSelectedPacks(ctx, packs, listPacksCollectPackLimit, listPacksCollectBlobLimit); complete && len(packs) <= listPacksCollectPackLimit {
					paths["collect"]++
				} else if routes, ok := st.mi.buildRepackRoutes(ctx, packs, repackRouteSlots); ok && routes != nil {
					paths["routes"]++
				} else {
					paths["fallback"]++
				}
			}
		}
	}
	t.Logf("paths taken by ListPacksForRepack: %v", paths)
	for _, p := range []string{"collect", "routes", "fallback"} {
		if paths[p] == 0 {
			t.Fatalf("path %q was never exercised: %v", p, paths)
		}
	}
}

// The consumer of ListPacksForRepack (repack) saves blobs while it reads the
// channel: new packs enter an unsaved index, full indexes are finalized and
// merged into the first index. Stock rescans the current master for each of
// the 16 groups; S020's routes hold ordinals taken once. Both must list the
// same locations for the SELECTED packs.
func TestWitnessS020ListPacksWhileIndexGrows(t *testing.T) {
	for round := 0; round < 30; round++ {
		seed := int64(1000 + round)
		rng := rand.New(rand.NewSource(seed))
		var st *witnessState
		for {
			st = buildWitnessState(rng, 700+rng.Intn(300), 6)
			if st.layout == "merged" {
				break
			}
		}
		packs := randomSelection(rng, st, 0.9)
		ctx := context.Background()
		if routes, ok := st.mi.buildRepackRoutes(ctx, packs, repackRouteSlots); !ok || routes == nil {
			t.Fatalf("seed %d: fixture does not take the route path", seed)
		}
		want := stockListPacksReference(st.mi, packs)
		step := 0
		got := collectWitness(t, st.mi.ListPacksForRepack(ctx, packs), func() {
			step++
			// a new pack with new blobs and with copies of old blobs
			id := witnessID(rng)
			var blobs pack.Blobs
			for k := 0; k < 1+rng.Intn(40); k++ {
				h := st.handles[rng.Intn(len(st.handles))]
				if rng.Intn(2) == 0 {
					h = restic.BlobHandle{ID: witnessID(rng), Type: restic.DataBlob}
				}
				blobs = append(blobs, pack.Blob{BlobHandle: h, Offset: uint(k * 500), Length: 500})
			}
			st.mi.storePack(id, blobs)
			if step%7 == 0 {
				for _, idx := range st.mi.finalizeNotFinalIndexes() {
					if err := idx.SetID(witnessID(rng)); err != nil {
						t.Fatal(err)
					}
				}
				if err := st.mi.MergeFinalIndexes(); err != nil {
					t.Fatal(err)
				}
			}
		})
		if err := equalListing(want, got); err != nil {
			t.Fatalf("seed %d: listing changed while the index grew: %v", seed, err)
		}
		// and the same again, compared with a stock rescan of the grown master
		if err := equalListing(stockListPacksReference(st.mi, packs), got); err != nil {
			t.Fatalf("seed %d: listing differs from a stock rescan of the grown index: %v", seed, err)
		}
	}
}

// AssociatedCountsInRange against the loop it replaces (prune.go: the missing
// blob test with [1,255] and the sanity panic with [1,1]).
func TestWitnessS020AssociatedCountsInRangeDifferential(t *testing.T) {
	reference := func(a *AssociatedSet[uint8], lo, hi uint8) bool {
		for _, count := range a.All() {
			if count < lo || count > hi {
				return false
			}
		}
		return true
	}
	stable, slow := 0, 0
	for round := 0; round < 400; round++ {
		rng := rand.New(rand.NewSource(int64(round)))
		st := buildWitnessState(rng, 10+rng.Intn(40), 8)
		a := NewAssociatedSet[uint8](st.mi)
		// grow the index after the set exists (handles then live in overflow or
		// are out of the arrays' range)
		if rng.Intn(4) == 0 {
			extra := NewIndex()
			h := restic.BlobHandle{ID: witnessID(rng), Type: restic.TreeBlob}
			st.handles = append(st.handles, h)
			extra.StorePack(witnessID(rng), pack.Blobs{{BlobHandle: h, Length: 10}})
			extra.Finalize()
			if err := extra.SetID(witnessID(rng)); err != nil {
				t.Fatal(err)
			}
			st.mi.Insert(extra)
			if err := st.mi.MergeFinalIndexes(); err != nil {
				t.Fatal(err)
			}
		}
		maxValue := 1 + rng.Intn(3)
		zeroChance := rng.Intn(5) // 0: no zero values at all
		for _, h := range st.handles {
			if rng.Intn(3) == 0 {
				continue
			}
			v := uint8(1 + rng.Intn(maxValue))
			if zeroChance != 0 && rng.Intn(40*zeroChance) == 0 {
				v = 0
			}
			a.Set(h, v)
		}
		if rng.Intn(5) == 0 {
			// a handle that is in no index: lives in the overflow map
			a.Set(restic.BlobHandle{ID: witnessID(rng), Type: restic.DataBlob}, uint8(rng.Intn(3)))
		}
		if rng.Intn(6) == 0 && len(st.handles) > 0 {
			a.Delete(st.handles[rng.Intn(len(st.handles))])
		}
		if _, supported := associatedCountsInRangeStable(a, 1, 255); supported {
			stable++
		} else {
			slow++
		}
		for _, r := range [][2]uint8{{1, 255}, {1, 1}, {0, 1}, {2, 3}, {0, 255}} {
			want := reference(a, r[0], r[1])
			got := AssociatedCountsInRange(a, r[0], r[1])
			if want != got {
				t.Fatalf("round %d layout %s range %v: loop over All says %v, AssociatedCountsInRange says %v", round, st.layout, r, want, got)
			}
		}
	}
	t.Logf("compact-array path %d times, loop path %d times", stable, slow)
	if stable == 0 || slow == 0 {
		t.Fatal("one of the two paths was never exercised")
	}
}
