package index

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func boundedPackFixture(blobsPerPack int) (*MasterIndex, restic.IDSet) {
	mi := NewMasterIndex()
	idx := NewIndex()
	mi.idx = []*Index{idx}
	packs := restic.NewIDSet()
	for i := 0; i < 19; i++ {
		var id restic.ID
		id[0], id[1] = byte(i), 1
		packs.Insert(id)
		var blobs pack.Blobs
		for j := 0; j < blobsPerPack; j++ {
			var bh restic.BlobHandle
			bh.Type = restic.DataBlob
			if j%2 == 0 {
				bh.Type = restic.TreeBlob
			}
			bh.ID[0], bh.ID[1], bh.ID[2] = byte(j), byte(j>>8), 1
			blobs = append(blobs, pack.Blob{BlobHandle: bh, Offset: uint(j * 100), Length: 100, UncompressedLength: uint(j)})
		}
		idx.StorePack(id, blobs)
	}
	var missing restic.ID
	missing[0], missing[1] = 31, 1
	packs.Insert(missing)
	return mi, packs
}

func collectPackChannel(t *testing.T, ch <-chan PackBlobs) map[restic.ID]pack.Blobs {
	t.Helper()
	out := make(map[restic.ID]pack.Blobs)
	last := byte(0)
	for pb := range ch {
		if _, exists := out[pb.PackID]; exists {
			t.Fatal("pack emitted twice")
		}
		group := pb.PackID[0] & 0xf
		if group < last {
			t.Fatal("low-nibble group progression changed")
		}
		last = group
		out[pb.PackID] = pb.Blobs
	}
	return out
}

func TestListPacksBoundedMatchesGroupedAndFallback(t *testing.T) {
	for _, count := range []int{0, 23, 1000} {
		mi, packs := boundedPackFixture(count)
		want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
		got := collectPackChannel(t, mi.ListPacksForRepack(context.Background(), packs))
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("per-pack entries or order differ for count %d", count)
		}
	}
}

func TestCollectSelectedPacksBoundaries(t *testing.T) {
	mi, packs := boundedPackFixture(23)
	if got, ok := mi.collectSelectedPacks(context.Background(), packs, len(packs)-1, 8192); ok || got != nil {
		t.Fatal("pack cardinality overflow accepted")
	}
	if got, ok := mi.collectSelectedPacks(context.Background(), packs, len(packs), 19*32-1); ok || got != nil {
		t.Fatal("slice capacity overflow accepted or partial data escaped")
	}
	got, ok := mi.collectSelectedPacks(context.Background(), packs, len(packs), 19*32)
	if !ok || len(got) != len(packs) {
		t.Fatal("exact capacity boundary failed")
	}
	capacity := 0
	for _, blobs := range got {
		capacity += cap(blobs)
	}
	if capacity != 19*32 {
		t.Fatalf("capacity=%d", capacity)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range mi.ListPacksForRepack(ctx, packs) {
		t.Fatal("cancelled collection published a pack")
	}
}

// The large selection overflows collection before the first emission. Cancel
// while the same owner is blocked publishing fallback output, then verify its
// channel has closed and every index read lock has been released.
func TestListPacksFallbackCancellationJoins(t *testing.T) {
	mi, packs := boundedPackFixture(1000)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := mi.ListPacksForRepack(ctx, packs)
	select {
	case _, ok := <-ch:
		if !ok {
			t.Fatal("fallback produced no packs")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fallback did not start")
	}
	cancel()
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled fallback did not join")
	}
	unlocked := make(chan struct{})
	go func() {
		mi.idxMutex.Lock()
		defer mi.idxMutex.Unlock()
		for _, idx := range mi.idx {
			idx.m.Lock()
			idx.m.Unlock()
		}
		close(unlocked)
	}()
	select {
	case <-unlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("closed fallback retained index locks")
	}
}

func TestRepackValuesMatchesPointerValues(t *testing.T) {
	mi, _ := boundedPackFixture(23)
	var want, got []pack.PackedBlob
	for pb := range mi.Values() {
		want = append(want, *pb)
	}
	for pb := range mi.packedBlobValues() {
		got = append(got, pb)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatal("value traversal changed entries or order")
	}
	count := 0
	for range mi.packedBlobValues() {
		count++
		break
	}
	if count != 1 {
		t.Fatal("early stop failed")
	}
	done := make(chan struct{})
	go func() {
		mi.idxMutex.Lock()
		defer mi.idxMutex.Unlock()
		for _, idx := range mi.idx {
			idx.m.Lock()
			idx.m.Unlock()
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("early stop retained locks")
	}
}

// Separates conversion from storage: every entry is visited, but only copied
// selected values survive the boundary. No command-level speed claim follows.
func BenchmarkRepackValueBoundary(b *testing.B) {
	mi, _ := boundedPackFixture(1000)
	for _, values := range []bool{false, true} {
		name := "pointer"
		if values {
			name = "value"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				count := 0
				if values {
					for range mi.packedBlobValues() {
						count++
					}
				} else {
					for range mi.Values() {
						count++
					}
				}
				if count != 19000 {
					b.Fatal(count)
				}
			}
		})
	}
}

type valueHeaderLister struct{ mi *MasterIndex }

func (l valueHeaderLister) ListBlobs(ctx context.Context, fn func(restic.PackBlob)) error {
	for pb := range l.mi.Values() {
		if err := ctx.Err(); err != nil {
			return err
		}
		fn(pb)
	}
	return nil
}
func TestValuePackHeaderSizesDifferential(t *testing.T) {
	mi, _ := boundedPackFixture(23)
	// A second, finalized index repeats all locations: header arithmetic must
	// count these exactly as the ordinary ListBlobs contract does.
	dup := NewIndex()
	for group := range mi.idx[0].EachByPack(context.Background(), nil) {
		dup.StorePack(group.PackID, group.Blobs)
	}
	dup.Finalize()
	mi.idx = append(mi.idx, dup)
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		want, we := pack.Size(ctx, valueHeaderLister{mi}, true)
		got, ge := mi.PackHeaderSizes(ctx)
		cancel()
		if !reflect.DeepEqual(want, got) || we != ge {
			t.Fatalf("headers differ: %v/%v %v/%v", want, we, got, ge)
		}
	}
}
