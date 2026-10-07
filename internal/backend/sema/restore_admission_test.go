package sema_test

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mock"
	"github.com/restic/restic/internal/backend/sema"
)

type restoreSlotBackend struct {
	backend.Backend
	allow bool
}

func (b restoreSlotBackend) AllowAdditionalLoadSlot() bool { return b.allow }

func TestRestoreScopedAdmissionAndCanceledFallback(t *testing.T) {
	for _, allow := range []bool{true, false} {
		t.Run(map[bool]string{true: "default", false: "explicit-limit"}[allow], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			entered := make(chan struct{}, 3)
			m := mock.NewBackend()
			m.PropertiesFn = func() backend.Properties { return backend.Properties{Connections: 2} }
			m.OpenReaderFn = func(ctx context.Context, _ backend.Handle, _ int, _ int64) (io.ReadCloser, error) {
				entered <- struct{}{}
				<-ctx.Done()
				return nil, ctx.Err()
			}
			be := sema.NewBackend(restoreSlotBackend{m, allow})
			h := backend.Handle{Type: backend.PackFile, Name: "pack"}
			load := func(ctx context.Context) error { return be.Load(ctx, h, 1, 0, func(io.Reader) error { return nil }) }
			ordinaryDone := make(chan error, 2)
			for i := 0; i < 2; i++ {
				go func() { ordinaryDone <- load(ctx) }()
			}
			for i := 0; i < 2; i++ {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("ordinary loads did not enter")
				}
			}
			scopedCtx, scopedCancel := context.WithCancel(ctx)
			defer scopedCancel()
			scopedDone := make(chan error, 1)
			go func() { scopedDone <- load(sema.WithAdditionalLoadSlot(scopedCtx)) }()
			if allow {
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("overflow was not admitted beside two loads")
				}
			}
			scopedCancel()
			select {
			case err := <-scopedDone:
				if err != context.Canceled {
					t.Fatalf("scoped load: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("canceled scoped load stayed queued")
			}
			if !allow {
				select {
				case <-entered:
					t.Fatal("explicit limit was exceeded")
				default:
				}
			}
			cancel()
			<-ordinaryDone
			<-ordinaryDone
		})
	}
}
