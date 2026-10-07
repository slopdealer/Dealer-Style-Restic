package index

import (
	"context"
	"iter"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// packedBlobValues transfers values, not pointers, to index-only consumers.
// The collector copies only selected blobs. Both locks and traversal order
// match Values, including duplicate locations and non-final indexes.
func (mi *MasterIndex) packedBlobValues() iter.Seq[pack.PackedBlob] {
	return func(yield func(pack.PackedBlob) bool) {
		mi.idxMutex.RLock()
		defer mi.idxMutex.RUnlock()
		for _, idx := range mi.idx {
			if !idx.packedBlobValues(yield) {
				return
			}
		}
	}
}

func (idx *Index) packedBlobValues(yield func(pack.PackedBlob) bool) bool {
	idx.m.RLock()
	defer idx.m.RUnlock()
	for typ := range idx.byType {
		for e := range idx.byType[typ].values() {
			pb := pack.PackedBlob{
				Pack: idx.packs[e.packIndex],
				Blob: pack.Blob{
					BlobHandle: restic.BlobHandle{ID: e.id, Type: restic.BlobType(typ)},
					Length:     uint(e.length), Offset: uint(e.offset),
					UncompressedLength: uint(e.uncompressedLength),
				},
			}
			if !yield(pb) {
				return false
			}
		}
	}
	return true
}

// PackHeaderSizes applies the same header arithmetic as pack.Size(..., true)
// directly to the loaded index. Every indexed location, including duplicates,
// contributes. No temporary per-entry interface pointer escapes this scan.
func (mi *MasterIndex) PackHeaderSizes(ctx context.Context) (map[restic.ID]int64, error) {
	sizes := make(map[restic.ID]int64)
	for pb := range mi.packedBlobValues() {
		if err := ctx.Err(); err != nil {
			return sizes, err
		}
		size, ok := sizes[pb.Pack]
		if !ok {
			size = int64(pack.CalculateHeaderSize(nil))
		}
		sizes[pb.Pack] = size + int64(pack.CalculateEntrySize(pb.IsCompressed()))
	}
	return sizes, nil
}
