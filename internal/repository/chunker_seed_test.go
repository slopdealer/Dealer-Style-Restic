package repository

import (
	"fmt"
	"math/rand"
	"reflect"
	"slices"
	"sync"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

type seedChunk struct {
	End int
	ID  restic.ID
}

func seedChunks(c restic.Chunker, payload []byte, partition int) []seedChunk {
	var out []seedChunk
	start, pos := 0, 0
	for pos < len(payload) {
		end := min(pos+partition, len(payload))
		split := c.NextSplitPoint(payload[pos:end])
		if split < 0 {
			pos = end
			continue
		}
		if split == 0 || split > end-pos {
			panic("invalid chunk boundary")
		}
		pos += split
		out = append(out, seedChunk{pos, restic.Hash(payload[start:pos])})
		start = pos
	}
	if start < pos {
		out = append(out, seedChunk{pos, restic.Hash(payload[start:pos])})
	}
	return out
}

// The reference deliberately uses the pinned dependency rather than the wrapper.
type seedReference struct {
	bc  *chunker.BaseChunker
	pol chunker.Pol
}

func (c seedReference) Reset()                      { c.bc.Reset(c.pol) }
func (c seedReference) NextSplitPoint(b []byte) int { return c.bc.NextSplitPoint(b) }

func TestChunkerSeedDifferential(t *testing.T) {
	for _, pol := range []chunker.Pol{0x3DA3358B4DC173, 0x2482734cacca49} {
		f := &chunkerFactory{pol: pol}
		got := f.NewChunker().(*baseChunker)
		want := seedReference{chunker.NewBase(pol), pol}
		for _, zero := range []bool{false, true} {
			for _, size := range []int{0, 1, 63, 65537, chunker.MinSize - 1, chunker.MinSize, chunker.MinSize + 1, 3*chunker.MaxSize + 31} {
				payload := make([]byte, size)
				if !zero {
					_, _ = rand.New(rand.NewSource(int64(size))).Read(payload)
				}
				partitions := []int{13001, 512 * 1024, chunker.MaxSize}
				if size <= chunker.MinSize+1 {
					partitions = append(partitions, 1, 63)
				}
				for _, partition := range partitions {
					// Leave an interrupted chunk, then reset twice before a new file.
					got.NextSplitPoint(payload[:min(100003, size)])
					want.NextSplitPoint(payload[:min(100003, size)])
					got.Reset()
					got.Reset()
					want.Reset()
					want.Reset()
					g := seedChunks(got, payload, partition)
					w := seedChunks(want, payload, partition)
					if !slices.Equal(g, w) || *got.bc != *want.bc {
						t.Fatalf("pol=%x zero=%v size=%d partition=%d: got=%v want=%v", pol, zero, size, partition, g, w)
					}
					// The same bytes, read in different partitions, must cut identically.
					want.Reset()
					if !slices.Equal(g, seedChunks(want, payload, 512*1024)) {
						t.Fatal("partition changed boundaries")
					}
					if got.initial != *chunker.NewBase(pol) {
						t.Fatal("seed mutated")
					}
					// Overwriting a previous read buffer cannot affect later files.
					clear(payload)
					got.Reset()
					want.Reset()
					if *got.bc != *want.bc {
						t.Fatal("reset retained previous input")
					}
					if !zero {
						_, _ = rand.New(rand.NewSource(int64(size))).Read(payload)
					}
				}
			}
		}
	}
}

func TestChunkerSeedWorkerIsolation(t *testing.T) {
	var wg sync.WaitGroup
	for _, pol := range []chunker.Pol{0x3DA3358B4DC173, 0x2482734cacca49} {
		f := &chunkerFactory{pol: pol}
		for worker := range 4 {
			wg.Go(func() {
				got := f.NewChunker().(*baseChunker)
				want := seedReference{chunker.NewBase(pol), pol}
				payload := make([]byte, 2*1024*1024+worker)
				_, _ = rand.New(rand.NewSource(int64(worker))).Read(payload)
				for round := range 4 {
					got.Reset()
					want.Reset()
					if !slices.Equal(seedChunks(got, payload, 13001+round), seedChunks(want, payload, 13001+round)) {
						t.Errorf("worker=%d pol=%x round=%d: boundaries differ", worker, pol, round)
						return
					}
				}
			})
		}
	}
	wg.Wait()
}

func TestChunkerSeedHasNoBorrowedStorage(t *testing.T) {
	var check func(reflect.Type)
	check = func(typ reflect.Type) {
		switch typ.Kind() {
		case reflect.Struct:
			for i := range typ.NumField() {
				check(typ.Field(i).Type)
			}
		case reflect.Array:
			check(typ.Elem())
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface, reflect.Chan, reflect.Func, reflect.String, reflect.UnsafePointer:
			t.Fatalf("dependency seed contains reference-bearing type %v", typ)
		}
	}
	check(reflect.TypeFor[chunker.BaseChunker]())
	// Also guard a polynomial for which the dependency intentionally has no tables.
	got := (&chunkerFactory{pol: 0}).NewChunker().(*baseChunker)
	got.Reset()
	defer func() {
		if p := recover(); fmt.Sprint(p) != "tables for polynomial computation not initialized" {
			t.Fatalf("zero-polynomial panic changed: %v", p)
		}
	}()
	got.NextSplitPoint([]byte{1})
}
