package restorer

import (
	"context"
	"errors"
	"os"
	"sync/atomic"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestPlannedSingletonSharedContent(t *testing.T) {
	data := []byte("shared singleton")
	id := restic.Hash(data)
	packID := restic.Hash([]byte("selected pack"))
	otherID := restic.Hash([]byte("duplicate pack"))
	var lookups, loads atomic.Int32
	lookup := func(h restic.BlobHandle) []restic.PackBlob {
		lookups.Add(1)
		return []restic.PackBlob{
			boundedTestBlob{id: h.ID, pack: packID, size: uint(len(data))},
			boundedTestBlob{id: h.ID, pack: otherID, size: uint(len(data))},
		}
	}
	loader := func(_ context.Context, pack restic.ID, handles []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
		loads.Add(1)
		if pack != packID || len(handles) != 1 || handles[0] != (restic.BlobHandle{ID: id, Type: restic.DataBlob}) {
			t.Errorf("incorrect selected pack or handles: %v %v", pack, handles)
		}
		return cb(handles[0], data, nil)
	}
	r := newFileRestorer(t.TempDir(), loader, lookup, 2, false, false, nil, nil, restic.ID{})
	for _, name := range []string{"a", "b"} {
		r.addFile(name, restic.IDs{id}, int64(len(data)), nil)
	}
	if err := r.restoreFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	if lookups.Load() != 2 || loads.Load() != 1 {
		t.Fatalf("lookups=%d loads=%d; want one planning lookup per file and one load", lookups.Load(), loads.Load())
	}
	for _, name := range []string{"a", "b"} {
		got, err := os.ReadFile(r.targetPath(name))
		if err != nil || string(got) != string(data) {
			t.Fatalf("%s: %q, %v", name, got, err)
		}
	}
}

func TestPlannedSingletonRequiresPlanning(t *testing.T) {
	id := restic.Hash([]byte("blob"))
	packID := restic.Hash([]byte("pack"))
	otherID := restic.Hash([]byte("other"))
	for _, matched := range []bool{false, true} {
		var lookups int
		lookup := func(h restic.BlobHandle) []restic.PackBlob {
			lookups++
			selected := otherID
			if matched {
				selected = packID
			}
			return []restic.PackBlob{boundedTestBlob{id: h.ID, pack: selected, size: 4}}
		}
		loader := func(_ context.Context, _ restic.ID, handles []restic.BlobHandle, _ func(restic.BlobHandle, []byte, error) error) error {
			if len(handles) != 0 {
				t.Fatalf("unplanned association bypassed membership: %v", handles)
			}
			return nil
		}
		r := newFileRestorer(t.TempDir(), loader, lookup, 2, false, false, nil, nil, restic.ID{})
		file := &fileInfo{blobs: restic.IDs{id}}
		if matched {
			file.state = &fileState{blobMatches: []bool{true}}
		}
		pack := &packInfo{id: packID, files: map[*fileInfo]struct{}{file: {}}}
		if err := r.downloadPack(context.Background(), pack); err != nil {
			t.Fatal(err)
		}
		if lookups != 1 {
			t.Fatalf("unplanned lookup count = %d", lookups)
		}
	}
}

func TestPlannedSingletonMixedFiles(t *testing.T) {
	repo := newTestRepo([]TestFile{
		{name: "single", blobs: []TestBlob{{data: "one", pack: "p"}}},
		{name: "multi", blobs: []TestBlob{{data: "two", pack: "p"}, {data: "three", pack: "p"}}},
		{name: "matched", blobs: []TestBlob{{data: "kept", pack: "p"}}},
	})
	var lookups atomic.Int32
	lookup := func(h restic.BlobHandle) []restic.PackBlob {
		lookups.Add(1)
		return repo.Lookup(h)
	}
	r := newFileRestorer(t.TempDir(), repo.loader, lookup, 2, false, false, nil, nil, restic.ID{})
	r.files = repo.files
	repo.files[2].state = &fileState{blobMatches: []bool{true}, sizeMatches: true}
	repo.files[2].size = 4
	if err := os.WriteFile(r.targetPath("matched"), []byte("kept"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := r.restoreFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Four planning lookups, and two retained delivery lookups for multi.
	if lookups.Load() != 6 {
		t.Fatalf("lookup count = %d, want 6", lookups.Load())
	}
	for name, want := range map[string]string{"single": "one", "multi": "twothree", "matched": "kept"} {
		got, err := os.ReadFile(r.targetPath(name))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q, %v", name, got, err)
		}
	}
}

func TestPlannedSingletonCanceledDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	id := restic.Hash([]byte("blob"))
	packID := restic.Hash([]byte("pack"))
	lookup := func(h restic.BlobHandle) []restic.PackBlob {
		return []restic.PackBlob{boundedTestBlob{id: h.ID, pack: packID, size: 4}}
	}
	loader := func(_ context.Context, _ restic.ID, handles []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
		cancel()
		if err := cb(handles[0], []byte("blob"), nil); err != context.Canceled {
			t.Errorf("callback after cancellation = %v", err)
		}
		return context.Canceled
	}
	r := newFileRestorer(t.TempDir(), loader, lookup, 2, false, false, nil, nil, restic.ID{})
	r.addFile("canceled", restic.IDs{id}, 4, nil)
	_ = r.restoreFiles(ctx)
	if _, err := os.Stat(r.targetPath("canceled")); !os.IsNotExist(err) {
		t.Fatalf("canceled callback created destination: %v", err)
	}
}

func TestPlannedSingletonUnknownAndCanceled(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		var loads int
		lookup := func(restic.BlobHandle) []restic.PackBlob { return nil }
		loader := func(context.Context, restic.ID, []restic.BlobHandle, func(restic.BlobHandle, []byte, error) error) error {
			loads++
			return nil
		}
		r := newFileRestorer(t.TempDir(), loader, lookup, 2, false, false, nil, nil, restic.ID{})
		r.addFile("unknown", restic.IDs{restic.Hash([]byte("missing"))}, 1, nil)
		ctx, cancel := context.WithCancel(context.Background())
		if canceled {
			cancel()
		}
		err := r.restoreFiles(ctx)
		cancel()
		if err == nil || loads != 0 || (canceled && !errors.Is(err, context.Canceled)) {
			t.Fatalf("canceled=%v err=%v loads=%d", canceled, err, loads)
		}
	}
}

