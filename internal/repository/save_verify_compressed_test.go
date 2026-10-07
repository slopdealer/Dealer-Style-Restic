package repository

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
)

func encryptSaveVerifyTest(r *Repository, p []byte) []byte {
	nonce := crypto.NewRandomNonce()
	return r.key.Seal(append([]byte(nil), nonce...), nonce, p, nil)
}

// This is the inherited verifier, used as the error/acceptance oracle.
func eagerSaveVerifyTest(r *Repository, buf []byte, length int, id restic.ID) error {
	if r.opts.NoExtraVerify {
		return nil
	}
	n := r.key.NonceSize()
	p, err := r.key.Open(nil, buf[:n], buf[n:], nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	if length != 0 {
		p, err = r.getZstdDecoder().DecodeAll(p, nil)
		if err != nil {
			return fmt.Errorf("decompression failed: %w", err)
		}
	}
	if !restic.Hash(p).Equal(id) {
		return fmt.Errorf("hash mismatch")
	}
	return nil
}

func TestSavedStreamVerifyEquivalence(t *testing.T) {
	r := &Repository{key: crypto.NewRandomKey()}
	defer func() { r.getZstdDecoder().Close() }()
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(512<<10), zstd.WithEncoderCRC(true))
	if err != nil { t.Fatal(err) }
	defer enc.Close()
	wide, err := zstd.NewWriter(nil, zstd.WithWindowSize(2<<20), zstd.WithEncoderCRC(true))
	if err != nil { t.Fatal(err) }
	defer wide.Close()
	p := bytes.Repeat([]byte("a bounded verification example "), (4<<20)/31+1)
	frame := enc.EncodeAll(p, nil)
	late := append([]byte(nil), frame...)
	late[len(late)-1] ^= 1
	second := enc.EncodeAll([]byte("tail"), nil)
	joined := append(append([]byte(nil), frame...), second...)
	for _, tc := range []struct { name string; compressed []byte; id restic.ID }{
		{"valid", frame, restic.Hash(p)},
		{"wrong-content", frame, restic.Hash([]byte("wrong"))},
		{"truncated", frame[:len(frame)-3], restic.Hash(p)},
		{"checksum-and-wrong-id", late, restic.Hash([]byte("wrong"))},
		{"concatenated", joined, restic.Hash(append(append([]byte(nil), p...), []byte("tail")...))},
		{"trailing-corruption", append(append([]byte(nil), frame...), 1, 2, 3), restic.Hash(p)},
		{"larger-window", wide.EncodeAll(p, nil), restic.Hash(p)},
		{"empty-frame", enc.EncodeAll(nil, nil), restic.Hash(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := encryptSaveVerifyTest(r, tc.compressed)
			before := append([]byte(nil), buf...)
			got := r.verifyCiphertext(buf, len(p), tc.id)
			want := eagerSaveVerifyTest(r, buf, len(p), tc.id)
			if fmt.Sprint(got) != fmt.Sprint(want) { t.Fatalf("got %v, want %v", got, want) }
			if !bytes.Equal(buf, before) { t.Fatal("verification modified ciphertext") }
		})
	}
	buf := encryptSaveVerifyTest(r, frame)
	buf[len(buf)-1] ^= 1
	if err := r.verifyCiphertext(buf, len(p), restic.Hash(p)); err == nil || !strings.Contains(err.Error(), "decryption failed") { t.Fatalf("authentication: %v", err) }
	r.opts.NoExtraVerify = true
	if err := r.verifyCiphertext(buf, len(p), restic.Hash(p)); err != nil { t.Fatalf("NoExtraVerify: %v", err) }
}

