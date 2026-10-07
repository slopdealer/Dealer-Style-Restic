package crypto

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"fmt"
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
	if !poly1305Verify(ct, nonce, &k.MACKey, mac) {
		return nil, ErrUnauthenticated
	}
	c, err := aes.NewCipher(k.EncryptionKey[:])
	if err != nil {
		panic(fmt.Sprintf("unable to create cipher: %v", err))
	}
	return &cipher.StreamReader{S: cipher.NewCTR(c, nonce), R: bytes.NewReader(ct)}, nil
}
