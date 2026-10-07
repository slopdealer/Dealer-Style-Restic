package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"math/rand"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/restic"
)

func TestB23VerifyRejectedSaveSubmitsNoPack(t *testing.T) {
	r, _, _ := TestRepositoryWithVersion(t, 2)
	p := bytes.Repeat([]byte("compressible and fully verified\n"), (4<<20)/31+1)
	wrong := restic.Hash([]byte("different bytes"))
	err := r.WithBlobUploader(t.Context(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
		_, _, _, err := u.SaveBlob(ctx, restic.DataBlob, p, wrong, true)
		return err
	})
	want := fmt.Sprintf("Detected data corruption while saving blob %v: hash mismatch\nCorrupted blobs are either caused by hardware issues or software bugs. Please open an issue at https://github.com/restic/restic/issues/new/choose for further troubleshooting.", wrong)
	if err == nil || err.Error() != want {
		t.Fatalf("rejected save: %v", err)
	}
	if len(r.idx.Lookup(restic.BlobHandle{Type: restic.DataBlob, ID: wrong})) != 0 {
		t.Fatal("rejected blob entered index")
	}
	packs := 0
	if err := r.List(t.Context(), restic.PackFile, func(restic.ID, int64) error { packs++; return nil }); err != nil {
		t.Fatal(err)
	}
	if packs != 0 {
		t.Fatal("rejected save submitted a pack")
	}
}

func TestB23VerifyWriterBudget(t *testing.T) {
	h := sha256.New()
	w := saveVerifyHashWriter{h: h, remaining: 3}
	if n, err := w.Write([]byte("abc")); n != 3 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := w.Write(nil); n != 0 || err != nil {
		t.Fatal(n, err)
	}
	if n, err := w.Write([]byte("d")); n != 0 || err == nil || !w.exceeded {
		t.Fatal(n, err)
	}
	id := restic.Hash([]byte("abc"))
	if !bytes.Equal(h.Sum(nil), id[:]) {
		t.Fatal("excess output entered hash")
	}
}

// DecodeAll is independent of the streaming WriteTo organization. Every
// ordinary bounded stream must be handled, and no failed decode
// or excess output may be accepted even when its prefix has the expected ID.
func TestB23VerifyDecoderDifferential(t *testing.T) {
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(512<<10), zstd.WithEncoderCRC(true), zstd.WithZeroFrames(true))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	rng := rand.New(rand.NewSource(23))
	for _, size := range []int{0, 1, 63, 128 << 10, (2 << 20) + 1} {
		p := make([]byte, size)
		_, _ = rng.Read(p)
		frame := enc.EncodeAll(p, nil)
		empty := enc.EncodeAll(nil, nil)
		inputs := [][]byte{frame, append(append([]byte(nil), frame...), empty...), append(append([]byte(nil), empty...), frame...)}
		for _, tail := range [][]byte{{1}, {0x28, 0xb5, 0x2f}, enc.EncodeAll([]byte{7}, nil)} {
			inputs = append(inputs, append(append([]byte(nil), frame...), tail...))
		}
		for _, cut := range []int{0, 1, len(frame) / 2, len(frame) - 1} {
			inputs = append(inputs, frame[:cut])
		}
		for range 12 {
			mutated := append([]byte(nil), frame...)
			mutated[rng.Intn(len(mutated))] ^= byte(1 + rng.Intn(255))
			inputs = append(inputs, mutated)
		}
		for i, input := range inputs {
			decoded, eagerErr := dec.DecodeAll(input, nil)
			for _, limit := range []int64{0, int64(size), int64(size) + 1} {
				for _, id := range []restic.ID{restic.Hash(p), restic.Hash([]byte("wrong"))} {
					handled, got := verifySavedCompressedHashLimit(input, id, limit)
					if eagerErr != nil || int64(len(decoded)) > limit {
						if handled || got != nil {
							t.Fatalf("size=%d case=%d limit=%d accepted invalid stream: %v %v", size, i, limit, handled, got)
						}
						continue
					}
					want := "<nil>"
					if !restic.Hash(decoded).Equal(id) {
						want = "hash mismatch"
					}
					// A mutated header can request a wider window: declining is
					// legal, because the eager decoder supplies the final result.
					if !handled && i >= 3 && got == nil {
						continue
					}
					if !handled || fmt.Sprint(got) != want {
						t.Fatalf("size=%d case=%d limit=%d: %v %v want %s", size, i, limit, handled, got, want)
					}
				}
			}
		}
	}
}

func BenchmarkB23CompressedVerify(b *testing.B) {
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(512<<10))
	if err != nil {
		b.Fatal(err)
	}
	defer enc.Close()
	p := bytes.Repeat([]byte("exact verification output\n"), (4<<20)/26)
	frame := enc.EncodeAll(p, nil)
	id := restic.Hash(p)
	b.SetBytes(int64(len(p)))
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if handled, err := verifySavedCompressedHash(frame, id); !handled || err != nil {
			b.Fatal(handled, err)
		}
	}
}
