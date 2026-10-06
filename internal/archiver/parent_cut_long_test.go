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

// Start at a certified current cut whose following pair exceeds the inherited
// admission bound. The third endpoint proves the pair is not a parent EOF tail.
func b18LongFixture(t *testing.T) ([]byte, *data.Node, map[restic.ID]uint) {
	t.Helper()
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	ends := b18Cuts(input, pol, 13001)
	start := 0
	for i := 0; i+2 < len(ends); i++ {
		a, b := ends[i]-start, ends[i+1]-ends[i]
		if a > chunker.MinSize && b > chunker.MinSize && a+b > 2*1024*1024 && a+b < chunker.MaxSize {
			input = input[start:]
			parent, sizes := b18Guidance(input, pol)
			return input, parent, sizes
		}
		start = ends[i]
	}
	t.Fatal("no long pair in reference input")
	return nil, nil, nil
}
func b18ReadLong(ctx context.Context, rd io.Reader, factory restic.ChunkerFactory, parent *data.Node, sizes map[restic.ID]uint) ([]int, []restic.ID, *b18Guided, error) {
	g := b18Probed(&parentCutReader{parent: parent, factory: factory, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }})
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: ctx, guided: g.parentCutReader}
	c := g.ordinary(factory.NewChunker())
	scratch := make([]byte, 0, chunker.MaxSize)
	var ends []int
	var ids []restic.ID
	offset := 0
	for {
		out, err := state.readNextChunk(rd, c, scratch)
		if err != nil {
			if err == io.EOF {
				err = nil
			}
			return ends, ids, g, err
		}
		offset += len(out)
		ends = append(ends, offset)
		ids = append(ids, restic.Hash(out))
		scratch = out
	}
}
func TestB18LongPairIdentity(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	original, parent, sizes := b18LongFixture(t)
	mutated := bytes.Clone(original)
	for i := 700000; i < 710000; i++ {
		mutated[i] ^= 77
	}
	inserted := append(bytes.Clone(original[:700000]), []byte("insertion at a parent interior")...)
	inserted = append(inserted, original[700000:]...)
	deleted := append(bytes.Clone(original[:700000]), original[710000:]...)
	grown := append(bytes.Clone(original), original[:900000]...)
	var other chunker.Pol
	for p := pol; p != 0; p >>= 1 {
		other = (other << 1) | (p & 1)
	}
	for _, tc := range []struct {
		name  string
		input []byte
		pol   chunker.Pol
	}{
		{"unchanged", original, pol}, {"mutated", mutated, pol}, {"inserted", inserted, pol}, {"deleted", deleted, pol},
		{"grown", grown, pol}, {"truncated-first", original[:900001], pol}, {"truncated-second", original[:sizes[parent.Content[0]]+100], pol},
		{"other-polynomial", original, other},
	} {
		t.Run(tc.name, func(t *testing.T) {
			want := b18Cuts(tc.input, tc.pol, 13001)
			for _, division := range []int{37, 524288, 4000000} {
				got, ids, g, err := b18ReadLong(t.Context(), &b18TrialShortReader{r: bytes.NewReader(tc.input), limit: division}, b18TrialFactory{tc.pol}, parent, sizes)
				if err != nil || !slices.Equal(got, want) || !slices.Equal(ids, b18ContentIDs(tc.input, want)) {
					t.Fatalf("division=%d err=%v endpoints=%v vs %v", division, err, got, want)
				}
				if tc.name == "unchanged" && (g.scheduled == 0 || g.accepted == 0) {
					t.Fatal("long route not exercised")
				}
				t.Logf("division=%d accepted=%d replay=%d scheduled=%d", division, g.accepted, g.replayed, g.scheduled)
			}
		})
	}
}
func TestB18LongPairRefusal(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input, parent, sizes := b18LongFixture(t)
	for _, capacity := range []int{2 * 1024 * 1024, int(sizes[parent.Content[0]]+sizes[parent.Content[1]]) - 1} {
		g := &parentCutReader{parent: parent, factory: b18TrialFactory{pol}, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}
		state := fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
		rd := bytes.NewReader(input)
		out, err, used := g.readLongPair(t.Context(), &state, rd, b18TrialFactory{pol}.NewChunker(), make([]byte, 0, capacity))
		if used || err != nil || out != nil || rd.Len() != len(input) || g.extra != nil || g.root != nil {
			t.Fatal("refused pair consumed or allocated state")
		}
	}
	for _, bad := range []uint{0, chunker.MaxSize + 1, ^uint(0)} {
		broken := make(map[restic.ID]uint)
		for id, n := range sizes {
			broken[id] = n
		}
		broken[parent.Content[0]] = bad
		got, ids, g, err := b18ReadLong(t.Context(), bytes.NewReader(input), b18TrialFactory{pol}, parent, broken)
		want := b18Cuts(input, pol, 13001)
		if err != nil || !slices.Equal(got, want) || !slices.Equal(ids, b18ContentIDs(input, want)) || g.scheduled != 0 {
			t.Fatal("invalid guidance accepted", err)
		}
	}
}
func TestB18LongPairFallback(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input, parent, sizes := b18LongFixture(t)
	for _, d := range []int{-1, 10} {
		broken := make(map[restic.ID]uint)
		for id, n := range sizes {
			broken[id] = n
		}
		broken[parent.Content[0]] = uint(int(broken[parent.Content[0]]) + d)
		broken[parent.Content[1]] = uint(int(broken[parent.Content[1]]) - d)
		got, ids, g, err := b18ReadLong(t.Context(), bytes.NewReader(input), b18TrialFactory{pol}, parent, broken)
		want := b18Cuts(input, pol, 13001)
		if err != nil || !slices.Equal(got, want) || !slices.Equal(ids, b18ContentIDs(input, want)) || g.replayed == 0 {
			t.Fatalf("long fallback mismatch d=%d err=%v scheduled=%d replay=%d got=%v want=%v", d, err, g.scheduled, g.replayed, got, want)
		}
	}
}
func TestB18LongPairFailureAndJoin(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input, parent, sizes := b18LongFixture(t)
	sentinel := errors.New("long input failure")
	for _, at := range []int{0, 700000, int(sizes[parent.Content[0]]) + 100} {
		_, _, g, err := b18ReadLong(t.Context(), &b18FailReader{r: bytes.NewReader(input), left: at, err: sentinel}, b18TrialFactory{pol}, parent, sizes)
		if !errors.Is(err, sentinel) || g.root == nil || cap(g.root) != chunker.MaxSize {
			t.Fatal("failed long read lost allocation base", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	joined := make(chan struct{})
	factory := b18CancelFactory{b18TrialFactory{pol}, cancel, joined}
	g := b18Probed(&parentCutReader{parent: parent, factory: factory, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }})
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: ctx, guided: g.parentCutReader}
	storage := make([]byte, 0, chunker.MaxSize)
	out, err := state.readNextChunk(bytes.NewReader(input), g.ordinary(b18TrialFactory{pol}.NewChunker()), storage)
	if out != nil || !errors.Is(err, context.Canceled) || g.scheduled != 1 {
		t.Fatal("long validator published after cancellation", err)
	}
	select {
	case <-joined:
	default:
		t.Fatal("validator not joined")
	}
	buf := &buffer{Data: storage}
	g.reclaim(buf)
	if &buf.Data[:cap(buf.Data)][0] != &storage[:cap(storage)][0] || g.root != nil || g.pending != nil {
		t.Fatal("cancellation failed to reclaim assembly")
	}
	clear(storage[:cap(storage)])
}

// A rejected long certificate may replay beyond the capacity it borrowed.
// Retain the replacement allocation even when input fails before publication.
// A nonnil prefix accompanying an error is ownership, not a publishable chunk.
func TestB18LongReplayGrowthError(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	ends := b18Cuts(input, pol, 13001)
	start := 0
	found := false
	for _, end := range ends {
		if end-start > 4*1024*1024 {
			input = input[start:]
			found = true
			break
		}
		start = end
	}
	if !found {
		t.Fatal("no naturally long current chunk")
	}
	parent := &data.Node{}
	sizes := make(map[restic.ID]uint)
	for i, n := range []uint{900000, 1900000, 1000000} {
		id := restic.Hash([]byte{byte(i)})
		parent.Content = append(parent.Content, id)
		sizes[id] = n
	}
	g := b18Probed(&parentCutReader{parent: parent, factory: b18TrialFactory{pol}, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }})
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: g.parentCutReader}
	initial := make([]byte, 0, 3*1024*1024)
	sentinel := errors.New("after long replay growth")
	rd := &b18FailReader{r: bytes.NewReader(input), left: 4 * 1024 * 1024, err: sentinel}
	out, err := state.readNextChunk(rd, g.ordinary(b18TrialFactory{pol}.NewChunker()), initial)
	if len(out) == 0 || !errors.Is(err, sentinel) || g.scheduled != 1 || g.replayed == 0 || cap(g.root) <= cap(initial) {
		t.Fatalf("err=%v scheduled=%d replay=%d cap=%d", err, g.scheduled, g.replayed, cap(g.root))
	}
	replacement := g.root
	buf := &buffer{Data: initial}
	g.reclaim(buf)
	if cap(buf.Data) != cap(replacement) || &buf.Data[0] != &replacement[0] || g.pending != nil || g.root != nil {
		t.Fatal("grown replay base not reclaimed")
	}
}
