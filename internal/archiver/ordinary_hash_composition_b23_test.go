package archiver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// The stream's ownership contract applies even when its consumer has already
// received a terminal read error or cancellation. Finishing must join the live
// borrower before another reader, validator, or cleanup can reclaim storage.
func TestB23OrdinaryHeldHashFailureSettlement(t *testing.T) {
	for _, failure := range []error{errors.New("terminal input failure"), context.Canceled} {
		t.Run(failure.Error(), func(t *testing.T) {
			lane := &parentPairHashLane{active: make(chan struct{}, 1)}
			lane.active <- struct{}{}
			h := &ordinaryBlockedHash{Hash: sha256.New(), entered: make(chan struct{}), release: make(chan struct{}), stable: make(chan bool, 1)}
			stream := newOrdinaryHashStream(lane, h)
			assembly := bytes.Repeat([]byte{53}, 2*1024*1024)
			want := restic.Hash(assembly)
			stream.write(assembly)
			<-h.entered
			if lane.tryOrdinary() != nil || lane.try(assembly, assembly) != nil {
				t.Fatal("another borrower admitted while hashing")
			}
			returned := make(chan error, 1)
			closing := make(chan struct{})
			go func() {
				close(closing)
				got := stream.finish()
				if got != want {
					returned <- errors.New("held bytes hashed differently")
					return
				}
				// This models the error-side defer: digest is joined but not published.
				clear(assembly)
				returned <- failure
			}()
			<-closing
			select {
			case <-returned:
				t.Fatal("terminal failure reclaimed assembly before hash settlement")
			case <-time.After(10 * time.Millisecond):
			}
			close(h.release)
			select {
			case got := <-returned:
				if got != failure || !<-h.stable || len(lane.active) != 0 {
					t.Fatal("failure or borrowed bytes changed")
				}
			case <-time.After(5 * time.Second):
				t.Fatal("failure-side join stalled")
			}
			// A parent pair can use the same permit after the ordinary borrower settles.
			pair := lane.try(assembly, assembly)
			if pair == nil {
				t.Fatal("failure leaked shared parent admission")
			}
			ids, valid := pair.finish()
			if !valid[0] || !valid[1] || ids[0] != restic.Hash(assembly) || ids[1] != ids[0] {
				t.Fatal("later parent ownership differs")
			}
		})
	}
}

func TestB23SegmentedOrdinaryHashComposition(t *testing.T) {
	const pol = chunker.Pol(0x3da3358b4dc173)
	factory := repository.TestRepository(t).ChunkerFactory()
	random := b06Payload(19*1024*1024+137, 981)
	for inputIndex, payload := range [][]byte{random, make([]byte, len(random)), random[:1025], random[:0]} {
		for _, busy := range []bool{false, true} {
			lane := &parentPairHashLane{active: make(chan struct{}, 1)}
			if busy {
				lane.active <- struct{}{}
			}
			state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ordinaryHashes: lane}
			c := factory.NewChunker()
			reader := &b23ExactTerminalReader{payload, io.EOF}
			buffer := make([]byte, 0, 1024)
			at, hashes := 0, 0
			var endpoints []int
			for {
				body, err := state.readNextChunk(reader, c, buffer)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(body, payload[at:at+len(body)]) {
					t.Fatal("combined scanner changed assembly")
				}
				if state.hashed {
					hashes++
					if state.chunkID != restic.Hash(body) {
						t.Fatal("combined scanner changed current-byte identity")
					}
				}
				if len(lane.active) != b23BoolInt(busy) {
					t.Fatal("chunk publication retained or stole admission")
				}
				at += len(body)
				endpoints = append(endpoints, at)
				buffer = body[:0]
			}
			expected := b18Cuts(payload, pol, 65537)
			if len(payload) == 0 {
				expected = nil
			}
			if !slices.Equal(endpoints, expected) {
				t.Fatalf("combined stock endpoint mismatch got=%v want=%v", endpoints, expected)
			}
			if busy && hashes != 0 {
				t.Fatal("busy admission bypassed")
			}
			if !busy && inputIndex == 0 && hashes == 0 {
				t.Fatal("composition never exercised ordinary hashing")
			}
			if busy {
				<-lane.active
			}
		}
	}
	// Reader errors must preserve the completed prefix, discard the uncommitted
	// tail, and release the shared permit exactly as the dependency reader does.
	fault := errors.New("combined terminal read failure")
	for _, limit := range []int{17, 3*1024*1024 + 17, 9*1024*1024 + 31} {
		input := random[:limit]
		read := func(f restic.ChunkerFactory, lane *parentPairHashLane) ([]int, []restic.ID, error) {
			s := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ordinaryHashes: lane}
			c := f.NewChunker()
			rd := &b23ExactTerminalReader{input, fault}
			var ends []int
			var ids []restic.ID
			buf := make([]byte, 0, 1024)
			at := 0
			for {
				b, err := s.readNextChunk(rd, c, buf)
				if err != nil {
					if s.hashed {
						t.Fatal("failed tail published digest")
					}
					return ends, ids, err
				}
				at += len(b)
				ends = append(ends, at)
				ids = append(ids, restic.Hash(b))
				buf = b[:0]
			}
		}
		lane := &parentPairHashLane{active: make(chan struct{}, 1)}
		ge, gi, gerr := read(factory, lane)
		we, wi, werr := read(b18TrialFactory{pol}, nil)
		if !errors.Is(gerr, fault) || !errors.Is(werr, fault) || !slices.Equal(ge, we) || !slices.Equal(gi, wi) || len(lane.active) != 0 {
			t.Fatalf("failure prefix differs at %d", limit)
		}
	}
}

func b23BoolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}
