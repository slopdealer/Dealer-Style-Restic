package repository

import (
	"bufio"
	"context"
	"fmt"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
)

// Keep each worker's decoder across successful, failed and retried packs.
// Compare errors and retry counts with the pre-cleanup checker, then verify
// that a subsequent compressed pack remains readable after each transition.
func TestCheckerReaderReleaseAndReuse(t *testing.T) {
	key := crypto.NewRandomKey()
	for _, concurrency := range []int{1, 4} {
		t.Run(fmt.Sprint(concurrency), func(t *testing.T) {
			dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(concurrency))
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			rd := bufio.NewReaderSize(nil, maxStreamBufferSize)
			cases := []struct{ kind, mode string }{
				{"compressed", ""}, {"compressed-corrupt", ""},
				{"compressed", "backend-retry"}, {"compressed", "partial-good"},
				{"compressed", "partial-different"}, {"compressed", "partial"},
				{"compressed", "pack-hash"}, {"auth", ""},
				{"wrong-hash", ""}, {"compressed", "cancel"},
				{"ordinary", ""}, {"compressed", ""},
			}
			for _, tc := range cases {
				data, plan, id := b11MakePack(key, 128<<10, tc.kind)
				run := func(old bool) (string, int) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					be := &b11ScriptBackend{data: data, mode: tc.mode, cancel: cancel}
					repo := b11Repository(key, be, id, plan)
					var got error
					if old {
						oldDec, e := zstd.NewReader(nil, zstd.WithDecoderConcurrency(concurrency))
						if e != nil {
							t.Fatal(e)
						}
						defer oldDec.Close()
						got = b11BaselineCheckPack(ctx, repo, id, append(pack.Blobs(nil), plan...), int64(len(data)), bufio.NewReaderSize(nil, maxStreamBufferSize), oldDec)
					} else {
						got = checkPack(ctx, repo, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec)
					}
					if got == nil {
						return "", be.calls
					}
					return fmt.Sprintf("%T:%v", got, got), be.calls
				}
				want, wantCalls := run(true)
				got, gotCalls := run(false)
				if got != want || gotCalls != wantCalls {
					t.Fatalf("%+v: errors %q / %q, attempts %d / %d", tc, got, want, gotCalls, wantCalls)
				}
				if rd.Buffered() != 0 {
					t.Fatalf("%+v: reader retained %d buffered bytes", tc, rd.Buffered())
				}
				if _, e := dec.Read(make([]byte, 1)); e != zstd.ErrDecoderNilInput {
					t.Fatalf("%+v: decoder not detached: %v", tc, e)
				}
			}
			t.Logf("matched %d pack transitions; reusable readers detached", len(cases))
		})
	}
}
