package data

// WITNESS (review of S019-merge, area backup): tree and node JSON.
//
// TestWitnessNodeJSONEqualsStock: the candidate's hand-written "scalar" node serializer
//   (node_json_scalar.go, used by Node.MarshalJSON and TreeJSONBuilder.AddNode) must emit
//   exactly the bytes of stock's reflection path, because tree IDs are hashes of these
//   bytes. The stock path is replicated here verbatim (stockNodeJSON). 300k random nodes
//   including hostile names, times, zones and attribute shapes. PASS expected.
// TestWitnessTreeBuilderReferenceEqualsPlain: NewTreeJSONBuilderWithReference (parent tree
//   plaintext reuse) must return exactly the bytes the plain builder returns, for any
//   reference. PASS expected.
// TestWitnessTreeIteratorLeniency: stock decodes every node through Node.UnmarshalJSON,
//   which always runs strconv.Unquote on the name. The candidate's direct iterator (used by
//   LoadTree, i.e. by backup parents, restore, ls, check, ...) only unquotes names that
//   contain a backslash. A tree blob whose node name holds a raw double quote or a raw
//   newline (never written by restic, possible from another writer) is REJECTED by stock
//   ("Unquote: invalid syntax") and ACCEPTED by the candidate. This test FAILS on the
//   candidate to demonstrate the divergence.
//
// OS: any. Run from the candidate tree:
//   go test -run 'TestWitness' -count=1 -p 2 ./internal/data

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/restic/restic/internal/restic"
)

// stockNodeJSON is Node.MarshalJSON of stock restic ba802d42, verbatim.
func stockNodeJSON(node Node) ([]byte, error) {
	node.ModTime = fixTime(node.ModTime)
	node.AccessTime = fixTime(node.AccessTime)
	node.ChangeTime = fixTime(node.ChangeTime)

	type nodeJSON Node
	nj := nodeJSON(node)
	name := strconv.Quote(node.Name)
	nj.Name = name[1 : len(name)-1]
	if nj.LinkTargetRaw != nil {
		panic("LinkTargetRaw must not be set manually")
	}
	if !utf8.ValidString(node.LinkTarget) {
		nj.LinkTargetRaw = []byte(node.LinkTarget)
	}
	return json.Marshal(nj)
}

var witnessStrings = []string{
	"", "a", "file.txt", "with space", "UPPER_lower-0123456789.~", "tab\there", "nl\nhere", "quote\"here", "back\\slash",
	"lt<gt>amp&", "del\x7f", "nul\x00", "ctrl\x1f", "café", "日本語", "emoji\U0001F600", "ls ps ",
	"invalid\xff\xfeutf8", "\xc3", "trailing\\", "'single'", "{json}", "[arr]", "a/b", "a:b", "~", " ", "%", "\\x41", "\\u0041",
}

func witnessString(rnd *rand.Rand) string {
	switch rnd.Intn(12) {
	case 0:
		b := make([]byte, rnd.Intn(24))
		_, _ = rnd.Read(b)
		return string(b)
	case 1:
		return string(bytes.Repeat([]byte{'x'}, 4090+rnd.Intn(12)))
	case 2, 3, 4:
		b := make([]byte, 1+rnd.Intn(20))
		for i := range b {
			b[i] = byte(0x20 + rnd.Intn(0x60))
		}
		return string(b)
	default:
		return witnessStrings[rnd.Intn(len(witnessStrings))]
	}
}

func witnessTime(rnd *rand.Rand) time.Time {
	var t time.Time
	switch rnd.Intn(10) {
	case 0:
		return time.Time{}
	case 1:
		t = time.Unix(rnd.Int63n(1<<33), int64(rnd.Intn(1e9)))
	case 2:
		t = time.Unix(rnd.Int63n(1<<33), int64(rnd.Intn(1000))*1e6)
	case 3:
		t = time.Unix(rnd.Int63n(1<<33), 0)
	case 4:
		t = time.Date(-5+rnd.Intn(12), 1, 1, 0, 0, 0, rnd.Intn(1e9), time.UTC)
	case 5:
		t = time.Date(9995+rnd.Intn(12), 12, 31, 23, 59, 59, rnd.Intn(1e9), time.UTC)
	case 6:
		t = time.Unix(-rnd.Int63n(1<<36), int64(rnd.Intn(1e9)))
	default:
		t = time.Unix(rnd.Int63n(1<<32), int64(rnd.Intn(1e9)))
	}
	switch rnd.Intn(8) {
	case 0:
		return t.UTC()
	case 1:
		return t.In(time.FixedZone("x", rnd.Intn(2*86400+7200)-86400-3600))
	case 2:
		return t.In(time.FixedZone("s", rnd.Intn(121)-60))
	case 3:
		return t.In(time.FixedZone("edge", []int{86399, -86399, 86400, -86400, 86340, -86340, 1, -1, 59, 60, 3600 * 23}[rnd.Intn(11)]))
	case 4:
		return t.In(time.Local)
	default:
		return t
	}
}

