package index

import (
	"context"
	"fmt"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Gate an actual cancellation at an Err checkpoint, without scheduling sleeps
// or changing production code. The call count lets the test stop after some
// selected entries have been copied, rather than only before work begins.
type routeCheckpoint struct {
	context.Context
	calls   atomic.Int64
	stop    int64
	entered chan struct{}
	release chan struct{}
}

func (c *routeCheckpoint) Err() error {
	if c.calls.Add(1) == c.stop {
		close(c.entered)
		<-c.release
	}
	return c.Context.Err()
}

func awaitSettlement(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatalf("timeout: %s", what)
	}
}

func TestRepackRoutesPartialCancellation(t *testing.T) {
	for _, stage := range []string{"construction", "partition"} {
		t.Run(stage, func(t *testing.T) {
			mi, packs := routeFixture(20000, 19)
			parent, cancel := context.WithCancel(context.Background())
			defer cancel()
			gate := &routeCheckpoint{Context: parent, stop: 100, entered: make(chan struct{}), release: make(chan struct{})}
			defer func() {
				select {
				case <-gate.release:
				default:
					close(gate.release)
				}
			}()
			var routes *repackRoutes
			if stage == "partition" {
				var ok bool
				routes, ok = mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots)
				if !ok || routes == nil {
					t.Fatal("fixture rejected")
				}
			}
			done := make(chan struct{})
			var failure string
			go func() {
				defer close(done)
				if stage == "construction" {
					r, ok := mi.buildRepackRoutes(gate, packs, repackRouteSlots)
					if !ok || r != nil {
						failure = "partial construction escaped or requested fallback"
					}
				} else if r := routes.partition(gate, packs, 1); r != nil {
					failure = "partial partition escaped"
				}
			}()
			awaitSettlement(t, gate.entered, "partial-work checkpoint")
			cancel()
			close(gate.release)
			awaitSettlement(t, done, "canceled work settlement")
			if failure != "" {
				t.Fatal(failure)
			}
			// Check both write locks after settlement; no reader may linger.
			unlocked := make(chan struct{})
			go func() {
				mi.idxMutex.Lock()
				mi.idx[0].m.Lock()
				mi.idx[0].m.Unlock()
				mi.idxMutex.Unlock()
				close(unlocked)
			}()
			awaitSettlement(t, unlocked, "post-cancellation write locks")
		})
	}
}

func TestRepackRoutesConcurrentGrowth(t *testing.T) {
	mi, packs := routeFixture(20000, 19)
	want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
	routes, ok := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots)
	if !ok || routes == nil {
		t.Fatal("fixture rejected")
	}
	idx := routes.idx
	before := idx.byType[restic.DataBlob].blockList.blockSize
	// All fixture construction is outside the concurrent region. Merges add
	// distinct locations and selected-pack entries; capture excludes these.
	additions := make([]*Index, 8)
	for batch := range additions {
		other := NewIndex()
		blobs := make(pack.Blobs, 10000)
		for j := range blobs {
			blobs[j] = pack.Blob{BlobHandle: restic.BlobHandle{ID: routeID(1000000 + batch*10000 + j), Type: restic.DataBlob}, Offset: uint(j * 101), Length: 101, UncompressedLength: 202}
		}
		other.StorePack(routeID(1), blobs)
		other.Finalize()
		additions[batch] = other
	}
	gate := &routeCheckpoint{Context: context.Background(), stop: 100, entered: make(chan struct{}), release: make(chan struct{})}
	defer func() {
		select {
		case <-gate.release:
		default:
			close(gate.release)
		}
	}()
	first := make(chan map[restic.ID]pack.Blobs, 1)
	go func() { first <- routes.partition(gate, packs, 1) }()
	awaitSettlement(t, gate.entered, "conversion holding captured-index read lock")
	started := make(chan struct{})
	done := make(chan struct{})
	var mergeErr error
	go func() {
		defer close(done)
		close(started)
		for _, other := range additions {
			if err := idx.merge(other); err != nil {
				mergeErr = err
				return
			}
		}
	}()
	awaitSettlement(t, started, "merge started during conversion")
	select {
	case <-done:
		t.Fatal("merge finished while partition held read lock")
	default:
	}
	close(gate.release)
	got := make(map[restic.ID]pack.Blobs)
	select {
	case grouped := <-first:
		for id, blobs := range grouped {
			got[id] = blobs
		}
	case <-time.After(5 * time.Second):
		t.Fatal("partition did not settle before merge")
	}
	for group := range byte(16) {
		if group != 1 {
			for id, blobs := range routes.partition(context.Background(), packs, group) {
				got[id] = blobs
			}
		}
		if routes.groups[group] != nil {
			t.Fatalf("partition %d retains its ordinal array", group)
		}
	}
	awaitSettlement(t, done, "concurrent merges")
	if mergeErr != nil {
		t.Fatal(mergeErr)
	}
	if idx.byType[restic.DataBlob].blockList.blockSize <= before {
		t.Fatal("fixture did not relocate HAT blocks")
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatal("captured fields/order changed during growth")
	}
	if reflect.DeepEqual(got, collectPackChannel(t, mi.ListPacks(context.Background(), packs))) {
		t.Fatal("control did not expose new selected locations")
	}
}

func TestRepackRoutesConsumerOwnership(t *testing.T) {
	mi, packs := routeFixture(20000, 19)
	want := collectPackChannel(t, mi.ListPacks(context.Background(), packs))
	routes, ok := mi.buildRepackRoutes(context.Background(), packs, repackRouteSlots)
	if !ok || routes == nil {
		t.Fatal("fixture rejected")
	}
	got := routes.partition(context.Background(), packs, 1)
	if routes.groups[1] != nil {
		t.Fatal("converted partition retains ordinals")
	}
	// Discard producer-owned descriptors and remove the master reference.
	// Published Blobs must be owned values, independent of that lifetime.
	clear(routes.groups[:])
	routes.idx = nil
	mi.idxMutex.Lock()
	mi.clear()
	mi.idxMutex.Unlock()
	for id, blobs := range got {
		if !reflect.DeepEqual(blobs, want[id]) {
			t.Fatal(fmt.Sprintf("consumer lost pack %s", id))
		}
	}
}
