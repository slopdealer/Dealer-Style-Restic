package archiver

import (
	"context"
	"io"
	"sync"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// parentCutReader borrows only the file worker's assembly allocation. It does
// not own speculative payload storage. At most one additional validator runs;
// it is joined before emitting a chunk, reporting an error or reusing bytes.
// Hints predict cuts, never content IDs. EOF tails cannot certify a restart.
type parentCutLength struct {
	position         int
	length           uint64
	populated, valid bool
}

type parentCutReader struct {
	lengths                                 [2]parentCutLength
	disabled                                bool
	parent                                  *data.Node
	lookup                                  func(restic.BlobHandle) (uint, bool)
	factory                                 restic.ChunkerFactory
	extra                                   restic.Chunker
	hashes                                  *parentPairHashLane
	pendingID                               restic.ID
	pendingHashed                           bool
	offset, hintOffset                      uint64
	hint                                    int
	root, pending                           []byte
	nextCut                                 int
	accepted, replayed, scheduled, capacity uint64
}

func (g *parentCutReader) span(i int) (uint64, bool) {
	if i >= len(g.parent.Content) {
		return 0, false
	}
	if g.disabled {
		return 0, false
	}
	entry := &g.lengths[i%2]
	if entry.populated && entry.position == i {
		return entry.length, entry.valid
	}
	n, ok := g.lookup(restic.BlobHandle{Type: restic.DataBlob, ID: g.parent.Content[i]})
	valid := ok && n > 0 && n <= uint(g.factory.MaxChunkSize())
	*entry = parentCutLength{position: i, length: uint64(n), populated: true, valid: valid}
	if !valid {
		g.disabled = true
	}
	return uint64(n), valid
}
func (g *parentCutReader) pairLengths() (int, int, bool) {
	if g.disabled {
		return 0, 0, false
	}
	for g.hintOffset < g.offset && g.hint < len(g.parent.Content) {
		n, ok := g.span(g.hint)
		if !ok {
			return 0, 0, false
		}
		// n is bounded by MaxChunkSize, and offset is the actual bytes consumed.
		g.hintOffset += n
		g.hint++
	}
	if g.hintOffset != g.offset || g.hint+2 >= len(g.parent.Content) {
		return 0, 0, false
	}
	a, ok := g.span(g.hint)
	if !ok {
		return 0, 0, false
	}
	b, ok := g.span(g.hint + 1)
	if !ok {
		return 0, 0, false
	}
	// Minimum-size spans have essentially no rolling work to parallelize.
	if a <= chunker.MinSize || b <= chunker.MinSize || a+b > 2*1024*1024 {
		return 0, 0, false
	}
	return int(a), int(b), true
}
func (g *parentCutReader) read(ctx context.Context, state *fileChunkState, rd io.Reader, ordinary restic.Chunker, assembly []byte) ([]byte, error) {
	result, err := g.readNext(ctx, state, rd, ordinary, assembly)
	if err == nil {
		g.offset += uint64(len(result))
	}
	return result, err
}
func (g *parentCutReader) readNext(ctx context.Context, state *fileChunkState, rd io.Reader, ordinary restic.Chunker, assembly []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(g.pending) > 0 {
		return g.pop(state, rd, ordinary)
	}
	if g.root != nil {
		assembly = g.root[:0]
		g.root = nil
	}
	a, b, ok := g.pairLengths()
	if !ok {
		if chunk, err, used := g.readLongPair(ctx, state, rd, ordinary, assembly); used {
			return chunk, err
		}
	}
	if !ok || a+b > cap(assembly) {
		chunk, err := state.readNextChunkOrdinary(rd, ordinary, assembly)
		if err != nil {
			g.root = assembly[:cap(assembly)]
		}
		return chunk, err
	}
	input := assembly[:a+b]
	// Keep ownership before reading: a backend/file error can return before
	// any new output replaces the caller's previous interior chunk.
	g.root = input[:cap(input)]
	// Existing read-ahead must be consumed before reading more file bytes.
	n, err := io.ReadFull(parentCutDrain{state, rd}, input)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, err
	}
	g.capacity = max(g.capacity, uint64(cap(input)))
	g.pending = input[:n]
	g.nextCut = 0
	g.pendingHashed = false
	if n < a+b {
		ordinary.Reset()
		return g.pop(state, rd, ordinary)
	}
	if g.extra == nil {
		g.extra = g.factory.NewChunker()
	}
	ordinary.Reset()
	g.extra.Reset()
	hashJob := g.hashes.try(input[:a], input[a:])
	var second int
	var wg sync.WaitGroup
	wg.Add(1)
	g.scheduled++
	go func() { defer wg.Done(); second = g.extra.NextSplitPoint(input[a:]) }()
	first := ordinary.NextSplitPoint(input[:a])
	wg.Wait()
	ids, hashed := hashJob.finish()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if first == a {
		state.chunkID, state.hashed = ids[0], hashed[0]
		g.accepted += uint64(a)
		if second == b {
			g.nextCut = b
			g.pendingID, g.pendingHashed = ids[1], hashed[1]
			g.accepted += uint64(b)
		} else {
			g.replayed += uint64(b)
		}
		g.pending = input[a:]
		ordinary.Reset()
		return input[:a], nil
	}
	g.replayed += uint64(a + b)
	ordinary.Reset()
	return g.pop(state, rd, ordinary)
}
func (g *parentCutReader) pop(state *fileChunkState, rd io.Reader, ordinary restic.Chunker) ([]byte, error) {
	if g.nextCut > 0 {
		state.chunkID, state.hashed = g.pendingID, g.pendingHashed
		g.pendingHashed = false
		output := g.pending
		g.pending = nil
		g.nextCut = 0
		return output, nil
	}
	n := ordinary.NextSplitPoint(g.pending)
	if n >= 0 {
		output := g.pending[:n]
		g.pending = g.pending[n:]
		return output, nil
	}
	// Failed predictions are replayed in-place. Shift only after every validator
	// has joined and the preceding output has been consumed by the saving path.
	prefix := g.root[:len(g.pending)]
	copy(prefix, g.pending)
	g.pending = nil
	g.root = nil
	chunk, err := state.readChunkRemainder(rd, ordinary, prefix)
	if err != nil {
		// Keep the allocation base even if replay appended beyond its capacity.
		// The caller does not publish a chunk on failure, but still returns its
		// assembly to the pool after reclaim.
		g.root = chunk[:cap(chunk)]
	}
	return chunk, err
}

