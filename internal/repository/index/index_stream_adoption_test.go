package index

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/restic/restic/internal/restic"
)

type canonicalAdoptionLoader struct {
	input  string
	id     restic.ID
	result *Index
}

func (r *canonicalAdoptionLoader) Connections() uint { return 2 }
func (r *canonicalAdoptionLoader) List(_ context.Context, t restic.FileType, fn func(restic.ID, int64) error) error {
	if t != restic.IndexFile {
		return fmt.Errorf("unexpected list %v", t)
	}
	return fn(r.id, int64(len(r.input)))
}
func (r *canonicalAdoptionLoader) LoadUnpacked(context.Context, restic.FileType, restic.ID) ([]byte, error) {
	return nil, fmt.Errorf("stream capability was not used")
}
func (r *canonicalAdoptionLoader) LoadIndexStream(_ context.Context, id restic.ID) (*Index, []byte, error) {
	idx, ok := DecodeCanonicalIndexStream(func() (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(r.input)), nil }, id)
	if !ok {
		return nil, nil, fmt.Errorf("canonical stream rejected")
	}
	r.result = idx
	return idx, nil, nil
}
func TestCanonicalStreamFreshLoadAdoption(t *testing.T) {
	var document strings.Builder
	fmt.Fprintf(&document, `{"packs":[{"id":"%s","blobs":[`, restic.Hash([]byte("pack")))
	for i := 0; i < 10000; i++ {
		if i > 0 {
			document.WriteByte(',')
		}
		fmt.Fprintf(&document, `{"id":"%s","type":"data","offset":%d,"length":33}`, restic.Hash([]byte(fmt.Sprint(i))), i*33)
	}
	document.WriteString(`]}]}`)
	for _, callback := range []bool{false, true} {
		loader := &canonicalAdoptionLoader{input: document.String(), id: restic.Hash([]byte("index"))}
		mi := NewMasterIndex()
		calls := 0
		var cb func(restic.ID, *Index, error) error
		if callback {
			cb = func(_ restic.ID, idx *Index, err error) error {
				calls++
				if err != nil || idx == nil {
					t.Fatalf("idx=%v err=%v", idx, err)
				}
				return nil
			}
		}
		if err := mi.Load(context.Background(), loader, restic.NoopCounter, cb); err != nil {
			t.Fatal(err)
		}
		if loader.result == nil {
			t.Fatal("stream never dispatched")
		}
		if callback {
			if calls != 1 || mi.idx[0] == loader.result {
				t.Fatal("callback ownership was adopted")
			}
		} else if mi.idx[0] != loader.result {
			t.Fatal("exclusive fresh-load result was not adopted")
		}
		count := 0
		for range mi.Values() {
			count++
		}
		if count != 10000 {
			t.Fatalf("lost entries: %d", count)
		}
	}
}
