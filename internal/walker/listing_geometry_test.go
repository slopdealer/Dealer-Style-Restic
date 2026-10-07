package walker

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// This control retains the candidate's lexical admission, batch array, node and
// byte limits, and oversized-node path. Only the scheduling differs.
func listingSerialGeometry(source listingSource) data.TreeNodeIterator {
	return func(yield func(data.NodeOrError) bool) {
		defer source.close()
		for {
			batch := readListingBatch(source)
			for i := 0; i < batch.count; i++ {
				item := batch.items[i]
				batch.items[i] = data.NodeOrError{}
				if !yield(item) || item.Error != nil {
					return
				}
			}
			if batch.end {
				return
			}
			if batch.synchronous {
				item, ok := source.next()
				if !ok || !yield(item) || item.Error != nil {
					return
				}
			}
		}
	}
}

func BenchmarkListingMatchedGeometry(b *testing.B) {
	for _, nodes := range []int{32, 512, 4096} {
		tree := struct {
			Nodes []*data.Node `json:"nodes"`
		}{Nodes: make([]*data.Node, 0, nodes)}
		for i := 0; i < nodes; i++ {
			tree.Nodes = append(tree.Nodes, &data.Node{Name: fmt.Sprintf("file-%05d", i), Type: data.NodeTypeFile, Size: 4096, Mode: 0644})
		}
		payload, err := json.Marshal(tree)
		if err != nil {
			b.Fatal(err)
		}
		for _, arm := range []string{"serial", "prepared"} {
			b.Run(fmt.Sprintf("%d/%s", nodes, arm), func(b *testing.B) {
				b.ReportAllocs()
				for i := 0; i < b.N; i++ {
					loader := &listingTestLoader{payload: payload}
					r, err := data.LoadTreeNodeReader(context.Background(), loader, restic.ID{})
					if err != nil {
						b.Fatal(err)
					}
					source := listingSource{next: r.Next, size: r.PreparationSize, close: r.Close}
					p := newListingPreparation(nil)
					iterator := listingSerialGeometry(source)
					if arm == "prepared" {
						iterator = p.wrap(context.Background(), source)
					}
					count := 0
					for item := range iterator {
						if item.Error != nil {
							b.Fatal(item.Error)
						}
						// Same output interpretation work in each arm; no sleep or dummy work.
						out, err := json.Marshal(item.Node)
						if err != nil || len(out) == 0 {
							b.Fatal(err)
						}
						count++
					}
					p.Close()
					if count != nodes {
						b.Fatal(count)
					}
				}
			})
		}
	}
}
