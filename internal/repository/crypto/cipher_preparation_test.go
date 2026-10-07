package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"io"
	"math/rand"
	"sync"
	"testing"
)

// sealStock and openStock are the bodies of the Seal and Open methods of the
// unchanged restic, without their parameter checks: both AES keys are
// expanded for the call.
func sealStock(k *Key, nonce, plaintext []byte) []byte {
	out := make([]byte, len(plaintext)+macSize)
	c, err := aes.NewCipher(k.EncryptionKey[:])
	if err != nil {
		panic(err)
	}
	cipher.NewCTR(c, nonce).XORKeyStream(out, plaintext)
	copy(out[len(plaintext):], poly1305MAC(out[:len(plaintext)], nonce, &k.MACKey))
	return out
}

func openStock(k *Key, nonce, ciphertext []byte) ([]byte, error) {
	l := len(ciphertext) - macSize
	ct, mac := ciphertext[:l], ciphertext[l:]
	if !poly1305Verify(ct, nonce, &k.MACKey, mac) {
		return nil, ErrUnauthenticated
	}
	out := make([]byte, len(ct))
	c, err := aes.NewCipher(k.EncryptionKey[:])
	if err != nil {
		panic(err)
	}
	cipher.NewCTR(c, nonce).XORKeyStream(out, ct)
	return out, nil
}

// randomTestKey returns a valid key with fresh random bytes, with or without
// a holder for its expanded keys.
func randomTestKey(prepared bool) *Key {
	r := NewRandomKey()
	k := &Key{MACKey: r.MACKey, EncryptionKey: r.EncryptionKey}
	if prepared {
		k.Prepare()
	}
	return k
}

func randomTestNonce(rnd *rand.Rand) []byte {
	nonce := make([]byte, ivSize)
	for !validNonce(nonce) {
		rnd.Read(nonce)
	}
	return nonce
}

// checkKeyAgainstStock seals and opens with k and compares every byte and
// every verdict with the unchanged code for the key bytes k holds now.
func checkKeyAgainstStock(t testing.TB, rnd *rand.Rand, k *Key, sizes []int) {
	t.Helper()
	for _, size := range sizes {
		nonce := randomTestNonce(rnd)
		plaintext := make([]byte, size)
		rnd.Read(plaintext)

		want := sealStock(k, nonce, plaintext)
		got := k.Seal(nil, nonce, plaintext, nil)
		if !bytes.Equal(got, want) {
			t.Fatalf("%d bytes: Seal differs from the unchanged ciphertext", size)
		}

		opened, err := k.Open(nil, nonce, want, nil)
		if err != nil || !bytes.Equal(opened, plaintext) {
			t.Fatalf("%d bytes: Open of the unchanged ciphertext: %v", size, err)
		}
		rd, err := k.OpenReader(nonce, want)
		if err != nil {
			t.Fatalf("%d bytes: OpenReader of the unchanged ciphertext: %v", size, err)
		}
		streamed, err := io.ReadAll(rd)
		if err != nil || !bytes.Equal(streamed, plaintext) {
			t.Fatalf("%d bytes: OpenReader returns other bytes: %v", size, err)
		}

		// one flipped bit anywhere, in the ciphertext, the MAC or the nonce:
		// the unchanged code rejects it, so it must be rejected here
		for range 4 {
			damaged := bytes.Clone(want)
			badNonce := bytes.Clone(nonce)
			if rnd.Intn(4) == 0 {
				badNonce[rnd.Intn(len(badNonce))] ^= 1 << uint(rnd.Intn(8))
				if !validNonce(badNonce) {
					continue
				}
			} else {
				damaged[rnd.Intn(len(damaged))] ^= 1 << uint(rnd.Intn(8))
			}
			if _, err := openStock(k, badNonce, damaged); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("the unchanged code accepts a damaged ciphertext: the test does not test what it should")
			}
			if _, err := k.Open(nil, badNonce, damaged, nil); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("%d bytes: Open accepts a damaged ciphertext: %v", size, err)
			}
			if _, err := k.OpenReader(badNonce, damaged); !errors.Is(err, ErrUnauthenticated) {
				t.Fatalf("%d bytes: OpenReader accepts a damaged ciphertext: %v", size, err)
			}
		}
	}
}

var preparationTestSizes = []int{0, 1, 15, 16, 17, 31, 32, 33, 255, 4096, 65537, 1 << 20}

