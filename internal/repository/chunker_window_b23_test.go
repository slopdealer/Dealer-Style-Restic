package repository

import (
	"math/rand"
	"testing"

	"github.com/restic/chunker"
)

// A segment starts from the preceding 64 actual bytes, not the chunk's
// artificial initial byte. Derive the expected digest by polynomial division;
// this does not use either scanner's reduction or outgoing-byte tables.
func TestB23RollingWindowIndependence(t *testing.T) {
	for _, pol := range []chunker.Pol{0x101, 0x10001, 0x3da3358b4dc173, 0x2482734cacca49, 0x20000000000001} {
		rng := rand.New(rand.NewSource(int64(pol)))
		data := make([]byte, 1024)
		_, _ = rng.Read(data)
		f := newExactChunkerFactory(&chunkerFactory{pol: pol}).(*exactChunkerFactory)
		for _, seed := range []uint64{0, 1, uint64(pol) - 1} {
			digest := uint64(chunker.Pol(seed).Mod(pol))
			var win [64]byte
			// Earlier bytes can be arbitrary; their fingerprint and window must agree.
			// Start with zero history and append the chosen seed as eight bytes.
			digest = 0
			pos := 0
			for shift := 56; shift >= 0; shift -= 8 {
				b := byte(seed >> uint(shift))
				win[pos] = b
				pos++
				digest = uint64(chunker.Pol(digest<<8 | uint64(b)).Mod(pol))
			}
			for i, b := range data {
				old := win[pos%64]
				win[pos%64] = b
				pos++
				digest ^= f.out[old]
				digest = (digest<<8 | uint64(b)) ^ f.mod[digest>>f.shift]
				if i >= 63 {
					var want chunker.Pol
					for _, x := range data[i-63 : i+1] {
						want = (want<<8 | chunker.Pol(x)).Mod(pol)
					}
					if digest != uint64(want) {
						t.Fatalf("pol=%x seed=%x offset=%d digest=%x want=%x", pol, seed, i, digest, want)
					}
				}
			}
		}
	}
}
