package archiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

// Research reference: input and output ownership stay with the caller. This is
// deliberately not wired into saving files. At most two validators are active,
// and every call joins them before its immutable input can be reused.
type b18CutTrial struct {
	c                                            [2]*chunker.BaseChunker
	initial                                      chunker.BaseChunker
	pol                                          chunker.Pol
	parallel                                     bool
	accepted, discarded, replay, scheduled, live int
}

func b18NewTrial(pol chunker.Pol, parallel bool) *b18CutTrial {
	return &b18CutTrial{c: [2]*chunker.BaseChunker{chunker.NewBase(pol), chunker.NewBase(pol)}, initial: *chunker.NewBase(pol), pol: pol, parallel: parallel}
}
func (v *b18CutTrial) pair(ctx context.Context, input []byte, first int) ([2]int, error) {
	result := [2]int{-1, -1}
	if first <= 0 || first >= len(input) || len(input) > 2*1024*1024 {
		return result, errors.New("invalid bounded pair")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	v.live = max(v.live, len(input))
	scan := func(i int, b []byte) { *v.c[i] = v.initial; result[i] = v.c[i].NextSplitPoint(b) }
	if v.parallel {
		var wg sync.WaitGroup
		wg.Add(1)
		v.scheduled++
		go func() { defer wg.Done(); scan(1, input[first:]) }()
		scan(0, input[:first])
		wg.Wait()
	} else {
		scan(0, input[:first])
		scan(1, input[first:])
	}
	return result, ctx.Err()
}

// Parent endpoints are hints. After any failed prediction, replay from the
// last proven cut using the current polynomial. Rejoin only at an exact hinted
// start; EOF-only tails are never accepted as restart certificates.
func (v *b18CutTrial) cuts(ctx context.Context, payload []byte, parent []int) ([]int, error) {
	var ends []int
	ordinary := chunker.NewBase(v.pol)
	off, hint := 0, 0
	for off < len(payload) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for hint < len(parent) && parent[hint] <= off {
			hint++
		}
		start := 0
		if hint > 0 {
			start = parent[hint-1]
		}
		if hint+1 < len(parent)-1 && start == off && parent[hint] > off && parent[hint+1] > parent[hint] && parent[hint+1] <= len(payload) && parent[hint+1]-off <= 2*1024*1024 {
			first, end := parent[hint]-off, parent[hint+1]
			pair, err := v.pair(ctx, payload[off:end], first)
			if err != nil {
				return nil, err
			}
			if pair[0] == first {
				off += first
				ends = append(ends, off)
				v.accepted += first
				if pair[1] == end-off {
					v.accepted += end - off
					off = end
					ends = append(ends, off)
					continue
				}
				v.discarded += end - off
				v.replay += end - off
			} else {
				v.discarded += end - off
				v.replay += end - off
			}
		}
		*ordinary = v.initial
		n := ordinary.NextSplitPoint(payload[off:])
		if n < 0 {
			n = len(payload) - off
		}
		off += n
		ends = append(ends, off)
	}
	return ends, nil
}
func b18ContentIDs(payload []byte, ends []int) []restic.ID {
	ids := make([]restic.ID, 0, len(ends))
	start := 0
	for _, end := range ends {
		ids = append(ids, restic.Hash(payload[start:end]))
		start = end
	}
	return ids
}
func TestB18ConditionalCutTrial(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	original := b06Payload(32*1024*1024+173, 881)
	parent := b18Cuts(original, pol, 13001)
	mutated := bytes.Clone(original)
	for i := 700000; i < 710000; i++ {
		mutated[i] ^= 77
	}
	grown := append(bytes.Clone(original), b06Payload(2*1024*1024, 881)...)
	var reciprocal chunker.Pol
	for p := pol; p != 0; p >>= 1 {
		reciprocal = (reciprocal << 1) | (p & 1)
	}
	for _, tc := range []struct {
		name  string
		data  []byte
		hints []int
		pol   chunker.Pol
	}{
		{"unchanged", original, parent, pol}, {"mutation", mutated, parent, pol}, {"growth", grown, parent, pol}, {"truncation", original[:700003], parent, pol},
		{"other-polynomial", original, parent, reciprocal}, {"missing", original, nil, pol}, {"zero", original, []int{0, 0, 0}, pol},
		{"oversized", original, []int{9 * 1024 * 1024, 18 * 1024 * 1024, len(original)}, pol}, {"overflow", original, []int{int(^uint(0) >> 1), -1, len(original)}, pol},
		{"EOF-tail-growth", grown, []int{len(original), len(grown)}, pol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := b18Cuts(tc.data, tc.pol, 13001)
			for _, parallel := range []bool{false, true} {
				v := b18NewTrial(tc.pol, parallel)
				got, err := v.cuts(t.Context(), tc.data, tc.hints)
				if err != nil || !slices.Equal(got, want) || !slices.Equal(b18ContentIDs(tc.data, got), b18ContentIDs(tc.data, want)) {
					t.Fatalf("parallel=%v cuts %v vs %v err=%v", parallel, got, want, err)
				}
				if v.live > 2*1024*1024 {
					t.Fatal("capacity exceeded")
				}
				t.Logf("parallel=%v bytes=%d accepted=%d discarded=%d replay=%d scheduled=%d max_payload=%d", parallel, len(tc.data), v.accepted, v.discarded, v.replay, v.scheduled, v.live)
			}
		})
	}
}
func TestB18ConditionalCutCancellation(t *testing.T) {
	v := b18NewTrial(0x3DA3358B4DC173, true)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := v.pair(ctx, make([]byte, 1024*1024), 524288); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	// A scan may finish after cancellation, but pair must join before returning.
	for i := 0; i < 30; i++ {
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() { defer close(done); _, _ = v.pair(ctx, make([]byte, 1024*1024), 524288) }()
		cancel()
		<-done
	}
}

// This isolates the scan/coordination cost, with no speculative content hashing.
// Same input, same initialized workers, same two scans in both cases.
func BenchmarkB18CutPair(b *testing.B) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(2*1024*1024, 881)
	for _, size := range []int{524288, 786432, 1048576} {
		for _, parallel := range []bool{false, true} {
			b.Run(fmt.Sprintf("%d/parallel=%v", size, parallel), func(b *testing.B) {
				v := b18NewTrial(pol, parallel)
				ctx := context.Background()
				b.SetBytes(int64(2 * size))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if _, err := v.pair(ctx, input[:2*size], size); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

// Streaming reference for the next construction: short reads must not change
// endpoints. This uses ordinary fileChunkState, not an untested new read path.
func TestB18CutShortReadReference(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	payload := b06Payload(2*1024*1024+173, 881)
	rd := &b18TrialShortReader{r: bytes.NewReader(payload), limit: 37}
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
	c := &b18TrialChunker{chunker.NewBase(pol), pol}
	var ends []int
	offset := 0
	scratch := make([]byte, 0, 2*1024*1024)
	for {
		data, err := state.readNextChunk(rd, c, scratch)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		offset += len(data)
		ends = append(ends, offset)
		scratch = data
	}
	if !slices.Equal(ends, b18Cuts(payload, pol, 13001)) {
		t.Fatal(ends)
	}
}

type b18TrialShortReader struct {
	r     io.Reader
	limit int
}

func (r *b18TrialShortReader) Read(p []byte) (int, error) { return r.r.Read(p[:min(len(p), r.limit)]) }

type b18TrialChunker struct {
	*chunker.BaseChunker
	pol chunker.Pol
}

func (c *b18TrialChunker) Reset() { c.BaseChunker.Reset(c.pol) }
