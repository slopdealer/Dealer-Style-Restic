package archiver

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

type b18LongBlockedFactory struct {
	restic.ChunkerFactory
	started chan struct{}
	release chan struct{}
	checked chan bool
}

func (f b18LongBlockedFactory) NewChunker() restic.Chunker {
	return b18LongBlockedChunker{Chunker: f.ChunkerFactory.NewChunker(), started: f.started, release: f.release, checked: f.checked}
}

type b18LongBlockedChunker struct {
	restic.Chunker
	started chan struct{}
	release chan struct{}
	checked chan bool
}

func (c b18LongBlockedChunker) NextSplitPoint(p []byte) int {
	before := restic.Hash(p)
	close(c.started)
	<-c.release
	c.checked <- (before == restic.Hash(p))
	return c.Chunker.NextSplitPoint(p)
}

// Deliberately keep the extra validator reading the borrowed suffix after the
// ordinary validator finishes. Cancellation and failed-first replay must join,
// just as the successful path does, before the caller can recycle its storage.
func TestB18LongPairBlockedValidatorJoin(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	input, parent, original := b18LongFixture(t)
	for _, cancelRead := range []bool{false, true} {
		t.Run(map[bool]string{false: "failed-first", true: "cancelled"}[cancelRead], func(t *testing.T) {
			sizes := make(map[restic.ID]uint)
			for id, n := range original {
				sizes[id] = n
			}
			if !cancelRead {
				sizes[parent.Content[0]] += 10
				sizes[parent.Content[1]] -= 10
			}
			factory := b18LongBlockedFactory{b18TrialFactory{pol}, make(chan struct{}), make(chan struct{}), make(chan bool, 1)}
			g := &parentCutReader{parent: parent, factory: factory, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: ctx, guided: g}
			storage := make([]byte, 0, chunker.MaxSize)
			type result struct {
				out []byte
				err error
			}
			done := make(chan result, 1)
			go func() {
				out, err := state.readNextChunk(bytes.NewReader(input), b18TrialFactory{pol}.NewChunker(), storage)
				done <- result{out, err}
			}()
			select {
			case <-factory.started:
			case <-time.After(5 * time.Second):
				t.Fatal("extra validator did not start")
			}
			if cancelRead {
				cancel()
			}
			// This timeout only detects premature settlement; it is not a timing test.
			select {
			case <-done:
				close(factory.release)
				t.Fatal("reader returned with a live validator")
			case <-time.After(10 * time.Millisecond):
			}
			close(factory.release)
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("reader did not join")
			}
			if !<-factory.checked {
				t.Fatal("borrowed suffix changed while validator held it")
			}
			if cancelRead {
				if got.out != nil || !errors.Is(got.err, context.Canceled) {
					t.Fatal("cancelled read published output", got.err)
				}
			} else {
				want := int(original[parent.Content[0]])
				if got.err != nil || len(got.out) != want || !bytes.Equal(got.out, input[:want]) || g.replayed == 0 {
					t.Fatal("failed first certificate replay mismatch", got.err)
				}
			}
			buf := &buffer{Data: storage}
			g.reclaim(buf)
			if &buf.Data[0] != &storage[:cap(storage)][0] || cap(buf.Data) != cap(storage) {
				t.Fatal("joined read lost base")
			}
			clear(storage[:cap(storage)])
		})
	}
}
