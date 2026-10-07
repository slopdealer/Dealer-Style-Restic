package index

import (
	"fmt"
	"math"
	"sync"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func b06LookupContains(mi *MasterIndex, id restic.ID, blob pack.Blob) bool {
	for _, pb := range mi.Lookup(blob.BlobHandle) {
		if pb.PackID() == id && pb.Blob == blob {
			return true
		}
	}
	return false
}

// Queries are not index entries: oversized query fields must be compared in
// full, even though the index stores only uint32 fields.
func b06MembershipEquality(t testing.TB) {
	t.Helper()
	mi := NewMasterIndex()
	id := restic.Hash([]byte("shared data/tree ID"))
	packs := []restic.ID{restic.Hash([]byte("first pack")), restic.Hash([]byte("second pack"))}
	blobs := []pack.Blob{
		{BlobHandle: restic.BlobHandle{ID: id, Type: restic.DataBlob}, Offset: 13, Length: 31},
		{BlobHandle: restic.BlobHandle{ID: id, Type: restic.DataBlob}, Offset: 61, Length: 47, UncompressedLength: 99},
		{BlobHandle: restic.BlobHandle{ID: id, Type: restic.TreeBlob}, Offset: 111, Length: 67},
		{BlobHandle: restic.BlobHandle{ID: id, Type: restic.DataBlob}, Offset: math.MaxUint32, Length: math.MaxUint32, UncompressedLength: math.MaxUint32},
	}
	for i, pid := range packs {
		idx := NewIndex()
		idx.StorePack(pid, pack.Blobs{blobs[i], blobs[2], blobs[3], blobs[i]})
		idx.Finalize()
		if err := idx.SetID(restic.Hash([]byte(fmt.Sprint("index", i)))); err != nil {
			t.Fatal(err)
		}
		mi.Insert(idx)
	}
	queries := append([]pack.Blob(nil), blobs...)
	for _, blob := range blobs {
		q := blob
		q.Offset++
		queries = append(queries, q)
		q = blob
		q.Length++
		queries = append(queries, q)
		q = blob
		q.UncompressedLength++
		queries = append(queries, q)
		q = blob
		q.Type = restic.TreeBlob
		if blob.Type == restic.TreeBlob {
			q.Type = restic.DataBlob
		}
		queries = append(queries, q)
		q = blob
		q.ID = restic.Hash([]byte("absent ID"))
		queries = append(queries, q)
		if uint64(^uint(0)) > math.MaxUint32 {
			for field := range 3 {
				q = blob
				switch field {
				case 0:
					oversized := uint64(1) << 32
					q.Offset += uint(oversized)
				case 1:
					oversized := uint64(1) << 32
					q.Length += uint(oversized)
				case 2:
					oversized := uint64(1) << 32
					q.UncompressedLength += uint(oversized)
				}
				queries = append(queries, q)
			}
		}
	}
	packs = append(packs, restic.Hash([]byte("absent pack")))
	check := func() {
		for _, pid := range packs {
			for _, q := range queries {
				if got, want := mi.ContainsPackBlob(pid, q), b06LookupContains(mi, pid, q); got != want {
					t.Fatalf("membership changed: pack=%v query=%+v got=%v want=%v", pid, q, got, want)
				}
			}
		}
	}
	check()
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	check()
	// Returned lookup objects and the caller's query do not borrow index fields.
	borrowed := mi.Lookup(blobs[0].BlobHandle)
	for _, pb := range borrowed {
		pb.Blob.Offset = 12345
	}
	check()
	t.Logf("B06 membership equality: %d queries across %d packs before/after merge and caller mutation", len(queries), len(packs))
}

// Stable anchors let readers test meaningful answers while mutations happen.
// Comparing two separately locked calls for a newly inserted value would
// incorrectly require them to observe the same instant.
func b06MembershipConcurrentSettlement(t testing.TB) {
	t.Helper()
	mi := NewMasterIndex()
	pid := restic.Hash([]byte("stable pack"))
	anchor := pack.Blob{BlobHandle: restic.BlobHandle{ID: restic.Hash([]byte("anchor")), Type: restic.DataBlob}, Offset: 17, Length: 31}
	mi.storePack(pid, pack.Blobs{anchor})
	missing := anchor
	missing.Length++
	const count = 128
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			<-start
			for range 512 {
				if !mi.ContainsPackBlob(pid, anchor) || mi.ContainsPackBlob(pid, missing) {
					t.Error("stable answer changed during insert/merge")
					return
				}
			}
		})
	}
	wg.Go(func() {
		<-start
		for i := range count {
			blob := anchor
			blob.Offset = uint(i + 100)
			mi.storePack(restic.Hash([]byte(fmt.Sprint("new pack", i))), pack.Blobs{blob})
			if i%16 == 0 {
				for j, idx := range mi.finalizeNotFinalIndexes() {
					if err := idx.SetID(restic.Hash([]byte(fmt.Sprint("new index", i, j)))); err != nil {
						t.Error(err)
					}
				}
				if err := mi.MergeFinalIndexes(); err != nil {
					t.Error(err)
				}
			}
		}
	})
	close(start)
	wg.Wait()
	for i := range count {
		blob := anchor
		blob.Offset = uint(i + 100)
		id := restic.Hash([]byte(fmt.Sprint("new pack", i)))
		if !mi.ContainsPackBlob(id, blob) || !b06LookupContains(mi, id, blob) {
			t.Fatalf("settled insertion %d missing", i)
		}
		blob.Offset++
		if mi.ContainsPackBlob(id, blob) != b06LookupContains(mi, id, blob) {
			t.Fatalf("settled mismatch %d differs", i)
		}
	}
	t.Log("B06 membership concurrency: four readers, 128 insertions, eight merges and 256 settled comparisons")
}

func TestB06MembershipEquality(t *testing.T)             { b06MembershipEquality(t) }
func TestB06MembershipConcurrentSettlement(t *testing.T) { b06MembershipConcurrentSettlement(t) }
