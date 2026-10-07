package repository

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Mutating a borrowed blob must not overwrite the next blob or the pack tail.
// Large reads allocate once at the validated hint, small reads allocate nothing.
func TestOrdinaryCipherReaderOwnership(t *testing.T) {
	data := bytes.Repeat([]byte("abcdefg"), 100)
	r := newBufReader(bufio.NewReaderSize(bytes.NewReader(data), 64))
	r.ordinary = true
	r.reserve = 200
	pos := 0
	var allocation *byte
	for _, n := range []int{16, 48, 80, 170, 30, 200, 0} {
		got, err := r.ReadFull(n)
		if err != nil || !bytes.Equal(got, data[pos:pos+n]) {
			t.Fatalf("read %d at %d: %v", n, pos, err)
		}
		if n > r.rd.Size() {
			if cap(r.buf) != 200 || (allocation != nil && allocation != &r.buf[0]) {
				t.Fatal("ciphertext grew through multiple allocations")
			}
			allocation = &r.buf[0]
		} else if pos == 0 && r.buf != nil {
			t.Fatal("borrowed read allocated ciphertext")
		}
		clear(got)
		pos += n
	}
	if !bytes.Equal(data, bytes.Repeat([]byte("abcdefg"), 100)) {
		t.Fatal("reader modified backend-owned bytes")
	}
	var rest bytes.Buffer
	if _, err := io.Copy(&rest, r.rd); err != nil || !bytes.Equal(rest.Bytes(), data[pos:]) {
		t.Fatalf("tail damaged: %v", err)
	}
}

func TestOrdinaryCipherReaderShortReads(t *testing.T) {
	for _, size := range []int{0, 1, 15, 16, 17, 63, 64, 65, 100} {
		for _, n := range []int{0, 1, 16, 32, 64, 80, 128} {
			data := bytes.Repeat([]byte{1}, size)
			baseline := bufio.NewReaderSize(bytes.NewReader(data), 64)
			want := make([]byte, n)
			_, wantErr := io.ReadFull(baseline, want)
			r := newBufReader(bufio.NewReaderSize(bytes.NewReader(data), 64))
			r.ordinary = true
			got, gotErr := r.ReadFull(n)
			if gotErr != wantErr || (gotErr == nil && !bytes.Equal(got, want)) {
				t.Fatalf("size=%d n=%d got=%v want=%v", size, n, gotErr, wantErr)
			}
			gotTail, _ := io.ReadAll(r.rd)
			wantTail, _ := io.ReadAll(baseline)
			if !bytes.Equal(gotTail, wantTail) {
				t.Fatalf("size=%d n=%d consumed different prefix", size, n)
			}
		}
	}
	if ordinaryCheckCipherReserve(pack.Blobs{{Length: 17 << 20}}, 18<<20) != 0 || ordinaryCheckCipherReserve(pack.Blobs{{Length: 100}}, 99) != 0 {
		t.Fatal("invalid hint allowed speculative allocation")
	}
}

type ordinaryCipherInterruptedReader struct {
	data []byte
	step int
	err  error
}

func (r *ordinaryCipherInterruptedReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := min(len(p), len(r.data), r.step)
	copy(p, r.data[:n])
	r.data = r.data[n:]
	return n, nil
}

func TestOrdinaryCipherReaderInterruptedInput(t *testing.T) {
	failure := errors.New("interrupted backend callback")
	for _, step := range []int{1, 7, 31, 64, 100} {
		for _, length := range []int{0, 17, 63, 100} {
			for _, n := range []int{16, 64, 80, 128} {
				data := bytes.Repeat([]byte{9}, length)
				baseline := bufio.NewReaderSize(&ordinaryCipherInterruptedReader{data: data, step: step, err: failure}, 64)
				r := newBufReader(bufio.NewReaderSize(&ordinaryCipherInterruptedReader{data: data, step: step, err: failure}, 64))
				r.ordinary = true
				want := make([]byte, n)
				_, wantErr := io.ReadFull(baseline, want)
				got, gotErr := r.ReadFull(n)
				if wantErr != gotErr || (gotErr == nil && !bytes.Equal(got, want)) {
					t.Fatalf("step=%d length=%d n=%d errors %v / %v", step, length, n, gotErr, wantErr)
				}
			}
		}
	}
}

