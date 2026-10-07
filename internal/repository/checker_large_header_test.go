package repository

import (
	"bytes"
	"encoding/binary"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"

	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestLargeCheckHeaderSettlement(t *testing.T) {
	key := crypto.NewRandomKey()
	var out bytes.Buffer
	p := pack.NewPacker(key, &out)
	encoder, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer encoder.Close()
	for i := range 2000 {
		plain := binary.LittleEndian.AppendUint32(nil, uint32(i))
		nonce := crypto.NewRandomNonce()
		payload, uncompressed := plain, 0
		if i%2 == 1 {
			payload, uncompressed = encoder.EncodeAll(plain, nil), len(plain)
		}
		cipher := key.Seal(append([]byte(nil), nonce...), nonce, payload, nil)
		if _, err := p.Add(restic.DataBlob, restic.Hash(plain), cipher, uncompressed); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Finalize(); err != nil {
		t.Fatal(err)
	}
	data := out.Bytes()
	plan, _, err := pack.List(key, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	hlen := int(binary.LittleEndian.Uint32(data[len(data)-4:]))
	if hlen+4 < largeCheckHeaderThreshold {
		t.Fatal("fixture does not reach large-header route")
	}
	compare := func(input []byte, id restic.ID, readPlan, stored pack.Blobs, mode, want string) string {
		t.Helper()
		saved := bytes.Clone(input)
		old, oc := b11OwnedCheckerCall(key, input, id, readPlan, stored, mode, true)
		got, gc := b11OwnedCheckerCall(key, input, id, readPlan, stored, mode, false)
		if got != old || gc != oc || !bytes.Equal(saved, input) || (want != "" && !strings.Contains(got, want)) {
			t.Fatalf("mode=%s: %q/%q calls=%d/%d mutated=%v", mode, old, got, oc, gc, !bytes.Equal(saved, input))
		}
		return got
	}
	for _, mode := range []string{"", "two-full-callbacks", "full-then-transport-error", "cancel-during-tail"} {
		compare(data, restic.Hash(data), plan, plan, mode, "")
	}
	// A late malformed entry must outrank all earlier membership diagnostics.
	bad := bytes.Clone(data)
	start := len(bad) - hlen - 4
	header := bad[start : len(bad)-4]
	plain, err := key.Open(nil, header[:key.NonceSize()], header[key.NonceSize():], nil)
	if err != nil {
		t.Fatal(err)
	}
	plain[len(plain)-pack.CalculateEntrySize(plan[len(plan)-1].IsCompressed())] = 255
	nonce := crypto.NewRandomNonce()
	copy(header, key.Seal(append([]byte(nil), nonce...), nonce, plain, nil))
	got := compare(bad, restic.Hash(bad), plan, nil, "", "invalid type 255")
	if strings.Contains(got, "is not contained in index") {
		t.Fatal("malformed late entry published earlier membership diagnostics")
	}
	wrongID := restic.Hash(bad)
	wrongID[0] ^= 1
	compare(bad, wrongID, plan, nil, "", "unexpected pack id")
	bad = bytes.Clone(data)
	bad[len(bad)-5] ^= 1
	compare(bad, restic.Hash(bad), plan, plan, "", "")
	altered := append(pack.Blobs(nil), plan...)
	altered[0].UncompressedLength = 1
	got = compare(data, restic.Hash(data), altered, nil, "", "pack header size does not match")
	if !strings.Contains(got, "blob ") || strings.Count(got, "is not contained in index") != len(plan) {
		t.Fatal("competing errors omitted")
	}
	t.Log("matched eight large-header transitions: success, repeated callback, late backend error, cancellation, late malformed entry, pack-hash priority and authentication failure")
}

func TestLargeCheckHeaderDispatchBoundary(t *testing.T) {
	key := crypto.NewRandomKey()
	var out bytes.Buffer
	p := pack.NewPacker(key, &out)
	for i := range 1770 {
		plain := binary.LittleEndian.AppendUint32(nil, uint32(i))
		nonce := crypto.NewRandomNonce()
		cipher := key.Seal(append([]byte(nil), nonce...), nonce, plain, nil)
		if _, err := p.Add(restic.DataBlob, restic.Hash(plain), cipher, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Finalize(); err != nil {
		t.Fatal(err)
	}
	base := out.Bytes()
	plan, _, err := pack.List(key, bytes.NewReader(base), int64(len(base)))
	if err != nil {
		t.Fatal(err)
	}
	headerLen := int(binary.LittleEndian.Uint32(base[len(base)-4:])) + 4
	start := len(base) - headerLen
	for _, tailLen := range []int{largeCheckHeaderThreshold - 1, largeCheckHeaderThreshold, largeCheckHeaderThreshold + 1} {
		data := bytes.Clone(base[:start])
		data = append(data, make([]byte, tailLen-headerLen)...)
		data = append(data, base[start:]...)
		id := restic.Hash(data)
		old, oc := b11OwnedCheckerCall(key, data, id, plan, plan, "", true)
		got, gc := b11OwnedCheckerCall(key, data, id, plan, plan, "", false)
		if old != got || oc != gc || got != "" {
			t.Fatalf("tail=%d: %q/%q attempts=%d/%d", tailLen, old, got, oc, gc)
		}
	}
	t.Log("matched dispatch below, at and above 64KiB with owned padding before the authenticated header")
}
