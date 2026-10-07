package archiver

import (
	"bytes"
	"errors"
	"io"
	"math/rand"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// Compare the production factory/pipeline union with the pinned dependency and
// the ordinary reader. Errors accompanied by bytes must not publish a partial
// chunk or retain admission. Previously completed chunks remain observable.
func TestB23ScannerPipelineTerminalMatrix(t *testing.T) {
	const pol = chunker.Pol(0x3da3358b4dc173)
	factory := repository.TestRepository(t).ChunkerFactory()
	payload := make([]byte, 2*chunker.MaxSize+71)
	_, _ = rand.New(rand.NewSource(23004)).Read(payload)
	fault := errors.New("B23 source terminal failure")
	for _, size := range []int{0, chunkReadBufSize, 3*1024*1024 + 17, len(payload)} {
		for _, terminal := range []error{io.EOF, fault} {
			for _, busy := range []bool{false, true} {
				lane := &parentPairHashLane{active: make(chan struct{}, 1)}
				if busy {
					lane.active <- struct{}{}
				}
				got := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ordinaryHashes: lane}
				ref := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
				gc, rc := factory.NewChunker(), b18TrialFactory{pol}.NewChunker()
				gr := &b23ExactTerminalReader{bytes.Clone(payload[:size]), terminal}
				rr := &b23ExactTerminalReader{bytes.Clone(payload[:size]), terminal}
				gb, rb := make([]byte, 0, 1024), make([]byte, 0, 1024)
				at := 0
				for {
					g, eg := got.readNextChunk(gr, gc, gb)
					r, er := ref.readNextChunkOrdinary(rr, rc, rb)
					if !errors.Is(eg, er) || !bytes.Equal(g, r) {
						t.Fatalf("size=%d terminal=%v busy=%v at=%d got=%d/%v ref=%d/%v", size, terminal, busy, at, len(g), eg, len(r), er)
					}
					wantHeld := 0
					if busy {
						wantHeld = 1
					}
					if len(lane.active) != wantHeld {
						t.Fatal("union retained or consumed another reader's admission")
					}
					if eg != nil {
						if got.hashed {
							t.Fatal("failed/terminal chunk published an identity")
						}
						break
					}
					if got.hashed && got.chunkID != restic.Hash(r) {
						t.Fatal("union identity differs from current bytes")
					}
					if busy && got.hashed {
						t.Fatal("busy fallback published a borrowed identity")
					}
					at += len(g)
					gb, rb = g[:0], r[:0]
				}
				if busy {
					<-lane.active
				}
				// Reuse the same state after terminal failure, including the closed flag.
				got.reset()
				gc.Reset()
				g, err := got.readNextChunk(bytes.NewReader([]byte("fresh")), gc, gb)
				if err != nil || string(g) != "fresh" || got.hashed {
					t.Fatalf("reuse: %q %v hashed=%v", g, err, got.hashed)
				}
			}
		}
	}
}
