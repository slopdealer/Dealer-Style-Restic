package global

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

var errKeyListingFull = errors.New("opening key listing is full")

// errKeySearchRepeats ends the second key search before its first key: the
// keys are the ones that the first search read and rejected.
var errKeySearchRepeats = errors.New("key search would repeat the failed one")

// Only an already supplied password, without a hint, has a known first key
// listing. Leave prompting, hint lookup and one-connection backends unchanged.
func prepareOpenKeyListing(ctx context.Context, be backend.Backend, opts Options) (backend.Backend, func()) {
	retrying, ok := be.(*retry.Backend)
	if !ok || opts.Password == "" || opts.InsecureNoPassword || opts.KeyHint != "" || be.Properties().Connections < 2 {
		return be, func() {}
	}
	// Speculation must not emit retry diagnostics before config acceptance.
	// A failed single attempt is discarded; normal List retains its retries.
	return startOpenKeyListing(ctx, be, retrying.Backend)
}

// openKeyListing owns one short-lived goroutine and at most maxKeys+1 IDs.
// No key bytes, authentication result or repository state is prefetched.
type openKeyListing struct {
	backend.Backend
	done          chan struct{}
	files         []backend.FileInfo
	err           error
	used          bool
	quiet         backend.Backend
	parent        context.Context
	replaying     bool
	replayed      bool
	authenticated bool
	loadErr       error
	// names of the staged listing as it was replayed, and whether a key load
	// of that replay failed
	replayedNames []string
	loadFailed    bool
	// compareNext makes the next key listing, which is the one of the second
	// search, compare itself with the replayed listing before any key is tried
	compareNext bool
}

func startOpenKeyListing(ctx context.Context, ordinary, quiet backend.Backend) (backend.Backend, func()) {
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	b := &openKeyListing{Backend: ordinary, quiet: quiet, parent: parent, done: make(chan struct{})}
	go func() {
		defer close(b.done)
		b.err = quiet.List(ctx, backend.KeyFile, func(fi backend.FileInfo) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			_, err := restic.ParseID(fi.Name)
			if err != nil {
				return nil
			}
			// Match retry.List's duplicate suppression, without retaining arbitrary
			// names. ParseID requires exactly 64 bytes for each accepted name.
			for _, previous := range b.files {
				if previous.Name == fi.Name {
					return nil
				}
			}
			fi.Name = strings.Clone(fi.Name)
			b.files = append(b.files, fi)
			if len(b.files) == maxKeys+1 {
				return errKeyListingFull
			}
			return nil
		})
	}()
	return b, func() {
		cancel()
		<-b.done
		b.files = nil
	}
}

func (b *openKeyListing) Unwrap() backend.Backend { return b.Backend }

// Consume staged discovery only at the ordinary key-search position. Loads
// verify the current key's content hash. A failed search is retried afresh.
func (b *openKeyListing) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	if t == backend.KeyFile && b.compareNext {
		b.compareNext = false
		return b.listForSecondSearch(ctx, fn)
	}
	if t != backend.KeyFile || b.used {
		return b.Backend.List(ctx, t, fn)
	}
	b.used = true
	<-b.done
	files := b.files
	b.files = nil
	if b.err != nil && !errors.Is(b.err, errKeyListingFull) {
		return b.Backend.List(ctx, t, fn)
	}
	b.replayed = true
	b.replaying = true
	b.replayedNames = make([]string, len(files))
	for i, fi := range files {
		b.replayedNames[i] = fi.Name
	}
	defer func() {
		b.replaying = false
		b.loadErr = nil
	}()
	for _, fi := range files {
		if err := ctx.Err(); err != nil {
			if err == context.Canceled && b.parent.Err() == nil {
				b.authenticated = true
			}
			return err
		}
		if err := fn(fi); err != nil {
			return err
		}
	}
	if ctx.Err() == context.Canceled && b.parent.Err() == nil {
		b.authenticated = true
	}
	return ctx.Err()
}

