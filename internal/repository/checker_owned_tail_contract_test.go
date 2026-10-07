package repository

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type b11OwnedRepeatBackend struct {
	backend.Backend
	data   []byte
	mode   string
	calls  int
	cancel context.CancelFunc
}

func (b *b11OwnedRepeatBackend) Load(_ context.Context, _ backend.Handle, _ int, _ int64, consume func(io.Reader) error) error {
	b.calls++
	if b.mode == "two-full-callbacks" {
		if err := consume(bytes.NewReader(b.data)); err != nil {
			return err
		}
		return consume(bytes.NewReader(b.data))
	}
	if b.mode == "full-then-transport-error" && b.calls == 1 {
		if err := consume(bytes.NewReader(b.data)); err != nil {
			return err
		}
		return fmt.Errorf("transport failed after complete callback")
	}
	if b.mode == "cancel-during-tail" {
		return consume(&b11OwnedCancelReader{data: b.data, at: 0, cancel: b.cancel, tailStart: len(b.data) - int(binary.LittleEndian.Uint32(b.data[len(b.data)-4:])) - 4})
	}
	return consume(bytes.NewReader(b.data))
}

type b11OwnedCancelReader struct {
	data          []byte
	at, tailStart int
	cancel        context.CancelFunc
}

func (r *b11OwnedCancelReader) Read(dst []byte) (int, error) {
	if r.at == len(r.data) {
		return 0, io.EOF
	}
	if len(dst) > 16 {
		dst = dst[:16]
	}
	n := copy(dst, r.data[r.at:])
	r.at += n
	if r.at > r.tailStart && r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return n, nil
}
func b11OwnedCheckerCall(key *crypto.Key, data []byte, id restic.ID, plan, stored pack.Blobs, mode string, old bool) (string, int) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	be := &b11OwnedRepeatBackend{data: data, mode: mode, cancel: cancel}
	r := b11Repository(key, be, id, stored)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		panic(err)
	}
	defer dec.Close()
	rd := bufio.NewReaderSize(nil, maxStreamBufferSize)
	if old {
		err = b11BaselineCheckPack(ctx, r, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec)
	} else {
		err = checkPack(ctx, r, id, append(pack.Blobs(nil), plan...), int64(len(data)), rd, dec)
	}
	if err == nil {
		return "", be.calls
	}
	return fmt.Sprintf("%T:%v", err, err), be.calls
}
func b11OwnedCheckerCases() (int, error) {
	key := crypto.NewRandomKey()
	cases := 0
	for _, mode := range []string{"two-full-callbacks", "full-then-transport-error", "cancel-during-tail"} {
		data, plan, id := b11MakePack(key, 4096, "compressed")
		saved := bytes.Clone(data)
		want, wc := b11OwnedCheckerCall(key, data, id, plan, plan, mode, true)
		got, gc := b11OwnedCheckerCall(key, data, id, plan, plan, mode, false)
		cases++
		if want != got || wc != gc || !bytes.Equal(saved, data) {
			return cases, fmt.Errorf("%s differs: %s/%s calls %d/%d source_mutated=%v", mode, want, got, wc, gc, !bytes.Equal(saved, data))
		}
	}
	var out bytes.Buffer
	p := pack.NewPacker(key, &out)
	for _, plain := range [][]byte{[]byte("first"), []byte("second")} {
		nonce := crypto.NewRandomNonce()
		ct := key.Seal(append([]byte(nil), nonce...), nonce, plain, nil)
		if _, err := p.Add(restic.DataBlob, restic.Hash(plain), ct, 0); err != nil {
			return cases, err
		}
	}
	if err := p.Finalize(); err != nil {
		return cases, err
	}
	data := bytes.Clone(out.Bytes())
	plan, _, err := pack.List(key, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return cases, err
	}
	n := int(binary.LittleEndian.Uint32(data[len(data)-4:]))
	start := len(data) - n - 4
	ct := data[start : len(data)-4]
	plain, err := key.Open(nil, ct[:key.NonceSize()], ct[key.NonceSize():], nil)
	if err != nil {
		return cases, err
	}
	plain[pack.CalculateEntrySize(false)] = 255
	nonce := crypto.NewRandomNonce()
	changed := key.Seal(append([]byte(nil), nonce...), nonce, plain, nil)
	copy(ct, changed)
	id := restic.Hash(data)
	saved := bytes.Clone(data)
	want, _ := b11OwnedCheckerCall(key, data, id, plan, nil, "", true)
	got, _ := b11OwnedCheckerCall(key, data, id, plan, nil, "", false)
	cases++
	if want != got || !strings.Contains(got, "invalid type 255") || strings.Contains(got, "is not contained in index") || !bytes.Equal(saved, data) {
		return cases, fmt.Errorf("late malformed header changed precedence: %s/%s", want, got)
	}
	// Hash failure still takes priority over that authenticated malformed header.
	wrongID := id
	wrongID[0] ^= 1
	want, _ = b11OwnedCheckerCall(key, data, wrongID, plan, nil, "", true)
	got, _ = b11OwnedCheckerCall(key, data, wrongID, plan, nil, "", false)
	cases++
	if want != got || !strings.Contains(got, "unexpected pack id") || strings.Contains(got, "invalid type") {
		return cases, fmt.Errorf("pack-hash priority changed: %s/%s", want, got)
	}
	return cases, nil
}
func TestOwnedCheckerContract(t *testing.T) {
	n, err := b11OwnedCheckerCases()
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("executed %d ownership/settlement cases", n)
}
func b11PrintOwnedCheckerDiagnostic() {
	n, err := b11OwnedCheckerCases()
	if err != nil {
		panic(err)
	}
	b, _ := json.Marshal(map[string]any{"label": "B11_OWNED_CHECKER_M004", "cases": n, "two_full_callbacks": true, "retry_after_successful_callback": true, "cancel_during_tail_read": true, "late_header_precedence": true, "pack_hash_precedence": true, "authoritative_bytes_unchanged": true})
	fmt.Fprintln(os.Stderr, string(b))
}
