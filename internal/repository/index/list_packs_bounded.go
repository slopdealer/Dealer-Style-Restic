package index

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Bound both the map cardinality and the sum of copied blob slice capacities.
// This is a small-selection optimization; the ordinary grouped traversal is
// retained for larger selections. No index-entry pointers escape the scan.
const (
	listPacksCollectPackLimit = 512
	listPacksCollectBlobLimit = 8192
)

func (mi *MasterIndex) collectSelectedPacks(ctx context.Context, packs restic.IDSet, packLimit, blobLimit int) (map[restic.ID]pack.Blobs, bool) {
	if len(packs) > packLimit {
		return nil, false
	}
	grouped := make(map[restic.ID]pack.Blobs, len(packs))
	for id := range packs {
		grouped[id] = nil
	}
	capacity := 0
	for pb := range mi.packedBlobValues() {
		if ctx.Err() != nil {
			return nil, true
		}
		id := pb.PackID()
		blobs, selected := grouped[id]
		if !selected {
			continue
		}
		if len(blobs) == cap(blobs) {
			next := max(1, 2*cap(blobs))
			if capacity+next-cap(blobs) > blobLimit {
				return nil, false
			}
			capacity += next - cap(blobs)
			grown := make(pack.Blobs, len(blobs), next)
			copy(grown, blobs)
			blobs = grown
		}
		grouped[id] = append(blobs, pb.Blob)
	}
	return grouped, true
}

// ListPacksForRepack tries a bounded single traversal before using ListPacks.
// It preserves the low-nibble group progression and within-pack blob order.
// An overflowing attempt publishes nothing, so the original path can start
// cleanly. Callers must cancel the context if they stop consuming the channel.
func (mi *MasterIndex) ListPacksForRepack(ctx context.Context, packs restic.IDSet) <-chan PackBlobs {
	return mi.listPacks(ctx, packs, true)
}
