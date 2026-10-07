package repository

import (
	"github.com/restic/chunker"
	"math/rand"
	"testing"
)

// Place call boundaries immediately before and after dependency-derived cuts.
// In particular, continuation after a segment tail must use the last 64 bytes
// actually accepted, and the next chunk must discard the old window completely.
func TestB23SegmentCutAdjacentCalls(t *testing.T) {
	for _, pol := range []chunker.Pol{0x101, 0x10001, 0x3da3358b4dc173, 0x2482734cacca49} {
		payload := make([]byte, 2*chunker.MaxSize+123)
		_, _ = rand.New(rand.NewSource(int64(pol) + 23004)).Read(payload)
		cuts := seedChunks(seedReference{chunker.NewBase(pol), pol}, payload, 65537)
		for _, delta := range []int{-65, -64, -63, -1, 0, 1, 63, 64, 65, 255, 256, 257} {
			got := newSegmentedChunkerFactory(&chunkerFactory{pol: pol}).NewChunker()
			ref := chunker.NewBase(pol)
			at, next := 0, 0
			for at < len(payload) {
				for next < len(cuts) && cuts[next].End+delta <= at {
					next++
				}
				end := len(payload)
				if next < len(cuts) {
					end = min(end, cuts[next].End+delta)
				}
				// Leave differently-sized scalar and segmented tails around every cut.
				end = min(end, at+chunker.MinSize+1025)
				g, w := got.NextSplitPoint(payload[at:end]), ref.NextSplitPoint(payload[at:end])
				if g != w {
					t.Fatalf("pol=%x delta=%d at=%d end=%d got=%d want=%d", pol, delta, at, end, g, w)
				}
				if got.NextSplitPoint(nil) != ref.NextSplitPoint(nil) {
					t.Fatal("empty continuation differs")
				}
				if g < 0 {
					at = end
				} else {
					at += g
				}
			}
		}
	}
}
