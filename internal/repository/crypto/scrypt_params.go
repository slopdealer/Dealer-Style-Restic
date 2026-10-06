package crypto

import "errors"

const maxInt = int(^uint(0) >> 1)

// checkScryptParams repeats the parameter checks of scrypt.Key in
// golang.org/x/crypto/scrypt: the same conditions, in the same order, with the
// same error texts. parallelScryptKey runs it before its own lanes, which do
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
