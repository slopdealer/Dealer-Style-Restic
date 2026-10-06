package index

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type rewriteFaultRepo struct {
	*mergeTestRepo
	faultMutex             sync.Mutex
	saveCalls, removeCalls int
	failSave, failRemove   int
	outputs                restic.IDSet
}

func (r *rewriteFaultRepo) SaveUnpacked(ctx context.Context, typ restic.FileType, buf []byte) (restic.ID, error) {
	r.faultMutex.Lock()
	defer r.faultMutex.Unlock()
	r.saveCalls++
	if r.saveCalls == r.failSave {
		return restic.ID{}, errors.New("injected later save failure")
	}
	id, err := r.mergeTestRepo.SaveUnpacked(ctx, typ, buf)
	if err == nil {
		r.outputs.Insert(id)
	}
	return id, err
}

func (r *rewriteFaultRepo) RemoveUnpacked(ctx context.Context, typ restic.FileType, id restic.ID) error {
	r.faultMutex.Lock()
	defer r.faultMutex.Unlock()
	r.removeCalls++
	if r.removeCalls == r.failRemove {
		return errors.New("injected later deletion failure")
	}
	return r.mergeTestRepo.RemoveUnpacked(ctx, typ, id)
}

func rewriteInterruptionInput(t *testing.T) (*rewriteFaultRepo, restic.IDSet) {
	t.Helper()
	base := &mergeTestRepo{files: map[restic.ID][]byte{}, loaded: map[restic.ID]int{}}
	inputs := restic.NewIDSet()
	for i := byte(1); i <= 3; i++ {
		idx := NewIndex()
		idx.StorePack(restic.ID{i}, pack.Blobs{{BlobHandle: restic.BlobHandle{ID: restic.ID{i, 1}, Type: restic.DataBlob}, Length: 60}})
		idx.Finalize()
		id, err := idx.SaveIndex(context.Background(), base)
		if err != nil {
			t.Fatal(err)
		}
		inputs.Insert(id)
	}
	oldFull := Full
	Full = func(idx *Index) bool { return idx.Len(restic.DataBlob) >= 2 }
	t.Cleanup(func() { Full = oldFull })
	return &rewriteFaultRepo{mergeTestRepo: base, outputs: restic.NewIDSet()}, inputs
}

func TestRewriteSavedPartialSaveNoDeletes(t *testing.T) {
	repo, inputs := rewriteInterruptionInput(t)
	repo.failSave = 2
	mi := NewMasterIndex()
	if err := mi.Load(context.Background(), repo, restic.NoopCounter, nil); err != nil {
		t.Fatal(err)
	}
	if err := mi.Rewrite(context.Background(), repo, nil, nil, nil, MasterIndexRewriteOpts{}); err == nil {
		t.Fatal("later save failure lost")
	}
	if repo.saveCalls != 2 || len(repo.outputs) != 1 || repo.removeCalls != 0 {
		t.Fatalf("save/deletion settlement: %d saves, %d outputs, %d removes", repo.saveCalls, len(repo.outputs), repo.removeCalls)
	}
	for id := range inputs {
		if _, ok := repo.files[id]; !ok {
			t.Fatal("failed rewrite removed an input")
		}
	}
	for id := range repo.outputs {
		if _, ok := repo.files[id]; !ok {
			t.Fatal("failed rewrite removed a completed output")
		}
	}
}

func TestRewriteSavedDeletionFailureOutputsRemain(t *testing.T) {
	repo, _ := rewriteInterruptionInput(t)
	repo.failRemove = 2
	mi := NewMasterIndex()
	if err := mi.Load(context.Background(), repo, restic.NoopCounter, nil); err != nil {
		t.Fatal(err)
	}
	if err := mi.Rewrite(context.Background(), repo, nil, nil, nil, MasterIndexRewriteOpts{}); err == nil {
		t.Fatal("later deletion failure lost")
	}
	if repo.removeCalls < 2 || len(repo.outputs) != 2 {
		t.Fatal("did not exercise partial deletion")
	}
	for id := range repo.outputs {
		if _, ok := repo.files[id]; !ok {
			t.Fatal("partial deletion removed a completed output")
		}
	}
	after := NewMasterIndex()
	if err := after.Load(context.Background(), repo, restic.NoopCounter, nil); err != nil {
		t.Fatal(err)
	}
	if after.idx[0].Len(restic.DataBlob) != 3 {
		t.Fatal("partial deletion lost blob locations")
	}
}
