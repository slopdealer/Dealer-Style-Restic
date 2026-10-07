package repository

import (
	"bytes"
	"math/rand"
	"testing"
)

// encodeBlob must return the bytes of EncodeAll(data, nil) for every input
// and every compression mode, and from presizedEncodeMinSize on the encoder
// must never outgrow the destination it was given.
func TestEncodeBlobEqualsEncodeAll(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261007))
	random := func(n int) []byte {
		buf := make([]byte, n)
		rnd.Read(buf)
		return buf
	}
	const KiB, MiB = 1 << 10, 1 << 20

	type input struct {
		name string
		data []byte
	}
	var inputs []input
	for _, c := range autoCompressionCases() {
		inputs = append(inputs, input{c.name, c.data})
	}
	// sizes around every threshold involved: the smallest raw blob, the
	// encoder's own 1 MiB limit for an up-front buffer, its 128 KiB block
	// size, and the largest chunk
	for _, n := range []int{1, 100, 128 * KiB, 128*KiB + 1, 512*KiB - 1, 512 * KiB, 512*KiB + 1, MiB - 1, MiB, MiB + 1, 3*MiB + 12345, 8 * MiB} {
		inputs = append(inputs,
			input{"random", random(n)},
			input{"zero", make([]byte, n)},
			input{"half random, half zero", append(random(n/2), make([]byte, n-n/2)...)})
	}

	for _, mode := range []CompressionMode{CompressionAuto, CompressionMax, CompressionFastest, CompressionBetter} {
		repo, _, _ := TestRepositoryWithVersion(t, 2)
		repo.opts.Compression = mode
		enc := repo.getZstdEncoder()
		presized := 0
		for _, in := range inputs {
			want := enc.EncodeAll(in.data, nil)
			got := repo.encodeBlob(in.data)
			if !bytes.Equal(want, got) {
				t.Fatalf("mode %v, %s, %d bytes: encodeBlob returns %d bytes that differ from EncodeAll's %d",
					mode, in.name, len(in.data), len(got), len(want))
			}
			if len(in.data) >= presizedEncodeMinSize {
				presized++
				if limit := enc.MaxEncodedSize(len(in.data)); cap(got) != limit {
					t.Fatalf("mode %v, %s, %d bytes: the destination was reallocated (capacity %d, allocated %d, output %d)",
						mode, in.name, len(in.data), cap(got), limit, len(got))
				}
			}
		}
		if presized < 30 {
			t.Fatalf("mode %v: only %d inputs took the presized destination", mode, presized)
		}
	}
}
