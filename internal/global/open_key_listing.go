package global

import (
	"context"
	"errors"
	"io"
	"strings"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

var errKeyListingFull = errors.New("opening key listing is full")

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
		}
		return b.loadErr
	}
	return b.Backend.Load(ctx, h, length, offset, fn)
}

func searchOpeningKey(ctx context.Context, s *repository.Repository, password, hint string, listing *openKeyListing) error {
	err := s.SearchKey(ctx, password, maxKeys, hint)
	if err != nil && listing != nil && listing.replayed && !listing.authenticated && ctx.Err() == nil {
		// SearchKey owns its attempt counter and has not installed a master key on
		// failure. A new call restores fresh listing, order, retries and diagnostics.
		return s.SearchKey(ctx, password, maxKeys, hint)
	}
	return err
}