func witnessUint(rnd *rand.Rand) uint64 {
	switch rnd.Intn(5) {
	case 0:
		return 0
	case 1:
		return ^uint64(0)
	case 2:
		return uint64(rnd.Intn(10))
	default:
		return rnd.Uint64() >> uint(rnd.Intn(64))
	}
}

func witnessNode(rnd *rand.Rand) Node {
	types := []NodeType{NodeTypeFile, NodeTypeDir, NodeTypeSymlink, NodeTypeDev, NodeTypeCharDev, NodeTypeFifo, NodeTypeSocket, NodeTypeIrregular, NodeTypeInvalid, NodeType("we<ird"), NodeType("x\"y")}
	n := Node{
		Name:       witnessString(rnd),
		Type:       types[rnd.Intn(len(types))],
		Mode:       os.FileMode(witnessUint(rnd)),
		ModTime:    witnessTime(rnd),
		AccessTime: witnessTime(rnd),
		ChangeTime: witnessTime(rnd),
		UID:        uint32(witnessUint(rnd)),
		GID:        uint32(witnessUint(rnd)),
		Inode:      witnessUint(rnd),
		DeviceID:   witnessUint(rnd),
		Size:       witnessUint(rnd),
		Links:      witnessUint(rnd),
		Device:     witnessUint(rnd),
		Path:       "ignored",
	}
	if rnd.Intn(2) == 0 {
		n.User = witnessString(rnd)
	}
	if rnd.Intn(2) == 0 {
		n.Group = witnessString(rnd)
	}
	if rnd.Intn(3) == 0 {
		n.LinkTarget = witnessString(rnd)
	}
	if rnd.Intn(4) == 0 {
		n.Error = witnessString(rnd)
	}
	switch rnd.Intn(6) {
	case 0:
	case 1:
		n.Content = restic.IDs{}
	default:
		k := 1 + rnd.Intn(4)
		if rnd.Intn(2000) == 0 {
			k = 4090 + rnd.Intn(12)
		}
		for range k {
			var id restic.ID
			_, _ = rnd.Read(id[:])
			n.Content = append(n.Content, id)
		}
	}
	if rnd.Intn(3) == 0 {
		var id restic.ID
		_, _ = rnd.Read(id[:])
		n.Subtree = &id
	}
	switch rnd.Intn(12) {
	case 0:
		n.ExtendedAttributes = []ExtendedAttribute{}
	case 1:
		n.ExtendedAttributes = []ExtendedAttribute{{Name: witnessString(rnd), Value: []byte(witnessString(rnd))}}
	}
	switch rnd.Intn(12) {
	case 0:
		n.GenericAttributes = map[GenericAttributeType]json.RawMessage{}
	case 1:
		n.GenericAttributes = map[GenericAttributeType]json.RawMessage{"windows.file_attributes": json.RawMessage(`32`)}
	}
	return n
}

func TestWitnessNodeJSONEqualsStock(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261006))
	scalar, general, errs := 0, 0, 0
	for i := range 300000 {
		n := witnessNode(rnd)
		want, wantErr := stockNodeJSON(n)
		got, gotErr := json.Marshal(&n) // exactly the call stock's AddNode makes
		if (wantErr == nil) != (gotErr == nil) {
			t.Fatalf("node %d: error mismatch stock=%v candidate=%v\nnode=%#v", i, wantErr, gotErr, n)
		}
		if wantErr != nil {
			errs++
			continue
		}
		if !bytes.Equal(want, got) {
			t.Fatalf("node %d: JSON BYTES DIFFER\nstock:     %s\ncandidate: %s", i, want, got)
		}
		// the direct call used by the candidate's AddNode
		norm := n
		norm.ModTime, norm.AccessTime, norm.ChangeTime = fixTime(n.ModTime), fixTime(n.AccessTime), fixTime(n.ChangeTime)
		if direct, ok := appendScalarNodeJSON(nil, norm); ok {
			scalar++
			if !bytes.Equal(want, direct) {
				t.Fatalf("node %d: SCALAR BYTES DIFFER\nstock:     %s\ncandidate: %s", i, want, direct)
			}
		} else {
			general++
		}
	}
	t.Logf("scalar path %d nodes, general path %d nodes, %d nodes rejected identically by both", scalar, general, errs)
	if scalar < 1000 {
		t.Fatalf("generator did not exercise the scalar path (%d)", scalar)
	}
}

func stockTreeJSON(nodes []*Node) []byte {
	var buf bytes.Buffer
	buf.WriteString(`{"nodes":[`)
	for i, n := range nodes {
		if i > 0 {
			buf.WriteByte(',')
		}
		val, err := stockNodeJSON(*n)
		if err != nil {
			panic(err)
		}
		buf.Write(val)
	}
	buf.WriteString("]}\n")
	return buf.Bytes()
}