// A prepared key, an unprepared key and a key from NewRandomKey produce the
// unchanged bytes and the unchanged verdicts.
func TestPreparedKeyEqualsStock(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261007))
	for i := 0; i < 40; i++ {
		checkKeyAgainstStock(t, rnd, randomTestKey(true), preparationTestSizes)
		checkKeyAgainstStock(t, rnd, randomTestKey(false), preparationTestSizes)
	}
	checkKeyAgainstStock(t, rnd, NewRandomKey(), preparationTestSizes)
}

// The kept pair is really reused, and it is never used for other key bytes:
// after any change of the key, and in a changed copy, the output is the
// unchanged code's for the bytes the Key holds at that moment.
func TestPreparedKeyFollowsItsKeyBytes(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	sizes := []int{0, 1, 100, 5000}

	k := randomTestKey(true)
	checkKeyAgainstStock(t, rnd, k, sizes)
	first := k.preparation.current.Load()
	checkKeyAgainstStock(t, rnd, k, sizes)
	if first == nil || k.preparation.current.Load() != first {
		t.Fatal("a prepared key expanded its keys again without a change of the key")
	}

	// a copy shares the pair as long as it holds the same bytes
	clone := *k
	checkKeyAgainstStock(t, rnd, &clone, sizes)
	if clone.preparation.current.Load() != first {
		t.Fatal("an unchanged copy did not reuse the pair")
	}

	changes := 0
	for i := 0; i < 300; i++ {
		target := k
		if rnd.Intn(2) == 0 {
			target = &clone
		}
		before := *target
		switch rnd.Intn(4) {
		case 0:
			target.EncryptionKey[rnd.Intn(len(target.EncryptionKey))] ^= byte(1 + rnd.Intn(255))
		case 1:
			target.MACKey.K[rnd.Intn(len(target.MACKey.K))] ^= byte(1 + rnd.Intn(255))
		case 2:
			target.MACKey.R[rnd.Intn(len(target.MACKey.R))] ^= byte(1 + rnd.Intn(255))
		case 3:
			// no change: only alternate between the two keys
		}
		if !target.Valid() {
			// not a key restic would use: take the change back
			target.MACKey, target.EncryptionKey = before.MACKey, before.EncryptionKey
		} else if target.MACKey != before.MACKey || target.EncryptionKey != before.EncryptionKey {
			changes++
		}
		checkKeyAgainstStock(t, rnd, target, sizes)
	}
	if changes < 50 {
		t.Fatalf("only %d key changes were exercised", changes)
	}

	// a key that was never prepared keeps nothing
	plain := randomTestKey(false)
	checkKeyAgainstStock(t, rnd, plain, sizes)
	if plain.preparation != nil {
		t.Fatal("an unprepared key got a holder")
	}
}

// One prepared key used by many goroutines at once (to be run with -race
// where a race detector is available).
func TestPreparedKeyConcurrentUse(t *testing.T) {
	k := NewRandomKey()
	var wg sync.WaitGroup
	failures := make(chan string, 16)
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rnd := rand.New(rand.NewSource(seed))
			for i := 0; i < 400; i++ {
				nonce := randomTestNonce(rnd)
				plaintext := make([]byte, rnd.Intn(3000))
				rnd.Read(plaintext)
				sealed := k.Seal(nil, nonce, plaintext, nil)
				if !bytes.Equal(sealed, sealStock(k, nonce, plaintext)) {
					failures <- "Seal differs from the unchanged code under concurrent use"
					return
				}
				opened, err := k.Open(nil, nonce, sealed, nil)
				if err != nil || !bytes.Equal(opened, plaintext) {
					failures <- "Open fails under concurrent use"
					return
				}
			}
		}(int64(g))
	}
	wg.Wait()
	close(failures)
	for msg := range failures {
		t.Error(msg)
	}
}

func BenchmarkSealOpenSmallBlob(b *testing.B) {
	rnd := rand.New(rand.NewSource(1))
	plaintext := make([]byte, 300)
	rnd.Read(plaintext)
	nonce := randomTestNonce(rnd)
	for _, prepared := range []bool{false, true} {
		name := "unprepared"
		if prepared {
			name = "prepared"
		}
		b.Run(name, func(b *testing.B) {
			k := randomTestKey(prepared)
			buf := make([]byte, 0, len(plaintext)+macSize)
			out := make([]byte, 0, len(plaintext))
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				sealed := k.Seal(buf[:0], nonce, plaintext, nil)
				if _, err := k.Open(out[:0], nonce, sealed, nil); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
