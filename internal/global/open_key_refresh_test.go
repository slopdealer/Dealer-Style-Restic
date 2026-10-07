package global

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

type openingFaultBackend struct {
	backend.Backend
	list func(context.Context, backend.FileType, func(backend.FileInfo) error) error
	load func(context.Context, backend.Handle, int, int64, func(io.Reader) error) error
}

func (b *openingFaultBackend) List(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
	if b.list != nil {
		return b.list(ctx, typ, fn)
	}
	return b.Backend.List(ctx, typ, fn)
}
func (b *openingFaultBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	if b.load != nil {
		return b.load(ctx, h, length, offset, fn)
	}
	return b.Backend.Load(ctx, h, length, offset, fn)
}

// Complete key searches exercise the private counter, actual KDF, content hash,
// authentication and config acceptance. The failed staged result never decides
// the outcome of an opening.
func TestOpenKeyRefreshAddedKey(t *testing.T) {
	for _, count := range []int{0, 1, 21} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			ctx := context.Background()
			original, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
			ids := []string{original.KeyID().String()}
			for len(ids) < count {
				k, err := repository.AddKey(ctx, original, rtest.TestPassword, "", "", original.Key())
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, k.ID().String())
			}
			var lists atomic.Int32
			ordinary := &openingFaultBackend{Backend: be, list: func(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
				lists.Add(1)
				if count == 21 {
					for _, id := range ids {
						if err := be.Remove(ctx, backend.Handle{Type: backend.KeyFile, Name: id}); err != nil {
							return err
						}
					}
				}
				return be.List(ctx, typ, fn)
			}}
			quiet := &openingFaultBackend{Backend: be, list: func(_ context.Context, _ backend.FileType, fn func(backend.FileInfo) error) error {
				for _, id := range ids[:count] {
					if err := fn(backend.FileInfo{Name: id}); err != nil {
						return err
					}
				}
				return nil
			}}
			staged, finish := startOpenKeyListing(ctx, ordinary, quiet)
			defer finish()
			listing := staged.(*openKeyListing)
			<-listing.done
			const password = "added after discovery"
			if _, err := repository.AddKey(ctx, original, password, "", "", original.Key()); err != nil {
				t.Fatal(err)
			}
			// Remove old keys before the fresh list, so the maxKeys case proves that
			// the refresh counter starts at zero rather than inheriting 21 attempts.

			fresh, err := repository.New(staged, repository.Options{})
			if err != nil {
				t.Fatal(err)
			}
			if err = searchOpeningKey(ctx, fresh, password, "", listing); err != nil {
				t.Fatal(err)
			}
			if lists.Load() != 1 || fresh.Config().ID != original.Config().ID {
				t.Fatalf("lists=%d, wrong repository=%v", lists.Load(), fresh.Config().ID != original.Config().ID)
			}
		})
	}
}

