package repository_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

type repackHeldBudgetUploader struct {
	restic.BlobSaverWithAsync
	held    chan struct{}
	release <-chan struct{}
}

func (u *repackHeldBudgetUploader) SaveBlobAsync(ctx context.Context, kind restic.BlobType, buf []byte, id restic.ID, duplicate bool, cb func(restic.ID, bool, int, error)) {
	u.BlobSaverWithAsync.SaveBlobAsync(ctx, kind, buf, id, duplicate, func(saved restic.ID, known bool, size int, err error) {
		u.held <- struct{}{}
		// An admitted callback may retire after read cancellation. Model that
		// explicitly instead of letting cancellation release this test's gate.
		<-u.release
		cb(saved, known, size, err)
	})
}

// CopyBlobs serializes access to this set with its own keepMutex. Claiming a
// third blob happens immediately before attempting its plaintext byte permit.
type repackBudgetClaims struct {
	restic.BlobSet
	claims int
	third  chan struct{}
}

func (s *repackBudgetClaims) Delete(h restic.BlobHandle) {
	s.BlobSet.Delete(h)
	s.claims++
	if s.claims == 3 {
		close(s.third)
	}
}

func TestRepackExhaustedBudgetCancellationJoinsCallbacks(t *testing.T) {
	repo, _ := repository.TestRepositoryWithBackend(t, repository.TestBackend(t), 2, repository.Options{})
	keep := restic.NewBlobSet()
	packs := restic.NewIDSet()
	err := repo.WithBlobUploader(context.Background(), func(ctx context.Context, u restic.BlobSaverWithAsync) error {
		for i := 1; i <= 3; i++ {
			id, _, _, err := u.SaveBlob(ctx, restic.DataBlob, bytes.Repeat([]byte{byte(i)}, 4<<20), restic.ID{}, false)
			if err != nil {
				return err
			}
			keep.Insert(restic.BlobHandle{Type: restic.DataBlob, ID: id})
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for h := range keep {
		for _, blob := range repo.LookupBlob(h) {
			packs.Insert(blob.PackID())
		}
	}
	if len(packs) != 1 {
		t.Fatalf("setup created %d packs, want one", len(packs))
	}
	ctx, cancel := context.WithCancel(context.Background())
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	held := make(chan struct{}, 3)
	claims := &repackBudgetClaims{BlobSet: keep, third: make(chan struct{})}
	done := make(chan error, 1)
	finished := false
	defer func() {
		cancel()
		unblock()
		if !finished {
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("canceled repack did not retire")
			}
		}
	}()
	go func() {
		done <- repo.WithBlobUploader(ctx, func(ctx context.Context, u restic.BlobSaverWithAsync) error {
			return repository.CopyBlobs(ctx, repo, repo, &repackHeldBudgetUploader{BlobSaverWithAsync: u, held: held, release: release}, packs, claims, restic.NoopCounter, nil)
		})
	}()
	for i := 0; i < 2; i++ {
		select {
		case <-held:
		case err := <-done:
			finished = true
			t.Fatalf("returned before exhausting permits: %v", err)
		case <-time.After(5 * time.Second):
			t.Fatal("two 4 MiB callbacks were not admitted")
		}
	}
	select {
	case <-claims.third:
	case <-time.After(5 * time.Second):
		t.Fatal("third blob did not reach byte admission")
	}
	select {
	case <-held:
		t.Fatal("third 4 MiB save exceeded the 8 MiB budget")
	default:
	}
	cancel()
	select {
	case err := <-done:
		finished = true
		t.Fatalf("returned without joining held callbacks: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	unblock()
	select {
	case err := <-done:
		finished = true
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("callback retirement did not release canceled repack")
	}
	select {
	case <-held:
		t.Fatal("canceled byte admission started a third save")
	default:
	}
}
