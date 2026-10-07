package data

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

// This reference retains the M001-1 operation and failure-state ordering.
type testTreeMarshalBuilder struct {
	buf        bytes.Buffer
	lastName   string
	countNodes int
}

func testTreeNewMarshalBuilder() *testTreeMarshalBuilder {
	b := &testTreeMarshalBuilder{}
	b.buf.WriteString(`{"nodes":[`)
	return b
}

func (b *testTreeMarshalBuilder) add(node *Node) error {
	if node.Name <= b.lastName {
		return fmt.Errorf("node %q, last %q: %w", node.Name, b.lastName, ErrTreeNotOrdered)
	}
	if b.lastName != "" {
		b.buf.WriteByte(',')
	}
	b.lastName = node.Name
	val, err := json.Marshal(node)
	if err != nil {
		return err
	}
	b.buf.Write(val)
	b.countNodes++
	return nil
}

func (b *testTreeMarshalBuilder) finish() []byte {
	b.buf.WriteString("]}\n")
	out := b.buf.Bytes()
	b.buf = bytes.Buffer{}
	return out
}

func testTreeError(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprintf("%T:%s", err, err)
}

func testTreeEncoderCases() (int, error) {
	id := restic.Hash([]byte("content"))
	suites := [][]*Node{
		{},
		{{Name: "a"}, {Name: "b", Content: restic.IDs{}}, {Name: "c", Content: restic.IDs{id}, Subtree: &id}},
		{{Name: "a<&>\"\\\n\x00\xff", LinkTarget: "\xff\xfe<&>\u2028", Type: NodeTypeSymlink, Error: "<&>\u2029"}},
		{{Name: "a", ModTime: time.Date(-12, 2, 3, 4, 5, 6, 7, time.UTC), AccessTime: time.Date(12000, 2, 3, 4, 5, 6, 7, time.UTC)}},
		{{Name: "a", GenericAttributes: map[GenericAttributeType]json.RawMessage{"z": []byte(`{"x":1,"x":2}`), "a": []byte(` [1, "<&>"] `)}, ExtendedAttributes: []ExtendedAttribute{{Name: "x", Value: []byte{0, 1, 255}}, {Name: "nil"}, {Name: "empty", Value: []byte{}}}}},
		{{Name: "a"}, {Name: "a"}, {Name: "0"}, {Name: "b"}},
		{{Name: ""}, {Name: "a"}},
		{{Name: "a"}, {Name: "b", GenericAttributes: map[GenericAttributeType]json.RawMessage{"bad": []byte(`{"x":`)}}, {Name: "b"}, {Name: "c"}},
		{{Name: "a", GenericAttributes: map[GenericAttributeType]json.RawMessage{"bad": []byte(`true false`)}}, {Name: "b"}},
		{{Name: "a", ExtendedAttributes: []ExtendedAttribute{{Name: "large", Value: bytes.Repeat([]byte{128}, 2*1024*1024)}}, Content: make(restic.IDs, 16384)}, {Name: "b"}},
	}
	// Exercise capacity boundaries and reuse after a failed serialization.
	for _, n := range []int{0, 1, 31, 63, 64, 65, 511, 1023, 1024, 4095, 8191} {
		suites = append(suites, []*Node{{Name: "a", User: strings.Repeat("x", n)}, {Name: "b"}})
	}
	for i, nodes := range suites {
		got, want := NewTreeJSONBuilder(), testTreeNewMarshalBuilder()
		for j, node := range nodes {
			before, err := json.Marshal(node)
			ge, we := got.AddNode(node), want.add(node)
			if testTreeError(ge) != testTreeError(we) || errors.Is(ge, ErrTreeNotOrdered) != errors.Is(we, ErrTreeNotOrdered) {
				return i, fmt.Errorf("suite %d node %d errors differ: %v / %v", i, j, ge, we)
			}
			if !bytes.Equal(got.buf.Bytes(), want.buf.Bytes()) || got.lastName != want.lastName || got.Count() != want.countNodes {
				return i, fmt.Errorf("suite %d node %d builder state differs", i, j)
			}
			after, ae := json.Marshal(node)
			if testTreeError(err) != testTreeError(ae) || !bytes.Equal(before, after) {
				return i, fmt.Errorf("suite %d node %d input mutated", i, j)
			}
		}
		gb, err := got.Finalize()
		wb := want.finish()
		if err != nil || !bytes.Equal(gb, wb) || restic.Hash(gb) != restic.Hash(wb) {
			return i, fmt.Errorf("suite %d final bytes differ", i)
		}
		owned := bytes.Clone(gb)
		// Both builders retain count/name but must release finalized storage.
		ge, we := got.AddNode(&Node{Name: "zzzz"}), want.add(&Node{Name: "zzzz"})
		if testTreeError(ge) != testTreeError(we) || !bytes.Equal(gb, owned) {
			return i, fmt.Errorf("suite %d finalized storage reused", i)
		}
	}
	return len(suites), nil
}

