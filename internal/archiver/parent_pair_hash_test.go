package archiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

func TestParentPairHashAdmission(t *testing.T) {
	lane := &parentPairHashLane{active: make(chan struct{}, 1)}
	first := b06Payload(2*asyncHashBufferSize+13, 271)
	second := b06Payload(asyncHashBufferSize+29, 277)
	job := lane.try(first, second)
	if job == nil {
		t.Fatal("large pair rejected")
	}
	// A completed but not joined worker still owns its borrowed assembly.
	<-job.done
	if lane.try(first, second) != nil {
		t.Fatal("unjoined worker released admission")
	}
	ids, valid := job.finish()
	if valid != [2]bool{true, true} || ids != [2]restic.ID{restic.Hash(first), restic.Hash(second)} {
		t.Fatal("pair hash differs from complete SHA256")
	}
	if len(lane.active) != 0 || lane.try(first[:asyncHashBufferSize], second[:asyncHashBufferSize]) != nil {
		t.Fatal("admission was retained or small route was changed")
	}
	one := lane.try(first, second[:17])
	ids, valid = one.finish()
	if valid != [2]bool{true, false} || ids[0] != restic.Hash(first) {
		t.Fatal("small side was unexpectedly admitted")
	}
}

func b02ReadHashed(ctx context.Context, rd io.Reader, factory b18TrialFactory, parent *data.Node, sizes map[restic.ID]uint, lane *parentPairHashLane) ([]int, []restic.ID, int, error) {
	g := &parentCutReader{parent: parent, factory: factory, hashes: lane, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}
	state := fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: ctx, guided: g}
	c := factory.NewChunker()
	scratch := make([]byte, 0, 2*1024*1024)
	var ends []int
	var ids []restic.ID
	offset, admitted := 0, 0
	for {
		chunk, err := state.readNextChunk(rd, c, scratch)
		if err != nil {
			return ends, ids, admitted, err
		}
		id := restic.Hash(chunk)
		if state.hashed {
			admitted++
			if state.chunkID != id {
				return nil, nil, admitted, errors.New("speculative hash accepted for different bytes")
			}
			id = state.chunkID
		}
		offset += len(chunk)
		ends = append(ends, offset)
		ids = append(ids, id)
		scratch = chunk
	}
}

func TestParentPairHashIndependentBoundaries(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	base := b06Payload(40*1024*1024+173, 281)
	parent, sizes := b18Guidance(base, pol)
	mutation := bytes.Clone(base)
	for i := 3 * 1024 * 1024; i < 3*1024*1024+70013; i++ {
		mutation[i] ^= 29
	}
	insertion := append(bytes.Clone(base[:2100013]), base[:277]...)
	insertion = append(insertion, base[2100013:]...)
	var hashed int
	for _, tc := range []struct {
		name  string
		input []byte
	}{
		{"unchanged", base}, {"mutation", mutation}, {"insertion", insertion},
		{"growth", append(bytes.Clone(base), base[:1300013]...)},
		{"truncation", base[:7*1024*1024+13]}, {"empty", nil},
		{"zeros", make([]byte, 4*1024*1024+71)},
	} {
		for _, bad := range []int{-1, 0, 1} {
			for _, division := range []int{37, 13001, 524288} {
				t.Run(fmt.Sprintf("%s/hint%d/read%d", tc.name, bad, division), func(t *testing.T) {
					local := make(map[restic.ID]uint)
					for id, n := range sizes {
						local[id] = n
					}
					if bad >= 0 {
						local[parent.Content[bad]] += 113
					}
					lane := &parentPairHashLane{active: make(chan struct{}, 1)}
					got, ids, count, err := b02ReadHashed(t.Context(), &b18TrialShortReader{r: bytes.NewReader(tc.input), limit: division}, b18TrialFactory{pol}, parent, local, lane)
					hashed += count
					want := b18Cuts(tc.input, pol, 13001)
					if len(tc.input) == 0 {
						want = nil
					}
					if err != io.EOF || !slices.Equal(got, want) || !slices.Equal(ids, b18ContentIDs(tc.input, want)) || len(lane.active) != 0 {
						t.Fatalf("boundaries, IDs or settlement changed: %v", err)
					}
				})
			}
		}
	}
	if hashed == 0 {
		t.Fatal("no validated hash was exercised")
	}
}

func TestParentPairHashFailureSettlement(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	base := b06Payload(10*1024*1024+13, 283)
	parent, sizes := b18Guidance(base, pol)
	for _, cancel := range []bool{false, true} {
		ctx, stop := context.WithCancel(t.Context())
		failure := errors.New("terminal file failure")
		var rd io.Reader = io.MultiReader(bytes.NewReader(base), b18ErrorReader{failure})
		if cancel {
			stop()
			failure = context.Canceled
		}
		lane := &parentPairHashLane{active: make(chan struct{}, 1)}
		_, _, _, err := b02ReadHashed(ctx, rd, b18TrialFactory{pol}, parent, sizes, lane)
		stop()
		if !errors.Is(err, failure) || len(lane.active) != 0 {
			t.Fatalf("failure not joined: %v", err)
		}
	}
}

func TestParentPairHashSaverOwnership(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	original := b06Payload(20*1024*1024+173, 293)
	parent, sizes := b18Guidance(original, pol)
	mutated := bytes.Clone(original)
	for i := 3100013; i < 3170026; i++ {
		mutated[i] ^= 31
	}
	for _, input := range [][]byte{original, mutated} {
		want := b06ExpectedChunks(input)
		u := &b18HashUploader{known: make(map[restic.ID]int)}
		for i, p := range want {
			if i%2 == 0 {
				u.known[p.id] = p.size
			}
		}
		factory := b18TrialFactory{pol}
		s := newFileSaver(t.Context(), nil, u, factory, 0)
		b18EnableHash(s)
		lane := &parentPairHashLane{active: make(chan struct{}, 1)}
		s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
			return &data.Node{Type: data.NodeTypeFile, Size: uint64(len(input))}, nil
		}
		state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize), ctx: t.Context(), guided: &parentCutReader{parent: parent, factory: factory, hashes: lane, lookup: func(h restic.BlobHandle) (uint, bool) { n, ok := sizes[h.ID]; return n, ok }}}
		file := &b06SaverFile{r: bytes.NewReader(input)}
		var result futureNodeResult
		reading, completed := 0, 0
		s.saveFile(t.Context(), factory.NewChunker(), state, "/file", "file", file, func() {}, func() { reading++ }, func(r futureNodeResult) { result = r; completed++ })
		if !file.closed || reading != 1 || completed != 0 || len(lane.active) != 0 {
			t.Fatal("reading or admission settlement changed")
		}
		byID := make(map[restic.ID]b06PendingUpload)
		for _, p := range want {
			byID[p.id] = p
		}
		// Destroy the read/assembly view before delayed callbacks settle. Unknown
		// upload bytes must have already detached from that storage.
		if state.guided.root != nil {
			clear(state.guided.root)
		}
		clear(state.readBuf)
		for i := len(u.pending) - 1; i >= 0; i-- {
			p := u.pending[i]
			w, ok := byID[p.id]
			if !ok || p.size != w.size || (!p.known && !bytes.Equal(p.data, w.data)) {
				t.Fatal("upload borrowed assembly or used speculative ID")
			}
			p.cb(p.id, p.known, p.size, nil)
		}
		ids := make([]restic.ID, len(want))
		for i, p := range want {
			ids[i] = p.id
		}
		if completed != 1 || result.err != nil || !slices.Equal(result.node.Content, ids) {
			t.Fatal("callbacks changed ordered file completion")
		}
	}
}
