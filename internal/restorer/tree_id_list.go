package restorer

import "github.com/restic/restic/internal/restic"

// maxPrefetchTreeIDs bounds the list of tree IDs that the first pass of a
// restore records for the second pass's tree prefetch: 2 MiB of IDs.
//
// Stock keeps nothing per directory during a restore. The list used to hold
// one ID for every directory of the snapshot (32 bytes each, more while the
// slice was regrown), which is 100 bytes of peak memory per directory: 170
// MiB more than stock for two million directories. With the bound, a restore
// keeps at most this many IDs, whatever the size of the snapshot.
//
// The list is a prefix of the trees in the order in which both passes visit
// them. The prefetch prepares exactly the trees on the list; when the second
// pass asks for a tree behind its end, the prefetch's queue is closed and the
// pass loads that tree itself, with the same loader and at the moment it
// needs it (restoreTreePrefetch.Load). So a snapshot with more directories
// than the bound restores as before, without the read-ahead for the trees
// past the bound; a snapshot with fewer is not affected at all.
const maxPrefetchTreeIDs = 1 << 16

// restoreTreeIDList is a list of tree IDs that never holds, and never
// allocates room for, more than limit IDs.
type restoreTreeIDList struct {
	ids   []restic.ID
	limit int
}

func newRestoreTreeIDList(limit int) restoreTreeIDList {
	return restoreTreeIDList{limit: limit}
}

// add appends id while the list is below its limit and drops it afterwards.
func (l *restoreTreeIDList) add(id restic.ID) {
	if len(l.ids) >= l.limit {
		return
	}
	if len(l.ids) == cap(l.ids) {
		// Grow by doubling, but never past the limit: append would round the
		// capacity up to a size class beyond it.
		grown := make([]restic.ID, len(l.ids), min(max(2*cap(l.ids), 64), l.limit))
		copy(grown, l.ids)
		l.ids = grown
	}
	l.ids = append(l.ids, id)
}
