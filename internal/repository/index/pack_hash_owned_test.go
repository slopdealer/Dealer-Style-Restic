package index

import (
	"context"
	"math/rand"
	"reflect"
	"slices"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestPackBlobsHashOwnedMatchesPublic(t *testing.T) {
	rng := rand.New(rand.NewSource(17))
	for _, n := range []int{0, 1, 2, 17, 4096} {
		pbs := PackBlobs{Blobs: make(pack.Blobs, n)}
		rng.Read(pbs.PackID[:])
		for i := range pbs.Blobs {
			b := &pbs.Blobs[i]
			rng.Read(b.ID[:])
			b.Type = restic.BlobType(i % 2)
			b.Offset = uint(rng.Intn(8)) // equal offsets with different records
			b.Length = uint(rng.Uint64())
			b.UncompressedLength = uint(rng.Uint64())
			if i > 0 && i%11 == 0 {
				*b = pbs.Blobs[i-1] // exact duplicates are retained
			}
		}
		original := slices.Clone(pbs.Blobs)
		want := PackBlobsHash(pbs)
		if !reflect.DeepEqual(original, pbs.Blobs) {
			t.Fatal("public helper changed its input")
		}
		if got := PackBlobsHashOwned(pbs); got != want {
			t.Fatalf("%d records: got %v, want %v", n, got, want)
		}
	}
}

func TestPackBlobsHashOwnedGroupIsolation(t *testing.T) {
	idx := NewIndex()
	var id restic.ID
	id[0] = 9
	idx.StorePack(id, pack.Blobs{
		{BlobHandle: restic.BlobHandle{ID: id, Type: restic.DataBlob}, Offset: 100, Length: 50},
		{BlobHandle: restic.BlobHandle{ID: id, Type: restic.TreeBlob}, Offset: 0, Length: 100, UncompressedLength: 200},
	})
	var before restic.ID
	for pbs := range idx.EachByPackForCheck(context.Background(), restic.NewIDSet()) {
		want := PackBlobsHash(pbs)
		before = want
		if got := PackBlobsHashOwned(pbs); got != want {
			t.Fatal("group hash changed")
		}
		for i := range pbs.Blobs {
			pbs.Blobs[i] = pack.Blob{}
		}
	}
	for pbs := range idx.EachByPackForCheck(context.Background(), restic.NewIDSet()) {
		if after := PackBlobsHash(pbs); before != after {
			t.Fatal("private group scratch aliases index storage")
		}
	}
}

func TestPackBlobsHashOwnedAllocation(t *testing.T) {
	pbs := PackBlobs{Blobs: make(pack.Blobs, 4096)}
	for i := range pbs.Blobs {
		pbs.Blobs[i].Offset = uint(i)
	}
	public := testing.AllocsPerRun(5, func() { PackBlobsHash(pbs) })
	owned := testing.AllocsPerRun(5, func() { PackBlobsHashOwned(pbs) })
	t.Logf("4096 records: public %.0f allocs, owned %.0f allocs", public, owned)
	if owned >= public {
		t.Fatal("owned hashing did not remove allocations")
	}
}
