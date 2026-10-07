package repository

import (
	"bufio"
	"context"
	"fmt"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

const (
	checkCipherCapacity = 2 << 20
	checkHeaderCapacity = 256 << 10
	checkWindowCapacity = 512 << 10
)

// A single extra worker owns these limits. Its decoder remains charged while
// idle. Ciphertext is a fixed reusable allocation, still charged between packs;
// captured headers and per-task references are relinquished after checking.
// Refusal is internal scheduling, never a corruption verdict or backend error.
var errCheckCapacity = errors.New("bounded check capacity exhausted")

type checkCapacityState struct{ cipher []byte }
type checkCapacityRefusal struct{ prior error }

func (e *checkCapacityRefusal) Error() string { return errCheckCapacity.Error() }
func (e *checkCapacityRefusal) Unwrap() error { return errCheckCapacity }

func capacityCheckTask(blobs pack.Blobs, size int64) bool {
	if len(blobs) == 0 || size < 0 {
		return false
	}
	var end uint
	for _, b := range blobs {
		if b.Length > checkCipherCapacity || b.Offset+b.Length < b.Offset {
			return false
		}
		end = max(end, b.Offset+b.Length)
	}
	if end > uint(size) {
		return false
	}
	tail := size - int64(end)
	return tail >= 0 && tail <= checkHeaderCapacity
}

func newCapacityCheckDecoder() (*zstd.Decoder, error) {
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderLowmem(true), zstd.WithDecoderMaxWindow(checkWindowCapacity),
		zstd.WithDecoderMaxMemory(checkWindowCapacity), zstd.WithDecodeBuffersBelow(0))
}

func checkPackCapacity(ctx context.Context, r *Repository, id restic.ID, blobs pack.Blobs, size int64, rd *bufio.Reader, dec *zstd.Decoder, state *checkCapacityState) error {
	err := checkPackInnerWithCapacity(ctx, r, id, blobs, size, rd, dec, state)
	if err == nil || errors.Is(err, errCheckCapacity) {
		return err
	}
	if r.cache != nil {
		_ = r.cache.Forget(backend.Handle{Type: backend.PackFile, Name: id.String()})
	}
	second := checkPackInnerWithCapacity(ctx, r, id, blobs, size, rd, dec, state)
	var refused *checkCapacityRefusal
	if errors.As(second, &refused) {
		// A real first failure still belongs to this task after resource promotion.
		refused.prior = err
		return refused
	}
	if second != nil {
		return second
	}
	return fmt.Errorf("check successful on second attempt, original error %w", err)
}
