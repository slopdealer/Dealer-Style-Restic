package retry

import (
	"context"
	"errors"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mock"
	"io"
	"testing"
	"time"
)

type b23CancelAtTokenLookup struct {
	context.Context
	cancel context.CancelFunc
}

func (c b23CancelAtTokenLookup) Value(key any) any {
	value := c.Context.Value(key)
	if _, ok := key.(preparedLoadKey); ok {
		c.cancel()
	}
	return value
}

// Cancel after retry's initial Err check but before replay consumption. The
// context remains valid: once canceled, Err and Done immediately reflect it.
// This avoids a timing race and exercises the distinct consumption check.
func TestB23PreparedLoadCancellationAtTokenLookup(t *testing.T) {
	h := backend.Handle{Type: backend.SnapshotFile, Name: "snapshot"}
	network, consumed := 0, 0
	base := &mock.Backend{OpenReaderFn: func(ctx context.Context, _ backend.Handle, _ int, _ int64) (io.ReadCloser, error) {
		network++
		return nil, ctx.Err()
	}}
	be := New(base, time.Second, nil, nil)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	ctx, finish := WithPreparedLoad(ctx, be, h, []byte("prepared"))
	defer finish()
	ctx = b23CancelAtTokenLookup{ctx, cancel}
	err := be.Load(ctx, h, 0, 0, func(io.Reader) error { consumed++; return nil })
	if !errors.Is(err, context.Canceled) || consumed != 0 || network != 0 {
		t.Fatalf("err=%v consumed=%d network=%d", err, consumed, network)
	}
}
