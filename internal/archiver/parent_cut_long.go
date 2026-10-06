package archiver

import (
	"context"
	"io"
	"sync"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

// readLongPair supplements the original bounded route only for oversized pairs
// that fit assembly storage already grown by ordinary chunking. It never grows
// storage to obtain admission. The original route and its minimum-size policy
// remain intact. Parent lengths predict endpoints, not identities or content.
func (g *parentCutReader) readLongPair(ctx context.Context, state *fileChunkState, rd io.Reader, ordinary restic.Chunker, assembly []byte) ([]byte, error, bool) {
	if g.disabled || g.hintOffset != g.offset || g.hint+2 >= len(g.parent.Content) {
		return nil, nil, false
	}
	a, ok := g.span(g.hint)
	if !ok {
		return nil, nil, false
	}
	b, ok := g.span(g.hint + 1)
	if !ok || a <= chunker.MinSize || b <= chunker.MinSize || a+b <= 2*1024*1024 || a+b > uint64(cap(assembly)) {
		return nil, nil, false
	}
	// Both spans have already passed the MaxChunkSize bound. Capacity is an
	// allocation-base capacity, restored by readNext before either route runs.
	input := assembly[:int(a+b)]
	g.root = input[:cap(input)]
	n, err := io.ReadFull(parentCutDrain{state, rd}, input)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err, true
	}
	g.pending = input[:n]
	g.nextCut = 0
	g.pendingHashed = false
	if n < len(input) {
		ordinary.Reset()
		out, err := g.pop(state, rd, ordinary)
		return out, err, true
	}
	if g.extra == nil {
		g.extra = g.factory.NewChunker()
	}
	ordinary.Reset()
	g.extra.Reset()
	hashJob := g.hashes.try(input[:int(a)], input[int(a):])
	var second int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); second = g.extra.NextSplitPoint(input[int(a):]) }()
	first := ordinary.NextSplitPoint(input[:int(a)])
	wg.Wait()
	ids, hashed := hashJob.finish()
	if err := ctx.Err(); err != nil {
		return nil, err, true
	}
	if first == int(a) {
		state.chunkID, state.hashed = ids[0], hashed[0]
		if second == int(b) {
			g.nextCut = int(b)
			g.pendingID, g.pendingHashed = ids[1], hashed[1]
		}
		g.pending = input[int(a):]
		ordinary.Reset()
		return input[:int(a)], nil, true
	}
	ordinary.Reset()
	out, err := g.pop(state, rd, ordinary)
	return out, err, true
}
