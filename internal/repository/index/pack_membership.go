package index

import (
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// ContainsPackBlob checks the complete stored location without constructing
// lookup result objects. Duplicates are accepted when any location matches.
func (idx *Index) ContainsPackBlob(packID restic.ID, blob pack.Blob) bool {
	idx.m.RLock()
	defer idx.m.RUnlock()
	for e := range idx.byType[blob.Type].valuesWithID(blob.ID) {
		if idx.packs[e.packIndex] == packID &&
			uint(e.length) == blob.Length && uint(e.offset) == blob.Offset &&
			uint(e.uncompressedLength) == blob.UncompressedLength {
			return true
		}
	}
	return false
}

// ContainsPackBlob queries each index under the same locks as Lookup.
func (mi *MasterIndex) ContainsPackBlob(packID restic.ID, blob pack.Blob) bool {
	mi.idxMutex.RLock()
	defer mi.idxMutex.RUnlock()
	for _, idx := range mi.idx {
		if idx.ContainsPackBlob(packID, blob) {
			return true
		}
	}
	return false
}
