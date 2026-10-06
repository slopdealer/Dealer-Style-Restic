package index

import (
	"bytes"
	"encoding/binary"
	"hash/maphash"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// This test uses only merge's existing API, so it can accompany either the
// original loop or the fused lookup/insertion implementation.
func TestMergeDuplicatesAtGrowthBoundary(t *testing.T) {
	idFor := func(n uint64) restic.ID {
		var id restic.ID
		binary.LittleEndian.PutUint64(id[:], n+1)
		return id
	}
	for _, typ := range []restic.BlobType{restic.DataBlob, restic.TreeBlob} {
		t.Run(typ.String(), func(t *testing.T) {
			dst := NewIndex()
			blobs := make(pack.Blobs, 256)
			for i := range blobs {
				blobs[i] = pack.Blob{
					BlobHandle: restic.BlobHandle{Type: typ, ID: idFor(uint64(i))},
					Offset:     uint(i * 64), Length: 33,
				}
			}
			packID := idFor(10000)
			dst.StorePack(packID, blobs)
			dst.Finalize()
			m := &dst.byType[typ]
			if int(m.numentries) != len(m.buckets)*maxLoad {
				t.Fatal("fixture is not at the bucket-growth boundary")
			}
			bucketsBefore := append([]uint(nil), m.buckets...)
			entriesBefore := m.blockList.Size()
			lookupBefore := dst.Lookup(blobs[0].BlobHandle, nil)
			indexBefore := dst.BlobIndex(blobs[0].BlobHandle)

			// Repeated pack records remain valid inputs. They must not trigger
			// growth or allocation when every location already exists.
			src := NewIndex()
			src.StorePack(packID, blobs)
			src.StorePack(packID, blobs)
			src.Finalize()
			var sourceBefore, sourceAfter bytes.Buffer
			if err := src.Encode(&sourceBefore); err != nil {
				t.Fatal(err)
			}
			if err := dst.merge(src); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(bucketsBefore, m.buckets) || entriesBefore != m.blockList.Size() || dst.Len(typ) != 256 {
				t.Fatal("duplicate-only merge changed the entry table")
			}
			if err := src.Encode(&sourceAfter); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(sourceBefore.Bytes(), sourceAfter.Bytes()) {
				t.Fatal("merge changed its source encoding")
			}

			// The very next retained entry must grow the table. A fused helper
			// must use a bucket computed for the new size, not its lookup size.
			// Select an ID whose next bucket bit is set, so a stale bucket
			// would fail deterministically regardless of the randomized seed.
			var incomingID restic.ID
			for n := uint64(256); ; n++ {
				incomingID = idFor(n)
				var h maphash.Hash
				h.SetSeed(m.mh.Seed())
				_, _ = h.Write(incomingID[:])
				if h.Sum64()&uint64(len(bucketsBefore)) != 0 {
					break
				}
			}
			incoming := pack.Blob{
				BlobHandle: restic.BlobHandle{Type: typ, ID: incomingID},
				Offset:     256 * 64, Length: 33,
			}
			next := NewIndex()
			next.StorePack(packID, pack.Blobs{incoming})
			next.Finalize()
			if err := dst.merge(next); err != nil {
				t.Fatal(err)
			}
			if len(m.buckets) != 2*len(bucketsBefore) || dst.Len(typ) != 257 {
				t.Fatal("distinct entry did not grow the table as expected")
			}
			want := []*pack.PackedBlob{{Pack: packID, Blob: incoming}}
			if !reflect.DeepEqual(want, dst.Lookup(incoming.BlobHandle, nil)) {
				t.Fatal("entry inserted during growth is not reachable")
			}
			if dst.BlobIndex(blobs[0].BlobHandle) != indexBefore || !reflect.DeepEqual(lookupBefore, dst.Lookup(blobs[0].BlobHandle, nil)) {
				t.Fatal("growth changed an existing entry's identity or lookup")
			}
		})
	}
}
