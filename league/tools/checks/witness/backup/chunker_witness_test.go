package repository

// WITNESS (review of S019-merge, area backup): chunk boundaries of the candidate's
// "exact" and "segmented" scanners versus stock restic's chunker.
//
// What it shows: for the same bytes and the same polynomial, every cut point returned
// by the candidate's restic.Chunker (repo.ChunkerFactory() -> segmentedChunker, and the
// exactChunker underneath it) equals the cut point of
//   (a) github.com/restic/chunker v0.5.0 BaseChunker.NextSplitPoint fed in 512 KiB tiles
//       (exactly what stock restic 0.19.1-dev does in internal/archiver/file_saver.go), and
//   (b) the streaming chunker.New(rd, pol).Next() API (an independent second reference).
// The candidate is fed the data in randomly sized pieces (1 byte .. 9 MiB), in exact
// chunk-length pieces (what parentCutReader does) and in chunk-length-minus-one pieces.
//
// OS: any (pure Go, no OS specific code). Run from the candidate tree:
//   go test -run 'TestWitnessChunker' -count=1 -p 2 ./internal/repository
//
// A failure here would be a BLOCKER: different cut points mean different blob IDs,
// i.e. no deduplication against repositories written by stock restic.

import (
	"bytes"
	"fmt"
	"io"
	"math/rand"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/restic"
)

func witnessFactory(pol chunker.Pol) *chunkerFactory {
	return &chunkerFactory{pol: pol, zeroChunk: func() restic.ID { return restic.ID{} }}
}

// stockCuts returns the absolute cut offsets stock restic produces: BaseChunker fed in
// 512 KiB tiles. The final partial chunk (EOF) is not a cut and is not reported.
func stockCuts(data []byte, pol chunker.Pol) []int {
	bc := chunker.NewBase(pol)
	var cuts []int
	const tile = 512 * 1024
	pos := 0
	for pos < len(data) {
		end := min(pos+tile, len(data))
		buf := data[pos:end]
		for len(buf) > 0 {
			n := bc.NextSplitPoint(buf)
			if n < 0 {
				break
			}
			cuts = append(cuts, end-len(buf)+n)
			buf = buf[n:]
		}
		pos = end
	}
	return cuts
}

// streamCuts uses the classic streaming API as a second, independent reference.
func streamCuts(t *testing.T, data []byte, pol chunker.Pol) []int {
	c := chunker.New(bytes.NewReader(data), pol)
	buf := make([]byte, chunker.MaxSize)
	var cuts []int
	off := 0
	for {
		chunk, err := c.Next(buf)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		off += int(chunk.Length)
		cuts = append(cuts, off)
	}
	// the streaming API reports the EOF tail as a chunk as well; drop it unless it is a real cut
	if len(cuts) > 0 && cuts[len(cuts)-1] == len(data) {
		cuts = cuts[:len(cuts)-1]
	}
	return cuts
}

// candidateCuts feeds data to c in pieces chosen by next() and returns absolute cuts.
func candidateCuts(c restic.Chunker, data []byte, next func(remaining int) int) []int {
	c.Reset()
	var cuts []int
	pos := 0
	for pos < len(data) {
		n := next(len(data) - pos)
		if n <= 0 {
			n = 1
		}
		n = min(n, len(data)-pos)
		buf := data[pos : pos+n]
		base := pos
		for len(buf) > 0 {
			s := c.NextSplitPoint(buf)
			if s < 0 {
				break
			}
			cuts = append(cuts, base+s)
			buf = buf[s:]
			base += s
		}
		pos += n
	}
	return cuts
}

func equalCuts(a, b []int) (int, bool) {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return i, false
		}
	}
	if len(a) != len(b) {
		return min(len(a), len(b)), false
	}
	return 0, true
}

type witnessInput struct {
	name string
	data []byte
}

