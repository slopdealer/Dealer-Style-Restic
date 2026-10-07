package index

// reserveIndexSlots sizes the entry geometry for a counted load, including
// the null entry at position zero. Bucket sizing counts only real blobs.
// No payload blocks are allocated for unfilled slots. Existing blocks may
// move now, instead of moving again near the end of the counted insertion.
func (m *indexMap) reserveIndexSlots(numEntries int) {
	if numEntries > 0 {
		m.blockList.preallocate(uint(numEntries) + 1)
	}
}
