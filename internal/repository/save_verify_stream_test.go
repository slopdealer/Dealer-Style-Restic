package repository

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
)

func TestRawSaveVerificationStreamSettlement(t *testing.T) {
	r := TestRepository(t)
	for _, size := range []int{streamSaveVerifyMinSize - 33, streamSaveVerifyMinSize - 32, streamSaveVerifyMinSize, 4 << 20} {
		data := bytes.Repeat([]byte{0x8a}, size)
		nonce := crypto.NewRandomNonce()
		buf := r.key.Seal(bytes.Clone(nonce), nonce, data, nil)
		original := bytes.Clone(buf)
		if err := r.verifyCiphertext(buf, 0, restic.Hash(data)); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(buf, original) {
			t.Fatal("verification changed pack input")
		}
		if err := r.verifyCiphertext(buf, 0, restic.ID{}); err == nil || err.Error() != "hash mismatch" {
			t.Fatalf("wrong content accepted: %v", err)
		}
		buf[len(buf)-1] ^= 1
		if err := r.verifyCiphertext(buf, 0, restic.ID{}); err == nil || !strings.Contains(err.Error(), "decryption failed") {
			t.Fatalf("authentication did not win: %v", err)
		}
	}
}

func TestRawSaveVerificationStreamConcurrentRecycling(t *testing.T) {
	r := TestRepository(t)
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			data := bytes.Repeat([]byte{byte(worker)}, 512<<10)
			for range 8 {
				nonce := crypto.NewRandomNonce()
				buf := r.key.Seal(bytes.Clone(nonce), nonce, data, nil)
				if err := r.verifyCiphertext(buf, 0, restic.Hash(data)); err != nil {
					t.Error(fmt.Errorf("worker %d: %w", worker, err))
				}
				// Verification is synchronous: the caller may recycle input now.
				clear(buf)
			}
		}()
	}
	wg.Wait()
}
