package restorer

// zeroRestoreOffset is shared only for the first offset of an association.
// Its capacity is one: appending a repeated occurrence allocates independent
// storage, so no worker or file can modify another association's offsets.
// Callback consumers only read offsets.
var zeroRestoreOffset = [1]int64{0}

func appendRestoreOffset(offsets []int64, offset int64) []int64 {
	if len(offsets) == 0 && offset == 0 {
		return zeroRestoreOffset[:]
	}
	return append(offsets, offset)
}
