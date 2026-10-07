package repository_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

type predecessorTwoConnectionBackend struct{ backend.Backend }

func (b *predecessorTwoConnectionBackend) Properties() backend.Properties {
	p := b.Backend.Properties()
	p.Connections = 2
	return p
}
func (b *predecessorTwoConnectionBackend) Unwrap() backend.Backend { return b.Backend }

type predecessorRepackUploader struct {
	restic.BlobSaverWithAsync
	calls   atomic.Int32
	held    chan struct{}
	next    chan struct{}
	release <-chan struct{}
	failure error
	mu      sync.Mutex
	saved   map[restic.BlobHandle]int
}

func (u *predecessorRepackUploader) SaveBlobAsync(ctx context.Context, kind restic.BlobType, buf []byte, id restic.ID, duplicate bool, cb func(restic.ID, bool, int, error)) {
	n := u.calls.Add(1)
	expected := append([]byte(nil), buf...)
	u.mu.Lock()
	u.saved[restic.BlobHandle{ID: id, Type: kind}]++
	u.mu.Unlock()
	if n == 2 {
		close(u.next)
	}
	u.BlobSaverWithAsync.SaveBlobAsync(ctx, kind, buf, id, duplicate, func(newID restic.ID, known bool, size int, err error) {
		if n == 1 {
			close(u.held)
			select {
			case <-u.release:
				if err == nil {
					err = u.failure
				}
			case <-ctx.Done():
				if err == nil {
					err = ctx.Err()
				}
			}
			if !bytes.Equal(buf, expected) {
				err = errors.New("borrowed plaintext escaped across pack reads")
			}
		}
		cb(newID, known, size, err)
	})
}

func TestRepackCompletionDuplicateOwnership(t *testing.T) {
	repository.TestAllVersions(t, func(t *testing.T, version uint) {
		repo, _ := repository.TestRepositoryWithBackend(t, &predecessorTwoConnectionBackend{Backend: repository.TestBackend(t)}, version, repository.Options{})
		keep := restic.NewBlobSet()
		for i := 0; i < 3; i++ {
			rtest.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
				for _, value := range []byte{42, byte(i + 1)} {
					id, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, bytes.Repeat([]byte{value}, 4096), restic.ID{}, true)
					if err != nil {
						return err
					}
					keep.Insert(restic.BlobHandle{ID: id, Type: restic.DataBlob})
				}
				return nil
			}))
		}
		packs := findPacksForBlobs(t, repo, keep)
		release := make(chan struct{})
		close(release)
		u := &predecessorRepackUploader{held: make(chan struct{}), next: make(chan struct{}), release: release, saved: make(map[restic.BlobHandle]int)}
		rtest.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
			u.BlobSaverWithAsync = uploader
			return repository.CopyBlobs(ctx, repo, repo, u, packs, keep, restic.NoopCounter, nil)
		}))
		if len(u.saved) != 4 || keep.Len() != 0 {
			t.Fatalf("saved %d blobs; remaining %d", len(u.saved), keep.Len())
		}
		for blob, count := range u.saved {
			if count != 1 {
				t.Fatalf("%v saved %d times", blob, count)
			}
		}
	})
}

type oversizedCompletionUploader struct {
	restic.BlobSaverWithAsync
	synchronous, asynchronous int
}

func (u *oversizedCompletionUploader) SaveBlob(ctx context.Context, kind restic.BlobType, buf []byte, id restic.ID, duplicate bool) (restic.ID, bool, int, error) {
	u.synchronous++
	return u.BlobSaverWithAsync.SaveBlob(ctx, kind, buf, id, duplicate)
}
func (u *oversizedCompletionUploader) SaveBlobAsync(ctx context.Context, kind restic.BlobType, buf []byte, id restic.ID, duplicate bool, cb func(restic.ID, bool, int, error)) {
	u.asynchronous++
	u.BlobSaverWithAsync.SaveBlobAsync(ctx, kind, buf, id, duplicate, cb)
}

