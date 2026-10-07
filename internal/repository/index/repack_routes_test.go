package index

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func routeID(n int) (id restic.ID) {
	binary.LittleEndian.PutUint64(id[:], uint64(n))
	return id
}

func routeFixture(n, selected int) (*MasterIndex, restic.IDSet) {
	idx := NewIndex()
	packs := restic.NewIDSet()
	for i := 0; i < n; i += 1000 {
		id := routeID(i/1000 + 1)
		if i/1000 < selected {
			packs.Insert(id)
		}
		blobs := make(pack.Blobs, 0, 1000)
		for j := i; j < min(i+1000, n); j++ {
			blobs = append(blobs, pack.Blob{BlobHandle: restic.BlobHandle{ID: routeID(j + 1), Type: restic.BlobType(1 + j%2)}, Offset: uint((j - i) * 100), Length: 100, UncompressedLength: uint(j%3) * 200})
		}
		idx.StorePack(id, blobs)
	}
	idx.Finalize()
	mi := NewMasterIndex()
	mi.idx = []*Index{idx}
	packs.Insert(routeID(n + 42)) // absent requested packs must still be emitted
	return mi, packs
}

func routeOutput(t *testing.T, mi *MasterIndex, packs restic.IDSet, limit int) map[restic.ID]pack.Blobs {
	t.Helper()
	routes, ok := mi.buildRepackRoutes(context.Background(), packs, limit)
	if !ok || routes == nil {
		t.Fatal("fixture rejected")
	}
	got := make(map[restic.ID]pack.Blobs)
	for group := range byte(16) {
		for id, blobs := range routes.partition(context.Background(), packs, group) {
			got[id] = blobs
		}
	}
	return got
}

func TestRepackRoutesDifferential(t *testing.T) {
	mi, packs := routeFixture(20000, 19)
	// Repeat exact locations and add another location of one blob in a mixed
	// pack, as distinct locations must survive even when identities coincide.
	dup := NewIndex()
	dup.StorePack(routeID(1), pack.Blobs{{BlobHandle: restic.BlobHandle{ID: routeID(1), Type: restic.DataBlob}, Length: 100}, {BlobHandle: restic.BlobHandle{ID: routeID(1), Type: restic.DataBlob}, Offset: 1234, Length: 100}})
	dup.Finalize()
	if err := mi.idx[0].merge(dup); err != nil {
		t.Fatal(err)
	}
	want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
	got := routeOutput(t, mi, packs, repackRouteSlots)
	if !reflect.DeepEqual(got, want) {
		t.Fatal("locations, mixed types, missing packs or within-pack order changed")
	}
}

func TestRepackRoutesBoundsAndUnsupported(t *testing.T) {
	mi, packs := routeFixture(20000, 19)
	if routes, ok := mi.buildRepackRoutes(context.Background(), packs, 1); ok || routes != nil {
		t.Fatal("overflow published partial routes")
	}
	routes, ok := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots)
	if !ok {
		t.Fatal("supported fixture rejected")
	}
	capacity := 0
	for _, slots := range routes.groups {
		capacity += cap(slots)
	}
	if _, ok := mi.buildRepackRoutes(context.Background(), packs, capacity); !ok {
		t.Fatal("exact capacity rejected")
	}
	if _, ok := mi.buildRepackRoutes(context.Background(), packs, capacity-1); ok {
		t.Fatal("capacity overflow accepted")
	}
	mi.idx[0].final = false
	if _, ok := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots); ok {
		t.Fatal("mutable index accepted")
	}
	mi.idx[0].final = true
	mi.idx = append(mi.idx, NewIndex())
	if _, ok := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots); ok {
		t.Fatal("multiple indexes accepted")
	}
}

func TestRepackRoutesProductionAndOverflow(t *testing.T) {
	for _, n := range []int{0, 10000, 20000, 100000} {
		mi, packs := routeFixture(n, 128)
		want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
		got := collectPackChannel(t, mi.ListPacksForRepack(context.Background(), packs))
		if !reflect.DeepEqual(want, got) {
			t.Fatalf("production output differs for %d entries", n)
		}
	}
}

func TestRepackRoutesMergeAndReplacement(t *testing.T) {
	mi, packs := routeFixture(20000, 19)
	want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
	routes, ok := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots)
	if !ok {
		t.Fatal("routes rejected")
	}
	// Existing ordinals survive append-only merges, including a duplicate.
	// Cross HAT block relocation and hash/pack-table growth after capture.
	other, _ := routeFixture(100000, 1)
	if err := mi.idx[0].merge(other.idx[0]); err != nil {
		t.Fatal(err)
	}
	mi.idxMutex.Lock()
	mi.clear() // replacement does not invalidate the route's captured object
	mi.idxMutex.Unlock()
	got := make(map[restic.ID]pack.Blobs)
	for group := range byte(16) {
		for id, blobs := range routes.partition(context.Background(), packs, group) {
			got[id] = blobs
		}
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("captured locations changed after merge/replacement")
	}
}

