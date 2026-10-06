package archiver

import (
	"sync"

	"github.com/restic/restic/internal/restic"
)

// cutProbe counts what a parentCutReader does with its predictions. The
// reader itself keeps no counters; the probe derives them from the calls the
// reader makes to its two chunkers:
//
//   - scheduled: validations started, one scan of the additional chunker each;
//   - accepted: bytes of predicted chunks whose cut was confirmed;
//   - replayed: bytes of predictions that were not confirmed and are scanned
//     again.
//
// The reader resets the additional chunker right before every validation and
// at no other time. The next scan of the ordinary chunker is then the scan of
// the first predicted chunk, and the scan of the additional chunker is the
// scan of the second.
type cutProbe struct {
	mu                            sync.Mutex
	scheduled, accepted, replayed uint64

	armed       bool
	first, a    int
	second, b   int
	haveFirst   bool
	haveSecond  bool
	ordinaryFor restic.ChunkerFactory
}

// b18Guided is a reader together with its probe.
type b18Guided struct {
	*parentCutReader
	*cutProbe
}

// b18Probed attaches a probe to g. The ordinary chunker that the test hands
// to the reader must come from ordinary().
func b18Probed(g *parentCutReader) *b18Guided {
	p := &cutProbe{ordinaryFor: g.factory}
	g.factory = cutProbeFactory{g.factory, p}
	return &b18Guided{g, p}
}

// ordinary wraps the chunker of the ordinary read path.
func (p *cutProbe) ordinary(c restic.Chunker) restic.Chunker {
	return &cutProbeOrdinary{c, p}
}

// newOrdinary returns a probed ordinary chunker from the reader's own factory.
func (p *cutProbe) newOrdinary() restic.Chunker {
	return p.ordinary(p.ordinaryFor.NewChunker())
}

func (p *cutProbe) settle() {
	if !p.haveFirst || !p.haveSecond {
		return
	}
	p.haveFirst, p.haveSecond = false, false
	if p.first != p.a {
		p.replayed += uint64(p.a + p.b)
		return
	}
	p.accepted += uint64(p.a)
	if p.second == p.b {
		p.accepted += uint64(p.b)
	} else {
		p.replayed += uint64(p.b)
	}
}

type cutProbeFactory struct {
	restic.ChunkerFactory
	p *cutProbe
}

func (f cutProbeFactory) NewChunker() restic.Chunker {
	return &cutProbeValidator{f.ChunkerFactory.NewChunker(), f.p}
}

type cutProbeValidator struct {
	restic.Chunker
	p *cutProbe
}

func (c *cutProbeValidator) Reset() {
	c.Chunker.Reset()
	c.p.mu.Lock()
	c.p.armed, c.p.haveFirst, c.p.haveSecond = true, false, false
	c.p.mu.Unlock()
}

func (c *cutProbeValidator) NextSplitPoint(buf []byte) int {
	n := c.Chunker.NextSplitPoint(buf)
	c.p.mu.Lock()
	c.p.scheduled++
	c.p.second, c.p.b, c.p.haveSecond = n, len(buf), true
	c.p.settle()
	c.p.mu.Unlock()
	return n
}

type cutProbeOrdinary struct {
	restic.Chunker
	p *cutProbe
}

func (c *cutProbeOrdinary) NextSplitPoint(buf []byte) int {
	n := c.Chunker.NextSplitPoint(buf)
	c.p.mu.Lock()
	if c.p.armed {
		c.p.armed = false
		c.p.first, c.p.a, c.p.haveFirst = n, len(buf), true
		c.p.settle()
	}
	c.p.mu.Unlock()
	return n
}
