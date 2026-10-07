package index

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type replacedIndexFiles map[restic.ID][]byte

func (f replacedIndexFiles) Connections() uint { return 2 }
func (f replacedIndexFiles) List(ctx context.Context, typ restic.FileType, consume func(restic.ID, int64) error) error {
	if typ != restic.IndexFile {
		return fmt.Errorf("unexpected type %v", typ)
	}
	for id, content := range f {
		if err := consume(id, int64(len(content))); err != nil {
			return err
		}
	}
	return ctx.Err()
}
func (f replacedIndexFiles) LoadUnpacked(ctx context.Context, typ restic.FileType, id restic.ID) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if typ != restic.IndexFile {
		return nil, fmt.Errorf("unexpected type %v", typ)
	}
	content, ok := f[id]
	if !ok {
		return nil, fmt.Errorf("missing %s", id)
	}
	return bytes.Clone(content), nil
}

// Incremental Load explicitly supports replacement of old index files (e.g.
// after a concurrent prune). The number of live files/blobs remains one.
func TestIndexNotesReleasedOnReload(t *testing.T) {
	mi := NewMasterIndex()
	for round := 0; round < 100; round++ {
		idx := NewIndex()
		id := restic.Hash([]byte(fmt.Sprintf("generation %d", round)))
		idx.StorePack(id, pack.Blobs{{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: id}, Length: 64}})
		var encoded bytes.Buffer
		if err := idx.Encode(&encoded); err != nil {
			t.Fatal(err)
		}
		fileID := restic.Hash(encoded.Bytes())
		if err := mi.Load(context.Background(), replacedIndexFiles{fileID: encoded.Bytes()}, restic.NoopCounter, nil); err != nil {
			t.Fatal(err)
		}
		if len(mi.IDs()) != 1 || mi.idx[0].Len(restic.DataBlob) != 1 {
			t.Fatal("live index grew; probe did not isolate historical metadata")
		}
	}
	if len(mi.fileNotes) != 1 {
		t.Fatalf("one live index/blob retains %d historical index notes and their pack-ID slices", len(mi.fileNotes))
	}
}