func TestRepackCompletionOversizedPlaintext(t *testing.T) {
	repository.TestAllVersions(t, func(t *testing.T, version uint) {
		repo, _ := repository.TestRepositoryWithBackend(t, &predecessorTwoConnectionBackend{Backend: repository.TestBackend(t)}, version, repository.Options{})
		var id restic.ID
		rtest.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
			var err error
			id, _, _, err = u.SaveBlob(ctx, restic.DataBlob, bytes.Repeat([]byte{123}, (8<<20)+1), restic.ID{}, false)
			return err
		}))
		keep := restic.NewBlobSet(restic.BlobHandle{ID: id, Type: restic.DataBlob})
		packs := findPacksForBlobs(t, repo, keep)
		var counted *oversizedCompletionUploader
		rtest.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
			counted = &oversizedCompletionUploader{BlobSaverWithAsync: u}
			return repository.CopyBlobs(ctx, repo, repo, counted, packs, keep, restic.NoopCounter, nil)
		}))
		if counted.synchronous != 1 || counted.asynchronous != 0 {
			t.Fatalf("sync=%d async=%d", counted.synchronous, counted.asynchronous)
		}
	})
}

func TestRepackCompletionNextPackAndFailure(t *testing.T) {
	for _, action := range []string{"success", "failure", "cancel"} {
		t.Run(action, func(t *testing.T) {
			repository.TestAllVersions(t, func(t *testing.T, version uint) {
				repo, _ := repository.TestRepositoryWithBackend(t, &predecessorTwoConnectionBackend{Backend: repository.TestBackend(t)}, version, repository.Options{})
				keep := restic.NewBlobSet()
				// One blob per pack makes next SaveBlobAsync a next-pack read.
				for i := 0; i < 3; i++ {
					rtest.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
						id, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, bytes.Repeat([]byte{byte(i + 1)}, 4096), restic.ID{}, false)
						keep.Insert(restic.BlobHandle{ID: id, Type: restic.DataBlob})
						return err
					}))
				}
				packs := findPacksForBlobs(t, repo, keep)
				if len(packs) != 3 {
					t.Fatalf("got %d packs", len(packs))
				}
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				release := make(chan struct{})
				defer close(release)
				var wantErr error
				if action == "failure" {
					wantErr = errors.New("callback failure after insertion")
				}
				u := &predecessorRepackUploader{held: make(chan struct{}), next: make(chan struct{}), release: release, failure: wantErr, saved: make(map[restic.BlobHandle]int)}
				done := make(chan error, 1)
				go func() {
					done <- repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
						u.BlobSaverWithAsync = uploader
						return repository.CopyBlobs(ctx, repo, repo, u, packs, keep, restic.NoopCounter, nil)
					})
				}()
				select {
				case <-u.held:
				case <-ctx.Done():
					t.Fatal("first save did not start")
				}
				select {
				case <-u.next:
				case <-ctx.Done():
					t.Fatal("next pack did not stream while callback was held")
				}
				select {
				case err := <-done:
					t.Fatalf("returned before predecessor callback: %v", err)
				default:
				}
				if action == "cancel" {
					cancel()
				} else {
					release <- struct{}{}
				}
				select {
				case err := <-done:
					if action == "success" && err != nil {
						t.Fatal(err)
					}
					if action == "failure" && !errors.Is(err, wantErr) {
						t.Fatalf("want callback error, got %v", err)
					}
					if action == "cancel" && !errors.Is(err, context.Canceled) {
						t.Fatalf("want cancellation, got %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("callbacks were not drained")
				}
				u.mu.Lock()
				for blob, count := range u.saved {
					if count != 1 {
						t.Errorf("blob %v saved %d times", blob, count)
					}
				}
				if action == "success" && (len(u.saved) != 3 || keep.Len() != 0) {
					t.Error("successful repack omitted a blob")
				}
				u.mu.Unlock()
				remaining := listPacks(t, repo)
				for id := range packs {
					if !remaining.Has(id) {
						t.Fatal("source pack deleted")
					}
				}
			})
		})
	}
}
