package data

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

// Freeze the immediately preceding Encoder path, including failure state.
func directScalarReferenceAdd(b *TreeJSONBuilder, n *Node) error {
	if n.Name <= b.lastName {
		return fmt.Errorf("node %q, last %q: %w", n.Name, b.lastName, ErrTreeNotOrdered)
	}
	if b.lastName != "" {
		_ = b.buf.WriteByte(',')
	}
	b.lastName = n.Name
	if b.encoder == nil {
		b.encoder = json.NewEncoder(&b.buf)
	}
	if err := b.encoder.Encode(n); err != nil {
		return err
	}
	b.buf.Truncate(b.buf.Len() - 1)
	b.countNodes++
	return nil
}

func TestTreeScalarDirectDifferential(t *testing.T) {
	id := restic.Hash([]byte("content"))
	cases := []Node{
		{Name: "a"}, {Name: "a", Content: restic.IDs{}},
		{Name: "a", Content: restic.IDs{id}, Subtree: &id},
		{Name: "a\xff<&>\"\\\n", LinkTarget: "\xff\xfe"},
		{Name: "a", ExtendedAttributes: []ExtendedAttribute{{Name: "x", Value: []byte{0, 255}}}},
		{Name: "a", GenericAttributes: map[GenericAttributeType]json.RawMessage{"a": []byte(` {"x":1,"x":2} `)}},
		{Name: "a", GenericAttributes: map[GenericAttributeType]json.RawMessage{"bad": []byte(`{"x":`)}},
		{Name: "a", Content: make(restic.IDs, 4096)},
		{Name: "a", Content: make(restic.IDs, 4097)},
	}
	for _, year := range []int{-12000, -1, 0, 9999, 10000, 12000} {
		for _, offset := range []int{-86460, -86400, -86399, -1, 0, 1, 86399, 86400, 86460} {
			tm := time.Date(year, 2, 29, 1, 2, 3, 999999999, time.FixedZone("odd", offset))
			cases = append(cases, Node{Name: "a", ModTime: tm, AccessTime: tm.Add(time.Second), ChangeTime: tm.Add(-time.Second)})
		}
	}
	for i := range cases {
		got, want := NewTreeJSONBuilder(), NewTreeJSONBuilder()
		for _, n := range []*Node{&cases[i], &cases[i], {Name: "0"}, {Name: "b"}} {
			before, _ := json.Marshal(n)
			ge, we := got.AddNode(n), directScalarReferenceAdd(want, n)
			if testTreeError(ge) != testTreeError(we) || !bytes.Equal(got.buf.Bytes(), want.buf.Bytes()) || got.lastName != want.lastName || got.Count() != want.Count() {
				t.Fatalf("case %d state/errors differ: %v / %v", i, ge, we)
			}
			after, _ := json.Marshal(n)
			if !bytes.Equal(before, after) {
				t.Fatalf("case %d input changed", i)
			}
		}
		gb, ge := got.Finalize()
		wb, we := want.Finalize()
		if testTreeError(ge) != testTreeError(we) || !bytes.Equal(gb, wb) {
			t.Fatalf("case %d final bytes differ", i)
		}
	}
	for _, raw := range [][]byte{{}, {1}} {
		got, want := NewTreeJSONBuilder(), NewTreeJSONBuilder()
		n := &Node{Name: "a", LinkTargetRaw: raw}
		panicOf := func(f func() error) (p any) { defer func() { p = recover() }(); _ = f(); return }
		gp := panicOf(func() error { return got.AddNode(n) })
		wp := panicOf(func() error { return directScalarReferenceAdd(want, n) })
		if !reflect.DeepEqual(gp, wp) || !bytes.Equal(got.buf.Bytes(), want.buf.Bytes()) || got.lastName != want.lastName || got.Count() != want.Count() {
			t.Fatal("manual raw-link panic/state differ")
		}
	}
}

// Use workload-scale node counts without fixture-dependent code in production.
func TestTreeScalarDirectManyNodes(t *testing.T) {
	got, want := NewTreeJSONBuilder(), NewTreeJSONBuilder()
	for i := range 91000 {
		n := &Node{Name: fmt.Sprintf("file-%06d", i), Type: NodeTypeFile, Mode: 0644, Size: uint64(i)}
		if i%97 == 0 {
			n.User = "user<&>"
		}
		if ge, we := got.AddNode(n), directScalarReferenceAdd(want, n); ge != nil || we != nil {
			t.Fatalf("node %d: %v / %v", i, ge, we)
		}
	}
	gb, ge := got.Finalize()
	wb, we := want.Finalize()
	if ge != nil || we != nil || !bytes.Equal(gb, wb) || restic.Hash(gb) != restic.Hash(wb) || got.Count() != 91000 {
		t.Fatal("large tree bytes/count/hash differ")
	}
}

func TestTreeScalarAppendDestination(t *testing.T) {
	for _, n := range []Node{
		{Name: "a"}, {Name: "a", Content: restic.IDs{}},
		{Name: "a", User: "<&>"},
		{Name: "a", GenericAttributes: map[GenericAttributeType]json.RawMessage{"bad": []byte(`{"x":`)}},
	} {
		storage := bytes.Repeat([]byte{0xa5}, 4096)
		prefix := []byte("prefix:")
		copy(storage, prefix)
		before := bytes.Clone(storage)
		got, ok := appendScalarNodeJSON(storage[:len(prefix)], n)
		want, wantOK := scalarNodeJSON(n)
		if ok != wantOK {
			t.Fatal("destination changed eligibility")
		}
		if !ok {
			if !bytes.Equal(storage, before) {
				t.Fatal("fallback modified destination")
			}
			continue
		}
		if !bytes.Equal(got[:len(prefix)], prefix) || !bytes.Equal(got[len(prefix):], want) {
			t.Fatal("destination bytes differ")
		}
		if &got[0] != &storage[0] {
			t.Fatal("spare capacity was not reused")
		}
		if !bytes.Equal(storage[len(got):], before[len(got):]) {
			t.Fatal("unused destination tail modified")
		}
	}
}
