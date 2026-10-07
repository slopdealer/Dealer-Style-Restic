package index

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestB18OrdinalMembership(t *testing.T) {
	for _, n := range []int{0, 1, 63, 64, 65, repackOrdinalPackLimit, repackOrdinalPackLimit + 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			packs := make([]restic.ID, n)
			grouped := make(map[restic.ID]pack.Blobs)
			for i := range packs {
				packs[i] = routeID(i%113 + 1) // repeated table IDs must set every ordinal
				if i%3 == 0 {
					grouped[packs[i]] = nil
				}
			}
			bits := repackOrdinalSelection(context.Background(), packs, grouped, uint(n)*4)
			if n == 0 || n > repackOrdinalPackLimit {
				if bits != nil {
					t.Fatal("unsupported table admitted")
				}
				return
			}
			if len(bits)*8 > 8192 {
				t.Fatal("membership exceeds byte bound")
			}
			for i, id := range packs {
				_, want := grouped[id]
				if got := bits[i>>6]&(uint64(1)<<(uint(i)&63)) != 0; got != want {
					t.Fatalf("ordinal %d: got %v want %v", i, got, want)
				}
			}
			if repackOrdinalSelection(context.Background(), packs, grouped, uint(n)*4-1) != nil {
				t.Fatal("low density admitted")
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if repackOrdinalSelection(ctx, packs, grouped, uint(n)*4) != nil {
				t.Fatal("canceled construction retained")
			}
		})
	}
}

// Baseline includes identical locks, reconstruction, context checks and output
// ownership. Only ordinal construction/rejection differs.
func b18AppendMapPartition(ctx context.Context, idx *Index, grouped map[restic.ID]pack.Blobs) {
	idx.m.RLock()
	defer idx.m.RUnlock()
	for typ := range idx.byType {
		for e := range idx.byType[typ].values() {
			if ctx.Err() != nil {
				return
			}
			id := idx.packs[e.packIndex]
			if blobs, ok := grouped[id]; ok {
				grouped[id] = append(blobs, pack.Blob{BlobHandle: restic.BlobHandle{ID: e.id, Type: restic.BlobType(typ)}, Offset: uint(e.offset), Length: uint(e.length), UncompressedLength: uint(e.uncompressedLength)})
			}
		}
	}
}

func TestB18OrdinalPartitionDifferential(t *testing.T) {
	for _, layout := range []string{"final", "mutable", "fragmented"} {
		mi, packs := routeFixture(130000, 128)
		b18Layout(mi, layout)
		for group := range byte(16) {
			want, got := make(map[restic.ID]pack.Blobs), make(map[restic.ID]pack.Blobs)
			for id := range packs {
				if id[0]&15 == group {
					want[id] = nil
					got[id] = nil
				}
			}
			for _, idx := range mi.idx {
				b18AppendMapPartition(context.Background(), idx, want)
			}
			mi.repackFallbackPartition(context.Background(), got)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("%s partition %d differs", layout, group)
			}
		}
	}
}

func BenchmarkB18OrdinalPartition(b *testing.B) {
	for _, n := range []int{10000, 1500000} {
		for _, selected := range []int{16, 128, n / 1000} {
			mi, packs := routeFixture(n, selected)
			for _, ordinal := range []bool{false, true} {
				b.Run(fmt.Sprintf("entries%d/selected%d/ordinal%v", n, selected, ordinal), func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
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
							if ordinal {
								mi.repackFallbackPartition(context.Background(), grouped)
							} else {
								for _, idx := range mi.idx {
									b18AppendMapPartition(context.Background(), idx, grouped)
								}
							}
						}
					}
				})
			}
		}
	}
}

// Includes both failed fast-path attempts, partition construction, channel
// publication and closure. The retained collector and routing are identical.
func b18OrdinalMapControl(ctx context.Context, mi *MasterIndex, packs restic.IDSet, repack bool) <-chan PackBlobs {
	out := make(chan PackBlobs)
	go func() {
		defer close(out)
		publish := func(grouped map[restic.ID]pack.Blobs) bool {
			for group := range byte(16) {
				for id, blobs := range grouped {
					if id[0]&15 != group {
						continue
					}
					delete(grouped, id)
					select {
					case out <- PackBlobs{PackID: id, Blobs: blobs}:
					case <-ctx.Done():
						return false
					}
				}
			}
			return true
		}
		if repack && len(packs) > 0 && len(packs) <= listPacksCollectPackLimit {
			grouped, complete := mi.collectSelectedPacks(ctx, packs, listPacksCollectPackLimit, listPacksCollectBlobLimit)
			if ctx.Err() != nil {
				return
			}
			if complete {
				publish(grouped)
				return
			}
		}
		if repack && len(packs) > 0 && mi.publishRepackRoutes(ctx, packs, out) {
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
			mi.idxMutex.RLock()
			for _, idx := range mi.idx {
				b18AppendMapPartition(ctx, idx, grouped)
			}
			mi.idxMutex.RUnlock()
			if ctx.Err() != nil {
				return
			}
			for id, blobs := range grouped {
				delete(grouped, id)
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

func TestB18OrdinalCompleteDifferential(t *testing.T) {
	for _, layout := range []string{"final", "mutable", "fragmented", "wide"} {
		mi, packs := routeFixture(130000, 128)
		if layout == "wide" {
			for len(mi.idx[0].packs) <= repackOrdinalPackLimit {
				mi.idx[0].packs = append(mi.idx[0].packs, routeID(900001))
			}
		} else {
			b18Layout(mi, layout)
		}
		for _, repack := range []bool{false, true} {
			want := collectPackChannel(t, b18OrdinalMapControl(context.Background(), mi, packs, repack))
			out := mi.ListPacks(context.Background(), packs)
			if repack { // settle the unused iterator before starting the repack arm
				for range out {
				}
				out = mi.ListPacksForRepack(context.Background(), packs)
			}
			got := collectPackChannel(t, out)
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("%s repack%v differs", layout, repack)
			}
		}
	}
}

func BenchmarkB18OrdinalComplete(b *testing.B) {
	for _, layout := range []string{"final", "mutable", "fragmented"} {
		for _, selected := range []int{16, 128, 1500} {
			mi, packs := routeFixture(1500000, selected)
			b18Layout(mi, layout)
			for _, repack := range []bool{false, true} {
				for _, ordinal := range []bool{false, true} {
					b.Run(fmt.Sprintf("%s/selected%d/repack%v/ordinal%v", layout, selected, repack, ordinal), func(b *testing.B) {
						b.ReportAllocs()
						for range b.N {
							var out <-chan PackBlobs
							if !ordinal {
								out = b18OrdinalMapControl(context.Background(), mi, packs, repack)
							} else if repack {
								out = mi.ListPacksForRepack(context.Background(), packs)
							} else {
								out = mi.ListPacks(context.Background(), packs)
							}
							seen, locations := 0, 0
							for pbs := range out {
								seen++
								locations += len(pbs.Blobs)
							}
							wantLocations := min(selected, 1500) * 1000
							if layout == "fragmented" {
								wantLocations += 2
							}
							if locations != wantLocations {
								b.Fatalf("locations %d want %d", locations, wantLocations)
							}
							if seen != len(packs) {
								b.Fatal("lost packs")
							}
						}
					})
				}
			}
		}
	}
}
