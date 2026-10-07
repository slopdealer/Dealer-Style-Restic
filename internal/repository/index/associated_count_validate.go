package index

// AssociatedCountsInRange reports whether every admitted count is in [min, max].
// Like All, it requires that the caller serialize changes to the associated set.
// It does not enumerate handles or change All's occurrence and ordering semantics.
func AssociatedCountsInRange(a *AssociatedSet[uint8], min, max uint8) bool {
	if valid, supported := associatedCountsInRangeStable(a, min, max); supported {
		return valid
	}
	for _, count := range a.All() {
		if count < min || count > max {
			return false
		}
	}
	return true
}

// The compact arrays suffice when there is no overflow shadowing and the whole
// index still fits the arrays allocated for this set. Otherwise All remains the
// authority, including entries in unmerged indexes and handles added later.
func associatedCountsInRangeStable(a *AssociatedSet[uint8], min, max uint8) (bool, bool) {
	if len(a.overflow) != 0 {
		return false, false
	}
	mi := a.idx
	mi.idxMutex.RLock()
	defer mi.idxMutex.RUnlock()
	if len(mi.idx) != 1 {
		return false, false
	}
	idx := mi.idx[0]
	idx.m.RLock()
	defer idx.m.RUnlock()
	if !idx.final {
		return false, false
	}
	for typ := 1; typ < len(a.byType); typ++ {
		if int(idx.byType[typ].len())+1 != len(a.byType[typ].value) {
			return false, false
		}
	}
	for typ := 1; typ < len(a.byType); typ++ {
		bt := &a.byType[typ]
		for pos, present := range bt.isSet {
			if present && (bt.value[pos] < min || bt.value[pos] > max) {
				return false, true
			}
		}
	}
	return true, true
}
