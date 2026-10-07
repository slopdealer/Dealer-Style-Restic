package archiver

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// A second certified ID belongs to the reader, not the shared worker slot.
// Reusing that slot in another file must not replace the pending ID or leave
// state.hashed set when the next operation falls back or is cancelled.
func TestB01PairHashPendingIdentitySurvivesLaneReuse(t *testing.T) {
	const size = 1536 << 10
	factory := bufferTestFactory{size: size}
	lane := &parentPairHashLane{active: make(chan struct{}, 1)}
	input := append(bytes.Repeat([]byte{1}, size), bytes.Repeat([]byte{2}, size)...)
	input = append(input, bytes.Repeat([]byte{3}, size)...)
	other := bytes.Clone(input)
	for i := range other {
		other[i] += 8
	}
	parent := &data.Node{}
	sizes := make(map[restic.ID]uint)
	for i := range 3 {
		id := restic.Hash(input[i*size : (i+1)*size])
		parent.Content = append(parent.Content, id)
		sizes[id] = size
	}
	newState := func(ctx context.Context) *fileChunkState {
		g := &parentCutReader{parent: parent, factory: factory, hashes: lane, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}
		return &fileChunkState{ctx: ctx, readBuf: make([]byte, chunkReadBufSize), guided: g}
	}
	a := newState(t.Context())
	bCtx, cancel := context.WithCancel(t.Context())
	defer cancel()
	b := newState(bCtx)
	aReader, bReader := bytes.NewReader(input), bytes.NewReader(other)
	aChunker, bChunker := factory.NewChunker(), factory.NewChunker()
	aStorage, bStorage := make([]byte, 0, 4<<20), make([]byte, 0, 4<<20)
	aFirst, err := a.readNextChunk(aReader, aChunker, aStorage)
	if err != nil || !a.hashed || !a.guided.pendingHashed || a.chunkID != parent.Content[0] {
		t.Fatalf("first reader: hashed=%v err=%v", a.hashed, err)
	}
	// Saving has consumed the prefix; destruction must not affect its suffix.
	clear(aFirst)
	bFirst, err := b.readNextChunk(bReader, bChunker, bStorage)
	if err != nil || !b.hashed || !b.guided.pendingHashed || b.chunkID != restic.Hash(other[:size]) {
		t.Fatalf("second reader: hashed=%v err=%v", b.hashed, err)
	}
	if len(lane.active) != 0 {
		t.Fatal("joined jobs retained the lane")
	}
	aSecond, err := a.readNextChunk(aReader, aChunker, aFirst)
	if err != nil || !a.hashed || a.chunkID != parent.Content[1] || !bytes.Equal(aSecond, input[size:2*size]) {
		t.Fatalf("pending identity overwritten: hashed=%v err=%v", a.hashed, err)
	}
	aThird, err := a.readNextChunk(aReader, aChunker, aSecond)
	if err != nil || a.hashed || !bytes.Equal(aThird, input[2*size:]) {
		t.Fatalf("ordinary fallback retained a speculative ID: hashed=%v err=%v", a.hashed, err)
	}
	cancel()
	out, err := b.readNextChunk(bReader, bChunker, bFirst)
	if out != nil || !errors.Is(err, context.Canceled) || b.hashed {
		t.Fatalf("cancelled reader published pending ID: hashed=%v err=%v", b.hashed, err)
	}
	buf := &buffer{Data: bFirst}
	b.guided.reclaim(buf)
	if b.guided.pending != nil || &buf.Data[:cap(buf.Data)][0] != &bStorage[:cap(bStorage)][0] {
		t.Fatal("cancellation did not reclaim the allocation base")
	}
	job := lane.try(input[:size], input[size:2*size])
	if job == nil {
		t.Fatal("cancellation stranded shared admission")
	}
	job.finish()
}
