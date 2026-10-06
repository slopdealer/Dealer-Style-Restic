package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/restic"
)

// An operation owns this adapter. It retains no plaintext or
// decoder after returning a complete index, including on compatibility fallback.
// Public Repository deliberately does not expose the capability.
type ordinaryIndexStreamRepository struct {
	*internalRepository
}

func (r *Repository) loadIndexesStreaming(ctx context.Context, p restic.Counter, cb func(restic.ID, *index.Index, error) error) error {
	loader := &ordinaryIndexStreamRepository{internalRepository: &internalRepository{r}}
	return r.idx.Load(ctx, loader, p, cb)
}

func (r *ordinaryIndexStreamRepository) LoadIndexStream(ctx context.Context, id restic.ID) (*index.Index, []byte, error) {
	buf, err := r.LoadRaw(ctx, restic.IndexFile, id)
	if err != nil {
		return nil, nil, err
	}
	if len(buf) < crypto.CiphertextLength(0) {
		return nil, nil, fmt.Errorf("invalid data in %v file, too short", restic.IndexFile)
	}
	nonce, ciphertext := buf[:r.key.NonceSize()], buf[r.key.NonceSize():]
	buf, err = r.key.Open(ciphertext[:0], nonce, ciphertext, nil)
	if err != nil {
		return nil, nil, err
	}
	if r.cfg.Version >= 2 && len(buf) >= 1024*1024 && buf[0] == 2 {
		idx, ok := index.DecodeCanonicalIndexStream(func() (io.ReadCloser, error) {
			dec, err := zstd.NewReader(bytes.NewReader(buf[1:]), zstd.WithDecoderConcurrency(1), zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxMemory(16*1024*1024*1024))
			if err != nil {
				return nil, err
			}
			return ordinaryIndexDecoder{dec}, nil
		}, id)
		if ok {
			return idx, nil, nil
		}
	}
	plaintext, err := r.decompressUnpacked(buf)
	return nil, plaintext, err
}

type ordinaryIndexDecoder struct{ *zstd.Decoder }

func (d ordinaryIndexDecoder) Close() error { d.Decoder.Close(); return nil }
