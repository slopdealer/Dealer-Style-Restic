package index

import "github.com/restic/restic/internal/restic"

// IncrementAssociatedCount records one indexed occurrence of an admitted blob.
// Missing handles stay missing; present zero and overflow entries stay present.
// Like Get and Set, the caller must serialize access to the associated set.
// No stable slot address survives this operation.
func IncrementAssociatedCount(a *AssociatedSet[uint8], bh restic.BlobHandle) bool {
	return IncrementAssociatedCountAt(a, bh, SlotUnknown)
}

// IncrementAssociatedCountAt is IncrementAssociatedCount for a handle whose
// slot is already known (see EachSlot).
func IncrementAssociatedCountAt(a *AssociatedSet[uint8], bh restic.BlobHandle, slot Slot) bool {
	if count, ok := a.overflow[bh]; ok {
		if count < 255 {
			a.overflow[bh] = count + 1
		}
		return true
	}

	idx := a.slotIndex(bh, slot)
	bt := &a.byType[bh.Type]
	if idx == -1 || idx >= len(bt.value) || !bt.isSet[idx] {
		return false
	}
	if bt.value[idx] < 255 {
		bt.value[idx]++
	}
	return true
}
