package restorer

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestRestoreSettledAssociationsRetireAfterCallback(t *testing.T) {
	for _, damaged := range []bool{false, true} {
		ctx := context.Background()
		first := restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("first"))}
		last := restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("last"))}
		f1 := &fileInfo{location: "first", size: 5}
		f2 := &fileInfo{location: "last", size: 4}
		blobs := blobToFileOffsetsMapping{
			first.ID: {files: map[*fileInfo][]int64{f1: {0}}, blob: first},
			last.ID:  {files: map[*fileInfo][]int64{f2: {0}}, blob: last},
		}
		processed := restic.NewBlobSet()
		failure := errors.New("late backend failure")
		corruption := errors.New("blob authentication failed")
		var reported []string
		loader := func(_ context.Context, _ restic.ID, handles []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
			if len(handles) != 2 {
				t.Fatalf("lost admission: %d handles", len(handles))
			}
			var err error
			if damaged {
				err = corruption
			}
			if err := cb(first, []byte("first"), err); err != nil {
				t.Fatal(err)
			}
			if _, live := blobs[first.ID]; live {
				t.Fatal("settled blob still owns file associations")
			}
			if _, live := blobs[last.ID]; !live {
				t.Fatal("unfinished blob lost diagnostic owner")
			}
			return failure
		}
		r := newFileRestorer(t.TempDir(), loader, nil, 2, false, false, nil, nil, restic.ID{})
		defer r.filesWriter.flush()
		r.Error = func(path string, err error) error { reported = append(reported, path); return nil }
		err := r.downloadBlobs(ctx, restic.ID{}, blobs, processed)
		if !errors.Is(err, failure) {
			t.Fatalf("late error lost: %v", err)
		}
		if err := r.reportError(blobs, processed, err); err != nil {
			t.Fatal(err)
		}
		want := 1
		if damaged {
			want = 2
		}
		if len(reported) != want || reported[len(reported)-1] != "last" {
			t.Fatalf("wrong error settlement: %v", reported)
		}
		if !damaged {
			data, err := os.ReadFile(filepath.Join(r.dst, "first"))
			if err != nil || string(data) != "first" {
				t.Fatalf("completed write lost: %q %v", data, err)
			}
		}
	}
}

func TestRestorePackPlanningOwnerTransferredBeforeLoad(t *testing.T) {
	repo := newTestRepo([]TestFile{{name: "single", blobs: []TestBlob{{data: "contents", pack: "p"}}}})
	file := repo.files[0]
	packID := repo.Lookup(restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("contents"))})[0].PackID()
	p := &packInfo{id: packID, files: map[*fileInfo]struct{}{file: {}}}
	loader := func(ctx context.Context, id restic.ID, handles []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
		if p.files != nil {
			t.Fatal("planning owner remains live during load")
		}
		return repo.loader(ctx, id, handles, cb)
	}
	r := newFileRestorer(t.TempDir(), loader, repo.Lookup, 2, false, false, nil, nil, restic.ID{})
	defer r.filesWriter.flush()
	if err := r.downloadPack(context.Background(), p); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreSettledAssociationRetiresOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("x"))}
	f := &fileInfo{location: "cancelled", size: 1}
	blobs := blobToFileOffsetsMapping{h.ID: {files: map[*fileInfo][]int64{f: {0, 0}}, blob: h}}
	loader := func(_ context.Context, _ restic.ID, _ []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
		cancel()
		return cb(h, []byte("x"), nil)
	}
	r := newFileRestorer(t.TempDir(), loader, nil, 2, false, false, nil, nil, restic.ID{})
	defer r.filesWriter.flush()
	if err := r.downloadBlobs(ctx, restic.ID{}, blobs, restic.NewBlobSet()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if len(blobs) != 0 {
		t.Fatal("cancelled callback left settled association")
	}
}

func TestRestoreSettledAssociationRetiresWhenDiagnosticStopsLoad(t *testing.T) {
	ctx := context.Background()
	h := restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("bad"))}
	f := &fileInfo{location: "damaged", size: 3}
	blobs := blobToFileOffsetsMapping{h.ID: {files: map[*fileInfo][]int64{f: {0}}, blob: h}}
	corruption := errors.New("authentication failed")
	stop := errors.New("diagnostic requests stop")
	processed := restic.NewBlobSet()
	loader := func(_ context.Context, _ restic.ID, _ []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
		return cb(h, nil, corruption)
	}
	r := newFileRestorer(t.TempDir(), loader, nil, 2, false, false, nil, nil, restic.ID{})
	defer r.filesWriter.flush()
	reports := 0
	r.Error = func(path string, err error) error {
		reports++
		if path != f.location || !errors.Is(err, corruption) {
			t.Fatalf("diagnostic changed: %s %v", path, err)
		}
		return stop
	}
	err := r.downloadBlobs(ctx, restic.ID{}, blobs, processed)
	if !errors.Is(err, stop) || len(blobs) != 0 || !processed.Has(h) {
		t.Fatalf("failed diagnostic settlement: err=%v live=%d processed=%v", err, len(blobs), processed.Has(h))
	}
	if err := r.reportError(blobs, processed, err); err != nil || reports != 1 {
		t.Fatalf("callback diagnostic repeated: err=%v reports=%d", err, reports)
	}
}

// Refusing the first diagnostic aborts the load. Retirement must not discard
// the independent mapping that has not received a callback yet.
func TestRestoreSettledAssociationDiagnosticRefusal(t *testing.T) {
	first := restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("first"))}
	last := restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte("last"))}
	f1 := &fileInfo{location: "first", size: 5}
	f2 := &fileInfo{location: "last", size: 4}
	blobs := blobToFileOffsetsMapping{
		first.ID: {files: map[*fileInfo][]int64{f1: {0}}, blob: first},
		last.ID:  {files: map[*fileInfo][]int64{f2: {0}}, blob: last},
	}
	corruption := errors.New("damaged blob")
	refusal := errors.New("diagnostic refused")
	calls := 0
	loader := func(_ context.Context, _ restic.ID, _ []restic.BlobHandle, cb func(restic.BlobHandle, []byte, error) error) error {
		return cb(first, nil, corruption)
	}
	r := newFileRestorer(t.TempDir(), loader, nil, 2, false, false, nil, nil, restic.ID{})
	defer r.filesWriter.flush()
	r.Error = func(path string, err error) error {
		calls++
		if path != "first" || !errors.Is(err, corruption) {
			t.Fatalf("diagnostic lost: %s %v", path, err)
		}
		return refusal
	}
	processed := restic.NewBlobSet()
	if err := r.downloadBlobs(context.Background(), restic.ID{}, blobs, processed); !errors.Is(err, refusal) {
		t.Fatalf("callback refusal lost: %v", err)
	}
	if calls != 1 || !processed.Has(first) || processed.Has(last) {
		t.Fatal("callback settlement duplicated or lost")
	}
	if _, live := blobs[first.ID]; live {
		t.Fatal("refused callback retains association")
	}
	if _, live := blobs[last.ID]; !live {
		t.Fatal("unfinished diagnostic owner retired")
	}
}
