package crypto

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestOpenReaderMatchesOpenAndPreservesCiphertext(t *testing.T) {
	k := NewRandomKey()
	nonce := NewRandomNonce()
	for _, size := range []int{0, 1, 15, 16, 17, 32769, 4 << 20} {
		data := bytes.Repeat([]byte{0x5a}, size)
		ct := k.Seal(nil, nonce, data, nil)
		before := bytes.Clone(ct)
		for _, step := range []int{1, 7, 4096, 32768} {
			rd, err := k.OpenReader(nonce, ct)
			if err != nil {
				t.Fatal(err)
			}
			var out bytes.Buffer
			if _, err := io.CopyBuffer(struct{ io.Writer }{&out}, rd, make([]byte, step)); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), data) || !bytes.Equal(before, ct) {
				t.Fatalf("size %d step %d: plaintext mismatch or modified ciphertext", size, step)
			}
		}
	}
}

func TestOpenReaderAuthenticationBeforeConsumption(t *testing.T) {
	k := NewRandomKey()
	nonce := NewRandomNonce()
	ct := k.Seal(nil, nonce, bytes.Repeat([]byte{42}, 1<<20), nil)
	for _, pos := range []int{0, len(ct) / 2, len(ct) - 1} {
		bad := bytes.Clone(ct)
		bad[pos] ^= 1
		before := bytes.Clone(bad)
		rd, err := k.OpenReader(nonce, bad)
		if err != ErrUnauthenticated || rd != nil || !bytes.Equal(before, bad) {
			t.Fatalf("position %d: unauthenticated plaintext escaped or input changed: %v", pos, err)
		}
	}
	for _, tc := range []struct {
		key       *Key
		nonce, ct []byte
	}{
		{&Key{}, nonce, ct}, {k, make([]byte, ivSize), ct}, {k, nonce, ct[:15]},
	} {
		_, want := tc.key.Open(nil, tc.nonce, tc.ct, nil)
		got, err := tc.key.OpenReader(tc.nonce, tc.ct)
		if got != nil || err == nil || !strings.EqualFold(err.Error(), want.Error()) {
			t.Fatalf("error parity: got %v want %v", err, want)
		}
	}
}
