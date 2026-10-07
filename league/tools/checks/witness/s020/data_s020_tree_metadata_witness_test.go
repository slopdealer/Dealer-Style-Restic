package data

// WITNESS (S020 review, mechanism 5: internal/data/tree_metadata.go).
//
// LoadMetadataTree is the tree decoder S020 uses for the second (metadata)
// pass of restore (internal/restorer/restorer.go:817). It must give what
// stock's decoder gives, minus the content IDs.
//
// TestWitnessS020MetadataTreeDifferential
//   LoadMetadataTree against STOCK's decoder (NewTreeNodeIterator ->
//   treeIterator.next -> Node.UnmarshalJSON, still in the tree) on trees
//   written by restic's own serializer with hostile names and attributes, and
//   on hand-made and mutated JSON. Expected: PASS (same nodes apart from
//   Content, same errors at the same node).
//
// TestWitnessS020MetadataTreeNameLeniency
//   The one divergence found: a node name that, after JSON decoding, holds a
//   raw double quote or a raw newline and no backslash. Stock rejects the tree
//   ("Unquote: invalid syntax"); LoadMetadataTree accepts it
//   (tree_metadata.go:124-129 only unquotes names that contain a backslash).
//   This is repair R7 again, in a file R7 did not know. Expected on S020: FAIL.
//
// OS: any. Run from the tree root:
//   go test -run 'TestWitnessS020MetadataTree' -count=1 -p 2 -v ./internal/data

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

type s020TreeLoader map[restic.ID][]byte

func (l s020TreeLoader) LoadBlob(_ context.Context, h restic.BlobHandle, _ []byte) ([]byte, error) {
	return l[h.ID], nil
}

type s020Decoded struct {
	nodes   []*Node
	openErr string
	nodeErr string
}

func s020Collect(it TreeNodeIterator, err error) (res s020Decoded) {
	if err != nil {
		res.openErr = err.Error()
		return res
	}
	for item := range it {
		if item.Error != nil {
			res.nodeErr = item.Error.Error()
			return res
		}
		res.nodes = append(res.nodes, item.Node)
	}
	return res
}

func s020Stock(payload []byte) s020Decoded {
	it, err := NewTreeNodeIterator(bytes.NewReader(payload))
	res := s020Collect(it, err)
	for _, n := range res.nodes {
		n.Content = nil // the metadata decoder returns no content IDs by design
	}
	return res
}

func s020Metadata(payload []byte) s020Decoded {
	id := restic.Hash(payload)
	it, err := LoadMetadataTree(context.Background(), s020TreeLoader{id: payload}, id)
	return s020Collect(it, err)
}

func s020Compare(t *testing.T, what string, payload []byte) (accepted bool) {
	t.Helper()
	want := s020Stock(payload)
	got := s020Metadata(payload)
	if want.openErr != got.openErr {
		t.Fatalf("%s: opening error: stock %q, metadata decoder %q\npayload: %.300q", what, want.openErr, got.openErr, payload)
	}
	if len(want.nodes) != len(got.nodes) {
		t.Fatalf("%s: stock yields %d nodes (then %q), metadata decoder %d (then %q)\npayload: %.300q", what, len(want.nodes), want.nodeErr, len(got.nodes), got.nodeErr, payload)
	}
	for i := range want.nodes {
		if !reflect.DeepEqual(want.nodes[i], got.nodes[i]) {
			t.Fatalf("%s: node %d differs:\nstock    %#v\nmetadata %#v", what, i, want.nodes[i], got.nodes[i])
		}
	}
	if want.nodeErr != got.nodeErr {
		t.Fatalf("%s: error after %d nodes: stock %q, metadata decoder %q\npayload: %.300q", what, len(want.nodes), want.nodeErr, got.nodeErr, payload)
	}
	return want.openErr == "" && want.nodeErr == ""
}

func s020RandomName(rng *rand.Rand) string {
	alphabet := []string{"a", "b", "Z", "0", " ", ".", "-", "\\", "\"", "'", "\n", "\r", "\t", "\x00", "\x01", "\x7f", "\xff", "\xc3\x28", "é", "日本", " ", "<", ">", "&", "/", "{", "}", "[", "]", ",", ":"}
	var sb strings.Builder
	for i := 0; i < 1+rng.Intn(12); i++ {
		sb.WriteString(alphabet[rng.Intn(len(alphabet))])
	}
	return sb.String()
}

