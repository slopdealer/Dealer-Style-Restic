package index

import (
	"hash/maphash"
	"math/rand"
	"testing"

	"github.com/restic/restic/internal/restic"
)

// Compare bucket selection with the streaming interface using the same seed.
// Include the zero ID, multiple table sizes, and independent randomized seeds.
func TestIndexMapDirectHashEquivalent(t *testing.T) {
	r := rand.New(rand.NewSource(17))
	for range 8 {
		var m indexMap
		for _, size := range []int{1, 64, 1024, 65536} {
			m.buckets = make([]uint, size)
			for n := 0; n < 257; n++ {
				var id restic.ID
				if n > 0 {
					_, _ = r.Read(id[:])
				}
				var old maphash.Hash
				old.SetSeed(m.mh.Seed())
				_, _ = old.Write(id[:])
				want := uint(old.Sum64()) & uint(size-1)
				if got := m.hash(id); got != want {
					t.Fatalf("size=%d id=%v: got %d want %d", size, id, got, want)
				}
			}
		}
	}
}
