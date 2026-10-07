package archiver

import (
	"bytes"
	"io"
	"slices"
	"testing"

	"github.com/restic/chunker"
)

// Hash acceptance must be independent of the repository polynomial. The
// dependency produces both endpoints and complete byte identities separately
// from the parent-guided reader's speculative state.
func TestParentPairHashRepositoryPolynomials(t *testing.T) {
	input := b06Payload(24*1024*1024+131, 719)
	for _, pol := range []chunker.Pol{0x3DA3358B4DC173, 0x2482734cacca49} {
		parent, sizes := b18Guidance(input, pol)
		lane := &parentPairHashLane{active: make(chan struct{}, 1)}
		endpoints, ids, _, err := b02ReadHashed(t.Context(), bytes.NewReader(input), b18TrialFactory{pol}, parent, sizes, lane)
		want := b18Cuts(input, pol, 997)
		if err != io.EOF || !slices.Equal(endpoints, want) || !slices.Equal(ids, b18ContentIDs(input, want)) || len(lane.active) != 0 {
			t.Fatalf("polynomial %x: endpoints, IDs or settlement differ: %v", uint64(pol), err)
		}
	}
}
