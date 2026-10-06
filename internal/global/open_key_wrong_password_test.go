package global

import (
	"context"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

type keyAccessCounter struct {
	backend.Backend
	keyLoads atomic.Int64
	keyLists atomic.Int64
}

func (b *keyAccessCounter) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	if h.Type == backend.KeyFile {
		b.keyLoads.Add(1)
	}
	return b.Backend.Load(ctx, h, length, offset, fn)
}

func (b *keyAccessCounter) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	if t == backend.KeyFile {
		b.keyLists.Add(1)
	}
	return b.Backend.List(ctx, t, fn)
}

func (b *keyAccessCounter) Unwrap() backend.Backend { return b.Backend }

// searchWithCounts opens the repository in raw the way OpenRepository does
// (staged key listing) or the way stock does (plain SearchKey) and returns
// the key files loaded and the key listings made.
func searchWithCounts(t *testing.T, raw backend.Backend, password string, staged bool, afterStaging func()) (loads, lists int64, err error) {
	ctx := context.Background()
	counter := &keyAccessCounter{Backend: raw}
	var be backend.Backend = retry.New(sema.NewBackend(counter), time.Second, nil, nil)
	var listing *openKeyListing
	if staged {
		wrapped, finish := prepareOpenKeyListing(ctx, be, Options{Password: password})
		defer finish()
		be = wrapped
		listing, _ = wrapped.(*openKeyListing)
		if listing == nil {
			t.Fatal("no key listing was staged")
		}
		<-listing.done
	}
	if afterStaging != nil {
		afterStaging()
	}
	repo, err := repository.New(be, repository.Options{})
	rtest.OK(t, err)
	if staged {
		err = searchOpeningKey(ctx, repo, password, "", listing)
	} else {
		err = repo.SearchKey(ctx, password, maxKeys, "")
	}
	return counter.keyLoads.Load(), counter.keyLists.Load(), err
}

// A wrong password that is supplied without a prompt (RESTIC_PASSWORD, a
// password file or command) costs one key search: every key file is loaded
// and tried once, as in stock. Every attempt is one scrypt derivation.
func TestOpenKeyWrongPasswordCostsOneSearch(t *testing.T) {
	for _, keys := range []int{1, 4, maxKeys} {
		raw := mem.New()
		setup, _ := repository.TestRepositoryWithBackend(t, raw, 0, repository.Options{})
		for i := 1; i < keys; i++ {
			_, err := repository.AddKey(context.Background(), setup, "another password", "user", "host", setup.Key())
			rtest.OK(t, err)
		}

		stockLoads, stockLists, stockErr := searchWithCounts(t, raw, "wrong password", false, nil)
		loads, lists, err := searchWithCounts(t, raw, "wrong password", true, nil)
		if stockErr == nil || err == nil {
			t.Fatal("wrong password was accepted")
		}
		rtest.Equals(t, stockErr.Error(), err.Error())
		rtest.Equals(t, int64(keys), stockLoads)
		rtest.Equals(t, int64(1), stockLists)
		if loads != stockLoads {
			t.Errorf("%d keys: %d key files loaded and tried, stock tries %d", keys, loads, stockLoads)
		}
		// the staged listing and the one that confirms the keys are unchanged
		rtest.Equals(t, int64(2), lists)

		// the right password still opens the repository
		_, _, err = searchWithCounts(t, raw, rtest.TestPassword, true, nil)
		rtest.OK(t, err)
	}
}

// The first result is final only while the repository has the keys that were
// tried. A key that appears after the staged listing is found by a second
// search over the new listing; a different set of keys that still does not
// accept the password is searched again and gives stock's error.
func TestOpenKeyChangedListingIsSearchedAgain(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	setup, _ := repository.TestRepositoryWithBackend(t, raw, 0, repository.Options{})
	for i := 0; i < 2; i++ {
		_, err := repository.AddKey(ctx, setup, "another password", "user", "host", setup.Key())
		rtest.OK(t, err)
	}

	// a key for the password is added between the staged listing and the search
	loads, lists, err := searchWithCounts(t, raw, "late password", true, func() {
		_, err := repository.AddKey(ctx, setup, "late password", "user", "host", setup.Key())
		rtest.OK(t, err)
	})
	rtest.OK(t, err)
	rtest.Equals(t, int64(2), lists)
	if loads < 3+1 || loads > 3+4 {
		t.Errorf("%d key files loaded: the three staged keys once, then up to the four listed ones", loads)
	}

	// a key for another password is added: both searches fail, the second is complete
	stockLoads, _, stockErr := searchWithCounts(t, raw, "wrong password", false, nil)
	rtest.Equals(t, int64(4), stockLoads)
	loads, lists, err = searchWithCounts(t, raw, "wrong password", true, func() {
		_, err := repository.AddKey(ctx, setup, "yet another password", "user", "host", setup.Key())
		rtest.OK(t, err)
	})
	if stockErr == nil || err == nil {
		t.Fatal("wrong password was accepted")
	}
	rtest.Equals(t, stockErr.Error(), err.Error())
	rtest.Equals(t, int64(4+5), loads)
	rtest.Equals(t, int64(2), lists)
}
