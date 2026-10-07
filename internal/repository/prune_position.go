package repository

import (
	"context"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// listBlobsWithSlot is ListBlobs plus, for every blob, its slot in set.
//
// Prune planning asks the set about every index entry in several passes. When
// the lister is the repository whose index the set belongs to, the walk hands
// over the position it stands on and the set needs no hash lookup. For any
// other lister the slot is unknown and the set looks the handle up as before.
//
// The blob passed to fn is only valid during the call.
func listBlobsWithSlot(ctx context.Context, idx restic.ListBlobser, set *index.AssociatedSet[uint8], fn func(blob restic.PackBlob, slot index.Slot)) error {
	repo, ok := idx.(*Repository)
	if !ok || !set.BoundTo(repo.idx) {
		return idx.ListBlobs(ctx, func(blob restic.PackBlob) {
			fn(blob, index.SlotUnknown)
		})
	}

	var err error
	repo.idx.EachSlot(func(pb *pack.PackedBlob, slot index.Slot) bool {
		if err = ctx.Err(); err != nil {
			return false
		}
		fn(pb, slot)
		return true
	})
	return err
}
