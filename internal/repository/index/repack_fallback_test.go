package index

import (
	"context"
	"fmt"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
	"reflect"
	"testing"
	"time"
)

// Complete M003 controls: include locked collector, routing construction and
// overflow, partition conversion, channel publication and producer closure.
func b18FallbackControl(ctx context.Context, mi *MasterIndex, packs restic.IDSet, values bool) <-chan PackBlobs {
	out := make(chan PackBlobs)
	go func() {
		defer close(out)
		if len(packs) > 0 && len(packs) <= listPacksCollectPackLimit {
			grouped, ok := mi.collectSelectedPacks(ctx, packs, listPacksCollectPackLimit, listPacksCollectBlobLimit)
			if ctx.Err() != nil {
				return
			}
			if ok {
				for group := range byte(16) {
					for id, blobs := range grouped {
						if id[0]&15 != group {
							continue
						}
						delete(grouped, id)
						select {
						case out <- PackBlobs{PackID: id, Blobs: blobs}:
						case <-ctx.Done():
							return
						}
					}
				}
				return
			}
		}
		if len(packs) > 0 && mi.publishRepackRoutes(ctx, packs, out) {
			return
		}
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
					if ctx.Err() != nil {
						return
					}
					if _, ok := grouped[pb.Pack]; ok {
						grouped[pb.Pack] = append(grouped[pb.Pack], pb.Blob)
					}
				}
			} else {
				for pb := range mi.Values() {
					if ctx.Err() != nil {
						return
					}
					id := pb.PackID()
					if packs.Has(id) && id[0]&15 == group {
						grouped[id] = append(grouped[id], pb.Blob)
					}
				}
			}
			for id, blobs := range grouped {
				grouped[id] = nil
				select {
				case out <- PackBlobs{PackID: id, Blobs: blobs}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}

func b18Layout(mi *MasterIndex, layout string) {
	switch layout {
	case "mutable":
		mi.idx[0].final = false
	case "fragmented":
		extra := NewIndex()
		// Mixed type, exact duplicate, and second location; both indexes must count.
		extra.StorePack(routeID(1), pack.Blobs{
			{BlobHandle: restic.BlobHandle{ID: routeID(1), Type: restic.DataBlob}, Length: 100},
			{BlobHandle: restic.BlobHandle{ID: routeID(1), Type: restic.DataBlob}, Offset: 777, Length: 101, UncompressedLength: 202},
		})
		extra.Finalize()
		mi.idx = append(mi.idx, extra)
	}
}

func TestB18FallbackDifferential(t *testing.T) {
	for _, layout := range []string{"final", "mutable", "fragmented"} {
		for _, selected := range []int{0, 16, 128} {
			t.Run(fmt.Sprintf("%s/selected%d", layout, selected), func(t *testing.T) {
				mi, packs := routeFixture(130000, selected)
				b18Layout(mi, layout)
				want := collectPackChannel(t, mi.listPacks(context.Background(), packs, false))
				ordinary := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
				if !reflect.DeepEqual(want, ordinary) {
					t.Fatal("ordinary fallback differs from original pointer traversal")
				}
				got := collectPackChannel(t, mi.ListPacksForRepack(context.Background(), packs))
				if !reflect.DeepEqual(want, got) {
					t.Fatal("production locations/order/empty packs differ")
				}
				for _, values := range []bool{false, true} {
					control := collectPackChannel(t, b18FallbackControl(context.Background(), mi, packs, values))
					if !reflect.DeepEqual(want, control) {
						t.Fatal("control differs")
					}
				}
			})
		}
	}
}

func TestB18FallbackPublicationSettlement(t *testing.T) {
	for _, layout := range []string{"mutable", "fragmented"} {
		mi, packs := routeFixture(100000, 128)
		b18Layout(mi, layout)
		ctx, cancel := context.WithCancel(context.Background())
		out := mi.ListPacksForRepack(ctx, packs)
		select {
		case <-out:
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("no publication")
		}
		// Producer blocked on publication must permit master replacement, index
		// growth, and modifications by an index-saving consumer.
		done := make(chan struct{})
		growth := NewIndex()
		growth.StorePack(routeID(1), pack.Blobs{{BlobHandle: restic.BlobHandle{ID: routeID(900001), Type: restic.TreeBlob}, Length: 102}})
		growth.Finalize()
		var mergeErr error
		go func() {
			mi.idxMutex.Lock()
			old := mi.idx[0]
			mergeErr = old.merge(growth)
			mi.clear()
			mi.idxMutex.Unlock()
			close(done)
		}()
		select {
		case <-done:
			if mergeErr != nil {
				t.Fatal(mergeErr)
			}
		case <-time.After(5 * time.Second):
			cancel()
			t.Fatal("publication holds locks")
		}
		cancel()
		joined := make(chan struct{})
		go func() {
			for range out {
			}
			close(joined)
		}()
		select {
		case <-joined:
		case <-time.After(5 * time.Second):
			t.Fatal("interrupted consumer did not settle")
		}
	}
}

func TestB18FallbackSeesPartitionGrowthAndReplacement(t *testing.T) {
	mi, packs := routeFixture(100000, 128)
	mi.idx[0].final = false
	grouped := map[restic.ID]pack.Blobs{routeID(1): nil}
	mi.repackFallbackPartition(context.Background(), grouped)
	want := len(grouped[routeID(1)])
	mi.idx[0].StorePack(routeID(1), pack.Blobs{{BlobHandle: restic.BlobHandle{ID: routeID(900001), Type: restic.TreeBlob}, Length: 102}})
	grouped[routeID(1)] = nil
	mi.repackFallbackPartition(context.Background(), grouped)
	if len(grouped[routeID(1)]) != want+1 {
		t.Fatal("next scan lost growth")
	}
	mi.idxMutex.Lock()
	mi.clear()
	mi.idxMutex.Unlock()
	grouped[routeID(1)] = nil
	mi.repackFallbackPartition(context.Background(), grouped)
	if len(grouped[routeID(1)]) != 0 {
		t.Fatal("next scan ignored replacement")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range mi.ListPacksForRepack(ctx, packs) {
		t.Fatal("canceled producer published")
	}
}

func BenchmarkB18CompleteFallback(b *testing.B) {
	for _, n := range []int{10000, 100000, 1500000} {
		for _, layout := range []string{"final", "mutable", "fragmented"} {
			for _, selected := range []int{16, 128, n / 1000} {
				if selected > n/1000 {
					continue
				}
				mi, packs := routeFixture(n, selected)
				b18Layout(mi, layout)
				want := 0
				for pbs := range mi.ListPacks(context.Background(), packs) {
					want += len(pbs.Blobs)
				}
				for _, arm := range []string{"pointer", "value", "reject"} {
					b.Run(fmt.Sprintf("entries%d/%s/selected%d/%s", n, layout, selected, arm), func(b *testing.B) {
						b.ReportAllocs()
						for range b.N {
							var ch <-chan PackBlobs
							if arm == "reject" {
								ch = mi.ListPacksForRepack(context.Background(), packs)
							} else {
								ch = b18FallbackControl(context.Background(), mi, packs, arm == "value")
							}
							count := 0
							for pbs := range ch {
								count += len(pbs.Blobs)
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
}

func TestB18FallbackPartialCancellation(t *testing.T) {
	mi, packs := routeFixture(100000, 128)
	mi.idx[0].final = false
	parent, cancel := context.WithCancel(context.Background())
	defer cancel()
	gate := &b26RouteCheckpoint{Context: parent, stop: 100, entered: make(chan struct{}), release: make(chan struct{})}
	out := make(chan PackBlobs)
	done := make(chan struct{})
	go func() { mi.publishRepackFallback(gate, packs, out); close(out); close(done) }()
	b26Await(t, gate.entered, "partial fallback scan")
	cancel()
	close(gate.release)
	for range out {
		t.Fatal("partial canceled partition escaped")
	}
	b26Await(t, done, "fallback producer settlement")
	unlocked := make(chan struct{})
	go func() {
		mi.idxMutex.Lock()
		mi.idx[0].m.Lock()
		mi.idx[0].m.Unlock()
		mi.idxMutex.Unlock()
		close(unlocked)
	}()
	b26Await(t, unlocked, "fallback post-cancellation locks")
}

func TestB18FallbackOwnsPublishedValues(t *testing.T) {
	mi, _ := routeFixture(100000, 128)
	grouped := map[restic.ID]pack.Blobs{routeID(1): nil}
	mi.repackFallbackPartition(context.Background(), grouped)
	original := grouped[routeID(1)][0]
	grouped[routeID(1)][0].Offset++
	another := map[restic.ID]pack.Blobs{routeID(1): nil}
	mi.repackFallbackPartition(context.Background(), another)
	if another[routeID(1)][0] != original {
		t.Fatal("published values alias index locations")
	}
}

func BenchmarkB18OrdinaryFallback(b *testing.B) {
	for _, n := range []int{10000, 100000, 1500000} {
		mi, packs := routeFixture(n, n/1000)
		for _, arm := range []string{"pointer", "reject"} {
			b.Run(fmt.Sprintf("entries%d/%s", n, arm), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var out <-chan PackBlobs
					if arm == "reject" {
						out = mi.ListPacks(context.Background(), packs)
					} else {
						out = mi.listPacks(context.Background(), packs, false)
					}
					count := 0
					for pbs := range out {
						count += len(pbs.Blobs)
					}
					if count != n {
						b.Fatal(count, n)
					}
				}
			})
		}
	}
}
