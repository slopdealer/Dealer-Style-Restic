package repository

import (
	"bytes"
	"crypto/sha256"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/restic"
)

// Decoder construction owns roughly a megabyte of scratch even for small
// output. Leave those blobs on the shared eager decoder.
const streamedSaveVerifyMinSize = 2 << 20

// verifySavedCompressedHash consumes authenticated compressed bytes. Large
// blobs do not need a second complete decoded representation just to hash it.
// The normal saver encodes with a 512 KiB window. A larger-window frame or
// any stream error declines this route, preserving the ordinary decoder's
// interpretation and diagnostic precedence. No partial hash can succeed.
// Scratch is operation-owned and closed before packer contention begins.
func verifySavedCompressedHash(compressed []byte, id restic.ID) (bool, error) {
	return verifySavedCompressedHashLimit(compressed, id, 16*1024*1024*1024)
}

// Match DecodeAll's cumulative output limit, including concatenated frames.
// Exact-limit output still requires the decoder to settle its terminal state.
func verifySavedCompressedHashLimit(compressed []byte, id restic.ID, limit int64) (bool, error) {
	dec, err := zstd.NewReader(bytes.NewReader(compressed),
		zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true),
		zstd.WithDecoderMaxWindow(512<<10),
		zstd.WithDecoderMaxMemory(16*1024*1024*1024),
		zstd.WithDecodeBuffersBelow(0))
	if err != nil {
		return false, nil
	}
	defer dec.Close()
	h := sha256.New()
	bounded := &saveVerifyHashWriter{h: h, remaining: limit}
	_, err = dec.WriteTo(bounded)
	if err != nil || bounded.exceeded {
		return false, nil
	}
	var sum [sha256.Size]byte
	var got restic.ID
	copy(got[:], h.Sum(sum[:0]))
	if !got.Equal(id) {
		return true, errors.New("hash mismatch")
	}
	return true, nil
}
