package repository

import (
	"runtime"

	"github.com/klauspost/compress/zstd"

	"github.com/restic/restic/internal/backend"
)

type additionalLoadSlotBackend interface {
	backend.Backend
	AllowAdditionalLoadSlot() bool
}

// AdditionalRestoreWorker reports whether a restore may run one ordinary
// worker more than configured: only on a backend that grants the additional
// load slot, which is the local backend with its default connection count
// (an explicit -o local.connections=N keeps its cap). Every other backend
// restores with the configured workers exactly as before.
func (r *Repository) AdditionalRestoreWorker() bool {
	be := backend.AsBackend[additionalLoadSlotBackend](r.be)
	return be != nil && be.AllowAdditionalLoadSlot()
}

// The ordinary restore pack streams decode with their own decoder. The
// repository's general decoder keeps one block decoder per core and hands them
// out in rotation, so every one of them grows to the largest block it has
// seen. This one keeps as many block decoders as the restore has configured
// workers: the additional restore worker shares them and waits for one when
// all are busy, instead of bringing another decoder's state with it.
//
// Decoding is the same DecodeAll with the same memory limit; only the number
// of block decoders differs.
func (r *Repository) getRestoreZstdDecoder(workers int) *zstd.Decoder {
	r.allocRestoreDec.Do(func() {
		if limit := runtime.GOMAXPROCS(0); workers > limit {
			workers = limit
		}
		if workers < 1 {
			workers = 1
		}
		dec, err := zstd.NewReader(nil,
			zstd.WithDecoderConcurrency(workers),
			zstd.WithDecoderMaxMemory(16*1024*1024*1024))
		if err != nil {
			panic(err)
		}
		r.restoreDec = dec
	})
	return r.restoreDec
}
