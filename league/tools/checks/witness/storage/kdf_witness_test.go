package crypto

// WITNESS (storage review, mechanism "parallel scrypt").
// OS: any (pure Go). Needs GOMAXPROCS >= 2, which the tests set themselves.
//
// What it shows:
//  1. TestWitnessParallelScryptDifferential: for valid parameters the hand-rolled
//     parallelScryptKey equals golang.org/x/crypto/scrypt.Key (expected: PASS).
//  2. TestWitnessKDFInvalidN12NoError: stock rejects N that is not a power of two
//     ("scrypt: N must be > 1 and a power of 2"); simple-scrypt's Params.Check only
//     rejects ODD N, so N=12 reaches the hand-rolled lanes, which derive a key and
//     return NO error (expected on candidate: FAIL = behaviour differs from stock).
//  3. TestWitnessKDFInvalidN6Panics: with N=6 the lane code indexes past its stride
//     table and the process dies with an unrecoverable panic in a worker goroutine
//     (expected on candidate: the test binary crashes; stock returns an error).
//     Run it alone: go test -run TestWitnessKDFInvalidN6Panics ./internal/repository/crypto

import (
	"bytes"
	"math/rand"
	"runtime"
	"testing"

	"golang.org/x/crypto/scrypt"
)

func TestWitnessParallelScryptDifferential(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	rnd := rand.New(rand.NewSource(20261006))
	for i := 0; i < 60; i++ {
		N := 1 << (1 + rnd.Intn(11)) // 2 .. 2048
		r := 1 + rnd.Intn(8)
		p := 2 + rnd.Intn(7)
		password := make([]byte, rnd.Intn(40))
		rnd.Read(password)
		salt := make([]byte, saltLength)
		rnd.Read(salt)
		want, err := scrypt.Key(password, salt, N, r, p, macKeySize+aesKeySize)
		if err != nil {
			t.Fatalf("scrypt.Key(N=%d r=%d p=%d): %v", N, r, p, err)
		}
		got, err := parallelScryptKey(password, salt, N, r, p, macKeySize+aesKeySize)
		if err != nil {
			t.Fatalf("parallelScryptKey(N=%d r=%d p=%d): %v", N, r, p, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("N=%d r=%d p=%d: parallelScryptKey differs from x/crypto scrypt", N, r, p)
		}
	}
}

func TestWitnessKDFInvalidN12NoError(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	salt := bytes.Repeat([]byte{0x5a}, saltLength)
	_, stockErr := scrypt.Key([]byte("pw"), salt, 12, 8, 2, macKeySize+aesKeySize)
	if stockErr == nil {
		t.Fatal("x/crypto accepted N=12; witness premise is wrong")
	}
	key, err := KDF(Params{N: 12, R: 8, P: 2}, salt, "pw")
	if err == nil {
		t.Fatalf("candidate KDF derived a key (valid=%v) from N=12,p=2 without error; stock fails with %q", key.Valid(), stockErr)
	}
}

func TestWitnessKDFInvalidN6Panics(t *testing.T) {
	old := runtime.GOMAXPROCS(4)
	defer runtime.GOMAXPROCS(old)
	salt := bytes.Repeat([]byte{0x5a}, saltLength)
	_, stockErr := scrypt.Key([]byte("pw"), salt, 6, 8, 2, macKeySize+aesKeySize)
	if stockErr == nil {
		t.Fatal("x/crypto accepted N=6; witness premise is wrong")
	}
	t.Logf("stock scrypt.Key error for N=6: %v", stockErr)
	// On the candidate this call crashes the process ("slice bounds out of range"
	// in parallelSmix, in a goroutine that the caller cannot recover).
	_, err := KDF(Params{N: 6, R: 8, P: 2}, salt, "pw")
	if err == nil {
		t.Fatal("candidate KDF derived a key from N=6,p=2 without error")
	}
}
