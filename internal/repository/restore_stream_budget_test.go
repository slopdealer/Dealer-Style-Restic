package repository

import (
	"bytes"
	"context"
	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mock"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/restic"
	"io"
	"testing"
	"time"
)

// Request extent and resident ciphertext extent are independent. A full span
// travels in one response while callbacks borrow only one processing window.
func TestCoalescedRestoreProcessingBudget(t *testing.T) {
	key := testKey(t)
	blobs, data := buildPackfileWithoutHeader([]int{600000, 600000, 600000}, &key, true)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	var scratch []byte
	loads, callbacks := 0, 0
	load := func(_ context.Context, _ backend.Handle, n int, off int64, consume func(io.Reader) error) error {
		loads++
		if n <= int(restic.SmallRestoreCiphertextLimit) {
			t.Fatal("request did not combine windows")
		}
		return consume(bytes.NewReader(data[int(off) : int(off)+n]))
	}
	err = streamPackResponse(context.Background(), load, nil, dec, &key, restic.ID{}, blobs, &scratch,
		func(h restic.BlobHandle, b []byte, err error) error {
			callbacks++
			if cap(scratch) > restic.SmallRestoreCiphertextLimit {
				t.Fatalf("ciphertext capacity %d exceeds window", cap(scratch))
			}
			if err != nil {
				return err
			}
			if restic.Hash(b) != h.ID {
				t.Fatal("wrong plaintext")
			}
			return nil
		}, restic.SmallRestoreCiphertextLimit)
	if err != nil || loads != 1 || callbacks != 3 {
		t.Fatalf("err=%v loads=%d callbacks=%d", err, loads, callbacks)
	}
}

// The extended worker borrows medium uncompressed data, including blobs larger than its
// aggregation window. Decoder promotion must occur after releasing the sole
// backend connection, including when the index understates decoded size.
func TestMediumRestoreBudgetAndPromotion(t *testing.T) {
	for _, variant := range []string{"medium", "compressed", "understated", "oversized"} {
		t.Run(variant, func(t *testing.T) {
			key := testKey(t)
			size := 2500000
			if variant == "oversized" {
				size = 4000000
			}
			blobs, data := buildPackfileWithoutHeader([]int{size, 600000}, &key, variant != "medium")
			if variant == "understated" || variant == "oversized" {
				blobs[0].UncompressedLength = 1
			}
			m := mock.NewBackend()
			m.PropertiesFn = func() backend.Properties { return backend.Properties{Connections: 1} }
			loads, admissions, callbacks := 0, 0, 0
			m.OpenReaderFn = func(_ context.Context, _ backend.Handle, n int, off int64) (io.ReadCloser, error) {
				loads++
				if variant == "oversized" && admissions != 1 {
					t.Fatal("oversized selected copy entered without admission")
				}
				return io.NopCloser(bytes.NewReader(data[int(off) : int(off)+n])), nil
			}
			r := &Repository{be: sema.NewBackend(m), key: &key}
			defer r.Close()
			ctx := restic.WithSmallRestoreLoad(context.Background(), func(context.Context) (func(), error) {
				// Prove the sole connection is free by taking it synchronously.
				// Promotion from inside a consumer would deadlock here.
				if variant == "understated" {
					probeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
					defer cancel()
					if err := r.be.Load(sema.WithAdditionalLoadSlot(probeCtx), backend.Handle{Type: backend.PackFile, Name: "probe"}, 1, 0,
						func(rd io.Reader) error { _, err := io.Copy(io.Discard, rd); return err }); err != nil {
						return nil, err
					}
				}
				admissions++
				return func() {}, nil
			})
			err := r.loadBlobsFromPack(ctx, restic.ID{}, blobs, func(h restic.BlobHandle, b []byte, err error) error {
				if callbacks >= len(blobs) || h != blobs[callbacks].BlobHandle {
					t.Fatal("repeated or reordered delivery")
				}
				callbacks++
				if err != nil {
					return err
				}
				if restic.Hash(b) != h.ID {
					t.Fatal("wrong medium plaintext")
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if callbacks != 2 || loads == 0 {
				t.Fatalf("loads/callbacks %d/%d", loads, callbacks)
			}
			wantAdmission := 0
			if variant != "medium" {
				wantAdmission = 1
			}
			if admissions != wantAdmission {
				t.Fatalf("admissions %d, want %d", admissions, wantAdmission)
			}
		})
	}
}
