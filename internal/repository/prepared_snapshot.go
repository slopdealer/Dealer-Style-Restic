package repository

import (
	"context"
	"io"
	"sync"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/restic"
)

const PreparedSnapshotLimit = 64 * 1024

// preparedSnapshot owns one verified ciphertext. Consumption transfers it to
// the existing retry/cache path; authentication and decoding remain ordinary.
type preparedSnapshot struct {
	*Repository
	id    restic.ID
	owner *retry.Backend
	mu    sync.Mutex
	raw   []byte
}

// PrepareSnapshot performs one quiet bounded load without populating the cache.
// The caller must cancel and join this operation before releasing its lock.
func (r *Repository) PrepareSnapshot(ctx context.Context, id restic.ID, size int64) restic.LoaderUnpacked {
	if r.Connections() < 2 || size <= 0 || size > PreparedSnapshotLimit {
		return r
	}
	h := backend.Handle{Type: backend.SnapshotFile, Name: id.String()}
	// Warm metadata already has an ordinary source, so avoid an extra request.
	if r.cache != nil && r.cache.Has(h) {
		return r
	}
	owner := backend.AsBackend[*retry.Backend](r.be)
	if owner == nil {
		return r
	}
	storage := make([]byte, int(size)+1)
	var raw []byte
	err := owner.Backend.Load(ctx, h, 0, 0, func(rd io.Reader) error {
		n, err := io.ReadFull(rd, storage)
		raw = storage[:n]
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil
		}
		return err
	})
	if err != nil || ctx.Err() != nil || int64(len(raw)) != size || restic.Hash(raw) != id {
		return r
	}
	return &preparedSnapshot{Repository: r, id: id, owner: owner, raw: raw}
}

func (p *preparedSnapshot) LoadUnpacked(ctx context.Context, t restic.FileType, id restic.ID) ([]byte, error) {
	if t != restic.SnapshotFile || id != p.id {
		return p.Repository.LoadUnpacked(ctx, t, id)
	}
	p.mu.Lock()
	raw := p.raw
	p.raw = nil
	p.mu.Unlock()
	if raw != nil {
		if ctx.Err() != nil {
			// A warm cache may satisfy even a canceled ordinary load. Let
			// that path decide, without offering speculative bytes.
			return p.Repository.LoadUnpacked(ctx, t, id)
		}
		var finish func()
		ctx, finish = retry.WithPreparedLoad(ctx, p.owner, backend.Handle{Type: backend.SnapshotFile, Name: id.String()}, raw)
		defer finish()
	}
	return p.Repository.LoadUnpacked(ctx, t, id)
}
