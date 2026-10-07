package global

// WITNESS (review of S019-merge, area backup / start-up): the key listing that is started
// before the config check and replayed into SearchKey (internal/global/open_key_listing.go).
//
// What it shows: with a WRONG password supplied non-interactively (RESTIC_PASSWORD,
// --password-file, --password-command) stock tries every key file once and fails. The
// candidate tries every key file once from the prefetched listing, then, because that
// attempt failed, runs the whole search again (searchOpeningKey, open_key_listing.go:136-144).
// Every attempt is one full scrypt derivation, so a wrong password costs twice the key
// derivations (time and memory-hard work) before the same error is reported.
// The test counts key-file loads: stock = number of keys, candidate = 2 x number of keys.
// It FAILS on the candidate to make the difference visible. The right password still opens
// the repository (checked too).
//
// OS: any. Run from the candidate tree:
//   go test -run 'TestWitnessOpenKey' -count=1 -p 2 ./internal/global

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

type keyLoadCounter struct {
	backend.Backend
	keyLoads atomic.Int64
	keyLists atomic.Int64
}

func (b *keyLoadCounter) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	if h.Type == backend.KeyFile {
		b.keyLoads.Add(1)
	}
	return b.Backend.Load(ctx, h, length, offset, fn)
}

func (b *keyLoadCounter) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	if t == backend.KeyFile {
		b.keyLists.Add(1)
	}
	return b.Backend.List(ctx, t, fn)
}

func (b *keyLoadCounter) Unwrap() backend.Backend { return b.Backend }

func TestWitnessOpenKeyWrongPasswordDoublesWork(t *testing.T) {
	ctx := context.Background()
	raw := mem.New()
	setup, _ := repository.TestRepositoryWithBackend(t, raw, 0, repository.Options{})
	const keys = 4
	for i := 1; i < keys; i++ {
		if _, err := repository.AddKey(ctx, setup, "other-user-password", "user", "host", setup.Key()); err != nil {
			t.Fatal(err)
		}
	}

	measure := func(password string, candidate bool) (loads, lists int64, err error) {
		counter := &keyLoadCounter{Backend: raw}
		var be backend.Backend = retry.New(sema.NewBackend(counter), time.Second, nil, nil)
		var listing *openKeyListing
		if candidate {
			staged, finish := prepareOpenKeyListing(ctx, be, Options{Password: password})
			defer finish()
			be = staged
			listing, _ = staged.(*openKeyListing)
			if listing == nil {
				t.Fatal("candidate path did not stage a key listing")
			}
		}
		repo, nerr := repository.New(be, repository.Options{})
		if nerr != nil {
			t.Fatal(nerr)
		}
		if candidate {
			err = searchOpeningKey(ctx, repo, password, "", listing) // what OpenRepository calls
		} else {
			err = repo.SearchKey(ctx, password, maxKeys, "") // what stock OpenRepository calls
		}
		return counter.keyLoads.Load(), counter.keyLists.Load(), err
	}

	sLoads, sLists, sErr := measure("wrong password", false)
	cLoads, cLists, cErr := measure("wrong password", true)
	t.Logf("wrong password, %d keys: stock    key loads=%d lists=%d err=%v", keys, sLoads, sLists, sErr)
	t.Logf("wrong password, %d keys: candidate key loads=%d lists=%d err=%v", keys, cLoads, cLists, cErr)
	if sErr == nil || cErr == nil {
		t.Fatal("wrong password was accepted")
	}
	if sErr.Error() != cErr.Error() {
		t.Errorf("error text differs: stock %q candidate %q", sErr, cErr)
	}

	okS, _, errS := measure(rtest.TestPassword, false)
	okC, _, errC := measure(rtest.TestPassword, true)
	// The in-memory backend lists keys in random order, so the number of loads until the
	// right key is found is not comparable between two runs; only success is checked here.
	t.Logf("right password: stock err=%v (after %d loads) ; candidate err=%v (after %d loads); load counts depend on random listing order", errS, okS, errC, okC)
	if errS != nil || errC != nil {
		t.Fatalf("right password rejected: %v / %v", errS, errC)
	}

	if cLoads != sLoads {
		t.Errorf("WRONG PASSWORD COSTS %dx THE KEY DERIVATIONS: stock tried %d key files, candidate tried %d", cLoads/sLoads, sLoads, cLoads)
	}
}
