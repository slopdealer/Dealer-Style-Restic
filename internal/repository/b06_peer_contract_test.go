package repository

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type b06PeerLoader struct{ payload []byte }

func (l b06PeerLoader) LoadBlob(context.Context, restic.BlobHandle, []byte) ([]byte, error) {
	return l.payload, nil
}

type b06PeerError struct {
	Text, Type, Field, Struct string
	Syntax, Typed             int64
}

func b06PeerFacts(err error) b06PeerError {
	e := b06PeerError{Text: fmt.Sprint(err), Type: fmt.Sprintf("%T", err)}
	var syntax *json.SyntaxError
	if errors.As(err, &syntax) {
		e.Syntax = syntax.Offset
	}
	var typed *json.UnmarshalTypeError
	if errors.As(err, &typed) {
		e.Typed = typed.Offset
		e.Field = typed.Field
		e.Struct = typed.Struct
	}
	return e
}
func b06PeerParentCases(t testing.TB) {
	for i, tail := range []string{
		`{"name":"a\\\"},[]","mode":"bad","content":["bad"]}`,
		`{"user":"` + strings.Repeat("x", 32769) + `","uid":-1,"mtime":"bad"}`,
		`{"name":"b","name":"\\q","linktarget_raw":"bad!"}`,
		`{"content":["bad"],"name":"\\q"}`,
		`{"mode":false,"extended_attributes":[{"value":"bad!"}]}`,
		`{"generic_attributes":{"x":{"nodes":["}","]",","]}},"uid":"bad"}`,
		`{"name":"valid","user":"x","content":null}`,
		`null`, `false`, `{"name":"b",`,
	} {
		payload := []byte("{\"nodes\":[{\"name\":\"prefix\"},\r\n\t " + tail + "],\"after\":{\"x\":true}}")
		var outcomes [2][]*data.Node
		var failures [2]b06PeerError
		for side := range 2 {
			var it data.TreeNodeIterator
			var err error
			if side == 0 {
				it, err = data.NewTreeNodeIterator(bytes.NewReader(payload))
			} else {
				it, err = data.LoadParentTree(context.Background(), b06PeerLoader{payload}, restic.Hash(payload))
			}
			if err == nil {
				for item := range it {
					if item.Error != nil {
						err = item.Error
						break
					}
					outcomes[side] = append(outcomes[side], item.Node)
				}
			}
			failures[side] = b06PeerFacts(err)
		}
		if !reflect.DeepEqual(outcomes[0], outcomes[1]) || failures[0] != failures[1] {
			t.Fatalf("parent case%d differs: %+v / %+v", i, failures[0], failures[1])
		}
		fmt.Fprintf(os.Stderr, "B06-PEER-CASE parent-span=%d syntax=%d type=%d passed=true\n", i, failures[1].Syntax, failures[1].Typed)
	}
}

type b06PeerBackend struct {
	backend.Backend
	data, original []byte
	callbacks      int
	mode           string
	cancel         context.CancelFunc
}

func (b *b06PeerBackend) Load(ctx context.Context, _ backend.Handle, _ int, _ int64, consume func(io.Reader) error) error {
	for attempt := range 2 {
		b.callbacks++
		if err := consume(bytes.NewReader(b.data)); err != nil {
			return err
		}
		if !bytes.Equal(b.data, b.original) {
			return errors.New("borrowed backend bytes mutated in callback")
		}
		if attempt == 0 && b.mode == "cancel" {
			b.cancel()
		}
	}
	if b.mode == "transport" {
		return errors.New("authoritative transport after complete callbacks")
	}
	return nil
}
func b06PeerHeaderCases(t testing.TB) {
	key := crypto.NewRandomKey()
	for _, compressed := range []bool{false, true} {
		var out bytes.Buffer
		p := pack.NewPacker(key, &out)
		for _, plain := range [][]byte{bytes.Repeat([]byte("content-alpha-"), 4000), bytes.Repeat([]byte("content-beta-"), 3000)} {
			payload := plain
			u := 0
			if compressed {
				enc, err := zstd.NewWriter(nil)
				if err != nil {
					t.Fatal(err)
				}
				payload = enc.EncodeAll(plain, nil)
				enc.Close()
				u = len(plain)
			}
			nonce := crypto.NewRandomNonce()
			cipher := key.Seal(append([]byte(nil), nonce...), nonce, payload, nil)
			if _, err := p.Add(restic.DataBlob, restic.Hash(plain), cipher, u); err != nil {
				t.Fatal(err)
			}
		}
		if err := p.Finalize(); err != nil {
			t.Fatal(err)
		}
		original := bytes.Clone(out.Bytes())
		id := restic.Hash(original)
		blobs, _, err := pack.List(key, bytes.NewReader(original), int64(len(original)))
		if err != nil {
			t.Fatal(err)
		}
		for _, mode := range []string{"good", "transport", "cancel"} {
			ctx, cancel := context.WithCancel(context.Background())
			be := &b06PeerBackend{data: bytes.Clone(original), original: original, mode: mode, cancel: cancel}
			mi := index.NewMasterIndex()
			idx := index.NewIndex()
			idx.StorePack(id, blobs)
			mi.Insert(idx)
			r := &Repository{be: be, key: key, idx: mi}
			rd := bufio.NewReaderSize(nil, maxStreamBufferSize)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			err = checkPack(ctx, r, id, append(pack.Blobs(nil), blobs...), int64(len(original)), rd, dec)
			if mode == "good" && err != nil || mode == "transport" && (err == nil || !strings.Contains(err.Error(), "authoritative transport after complete callbacks")) || mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("header mode%s: %v", mode, err)
			}
			if !bytes.Equal(be.data, original) {
				t.Fatal("owned header modified borrowed authoritative input")
			}
			// A later authoritative attempt uses the same input and worker decoder.
			be.mode = "good"
			be.callbacks = 0
			if err := checkPack(context.Background(), r, id, append(pack.Blobs(nil), blobs...), int64(len(original)), rd, dec); err != nil || be.callbacks != 2 {
				t.Fatalf("worker/input reuse: %v callbacks=%d", err, be.callbacks)
			}
			dec.Close()
			cancel()
			fmt.Fprintf(os.Stderr, "B06-PEER-CASE header-compressed=%t mode=%s full-callbacks=2 reused-worker=true passed=true\n", compressed, mode)
		}
	}
}

func TestB06PeerParentSpans(t *testing.T)     { b06PeerParentCases(t) }
func TestB06PeerHeaderOwnership(t *testing.T) { b06PeerHeaderCases(t) }
