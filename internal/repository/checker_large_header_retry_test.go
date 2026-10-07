package repository

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Completed callbacks are attempts, not committed tasks. The backend may retry
// with different bytes or fail after completion, and owns its recycled input.
type largeHeaderAttemptBackend struct {
	backend.Backend
	data  []byte
	mode  string
	calls int
	rd    *bufio.Reader
	dec   *zstd.Decoder
	t     *testing.T
}

func (b *largeHeaderAttemptBackend) Load(_ context.Context, _ backend.Handle, _ int, _ int64, consume func(io.Reader) error) error {
	b.calls++
	attempt := func(corrupt, short bool) error {
		input := bytes.Clone(b.data)
		if corrupt {
			input[len(input)-5] ^= 1
		}
		if short {
			input = input[:len(input)-1]
		}
		err := consume(bytes.NewReader(input))
		if b.rd != nil {
			if b.rd.Buffered() != 0 {
				b.t.Fatal("completed callback retained buffered input")
			}
			if _, e := b.dec.Read(make([]byte, 1)); e != zstd.ErrDecoderNilInput {
				b.t.Fatalf("completed callback retained decoder input: %v", e)
			}
		}
		clear(input)
		return err
	}
	switch b.mode {
	case "complete-then-short":
		if err := attempt(false, false); err != nil {
			return err
		}
		return attempt(false, true)
	case "corrupt-then-good":
		if err := attempt(true, false); err != nil {
			return err
		}
		return attempt(false, false)
	case "late-failure-then-corrupt":
		if b.calls == 1 {
			if err := attempt(false, false); err != nil {
				return err
			}
			return fmt.Errorf("late backend failure")
		}
		return attempt(true, false)
	case "short-then-good":
		return attempt(false, b.calls == 1)
	}
	return attempt(false, false)
}

func TestLargeCheckHeaderChangingAttempts(t *testing.T) {
	key := crypto.NewRandomKey()
	var out bytes.Buffer
	p := pack.NewPacker(key, &out)
	for i := range 2000 {
		plain := binary.LittleEndian.AppendUint32(nil, uint32(i))
		nonce := crypto.NewRandomNonce()
		ciphertext := key.Seal(append([]byte(nil), nonce...), nonce, plain, nil)
		if _, err := p.Add(restic.DataBlob, restic.Hash(plain), ciphertext, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Finalize(); err != nil {
		t.Fatal(err)
	}
	data := out.Bytes()
	plan, _, err := pack.List(key, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if len(data)-int(plan[len(plan)-1].Offset+plan[len(plan)-1].Length) < largeCheckHeaderThreshold {
		t.Fatal("fixture misses large route")
	}
	id := restic.Hash(data)
	for _, concurrency := range []int{1, 4} {
		for _, mode := range []string{"complete-then-short", "corrupt-then-good", "late-failure-then-corrupt", "short-then-good"} {
			t.Run(fmt.Sprintf("%d/%s", concurrency, mode), func(t *testing.T) {
				run := func(old bool) (string, int) {
					dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(concurrency))
					if err != nil {
						t.Fatal(err)
					}
					defer dec.Close()
					rd := bufio.NewReaderSize(nil, maxStreamBufferSize)
					be := &largeHeaderAttemptBackend{data: data, mode: mode, dec: dec, t: t}
					if !old {
						be.rd = rd
					}
					r := b11Repository(key, be, id, plan)
					check := checkPack
					if old {
						check = b11BaselineCheckPack
					}
					err = check(context.Background(), r, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec)
					result := fmt.Sprintf("%T:%v", err, err)
					calls := be.calls
					be.mode, be.calls = "", 0
					if err := check(context.Background(), r, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec); err != nil || be.calls != 1 {
						t.Fatalf("next independent pack: err=%v calls=%d", err, be.calls)
					}
					return result, calls
				}
				want, wc := run(true)
				got, gc := run(false)
				if got != want || gc != wc {
					t.Fatalf("settlement differs: %s/%s calls=%d/%d", got, want, gc, wc)
				}
			})
		}
	}
}
