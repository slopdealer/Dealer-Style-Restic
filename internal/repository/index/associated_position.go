package index

import (
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Position-addressed access to an AssociatedSet.
//
// Get, Set and Delete find the slot of a blob handle with MasterIndex.blobIndex:
// a hash lookup that misses the cache for every blob. Prune planning walks the
// whole index several times and asks the set about every entry it meets. A walk
// in index order already stands on the slot: the slot of a handle is the
// position of the first entry with its ID in the merged index, so the entry at
// position p owns slot p unless an earlier entry carries the same ID.
//
// EachSlot reports every entry together with that slot. The *At methods take
// the slot from the caller instead of looking it up. The slot is computed from
// the index alone and nothing is remembered between calls, so any number of
// walks and callers may run at the same time under the usual rules of the set
// (readers may share it, a writer needs it exclusively).

// Slot is the place of a blob handle in the AssociatedSets of one MasterIndex,
// exactly as MasterIndex.blobIndex reports it.
type Slot int

// SlotUnknown makes the *At methods look the handle up, as Get, Set and Delete do.
const SlotUnknown Slot = -2

// firstPosition returns the position of the first entry that carries the ID of
// the entry e stored at position pos. It equals firstIndex(e.id).
//
// Every chain is ordered by descending position: add, addIfMissing and the
// rehash in preallocate all prepend, and positions only grow. Entries with an
// equal ID and a smaller position can therefore only follow e in its chain, and
// the bloom bits kept in e.next cover exactly those entries.
func (m *indexMap) firstPosition(e *indexEntry, pos uint) uint {
	first := pos
	for ei := e.next; bloomHasID(ei, e.id); {
		o := m.resolve(ei)
		if o.id == e.id {
			first = bloomCleanID(ei)
		}
		ei = o.next
	}
	return first
}

// EachSlot calls fn for every blob known to the index, in the order of Values,
// together with the blob's slot in the AssociatedSets of this MasterIndex.
// The walk stops when fn returns false. This blocks any modification of the
// index, like Values.
//
// The PackedBlob is reused between calls: fn must not retain it.
func (mi *MasterIndex) EachSlot(fn func(pb *pack.PackedBlob, slot Slot) bool) {
	mi.idxMutex.RLock()
	defer mi.idxMutex.RUnlock()

	var pb pack.PackedBlob
	for i, idx := range mi.idx {
		if !idx.eachSlot(i == 0, &pb, fn) {
			return
		}
	}
}

func (idx *Index) eachSlot(stable bool, pb *pack.PackedBlob, fn func(pb *pack.PackedBlob, slot Slot) bool) bool {
	idx.m.RLock()
	defer idx.m.RUnlock()

	for typ := range idx.byType {
		m := &idx.byType[typ]
		blockCount := m.blockList.Size()
		for pos := uint(1); pos < blockCount; pos++ {
			e := m.blockList.Ref(pos)
			// Only the first, merged index has stable positions. Entries of the
			// other indexes are looked up, as their handle may also be stored there.
			slot := SlotUnknown
			if stable {
				slot = Slot(m.firstPosition(e, pos))
			}
			pb.Pack = idx.packs[e.packIndex]
			pb.Blob = pack.Blob{
				BlobHandle: restic.BlobHandle{
					ID:   e.id,
					Type: restic.BlobType(typ),
				},
				Length:             uint(e.length),
				Offset:             uint(e.offset),
				UncompressedLength: uint(e.uncompressedLength),
			}
			if !fn(pb, slot) {
				return false
			}
		}
	}
	return true
}

// BoundTo reports whether the slots of mi address this set.
func (a *AssociatedSet[T]) BoundTo(mi *MasterIndex) bool {
	return a != nil && a.idx == mi
}

func (a *AssociatedSet[T]) slotIndex(bh restic.BlobHandle, slot Slot) int {
	if slot == SlotUnknown {
		return a.idx.blobIndex(bh)
	}
	return int(slot)
}

// GetAt is Get for a handle whose slot is already known.
func (a *AssociatedSet[T]) GetAt(bh restic.BlobHandle, slot Slot) (T, bool) {
	if val, ok := a.overflow[bh]; ok {
		return val, true
	}

	idx := a.slotIndex(bh, slot)
	bt := &a.byType[bh.Type]
	if idx >= len(bt.value) || idx == -1 {
		var zero T
		return zero, false
	}

	if bt.isSet[idx] {
		return bt.value[idx], true
	}
	var zero T
	return zero, false
}

// SetAt is Set for a handle whose slot is already known.
func (a *AssociatedSet[T]) SetAt(bh restic.BlobHandle, slot Slot, val T) {
	if _, ok := a.overflow[bh]; ok {
		a.overflow[bh] = val
		return
	}

	idx := a.slotIndex(bh, slot)
	bt := &a.byType[bh.Type]
	if idx >= len(bt.value) || idx == -1 {
		a.overflow[bh] = val
	} else {
		bt.value[idx] = val
		bt.isSet[idx] = true
	}
}

// DeleteAt is Delete for a handle whose slot is already known.
func (a *AssociatedSet[T]) DeleteAt(bh restic.BlobHandle, slot Slot) {
	if _, ok := a.overflow[bh]; ok {
		delete(a.overflow, bh)
		return
	}

	idx := a.slotIndex(bh, slot)
	bt := &a.byType[bh.Type]
	if idx < len(bt.value) && idx != -1 {
		bt.isSet[idx] = false
	}
}

// allAt yields what All yields, in the same order, without a lookup per entry.
func (a *AssociatedSet[T]) allAt(yield func(restic.BlobHandle, T) bool) {
	for k, v := range a.overflow {
		if !yield(k, v) {
			return
		}
	}

	a.idx.EachSlot(func(pb *pack.PackedBlob, slot Slot) bool {
		bh := pb.Blob.BlobHandle
		if _, ok := a.overflow[bh]; ok {
			// already reported via overflow set
			return true
		}

		val, known := a.GetAt(bh, slot)
		if known {
			return yield(bh, val)
		}
		return true
	})
}
