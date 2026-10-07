package crypto

// WITNESS (review of S019-merge, area backup / start-up): the hand-written parallel scrypt
// in parallel_scrypt.go versus golang.org/x/crypto/scrypt (what stock restic calls).
//
// TestWitnessParallelScryptParity: for parameter sets stock accepts (N a power of two > 1,
//   r >= 1, p >= 1) the derived bytes are identical. PASS expected.
// TestWitnessKDFNonPowerOfTwoN: key files carry N, r, p. simple-scrypt's Params.Check only
//   requires N to be even, so N = 6, 12, 20 ... reach the KDF. Stock then fails cleanly in
//   scrypt.Key ("scrypt: N must be > 1 and a power of 2"). The candidate's parallel path
//   (taken when p >= 2 and GOMAXPROCS >= 2) has no such check: it either panics with an
//   index out of range inside a worker goroutine (process crash, cannot be recovered by the
//   caller) or silently derives a non-scrypt key. The test runs the candidate in a child
//   process because the panic is in a goroutine.
//
// OS: any. Run from the candidate tree:
//   go test -run 'TestWitness' -count=1 -p 2 ./internal/repository/crypto

import (
	"bytes"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"

	"golang.org/x/crypto/scrypt"
)

func TestWitnessParallelScryptParity(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	orig := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(orig)
	n := 0
	for _, procs := range []int{1, 2, 3, 4, 8} {
		runtime.GOMAXPROCS(procs)
		for _, N := range []int{2, 4, 8, 16, 32, 1024, 4096, 16384} {
			for _, r := range []int{1, 2, 3, 8, 9} {
				for _, p := range []int{1, 2, 3, 4, 5, 7, 9} {
					if N*r > 16384*8 || (N >= 4096 && p > 5) {
						continue
					}
					pw := make([]byte, rnd.Intn(40))
					_, _ = rnd.Read(pw)
					salt := make([]byte, saltLength)
					_, _ = rnd.Read(salt)
					want, err := scrypt.Key(pw, salt, N, r, p, macKeySize+aesKeySize)
					if err != nil {
						t.Fatalf("stock scrypt N=%d r=%d p=%d: %v", N, r, p, err)
					}
					got, err := parallelScryptKey(pw, salt, N, r, p, macKeySize+aesKeySize)
					if err != nil {
						t.Fatalf("candidate scrypt N=%d r=%d p=%d: %v", N, r, p, err)
					}
					if !bytes.Equal(want, got) {
						t.Errorf("KEY MISMATCH procs=%d N=%d r=%d p=%d", procs, N, r, p)
					}
					n++
				}
			}
		}
	}
	// the default parameters and a calibrate-like set with large p
	runtime.GOMAXPROCS(4)
	for _, c := range []Params{DefaultKDFParams, {N: 32768, R: 8, P: 7}, {N: 65536, R: 8, P: 2}} {
		salt := bytes.Repeat([]byte{7}, saltLength)
		want, _ := scrypt.Key([]byte("pw"), salt, c.N, c.R, c.P, macKeySize+aesKeySize)
		got, err := parallelScryptKey([]byte("pw"), salt, c.N, c.R, c.P, macKeySize+aesKeySize)
		if err != nil || !bytes.Equal(want, got) {
			t.Errorf("KEY MISMATCH %+v err=%v", c, err)
		}
		n++
	}
	t.Logf("%d parameter sets compared", n)
}

func TestWitnessKDFNonPowerOfTwoN(t *testing.T) {
	if os.Getenv("WITNESS_KDF_CHILD") != "" {
		var N, R, P int
		_, _ = fmt.Sscanf(os.Getenv("WITNESS_KDF_CHILD"), "%d,%d,%d", &N, &R, &P)
		runtime.GOMAXPROCS(4)
		_, err := KDF(Params{N: N, R: R, P: P}, bytes.Repeat([]byte{1}, saltLength), "password")
		fmt.Printf("CHILD-RESULT err=%v\n", err)
		return
	}
	salt := bytes.Repeat([]byte{1}, saltLength)
	for _, c := range []Params{{N: 6, R: 8, P: 2}, {N: 12, R: 8, P: 2}, {N: 20, R: 1, P: 4}, {N: 6, R: 8, P: 1}} {
		_, stockErr := scrypt.Key([]byte("password"), salt, c.N, c.R, c.P, macKeySize+aesKeySize)
		cmd := exec.Command(os.Args[0], "-test.run", "^TestWitnessKDFNonPowerOfTwoN$")
		cmd.Env = append(os.Environ(), fmt.Sprintf("WITNESS_KDF_CHILD=%d,%d,%d", c.N, c.R, c.P))
		out, runErr := cmd.CombinedOutput()
		text := string(out)
		verdict := "same as stock (clean error)"
		switch {
		case strings.Contains(text, "panic:"):
			verdict = "CANDIDATE PANICS (process crash)"
			first := text[strings.Index(text, "panic:"):]
			if i := strings.IndexByte(first, '\n'); i > 0 {
				first = first[:i]
			}
			verdict += ": " + first
		case strings.Contains(text, "CHILD-RESULT err=<nil>"):
			verdict = "CANDIDATE DERIVES A KEY from parameters stock rejects"
		}
		t.Logf("N=%d r=%d p=%d: stock scrypt.Key error = %v ; candidate KDF: %s (child exit: %v)", c.N, c.R, c.P, stockErr, verdict, runErr)
		if stockErr == nil {
			t.Fatalf("test assumption broken: stock accepted N=%d", c.N)
		}
		if verdict != "same as stock (clean error)" {
			t.Errorf("DIVERGENCE for N=%d r=%d p=%d: %s", c.N, c.R, c.P, verdict)
		}
	}
}