func TestRepackRoutesLaterSelectedLocations(t *testing.T) {
	mi, packs := routeFixture(6000, 5)
	want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
	routes, ok := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots)
	if !ok {
		t.Fatal("routes rejected")
	}
	// Compare the installed collection snapshot against route capture before
	// appending a new location in a selected pack and populating an absent pack.
	collected, ok := mi.collectSelectedPacks(context.Background(), packs, listPacksCollectPackLimit, listPacksCollectBlobLimit)
	if !ok {
		t.Fatal("comparison collection overflowed")
	}
	later := NewIndex()
	bh := restic.BlobHandle{ID: routeID(900001), Type: restic.TreeBlob}
	later.StorePack(routeID(1), pack.Blobs{{BlobHandle: bh, Offset: 777777, Length: 101, UncompressedLength: 202}})
	later.StorePack(routeID(6042), pack.Blobs{{BlobHandle: bh, Length: 101}})
	later.Finalize()
	if err := mi.idx[0].merge(later); err != nil {
		t.Fatal(err)
	}
	got := make(map[restic.ID]pack.Blobs)
	for group := range byte(16) {
		for id, blobs := range routes.partition(context.Background(), packs, group) {
			got[id] = blobs
		}
	}
	if !reflect.DeepEqual(got, want) || !reflect.DeepEqual(got, collected) {
		t.Fatal("capture included later selected locations or lost originals")
	}
	if reflect.DeepEqual(got, collectPackChannel(t, mi.ListPacks(context.Background(), packs))) {
		t.Fatal("control did not expose later locations")
	}
}

func TestRepackRoutesCancellationAndPublication(t *testing.T) {
	mi, packs := routeFixture(20000, 19)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if routes, ok := mi.buildRepackRoutes(ctx, packs, repackRouteSlots); !ok || routes != nil {
		t.Fatal("canceled build did not settle")
	}
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	out := make(chan PackBlobs)
	done := make(chan struct{})
	go func() { mi.publishRepackRoutes(ctx, packs, out); close(out); close(done) }()
	select {
	case <-out:
	case <-time.After(5 * time.Second):
		t.Fatal("publication blocked")
	}
	// Acquire both write locks while the producer is blocked publishing.
	unlocked := make(chan struct{})
	go func() {
		mi.idxMutex.Lock()
		mi.idx[0].m.Lock()
		mi.idx[0].m.Unlock()
		mi.idxMutex.Unlock()
		close(unlocked)
	}()
	select {
	case <-unlocked:
	case <-time.After(5 * time.Second):
		t.Fatal("publication retains producer locks")
	}
	cancel()
	for range out {
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled producer did not join")
	}
}

func routeGroupingControl(ctx context.Context, mi *MasterIndex, packs restic.IDSet, values bool) int {
	count := 0
	for group := range byte(16) {
		grouped := make(map[restic.ID]pack.Blobs)
		for id := range packs {
			if id[0]&15 == group {
				grouped[id] = nil
			}
		}
		if len(grouped) == 0 {
			continue
		}
		if values {
			for pb := range mi.packedBlobValues() {
				if _, ok := grouped[pb.Pack]; ok {
					grouped[pb.Pack] = append(grouped[pb.Pack], pb.Blob)
				}
			}
		} else {
			for pb := range mi.Values() {
				if _, ok := grouped[pb.Pack]; ok {
					grouped[pb.Pack] = append(grouped[pb.Pack], pb.Blob)
				}
			}
		}
		for _, blobs := range grouped {
			count += len(blobs)
		}
	}
	return count
}

// Construction, overflow restart and partition conversion are all timed.
// The fixture is preloaded, as it is for the repack execution stage.
func BenchmarkB18RepackRouting(b *testing.B) {
	for _, n := range []int{10000, 100000, 1500000} {
		for _, selected := range []int{16, 128} {
			mi, packs := routeFixture(n, selected)
			want := min(n, selected*1000)
			for _, arm := range []string{"pointer", "value", "routes"} {
				b.Run(fmt.Sprintf("entries%d/selected%d/%s", n, selected, arm), func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						count := 0
						if arm == "routes" {
							routes, ok := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots)
							if ok {
								for group := range byte(16) {
									for _, blobs := range routes.partition(context.Background(), packs, group) {
										count += len(blobs)
									}
								}
							} else {
								count = routeGroupingControl(context.Background(), mi, packs, false)
							}
						} else {
							count = routeGroupingControl(context.Background(), mi, packs, arm == "value")
						}
						if count != want {
							b.Fatal(count, want)
						}
					}
				})
			}
		}
	}
}
