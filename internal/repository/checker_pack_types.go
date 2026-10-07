package repository

import (
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/restic"
)

// A fresh load sees every stored blob in its callbacks. Incremental loads and
// indexes containing unsaved blobs keep the authoritative post-load traversal.
// Breaking Values also releases its read locks before loading begins.
func freshCheckPackTypes(idx *index.MasterIndex) map[restic.ID]restic.BlobType {
	for range idx.Values() {
		return nil
	}
	return make(map[restic.ID]restic.BlobType)
}

// collectCheckPackType charges one map update per already-built integrity
// group, rather than reconstructing a PackBlob and updating a map per entry.
// Combining summaries is insensitive to exact-location deduplication during
// merging: deduplication cannot remove the last instance of either blob type.
func collectCheckPackType(types map[restic.ID]restic.BlobType, pbs index.PackBlobs) {
	if len(pbs.Blobs) == 0 {
		return
	}
	typ := pbs.Blobs[0].Type
	for _, blob := range pbs.Blobs[1:] {
		if blob.Type != typ {
			typ = restic.InvalidBlob
			break
		}
	}
	if previous, ok := types[pbs.PackID]; ok && previous != typ {
		typ = restic.InvalidBlob
	}
	types[pbs.PackID] = typ
}
