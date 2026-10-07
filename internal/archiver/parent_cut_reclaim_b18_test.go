package archiver

import (
	"bytes"
	"errors"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// A accepted first span returns an interior suffix on the next read. If that
// suffix has no cut and extending it fails, cleanup must return the allocation
// base, not the previous output's shorter capacity, to the pool.
func TestB18FallbackReadErrorReclaimsAssembly(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	ends := b18Cuts(input, pol, 13001)
	a, b := ends[0], ends[1]-ends[0]+10
	if a <= chunker.MinSize || b <= chunker.MinSize || a+b > 2<<20 {
		t.Fatal("fixture does not admit a pair")
	}
	parent := &data.Node{}
	sizes := make(map[restic.ID]uint)
	for i, n := range []int{a, b, 1} {
		id := restic.Hash([]byte{byte(i)})
		parent.Content = append(parent.Content, id)
		sizes[id] = uint(n)
	}
	factory := b18TrialFactory{pol}
	g := &parentCutReader{parent: parent, factory: factory, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: g}
	c := factory.NewChunker()
	pool := newBufferPool(2 << 20)
	buf := pool.Get()
	base := &buf.Data[0]
	sentinel := errors.New("read failure after paired prefix")
	rd := &b18FailReader{r: bytes.NewReader(input), left: a + b, err: sentinel}
	first, err := state.readNextChunk(rd, c, buf.Data)
	if err != nil || len(first) != a || g.accepted != uint64(a) {
		t.Fatalf("first len=%d accepted=%d err=%v", len(first), g.accepted, err)
	}
	buf.Data = first
	interior, err := state.readNextChunk(rd, c, buf.Data)
	if err != nil || len(interior) != b-10 || cap(interior) >= pool.defaultSize {
		t.Fatalf("no interior fallback cut: len=%d cap=%d err=%v", len(interior), cap(interior), err)
	}
	buf.Data = interior
	second, err := state.readNextChunk(rd, c, buf.Data)
	if !errors.Is(err, sentinel) {
		t.Fatalf("fallback returned len=%d err=%v", len(second), err)
	}
	g.reclaim(buf)
	if cap(buf.Data) != pool.defaultSize || &buf.Data[:cap(buf.Data)][0] != base || g.pending != nil || g.root != nil {
		t.Fatalf("lost base: capacity=%d want=%d", cap(buf.Data), pool.defaultSize)
	}
	buf.Release()
	recycled := pool.Get()
	defer recycled.Release()
	if recycled != buf || cap(recycled.Data) != pool.defaultSize {
		t.Fatal("assembly was not recycled intact")
	}
}

// This scripted chunker isolates slice ownership from Rabin cut probabilities.
// The real-cut test above separately checks this path with the pinned chunker.
type b18ReclaimFactory struct{}

func (b18ReclaimFactory) MaxChunkSize() int          { return 8 << 20 }
func (b18ReclaimFactory) ZeroChunk() restic.ID       { return restic.ID{} }
func (b18ReclaimFactory) NewChunker() restic.Chunker { return b18ReclaimChunker{} }

type b18ReclaimChunker struct{}

func (b18ReclaimChunker) Reset() {}
func (b18ReclaimChunker) NextSplitPoint(p []byte) int {
	if len(p) == 0 {
		return -1
	}
	return len(p)
}

func TestB18NextPairReadErrorReclaimsAssembly(t *testing.T) {
	const span = 600 << 10
	parent := &data.Node{}
	for i := 0; i < 5; i++ {
		parent.Content = append(parent.Content, restic.Hash([]byte{byte(i)}))
	}
	g := &parentCutReader{parent: parent, factory: b18ReclaimFactory{}, lookup: func(restic.BlobHandle) (uint, bool) { return span, true }}
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: g}
	pool := newBufferPool(2 << 20)
	buf := pool.Get()
	base := &buf.Data[0]
	failure := errors.New("second pair read interrupted")
	rd := &b18FailReader{r: bytes.NewReader(make([]byte, 2*span)), left: 2 * span, err: failure}
	c := g.factory.NewChunker()
	for i := 0; i < 2; i++ {
		out, err := state.readNextChunk(rd, c, buf.Data)
		if err != nil || len(out) != span {
			t.Fatalf("span %d: len=%d err=%v", i, len(out), err)
		}
		buf.Data = out
	}
	if cap(buf.Data) >= pool.defaultSize {
		t.Fatal("fixture did not return interior output")
	}
	_, err := state.readNextChunk(rd, c, buf.Data)
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	g.reclaim(buf)
	if cap(buf.Data) != pool.defaultSize || &buf.Data[:cap(buf.Data)][0] != base {
		t.Fatalf("second pair lost base: %d", cap(buf.Data))
	}
	buf.Release()
	recycled := pool.Get()
	defer recycled.Release()
	if recycled != buf {
		t.Fatal("second pair error did not recycle assembly")
	}
}
