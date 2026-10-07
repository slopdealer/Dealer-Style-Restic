package repository

import (
	"fmt"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

// Compare each returned position, including calls with no bytes and resets in
// unfinished chunks. The dependency supplies the expected positions; neither
// table arithmetic nor state transitions from the candidate form the oracle.
func TestB23ExactChunkerCalls(t *testing.T) {
	for _, pol := range []chunker.Pol{0x3da3358b4dc173, 0x2482734cacca49, 0x101, 0x10001, 0x20000000000001} {
		f := newExactChunkerFactory(&chunkerFactory{pol: pol})
		c := f.NewChunker()
		ref := chunker.NewBase(pol)
		rng := rand.New(rand.NewSource(int64(pol)))
		for trial := range 8 {
			c.Reset()
			ref.Reset(pol)
			p := make([]byte, 2*chunker.MaxSize+trial)
			if trial%3 != 0 {
				_, _ = rng.Read(p)
			}
			for pos := 0; pos < len(p); {
				if got, want := c.NextSplitPoint(nil), ref.NextSplitPoint(nil); got != want {
					t.Fatal("empty call", got, want)
				}
				end := min(len(p), pos+1+rng.Intn(600000))
				got, want := c.NextSplitPoint(p[pos:end]), ref.NextSplitPoint(p[pos:end])
				if got != want {
					t.Fatalf("pol=%x trial=%d pos=%d: got %d want %d", pol, trial, pos, got, want)
				}
				if got < 0 {
					pos = end
				} else {
					pos += got
				}
			}
		}
	}
}

func TestB23ExactChunkerBoundaryIDs(t *testing.T) {
	for _, pol := range []chunker.Pol{0x3da3358b4dc173, 0x2482734cacca49} {
		f := newExactChunkerFactory(&chunkerFactory{pol: pol})
		c := f.NewChunker()
		ref := seedReference{chunker.NewBase(pol), pol}
		rng := rand.New(rand.NewSource(2317))
		p := make([]byte, 3*chunker.MaxSize+17)
		_, _ = rng.Read(p)
		for _, size := range []int{0, 1, 63, chunker.MinSize - 65, chunker.MinSize - 64, chunker.MinSize - 63, chunker.MinSize, chunker.MaxSize, len(p)} {
			ref.Reset()
			want := seedChunks(ref, p[:size], 65537)
			for _, part := range []int{37, 13001, 512 << 10, chunker.MaxSize} {
				c.NextSplitPoint(p[:min(size, 1027)])
				c.Reset()
				c.Reset()
				got := seedChunks(c, p[:size], part)
				if !slices.Equal(got, want) {
					t.Fatalf("pol=%x size=%d part=%d: got %v want %v", pol, size, part, got, want)
				}
			}
		}
	}
}

func b23Panic(f func()) (out string) { defer func() { out = fmt.Sprint(recover()) }(); f(); return }
func TestB23ExactChunkerInvalidPolynomials(t *testing.T) {
	for _, pol := range []chunker.Pol{0, 1, 0x81, 1 << 54, 1 << 63} {
		for _, input := range [][]byte{nil, {1}, make([]byte, chunker.MinSize)} {
			var got, want string
			got = b23Panic(func() {
				c := newExactChunkerFactory(&chunkerFactory{pol: pol}).NewChunker()
				c.Reset()
				c.NextSplitPoint(input)
			})
			want = b23Panic(func() { c := chunker.NewBase(pol); c.Reset(pol); c.NextSplitPoint(input) })
			if got != want {
				t.Fatalf("pol=%x len=%d panic=%q want=%q", pol, len(input), got, want)
			}
		}
	}
}

func TestB23ExactChunkerSharedTables(t *testing.T) {
	f := newExactChunkerFactory(&chunkerFactory{pol: 0x3da3358b4dc173}).(*exactChunkerFactory)
	original := *f
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(worker)))
			p := make([]byte, 4<<20)
			_, _ = rng.Read(p)
			c := f.NewChunker()
			ref := seedReference{chunker.NewBase(f.pol), f.pol}
			for range 4 {
				c.Reset()
				ref.Reset()
				if !slices.Equal(seedChunks(c, p, 13001+worker), seedChunks(ref, p, 65537)) {
					t.Error("worker boundaries differ")
				}
			}
		}()
	}
	wg.Wait()
	if *f != original {
		t.Fatal("shared configuration changed")
	}
}

// Use the public repository factory in both arms. go_bench overlays this test
// file on the incumbent, so the benchmark never names candidate-only types.
func BenchmarkB23RepositoryChunker(b *testing.B) {
	r, _, _ := TestRepositoryWithVersion(b, 2)
	f := r.ChunkerFactory()
	for _, size := range []int{4096, 4 << 20} {
		b.Run(fmt.Sprint(size), func(b *testing.B) {
			c := f.NewChunker()
			p := make([]byte, size)
			_, _ = rand.New(rand.NewSource(23)).Read(p)
			b.SetBytes(int64(size))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				c.Reset()
				pos := 0
				for pos < len(p) {
					cut := c.NextSplitPoint(p[pos:])
					if cut < 0 {
						break
					}
					pos += cut
				}
			}
		})
	}
}

var _ restic.Chunker = (*exactChunker)(nil)

func TestB23ExactChunkerForcedMaximumAndEdits(t *testing.T) {
	pol := chunker.Pol(0x3da3358b4dc173)
	f := newExactChunkerFactory(&chunkerFactory{pol: pol})
	ref := seedReference{chunker.NewBase(pol), pol}
	// A constant nonzero stream has a stable nonzero fingerprint. Verify the
	// reference really reaches the maximum; this is not a guessed expected cut.
	p := make([]byte, 2*chunker.MaxSize+73)
	for i := range p {
		p[i] = 255
	}
	ref.Reset()
	cuts := seedChunks(ref, p, 524288)
	if len(cuts) != 3 || cuts[0].End != chunker.MaxSize || cuts[1].End != 2*chunker.MaxSize {
		t.Fatalf("fixture is not a maximum-cut stream: %v", cuts)
	}
	inputs := [][]byte{p}
	mutated := slices.Clone(p)
	mutated[chunker.MinSize-64] ^= 31
	inputs = append(inputs, mutated)
	inserted := append(slices.Clone(p[:chunker.MaxSize-1]), 0)
	inserted = append(inserted, p[chunker.MaxSize-1:]...)
	inputs = append(inputs, inserted)
	deleted := append(slices.Clone(p[:chunker.MaxSize-1]), p[chunker.MaxSize:]...)
	inputs = append(inputs, deleted)
	for i, input := range inputs {
		ref.Reset()
		want := seedChunks(ref, input, 65537)
		for _, part := range []int{63, 13001, 512 << 10, chunker.MaxSize + 64} {
			c := f.NewChunker()
			got := seedChunks(c, input, part)
			if !slices.Equal(got, want) {
				t.Fatalf("case=%d part=%d got=%v want=%v", i, part, got, want)
			}
		}
	}
}
