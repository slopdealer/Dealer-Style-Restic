package index

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// listPacksStock is stock's MasterIndex.ListPacks, unchanged: sixteen walks
// over all index values. Production code does not call it any more; the
// listing tests compare every listing path with it.
func (mi *MasterIndex) listPacksStock(ctx context.Context, packs restic.IDSet) <-chan PackBlobs {
	out := make(chan PackBlobs)
	go func() {
		defer close(out)
		// only resort a part of the index to keep the memory overhead bounded
		for i := range byte(16) {
			packBlob := make(map[restic.ID]pack.Blobs)
			for pack := range packs {
				if pack[0]&0xf == i {
					packBlob[pack] = nil
				}
			}
			if len(packBlob) == 0 {
				continue
			}
			for pb := range mi.Values() {
				if ctx.Err() != nil {
					return
				}
				packID := pb.PackID()
				if packs.Has(packID) && packID[0]&0xf == i {
					packBlob[packID] = append(packBlob[packID], pb.Blob)
				}
			}

			// pass on packs
			for packID, pbs := range packBlob {
				// allow GC
				packBlob[packID] = nil
				select {
				case out <- PackBlobs{PackID: packID, Blobs: pbs}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	return out
}
