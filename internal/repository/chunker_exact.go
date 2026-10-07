// Rabin scan recurrence adapted from github.com/restic/chunker v0.5.0.
/*
Copyright (c) 2014, Alexander Neumann <alexander@bumpern.de>
All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are met:

1. Redistributions of source code must retain the above copyright notice, this
   list of conditions and the following disclaimer.

2. Redistributions in binary form must reproduce the above copyright notice,
   this list of conditions and the following disclaimer in the documentation
   and/or other materials provided with the distribution.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS "AS IS" AND
ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT LIMITED TO, THE IMPLIED
WARRANTIES OF MERCHANTABILITY AND FITNESS FOR A PARTICULAR PURPOSE ARE
DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT HOLDER OR CONTRIBUTORS BE LIABLE
FOR ANY DIRECT, INDIRECT, INCIDENTAL, SPECIAL, EXEMPLARY, OR CONSEQUENTIAL
DAMAGES (INCLUDING, BUT NOT LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR
SERVICES; LOSS OF USE, DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER
CAUSED AND ON ANY THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY,
OR TORT (INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.
*/

package repository

import (
	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

// exactChunkerFactory shares only immutable polynomial tables. Readers and
// parent validators each own a window, digest and counters. No input is retained.
// Unlike BaseChunker's automatic reset, a cut never locks or copies tables.
type exactChunkerFactory struct {
	*chunkerFactory
	out, mod [256]uint64
	shift    uint
}

func newExactChunkerFactory(f *chunkerFactory) restic.ChunkerFactory {
	degree := f.pol.Deg()
	// Retain the dependency's construction and panic behavior outside the range
	// in which its scanner supports rolling computation, including pol == 0.
	if degree < 8 || degree > 53 {
		return f
	}
	c := &exactChunkerFactory{chunkerFactory: f, shift: uint(degree - 8)}
	for b := range 256 {
		h := chunker.Pol(b).Mod(f.pol)
		for range 63 {
			h = (h << 8).Mod(f.pol)
		}
		c.out[b] = uint64(h)
		high := chunker.Pol(b) << uint(degree)
		c.mod[b] = uint64(high.Mod(f.pol) | high)
	}
	return c
}

func (f *exactChunkerFactory) NewChunker() restic.Chunker {
	c := &exactChunker{config: f}
	c.Reset()
	return c
}

type exactChunker struct {
	config           *exactChunkerFactory
	window           [64]byte
	digest           uint64
	pos, count, skip uint
}

func (c *exactChunker) Reset() {
	clear(c.window[:])
	// BaseChunker primes the zero window by sliding in 1 before any input.
	c.window[0] = 1
	c.digest = 1
	c.pos = 1
	c.count = 0
	c.skip = chunker.MinSize - 64
}

func (c *exactChunker) NextSplitPoint(buf []byte) int {
	idx := 0
	if c.skip > 0 {
		n := min(c.skip, uint(len(buf)))
		c.skip -= n
		c.count += n
		if n == uint(len(buf)) {
			return -1
		}
		idx = int(n)
		buf = buf[n:]
	}
	f := c.config
	shift := f.shift
	// Keep the scanner bound explicit so Go can omit wide-shift guards.
	if shift > 45 {
		panic("the polynomial must have a degree less than or equal 53")
	}
	digest, win, pos, count := c.digest, c.window, c.pos, c.count
	for i, b := range buf {
		old := win[pos%64]
		win[pos%64] = b
		pos++
		digest ^= f.out[old]
		high := digest >> shift
		digest = (digest<<8 | uint64(b)) ^ f.mod[high]
		count++
		if (digest&((1<<20)-1) == 0 || count >= chunker.MaxSize) && count >= chunker.MinSize {
			c.Reset()
			return idx + i + 1
		}
	}
	c.digest, c.window, c.pos, c.count = digest, win, pos%64, count
	return -1
}
