package repository

import (
	"bytes"
	"context"
	"fmt"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/restic"
)

// LoadIndexFile retains authenticated compressed bytes while decoding one pack
// at a time. Load errors and JSON errors are separate so rewrite preserves its
// existing diagnostic wrapping. Any unsupported shape or stream error retries
// interpretation through the ordinary complete decompression and JSON paths.
// The extension lives on the opt-in internal adapter, not Repository. A wrapper
// embedding Repository and overriding LoadUnpacked must not silently inherit a
// second loader which bypasses its override (notably cache-only preparation).
func (r *internalRepository) LoadIndexFile(ctx context.Context, id restic.ID) (idx *index.Index, loadErr, decodeErr error) {
	buf, err := r.LoadRaw(ctx, restic.IndexFile, id)
	if err != nil {
		return nil, err, nil
	}
	if len(buf) < crypto.CiphertextLength(0) {
		return nil, fmt.Errorf("invalid data in %v file, too short", restic.IndexFile), nil
	}
	nonce, ciphertext := buf[:r.key.NonceSize()], buf[r.key.NonceSize():]
	buf, err = r.key.Open(ciphertext[:0], nonce, ciphertext, nil)
	if err != nil {
		return nil, err, nil
	}
	if r.cfg.Version >= 2 && len(buf) > 0 && buf[0] == 2 {
		dec, err := zstd.NewReader(bytes.NewReader(buf[1:]),
			zstd.WithDecoderConcurrency(1),
			zstd.WithDecoderMaxMemory(16*1024*1024*1024))
		if err == nil {
			idx, ok := index.DecodeIndexReader(dec, id)
			dec.Close()
			if ok {
				return idx, nil, nil
			}
		}
	}
	plaintext, err := r.decompressUnpacked(buf)
	if err != nil {
		return nil, err, nil
	}
	idx, err = index.DecodeIndex(plaintext, id)
	return idx, nil, err
}
