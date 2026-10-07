package crypto

import (
	"bytes"
	"runtime"
	"testing"
	"time"

	"golang.org/x/crypto/scrypt"
)

func TestCalibrate(t *testing.T) {
	params, err := Calibrate(25*time.Millisecond, 50)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("testing calibrate, params after: %v", params)
}

func TestParallelScryptKeyMatchesXCrypto(t *testing.T) {
	password := []byte("restic KDF parity")
	salt := bytes.Repeat([]byte{0x5a}, saltLength)
	originalProcs := runtime.GOMAXPROCS(0)
	defer runtime.GOMAXPROCS(originalProcs)

	for _, params := range []struct {
		N, r, p, procs int
	}{
		{N: 2, r: 1, p: 2, procs: 2},
		{N: 4, r: 1, p: 4, procs: 4},
		{N: 64, r: 2, p: 5, procs: 4},
		{N: 64, r: 1, p: 3, procs: 2},
		{N: 16, r: 1, p: 1, procs: 4},
		{N: 16, r: 1, p: 8, procs: 1},
		{N: DefaultKDFParams.N, r: DefaultKDFParams.R, p: DefaultKDFParams.P, procs: 4},
	} {
		runtime.GOMAXPROCS(params.procs)
		want, err := scrypt.Key(password, salt, params.N, params.r, params.p, macKeySize+aesKeySize)
		if err != nil {
			t.Fatalf("scrypt.Key(%+v): %v", params, err)
		}
		got, err := parallelScryptKey(password, salt, params.N, params.r, params.p, macKeySize+aesKeySize)
		if err != nil {
			t.Fatalf("parallelScryptKey(%+v): %v", params, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("parallelScryptKey(%+v) differs from scrypt.Key", params)
		}
	}
}

func TestKDFRejectsInvalidScryptParams(t *testing.T) {
	salt := bytes.Repeat([]byte{0x5a}, saltLength)
	for _, params := range []Params{
		{N: 1, R: 1, P: 1},
		{N: 16, R: 0, P: 1},
		{N: 16, R: 1, P: 0},
	} {
		if _, err := KDF(params, salt, "password"); err == nil {
			t.Errorf("KDF(%+v) succeeded, want parameter validation error", params)
		}
	}
}
