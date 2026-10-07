package crypto

import (
	"bytes"
	"crypto/cipher"
	"io"

	"github.com/restic/restic/internal/errors"
)

// OpenReader authenticates the complete ciphertext before exposing plaintext.
// The reader borrows immutable ciphertext until EOF; it neither overwrites it
// nor allocates a full plaintext copy. The caller must finish reading before
// recycling the input. Like Open, this does not support additional data.
func (k *Key) OpenReader(nonce, ciphertext []byte) (io.Reader, error) {
	if !k.Valid() {
		return nil, errors.New("invalid key")
	}
	if len(nonce) != ivSize {
		panic("incorrect nonce length")
	}
	if !validNonce(nonce) {
		return nil, errors.New("nonce is invalid")
	}
	if len(ciphertext) < k.Overhead() {
		return nil, errors.Errorf("trying to decrypt invalid data: ciphertext too short")
	}
	l := len(ciphertext) - macSize
	ct, mac := ciphertext[:l], ciphertext[l:]
	prepared := k.preparedCiphers()
	if !prepared.verify(ct, nonce, mac) {
		return nil, ErrUnauthenticated
	}
	return &cipher.StreamReader{S: cipher.NewCTR(prepared.encryption, nonce), R: bytes.NewReader(ct)}, nil
}