func TestOpenKeyRefreshQuietLoadFailure(t *testing.T) {
	ctx := context.Background()
	_, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	var loads, reports, lists atomic.Int32
	fault := &openingFaultBackend{Backend: be}
	fault.list = func(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
		lists.Add(1)
		return be.List(ctx, typ, fn)
	}
	fault.load = func(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
		if h.Type == backend.KeyFile && loads.Add(1) <= 2 {
			return errors.New("temporary key transport failure")
		}
		return be.Load(ctx, h, n, off, fn)
	}
	ordinary := retry.New(fault, time.Second, func(string, error, time.Duration) { reports.Add(1) }, nil)
	staged, finish := startOpenKeyListing(ctx, ordinary, fault)
	defer finish()
	listing := staged.(*openKeyListing)
	<-listing.done
	fresh, err := repository.New(staged, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = searchOpeningKey(ctx, fresh, rtest.TestPassword, "", listing); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 3 || reports.Load() != 1 || lists.Load() != 2 {
		t.Fatalf("loads=%d reports=%d lists=%d", loads.Load(), reports.Load(), lists.Load())
	}
}

func TestOpenKeyRefreshConfigFailureDoesNotRepeat(t *testing.T) {
	ctx := context.Background()
	_, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	var lists, configs atomic.Int32
	marker := errors.New("config failure after authentication")
	fault := &openingFaultBackend{Backend: be}
	fault.list = func(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
		lists.Add(1)
		return be.List(ctx, typ, fn)
	}
	fault.load = func(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
		if h.Type == backend.ConfigFile {
			configs.Add(1)
			return marker
		}
		return be.Load(ctx, h, n, off, fn)
	}
	staged, finish := startOpenKeyListing(ctx, fault, fault)
	defer finish()
	listing := staged.(*openKeyListing)
	fresh, err := repository.New(staged, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = searchOpeningKey(ctx, fresh, rtest.TestPassword, "", listing)
	if !errors.Is(err, marker) || lists.Load() != 1 || configs.Load() != 1 || !listing.authenticated {
		t.Fatalf("err=%v lists=%d configs=%d authenticated=%v", err, lists.Load(), configs.Load(), listing.authenticated)
	}
}

func TestOpenKeyRefreshCancellationDoesNotRepeat(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	var lists atomic.Int32
	fault := &openingFaultBackend{Backend: be}
	fault.list = func(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
		lists.Add(1)
		return be.List(ctx, typ, fn)
	}
	fault.load = func(context.Context, backend.Handle, int, int64, func(io.Reader) error) error {
		cancel()
		return context.Canceled
	}
	staged, finish := startOpenKeyListing(ctx, fault, fault)
	defer finish()
	listing := staged.(*openKeyListing)
	<-listing.done
	fresh, err := repository.New(staged, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	_ = searchOpeningKey(ctx, fresh, rtest.TestPassword, "", listing)
	if lists.Load() != 1 || listing.authenticated {
		t.Fatalf("lists=%d authenticated=%v", lists.Load(), listing.authenticated)
	}
}

func TestOpenKeyRefreshLateListingFailure(t *testing.T) {
	ctx := context.Background()
	original, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	quiet := &openingFaultBackend{Backend: be, list: func(_ context.Context, _ backend.FileType, fn func(backend.FileInfo) error) error {
		if err := fn(backend.FileInfo{Name: original.KeyID().String()}); err != nil {
			return err
		}
		return errors.New("late listing failure")
	}}
	var lists atomic.Int32
	ordinary := &openingFaultBackend{Backend: be, list: func(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
		lists.Add(1)
		return be.List(ctx, typ, fn)
	}}
	staged, finish := startOpenKeyListing(ctx, ordinary, quiet)
	defer finish()
	fresh, err := repository.New(staged, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = searchOpeningKey(ctx, fresh, rtest.TestPassword, "", staged.(*openKeyListing)); err != nil {
		t.Fatal(err)
	}
	if lists.Load() != 1 {
		t.Fatal(lists.Load())
	}
}

func TestOpenKeyRefreshFailureMatchesOrdinary(t *testing.T) {
	for _, mode := range []string{"wrong-password", "missing-key", "damaged-key"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			original, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
			staged, finish := startOpenKeyListing(ctx, be, be)
			defer finish()
			listing := staged.(*openKeyListing)
			<-listing.done
			password := rtest.TestPassword
			h := backend.Handle{Type: backend.KeyFile, Name: original.KeyID().String()}
			switch mode {
			case "wrong-password":
				password = "wrong password"
			case "missing-key":
				if err := be.Remove(ctx, h); err != nil {
					t.Fatal(err)
				}
			case "damaged-key":
				if err := be.Remove(ctx, h); err != nil {
					t.Fatal(err)
				}
				if err := be.Save(ctx, h, backend.NewByteReader([]byte("damaged key"), be.Hasher())); err != nil {
					t.Fatal(err)
				}
			}
			candidate, err := repository.New(staged, repository.Options{})
			if err != nil {
				t.Fatal(err)
			}
			ordinary, err := repository.New(be, repository.Options{})
			if err != nil {
				t.Fatal(err)
			}
			got := searchOpeningKey(ctx, candidate, password, "", listing)
			want := ordinary.SearchKey(ctx, password, maxKeys, "")
			if got == nil || want == nil || got.Error() != want.Error() {
				t.Fatalf("candidate=%v ordinary=%v", got, want)
			}
		})
	}
}

// Key rotation can remove the staged key as well as add its replacement.
// ErrNoKeyFound-only refresh does not cover the resulting LoadRaw error.
func TestOpenKeyRefreshRemovedAndReplacedKey(t *testing.T) {
	ctx := context.Background()
	original, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	staged, finish := startOpenKeyListing(ctx, be, be)
	defer finish()
	listing := staged.(*openKeyListing)
	<-listing.done
	const password = "rotated password"
	if _, err := repository.AddKey(ctx, original, password, "", "", original.Key()); err != nil {
		t.Fatal(err)
	}
	if err := be.Remove(ctx, backend.Handle{Type: backend.KeyFile, Name: original.KeyID().String()}); err != nil {
		t.Fatal(err)
	}
	fresh, err := repository.New(staged, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = searchOpeningKey(ctx, fresh, password, "", listing); err != nil {
		t.Fatal(err)
	}
	if fresh.Config().ID != original.Config().ID {
		t.Fatal("different repository")
	}
}