type parentCutDrain struct {
	state *fileChunkState
	rd    io.Reader
}

func (r parentCutDrain) Read(p []byte) (int, error) {
	s := r.state
	if s.bpos < s.bmax {
		n := copy(p, s.readBuf[s.bpos:s.bmax])
		s.bpos += uint(n)
		return n, nil
	}
	return r.rd.Read(p)
}

// Continue the ordinary reader with an already-scanned prefix. The file
// worker's read buffer and chunker state remain private across this handoff.
func (s *fileChunkState) readChunkRemainder(rd io.Reader, c restic.Chunker, data []byte) ([]byte, error) {
	for {
		if s.bpos >= s.bmax {
			n, err := io.ReadFull(rd, s.readBuf)
			if err == io.ErrUnexpectedEOF {
				err = nil
			}
			if err == io.EOF && !s.closed {
				s.closed = true
				if len(data) > 0 {
					return data, nil
				}
			}
			if err != nil {
				return data, err
			}
			s.bpos = 0
			s.bmax = uint(n)
		}
		split := c.NextSplitPoint(s.readBuf[s.bpos:s.bmax])
		if split < 0 {
			data = append(data, s.readBuf[s.bpos:s.bmax]...)
			s.bpos = s.bmax
		} else {
			data = append(data, s.readBuf[s.bpos:s.bpos+uint(split)]...)
			s.bpos += uint(split)
			return data, nil
		}
	}
}

// Restore the pool's base slice on EOF, cancellation or a failed prediction.
// A returned interior chunk must never shorten the next file's assembly.
func (g *parentCutReader) reclaim(buf *buffer) {
	if g.root != nil {
		buf.Data = g.root
		g.root = nil
	}
	g.pending = nil
}
