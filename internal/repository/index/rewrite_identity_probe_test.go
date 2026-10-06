package index

import (
	"context"
	"errors"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestRewriteIdentityProbe(t *testing.T) {
	repo := &mergeTestRepo{files: map[restic.ID][]byte{}, loaded: map[restic.ID]int{}}
	idx := NewIndex()
	id := restic.ID{1}
	idx.StorePack(id, pack.Blobs{{BlobHandle: restic.BlobHandle{ID: restic.ID{2}, Type: restic.DataBlob}, Length: 60}})
	idx.Finalize()
	oldID, err := idx.SaveIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	mi := NewMasterIndex()
	if err := mi.Load(context.Background(), repo, restic.NoopCounter, nil); err != nil {
		t.Fatal(err)
	}
	if err := mi.Rewrite(context.Background(), repo, nil, nil, nil, MasterIndexRewriteOpts{}); err != nil {
		t.Fatal(err)
	}
	if len(repo.files) == 0 {
		t.Fatalf("content-identical output %v was deleted as obsolete", oldID)
	}
}

type failedRewriteSaver struct{ *mergeTestRepo }

func (r failedRewriteSaver) SaveUnpacked(context.Context, restic.FileType, []byte) (restic.ID, error) {
	return restic.ID{}, errors.New("injected rewrite save failure")
}

func TestRewriteIdentitySaveFailureLeavesInput(t *testing.T) {
	repo := &mergeTestRepo{files: map[restic.ID][]byte{}, loaded: map[restic.ID]int{}}
	idx := NewIndex()
	idx.StorePack(restic.ID{1}, pack.Blobs{{BlobHandle: restic.BlobHandle{ID: restic.ID{2}, Type: restic.DataBlob}, Length: 60}})
	idx.Finalize()
	id, err := idx.SaveIndex(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}
	mi := NewMasterIndex()
	if err := mi.Load(context.Background(), repo, restic.NoopCounter, nil); err != nil {
		t.Fatal(err)
	}
	err = mi.Rewrite(context.Background(), failedRewriteSaver{repo}, nil, nil, nil, MasterIndexRewriteOpts{})
	if err == nil {
		t.Fatal("save failure was lost")
	}
	if _, ok := repo.files[id]; !ok {
		t.Fatal("failed save removed the original index")
	}
}
