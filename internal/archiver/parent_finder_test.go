package archiver

import (
	"context"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

type finderPayloadLoader struct{ payload []byte }

func (l *finderPayloadLoader) LoadBlob(_ context.Context, _ restic.BlobHandle, _ []byte) ([]byte, error) {
	return l.payload, nil
}

func TestParentFinderRetainedAndFallback(t *testing.T) {
	payload := encodeReuseTree(t, &data.Node{Name: "a", Type: data.NodeTypeFile}, &data.Node{Name: "c", Type: data.NodeTypeFile})
	for _, retain := range []bool{false, true} {
		budget := new(parentTreeBudget)
		var parent *parentTreeBytes
		if retain {
			parent = budget.retain(restic.Hash(payload), payload)
		}
		loader := &finderPayloadLoader{payload: payload}
		it, err := data.LoadParentTree(t.Context(), loader, restic.Hash(payload))
		if err != nil {
			t.Fatal(err)
		}
		finder := parentTreeFinder(it, parent)
		for _, name := range []string{"a", "b", "c", "d"} {
			n, err := finder.Find(name)
			if err != nil {
				t.Fatal(err)
			}
			var want *data.Node
			if name == "a" || name == "c" {
				want = &data.Node{Name: name, Type: data.NodeTypeFile}
			}
			if !reflect.DeepEqual(n, want) {
				t.Fatalf("retained=%v Find(%q): %+v", retain, name, n)
			}
		}
		finder.Close()
		if retain && budget.used.Load() != int64(cap(payload)) {
			t.Fatal("finder close released tree-saver ownership")
		}
		parent.release()
		if budget.used.Load() != 0 {
			t.Fatal("retention leaked")
		}
	}
}
