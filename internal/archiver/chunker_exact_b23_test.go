package archiver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

type b23ExactTerminalReader struct {
	data []byte
	err  error
}

func (r *b23ExactTerminalReader) Read(p []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, r.err
	}
	n := copy(p, r.data[:min(len(r.data), 13001)])
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func TestB23ExactFactoryGuidedReader(t *testing.T) {
	const pol = chunker.Pol(0x3da3358b4dc173)
	factory := repository.TestRepository(t).ChunkerFactory()
	original, parent, sizes := b18LongFixture(t)
	for _, mode := range []string{"unchanged", "skip-mutation", "first-certificate", "second-certificate", "truncated", "grown"} {
		t.Run(mode, func(t *testing.T) {
			input := bytes.Clone(original)
			lengths := make(map[restic.ID]uint)
			for id, n := range sizes {
				lengths[id] = n
			}
			switch mode {
			case "skip-mutation":
				input[17] ^= 77
			case "first-certificate":
				lengths[parent.Content[0]]++
			case "second-certificate":
				lengths[parent.Content[1]]++
			case "truncated":
				input = input[:int(sizes[parent.Content[0]])+17]
			case "grown":
				input = append(input, original[:900000]...)
			}
			ends, ids, _, err := b18ReadLong(t.Context(), &b23ExactTerminalReader{input, io.EOF}, factory, parent, lengths)
			if err != nil {
				t.Fatal(err)
			}
			want := b18Cuts(input, pol, 65537)
			if !slices.Equal(ends, want) {
				t.Fatalf("got endpoints %v want %v", ends, want)
			}
			start := 0
			for i, end := range want {
				if ids[i] != restic.Hash(input[start:end]) {
					t.Fatal("identity differs")
				}
				start = end
			}
		})
	}
	// The input bytes and terminal error travel together. The independent
	// dependency-backed factory defines which cuts may already have settled.
	fault := errors.New("input terminal failure")
	for _, limit := range []int{17, chunker.MinSize, 2 * chunker.MaxSize} {
		input := original[:limit]
		ge, gi, _, gerr := b18ReadLong(t.Context(), &b23ExactTerminalReader{input, fault}, factory, parent, sizes)
		we, wi, _, werr := b18ReadLong(t.Context(), &b23ExactTerminalReader{input, fault}, b18TrialFactory{pol}, parent, sizes)
		if !errors.Is(gerr, fault) || !errors.Is(werr, fault) || !slices.Equal(ge, we) || !slices.Equal(gi, wi) {
			t.Fatalf("partial error limit=%d got=%v/%v/%v want=%v/%v/%v", limit, ge, gi, gerr, we, wi, werr)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	ends, ids, _, err := b18ReadLong(ctx, bytes.NewReader(original), factory, parent, sizes)
	if !errors.Is(err, context.Canceled) || len(ends) != 0 || len(ids) != 0 {
		t.Fatalf("cancelled input emitted: %v %v %v", ends, ids, err)
	}
}
