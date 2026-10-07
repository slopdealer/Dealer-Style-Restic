package repository

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Keep the verification plan fixed while varying the actual index membership.
// Authentication and whole-header interpretation must settle before this query.
func membershipCheckerResult(t *testing.T, key *crypto.Key, data []byte, id restic.ID, plan, stored pack.Blobs, baseline bool) string {
	t.Helper()
	r := b11Repository(key, &b11ScriptBackend{data: data}, id, stored)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	rd := bufio.NewReaderSize(nil, maxStreamBufferSize)
	if baseline {
		err = b11BaselineCheckPack(context.Background(), r, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec)
	} else {
		err = checkPack(context.Background(), r, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec)
	}
	if err == nil {
		return ""
	}
	var pe *ErrPackData
	if !errors.As(err, &pe) {
		t.Fatalf("classification changed: %v", err)
	}
	return err.Error()
}

func TestMembershipCheckerLocationSettlement(t *testing.T) {
	key := crypto.NewRandomKey()
	data, blobs, id := b11MakePack(key, 4096, "compressed")
	for _, mode := range []string{"missing", "offset", "length", "uncompressed-length", "type", "id", "duplicate-match"} {
		t.Run(mode, func(t *testing.T) {
			b := blobs[0]
			switch mode {
			case "offset":
				b.Offset++
			case "length":
				b.Length++
			case "uncompressed-length":
				b.UncompressedLength++
			case "type":
				b.Type = restic.TreeBlob
			case "id":
				b.ID[0] ^= 1
			}
			stored := pack.Blobs{b}
			if mode == "missing" {
				stored = nil
			}
			if mode == "duplicate-match" {
				wrong := b
				wrong.Offset++
				stored = pack.Blobs{wrong, b, wrong, b}
			}
			want := membershipCheckerResult(t, key, data, id, blobs, stored, true)
			got := membershipCheckerResult(t, key, data, id, blobs, stored, false)
			if got != want {
				t.Fatalf("got %q want %q", got, want)
			}
			if mode == "duplicate-match" {
				if got != "" {
					t.Fatal(got)
				}
			} else if !strings.Contains(got, "is not contained in index or position is incorrect") {
				t.Fatalf("wrong membership diagnostic: %s", got)
			}
		})
	}
}

func TestMembershipMalformedLateHeaderPrecedence(t *testing.T) {
	key := crypto.NewRandomKey()
	var out bytes.Buffer
	p := pack.NewPacker(key, &out)
	for _, plain := range [][]byte{[]byte("first blob"), []byte("second blob")} {
		nonce := crypto.NewRandomNonce()
		ciphertext := key.Seal(append([]byte(nil), nonce...), nonce, plain, nil)
		if _, err := p.Add(restic.DataBlob, restic.Hash(plain), ciphertext, 0); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Finalize(); err != nil {
		t.Fatal(err)
	}
	data := bytes.Clone(out.Bytes())
	plan, _, err := pack.List(key, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	n := int(binary.LittleEndian.Uint32(data[len(data)-4:]))
	start := len(data) - 4 - n
	cipher := data[start : len(data)-4]
	header, err := key.Open(nil, cipher[:key.NonceSize()], cipher[key.NonceSize():], nil)
	if err != nil {
		t.Fatal(err)
	}
	// First entry is valid and absent from the index. Second is authenticated but
	// invalid. Stock reports only the complete-header error, no membership error.
	header[pack.CalculateEntrySize(false)] = 255
	nonce := crypto.NewRandomNonce()
	changed := key.Seal(append([]byte(nil), nonce...), nonce, header, nil)
	copy(data[start:len(data)-4], changed)
	id := restic.Hash(data)
	want := membershipCheckerResult(t, key, data, id, plan, nil, true)
	got := membershipCheckerResult(t, key, data, id, plan, nil, false)
	if got != want || !strings.Contains(got, "invalid type 255") || strings.Contains(got, "is not contained in index") {
		t.Fatalf("late header precedence changed: got %q want %q", got, want)
	}
	t.Log("executed authenticated late-header failure with earlier missing membership")
}