func witnessInputs(rnd *rand.Rand) []witnessInput {
	random := func(n int) []byte {
		b := make([]byte, n)
		_, _ = rnd.Read(b)
		return b
	}
	var in []witnessInput
	in = append(in, witnessInput{"random-24MiB", random(24 << 20)})
	in = append(in, witnessInput{"zeros-6MiB+17", make([]byte, 6<<20+17)})
	in = append(in, witnessInput{"ff-17MiB", bytes.Repeat([]byte{0xff}, 17<<20)})
	for _, period := range []int{1, 2, 3, 63, 64, 65, 255, 256, 257, 511, 512, 513, 1000, 4096} {
		pat := random(period)
		in = append(in, witnessInput{fmt.Sprintf("periodic-%d-9MiB", period), bytes.Repeat(pat, (9<<20)/period+1)})
	}
	// small alphabet: many more near-misses of the split mask
	low := random(12 << 20)
	for i := range low {
		low[i] &= 0x03
	}
	in = append(in, witnessInput{"low-entropy-12MiB", low})
	// zero runs embedded in random data, at offsets around MinSize boundaries
	mixed := random(20 << 20)
	for _, at := range []int{0, chunker.MinSize - 64, chunker.MinSize - 1, chunker.MinSize, chunker.MinSize + 1, 3 << 20, 7<<20 + 123, 11<<20 - 5} {
		for i := at; i < at+chunker.MinSize+777 && i < len(mixed); i++ {
			mixed[i] = 0
		}
	}
	in = append(in, witnessInput{"random+zero-runs-20MiB", mixed})
	// short inputs around the interesting sizes
	for _, n := range []int{0, 1, 63, 64, 65, 1023, 1024, 1025, chunker.MinSize - 65, chunker.MinSize - 64, chunker.MinSize - 1, chunker.MinSize, chunker.MinSize + 1, chunker.MinSize + 63, chunker.MinSize + 64, chunker.MinSize + 65, chunker.MinSize + 1023, chunker.MinSize + 1024} {
		in = append(in, witnessInput{fmt.Sprintf("zeros-%d", n), make([]byte, n)})
		in = append(in, witnessInput{fmt.Sprintf("random-%d", n), random(n)})
	}
	return in
}

func TestWitnessChunkerCutsEqualStock(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261006))
	pols := []chunker.Pol{0x3DA3358B4DC173} // polynomial used by restic's own chunker tests
	for range 2 {
		p, err := chunker.RandomPolynomial()
		if err != nil {
			t.Fatal(err)
		}
		pols = append(pols, p)
	}
	inputs := witnessInputs(rnd)
	compared, totalCuts := 0, 0
	for _, pol := range pols {
		base := witnessFactory(pol)
		seg := newSegmentedChunkerFactory(base)
		if _, ok := seg.(*segmentedChunkerFactory); !ok {
			t.Fatalf("pol %x: ChunkerFactory did not return the segmented scanner (%T)", uint64(pol), seg)
		}
		exact := newExactChunkerFactory(base)
		for _, in := range inputs {
			want := stockCuts(in.data, pol)
			if ref2 := streamCuts(t, in.data, pol); len(in.data) > 0 {
				// the streaming API cannot tell a real cut at the very end from the EOF
				// tail, so a cut exactly at len(data) is ignored for this self-check only
				ref1 := want
				if len(ref1) > 0 && ref1[len(ref1)-1] == len(in.data) {
					ref1 = ref1[:len(ref1)-1]
				}
				if i, ok := equalCuts(ref1, ref2); !ok {
					t.Fatalf("reference self-check failed pol %x %s at cut %d", uint64(pol), in.name, i)
				}
			}
			totalCuts += len(want)
			feeds := map[string]func(int) int{
				"whole":    func(rem int) int { return rem },
				"tile512k": func(int) int { return 512 * 1024 },
				"1..1500":  func(int) int { return 1 + rnd.Intn(1500) },
				"1..70000": func(int) int { return 1 + rnd.Intn(70000) },
				"1..9MiB":  func(int) int { return 1 + rnd.Intn(9<<20) },
				"mixed": func(int) int {
					switch rnd.Intn(6) {
					case 0:
						return 1
					case 1:
						return 63 + rnd.Intn(3)
					case 2:
						return 1023 + rnd.Intn(3)
					case 3:
						return 64 + 512*rnd.Intn(8)
					case 4:
						return chunker.MinSize - 65 + rnd.Intn(131)
					default:
						return 1 + rnd.Intn(3<<20)
					}
				},
			}
			if len(in.data) > 4<<20 {
				// single-byte feeding of large inputs is slow and adds nothing over the small inputs
				delete(feeds, "1..1500")
			} else {
				feeds["bytewise"] = func(int) int { return 1 }
			}
			// exact chunk-length pieces: what parentCutReader feeds on a correct prediction,
			// and one byte short: a prediction that must NOT be accepted.
			lengths := make([]int, 0, len(want)+1)
			prev := 0
			for _, c := range want {
				lengths = append(lengths, c-prev)
				prev = c
			}
			i := 0
			feeds["chunk-lengths"] = func(rem int) int {
				if i < len(lengths) {
					i++
					return lengths[i-1]
				}
				return rem
			}
			j, short := 0, true
			feeds["chunk-lengths-minus-1-then-1"] = func(rem int) int {
				if j < len(lengths) {
					if short && lengths[j] > 1 {
						short = false
						return lengths[j] - 1
					}
					short = true
					j++
					if lengths[j-1] > 1 {
						return 1
					}
					return lengths[j-1]
				}
				return rem
			}
			for feedName, feed := range feeds {
				for implName, f := range map[string]restic.ChunkerFactory{"segmented": seg, "exact": exact} {
					got := candidateCuts(f.NewChunker(), in.data, feed)
					compared++
					if k, ok := equalCuts(want, got); !ok {
						var w, g int = -1, -1
						if k < len(want) {
							w = want[k]
						}
						if k < len(got) {
							g = got[k]
						}
						t.Errorf("CUT MISMATCH pol %x input %s feed %s impl %s: cut #%d stock=%d candidate=%d (stock %d cuts, candidate %d cuts)",
							uint64(pol), in.name, feedName, implName, k, w, g, len(want), len(got))
					}
				}
			}
		}
	}
	t.Logf("compared %d (polynomial,input,feed,impl) runs covering %d stock cut points", compared, totalCuts)
}

