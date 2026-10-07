package index

import (
	"encoding/binary"
	"fmt"
	"math/rand"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func countValidationIndex(t testing.TB, entries, unique int) (*MasterIndex, []restic.BlobHandle) {
	t.Helper()
	idx := NewIndex()
	handles := make([]restic.BlobHandle, unique)
	for i := range handles {
		handles[i].Type = restic.DataBlob
		if i%4 == 0 {
			handles[i].Type = restic.TreeBlob
		}
		binary.LittleEndian.PutUint64(handles[i].ID[:], uint64(i+1))
	}
	var blobs pack.Blobs
	for i := 0; i < entries; i++ {
		blobs = append(blobs, pack.Blob{BlobHandle: handles[i%unique], Length: 100, Offset: uint(i%128) * 100})
		if len(blobs) == 128 || i == entries-1 {
			var pid restic.ID
			binary.LittleEndian.PutUint64(pid[:], uint64(i/128+1))
			idx.StorePack(pid, blobs)
			blobs = blobs[:0]
		}
	}
	idx.Finalize()
	if err := idx.SetID(restic.ID{250}); err != nil {
		t.Fatal(err)
	}
	mi := NewMasterIndex()
	mi.Insert(idx)
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	return mi, handles
}

func TestAssociatedCountsInRange(t *testing.T) {
	mi, handles := countValidationIndex(t, 1200, 400)
	a := NewAssociatedSet[uint8](mi)
	check := func(supported bool) {
		t.Helper()
		for _, bounds := range [][2]uint8{{0, 255}, {1, 255}, {1, 1}, {255, 255}} {
			want := true
			for _, count := range a.All() {
				if count < bounds[0] || count > bounds[1] {
					want = false
				}
			}
			got := AssociatedCountsInRange(a, bounds[0], bounds[1])
			fast, ok := associatedCountsInRangeStable(a, bounds[0], bounds[1])
			if got != want || ok != supported || ok && fast != want {
				t.Fatalf("bounds %v: got %v, want %v; fast %v/%v, supported %v", bounds, got, want, fast, ok, supported)
			}
		}
	}
	check(true) // empty membership ignores zero array values
	rnd := rand.New(rand.NewSource(42))
	for i := 0; i < 200; i++ {
		h := handles[rnd.Intn(len(handles))]
		switch i % 5 {
		case 0:
			a.Insert(h) // present zero
		case 1:
			a.Set(h, 1)
		case 2:
			a.Set(h, 255) // saturated occurrence counter
		case 3:
			a.Delete(h)
		case 4:
			IncrementAssociatedCount(a, h)
		}
		check(true)
	}
	missing := restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{255, 255}}
	a.Insert(missing)
	check(false)
	a.Set(missing, 1)
	check(false)
	a.Delete(missing)
	check(true)
	// A shadowing overflow entry must replace the array value, as All does.
	a.Set(handles[0], 0)
	a.overflow[handles[0]] = 1
	check(false)
	delete(a.overflow, handles[0])
	// A newly admitted handle outside the merged index uses overflow.
	extra := NewIndex()
	extra.StorePack(restic.ID{251}, pack.Blobs{{BlobHandle: missing, Length: 100}})
	mi.Insert(extra)
	a.Set(missing, 1)
	check(false)
	extra.Finalize()
	if err := extra.SetID(restic.ID{251}); err != nil {
		t.Fatal(err)
	}
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	// Even after removing overflow, array lengths no longer match the index.
	a.Delete(missing)
	check(false)
}

// Exercise the full uint8 domain with duplicate physical occurrences. A
// membership entry represents one logical handle, regardless of its copies.
func TestAssociatedCountsInRangeDomain(t *testing.T) {
	mi, handles := countValidationIndex(t, 1024, 256)
	a := NewAssociatedSet[uint8](mi)
	// Test each value independently so an early low-count failure cannot hide
	// a broken upper bound later in the membership array.
	for i, h := range handles {
		a.Set(h, uint8(i))
		for _, bounds := range [][2]uint8{{0, 255}, {0, 127}, {1, 255}, {1, 1}, {128, 255}, {100, 20}} {
			want := true
			for _, count := range a.All() {
				if count < bounds[0] || count > bounds[1] {
					want = false
				}
			}
			if got := AssociatedCountsInRange(a, bounds[0], bounds[1]); got != want {
				t.Fatalf("value %d, bounds %v: got %v, want %v", i, bounds, got, want)
			}
		}
		a.Delete(h)
	}
	for i, h := range handles {
		a.Set(h, uint8(i))
	}
	for _, bounds := range [][2]uint8{{0, 255}, {1, 255}, {1, 1}, {128, 255}, {100, 20}} {
		want := true
		for _, count := range a.All() {
			if count < bounds[0] || count > bounds[1] {
				want = false
			}
		}
		if got := AssociatedCountsInRange(a, bounds[0], bounds[1]); got != want {
			t.Fatalf("bounds %v: got %v, want %v", bounds, got, want)
		}
	}
	// Deleting the only admitted zero must make the missing-count test pass.
	a.Delete(handles[0])
	if !AssociatedCountsInRange(a, 1, 255) {
		t.Fatal("deleted zero still participates in validation")
	}
}

func TestAssociatedCountsInRangeNonFinal(t *testing.T) {
	mi := NewMasterIndex()
	mi.idx[0] = NewIndex()
	a := NewAssociatedSet[uint8](mi)
	// A mutable index must use the authoritative iterator even when
	// its current size happens to match the associated arrays.
	if _, supported := associatedCountsInRangeStable(a, 1, 1); supported {
		t.Fatal("accepted mutable index")
	}
	if !AssociatedCountsInRange(a, 1, 1) {
		t.Fatal("empty membership is invalid")
	}
	h := restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{19}}
	a.Set(h, 0)
	if AssociatedCountsInRange(a, 1, 255) {
		t.Fatal("overflow missing handle was ignored")
	}
	a.Set(h, 1)
	if !AssociatedCountsInRange(a, 1, 1) {
		t.Fatal("overflow count was ignored")
	}
}

func BenchmarkAssociatedCountValidation(b *testing.B) {
	for _, entries := range []int{100000, 1500000} {
		for _, density := range []int{1, 10} {
			b.Run(fmt.Sprintf("entries=%d/density=1in%d", entries, density), func(b *testing.B) {
				mi, handles := countValidationIndex(b, entries, entries)
				a := NewAssociatedSet[uint8](mi)
				for i := 0; i < len(handles); i += density {
					a.Set(handles[i], 1)
				}
				for _, compact := range []bool{false, true} {
					b.Run(fmt.Sprintf("compact=%v", compact), func(b *testing.B) {
						b.ReportAllocs()
						for i := 0; i < b.N; i++ {
							if compact {
								if !AssociatedCountsInRange(a, 1, 1) {
									b.Fatal("invalid count")
								}
							} else {
								for _, count := range a.All() {
									if count != 1 {
										b.Fatal("invalid count")
									}
								}
							}
						}
					})
				}
			})
		}
	}
}
