package archiver

import (
	"bytes"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// Admission must work from ordinary growth, rather than depending on a test
// that gives every file MaxSize storage up front. Check every emitted endpoint
// and ID while preserving the saver contract (only the last output is returned).
func TestB18LongPairNaturallyGrownAssembly(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input := b06Payload(32*1024*1024+173, 881)
	parent, sizes := b18Guidance(input, pol)
	factory := b18TrialFactory{pol}
	g := b18Probed(&parentCutReader{parent: parent, factory: factory, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }})
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: g.parentCutReader}
	c := g.ordinary(factory.NewChunker())
	assembly := make([]byte, 0, 2<<20)
	rd := bytes.NewReader(input)
	var ends []int
	var ids []restic.ID
	offset, longPairs := 0, 0
	for {
		scheduled := g.scheduled
		out, err := state.readNextChunk(rd, c, assembly)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if g.scheduled > scheduled && len(out)+len(g.pending) > 2<<20 {
			longPairs++
			if cap(g.root) < len(out)+len(g.pending) {
				t.Fatal("pair exceeded borrowed allocation")
			}
		}
		offset += len(out)
		ends = append(ends, offset)
		ids = append(ids, restic.Hash(out))
		assembly = out
	}
	want := b18Cuts(input, pol, 13001)
	if !slices.Equal(ends, want) || !slices.Equal(ids, b18ContentIDs(input, want)) || longPairs == 0 {
		t.Fatalf("natural-growth identity/coverage: longPairs=%d endpoints=%v want=%v", longPairs, ends, want)
	}
	t.Logf("naturally grown long pairs=%d", longPairs)
}

// Alternate the two dispatch routes, consume existing read-ahead, then fail a
// new pair after an interior output. Reclaim must return the allocation base.
// This scripted chunker isolates ownership; real endpoint equality is above.
func TestB18LongPairRouteTransitionReadError(t *testing.T) {
	lengths := []int{600 << 10, 600 << 10, 1500 << 10, 1500 << 10, 600 << 10, 600 << 10, 1500 << 10, 1500 << 10, 1}
	parent := &data.Node{}
	sizes := make(map[restic.ID]uint)
	var input []byte
	for i, n := range lengths {
		id := restic.Hash([]byte{byte(i)})
		parent.Content = append(parent.Content, id)
		sizes[id] = uint(n)
		if i < 6 {
			input = append(input, bytes.Repeat([]byte{byte(i + 1)}, n)...)
		}
	}
	g := b18Probed(&parentCutReader{parent: parent, factory: b18ReclaimFactory{}, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }})
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: g.parentCutReader, bmax: 111}
	copy(state.readBuf, input[:111])
	sentinel := errors.New("next long pair failed")
	rd := &b18FailReader{r: bytes.NewReader(input[111:]), left: len(input) - 111, err: sentinel}
	storage := make([]byte, 0, 4<<20)
	assembly := storage
	c := g.newOrdinary()
	for i := 0; i < 6; i++ {
		out, err := state.readNextChunk(rd, c, assembly)
		if err != nil || len(out) != lengths[i] || !bytes.Equal(out, bytes.Repeat([]byte{byte(i + 1)}, lengths[i])) {
			t.Fatalf("span %d len=%d err=%v", i, len(out), err)
		}
		if i%2 == 0 && &out[0] != &storage[:cap(storage)][0] {
			t.Fatal("pair did not restore base")
		}
		assembly = out
	}
	if g.scheduled != 3 {
		t.Fatalf("scheduled=%d want=3", g.scheduled)
	}
	out, err := state.readNextChunk(rd, c, assembly)
	if out != nil || !errors.Is(err, sentinel) {
		t.Fatalf("out=%d err=%v", len(out), err)
	}
	buf := &buffer{Data: assembly}
	g.reclaim(buf)
	if cap(buf.Data) != cap(storage) || &buf.Data[0] != &storage[:cap(storage)][0] || g.root != nil || g.pending != nil {
		t.Fatal("transition error lost allocation base")
	}
}
