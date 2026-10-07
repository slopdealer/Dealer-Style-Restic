package index

import (
	"fmt"
	"math"
	"strconv"
	"sync"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// The oracle deliberately materializes Lookup results exactly as the checker
// did before ContainsPackBlob. Do not implement it in terms of the predicate.
func lookupContains(mi *MasterIndex, packID restic.ID, blob pack.Blob) bool {
	for _, pb := range mi.Lookup(blob.BlobHandle) {
		if pb.PackID() == packID && pb.Blob == blob {
			return true
		}
	}
	return false
}

type membershipQuery struct {
	packID restic.ID
	blob   pack.Blob
}

func membershipFixture() (*MasterIndex, []membershipQuery) {
	mi := NewMasterIndex()
	shared := restic.Hash([]byte("shared data and tree id"))
	var qs []membershipQuery
	for i := range 4 {
		pid := restic.Hash([]byte(fmt.Sprint("pack-", i)))
		idx := NewIndex()
		for _, typ := range []restic.BlobType{restic.DataBlob, restic.TreeBlob} {
			for j := range 3 {
				b := pack.Blob{BlobHandle: restic.BlobHandle{ID: shared, Type: typ}, Offset: uint(i*100 + j*10), Length: uint(31 + j), UncompressedLength: uint(j * 100)}
				idx.StorePack(pid, pack.Blobs{b, b}) // identical and differing duplicates
				qs = append(qs, membershipQuery{pid, b})
			}
		}
		// Give finalized indexes IDs so MergeFinalIndexes actually merges them.
		idx.Finalize()
		if err := idx.SetID(restic.Hash([]byte(fmt.Sprint("index-", i)))); err != nil {
			panic(err)
		}
		mi.Insert(idx)
	}
	return mi, qs
}

func TestPackMembershipEqualityThroughMerge(t *testing.T) {
	mi, qs := membershipFixture()
	// Include values exactly at uint32's limit, as well as wider mismatches.
	edgeID := restic.Hash([]byte("edge pack"))
	edge := pack.Blob{BlobHandle: restic.BlobHandle{ID: restic.Hash([]byte("edge blob")), Type: restic.DataBlob}, Offset: math.MaxUint32, Length: math.MaxUint32, UncompressedLength: math.MaxUint32}
	idx := NewIndex()
	idx.StorePack(edgeID, pack.Blobs{edge})
	mi.Insert(idx)
	qs = append(qs, membershipQuery{edgeID, edge})
	queries := append([]membershipQuery(nil), qs...)
	for _, q := range qs {
		b := q.blob
		b.Offset++
		queries = append(queries, membershipQuery{q.packID, b})
		b = q.blob
		b.Length++
		queries = append(queries, membershipQuery{q.packID, b})
		b = q.blob
		b.UncompressedLength++
		queries = append(queries, membershipQuery{q.packID, b})
		b = q.blob
		b.ID[0] ^= 0x80
		queries = append(queries, membershipQuery{q.packID, b})
		p := q.packID
		p[0] ^= 0x80
		queries = append(queries, membershipQuery{p, q.blob})
		if strconv.IntSize > 32 {
			wide := uint(uint64(1) << 32)
			b = q.blob
			b.Offset += wide
			queries = append(queries, membershipQuery{q.packID, b})
			b = q.blob
			b.Length += wide
			queries = append(queries, membershipQuery{q.packID, b})
			b = q.blob
			b.UncompressedLength += wide
			queries = append(queries, membershipQuery{q.packID, b})
		}
	}
	verify := func(stage string) {
		for i, q := range queries {
			want := lookupContains(mi, q.packID, q.blob)
			if got := mi.ContainsPackBlob(q.packID, q.blob); got != want {
				t.Fatalf("%s query %d: got %v want %v", stage, i, got, want)
			}
		}
	}
	verify("before merge")
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	if len(mi.idx) != 2 {
		t.Fatalf("expected merged final and unfinalized index, got %d", len(mi.idx))
	}
	verify("after merge")
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	verify("after repeated merge")
	t.Logf("executed %d differential queries at each of three merge stages", len(queries))
}

func TestPackMembershipConcurrentStoreMerge(t *testing.T) {
	mi, qs := membershipFixture()
	// This mutable index is not merged; readers must hold both master and inner
	// read locks while its hash-table backing storage grows.
	mutable := NewIndex()
	mi.Insert(mutable)
	start := make(chan struct{})
	failures := make(chan string, 8)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for range 100 {
				for _, q := range qs {
					if !mi.ContainsPackBlob(q.packID, q.blob) || !lookupContains(mi, q.packID, q.blob) {
						failures <- "stable location lost"
						return
					}
					bad := q.blob
					bad.Length += uint(1 << 20)
					if mi.ContainsPackBlob(q.packID, bad) || lookupContains(mi, q.packID, bad) {
						failures <- "false location matched"
						return
					}
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := range 200 {
			pid := restic.Hash([]byte(fmt.Sprint("new-pack-", i)))
			b := pack.Blob{BlobHandle: restic.BlobHandle{ID: restic.Hash([]byte(fmt.Sprint("new-blob-", i))), Type: restic.DataBlob}, Offset: uint(i), Length: 42}
			if i%2 == 0 {
				mutable.StorePack(pid, pack.Blobs{b})
			} else {
				mi.storePack(pid, pack.Blobs{b})
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-start
		for i := range 30 {
			idx := NewIndex()
			idx.StorePack(restic.Hash([]byte(fmt.Sprint("merge-pack-", i))), pack.Blobs{qs[i%len(qs)].blob})
			idx.Finalize()
			if err := idx.SetID(restic.Hash([]byte(fmt.Sprint("merge-index-", i)))); err != nil {
				failures <- err.Error()
				return
			}
			mi.Insert(idx)
			if err := mi.MergeFinalIndexes(); err != nil {
				failures <- err.Error()
				return
			}
		}
	}()
	close(start)
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Error(msg)
	}
	// Mutating queries are compared only after settlement; two separate reads
	// during mutation need not observe the same generation.
	for pb := range mi.idx[0].Values() {
		if !mi.ContainsPackBlob(pb.Pack, pb.Blob) || !lookupContains(mi, pb.Pack, pb.Blob) {
			t.Fatal("settled merged location lost")
		}
	}
	for i := range 200 {
		pid := restic.Hash([]byte(fmt.Sprint("new-pack-", i)))
		b := pack.Blob{BlobHandle: restic.BlobHandle{ID: restic.Hash([]byte(fmt.Sprint("new-blob-", i))), Type: restic.DataBlob}, Offset: uint(i), Length: 42}
		if !mi.ContainsPackBlob(pid, b) || !lookupContains(mi, pid, b) {
			t.Fatalf("settled stored location %d lost", i)
		}
	}
	t.Log("executed four readers with direct/master stores and 30 index insert/merge operations")
}

func TestPackMembershipInvalidTypePreservesPanic(t *testing.T) {
	mi := NewMasterIndex()
	b := pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.NumBlobTypes}}
	panics := func(f func()) (p any) { defer func() { p = recover() }(); f(); return }
	if panics(func() { lookupContains(mi, restic.ID{}, b) }) == nil || panics(func() { mi.ContainsPackBlob(restic.ID{}, b) }) == nil {
		t.Fatal("invalid type panic changed")
	}
}
