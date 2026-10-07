package index

import (
	"context"

	"github.com/restic/restic/internal/restic"
)

// EachByPackForCheck preserves the ordinary grouped representation while
// checking cancellation during gathering and conversion. The read lock is
// released before channel closure. Ordinary rewrite callers keep EachByPack.
func (idx *Index) EachByPackForCheck(ctx context.Context, packBlacklist restic.IDSet) <-chan PackBlobs {
	idx.m.RLock()

	ch := make(chan PackBlobs)

	go func() {
		defer close(ch)
		defer idx.m.RUnlock()

		byPack := make(map[restic.ID][restic.NumBlobTypes][]*indexEntry)

		for typ := range idx.byType {
			m := &idx.byType[typ]
			for e := range m.values() {
				if ctx.Err() != nil {
					return
				}
				packID := idx.packs[e.packIndex]
				if !idx.final || !packBlacklist.Has(packID) {
					v := byPack[packID]
					v[typ] = append(v[typ], e)
					byPack[packID] = v
				}
			}
		}

		for packID, packByType := range byPack {
			var result PackBlobs
			result.PackID = packID
			for typ, p := range packByType {
				for _, e := range p {
					if ctx.Err() != nil {
						return
					}
					result.Blobs = append(result.Blobs, idx.toPackedBlob(e, restic.BlobType(typ)).Blob)
				}
			}
			// allow GC once entry is no longer necessary
			delete(byPack, packID)
			select {
			case <-ctx.Done():
				return
			case ch <- result:
			}
		}
	}()

	return ch
}