func s020RandomNode(rng *rand.Rand, name string) *Node {
	types := []NodeType{NodeTypeFile, NodeTypeDir, NodeTypeSymlink, NodeTypeDev, NodeTypeCharDev, NodeTypeFifo, NodeTypeSocket}
	stamp := time.Unix(rng.Int63n(4e9), rng.Int63n(1e9)).In(time.FixedZone("z", rng.Intn(7200)-3600))
	n := &Node{
		Name: name, Type: types[rng.Intn(len(types))], Mode: os.FileMode(rng.Uint32()),
		ModTime: stamp, AccessTime: stamp.Add(time.Second), ChangeTime: stamp.Add(2 * time.Second),
		UID: rng.Uint32(), GID: rng.Uint32(), Inode: rng.Uint64(), Size: rng.Uint64(), Links: uint64(rng.Intn(4)),
	}
	if rng.Intn(2) == 0 {
		n.User, n.Group = s020RandomName(rng), s020RandomName(rng)
	}
	if rng.Intn(3) == 0 {
		n.DeviceID, n.Device = rng.Uint64(), rng.Uint64()
	}
	if n.Type == NodeTypeSymlink || rng.Intn(6) == 0 {
		n.LinkTarget = s020RandomName(rng) // sometimes invalid UTF-8: stored as linktarget_raw
	}
	if rng.Intn(3) == 0 {
		for k := 0; k < 1+rng.Intn(3); k++ {
			value := make([]byte, rng.Intn(20))
			rng.Read(value)
			n.ExtendedAttributes = append(n.ExtendedAttributes, ExtendedAttribute{Name: "user." + s020RandomName(rng), Value: value})
		}
	}
	if rng.Intn(4) == 0 {
		n.GenericAttributes = map[GenericAttributeType]json.RawMessage{"windows.x": json.RawMessage(fmt.Sprintf(`{"v":%d}`, rng.Intn(9)))}
	}
	if n.Type == NodeTypeFile || rng.Intn(8) == 0 {
		n.Content = restic.IDs{}
		for k := 0; k < rng.Intn(6); k++ {
			var id restic.ID
			rng.Read(id[:])
			n.Content = append(n.Content, id)
		}
		if rng.Intn(10) == 0 {
			n.Content = nil
		}
	}
	if n.Type == NodeTypeDir || rng.Intn(8) == 0 {
		var id restic.ID
		rng.Read(id[:])
		n.Subtree = &id
	}
	if rng.Intn(10) == 0 {
		n.Error = s020RandomName(rng)
	}
	return n
}

