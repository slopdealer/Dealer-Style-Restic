package index

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/restic/restic/internal/restic"
)

// PackBlobsHashOwned consumes the order of a privately owned blob slice.
// EachByPackForCheck transfers such a slice to its consumer; no index entry
// aliases it. Callers needing the original order must use PackBlobsHash.
func PackBlobsHashOwned(pbs PackBlobs) restic.ID {
	h := sha256.New()
	h.Write(pbs.PackID[:])
	pbs.Blobs.Sort()
	var fields [16]byte
	for _, blob := range pbs.Blobs {
		h.Write(blob.ID[:])
		binary.LittleEndian.PutUint32(fields[0:4], uint32(blob.Type))
		binary.LittleEndian.PutUint32(fields[4:8], uint32(blob.Offset))
		binary.LittleEndian.PutUint32(fields[8:12], uint32(blob.Length))
		binary.LittleEndian.PutUint32(fields[12:16], uint32(blob.UncompressedLength))
		h.Write(fields[:])
	}
	return restic.ID(h.Sum(nil))
}
