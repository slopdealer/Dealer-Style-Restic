package restorer

import "github.com/restic/restic/internal/restic"

// plannedSingleton reuses an association established by restoreFiles. A file
// with one blob belongs to exactly one selected pack, at offset zero. Only
// unmatched blobs enter that pack's file set. No index result is retained, and
// the loader still validates pack membership and verifies the blob contents.
// Packs constructed directly (including by tests) keep the ordinary lookup path.
func plannedSingleton(pack *packInfo, ids restic.IDs) (restic.BlobHandle, bool) {
	if !pack.associationsPlanned || len(ids) != 1 {
		return restic.BlobHandle{}, false
	}
	return restic.BlobHandle{Type: restic.DataBlob, ID: ids[0]}, true
}
