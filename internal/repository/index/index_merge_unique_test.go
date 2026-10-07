package index

import (
	"fmt"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func uniqueMergeInput(start, count int) *Index {
	idx := NewIndex()
	for first := start; first < start+count; first += 128 {
		var blobs pack.Blobs
		for i := first; i < first+128 && i < start+count; i++ {
			blobs = append(blobs, pack.Blob{
				BlobHandle: restic.BlobHandle{Type: restic.DataBlob + restic.BlobType(i%2), ID: mergeTestID(uint64(i))},
				Offset:     uint(i % 128 * 64), Length: 33,
			})
		}
		idx.StorePack(mergeTestID(uint64(first/128+1000000)), blobs)
	}
	idx.Finalize()
	return idx
}

// Exercise predominantly new entries and repeated merges across growth, in
// addition to the existing repeated-ID and exact growth-boundary cases.
func TestFusedMergeUniqueEntries(t *testing.T) {
	for _, reserved := range []bool{false, true} {
		t.Run(fmt.Sprint(reserved), func(t *testing.T) {
			got, want := uniqueMergeInput(0, 512), uniqueMergeInput(0, 512)
			if reserved {
				for typ := range restic.NumBlobTypes {
					got.Preallocate(typ, 4096)
					want.Preallocate(typ, 4096)
				}
			}
			for _, start := range []int{256, 4096, 256} {
				src, original := uniqueMergeInput(start, 4096), uniqueMergeInput(start, 4096)
				mergeUnfusedForTest(want, original)
				if err := got.merge(src); err != nil {
					t.Fatal(err)
				}
				mergeTestCompare(t, got, want)
				mergeTestCompare(t, src, original)
			}
		})
	}
}

func BenchmarkMergeUniquePredecoded(b *testing.B) {
	src := uniqueMergeInput(0, 65536)
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
