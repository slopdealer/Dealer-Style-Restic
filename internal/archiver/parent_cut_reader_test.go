package archiver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

type b18TrialFactory struct{ pol chunker.Pol }

func (f b18TrialFactory) NewChunker() restic.Chunker {
	return &b18TrialChunker{chunker.NewBase(f.pol), f.pol}
}
func (f b18TrialFactory) MaxChunkSize() int    { return chunker.MaxSize }
func (f b18TrialFactory) ZeroChunk() restic.ID { return restic.ID{} }
func b18Guidance(payload []byte, pol chunker.Pol) (*data.Node, map[restic.ID]uint) {
	parent := &data.Node{Size: uint64(len(payload))}
	sizes := make(map[restic.ID]uint)
	start := 0
	for _, end := range b18Cuts(payload, pol, 13001) {
		id := restic.Hash(payload[start:end])
		parent.Content = append(parent.Content, id)
		sizes[id] = uint(end - start)
		start = end
	}
	return parent, sizes
}
func b18ReadGuided(ctx context.Context, rd io.Reader, factory b18TrialFactory, parent *data.Node, sizes map[restic.ID]uint) ([]int, []restic.ID, *parentCutReader, error) {
	g := &parentCutReader{parent: parent, factory: factory, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: ctx, guided: g}
	c := factory.NewChunker()
	scratch := make([]byte, 0, 2*1024*1024)
	offset := 0
	var ends []int
	var ids []restic.ID
	for {
		chunk, err := state.readNextChunk(rd, c, scratch)
		if err == io.EOF {
			return ends, ids, g, nil
		}
		if err != nil {
			return ends, ids, g, err
		}
		offset += len(chunk)
		ends = append(ends, offset)
		ids = append(ids, restic.Hash(chunk))
		scratch = chunk
	}
}
func TestB18GuidedReader(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	original := b06Payload(32*1024*1024+173, 881)
	parent, sizes := b18Guidance(original, pol)
	mutation := bytes.Clone(original)
	for i := 700000; i < 710000; i++ {
		mutation[i] ^= 77
	}
	var other chunker.Pol
	for p := pol; p != 0; p >>= 1 {
		other = (other << 1) | (p & 1)
	}
	for _, tc := range []struct {
		name  string
		input []byte
		pol   chunker.Pol
	}{
		{"same", original, pol}, {"mutated", mutation, pol}, {"grown", append(bytes.Clone(original), original[:900000]...), pol}, {"truncated", original[:900001], pol},
		{"different-polynomial", original, other}, {"empty", nil, pol}, {"EOF-exact-cut", original[:b18Cuts(original, pol, 13001)[0]], pol},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, division := range []int{37, 524288, 4000000} {
				got, ids, g, err := b18ReadGuided(t.Context(), &b18TrialShortReader{r: bytes.NewReader(tc.input), limit: division}, b18TrialFactory{tc.pol}, parent, sizes)
				want := b18Cuts(tc.input, tc.pol, 13001)
				if len(tc.input) == 0 {
					want = nil
				}
				if err != nil || !slices.Equal(got, want) || !slices.Equal(ids, b18ContentIDs(tc.input, want)) {
					t.Fatalf("division=%d got=%v want=%v err=%v", division, got, want, err)
				}
				t.Logf("division=%d bytes=%d accepted=%d replayed=%d scheduled=%d assembly_capacity=%d", division, len(tc.input), g.accepted, g.replayed, g.scheduled, g.capacity)
			}
		})
	}
}
func TestB18GuidedReaderInvalidSizes(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(4*1024*1024+173, 881)
	parent, sizes := b18Guidance(input, pol)
	for _, bad := range []uint{0, ^uint(0), 9 * 1024 * 1024} {
		t.Run(string(rune(bad)), func(t *testing.T) {
			broken := make(map[restic.ID]uint)
			for id, n := range sizes {
				broken[id] = n
			}
			broken[parent.Content[0]] = bad
			got, _, g, err := b18ReadGuided(t.Context(), bytes.NewReader(input), b18TrialFactory{pol}, parent, broken)
			if err != nil || !slices.Equal(got, b18Cuts(input, pol, 13001)) || g.scheduled != 0 {
				t.Fatal(got, err, g.scheduled)
			}
		})
	}
	got, _, g, err := b18ReadGuided(t.Context(), bytes.NewReader(input), b18TrialFactory{pol}, parent, nil)
	if err != nil || !slices.Equal(got, b18Cuts(input, pol, 13001)) || g.scheduled != 0 {
		t.Fatal(err)
	}
}

