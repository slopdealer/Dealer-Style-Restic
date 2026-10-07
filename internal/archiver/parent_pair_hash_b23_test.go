package archiver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

func TestB23PairHashCurrentBytesAndFailedCertificates(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	original, parent, sizes := b18LongFixture(t)
	for _, mode := range []string{"current-bytes", "first-fails", "second-fails", "busy"} {
		t.Run(mode, func(t *testing.T) {
			input := bytes.Clone(original)
			input[17] ^= 1 // skipped prefix: same cut, different content identity
			input[int(sizes[parent.Content[0]])+17] ^= 1
			lengths := make(map[restic.ID]uint)
			for id, n := range sizes {
				lengths[id] = n
			}
			if mode == "first-fails" {
				lengths[parent.Content[0]] += 10
				lengths[parent.Content[1]] -= 10
			}
			if mode == "second-fails" {
				lengths[parent.Content[1]] += 10
			}
			lane := &parentPairHashLane{active: make(chan struct{}, 1)}
			if mode == "busy" {
				lane.active <- struct{}{}
			}
			g := &parentCutReader{parent: parent, factory: b18TrialFactory{pol}, hashes: lane, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := lengths[h.ID]; return n, ok }}
			state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: g}
			rd := bytes.NewReader(input)
			ordinary := b18TrialFactory{pol}.NewChunker()
			storage := make([]byte, 0, chunker.MaxSize)
			var ends []int
			offset, hashed := 0, 0
			for {
				out, err := state.readNextChunk(rd, ordinary, storage)
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				if state.hashed {
					hashed++
					if state.chunkID != restic.Hash(out) {
						t.Fatal("published hash does not name returned bytes")
					}
					if offset == 0 && state.chunkID == parent.Content[0] {
						t.Fatal("parent identity reused after content mutation")
					}
				}
				if offset == 0 && mode == "first-fails" && state.hashed {
					t.Fatal("failed first certificate published speculative hash")
				}
				if offset == int(sizes[parent.Content[0]]) && mode == "second-fails" && state.hashed {
					t.Fatal("failed second certificate published speculative hash")
				}
				if mode == "busy" && state.hashed {
					t.Fatal("busy lane supplied hash")
				}
				offset += len(out)
				ends = append(ends, offset)
				storage = out
			}
			if !slices.Equal(ends, b18Cuts(input, pol, 13001)) {
				t.Fatal("cuts differ from dependency")
			}
			if mode == "current-bytes" && hashed == 0 {
				t.Fatal("no speculative hash exercised")
			}
			if mode == "busy" {
				<-lane.active
			}
			if len(lane.active) != 0 {
				t.Fatal("lane permit stranded")
			}
		})
	}
}

func TestB23PairHashCancellationRetainsAdmissionUntilJoin(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input, parent, sizes := b18LongFixture(t)
	lane := &parentPairHashLane{active: make(chan struct{}, 1)}
	factory := b18LongBlockedFactory{b18TrialFactory{pol}, make(chan struct{}), make(chan struct{}), make(chan bool, 1)}
	g := &parentCutReader{parent: parent, factory: factory, hashes: lane, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: ctx, guided: g}
	storage := make([]byte, 0, chunker.MaxSize)
	done := make(chan error, 1)
	go func() {
		out, err := state.readNextChunk(bytes.NewReader(input), b18TrialFactory{pol}.NewChunker(), storage)
		if out != nil {
			done <- errors.New("cancelled read returned output")
			return
		}
		done <- err
	}()
	select {
	case <-factory.started:
	case <-time.After(5 * time.Second):
		t.Fatal("validator did not start")
	}
	if len(lane.active) != 1 {
		close(factory.release)
		t.Fatal("hash admission released before validators joined")
	}
	cancel()
	select {
	case <-done:
		close(factory.release)
		t.Fatal("returned before validator joined")
	case <-time.After(10 * time.Millisecond):
	}
	close(factory.release)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not join")
	}
	if !<-factory.checked || state.hashed || len(lane.active) != 0 {
		t.Fatal("borrow changed, hash published, or permit stranded")
	}
	buf := &buffer{Data: storage}
	g.reclaim(buf)
	if &buf.Data[:cap(buf.Data)][0] != &storage[:cap(storage)][0] {
		t.Fatal("allocation base lost")
	}
	clear(buf.Data[:cap(buf.Data)])
}
