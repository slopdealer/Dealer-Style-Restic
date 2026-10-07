package crypto

import (
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/binary"
	"runtime"
	"sync"

	"golang.org/x/crypto/scrypt"
)

// parallelScryptLanes derives the same output as scrypt.Key while processing
// independent scrypt lanes concurrently. It stores every fourth ROMix state
// and recomputes the intervening states during the second ROMix pass.
//
// It does not validate N, r and p, and its lanes index past their tables for
// values scrypt.Key rejects. Its only caller is parallelScryptKey in
// scrypt_params.go, which runs scrypt.Key's parameter checks first.
func parallelScryptLanes(password, salt []byte, N, r, p, keyLen int) ([]byte, error) {
	const stride = 4
	workers := min(4, p, runtime.GOMAXPROCS(0))
	if workers <= 1 {
		return scrypt.Key(password, salt, N, r, p, keyLen)
	}
	k := stride
	if N < k {
		k = 1
	}

	b, err := pbkdf2.Key(sha256.New, string(password), salt, 1, p*128*r)
	if err != nil {
		return nil, err
	}

	lanes := make(chan int, p)
	for i := 0; i < p; i++ {
		lanes <- i
	}
	close(lanes)

	var wg sync.WaitGroup
	for g := 0; g < workers && g < p; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := make([]uint32, 32*r*(N/k))
			for i := range lanes {
				parallelSmix(b[i*128*r:(i+1)*128*r], r, N, k, v)
			}
		}()
	}
	wg.Wait()

	return pbkdf2.Key(sha256.New, string(password), b, 1, keyLen)
}

func parallelSmix(b []byte, r, N, k int, v []uint32) {
	var tmp [16]uint32
	words := 32 * r
	x := make([]uint32, words)
	y := make([]uint32, words)
	t := make([]uint32, words)
	t2 := make([]uint32, words)
	for i := range x {
		x[i] = binary.LittleEndian.Uint32(b[4*i:])
	}
	for i := 0; i < N; i++ {
		if i%k == 0 {
			copy(v[(i/k)*words:], x)
		}
		parallelBlockMix(&tmp, x, y, r)
		x, y = y, x
	}
	jWord := (2*r - 1) * 16
	for i := 0; i < N; i++ {
		j := int((uint64(x[jWord]) | uint64(x[jWord+1])<<32) & uint64(N-1))
		copy(t, v[(j/k)*words:(j/k)*words+words])
		for step := 0; step < j%k; step++ {
			parallelBlockMix(&tmp, t, t2, r)
			t, t2 = t2, t
		}
		for q := range x {
			x[q] ^= t[q]
		}
		parallelBlockMix(&tmp, x, y, r)
		x, y = y, x
	}
	for i, word := range x[:words] {
		binary.LittleEndian.PutUint32(b[4*i:], word)
	}
}

func parallelBlockMix(tmp *[16]uint32, in, out []uint32, r int) {
	copy(tmp[:], in[(2*r-1)*16:])
	for i := 0; i < 2*r; i += 2 {
		parallelSalsaXOR(tmp, in[i*16:], out[i*8:])
		parallelSalsaXOR(tmp, in[i*16+16:], out[i*8+r*16:])
	}
}

func parallelSalsaXOR(tmp *[16]uint32, in, out []uint32) {
	w0, w1, w2, w3 := tmp[0]^in[0], tmp[1]^in[1], tmp[2]^in[2], tmp[3]^in[3]
	w4, w5, w6, w7 := tmp[4]^in[4], tmp[5]^in[5], tmp[6]^in[6], tmp[7]^in[7]
	w8, w9, w10, w11 := tmp[8]^in[8], tmp[9]^in[9], tmp[10]^in[10], tmp[11]^in[11]
	w12, w13, w14, w15 := tmp[12]^in[12], tmp[13]^in[13], tmp[14]^in[14], tmp[15]^in[15]
	x0, x1, x2, x3, x4, x5, x6, x7 := w0, w1, w2, w3, w4, w5, w6, w7
	x8, x9, x10, x11, x12, x13, x14, x15 := w8, w9, w10, w11, w12, w13, w14, w15
	for i := 0; i < 8; i += 2 {
		x4 ^= parallelRotL(x0+x12, 7)
		x8 ^= parallelRotL(x4+x0, 9)
		x12 ^= parallelRotL(x8+x4, 13)
		x0 ^= parallelRotL(x12+x8, 18)
		x9 ^= parallelRotL(x5+x1, 7)
		x13 ^= parallelRotL(x9+x5, 9)
		x1 ^= parallelRotL(x13+x9, 13)
		x5 ^= parallelRotL(x1+x13, 18)
		x14 ^= parallelRotL(x10+x6, 7)
		x2 ^= parallelRotL(x14+x10, 9)
		x6 ^= parallelRotL(x2+x14, 13)
		x10 ^= parallelRotL(x6+x2, 18)
		x3 ^= parallelRotL(x15+x11, 7)
		x7 ^= parallelRotL(x3+x15, 9)
		x11 ^= parallelRotL(x7+x3, 13)
		x15 ^= parallelRotL(x11+x7, 18)
		x1 ^= parallelRotL(x0+x3, 7)
		x2 ^= parallelRotL(x1+x0, 9)
		x3 ^= parallelRotL(x2+x1, 13)
		x0 ^= parallelRotL(x3+x2, 18)
		x6 ^= parallelRotL(x5+x4, 7)
		x7 ^= parallelRotL(x6+x5, 9)
		x4 ^= parallelRotL(x7+x6, 13)
		x5 ^= parallelRotL(x4+x7, 18)
		x11 ^= parallelRotL(x10+x9, 7)
		x8 ^= parallelRotL(x11+x10, 9)
		x9 ^= parallelRotL(x8+x11, 13)
		x10 ^= parallelRotL(x9+x8, 18)
		x12 ^= parallelRotL(x15+x14, 7)
		x13 ^= parallelRotL(x12+x15, 9)
		x14 ^= parallelRotL(x13+x12, 13)
		x15 ^= parallelRotL(x14+x13, 18)
	}
	x0 += w0
	x1 += w1
	x2 += w2
	x3 += w3
	x4 += w4
	x5 += w5
	x6 += w6
	x7 += w7
	x8 += w8
	x9 += w9
	x10 += w10
	x11 += w11
	x12 += w12
	x13 += w13
	x14 += w14
	x15 += w15
	out[0], tmp[0] = x0, x0
	out[1], tmp[1] = x1, x1
	out[2], tmp[2] = x2, x2
	out[3], tmp[3] = x3, x3
	out[4], tmp[4] = x4, x4
	out[5], tmp[5] = x5, x5
	out[6], tmp[6] = x6, x6
	out[7], tmp[7] = x7, x7
	out[8], tmp[8] = x8, x8
	out[9], tmp[9] = x9, x9
	out[10], tmp[10] = x10, x10
	out[11], tmp[11] = x11, x11
	out[12], tmp[12] = x12, x12
	out[13], tmp[13] = x13, x13
	out[14], tmp[14] = x14, x14
	out[15], tmp[15] = x15, x15
}

func parallelRotL(x uint32, n uint) uint32 { return x<<n | x>>(32-n) }
