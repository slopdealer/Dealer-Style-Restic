package archiver

import (
	"bytes"
	"errors"
	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
	"testing"
)

// An ordinary cut inside replay leaves an interior output in the saving buffer.
// A later input failure must return the original assembly, including any growth,
// rather than admitting that interior slice to the next file's buffer pool.
func TestParentCutReplayErrorReclaimsAssembly(t *testing.T) {
	for _, grow := range []bool{false, true} {
		t.Run(map[bool]string{false: "existing", true: "grown"}[grow], func(t *testing.T) {
			pool := newBufferPool(64)
			buf := pool.Get()
			root := buf.Data[:cap(buf.Data)]
			copy(root, bytes.Repeat([]byte{7}, 64))
			g := &parentCutReader{root: root, pending: root[16:48]}
			state := &fileChunkState{readBuf: make([]byte, 64)}
			c := &reclaimScriptChunker{split: 8}
			out, err := g.pop(state, nil, c)
			if err != nil || len(out) != 8 || &out[0] != &root[16] {
				t.Fatal("missing interior replay output", err)
			}
			buf.Data = out
			failure := errors.New("read failure after replay cut")
			var input []byte
			if grow {
				input = bytes.Repeat([]byte{9}, 64)
			}
			c.split = -1
			_, err = g.pop(state, &reclaimErrorReader{input: input, err: failure}, c)
			if !errors.Is(err, failure) {
				t.Fatal("input error changed", err)
			}
			g.reclaim(buf)
			if cap(buf.Data) < 64 || (!grow && &buf.Data[:cap(buf.Data)][0] != &root[0]) {
				t.Fatal("lost assembly base", cap(buf.Data))
			}
			if grow && cap(buf.Data) <= 64 {
				t.Fatal("lost grown replay allocation")
			}
			buf.Release()
			next := pool.Get()
			defer next.Release()
			if cap(next.Data) != 64 {
				t.Fatal("interior assembly admitted to pool", cap(next.Data))
			}
		})
	}
}

type reclaimScriptChunker struct{ split int }

func (*reclaimScriptChunker) Reset()                      {}
func (c *reclaimScriptChunker) NextSplitPoint([]byte) int { return c.split }

type reclaimErrorReader struct {
	input []byte
	err   error
}

func (r *reclaimErrorReader) Read(p []byte) (int, error) {
	if len(r.input) == 0 {
		return 0, r.err
	}
	n := copy(p, r.input)
	r.input = r.input[n:]
	return n, nil
}

func TestParentCutPairReadErrorReclaimsAssembly(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	pool := newBufferPool(2 << 20)
	buf := pool.Get()
	root := buf.Data
	// Model the caller's last output as an accepted interior span. The next
	// pair transfers the full root back to reading before input can fail.
	buf.Data = root[768<<10 : 1536<<10]
	parent := &data.Node{Content: []restic.ID{{1}, {2}, {3}}}
	g := &parentCutReader{root: root, parent: parent, factory: b18TrialFactory{pol}, lookup: func(restic.BlobHandle) (uint, bool) { return 768 << 10, true }}
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
	failure := errors.New("next pair input failed")
	_, err := g.read(t.Context(), state, &reclaimErrorReader{err: failure}, g.factory.NewChunker(), buf.Data)
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	g.reclaim(buf)
	if cap(buf.Data) != cap(root) || &buf.Data[:cap(buf.Data)][0] != &root[0] {
		t.Fatal("pair read error lost original assembly", cap(buf.Data))
	}
	buf.Release()
	next := pool.Get()
	defer next.Release()
	if cap(next.Data) != pool.defaultSize {
		t.Fatal("interior buffer admitted", cap(next.Data))
	}
}
