package data

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

func referenceTree(t testing.TB, nodes []*Node) []byte {
	t.Helper()
	b := NewTreeJSONBuilder()
	for _, n := range nodes {
		if err := b.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	buf, err := b.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return buf
}

func TestTreeReferenceFragments(t *testing.T) {
	nodes := []*Node{
		{Name: "a", Type: NodeTypeFile, ModTime: time.Date(2026, 1, 1, 1, 1, 1, 0, time.UTC)},
		{Name: "b\n\\\"<&", Type: NodeTypeSymlink, LinkTarget: string([]byte{0xff, 0xfe}), GenericAttributes: map[GenericAttributeType]json.RawMessage{"other.value": json.RawMessage(` { "x": 1 } `)}},
		{Name: "c", Type: NodeTypeFile, Content: restic.IDs{restic.Hash([]byte("content"))}},
	}
	canonical := referenceTree(t, nodes)
	for _, tc := range []struct {
		name    string
		parent  []byte
		current []*Node
	}{
		{"equal", canonical, nodes},
		{"empty-parent", nil, nodes},
		{"empty-current", canonical, nil},
		{"changed-first", append([]byte(" "), canonical...), nodes},
		{"trailing-parent", append(bytes.Clone(canonical), ' '), nodes},
		{"truncated-parent", canonical[:len(canonical)-1], nodes},
		{"omitted-last", canonical, nodes[:2]},
		{"added-last", referenceTree(t, nodes[:2]), nodes},
		{"changed-fallback", referenceTree(t, []*Node{nodes[0], {Name: nodes[1].Name, Type: NodeTypeFile}, nodes[2]}), nodes},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := bytes.Clone(tc.parent)
			got := NewTreeJSONBuilderWithReference(tc.parent)
			for _, n := range tc.current {
				if err := got.AddNode(n); err != nil {
					t.Fatal(err)
				}
			}
			if got.Count() != len(tc.current) {
				t.Fatal("node count changed")
			}
			buf, err := got.Finalize()
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(buf, referenceTree(t, tc.current)) {
				t.Fatalf("different bytes: %q", buf)
			}
			if !bytes.Equal(tc.parent, original) {
				t.Fatal("parent modified")
			}
			if tc.name == "equal" && &buf[0] != &tc.parent[0] {
				t.Fatal("matching tree copied")
			}
		})
	}
}

func TestTreeReferenceErrorOrder(t *testing.T) {
	for _, nodes := range [][]*Node{
		{{Name: "a"}, {Name: "a"}},
		{{Name: "a"}, {Name: "b", GenericAttributes: map[GenericAttributeType]json.RawMessage{"invalid": json.RawMessage(`!`)}}},
	} {
		ordinary := NewTreeJSONBuilder()
		matching := NewTreeJSONBuilderWithReference(referenceTree(t, []*Node{{Name: "a"}}))
		for _, node := range nodes {
			want, got := ordinary.AddNode(node), matching.AddNode(node)
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("error differs: %v / %v", got, want)
			}
			if got != nil {
				break
			}
		}
	}
}

var referenceBenchmarkID restic.ID

// Synthetic discrimination of encoding/buffer/hash work, not workload timing.
func BenchmarkTreeReference(b *testing.B) {
	for _, count := range []int{16, 128, 1024} {
		nodes := make([]*Node, count)
		for i := range nodes {
			nodes[i] = &Node{Name: fmt.Sprintf("file-%06d", i), Type: NodeTypeFile, User: strings.Repeat("u", 32), Content: restic.IDs{restic.Hash([]byte("blob"))}}
		}
		parent := referenceTree(b, nodes)
		parentID := restic.Hash(parent)
		for _, mode := range []string{"ordinary", "complete-compare", "fragment-compare"} {
			b.Run(fmt.Sprintf("%d/%s", count, mode), func(b *testing.B) {
				b.ReportAllocs()
				b.SetBytes(int64(len(parent)))
				for i := 0; i < b.N; i++ {
					builder := NewTreeJSONBuilder()
					if mode == "fragment-compare" {
						builder = NewTreeJSONBuilderWithReference(parent)
					}
					for _, n := range nodes {
						if err := builder.AddNode(n); err != nil {
							b.Fatal(err)
						}
					}
					buf, err := builder.Finalize()
					if err != nil {
						b.Fatal(err)
					}
					if mode != "ordinary" && bytes.Equal(buf, parent) {
						referenceBenchmarkID = parentID
					} else {
						referenceBenchmarkID = restic.Hash(buf)
					}
				}
			})
		}
	}
}

func BenchmarkTreeReferencePartial(b *testing.B) {
	for _, count := range []int{128, 1024} {
		nodes := make([]*Node, count)
		for i := range nodes {
			nodes[i] = &Node{Name: fmt.Sprintf("file-%06d", i), Type: NodeTypeFile, Content: restic.IDs{restic.Hash([]byte("blob"))}}
		}
		parent := referenceTree(b, nodes)
		for _, position := range []int{0, count / 2, count - 1} {
			current := append([]*Node(nil), nodes...)
			changed := *current[position]
			changed.Size = 1
			current[position] = &changed
			expected := referenceTree(b, current)
			for _, mode := range []string{"complete-compare", "fragment-compare"} {
				b.Run(fmt.Sprintf("%d/change-%d/%s", count, position, mode), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						builder := NewTreeJSONBuilder()
						if mode == "fragment-compare" {
							builder = NewTreeJSONBuilderWithReference(parent)
						}
						for _, node := range current {
							if err := builder.AddNode(node); err != nil {
								b.Fatal(err)
							}
						}
						buf, err := builder.Finalize()
						if err != nil {
							b.Fatal(err)
						}
						if !bytes.Equal(buf, expected) {
							b.Fatal("changed bytes differ")
						}
						if bytes.Equal(buf, parent) {
							b.Fatal("changed tree reused")
						}
						referenceBenchmarkID = restic.Hash(buf)
					}
				})
			}
		}
	}
}