func TestSavedStreamVerifyDispatchAndConcurrent(t *testing.T) {
	r := &Repository{key: crypto.NewRandomKey()}
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(512<<10))
	if err != nil { t.Fatal(err) }
	defer enc.Close()
	p := make([]byte, 4<<20)
	_, _ = rand.New(rand.NewSource(18)).Read(p)
	frame := enc.EncodeAll(p, nil)
	id := restic.Hash(p)
	if handled, err := verifySavedCompressedHash(frame, id); !handled || err != nil { t.Fatalf("normal window dispatch %v: %v", handled, err) }
	wide, err := zstd.NewWriter(nil, zstd.WithWindowSize(2<<20))
	if err != nil { t.Fatal(err) }
	defer wide.Close()
	if handled, _ := verifySavedCompressedHash(wide.EncodeAll(p, nil), id); handled { t.Fatal("large window must fall back") }
	buf := encryptSaveVerifyTest(r, frame)
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 3 { if err := r.verifyCiphertext(buf, len(p), id); err != nil { t.Error(err) } }
		}()
	}
	wg.Wait()
}

func TestSavedStreamCumulativeOutputLimit(t *testing.T) {
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(512<<10), zstd.WithEncoderCRC(true))
	if err != nil { t.Fatal(err) }
	defer enc.Close()
	part := bytes.Repeat([]byte{19}, 128<<10)
	frame := enc.EncodeAll(part, nil)
	joined := append(append([]byte(nil), frame...), frame...)
	id := restic.Hash(append(append([]byte(nil), part...), part...))
	if handled, _ := verifySavedCompressedHashLimit(joined, id, 128<<10); handled {
		t.Fatal("individually valid frames bypassed cumulative output limit")
	}
	if handled, err := verifySavedCompressedHashLimit(joined, id, 256<<10); !handled || err != nil {
		t.Fatalf("exact-limit complete stream declined: %v %v", handled, err)
	}
	joined[len(joined)-1] ^= 1
	if handled, _ := verifySavedCompressedHashLimit(joined, id, 256<<10); handled {
		t.Fatal("late checksum error escaped fallback")
	}
}

// Reaching the output budget is not completion: a following frame/header or
// checksum still has to settle before the hash can be accepted or rejected.
func TestSavedStreamExactLimitTerminalSettlement(t *testing.T) {
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(512<<10), zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	part := bytes.Repeat([]byte{37}, 128<<10)
	frame := enc.EncodeAll(part, nil)
	id := restic.Hash(part)
	for _, tc := range []struct {
		name string
		tail []byte
	}{
		{"partial-next-frame", []byte{0x28, 0xb5, 0x2f}},
		{"trailing-garbage", []byte{1, 2, 3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := append(append([]byte(nil), frame...), tc.tail...)
			if handled, err := verifySavedCompressedHashLimit(input, id, int64(len(part))); handled || err != nil {
				t.Fatalf("terminal failure must decline for inherited diagnostics: %v %v", handled, err)
			}
		})
	}
	if handled, err := verifySavedCompressedHashLimit(frame, restic.Hash([]byte("wrong")), int64(len(part))); !handled || err == nil || err.Error() != "hash mismatch" {
		t.Fatalf("complete exact-limit wrong content: %v %v", handled, err)
	}
	// A further frame containing even one byte must decline, even when the ID
	// names only the prefix that fitted the budget.
	input := append(append([]byte(nil), frame...), enc.EncodeAll([]byte{9}, nil)...)
	if handled, err := verifySavedCompressedHashLimit(input, id, int64(len(part))); handled || err != nil {
		t.Fatalf("extra concatenated output must decline: %v %v", handled, err)
	}
}

// Complete saving includes encryption, extra verification, packer writes and
// final uploader settlement. Each benchmark gets an independent repository.
func BenchmarkB18CompleteSaveVerify(b *testing.B) {
	for _, size := range []int{1<<20, 4<<20, 8<<20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			TestUseLowSecurityKDFParameters(b)
			r, _, _ := TestRepositoryWithVersion(b, 2)
			p := bytes.Repeat([]byte("representations and their lifetimes\n"), size/35+1)[:size]
			id := restic.Hash(p)
			b.ReportAllocs()
			b.SetBytes(int64(size))
			b.ResetTimer()
			err := r.WithBlobUploader(context.Background(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
				for range b.N {
					if _, _, _, err := u.SaveBlob(ctx, restic.DataBlob, p, id, true); err != nil { return err }
				}
				return nil
			})
			if err != nil { b.Fatal(err) }
		})
	}
}
