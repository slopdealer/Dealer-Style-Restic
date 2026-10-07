package index

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Rebuild under the index read lock for each partition. The bits never survive
// the scan, so growth and master replacement retain ordinary observation rules.
// At most 8 KiB is live per index. Small or very wide tables use map rejection.
const repackOrdinalPackLimit = 65536

func repackOrdinalSelection(ctx context.Context, packs []restic.ID, grouped map[restic.ID]pack.Blobs, entries uint) []uint64 {
	if len(packs) == 0 || len(packs) > repackOrdinalPackLimit || entries/4 < uint(len(packs)) {
		return nil
	}
	selected := make([]uint64, (len(packs)+63)/64)
	for ordinal, id := range packs {
		if ctx.Err() != nil {
			return nil
		}
		if _, ok := grouped[id]; ok {
			selected[ordinal>>6] |= uint64(1) << (uint(ordinal) & 63)
		}
	}
	return selected
}
