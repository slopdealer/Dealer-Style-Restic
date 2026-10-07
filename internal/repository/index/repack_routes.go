package index

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

const repackRouteSlots = 65536
const repackRoutePackSlots = 131072

// A route holds ordinals, never entry pointers. The supported layout is one
// finalized index. Merging only appends entries/packs to that object, so its
// existing ordinals and locations survive later merges and master replacement.
// No producer lock is held across publication (consumers can save indexes).
type repackRoutes struct {
	idx    *Index
	groups [16][]uint32
}

func (mi *MasterIndex) buildRepackRoutes(ctx context.Context, packs restic.IDSet, slotLimit int) (*repackRoutes, bool) {
	if ctx.Err() != nil {
		return nil, true
	}
	mi.idxMutex.RLock()
	defer mi.idxMutex.RUnlock()
	if len(mi.idx) != 1 {
		return nil, false
	}
	idx := mi.idx[0]
	idx.m.RLock()
	defer idx.m.RUnlock()
	if !idx.final || len(idx.packs) > repackRoutePackSlots {
		return nil, false
	}
	for typ := range idx.byType {
		if typ >= 4 || idx.byType[typ].blockList.Size() >= 1<<30 {
			return nil, false
		}
	}
	// One byte per pack slot replaces a selected-pack hash lookup per entry.
	// Zero is unselected; occupied partitions are encoded as nibble + 1.
	membership := make([]byte, len(idx.packs))
	for n, id := range idx.packs {
		if ctx.Err() != nil {
			return nil, true
		}
		if packs.Has(id) {
			membership[n] = (id[0] & 15) + 1
		}
	}
	routes := &repackRoutes{idx: idx}
	capacity := 0
	for typ := range idx.byType {
		m := &idx.byType[typ]
		for ordinal := uint(1); ordinal < m.blockList.Size(); ordinal++ {
			if ctx.Err() != nil {
				return nil, true
			}
			e := m.resolve(ordinal)
			group := membership[e.packIndex]
			if group == 0 {
				continue
			}
			slots := routes.groups[group-1]
			if len(slots) == cap(slots) {
				next := max(1, 2*cap(slots))
				if capacity+next-cap(slots) > slotLimit {
					return nil, false // no output escaped; ordinary fallback is safe
				}
				capacity += next - cap(slots)
				grown := make([]uint32, len(slots), next)
				copy(grown, slots)
				slots = grown
			}
			routes.groups[group-1] = append(slots, uint32(typ)<<30|uint32(ordinal))
		}
	}
	return routes, true
}

func (r *repackRoutes) partition(ctx context.Context, packs restic.IDSet, group byte) map[restic.ID]pack.Blobs {
	if ctx.Err() != nil {
		return nil
	}
	grouped := make(map[restic.ID]pack.Blobs)
	for id := range packs {
		if id[0]&15 == group {
			grouped[id] = nil
		}
	}
	r.idx.m.RLock()
	defer r.idx.m.RUnlock()
	for _, slot := range r.groups[group] {
		if ctx.Err() != nil {
			return nil
		}
		typ := restic.BlobType(slot >> 30)
		e := r.idx.byType[typ].resolve(uint(slot & ((1 << 30) - 1)))
		id := r.idx.packs[e.packIndex]
		grouped[id] = append(grouped[id], pack.Blob{
			BlobHandle: restic.BlobHandle{ID: e.id, Type: typ},
			Offset:     uint(e.offset), Length: uint(e.length),
			UncompressedLength: uint(e.uncompressedLength),
		})
	}
	r.groups[group] = nil
	return grouped
}

func (mi *MasterIndex) publishRepackRoutes(ctx context.Context, packs restic.IDSet, out chan<- PackBlobs) bool {
	routes, supported := mi.buildRepackRoutes(ctx, packs, repackRouteSlots)
	if !supported {
		return false
	}
	if routes == nil { // canceled construction
		return true
	}
	for group := range byte(16) {
		grouped := routes.partition(ctx, packs, group)
		for id, blobs := range grouped {
			delete(grouped, id)
			select {
			case <-ctx.Done():
				return true
			case out <- PackBlobs{PackID: id, Blobs: blobs}:
			}
		}
		if ctx.Err() != nil {
			return true
		}
	}
	return true
}