func witnessTree(rnd *rand.Rand, count int) []*Node {
	var nodes []*Node
	for i := 0; len(nodes) < count; i++ {
		n := witnessNode(rnd)
		if _, err := stockNodeJSON(n); err != nil {
			continue
		}
		n.Name = fmt.Sprintf("%06d-%s", len(nodes), n.Name)
		nodes = append(nodes, &n)
	}
	return nodes
}

func TestWitnessTreeBuilderReferenceEqualsPlain(t *testing.T) {
	rnd := rand.New(rand.NewSource(99))
	build := func(b *TreeJSONBuilder, nodes []*Node) []byte {
		for _, n := range nodes {
			if err := b.AddNode(n); err != nil {
				t.Fatal(err)
			}
		}
		out, err := b.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		return append([]byte(nil), out...)
	}
	for round := range 400 {
		nodes := witnessTree(rnd, rnd.Intn(40))
		want := stockTreeJSON(nodes)
		if got := build(NewTreeJSONBuilder(), nodes); !bytes.Equal(want, got) {
			t.Fatalf("round %d: plain builder differs from stock bytes", round)
		}
		refs := map[string][]byte{"identical": want, "empty": nil, "garbage": []byte("{\"nodes\":[garbage")}
		refs["identical+trailing"] = append(append([]byte(nil), want...), []byte("trailing bytes")...)
		if len(want) > 20 {
			refs["truncated"] = want[:len(want)/2]
			flipped := append([]byte(nil), want...)
			flipped[rnd.Intn(len(flipped))] ^= 1
			refs["one-bit-flipped"] = flipped
		}
		if len(nodes) > 2 {
			k := rnd.Intn(len(nodes))
			other := append([]*Node(nil), nodes...)
			changed := *other[k]
			changed.Size++
			other[k] = &changed
			refs["one-node-changed"] = stockTreeJSON(other)
			refs["one-node-removed"] = stockTreeJSON(append(append([]*Node(nil), nodes[:k]...), nodes[k+1:]...))
			refs["prefix-only"] = stockTreeJSON(nodes[:k])
		}
		for name, ref := range refs {
			refCopy := append([]byte(nil), ref...)
			got := build(NewTreeJSONBuilderWithReference(ref), nodes)
			if !bytes.Equal(want, got) {
				t.Fatalf("round %d reference %q: builder output differs from stock bytes\nwant %q\ngot  %q", round, name, want, got)
			}
			if !bytes.Equal(ref, refCopy) {
				t.Fatalf("round %d reference %q: builder MODIFIED the parent plaintext", round, name)
			}
		}
	}
}

func collect(it TreeNodeIterator, err error) ([]*Node, error) {
	if err != nil {
		return nil, err
	}
	var out []*Node
	for item := range it {
		if item.Error != nil {
			return out, item.Error
		}
		out = append(out, item.Node)
	}
	return out, nil
}

func TestWitnessTreeIteratorEqualsStockOnResticTrees(t *testing.T) {
	rnd := rand.New(rand.NewSource(5))
	for round := range 300 {
		payload := stockTreeJSON(witnessTree(rnd, rnd.Intn(30)))
		want, wantErr := collect(NewTreeNodeIterator(bytes.NewReader(payload))) // stock decoder (unchanged function)
		got, gotErr := collect(newDirectTreeIterator(payload))                  // what LoadTree uses in the candidate
		if (wantErr == nil) != (gotErr == nil) || !reflect.DeepEqual(want, got) {
			t.Fatalf("round %d: direct iterator differs from stock iterator (stock err %v, candidate err %v)", round, wantErr, gotErr)
		}
	}
}

func TestWitnessTreeIteratorLeniency(t *testing.T) {
	for _, c := range []struct{ what, payload string }{
		{"raw double quote in name", `{"nodes":[{"name":"a\"b","type":"file","mtime":"2020-01-01T00:00:00Z","atime":"2020-01-01T00:00:00Z","ctime":"2020-01-01T00:00:00Z","uid":0,"gid":0,"content":null}]}` + "\n"},
		{"raw newline in name", `{"nodes":[{"name":"a\nb","type":"file","mtime":"2020-01-01T00:00:00Z","atime":"2020-01-01T00:00:00Z","ctime":"2020-01-01T00:00:00Z","uid":0,"gid":0,"content":null}]}` + "\n"},
	} {
		_, stockErr := collect(NewTreeNodeIterator(bytes.NewReader([]byte(c.payload))))
		got, candErr := collect(newDirectTreeIterator([]byte(c.payload)))
		t.Logf("%s: stock error = %v ; candidate error = %v, nodes = %d", c.what, stockErr, candErr, len(got))
		if stockErr == nil {
			t.Fatalf("%s: test assumption broken, stock accepted the tree", c.what)
		}
		if candErr == nil {
			t.Errorf("DIVERGENCE (%s): stock rejects this tree, candidate accepts it and yields name %q", c.what, got[0].Name)
		}
	}
}