type testTreeRetainingSaver struct {
	buf   []byte
	err   error
	calls int
}

func (s *testTreeRetainingSaver) SaveBlob(ctx context.Context, typ restic.BlobType, buf []byte, id restic.ID, duplicate bool) (restic.ID, bool, int, error) {
	s.calls++
	s.buf = buf
	if ctx.Err() != nil {
		return restic.ID{}, false, 0, ctx.Err()
	}
	return restic.Hash(buf), false, len(buf), s.err
}

func testTreeWriterCases() error {
	saver := &testTreeRetainingSaver{}
	w := NewTreeWriter(saver)
	node := &Node{Name: "a", Content: restic.IDs{restic.Hash([]byte("original"))}}
	if err := w.AddNode(node); err != nil {
		return err
	}
	id, err := w.Finalize(context.Background())
	if err != nil || id != restic.Hash(saver.buf) {
		return fmt.Errorf("finalized writer: %v", err)
	}
	owned := bytes.Clone(saver.buf)
	node.Name = "changed"
	node.Content[0] = restic.ID{}
	if err := w.AddNode(&Node{Name: "z"}); err != nil {
		return err
	}
	if !bytes.Equal(saver.buf, owned) {
		return errors.New("retained payload changed after input/builder reuse")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelSaver := &testTreeRetainingSaver{}
	_, err = NewTreeWriter(cancelSaver).Finalize(ctx)
	if !errors.Is(err, context.Canceled) || cancelSaver.calls != 1 {
		return fmt.Errorf("cancelled finalize: %v, calls %d", err, cancelSaver.calls)
	}
	sentinel := errors.New("save failed")
	failSaver := &testTreeRetainingSaver{err: sentinel}
	_, err = NewTreeWriter(failSaver).Finalize(context.Background())
	if !errors.Is(err, sentinel) {
		return errors.New("save error lost")
	}
	iterErr := errors.New("iterator failed")
	nodes := func(yield func(NodeOrError) bool) {
		if yield(NodeOrError{Node: &Node{Name: "a"}}) {
			yield(NodeOrError{Error: iterErr})
		}
	}
	iterSaver := &testTreeRetainingSaver{}
	_, err = SaveTree(context.Background(), iterSaver, nodes)
	if !errors.Is(err, iterErr) || iterSaver.calls != 0 {
		return errors.New("iterator error published incomplete tree")
	}
	return nil
}

func TestTreeEncoderExactBytesAndOwnership(t *testing.T) {
	if n, err := testTreeEncoderCases(); err != nil {
		t.Fatal(n, err)
	}
	if err := testTreeWriterCases(); err != nil {
		t.Fatal(err)
	}
	// No hidden mutation of the manually-set LinkTargetRaw panic contract.
	got := func() (p any) {
		defer func() { p = recover() }()
		NewTreeJSONBuilder().AddNode(&Node{Name: "a", LinkTargetRaw: []byte{1}})
		return
	}
	want := func() (p any) {
		defer func() { p = recover() }()
		testTreeNewMarshalBuilder().add(&Node{Name: "a", LinkTargetRaw: []byte{1}})
		return
	}
	if !reflect.DeepEqual(got(), want()) {
		t.Fatal("panic behavior differs")
	}
}

// The zero value historically accepts nodes, even though it omits the prefix.
// Keep its byte/error state and defer encoder creation until a node is added.
func TestTreeEncoderZeroValue(t *testing.T) {
	var got TreeJSONBuilder
	var want testTreeMarshalBuilder
	for _, node := range []*Node{
		{Name: "a"},
		{Name: "b", GenericAttributes: map[GenericAttributeType]json.RawMessage{"bad": []byte(`{"x":`)}},
		{Name: "c"},
	} {
		ge, we := got.AddNode(node), want.add(node)
		if testTreeError(ge) != testTreeError(we) || !bytes.Equal(got.buf.Bytes(), want.buf.Bytes()) || got.Count() != want.countNodes || got.lastName != want.lastName {
			t.Fatalf("zero-value state changed: %v / %v", ge, we)
		}
	}
	gb, ge := got.Finalize()
	if ge != nil || !bytes.Equal(gb, want.finish()) {
		t.Fatal("zero-value final bytes changed")
	}
	empty := NewTreeJSONBuilder()
	if empty.encoder != nil {
		t.Fatal("encoder allocated before demand")
	}
	eb, e := empty.Finalize()
	if e != nil || !bytes.Equal(eb, []byte(`{"nodes":[]}`+"\n")) {
		t.Fatal("empty tree changed")
	}
}
