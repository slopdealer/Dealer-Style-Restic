package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"sync/atomic"

	"golang.org/x/crypto/poly1305"
)

// Seal, Open and OpenReader each need two AES key schedules: the one of the
// encryption key for the CTR stream and the one of the MAC key, which
// encrypts the nonce for Poly1305-AES. Stock expands both keys for every
// call, that is for every blob. A Key that has been prepared keeps the two
// expanded keys and uses them again.
//
// What is reused is only the result of aes.NewCipher(key), which is a pure
// function of the key bytes. The CTR stream, the Poly1305 key and every MAC
// are computed for each call as before, from the nonce of that call.
//
// A prepared pair is used only if its key bytes equal the Key's key bytes at
// the moment of the call: both keys are compared on every use. A Key whose
// fields were changed after it was prepared, or a copy of a Key that was
// changed, therefore never runs with the schedules of other key bytes; it
// gets a new pair. A Key that was never prepared (a Key built by hand or
// decoded from JSON without a call of Prepare) expands its keys for every
// call, exactly as stock.
//
// An aes cipher.Block holds the expanded key and nothing else; Encrypt reads
// it and does not write to it, so one Block serves all goroutines. The pair
// is published through an atomic pointer and never modified afterwards.
type cipherPreparation struct {
	current atomic.Pointer[preparedCipherPair]
}

type preparedCipherPair struct {
	encryptionKey EncryptionKey
	macKey        MACKey
	encryption    cipher.Block
	mac           cipher.Block
}

// Prepare lets k keep its expanded keys between calls. It must be called
// before k is used by more than one goroutine, like any other initialisation
// of k. Copies of k made afterwards share the kept pair.
func (k *Key) Prepare() {
	if k.preparation == nil {
		k.preparation = &cipherPreparation{}
	}
}

// preparedCiphers returns the two AES blocks for k's current key bytes.
func (k *Key) preparedCiphers() *preparedCipherPair {
	if k.preparation != nil {
		p := k.preparation.current.Load()
		if p != nil && p.encryptionKey == k.EncryptionKey && p.macKey == k.MACKey {
			return p
		}
	}
	p := &preparedCipherPair{encryptionKey: k.EncryptionKey, macKey: k.MACKey}
	var err error
	p.encryption, err = aes.NewCipher(p.encryptionKey[:])
	if err != nil {
		panic("unable to create cipher: " + err.Error())
	}
	p.mac, err = aes.NewCipher(p.macKey.K[:])
	if err != nil {
		panic(err)
	}
	if k.preparation != nil {
		k.preparation.current.Store(p)
	}
	return p
}

// polyKey is poly1305PrepareKey with the kept block of the MAC key: r||AES_k(n).
func (p *preparedCipherPair) polyKey(nonce []byte) [32]byte {
	var key [32]byte
	p.mac.Encrypt(key[16:], nonce)
	copy(key[:16], p.macKey.R[:])
	return key
}

// sum is poly1305MAC with the kept block of the MAC key.
func (p *preparedCipherPair) sum(msg, nonce []byte) []byte {
	key := p.polyKey(nonce)
	var tag [16]byte
	poly1305.Sum(&tag, msg, &key)
	return tag[:]
}

// verify is poly1305Verify with the kept block of the MAC key.
func (p *preparedCipherPair) verify(msg, nonce, mac []byte) bool {
	key := p.polyKey(nonce)
	var tag [16]byte
	copy(tag[:], mac)
	return poly1305.Verify(&tag, msg, &key)
}
