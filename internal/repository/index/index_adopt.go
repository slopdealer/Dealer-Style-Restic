package index

// adoptLoadedIndex removes the empty-destination copy on a fresh load. It is
// only called with an exclusively owned decoder result, never a public Insert
// argument or an index exposed to a callback. Remaining indexes still follow
// ordinary merging, including its sizing and exact-location deduplication.
func (mi *MasterIndex) adoptLoadedIndex(owned *Index) bool {
	if owned == nil {
		return false
	}
	mi.idxMutex.Lock()
	defer mi.idxMutex.Unlock()

	if len(mi.idx) < 2 || mi.idx[1] != owned {
		return false
	}
	empty := mi.idx[0]
	empty.m.RLock()
	isEmpty := empty.final && len(empty.ids) == 0 && len(empty.packs) == 0
	for typ := range empty.byType {
		isEmpty = isEmpty && empty.byType[typ].len() == 0
	}
	empty.m.RUnlock()
	if !isEmpty {
		return false
	}

	owned.m.RLock()
	defer owned.m.RUnlock()
	if !owned.final || len(owned.ids) == 0 {
		return false
	}
	// Ordinary merge also removes duplicates within the first source file.
	// Prove that none exist before changing ownership. Compare complete stored
	// locations, including pack IDs rather than per-file pack ordinals.
	// No temporary representation is allocated on the fallback path.
	for typ := range owned.byType {
		m := &owned.byType[typ]
		for e := range m.values() {
			// Existing links already describe earlier entries in this bucket.
			// Check each pair once without hashing every entry again.
			for pos := e.next; bloomHasID(pos, e.id); {
				other := m.resolve(pos)
				pos = other.next
				if other.id == e.id && other.offset == e.offset && other.length == e.length &&
					other.uncompressedLength == e.uncompressedLength &&
					owned.packs[other.packIndex] == owned.packs[e.packIndex] {
					return false
				}
			}
		}
	}

	mi.idx[0] = owned
	copy(mi.idx[1:], mi.idx[2:])
	mi.idx[len(mi.idx)-1] = nil
	mi.idx = mi.idx[:len(mi.idx)-1]
	return true
}