// Compare complete error text and attempts with the old checker, using the
// same reusable decoder across borrowed, large, corrupt and canceled packs.
func TestOrdinaryCipherCheckerFailureSettlement(t *testing.T) {
	key := crypto.NewRandomKey()
	for _, concurrency := range []int{1, 4} {
		dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(concurrency))
		if err != nil {
			t.Fatal(err)
		}
		rd := bufio.NewReaderSize(nil, maxStreamBufferSize)
		for _, n := range []int{128 << 10, 1100 << 10, 3 << 20} {
			for _, tc := range []struct{ kind, mode string }{
				{"compressed", ""}, {"ordinary", ""}, {"compressed-corrupt", ""},
				{"compressed", "backend-retry"}, {"compressed", "partial-good"},
				{"compressed", "partial-different"}, {"compressed", "partial"},
				{"compressed", "total"}, {"compressed", "pack-hash"},
				{"auth", ""}, {"wrong-hash", ""}, {"compressed", "cancel"},
				{"compressed", ""},
			} {
				data, plan, id := b11MakePack(key, n, tc.kind)
				run := func(old bool) (string, int) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					be := &b11ScriptBackend{data: data, mode: tc.mode, cancel: cancel}
					repo := b11Repository(key, be, id, plan)
					var e error
					if old {
						oldDec, e2 := zstd.NewReader(nil, zstd.WithDecoderConcurrency(concurrency))
						if e2 != nil {
							t.Fatal(e2)
						}
						e = b11BaselineCheckPack(ctx, repo, id, append(pack.Blobs(nil), plan...), int64(len(data)), bufio.NewReaderSize(nil, maxStreamBufferSize), oldDec)
						oldDec.Close()
					} else {
						e = checkPack(ctx, repo, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec)
					}
					if e == nil {
						return "", be.calls
					}
					return fmt.Sprintf("%T:%v", e, e), be.calls
				}
				want, wantCalls := run(true)
				got, gotCalls := run(false)
				if want != got || wantCalls != gotCalls {
					t.Fatalf("n=%d %+v errors=%q/%q attempts=%d/%d", n, tc, got, want, gotCalls, wantCalls)
				}
			}
		}
		dec.Close()
	}
}

// A borrowed compressed input is followed by a large ciphertext allocation and
// another borrowed input in the same backend callback. Corruption in the first
// blob must remain local while later blobs and the complete pack hash are checked.
func TestOrdinaryCipherMixedPackSettlement(t *testing.T) {
	key := crypto.NewRandomKey()
	for _, corrupt := range []bool{false, true} {
		var out bytes.Buffer
		p := pack.NewPacker(key, &out)
		enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
		if err != nil {
			t.Fatal(err)
		}
		for i, n := range []int{128 << 10, 2 << 20, 256 << 10, 4 << 20, 64 << 10} {
			plain := make([]byte, n)
			_, _ = rand.New(rand.NewSource(int64(700 + i))).Read(plain)
			payload, uncompressed := plain, 0
			if i%2 == 0 {
				payload, uncompressed = enc.EncodeAll(plain, nil), n
				if corrupt && i == 0 {
					payload = []byte("invalid zstd frame")
				}
			}
			nonce := crypto.NewRandomNonce()
			cipher := key.Seal(append([]byte(nil), nonce...), nonce, payload, nil)
			if _, err := p.Add(restic.DataBlob, restic.Hash(plain), cipher, uncompressed); err != nil {
				t.Fatal(err)
			}
		}
		enc.Close()
		if err := p.Finalize(); err != nil {
			t.Fatal(err)
		}
		data := out.Bytes()
		blobs, _, err := pack.List(key, bytes.NewReader(data), int64(len(data)))
		if err != nil {
			t.Fatal(err)
		}
		id := restic.Hash(data)
		for _, concurrency := range []int{1, 4} {
			results := [2]string{}
			calls := [2]int{}
			for side := range 2 {
				be := &b11ScriptBackend{data: data}
				r := b11Repository(key, be, id, blobs)
				dec, e := zstd.NewReader(nil, zstd.WithDecoderConcurrency(concurrency))
				if e != nil {
					t.Fatal(e)
				}
				rd := bufio.NewReaderSize(nil, maxStreamBufferSize)
				if side == 0 {
					e = b11BaselineCheckPack(context.Background(), r, id, append(pack.Blobs(nil), blobs...), int64(len(data)), rd, dec)
				} else {
					e = checkPack(context.Background(), r, id, append(pack.Blobs(nil), blobs...), int64(len(data)), rd, dec)
				}
				dec.Close()
				if e != nil {
					results[side] = fmt.Sprintf("%T:%v", e, e)
				}
				calls[side] = be.calls
			}
			if results[0] != results[1] || calls[0] != calls[1] || (!corrupt && results[1] != "") || (corrupt && results[1] == "") {
				t.Fatalf("corrupt=%v concurrency=%d results=%q calls=%v", corrupt, concurrency, results, calls)
			}
		}
	}
}
