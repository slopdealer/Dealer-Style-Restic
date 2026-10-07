package repository

import (
	"crypto/sha256"
	"fmt"
	"io"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/restic"
)

const streamSaveVerifyMinSize = 256 << 10

// verifyRawCiphertextStream removes the full verification plaintext allocation
// for large raw blobs. Authentication settles before hashing starts; hashing
// consumes the whole stream before pack submission. Ciphertext remains intact
// for Packer.Add, and upload admission and completion are unaffected.
func (r *Repository) verifyRawCiphertextStream(buf []byte, id restic.ID) error {
	nonce, ciphertext := buf[:r.key.NonceSize()], buf[r.key.NonceSize():]
	rd, err := r.key.OpenReader(nonce, ciphertext)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, rd); err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	var sum [sha256.Size]byte
	var got restic.ID
	copy(got[:], h.Sum(sum[:0]))
	if !got.Equal(id) {
		return errors.New("hash mismatch")
	}
	return nil
}
