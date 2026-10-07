package repository

import (
	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

type baseChunker struct {
	bc      *chunker.BaseChunker
	pol     chunker.Pol
	initial chunker.BaseChunker
}

func (c *baseChunker) Reset() {
	// BaseChunker contains only value fields and fixed arrays. Keep a private
	// initialized snapshot so resetting a file neither allocates nor consults
	// the dependency's global polynomial-table cache. Mutable state remains
	// private to this worker; no file bytes are retained by the snapshot.
	*c.bc = c.initial
}

func (c *baseChunker) NextSplitPoint(buf []byte) int {
	return c.bc.NextSplitPoint(buf)
}

type chunkerFactory struct {
	pol       chunker.Pol
	zeroChunk func() restic.ID
}

func newChunkerFactory(r *Repository) *chunkerFactory {
	return &chunkerFactory{
		pol:       r.Config().ChunkerPolynomial,
		zeroChunk: r.zeroChunk,
	}
}

func (f *chunkerFactory) NewChunker() restic.Chunker {
	bc := chunker.NewBase(f.pol)
	return &baseChunker{bc: bc, pol: f.pol, initial: *bc}
}

func (f *chunkerFactory) MaxChunkSize() int {
	return chunker.MaxSize
}

func (f *chunkerFactory) ZeroChunk() restic.ID {
	return f.zeroChunk()
}

func (r *Repository) ChunkerFactory() restic.ChunkerFactory {
	return newSegmentedChunkerFactory(newChunkerFactory(r))
}
