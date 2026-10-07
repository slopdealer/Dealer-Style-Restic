package index

import "github.com/restic/restic/internal/restic"

// addIfMissing combines exact-location lookup and insertion during merge.
// The caller holds the containing Index lock and supplies its combined pack
// table. Blob type is implicit in the map; equal IDs in different packs or
// with different range/encoding fields remain distinct entries.
func (m *indexMap) addIfMissing(src *indexEntry, packIndex uint32, packs restic.IDs) {
	if len(m.buckets) == 0 {
		m.preallocate(1)
	}

	h := m.hash(src.id)
	ei := m.buckets[h]
	for bloomHasID(ei, src.id) {
		e := m.resolve(ei)
		if e.id == src.id && e.offset == src.offset && e.length == src.length &&
			e.uncompressedLength == src.uncompressedLength && packs[e.packIndex] == packs[packIndex] {
			return
		}
		ei = e.next
	}

	// Master loading normally reserves this capacity. Direct merge callers
	// need growth support too: a bucket computed before growth is stale.
	bucketCount := len(m.buckets)
	m.preallocate(int(m.numentries) + 1)
	if len(m.buckets) != bucketCount {
		h = m.hash(src.id)
	}

	e, entryIndex := m.newEntry()
	e.id = src.id
	e.next = m.buckets[h]
	e.packIndex = packIndex
	e.offset = src.offset
	e.length = src.length
	e.uncompressedLength = src.uncompressedLength
	m.buckets[h] = bloomInsertID(entryIndex, e.next, src.id)
	m.numentries++
}
