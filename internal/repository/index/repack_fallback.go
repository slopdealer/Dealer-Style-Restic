package index

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// repackFallbackPartition keeps the ordinary partition traversal and lock
// lifetime. Only selected locations are reconstructed; rejected entries never
// become PackedBlobs. Each partition observes the current master, as Values
// did, including non-final indexes and duplicate locations.
func (mi *MasterIndex) repackFallbackPartition(ctx context.Context, grouped map[restic.ID]pack.Blobs) {
	mi.idxMutex.RLock()
	defer mi.idxMutex.RUnlock()
	for _, idx := range mi.idx {
		if !idx.appendRepackPartition(ctx, grouped) {
			return
		}
	}
}

func (idx *Index) appendRepackPartition(ctx context.Context, grouped map[restic.ID]pack.Blobs) bool {
	idx.m.RLock()
	defer idx.m.RUnlock()
	var entries uint
	for typ := range idx.byType {
		entries += idx.byType[typ].numentries
	}
	selected := repackOrdinalSelection(ctx, idx.packs, grouped, entries)
	for typ := range idx.byType {
		for e := range idx.byType[typ].values() {
			if ctx.Err() != nil {
				return false
			}
			if selected != nil && selected[e.packIndex>>6]&(uint64(1)<<(e.packIndex&63)) == 0 {
				continue
			}
			id := idx.packs[e.packIndex]
			blobs, selected := grouped[id]
			if !selected {
				continue
			}
			grouped[id] = append(blobs, pack.Blob{
				BlobHandle: restic.BlobHandle{ID: e.id, Type: restic.BlobType(typ)},
				Offset:     uint(e.offset), Length: uint(e.length),
				UncompressedLength: uint(e.uncompressedLength),
			})
		}
	}
	return true
}

func (mi *MasterIndex) publishRepackFallback(ctx context.Context, packs restic.IDSet, out chan<- PackBlobs) {
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
		mi.repackFallbackPartition(ctx, grouped)
		if ctx.Err() != nil {
			return
		}
		// Both locks have been released before consumers can modify/save indexes.
		for id, blobs := range grouped {
			delete(grouped, id)
			select {
			case out <- PackBlobs{PackID: id, Blobs: blobs}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// listPacksFallback is also used by ordinary checking and repair. These callers
// keep repeated current-index scans rather than adopting routing's capture.
func (mi *MasterIndex) listPacksFallback(ctx context.Context, packs restic.IDSet) <-chan PackBlobs {
	out := make(chan PackBlobs)
	go func() {
		defer close(out)
		mi.publishRepackFallback(ctx, packs, out)
	}()
	return out
}
