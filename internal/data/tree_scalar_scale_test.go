package data

import (
	"bytes"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

// A directory-sized tree, with a controlled share of nodes taking fallback.
// Keep nodes and IDs outside timed regions: the comparison is serialization.
func scalarScaleNodes(count, fallbackPercent int) []Node {
	nodes := make([]Node, count)
	tm := time.Date(2024, 5, 6, 7, 8, 9, 123456789, time.UTC)
	ids := restic.IDs{restic.Hash([]byte("one")), restic.Hash([]byte("two"))}
	for i := range nodes {
		nodes[i] = Node{Name: fmt.Sprintf("file-%06d", i), Type: NodeTypeFile, Mode: 0644,
			UID: 1000, GID: 1000, User: "user", Group: "group", Size: uint64(i),
			ModTime: tm, AccessTime: tm, ChangeTime: tm, Content: ids}
		if i%100 < fallbackPercent {
			nodes[i].ExtendedAttributes = []ExtendedAttribute{{Name: "user.test", Value: []byte{0, 255}}}
		}
	}
	return nodes
}

func TestTreeScalarDirectoryScaleFrozen(t *testing.T) {
	for _, count := range []int{1, 86, 1024} {
		for _, fallback := range []int{0, 10, 100} {
			nodes := scalarScaleNodes(count, fallback)
			got := NewTreeJSONBuilder()
			var want bytes.Buffer
			want.WriteString(`{"nodes":[`)
			for i := range nodes {
				if err := got.AddNode(&nodes[i]); err != nil {
					t.Fatal(err)
				}
				encoded, err := scalarFrozenMarshal(nodes[i])
				if err != nil {
					t.Fatal(err)
				}
				// Apply Encoder's outer compaction/HTML escaping independently.
				if i > 0 {
					want.WriteByte(',')
				}
				if err := json.NewEncoder(&want).Encode(json.RawMessage(encoded)); err != nil {
					t.Fatal(err)
				}
				want.Truncate(want.Len() - 1)
			}
			want.WriteString("]}\n")
			out, err := got.Finalize()
			if err != nil || !bytes.Equal(out, want.Bytes()) || got.Count() != count {
				t.Fatalf("count=%d fallback=%d: frozen bytes/count differ", count, fallback)
			}
		}
	}
}

func BenchmarkTreeScalarDirectoryScale(b *testing.B) {
	for _, fallback := range []int{0, 10, 100} {
		nodes := scalarScaleNodes(86, fallback)
		for _, direct := range []bool{false, true} {
			b.Run(fmt.Sprintf("fallback=%d/append=%t", fallback, direct), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					builder := NewTreeJSONBuilder()
					for i := range nodes {
						var err error
						if direct {
							err = builder.AddNode(&nodes[i])
						} else {
							err = directScalarReferenceAdd(builder, &nodes[i])
						}
						if err != nil {
							b.Fatal(err)
						}
					}
					if _, err := builder.Finalize(); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
