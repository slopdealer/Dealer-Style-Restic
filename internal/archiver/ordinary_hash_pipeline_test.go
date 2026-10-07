package archiver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

type ordinaryBlockedHash struct {
	hash.Hash
	entered chan struct{}
	release chan struct{}
	stable  chan bool
	first   bool
}

func (h *ordinaryBlockedHash) Write(b []byte) (int, error) {
	if !h.first {
		h.first = true
		before := bytes.Clone(b)
		close(h.entered)
		<-h.release
		h.stable <- bytes.Equal(before, b)
	}
	return h.Hash.Write(b)
}

func TestOrdinaryHashPipelineBlockedBorrower(t *testing.T) {
	lane := &parentPairHashLane{active: make(chan struct{}, 1)}
	lane.active <- struct{}{}
	h := &ordinaryBlockedHash{Hash: sha256.New(), entered: make(chan struct{}), release: make(chan struct{}), stable: make(chan bool, 1)}
	j := newOrdinaryHashStream(lane, h)
	data := make([]byte, 100, 200)
	for i := range data {
		data[i] = byte(i)
	}
	j.write(data)
	<-h.entered
	// Next-tile assembly may advance beyond the borrowed prefix. Passing the
	// next span, growing storage, and returning the chunk must first join it.
	data = append(data, bytes.Repeat([]byte{71}, 50)...)
	done := make(chan restic.ID, 1)
	go func() { j.write(data); done <- j.finish() }()
	select {
	case <-done:
		t.Fatal("settled with a live borrower")
	case <-time.After(10 * time.Millisecond):
	}
	close(h.release)
	select {
	case id := <-done:
		if id != restic.Hash(data) || !<-h.stable || len(lane.active) != 0 {
			t.Fatal("borrowed storage or settlement differs")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("borrower did not join")
	}
	clear(data[:cap(data)])
}

func TestOrdinaryHashPipelineStockBoundaries(t *testing.T) {
	const pol = chunker.Pol(0x3da3358b4dc173)
	random := make([]byte, 25*1024*1024+37)
	x := uint64(37)
	for i := range random {
		x ^= x << 13
		x ^= x >> 7
		x ^= x << 17
		random[i] = byte(x)
	}
	for _, payload := range [][]byte{nil, random[:17], random[:chunkReadBufSize], random[:3*1024*1024+31], random, make([]byte, len(random))} {
		for _, reserve := range []int{1024, 2 * 1024 * 1024, chunker.MaxSize} {
			lane := &parentPairHashLane{active: make(chan struct{}, 1)}
			s := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ordinaryHashes: lane}
			c := b18TrialFactory{pol}.NewChunker()
			r := &b23ExactTerminalReader{data: payload, err: io.EOF}
			buf := make([]byte, 0, reserve)
			var ends []int
			at := 0
			for {
				b, err := s.readNextChunk(r, c, buf)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(b, payload[at:at+len(b)]) {
					t.Fatal("assembly differs")
				}
				if s.hashed && s.chunkID != restic.Hash(b) {
					t.Fatal("incremental identity differs")
				}
				if len(lane.active) != 0 {
					t.Fatal("lane retained through submission")
				}
				at += len(b)
				ends = append(ends, at)
				buf = b[:0]
			}
			want := b18Cuts(payload, pol, 65537)
			// The dependency helper reports a zero-sized terminal cut for an
			// empty input. The ordinary file reader emits no blob for that case.
			if len(payload) == 0 {
				want = nil
			}
			if !slices.Equal(ends, want) {
				t.Fatalf("stock endpoints differ input=%d reserve=%d got=%v want=%v", len(payload), reserve, ends, want)
			}
		}
	}
}

func TestOrdinaryHashPipelineAdmissionAndReuse(t *testing.T) {
	const size = 3*1024*1024 + 17
	input := bytes.Repeat([]byte{71}, size*2+31)
	lane := &parentPairHashLane{active: make(chan struct{}, 1)}
	for _, busy := range []bool{true, false} {
		if busy {
			lane.active <- struct{}{}
		}
		s := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ordinaryHashes: lane}
		c := bufferTestFactory{size}.NewChunker()
		r := bufferTestShortReader{bytes.NewReader(input)}
		buf := make([]byte, 0, 1024)
		at, hashed := 0, 0
		for {
			b, err := s.readNextChunk(r, c, buf)
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if s.hashed {
				hashed++
				if s.chunkID != restic.Hash(b) {
					t.Fatal("wrong hash after growth or reuse")
				}
			}
			at += len(b)
			buf = b[:0]
		}
		if at != len(input) || (busy && hashed != 0) || (!busy && hashed != 2) {
			t.Fatalf("busy=%v at=%d hashed=%d", busy, at, hashed)
		}
		if busy {
			<-lane.active
		}
		if len(lane.active) != 0 {
			t.Fatal("admission leaked")
		}
	}
}

func TestOrdinaryHashPipelineTerminalErrorJoins(t *testing.T) {
	fault := errors.New("terminal read failure")
	for _, n := range []int{17, 3*1024*1024 + 17, 6 * 1024 * 1024} {
		lane := &parentPairHashLane{active: make(chan struct{}, 1)}
		s := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ordinaryHashes: lane}
		r := &b23ExactTerminalReader{data: bytes.Repeat([]byte{31}, n), err: fault}
		b, err := s.readNextChunk(r, bufferTestFactory{8 * 1024 * 1024}.NewChunker(), make([]byte, 0, 1024))
		if b != nil || !errors.Is(err, fault) || s.hashed || len(lane.active) != 0 {
			t.Fatalf("error settlement %d: len=%d err=%v hashed=%v", n, len(b), err, s.hashed)
		}
	}
}

func TestOrdinaryHashPipelineStalledSaveCancellation(t *testing.T) {
	const size = 3*1024*1024 + 17
	input := bytes.Repeat([]byte{71}, size+31)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	u := &bufferTestKnownUploader{}
	started, release := make(chan struct{}), make(chan struct{})
	u.onSave = func() { close(started); <-release }
	saver := newFileSaver(ctx, nil, u, bufferTestFactory{size}, 0)
	saver.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile, Size: uint64(len(input))}, nil
	}
	lane := &parentPairHashLane{active: make(chan struct{}, 1)}
	s := &fileChunkState{ctx: ctx, readBuf: make([]byte, chunkReadBufSize), ordinaryHashes: lane}
	returned := make(chan struct{})
	var result futureNodeResult
	completed := 0
	go func() {
		saver.saveFile(ctx, bufferTestFactory{size}.NewChunker(), s, "/file", "file", &bufferTestFile{r: bytes.NewReader(input)}, func() {}, func() { t.Error("cancelled file reported reading complete") }, func(r futureNodeResult) { result = r; completed++ })
		close(returned)
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("save never started")
	}
	// The save is blocked and its detached payload is still resident, but it
	// must retain neither the stream admission nor borrowed assembly.
	j := lane.tryOrdinary()
	if j == nil {
		close(release)
		t.Fatal("blocked save retained hash admission")
	}
	j.write([]byte("another file"))
	if j.finish() != restic.Hash([]byte("another file")) {
		t.Fatal("contending file hash differs")
	}
	cancel()
	close(release)
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled save did not return")
	}
	if completed != 1 || !errors.Is(result.err, context.Canceled) || len(u.pending) != 1 || !bytes.Equal(u.pending[0].buf, input[:size]) {
		t.Fatal("cancelled save settlement or detached payload differs")
	}
	if len(lane.active) != 0 {
		t.Fatal("cancelled save leaked admission")
	}
}
