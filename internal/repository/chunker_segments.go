package repository

import (
	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

// Independent segments use the fingerprint of the preceding 64 actual bytes.
// The seed has then left the rolling window. Two scalar recurrence chains can
// advance independently, but the earlier segment always decides the first cut.
// No worker, retained input, or additional payload buffer is introduced.
type segmentedChunkerFactory struct{ *exactChunkerFactory }

func newSegmentedChunkerFactory(f *chunkerFactory) restic.ChunkerFactory {
	exact := newExactChunkerFactory(f)
	if ef, ok := exact.(*exactChunkerFactory); ok {
		return &segmentedChunkerFactory{ef}
	}
	return exact
}

func (f *segmentedChunkerFactory) NewChunker() restic.Chunker {
	return &segmentedChunker{f.exactChunkerFactory.NewChunker().(*exactChunker)}
}

type segmentedChunker struct{ *exactChunker }

func (c *segmentedChunker) NextSplitPoint(input []byte) int {
	offset := 0
	if c.skip > 0 {
		n := min(int(c.skip), len(input))
		c.exactChunker.NextSplitPoint(input[:n])
		offset = n
		input = input[n:]
	}
	if len(input) < 1024 {
		cut := c.exactChunker.NextSplitPoint(input)
		if cut >= 0 {
			return offset + cut
		}
		return -1
	}
	// Establish a wholly input-derived window before indexing outgoing bytes
	// directly from input. This also preserves the earliest minimum-size cut.
	if cut := c.exactChunker.NextSplitPoint(input[:64]); cut >= 0 {
		return offset + cut
	}
	f := c.config
	shift := f.shift
	if shift > 45 {
		panic("the polynomial must have a degree less than or equal 53")
	}
	digest, count := c.digest, c.count
	pos := 64
	const segment = 256
	for pos+2*segment <= len(input) {
		var second uint64
		for _, b := range input[pos+segment-64 : pos+segment] {
			second = (second<<8 | uint64(b)) ^ f.mod[second>>shift]
		}
		secondCut := -1
		for i := 0; i < segment; i++ {
			digest ^= f.out[input[pos+i-64]]
			digest = (digest<<8 | uint64(input[pos+i])) ^ f.mod[digest>>shift]
			second ^= f.out[input[pos+segment+i-64]]
			second = (second<<8 | uint64(input[pos+segment+i])) ^ f.mod[second>>shift]
			firstCount := count + uint(i) + 1
			if (digest&((1<<20)-1) == 0 || firstCount >= chunker.MaxSize) && firstCount >= chunker.MinSize {
				c.Reset()
				return offset + pos + i + 1
			}
			secondCount := firstCount + segment
			if secondCut < 0 && (second&((1<<20)-1) == 0 || secondCount >= chunker.MaxSize) && secondCount >= chunker.MinSize {
				secondCut = i
			}
		}
		if secondCut >= 0 {
			c.Reset()
			return offset + pos + segment + secondCut + 1
		}
		digest = second
		count += 2 * segment
		pos += 2 * segment
	}
	for ; pos < len(input); pos++ {
		digest ^= f.out[input[pos-64]]
		digest = (digest<<8 | uint64(input[pos])) ^ f.mod[digest>>shift]
		count++
		if (digest&((1<<20)-1) == 0 || count >= chunker.MaxSize) && count >= chunker.MinSize {
			c.Reset()
			return offset + pos + 1
		}
	}
	c.digest, c.count, c.pos = digest, count, 0
	copy(c.window[:], input[len(input)-64:])
	return -1
}
