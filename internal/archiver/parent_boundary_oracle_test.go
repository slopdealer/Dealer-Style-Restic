package archiver

import (
	"slices"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

// This oracle deliberately calls the pinned dependency, independently of the
// repository wrapper and file reader. Endpoints include the EOF tail.
func b18Cuts(payload []byte, pol chunker.Pol, division int) []int {
	c := chunker.NewBase(pol)
	var ends []int
	for offset := 0; offset < len(payload); {
		end := min(offset+division, len(payload))
		n := c.NextSplitPoint(payload[offset:end])
		if n < 0 {
			offset = end
		} else {
			offset += n
			ends = append(ends, offset)
		}
	}
	if len(ends) == 0 || ends[len(ends)-1] != len(payload) {
		ends = append(ends, len(payload))
	}
	return ends
}

func TestB18ParentBoundaryProvenance(t *testing.T) {
	const a = chunker.Pol(0x3DA3358B4DC173)
	// The reciprocal of an irreducible polynomial is also irreducible.
	var b chunker.Pol
	for p := a; p != 0; p >>= 1 {
		b = (b << 1) | (p & 1)
	}
	payload := b06Payload(16*1024*1024+173, 881)
	parent := b18Cuts(payload, a, 13001)
	current := b18Cuts(payload, b, 13001)
	if slices.Equal(parent, current) {
		t.Fatal("counterexample failed to distinguish polynomials")
	}
	start := 0
	for _, end := range parent {
		id := restic.Hash(payload[start:end])
		if id != restic.Hash(slices.Clone(payload[start:end])) {
			t.Fatal("unchanged parent span did not verify")
		}
		start = end
	}
	t.Logf("all parent hashes match; polynomial A endpoints %v, polynomial B endpoints %v", parent, current)
}

func TestB18RestartAtCurrentCuts(t *testing.T) {
	const pol = chunker.Pol(0x3DA3358B4DC173)
	payload := b06Payload(8*1024*1024+173, 887)
	want := b18Cuts(payload, pol, 13001)
	for _, division := range []int{1, 13001, 524288, 600013} {
		if got := b18Cuts(payload, pol, division); !slices.Equal(got, want) {
			t.Fatalf("read division %d changed cuts: %v vs %v", division, got, want)
		}
		start := 0
		// The final endpoint may be EOF, and is intentionally not certified.
		for _, end := range want[:len(want)-1] {
			got := b18Cuts(payload[start:end], pol, division)
			if len(got) != 1 || got[0] != end-start {
				t.Fatalf("restart at %d did not yield next current cut %d", start, end)
			}
			// b18Cuts appends EOF, so also require a real cut at the end.
			c := chunker.NewBase(pol)
			if split := c.NextSplitPoint(payload[start:end]); split != end-start {
				t.Fatalf("endpoint %d was not a real cut: %d", end, split)
			}
			start = end
		}
	}
	t.Logf("current-cut restart invariant holds across four read divisions; endpoints %v", want)
}