// Cancellation and read failure must return before any assembly reuse, and
// without publishing a speculative second chunk.
func TestB18GuidedReaderFailures(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	parent, sizes := b18Guidance(input, pol)
	sentinel := errors.New("injected read failure")
	for _, at := range []int{0, 500000, 1200000, 3000000} {
		_, _, _, err := b18ReadGuided(t.Context(), &b18FailReader{r: bytes.NewReader(input), left: at, err: sentinel}, b18TrialFactory{pol}, parent, sizes)
		if !errors.Is(err, sentinel) {
			t.Fatalf("at=%d err=%v", at, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, _, err := b18ReadGuided(ctx, bytes.NewReader(input), b18TrialFactory{pol}, parent, sizes)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(t.Context())
	_, _, _, err = b18ReadGuided(ctx, &b18CancelReader{r: bytes.NewReader(input), cancel: cancel}, b18TrialFactory{pol}, parent, sizes)
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

type b18FailReader struct {
	r    io.Reader
	left int
	err  error
}

func (r *b18FailReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, r.err
	}
	n, err := r.r.Read(p[:min(len(p), r.left)])
	r.left -= n
	return n, err
}

type b18CancelReader struct {
	r      io.Reader
	cancel context.CancelFunc
}

func (r *b18CancelReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	r.cancel()
	return n, err
}

func TestB18GuidedReaderFallbackBranches(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	ends := b18Cuts(input, pol, 13001)
	a, b := ends[0], ends[1]-ends[0]
	for _, tc := range []struct {
		name string
		a, b int
	}{
		{"earlier-first-cut", a + 10, b - 10}, {"no-first-cut", a - 100000, b + 100000},
		{"earlier-second-cut", a, b + 10}, {"no-second-cut", a, b - 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := &data.Node{}
			sizes := make(map[restic.ID]uint)
			for i, n := range []int{tc.a, tc.b, len(input) - tc.a - tc.b} {
				id := restic.Hash([]byte{byte(i)})
				parent.Content = append(parent.Content, id)
				sizes[id] = uint(n)
			}
			got, ids, g, err := b18ReadGuided(t.Context(), bytes.NewReader(input), b18TrialFactory{pol}, parent, sizes)
			if err != nil || !slices.Equal(got, ends) || !slices.Equal(ids, b18ContentIDs(input, ends)) || g.scheduled == 0 || g.replayed == 0 {
				t.Fatalf("err=%v cuts=%v accepted=%d replayed=%d scheduled=%d", err, got, g.accepted, g.replayed, g.scheduled)
			}
			if (tc.a == a) != (g.accepted > 0) {
				t.Fatalf("wrong prefix acceptance: %d", g.accepted)
			}
			t.Logf("accepted=%d replayed=%d scheduled=%d", g.accepted, g.replayed, g.scheduled)
		})
	}
}

func TestB18GuidedSaverHashOwnership(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	original := b06Payload(32*1024*1024+173, 881)
	parent, sizes := b18Guidance(original, pol)
	input := bytes.Clone(original)
	first := b18Cuts(original, pol, 13001)[0]
	input[first+100] ^= 7
	factory := b18TrialFactory{pol}
	u := &b18HashUploader{known: make(map[restic.ID]int)}
	for id, n := range sizes {
		u.known[id] = int(n)
	}
	s := newFileSaver(t.Context(), nil, u, factory, 0)
	b18EnableHash(s)
	s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile, Size: uint64(len(input))}, nil
	}
	g := &parentCutReader{parent: parent, factory: factory, lookup: u.LookupStoredBlobSize}
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: g}
	file := &b06SaverFile{r: bytes.NewReader(input)}
	var result futureNodeResult
	completed, reading := 0, 0
	s.saveFile(t.Context(), factory.NewChunker(), state, "/guided", "guided", file, func() {}, func() { reading++ }, func(r futureNodeResult) { result = r; completed++ })
	if g.accepted == 0 || reading != 1 || completed != 0 || !file.closed {
		t.Fatalf("guided save not exercised: accepted=%d reading=%d completed=%d", g.accepted, reading, completed)
	}
	// The complete file has overwritten assembly before unknown uploads finish.
	// Neither the borrowed pending suffix nor the hash slot can have been reused.
	unknown := 0
	for i := len(u.pending) - 1; i >= 0; i-- {
		p := u.pending[i]
		if !p.known {
			unknown++
			if p.id != restic.Hash(p.data) {
				t.Fatal("assembly reuse changed submitted payload")
			}
		}
		p.cb(p.id, p.known, p.size, nil)
	}
	ends := b18Cuts(input, pol, 13001)
	if unknown == 0 || completed != 1 || result.err != nil || !slices.Equal(result.node.Content, b18ContentIDs(input, ends)) || result.node.Size != uint64(len(input)) || len(s.hashBuffers) != 1 {
		t.Fatalf("settlement changed: unknown=%d completed=%d result=%+v", unknown, completed, result)
	}
}