// A chunker that was abandoned mid-chunk (file read error, guided-reader replay) is
// Reset() and reused for the next file. Its cuts must equal a fresh stock chunker's.
func TestWitnessChunkerResetMidChunk(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	pol := chunker.Pol(0x3DA3358B4DC173)
	data := make([]byte, 6<<20)
	_, _ = rnd.Read(data)
	want := stockCuts(data, pol)
	for _, f := range []restic.ChunkerFactory{newSegmentedChunkerFactory(witnessFactory(pol)), newExactChunkerFactory(witnessFactory(pol))} {
		c := f.NewChunker()
		for range 200 {
			// dirty the state with a random prefix of unrelated bytes, in random pieces
			junk := make([]byte, rnd.Intn(2<<20))
			_, _ = rnd.Read(junk)
			for len(junk) > 0 {
				n := min(len(junk), 1+rnd.Intn(300000))
				c.NextSplitPoint(junk[:n])
				junk = junk[n:]
			}
			got := candidateCuts(c, data, func(int) int { return 1 + rnd.Intn(1<<20) })
			if k, ok := equalCuts(want, got); !ok {
				t.Fatalf("%T: after Reset mid-chunk cut #%d differs", c, k)
			}
		}
	}
}

// Many natural (mask) cuts: 192 MiB of random data per polynomial gives roughly 130 cuts
// each, landing at arbitrary positions relative to the segmented scanner's 2 x 256 byte
// segment pairs, its 64 byte window priming and its tail loop.
func TestWitnessChunkerDenseRandom(t *testing.T) {
	rnd := rand.New(rand.NewSource(424242))
	data := make([]byte, 192<<20)
	_, _ = rnd.Read(data)
	total := 0
	for _, pol := range []chunker.Pol{0x3DA3358B4DC173, 0x2482734CACCA49, 0x3AC2B3D5C1B7ED} {
		if !pol.Irreducible() {
			p, err := chunker.RandomPolynomial()
			if err != nil {
				t.Fatal(err)
			}
			pol = p
		}
		want := stockCuts(data, pol)
		total += len(want)
		seg := newSegmentedChunkerFactory(witnessFactory(pol))
		for name, feed := range map[string]func(int) int{
			"whole":    func(rem int) int { return rem },
			"tile512k": func(int) int { return 512 * 1024 },
			"1..9MiB":  func(int) int { return 1 + rnd.Intn(9<<20) },
			"1..5000":  func(int) int { return 1 + rnd.Intn(5000) },
			"odd":      func(int) int { return []int{1023, 1024, 1025, 64 + 511, 64 + 512, 64 + 513, 576, 577, 1088, 100003}[rnd.Intn(10)] },
		} {
			got := candidateCuts(seg.NewChunker(), data, feed)
			if k, ok := equalCuts(want, got); !ok {
				t.Errorf("CUT MISMATCH pol %x feed %s at cut #%d (stock %d cuts, candidate %d)", uint64(pol), name, k, len(want), len(got))
			}
		}
	}
	t.Logf("%d stock cut points on 3 polynomials x 192 MiB, 5 feed patterns each", total)
}
