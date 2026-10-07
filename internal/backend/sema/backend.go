package sema

import (
	"context"
	"io"
	"sync"

	"github.com/cenkalti/backoff/v4"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/errors"
)

// make sure that connectionLimitedBackend implements backend.Backend
var _ backend.Backend = &connectionLimitedBackend{}

// connectionLimitedBackend limits the number of concurrent operations.
type connectionLimitedBackend struct {
	backend.Backend
	sem                     semaphore
	extraSem                semaphore
	allowAdditionalLoadSlot bool
	freezeLock              sync.Mutex
}

type additionalLoadSlotKey struct{}

// WithAdditionalLoadSlot permits one additional concurrent pack load through
// a connection-limited backend. It is intended for bounded check and restore workers;
// all ordinary backend operations retain the configured connection limit.
func WithAdditionalLoadSlot(ctx context.Context) context.Context {
	return context.WithValue(ctx, additionalLoadSlotKey{}, true)
}

// NewBackend creates a backend that limits the concurrent operations on the underlying backend
func NewBackend(be backend.Backend) backend.Backend {
	sem, err := newSemaphore(be.Properties().Connections)
	if err != nil {
		panic(err)
	}
	allowAdditionalLoadSlot := false
	if configured, ok := be.(interface{ AllowAdditionalLoadSlot() bool }); ok {
		allowAdditionalLoadSlot = configured.AllowAdditionalLoadSlot()
	}
	extraSem, err := newSemaphore(1)
	if err != nil {
		panic(err)
	}

	return &connectionLimitedBackend{
		Backend:                 be,
		sem:                     sem,
		extraSem:                extraSem,
		allowAdditionalLoadSlot: allowAdditionalLoadSlot,
	}
}

// typeDependentLimit acquire a token unless the FileType is a lock file. The returned function
// must be called to release the token.
func (be *connectionLimitedBackend) typeDependentLimit(t backend.FileType) func() {
	// allow concurrent lock file operations to ensure that the lock refresh is always possible
	if t == backend.LockFile {
		return func() {}
	}
	be.sem.GetToken()
	// prevent token usage while the backend is frozen
	be.freezeLock.Lock()
	defer be.freezeLock.Unlock()

	return be.sem.ReleaseToken
}

// loadLimit normally uses the configured semaphore. A checker worker that
// carries an additional-load-slot context may use one separate, non-blocking
// slot; if it is busy, the load joins the normal semaphore instead.
func (be *connectionLimitedBackend) loadLimit(ctx context.Context, t backend.FileType) func() {
	if be.allowAdditionalLoadSlot && t == backend.PackFile && ctx.Value(additionalLoadSlotKey{}) != nil && be.extraSem.TryGetToken() {
		// Keep the freeze barrier identical to ordinary operations.
		be.freezeLock.Lock()
		be.freezeLock.Unlock()
		return be.extraSem.ReleaseToken
	}
	// A scoped worker can fall back to ordinary admission (including when an
	// explicit connection setting disables overflow). Do not leave it queued
	// behind other work after cancellation.
	if t == backend.PackFile && ctx.Value(additionalLoadSlotKey{}) != nil {
		select {
		case be.sem.ch <- struct{}{}:
			be.freezeLock.Lock()
			be.freezeLock.Unlock()
			return be.sem.ReleaseToken
		case <-ctx.Done():
			return func() {}
		}
	}
	return be.typeDependentLimit(t)
}

// Freeze blocks all backend operations except those on lock files
func (be *connectionLimitedBackend) Freeze() {
	be.freezeLock.Lock()
}

// Unfreeze allows all backend operations to continue
func (be *connectionLimitedBackend) Unfreeze() {
	be.freezeLock.Unlock()
}

// Save adds new Data to the backend.
func (be *connectionLimitedBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if err := h.Valid(); err != nil {
		return backoff.Permanent(err)
	}

	defer be.typeDependentLimit(h.Type)()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	return be.Backend.Save(ctx, h, rd)
}

// Load runs fn with a reader that yields the contents of the file at h at the
// given offset.
func (be *connectionLimitedBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	if err := h.Valid(); err != nil {
		return backoff.Permanent(err)
	}
	if offset < 0 {
		return backoff.Permanent(errors.New("offset is negative"))
	}
	if length < 0 {
		return backoff.Permanent(errors.Errorf("invalid length %d", length))
	}

	defer be.loadLimit(ctx, h.Type)()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	return be.Backend.Load(ctx, h, length, offset, fn)
}

// Stat returns information about a file in the backend.
func (be *connectionLimitedBackend) Stat(ctx context.Context, h backend.Handle) (backend.FileInfo, error) {
	if err := h.Valid(); err != nil {
		return backend.FileInfo{}, backoff.Permanent(err)
	}

	defer be.typeDependentLimit(h.Type)()

	if ctx.Err() != nil {
		return backend.FileInfo{}, ctx.Err()
	}

	return be.Backend.Stat(ctx, h)
}

// Remove deletes a file from the backend.
func (be *connectionLimitedBackend) Remove(ctx context.Context, h backend.Handle) error {
	if err := h.Valid(); err != nil {
		return backoff.Permanent(err)
	}

	defer be.typeDependentLimit(h.Type)()

	if ctx.Err() != nil {
		return ctx.Err()
	}

	return be.Backend.Remove(ctx, h)
}

func (be *connectionLimitedBackend) Unwrap() backend.Backend {
	return be.Backend
}