func TestB18GuidanceCursorCache(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	parent, sizes := b18Guidance(input, pol)
	counts := make(map[restic.ID]int)
	g := &parentCutReader{parent: parent, factory: b18TrialFactory{pol}, lookup: func(h restic.BlobHandle) (uint, bool) { counts[h.ID]++; n, ok := sizes[h.ID]; return n, ok }}
	for i := range parent.Content {
		g.offset = g.hintOffset
		_, _, _ = g.pairLengths()
		_, _, _ = g.pairLengths()
		n, ok := g.span(i)
		if !ok {
			t.Fatal("unexpected invalid span")
		}
		g.offset += n
		_, _, _ = g.pairLengths()
	}
	for id, n := range counts {
		if n != 1 {
			t.Fatalf("%v queried %d times", id, n)
		}
	}
	counts = make(map[restic.ID]int)
	g = &parentCutReader{parent: parent, factory: b18TrialFactory{pol}, lookup: func(h restic.BlobHandle) (uint, bool) { counts[h.ID]++; return 0, false }}
	for i := 0; i < 20; i++ {
		g.offset = uint64(i)
		_, _, _ = g.pairLengths()
	}
	if counts[parent.Content[0]] != 1 || len(counts) != 1 || !g.disabled {
		t.Fatal("missing guidance repeatedly queried", counts)
	}
}

func TestB18GuidedReaderRealignment(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	parent, sizes := b18Guidance(input, pol)
	// Incorrect interior guidance still ends the pair at a real current cut.
	// Ordinary replay can recover there and validate useful later pairs.
	sizes[parent.Content[0]] += 10
	sizes[parent.Content[1]] -= 10
	got, ids, g, err := b18ReadGuided(t.Context(), bytes.NewReader(input), b18TrialFactory{pol}, parent, sizes)
	want := b18Cuts(input, pol, 13001)
	if err != nil || g.replayed == 0 || g.accepted == 0 || g.scheduled < 2 || !slices.Equal(got, want) || !slices.Equal(ids, b18ContentIDs(input, want)) {
		t.Fatalf("err=%v accepted=%d replayed=%d scheduled=%d", err, g.accepted, g.replayed, g.scheduled)
	}
}

type b18CancelFactory struct {
	b18TrialFactory
	cancel context.CancelFunc
	joined chan struct{}
}

func (f b18CancelFactory) NewChunker() restic.Chunker {
	return &b18CancelChunker{Chunker: f.b18TrialFactory.NewChunker(), cancel: f.cancel, joined: f.joined}
}

type b18CancelChunker struct {
	restic.Chunker
	cancel context.CancelFunc
	joined chan struct{}
}

func (c *b18CancelChunker) NextSplitPoint(p []byte) int {
	n := c.Chunker.NextSplitPoint(p)
	c.cancel()
	close(c.joined)
	return n
}
func TestB18GuidedCancellationDuringValidator(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	parent, sizes := b18Guidance(input, pol)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	joined := make(chan struct{})
	factory := b18CancelFactory{b18TrialFactory{pol}, cancel, joined}
	g := &parentCutReader{parent: parent, factory: factory, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: ctx, guided: g}
	output, err := state.readNextChunk(bytes.NewReader(input), b18TrialFactory{pol}.NewChunker(), make([]byte, 0, 2*1024*1024))
	if !errors.Is(err, context.Canceled) || output != nil || g.scheduled != 1 {
		t.Fatal("validator cancellation did not prevent emission", err, g.scheduled)
	}
	select {
	case <-joined:
	default:
		t.Fatal("validator did not join before returning")
	}
	clear(input) // every validator has relinquished its borrowed bytes
}

func TestB18GuidedEOFReturnsAssemblyBase(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	original := b06Payload(32*1024*1024+173, 881)
	ends := b18Cuts(original, pol, 13001)
	parent, sizes := b18Guidance(original, pol)
	input := original[:ends[1]]
	factory := b18TrialFactory{pol}
	u := &b18HashUploader{known: make(map[restic.ID]int)}
	for id, n := range sizes {
		u.known[id] = int(n)
	}
	s := newFileSaver(t.Context(), nil, u, factory, 0)
	b18EnableHash(s)
	s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile, Size: uint64(len(input))}, nil
	}
	g := &parentCutReader{parent: parent, factory: factory, lookup: u.LookupStoredBlobSize}
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: g}
	var result futureNodeResult
	completed := 0
	s.saveFile(t.Context(), factory.NewChunker(), state, "/cut", "cut", &b06SaverFile{r: bytes.NewReader(input)}, func() {}, func() {}, func(r futureNodeResult) { result = r; completed++ })
	for _, p := range u.pending {
		p.cb(p.id, p.known, p.size, nil)
	}
	if g.accepted != uint64(len(input)) || g.root != nil || g.pending != nil || completed != 1 || result.err != nil {
		t.Fatalf("bad settlement: accepted=%d complete=%d err=%v", g.accepted, completed, result.err)
	}
	buf := s.saveFilePool.Get()
	defer buf.Release()
	if cap(buf.Data) != s.saveFilePool.defaultSize {
		t.Fatalf("interior slice shortened pooled assembly: capacity %d vs %d", cap(buf.Data), s.saveFilePool.defaultSize)
	}
}
