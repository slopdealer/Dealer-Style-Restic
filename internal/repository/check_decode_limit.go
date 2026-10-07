package repository

import (
	"io"

	"github.com/klauspost/compress/zstd"
)

// checkDecodedSizeLimit is the number of bytes to which check decodes one
// blob at most. It is the limit of the decoder with which upstream's check
// decodes a blob into memory (zstd's default limit for DecodeAll); a blob
// that decodes to more is damaged for upstream's check and for restore.
// Check hashes the decoded bytes as a stream here, and a stream has no such
// limit of its own, so the bytes are counted.
const checkDecodedSizeLimit uint64 = 64 << 30

// zstdBlockSizeBits is the width of the size field of a zstd block. A block
// takes at least one byte of input and decodes to less than 1<<21 bytes, so
// n bytes of input decode to less than n<<21 bytes.
const zstdBlockSizeBits = 21

// limitedHashWriter passes at most left bytes on and then fails with the
// error upstream's decoder returns for output beyond its limit.
type limitedHashWriter struct {
	w    io.Writer
	left uint64
}

func (l *limitedHashWriter) Write(p []byte) (int, error) {
	if uint64(len(p)) > l.left {
		return 0, zstd.ErrDecoderSizeExceeded
	}
	l.left -= uint64(len(p))
	return l.w.Write(p)
}

// hashDecoded writes the decoded content of compressed, on which b.dec has
// been reset, to b.blobHash. Content beyond b.hashLimit fails with
// zstd.ErrDecoderSizeExceeded, as it does in upstream's DecodeAll.
func (b *packBlobIterator) hashDecoded(compressed []byte) error {
	limit := b.hashLimit
	if uint64(len(compressed)) < limit>>zstdBlockSizeBits {
		// too short to decode to more than the limit
		_, err := io.Copy(b.blobHash, b.dec)
		return err
	}

	// DecodeAll refuses a frame which declares more content than the limit
	// before it decodes any of it, once the frame's window has been accepted;
	// a window no decoder accepts is left to the decoder. Later frames are
	// refused when the output passes the limit.
	var hdr zstd.Header
	if err := hdr.Decode(compressed); err == nil && hdr.HasFCS && hdr.FrameContentSize > limit && hdr.WindowSize <= zstd.MaxWindowSize {
		return zstd.ErrDecoderSizeExceeded
	}
	b.hashWriter = limitedHashWriter{w: b.blobHash, left: limit}
	_, err := io.Copy(&b.hashWriter, b.dec)
	return err
}