func TestWitnessS020MetadataTreeDifferential(t *testing.T) {
	accepted, rejected := 0, 0
	count := func(ok bool) {
		if ok {
			accepted++
		} else {
			rejected++
		}
	}

	// 1. trees written by restic's own serializers
	for round := 0; round < 400; round++ {
		rng := rand.New(rand.NewSource(int64(round)))
		var nodes []*Node
		for i := 0; i < rng.Intn(12); i++ {
			nodes = append(nodes, s020RandomNode(rng, fmt.Sprintf("%03d-%s", i, s020RandomName(rng))))
		}
		builder := NewTreeJSONBuilder()
		for _, n := range nodes {
			if err := builder.AddNode(n); err != nil {
				t.Fatal(err)
			}
		}
		payload, err := builder.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		if !s020Compare(t, fmt.Sprintf("builder round %d", round), payload) {
			t.Fatalf("round %d: stock rejects a tree restic wrote itself", round)
		}
		accepted++

		plain, err := json.Marshal(struct {
			Nodes []*Node `json:"nodes"`
		}{nodes})
		if err != nil {
			t.Fatal(err)
		}
		count(s020Compare(t, fmt.Sprintf("json.Marshal round %d", round), plain))

		// 2. byte mutations of a valid tree: truncations, flips, insertions
		for m := 0; m < 40 && len(plain) > 2; m++ {
			mutated := append([]byte(nil), plain...)
			switch rng.Intn(5) {
			case 0:
				mutated = mutated[:rng.Intn(len(mutated))]
			case 1:
				mutated[rng.Intn(len(mutated))] = "\"\\{}[],:x0n \n"[rng.Intn(13)]
			case 2:
				pos := rng.Intn(len(mutated))
				ins := []string{`"`, `\`, `,`, `null`, `{}`, `[]`, `"content":7,`, `"name":5,`, `x`, ` `, "\n"}[rng.Intn(11)]
				mutated = append(mutated[:pos], append([]byte(ins), mutated[pos:]...)...)
			case 3:
				pos := rng.Intn(len(mutated))
				end := min(len(mutated), pos+1+rng.Intn(8))
				mutated = append(mutated[:pos], mutated[end:]...)
			case 4:
				mutated = append(mutated, []byte{'}', ' ', 'x'}[rng.Intn(3)])
			}
			// a raw quote or newline in a name is the known divergence (second test)
			if s020NameLeniencyCase(mutated) {
				continue
			}
			count(s020Compare(t, fmt.Sprintf("mutation round %d/%d", round, m), mutated))
		}
	}

	// 3. hand-made cases around the content field and the framing
	id := restic.Hash([]byte("x")).String()
	contents := []string{`null`, `[]`, `[ ]`, `[null]`, `[false]`, `[0]`, `[""]`, `"` + id + `"`, `{}`, `7`,
		`["` + id + `"]`, `[ "` + id + `" , "` + id + `" ]`, `["` + id + `",]`, `["` + id + `" "` + id + `"]`,
		`["` + strings.ToUpper(id) + `"]`, `["` + id[:63] + `"]`, `["` + id + `0"]`, `["` + id[:63] + `g"]`,
		`['` + id + `']`, `["` + id + `",null]`, `["a` + id[1:] + `"]`, `[["` + id + `"]]`}
	keys := []string{`content`, `Content`, `CONTENT`, `content`, `content `, `contents`}
	for _, content := range contents {
		for _, key := range keys {
			node := `{"name":"n","type":"file","` + key + `":` + content + `,"size":3}`
			for _, payload := range []string{
				`{"nodes":[` + node + `]}`,
				`{"nodes":[` + node + `,` + node + `]}`,
				`{"nodes":[{"name":"m","content":["` + id + `"],"content":` + content + `}]}`,
				`{"x":1,"nodes":[` + node + `],"y":[{}]}`,
				`{"nodes":[` + node + `]`,
				`{"nodes":[` + node,
				`{"nodes":[` + node + `]}trailing`,
				` { "nodes" : [ ` + node + ` ] } `,
			} {
				count(s020Compare(t, "hand-made", []byte(payload)))
			}
		}
	}
	for _, payload := range []string{
		``, `null`, `[]`, `{}`, `{"nodes":null}`, `{"nodes":{}}`, `{"nodes":[null]}`, `{"nodes":[7]}`, `{"nodes":["x"]}`,
		`{"nodes":[[]]}`, `{"nodes":[{}]}`, `{"nodes":[{"name":7}]}`, `{"nodes":[{"name":"a","mode":"x"}]}`,
		`{"nodes":[{"name":"a","mtime":"yesterday"}]}`, `{"nodes":[{"name":"a","uid":-1}]}`,
		`{"nodes":[{"name":"a","subtree":"zz"}]}`, `{"nodes":[{"name":"a","linktarget_raw":"!!"}]}`,
		`{"nodes":[{"name":"a","linktarget":"t","linktarget_raw":"/w=="}]}`,
		`{"nodes":[{"name":"a\\x00b"}]}`, `{"nodes":[{"name":"a\\"}]}`, `{"nodes":[{"name":"a\\q"}]}`,
		`{"nodes":[{"name":"a\\u00e9\\n\\\\"}]}`, `{"nodes":[{"NAME":"a","Type":"dir"}]}`,
		`{"nodes":[{"name":"a","name":"b"}]}`, `{"nodes":[{"name":"a","path":"p","Path":"q"}]}`,
		`{"nodes":[{"name":"a","extended_attributes":[{"name":"x","value":"AA=="}],"generic_attributes":{"k":[1,2]}}]}`,
		`{"nodes":[{"name":"a","metadataNodeBase":{"name":"z"}}]}`,
	} {
		count(s020Compare(t, "hand-made", []byte(payload)))
	}
	t.Logf("compared: %d trees accepted by stock, %d rejected by stock; no difference", accepted, rejected)
}

// s020NameLeniencyCase reports whether stock fails on "Unquote" for the payload.
func s020NameLeniencyCase(payload []byte) bool {
	return strings.Contains(s020Stock(payload).nodeErr, "Unquote")
}

func TestWitnessS020MetadataTreeNameLeniency(t *testing.T) {
	for _, tc := range []struct{ desc, tree string }{
		// JSON "a\"b" decodes to a"b: a raw quote, no backslash
		{"raw double quote in name", `{"nodes":[{"name":"a\"b","type":"symlink","linktarget":"/etc/passwd"}]}`},
		// JSON "a\nb" decodes to a<LF>b: a raw newline, no backslash
		{"raw newline in name", `{"nodes":[{"name":"a\nb","type":"dir"}]}`},
		// control: what restic writes for the name a"b
		{"control: restic-written quote", `{"nodes":[{"name":"a\\\"b","type":"file","content":[]}]}`},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			want := s020Stock([]byte(tc.tree))
			got := s020Metadata([]byte(tc.tree))
			var names []string
			for _, n := range got.nodes {
				names = append(names, n.Name)
			}
			t.Logf("stock decoder:     %d nodes, error %q", len(want.nodes), want.nodeErr)
			t.Logf("LoadMetadataTree:  %d nodes %q, error %q", len(got.nodes), names, got.nodeErr)
			if (want.nodeErr == "") != (got.nodeErr == "") {
				t.Errorf("DIVERGENCE: stock rejects this tree (%q), LoadMetadataTree accepts it with names %q", want.nodeErr, names)
			}
		})
	}
}