// A staged key attempt uses a single quiet load. Failures are settled by a new
// ordinary search, so retry messages appear only on that ordinary path.
func (b *openKeyListing) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	if b.replaying && h.Type == backend.KeyFile {
		if err := ctx.Err(); err != nil {
			return err
		}
		// LoadRaw may read a damaged download a second time. A transport
		// failure must still discard this attempt even if that read succeeds.
		if b.loadErr == nil {
			b.loadErr = b.quiet.Load(ctx, h, length, offset, fn)
			if b.loadErr != nil {
				b.loadFailed = true
			}
		}
		return b.loadErr
	}
	return b.Backend.Load(ctx, h, length, offset, fn)
}

// listForSecondSearch is the key listing of the search that follows a failed
// search over the staged listing. It is the ordinary listing of that search,
// taken as far as a search can use it (maxKeys attempts and the entry that
// ends it) before a key is tried.
//
// When it lists the keys that were replayed, the second search would read the
// same key files (a key file is named by the hash of its content, and the
// replay read each one completely) and reject the password for each of them
// again. The search is then ended with errKeySearchRepeats before its first
// key derivation. The comparison is by set, since a search that tries every
// key fails in any order; with more keys than a search tries, the order
// decides which ones are tried, so the sequence must be the same.
//
// Otherwise the listing is handed to the search as the backend delivered it:
// the same entries in the same order, then the same error.
func (b *openKeyListing) listForSecondSearch(ctx context.Context, fn func(backend.FileInfo) error) error {
	var current []backend.FileInfo
	listErr := b.Backend.List(ctx, backend.KeyFile, func(fi backend.FileInfo) error {
		if _, err := restic.ParseID(fi.Name); err != nil {
			// the key search skips such a name
			return nil
		}
		fi.Name = strings.Clone(fi.Name)
		current = append(current, fi)
		if len(current) == maxKeys+1 {
			return errKeyListingFull
		}
		return nil
	})
	if errors.Is(listErr, errKeyListingFull) {
		listErr = nil
	}

	if listErr == nil && sameKeys(current, b.replayedNames) {
		return errKeySearchRepeats
	}

	for _, fi := range current {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(fi); err != nil {
			return err
		}
	}
	if listErr != nil {
		return listErr
	}
	return ctx.Err()
}

// sameKeys compares a listing with the replayed names, which are distinct.
func sameKeys(current []backend.FileInfo, replayed []string) bool {
	if len(current) != len(replayed) {
		return false
	}
	for i, fi := range current {
		if len(current) > maxKeys {
			if fi.Name != replayed[i] {
				return false
			}
			continue
		}
		if !slices.Contains(replayed, fi.Name) {
			return false
		}
		// equal length and every name among the replayed ones is the same
		// set only if no name comes twice
		for _, previous := range current[:i] {
			if previous.Name == fi.Name {
				return false
			}
		}
	}
	return true
}

func searchOpeningKey(ctx context.Context, s *repository.Repository, password, hint string, listing *openKeyListing) error {
	err := s.SearchKey(ctx, password, maxKeys, hint)
	if err != nil && listing != nil && listing.replayed && !listing.authenticated && ctx.Err() == nil {
		// SearchKey owns its attempt counter and has not installed a master key on
		// failure. A new call restores fresh listing, order, retries and diagnostics.
		//
		// When the first search ended because no key accepted the password, and
		// every key load of it succeeded, the new search first looks whether
		// the repository still has exactly these keys. Then it would only
		// repeat every key derivation with the same result, and the first
		// result stands: a wrong password costs one search, as in stock.
		listing.compareNext = !listing.loadFailed &&
			(errors.Is(err, repository.ErrNoKeyFound) || errors.Is(err, repository.ErrMaxKeysReached))
		second := s.SearchKey(ctx, password, maxKeys, hint)
		listing.compareNext = false
		if errors.Is(second, errKeySearchRepeats) {
			return err
		}
		return second
	}
	return err
}