func TestPlannedSingletonLoaderFailure(t *testing.T) {
	id := restic.Hash([]byte("blob"))
	packID := restic.Hash([]byte("pack"))
	want := errors.New("corrupt blob")
	lookup := func(h restic.BlobHandle) []restic.PackBlob {
		return []restic.PackBlob{boundedTestBlob{id: h.ID, pack: packID, size: 4}}
	}
	loader := func(_ context.Context, _ restic.ID, handles []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
		return want
	}
	r := newFileRestorer(t.TempDir(), loader, lookup, 2, false, false, nil, nil, restic.ID{})
	r.addFile("failed", restic.IDs{id}, 4, nil)
	if err := r.restoreFiles(context.Background()); err != want {
		t.Fatalf("error = %v, want %v", err, want)
	}
	if _, err := os.Stat(r.targetPath("failed")); !os.IsNotExist(err) {
		t.Fatalf("failed blob created destination: %v", err)
	}
}

func TestPlannedSingletonCallbackRefusal(t *testing.T) {
	id := restic.Hash([]byte("blob"))
	packID := restic.Hash([]byte("pack"))
	want := errors.New("corrupt blob")
	lookup := func(h restic.BlobHandle) []restic.PackBlob {
		return []restic.PackBlob{boundedTestBlob{id: h.ID, pack: packID, size: 4}}
	}
	var reports atomic.Int32
	loader := func(_ context.Context, _ restic.ID, handles []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
		err := cb(handles[0], nil, want)
		if err != want {
			t.Errorf("callback error = %v, want %v", err, want)
		}
		return err
	}
	r := newFileRestorer(t.TempDir(), loader, lookup, 2, false, false, nil, nil, restic.ID{})
	r.Error = func(_ string, err error) error {
		reports.Add(1)
		if err != want {
			t.Errorf("diagnostic = %v, want %v", err, want)
		}
		return err
	}
	r.addFile("failed", restic.IDs{id}, 4, nil)
	// Existing downloadPack reports only unsettled mappings after Load returns;
	// callback-settled errors have already reached Error and are not reported twice.
	_ = r.restoreFiles(context.Background())
	if reports.Load() != 1 {
		t.Fatalf("diagnostic count = %d, want one", reports.Load())
	}
	if _, err := os.Stat(r.targetPath("failed")); !os.IsNotExist(err) {
		t.Fatalf("failed blob created destination: %v", err)
	}
}
