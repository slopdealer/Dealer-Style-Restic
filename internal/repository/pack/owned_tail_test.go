package pack

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
)

func b11OwnedEncrypt(key *crypto.Key, plain []byte, prefix int) []byte {
	nonce := crypto.NewRandomNonce()
	out := key.Seal(append([]byte(nil), nonce...), nonce, plain, nil)
	out = binary.LittleEndian.AppendUint32(out, uint32(len(out)))
	return append(make([]byte, prefix), out...)
}
func b11OwnedPlain(n int) []byte {
	out := make([]byte, 0, n*int(entrySize))
	for i := range n {
		typ := byte(i % 4)
		out = append(out, typ)
		length := uint32(i + 1)
		if i%11 == 0 {
			length = ^uint32(0)
		}
		if i%13 == 0 {
			length = 0
		}
		out = binary.LittleEndian.AppendUint32(out, length)
		if typ >= 2 {
			out = binary.LittleEndian.AppendUint32(out, ^uint32(0))
		}
		id := restic.Hash([]byte{byte(i % 7)})
		out = append(out, id[:]...)
	}
	return out
}
func b11OwnedError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T:%v", err, err)
}
func b11OwnedCompare(key *crypto.Key, tail []byte) error {
	authoritative := bytes.Clone(tail)
	want, ws, we := List(key, bytes.NewReader(authoritative), int64(len(authoritative)))
	owned := bytes.Clone(tail)
	got, gs, ge := ListOwnedTail(key, owned)
	if b11OwnedError(we) != b11OwnedError(ge) || ws != gs || !reflect.DeepEqual(want, got) {
		return fmt.Errorf("owned differs: sizes %d/%d errors %v/%v entries %d/%d", ws, gs, we, ge, len(want), len(got))
	}
	if !bytes.Equal(authoritative, tail) {
		return fmt.Errorf("authoritative input mutated")
	}
	if ge == nil {
		saved := append(make(Blobs, 0, len(got)), got...)
		for i := range owned {
			owned[i] = 0
		}
		if !reflect.DeepEqual(saved, got) {
			return fmt.Errorf("entries borrow tail")
		}
		again, as, ae := List(key, bytes.NewReader(authoritative), int64(len(authoritative)))
		if ae != nil || as != ws || !reflect.DeepEqual(again, want) {
			return fmt.Errorf("subsequent authoritative parse changed")
		}
	}
	return nil
}
func b11OwnedTailCases() (int, error) {
	key := crypto.NewRandomKey()
	cases := 0
	check := func(b []byte) error { cases++; return b11OwnedCompare(key, b) }
	for _, n := range []int{0, 1, 14, 15, 16, 1024, (MaxHeaderSize-headerSize)/int(entrySize) - 1, 1} {
		if err := check(b11OwnedEncrypt(key, b11OwnedPlain(n), 71)); err != nil {
			return cases, err
		}
	}
	for _, typ := range []byte{2, 3, 4, 255} {
		for _, length := range []int{0, 1, 4, 5, 36, 37, 38, 40, 41} {
			plain := append(b11OwnedPlain(1), make([]byte, length)...)
			if length > 0 {
				plain[len(plain)-length] = typ
			}
			if err := check(b11OwnedEncrypt(key, plain, 71)); err != nil {
				return cases, err
			}
		}
	}
	for _, size := range []int{0, 3, 4, 35, 72, 73} {
		if err := check(make([]byte, size)); err != nil {
			return cases, err
		}
	}
	for _, hlen := range []uint32{0, 1, 31, 32, 37, 70, 1024, ^uint32(0)} {
		b := make([]byte, 128)
		binary.LittleEndian.PutUint32(b[len(b)-4:], hlen)
		if err := check(b); err != nil {
			return cases, err
		}
	}
	b := make([]byte, MaxHeaderSize+1)
	binary.LittleEndian.PutUint32(b[len(b)-4:], MaxHeaderSize-3)
	if err := check(b); err != nil {
		return cases, err
	}
	auth := b11OwnedEncrypt(key, b11OwnedPlain(2), 71)
	auth[len(auth)-5] ^= 1
	if err := check(auth); err != nil {
		return cases, err
	}
	// Exact cumulative uint offsets cross 32 bits. Duplicate IDs remain ordered.
	return cases, nil
}

func TestOwnedTailContract(t *testing.T) {
	n, err := b11OwnedTailCases()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("executed %d differential cases including near-limit then ordinary", n)
}
