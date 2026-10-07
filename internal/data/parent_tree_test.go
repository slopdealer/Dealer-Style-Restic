package data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/restic"
)

type parentCaseError struct {
	Text, Type, Field, Struct string
	SyntaxOffset, TypeOffset  int64
}

func parentCaseFacts(err error) parentCaseError {
	out := parentCaseError{Text: fmt.Sprint(err), Type: fmt.Sprintf("%T", err)}
	var se *json.SyntaxError
	if errors.As(err, &se) {
		out.SyntaxOffset = se.Offset
	}
	var te *json.UnmarshalTypeError
	if errors.As(err, &te) {
		out.TypeOffset, out.Field, out.Struct = te.Offset, te.Field, te.Struct
	}
	return out
}

func parentCaseRead(payload []byte, direct bool) ([]*Node, error) {
	var it TreeNodeIterator
	var err error
	if direct {
		it, err = newParentTreeIterator(payload)
	} else {
		it, err = NewTreeNodeIterator(bytes.NewReader(payload))
	}
	if err != nil {
		return nil, err
	}
	var nodes []*Node
	for item := range it {
		if item.Error != nil {
			return nodes, item.Error
		}
		nodes = append(nodes, item.Node)
	}
	return nodes, nil
}

func parentCaseFixtures() [][]byte {
	id := `"` + restic.Hash([]byte("id")).String() + `"`
	good := `{"name":"a","type":"file","mode":420,"mtime":"2024-05-06T07:08:09.123Z","uid":1000,"gid":1000,"content":[` + id + `]}`
	nodes := []string{good, `null`, `{}`, `{"name":"a","name":"b","content":[` + id + `],"content":null}`, `{"name":"a","linktarget":"fallback","linktarget_raw":"//4="}`, `{"name":"a","linktarget_raw":"","linktarget":"fallback"}`, `{"name":"a","generic_attributes":{"z": {"x":1,"x":2}},"extended_attributes":[{"name":"x","value":null}]}`, `{"name":"quote\\\""}`, `{"mode":"bad"}`, `{"name":"\\q"}`, `{"content":["bad"]}`, `{"mtime":"bad"}`, `{"uid":-1,"content":["bad"]}`, `{"uid":-1,"subtree":"bad"}`, `{"content":["bad"],"uid":-1}`, `{"extended_attributes":[{"value":"bad!"}]}`, `{"generic_attributes":[]}`, `{"subtree":null}`, `{"subtree":` + id + `}`, `false`, `123`, `"string"`, `{"Name":"case insensitive","MODE":420}`, `{"name":null,"mode":null,"mtime":null,"content":null}`, `{"user":false,"uid":"bad"}`, `{"name":"\ufffd","linktarget":"\u2028"}`}
	var cases [][]byte
	for _, prefix := range []string{"", good + ",", good + ", " + good + ", \n\t"} {
		for _, n := range nodes {
			cases = append(cases, []byte(`{"extra":{"nested":[null]},"nodes":[`+prefix+n+`],"after":{"nodes":[]}}`))
		}
	}
	for _, bad := range []string{"", `null`, `[]`, `{}`, `{"nodes":null}`, `{"nodes":{}}`, `{"nodes":[}`, `{"nodes":[` + good + `,{"name":"b",]}`, `{"nodes":[` + good + `,`, `{"nodes":[` + good + `]`, `{"nodes":[` + good + `],"after":}`, `{"nodes":[` + good + `],"after":[]`, `{"nodes":[` + good + `],"nodes":[false]}`, `{"nodes":[` + good + `]} true`} {
		cases = append(cases, []byte(bad))
	}
	for _, size := range []int{0, 1, 511, 512, 513, 4096, 65537} {
		cases = append(cases, []byte(`{"nodes":[`+good+`,{"name":"b","user":"`+strings.Repeat("x", size)+`","mode":"bad"}]}`))
	}
	return cases
}

func parentCaseDifferential(t interface{ Fatalf(string, ...any) }) int {
	cases := parentCaseFixtures()
	for i, payload := range cases {
		before := bytes.Clone(payload)
		old, oe := parentCaseRead(payload, false)
		next, ne := parentCaseRead(payload, true)
		if !reflect.DeepEqual(old, next) || parentCaseFacts(oe) != parentCaseFacts(ne) {
			t.Fatalf("case%d data=%q old=%+v next=%+v errors=%+v / %+v", i, payload, old, next, parentCaseFacts(oe), parentCaseFacts(ne))
		}
		if !bytes.Equal(payload, before) {
			t.Fatalf("case%d input mutated", i)
		}
		if oe == nil {
			owned, err := json.Marshal(next)
			if err != nil {
				t.Fatalf("case%d encode ownership reference: %v", i, err)
			}
			// Decoder strings, IDs, raw messages and attributes must own storage.
			for j := range payload {
				payload[j] = 'x'
			}
			after, err := json.Marshal(next)
			if err != nil || !bytes.Equal(after, owned) {
				t.Fatalf("case%d decoded storage borrowed input", i)
			}
		}
	}
	return len(cases)
}

type parentCaseLoader struct {
	payload []byte
	err     error
	calls   int
	id      restic.ID
}

func (l *parentCaseLoader) LoadBlob(ctx context.Context, h restic.BlobHandle, scratch []byte) ([]byte, error) {
	l.calls++
	l.id = h.ID
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return l.payload, l.err
}

func parentCaseOwnership(t interface{ Fatalf(string, ...any) }) {
	payload := []byte(`{"nodes":[{"name":"a","content":[]},{"name":"b"},{"mode":"bad"}]}`)
	for _, direct := range []bool{false, true} {
		var it TreeNodeIterator
		var err error
		if direct {
			it, err = newParentTreeIterator(payload)
		} else {
			it, err = NewTreeNodeIterator(bytes.NewReader(payload))
		}
		if err != nil {
			t.Fatalf("init %v", err)
		}
		count := 0
		for item := range it {
			if item.Error != nil {
				t.Fatalf("early stop parsed tail %v", item.Error)
			}
			count++
			break
		}
		if count != 1 {
			t.Fatalf("early stop count %d", count)
		}
		func() {
			defer func() {
				if p := recover(); fmt.Sprint(p) != "tree iterator is single use only" {
					t.Fatalf("single-use panic %v", p)
				}
			}()
			for range it {
			}
			t.Fatalf("missing panic")
		}()
	}
	for _, fail := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		if fail {
			cancel()
		}
		defer cancel()
		loader := &parentCaseLoader{payload: payload}
		id := restic.Hash(payload)
		_, err := LoadParentTree(ctx, loader, id)
		if loader.calls != 1 || loader.id != id || fail && !errors.Is(err, context.Canceled) || !fail && err != nil {
			t.Fatalf("loader/cancel contract %v calls%d", err, loader.calls)
		}
	}
	sentinel := errors.New("authentication failed")
	loader := &parentCaseLoader{err: sentinel}
	if _, err := LoadParentTree(context.Background(), loader, restic.ID{}); err != sentinel || loader.calls != 1 {
		t.Fatalf("load error lost %v", err)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 8 {
				old, oe := parentCaseRead(payload, false)
				next, ne := parentCaseRead(payload, true)
				if !reflect.DeepEqual(old, next) || parentCaseFacts(oe) != parentCaseFacts(ne) {
					t.Fatalf("worker isolation")
				}
			}
		}()
	}
	wg.Wait()
}

func TestParentTreeDifferential(t *testing.T)        { t.Logf("executed %d cases", parentCaseDifferential(t)) }
func TestParentTreeOwnershipAndFailure(t *testing.T) { parentCaseOwnership(t) }
