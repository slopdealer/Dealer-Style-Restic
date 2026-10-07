package global

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository"
)

// Cancel at the fresh ordinary discovery, after a complete failed staged
// authentication. Compare the diagnostic with an independently cancelled
// ordinary search; cancellation must not install config or load another key.
func TestB23RefreshCancellationDuringFreshDiscovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	var freshLists, keyLoads, configLoads atomic.Int32
	ordinary := &openingFaultBackend{Backend: be}
	ordinary.list = func(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
		freshLists.Add(1)
		cancel()
		return ctx.Err()
	}
	ordinary.load = func(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
		if h.Type == backend.KeyFile {
			keyLoads.Add(1)
		}
		if h.Type == backend.ConfigFile {
			configLoads.Add(1)
		}
		return be.Load(ctx, h, length, offset, fn)
	}
	staged, finish := startOpenKeyListing(ctx, ordinary, be)
	defer finish()
	listing := staged.(*openKeyListing)
	<-listing.done
	candidate, err := repository.New(staged, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	got := searchOpeningKey(ctx, candidate, "wrong password", "", listing)
	reference, err := repository.New(ordinary, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	want := reference.SearchKey(ctx, "wrong password", maxKeys, "")
	if got == nil || fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("candidate=%v ordinary=%v", got, want)
	}
	if freshLists.Load() != 2 || keyLoads.Load() != 0 || configLoads.Load() != 0 || listing.authenticated || candidate.Config().ID != "" {
		t.Fatalf("freshLists=%d keyLoads=%d configLoads=%d authenticated=%v config=%s", freshLists.Load(), keyLoads.Load(), configLoads.Load(), listing.authenticated, candidate.Config().ID)
	}
	finish()
	if listing.files != nil {
		t.Fatal("joined listing retained descriptors")
	}
}
