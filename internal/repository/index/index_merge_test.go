package index

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Preserve the old merge loop as an independent oracle. Both implementations
// receive freshly constructed, identical predecoded entries.
func mergeUnfusedForTest(dst, src *Index) {
	packOffset := uint32(len(dst.packs))
	dst.packs = append(dst.packs, src.packs...)
	for typ := range src.byType {
		m := &dst.byType[typ]
		for incoming := range src.byType[typ].values() {
			found := false
			for existing := range m.valuesWithID(incoming.id) {
				if *dst.toPackedBlob(existing, restic.BlobType(typ)) == *src.toPackedBlob(incoming, restic.BlobType(typ)) {
					found = true
					break
				}
			}
			if !found {
				m.add(incoming.id, incoming.packIndex+packOffset, incoming.offset, incoming.length, incoming.uncompressedLength)
			}
		}
	}
	dst.ids = append(dst.ids, src.ids...)
}

func mergeTestID(n uint64) restic.ID {
	var id restic.ID
	binary.LittleEndian.PutUint64(id[:], n+1)
	return id
}

func mergeTestInput(count, generation int) *Index {
	idx := NewIndex()
	for i := 0; i < count; i++ {
		// Repeated IDs and repeated pack records exercise duplicates within
		// each input and across merges, with multiple locations for one ID.
		blob := pack.Blob{
			BlobHandle: restic.BlobHandle{Type: restic.DataBlob + restic.BlobType(i%2), ID: mergeTestID(uint64(i % 113))},
			Offset: uint(i % 19), Length: 33,
		}
		packID := mergeTestID(uint64(1000 + i%7))
		if i%5 == 0 {
			// Each location field is independently significant.
			switch generation % 4 {
			case 0:
				packID = mergeTestID(uint64(2000 + i%7))
			case 1:
				blob.Offset++
			case 2:
				blob.Length++
			case 3:
				blob.UncompressedLength = 71
			}
		}
		blobs := pack.Blobs{blob}
		if i%37 == 0 {
			blobs = append(blobs, blob)
		}
		idx.StorePack(packID, blobs)
	}
	idx.ids = append(idx.ids, mergeTestID(uint64(10000+generation)))
	idx.Finalize()
	return idx
}

func mergeTestCompare(t *testing.T, got, want *Index) {
	t.Helper()
	var gotBytes, wantBytes bytes.Buffer
	if err := got.Encode(&gotBytes); err != nil {
		t.Fatal(err)
	}
	if err := want.Encode(&wantBytes); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(gotBytes.Bytes(), wantBytes.Bytes()) || !reflect.DeepEqual(got.ids, want.ids) || !reflect.DeepEqual(got.packs, want.packs) {
		t.Fatal("merge changed encoding, index IDs or pack table")
	}
	for typ := range restic.NumBlobTypes {
		if got.Len(typ) != want.Len(typ) {
			t.Fatalf("type %v: different entry counts", typ)
		}
		for e := range want.byType[typ].values() {
			bh := restic.BlobHandle{Type: typ, ID: e.id}
			if got.BlobIndex(bh) != want.BlobIndex(bh) ||
				!reflect.DeepEqual(got.Lookup(bh, nil), want.Lookup(bh, nil)) {
				t.Fatal("merge changed first index or lookup chain order")
			}
		}
	}
}

func TestFusedMergeMatchesUnfused(t *testing.T) {
	for _, reserved := range []bool{false, true} {
		for _, initial := range []int{0, 19, 257} {
			t.Run(fmt.Sprintf("reserved=%v/initial=%v", reserved, initial), func(t *testing.T) {
				got := mergeTestInput(initial, 0)
				want := mergeTestInput(initial, 0)
				if reserved {
					for typ := range restic.NumBlobTypes {
						got.Preallocate(typ, 12000)
						want.Preallocate(typ, 12000)
					}
				}
				for generation, count := range []int{0, 513, 4096, 513, 513} {
					src := mergeTestInput(count, generation)
					unchanged := mergeTestInput(count, generation)
					mergeUnfusedForTest(want, unchanged)
					if err := got.merge(src); err != nil {
						t.Fatal(err)
					}
					mergeTestCompare(t, got, want)
					mergeTestCompare(t, src, unchanged)
				}
			})
		}
	}
}

// This microbenchmark isolates lookup/insertion on identical predecoded inputs.
// It is deliberately separate from JSON interpretation and command wall time.
func BenchmarkMergePredecoded(b *testing.B) {
	src := mergeTestInput(65536, 0)
	for _, fused := range []bool{false, true} {
		b.Run(fmt.Sprintf("fused=%v", fused), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				dst := NewIndex()
				for typ := range restic.NumBlobTypes {
					dst.Preallocate(typ, int(src.Len(typ)))
				}
				b.StartTimer()
				if fused {
					if err := dst.merge(src); err != nil {
						b.Fatal(err)
					}
				} else {
					mergeUnfusedForTest(dst, src)
				}
			}
		})
	}
}
