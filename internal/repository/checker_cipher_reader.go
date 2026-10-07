package repository

import (
	"io"

	"github.com/restic/restic/internal/repository/pack"
)

// Only ordinary checking uses this hint. Do not retain the largest buffer across
// packs or trust an index hint to make an unbounded speculative allocation.
// Allocation remains lazy: packs whose blobs fit the existing buffered reader
// acquire no separate ciphertext buffer.
func ordinaryCheckCipherReserve(blobs pack.Blobs, size int64) int {
	const maxReserve = 16 << 20
	var largest uint
	for _, blob := range blobs {
		if blob.Offset+blob.Length < blob.Offset || size < 0 || uint64(blob.Offset+blob.Length) > uint64(size) {
			return 0
		}
		largest = max(largest, blob.Length)
	}
	if largest > maxReserve || int64(largest) > size {
		return 0
	}
	return int(largest)
}

// The discardReader contract allows modification of returned bytes until the
// next reader operation. Peek and Discard leave those bytes intact. The pack
// hashing reader has already hashed their ciphertext before in-place decryption.
func (b *bufReader) readFullOrdinary(n int) ([]byte, error) {
	if n <= b.rd.Size() {
		buf, err := b.rd.Peek(n)
		if err != nil {
			_, _ = b.rd.Discard(len(buf))
			if err == io.EOF && len(buf) != 0 {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		_, err = b.rd.Discard(n)
		return buf[:n:n], err
	}
	if cap(b.buf) < n {
		b.buf = make([]byte, max(n, b.reserve))
	}
	b.buf = b.buf[:n]
	_, err := io.ReadFull(b.rd, b.buf)
	if err != nil {
		return nil, err
	}
	return b.buf, nil
}
