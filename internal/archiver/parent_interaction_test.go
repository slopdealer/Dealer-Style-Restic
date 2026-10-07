package archiver

import (
	"bytes"
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/restic"
)

// Embedding supplies the unused repository operations. These tests exercise
// both archiver hooks against the public loader boundary and original iterator.
type parentInteractionRepo struct {
	archiverRepo
	payload []byte
	err     error
	calls   int
	id      restic.ID
}

func (r *parentInteractionRepo) LoadBlob(ctx context.Context, h restic.BlobHandle, scratch []byte) ([]byte, error) {
	r.calls++
	if h.Type != restic.TreeBlob || h.ID != r.id || scratch != nil {
		panic("parent loader boundary changed")
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return bytes.Clone(r.payload), r.err
}

func (r *parentInteractionRepo) LookupBlobSize(restic.BlobHandle) (uint, bool) { return 1, true }

func parentInteractionRead(it data.TreeNodeIterator) ([]*data.Node, error) {
	var nodes []*data.Node
	if it == nil {
		return nodes, nil
	}
	for item := range it {
		if item.Error != nil {
			return nodes, item.Error
		}
		nodes = append(nodes, item.Node)
	}
	return nodes, nil
}

func TestParentArchiverSerializationInteraction(t *testing.T) {
	id := restic.Hash([]byte("blob"))
	for _, escaped := range []bool{false, true} {
		builder := data.NewTreeJSONBuilder()
		for i := range 24 {
			n := &data.Node{Name: fmt.Sprintf("%03d", i), Type: data.NodeTypeFile,
				ModTime: time.Date(2024, 2, 3, 4, 5, 6, 7, time.UTC), Content: restic.IDs{id}, Size: 19}
			if escaped {
				n.User = "<user>\u2028"
				n.LinkTarget = string([]byte{255, 254})
			}
			if err := builder.AddNode(n); err != nil {
				t.Fatal(err)
			}
		}
		payload, err := builder.Finalize()
		if err != nil {
			t.Fatal(err)
		}
		for _, suffix := range []string{"", `,"mode":"bad"`} {
			input := bytes.Clone(payload)
			if suffix != "" {
				// Preserve successful prefix consumption before a semantic failure.
				input = append(input[:len(input)-3], []byte(`,{"name":"last"`+suffix+`}]}`)...)
			}
			ordinary, err := data.NewTreeNodeIterator(bytes.NewReader(input))
			if err != nil {
				t.Fatal(err)
			}
			want, we := parentInteractionRead(ordinary)
			for _, root := range []bool{false, true} {
				r := &parentInteractionRepo{payload: input, id: restic.Hash(input)}
				arch := &Archiver{Repo: r, Error: func(_ string, err error) error { t.Fatalf("unexpected warning: %v", err); return err }}
				var it data.TreeNodeIterator
				if root {
					it = arch.loadParentTree(t.Context(), &data.Snapshot{Tree: &r.id})
				} else {
					it, err = arch.loadSubtree(t.Context(), &data.Node{Type: data.NodeTypeDir, Subtree: &r.id})
					if err != nil {
						t.Fatal(err)
					}
				}
				got, ge := parentInteractionRead(it)
				if r.calls != 1 || !reflect.DeepEqual(got, want) || fmt.Sprintf("%T:%v", ge, ge) != fmt.Sprintf("%T:%v", we, we) {
					t.Fatalf("root=%v escaped=%v: calls=%d errors=%v/%v", root, escaped, r.calls, ge, we)
				}
			}
		}
	}
}

func TestParentArchiverFailureSettlement(t *testing.T) {
	for _, mode := range []string{"load", "framing", "cancel"} {
		ctx, cancel := context.WithCancel(t.Context())
		r := &parentInteractionRepo{payload: []byte(`{"nodes":[]}`), id: restic.Hash([]byte(mode))}
		if mode == "load" {
			r.err = errors.New("authoritative load failure")
		}
		if mode == "framing" {
			r.payload = []byte(`{"nodes":null}`)
		}
		if mode == "cancel" {
			cancel()
		}
		_, ordinaryErr := data.LoadTree(ctx, r, r.id)
		r.calls = 0
		warnings := 0
		arch := &Archiver{Repo: r}
		want := arch.wrapLoadTreeError(r.id, ordinaryErr).Error()
		arch.Error = func(item string, err error) error {
			warnings++
			if item != "/" || err.Error() != "/: "+want {
				t.Fatalf("warning %s: %v", item, err)
			}
			return err
		}
		if arch.loadParentTree(ctx, &data.Snapshot{Tree: &r.id}) != nil || warnings != 1 || r.calls != 1 {
			t.Fatal("root failure settlement")
		}
		_, err := arch.loadSubtree(ctx, &data.Node{Type: data.NodeTypeDir, Subtree: &r.id})
		if err == nil || err.Error() != want || warnings != 1 || r.calls != 2 {
			t.Fatal("subtree failure settlement")
		}
		cancel()
	}
}
