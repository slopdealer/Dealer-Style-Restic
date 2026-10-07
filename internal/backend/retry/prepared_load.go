package retry

import (
	"bytes"
	"context"
	"io"
	"sync"

	"github.com/restic/restic/internal/backend"
)

type preparedLoadKey struct{}

type preparedLoad struct {
	owner  *Backend
	handle backend.Handle
	mu     sync.Mutex
	raw    []byte
}

// WithPreparedLoad offers verified immutable ciphertext to one ordinary load.
// The caller owns its bytes until finish; cache population and retry policy
// remain with the ordinary backend. It must not offer mutable config data.
func WithPreparedLoad(ctx context.Context, owner *Backend, h backend.Handle, raw []byte) (context.Context, func()) {
	p := &preparedLoad{owner: owner, handle: h, raw: raw}
	return context.WithValue(ctx, preparedLoadKey{}, p), func() {
		p.mu.Lock()
		p.raw = nil
		p.mu.Unlock()
	}
}

func (be *Backend) loadWithPreparedData(ctx context.Context, h backend.Handle, length int, offset int64, consumer func(io.Reader) error) error {
	if p, ok := ctx.Value(preparedLoadKey{}).(*preparedLoad); ok && p.owner == be && p.handle == h && length == 0 && offset == 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		p.mu.Lock()
		raw := p.raw
		p.raw = nil // Spend before the callback. A retry uses the backend.
		p.mu.Unlock()
		if raw != nil {
			return consumer(bytes.NewReader(raw))
		}
	}
	return be.Backend.Load(ctx, h, length, offset, consumer)
}
