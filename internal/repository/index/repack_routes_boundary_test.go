package index

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// These layouts must reject routing before any publication. The production
// iterator must still return the ordinary locations and partition progression.
func TestRepackRoutesUnsupportedProductionFallback(t *testing.T) {
	for _, layout := range []string{"unsaved", "preloaded-multiple", "membership-overflow"} {
		t.Run(layout, func(t *testing.T) {
			mi, packs := routeFixture(20000, 19) // overflow the locked small collector
			switch layout {
			case "unsaved":
				mi.idx[0].final = false
			case "preloaded-multiple":
				other := NewIndex()
				other.StorePack(routeID(1), pack.Blobs{{BlobHandle: restic.BlobHandle{ID: routeID(900001), Type: restic.TreeBlob}, Offset: 123456, Length: 101, UncompressedLength: 202}})
				other.Finalize()
				mi.idx = append(mi.idx, other)
			case "membership-overflow":
				// Extra empty pack records cost membership storage but no entries.
				mi.idx[0].packs = append(mi.idx[0].packs, make(restic.IDs, repackRoutePackSlots+1-len(mi.idx[0].packs))...)
			}
			if routes, supported := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots); supported || routes != nil {
				t.Fatal("unsupported layout accepted or partial routes escaped")
			}
			want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
			got := collectPackChannel(t, mi.ListPacksForRepack(context.Background(), packs))
			if !reflect.DeepEqual(want, got) {
				t.Fatal("fallback changed locations or within-pack order")
			}
		})
	}
}

func TestRepackRoutesMembershipExactBound(t *testing.T) {
	mi, packs := routeFixture(1000, 1)
	mi.idx[0].packs = append(mi.idx[0].packs, make(restic.IDs, repackRoutePackSlots-len(mi.idx[0].packs))...)
	want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
	got := routeOutput(t, mi, packs, repackRouteSlots)
	if !reflect.DeepEqual(want, got) {
		t.Fatal("exact membership capacity changed output")
	}
	mi.idx[0].packs = append(mi.idx[0].packs, restic.ID{})
	if routes, supported := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots); supported || routes != nil {
		t.Fatal("membership capacity overflow accepted")
	}
}

// Err calls select reproducible cancellation points in this single-threaded
// construction/conversion witness. Cancellation itself is an ordinary context.
type routeCancelContext struct {
	context.Context
	cancel    context.CancelFunc
	remaining int
}

func (c *routeCancelContext) Err() error {
	c.remaining--
	if c.remaining == 0 {
		c.cancel()
	}
	return c.Context.Err()
}

func TestRepackRoutesMidScanCancellation(t *testing.T) {
	mi, packs := routeFixture(20000, 19)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	controlled := &routeCancelContext{Context: ctx, cancel: cancel, remaining: 1000}
	if routes, supported := mi.buildRepackRoutes(controlled, packs, repackRouteSlots); !supported || routes != nil || ctx.Err() == nil {
		t.Fatal("interrupted construction retained partial routes")
	}
	// A fresh construction succeeds after the interrupted producer releases locks.
	routes, supported := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots)
	if !supported || routes == nil {
		t.Fatal("fresh construction failed")
	}
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	controlled = &routeCancelContext{Context: ctx2, cancel: cancel2, remaining: 100}
	if got := routes.partition(controlled, packs, 1); got != nil || ctx2.Err() == nil {
		t.Fatal("interrupted conversion published partial grouping")
	}
	mi.idx[0].m.Lock()
	mi.idx[0].m.Unlock()
}

// Measure the public iterators, including the locked collector's first attempt,
// routing construction, overflow restart, conversion, publication and closure.
// Fixtures are loaded before timing, as they are before repack execution.
func BenchmarkB18RepackCompleteIterator(b *testing.B) {
	for _, size := range []struct{ entries, selected int }{
		{5000, 5}, {100000, 16}, {1500000, 16}, {1500000, 128},
	} {
		mi, packs := routeFixture(size.entries, size.selected)
		for _, routed := range []bool{false, true} {
			b.Run(fmt.Sprintf("entries%d/selected%d/routed%v", size.entries, size.selected, routed), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					var ch <-chan PackBlobs
					if routed {
						ch = mi.ListPacksForRepack(context.Background(), packs)
					} else {
						ch = mi.ListPacks(context.Background(), packs)
					}
					count, delivered := 0, 0
					for pb := range ch {
						count += len(pb.Blobs)
						delivered++
					}
					if count != min(size.entries, size.selected*1000) || delivered != len(packs) {
						b.Fatalf("incomplete publication: %d locations, %d packs", count, delivered)
					}
				}
			})
		}
	}
}
