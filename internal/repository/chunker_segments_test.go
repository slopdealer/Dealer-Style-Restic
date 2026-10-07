package repository

import (
	"github.com/restic/restic/internal/restic"
	"math/rand"
	"slices"
	"sync"
	"testing"

	"github.com/restic/chunker"
)

func TestB23SegmentCalls(t *testing.T) {
	for _, pol := range []chunker.Pol{0x101, 0x10001, 0x3da3358b4dc173, 0x2482734cacca49, 0x20000000000001} {
		rng := rand.New(rand.NewSource(int64(pol)))
		input := make([]byte, 2*chunker.MaxSize+321)
		for _, mode := range []string{"random", "zero", "maximum"} {
			switch mode {
			case "random":
				_, _ = rng.Read(input)
			case "zero":
				clear(input)
			case "maximum":
				for i := range input {
					input[i] = 255
				}
			}
			for _, part := range []int{1, 63, 64, 511, 1023, 1024, 1025, 4097, 65537, 512 << 10, len(input)} {
				c := newSegmentedChunkerFactory(&chunkerFactory{pol: pol}).NewChunker()
				ref := seedReference{chunker.NewBase(pol), pol}
				// A cut immediately resets; an empty call and explicit double reset
				// must not turn the preceding segment into part of the next chunk.
				c.NextSplitPoint(input[:min(700001, len(input))])
				c.Reset()
				c.Reset()
				ref.Reset()
				got := seedChunks(c, input, part)
				want := seedChunks(ref, input, part)
				if !slices.Equal(got, want) {
					t.Fatalf("pol=%x mode=%s part=%d got=%v want=%v", pol, mode, part, got, want)
				}
				if c.NextSplitPoint(nil) != ref.NextSplitPoint(nil) {
					t.Fatal("empty continuation differs")
				}
			}
		}
	}
}

// All three organizations run in the same candidate binary. The tool's base
// arm has no such benchmark; use these rows as unpaired micro evidence only.
func BenchmarkB23ScannerOrganizations(b *testing.B) {
	const pol = chunker.Pol(0x3da3358b4dc173)
	factories := []struct {
		name string
		make func() restic.Chunker
	}{
		{"dependency", func() restic.Chunker { return seedReference{chunker.NewBase(pol), pol} }},
		{"base-snapshot", func() restic.Chunker { return (&chunkerFactory{pol: pol}).NewChunker() }},
		{"scalar", func() restic.Chunker { return newExactChunkerFactory(&chunkerFactory{pol: pol}).NewChunker() }},
		{"segments", func() restic.Chunker { return newSegmentedChunkerFactory(&chunkerFactory{pol: pol}).NewChunker() }},
	}
	input := make([]byte, 4<<20)
	_, _ = rand.New(rand.NewSource(23)).Read(input)
	for _, entry := range factories {
		b.Run(entry.name, func(b *testing.B) {
			c := entry.make()
			b.SetBytes(int64(len(input)))
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				c.Reset()
				pos := 0
				for pos < len(input) {
					cut := c.NextSplitPoint(input[pos:])
					if cut < 0 {
						break
					}
					pos += cut
				}
			}
		})
	}
}

func TestB23SegmentInvalidPolynomials(t *testing.T) {
	for _, pol := range []chunker.Pol{0, 1, 0x81, 1 << 54, 1 << 63} {
		for _, input := range [][]byte{nil, {1}, make([]byte, chunker.MinSize)} {
			got := b23Panic(func() {
				c := newSegmentedChunkerFactory(&chunkerFactory{pol: pol}).NewChunker()
				c.Reset()
				c.NextSplitPoint(input)
			})
			want := b23Panic(func() { c := chunker.NewBase(pol); c.Reset(pol); c.NextSplitPoint(input) })
			if got != want {
				t.Fatalf("pol=%x len=%d panic=%q want=%q", pol, len(input), got, want)
			}
		}
	}
}

func TestB23SegmentWorkerIsolation(t *testing.T) {
	const pol = chunker.Pol(0x3da3358b4dc173)
	f := newSegmentedChunkerFactory(&chunkerFactory{pol: pol}).(*segmentedChunkerFactory)
	before := *f.exactChunkerFactory
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			payload := make([]byte, 4<<20)
			_, _ = rand.New(rand.NewSource(int64(worker))).Read(payload)
			c := f.NewChunker()
			ref := seedReference{chunker.NewBase(pol), pol}
			for round := range 3 {
				c.Reset()
				ref.Reset()
				if !slices.Equal(seedChunks(c, payload, 13001+round), seedChunks(ref, payload, 65537)) {
					t.Errorf("worker=%d round=%d boundaries differ", worker, round)
				}
			}
		})
	}
	wg.Wait()
	if *f.exactChunkerFactory != before {
		t.Fatal("immutable tables changed")
	}
}
