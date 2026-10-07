package crypto

import "errors"

const maxInt = int(^uint(0) >> 1)

// parallelScryptKey is the key derivation KDF calls: scrypt.Key's parameter
// checks, then the parallel lanes. Nothing else may call parallelScryptLanes.
//
// The checks live in this function, in a file of their own behind the lanes,
// and not at the top of parallelScryptLanes, because of where the code lands
// in the binary. The derivation's inner loop (parallelSalsaXOR) runs about 3%
// slower when it starts at 0 instead of 32 modulo 64, which is 6 ms in every
// restic process. Code placed in front of it moves it in steps of 32 bytes;
// the checks inside parallelScryptLanes were 17 such steps. That is a
// property of one build (linux/amd64, this toolchain), not of the checks.
func parallelScryptKey(password, salt []byte, N, r, p, keyLen int) ([]byte, error) {
	if err := checkScryptParams(N, r, p); err != nil {
		return nil, err
	}
	return parallelScryptLanes(password, salt, N, r, p, keyLen)
}

// checkScryptParams repeats the parameter checks of scrypt.Key in
// golang.org/x/crypto/scrypt: the same conditions, in the same order, with the
// same error texts. parallelScryptKey runs it before the lanes, which do
// not validate anything and index past their tables for other values. The
// check that KDF runs first (simple-scrypt) lets through an N that is even but
// not a power of two; a key file can carry any N.
func checkScryptParams(N, r, p int) error {
	if N <= 1 || N&(N-1) != 0 {
		return errors.New("scrypt: N must be > 1 and a power of 2")
	}
	if r <= 0 || p <= 0 {
		return errors.New("scrypt: parameters must be > 0")
	}
	if uint64(r)*uint64(p) >= 1<<30 || r > maxInt/128/p || r > maxInt/256 || N > maxInt/128/r {
		return errors.New("scrypt: parameters are too large")
	}
	return nil
}
