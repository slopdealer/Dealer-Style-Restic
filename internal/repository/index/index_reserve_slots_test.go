package index

import (
	"fmt"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestCountedIndexReservationKeepsGeometry(t *testing.T) {
	for _, count := range []int{1, 15, 16, 17, 255, 256, 257, 4095, 4096, 4097, 65536} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			idx := NewIndex()
			idx.Preallocate(restic.DataBlob, count)
			m := &idx.byType[restic.DataBlob]
			geometry := m.blockList.blockSize
			for i := 0; i < count; i++ {
				var id restic.ID
				for b := range 8 { id[b] = byte(uint64(i+1) >> (8*b)) }
				m.add(id, 0, uint32(i), 10, 0)
				if m.blockList.blockSize != geometry {
					t.Fatalf("entry %d resized counted geometry", i)
				}
				if got := m.firstIndex(id); got != i+1 { t.Fatalf("entry %d moved: %d", i, got) }
			}
			if m.len() != uint(count) || m.blockList.Size() != uint(count+1) { t.Fatal("count or null entry changed") }
		})
	}
}

func TestCountedIndexReservationWithoutBucketGrowth(t *testing.T) {
	idx := NewIndex()
	idx.Preallocate(restic.DataBlob, 16)
	m := &idx.byType[restic.DataBlob]
	for i := range 16 {
		m.add(restic.ID{byte(i+1)}, 0, uint32(i), 10, 0)
	}
	buckets := len(m.buckets)
	idx.Preallocate(restic.DataBlob, 256)
	if len(m.buckets) != buckets { t.Fatal("fixture should not grow buckets") }
	geometry := m.blockList.blockSize
	for i := 16; i < 256; i++ {
		m.add(restic.ID{byte(i), 1}, 0, uint32(i), 10, 0)
		if m.blockList.blockSize != geometry { t.Fatalf("entry %d resized geometry", i) }
	}
	for i := range 16 {
		if got := m.firstIndex(restic.ID{byte(i+1)}); got != i+1 { t.Fatal("reservation lost an existing entry") }
	}
}
