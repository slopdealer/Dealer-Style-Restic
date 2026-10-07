package repository

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
)

// A backend can fail after a callback has consumed a complete pack. That
// callback has released its inputs even though the task has not settled.
type ordinaryCipherSettlementBackend struct {
	backend.Backend
	data                                   []byte
	rd                                     *bufio.Reader
	dec                                    *zstd.Decoder
	t                                      *testing.T
	calls                                  int
	corruptFirst, corruptSecond, failFirst bool
}

func (b *ordinaryCipherSettlementBackend) Load(_ context.Context, _ backend.Handle, _ int, _ int64, consume func(io.Reader) error) error {
	b.calls++
	data := bytes.Clone(b.data)
	if (b.calls == 1 && b.corruptFirst) || (b.calls == 2 && b.corruptSecond) {
		data[0] ^= 1
	}
	err := consume(bytes.NewReader(data))
	if b.rd.Buffered() != 0 {
		b.t.Fatal("callback returned with buffered input attached")
	}
	if _, e := b.dec.Read(make([]byte, 1)); e != zstd.ErrDecoderNilInput {
		b.t.Fatalf("callback returned with decoder input attached: %v", e)
	}
	clear(data) // Backend storage may be recycled as soon as the callback returns.
	if b.calls == 1 && b.failFirst {
		return fmt.Errorf("backend failed after complete callback")
	}
	return err
}

func TestOrdinaryCipherCallbackCommitAndRetry(t *testing.T) {
	key := crypto.NewRandomKey()
	for _, concurrency := range []int{1, 4} {
		for _, size := range []int{128 << 10, 2 << 20} {
			for _, tc := range []struct {
				name                                   string
				corruptFirst, corruptSecond, failFirst bool
				want                                   string
			}{
				{name: "success"},
				{name: "late-backend-failure", failFirst: true, want: "check successful on second attempt, original error download error: backend failed after complete callback"},
				{name: "corruption-then-good", corruptFirst: true, want: "check successful on second attempt, original error"},
				{name: "good-then-corruption", failFirst: true, corruptSecond: true, want: "decrypting blob"},
				{name: "backend-overrides-callback", failFirst: true, corruptFirst: true, want: "check successful on second attempt, original error download error: backend failed after complete callback"},
			} {
				t.Run(fmt.Sprintf("%d/%d/%s", concurrency, size, tc.name), func(t *testing.T) {
					data, plan, id := b11MakePack(key, size, "compressed")
					dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(concurrency))
					if err != nil {
						t.Fatal(err)
					}
					defer dec.Close()
					rd := bufio.NewReaderSize(nil, maxStreamBufferSize)
					be := &ordinaryCipherSettlementBackend{data: data, rd: rd, dec: dec, t: t, corruptFirst: tc.corruptFirst, corruptSecond: tc.corruptSecond, failFirst: tc.failFirst}
					r := b11Repository(key, be, id, plan)
					err = checkPack(context.Background(), r, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec)
					if tc.want == "" {
						if err != nil || be.calls != 1 {
							t.Fatalf("success err=%v calls=%d", err, be.calls)
						}
					} else if err == nil || !strings.Contains(err.Error(), tc.want) || be.calls != 2 {
						t.Fatalf("settlement err=%v calls=%d want=%q", err, be.calls, tc.want)
					}
					// A failed attempt must not poison the worker's next independent task.
					be.calls = 0
					be.corruptFirst = false
					be.corruptSecond = false
					be.failFirst = false
					if err := checkPack(context.Background(), r, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec); err != nil || be.calls != 1 {
						t.Fatalf("next pack err=%v calls=%d", err, be.calls)
					}
				})
			}
		}
	}
}

func TestOrdinaryCipherReserveExtents(t *testing.T) {
	for _, tc := range []struct {
		blobs pack.Blobs
		size  int64
		want  int
	}{
		{pack.Blobs{{Offset: 0, Length: 100}, {Offset: 100, Length: 200}}, 300, 200},
		{pack.Blobs{{Offset: ^uint(0) - 3, Length: 10}}, 100, 0},
		{pack.Blobs{{Length: 100}}, -1, 0},
		{pack.Blobs{{Length: 100}}, 99, 0},
		{pack.Blobs{{Length: 16 << 20}}, 16 << 20, 16 << 20},
		{pack.Blobs{{Length: (16 << 20) + 1}}, 17 << 20, 0},
	} {
		if got := ordinaryCheckCipherReserve(tc.blobs, tc.size); got != tc.want {
			t.Fatalf("reserve=%d want=%d", got, tc.want)
		}
	}
}
