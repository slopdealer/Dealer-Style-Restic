package global

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/backend/location"
	"github.com/restic/restic/internal/options"
	"github.com/restic/restic/internal/restic"
)

// Exercise configuration and the actual limiter -> sema -> logger -> retry
// construction, rather than a mock semaphore with the limiter omitted.
func TestB18ProductionOrdinaryLimitsAndCancellation(t *testing.T) {
	for _, limit := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			registry := location.NewRegistry()
			registry.Register(local.NewFactory())
			opts := options.Options{}
			if limit != 0 {
				opts["local.connections"] = fmt.Sprint(limit)
			}
			path := "local:" + t.TempDir()
			scheme, cfg, err := parseConfig(registry, path, opts)
			if err != nil {
				t.Fatal(err)
			}
			gopts := Options{Backends: registry}
			rt, lim, err := setupTransport(gopts)
			if err != nil {
				t.Fatal(err)
			}
			be, err := createOrOpenBackend(context.Background(), scheme, cfg, rt, lim, gopts, path, true, restic.NewNoopPrinter())
			if err != nil {
				t.Fatal(err)
			}
			be, err = wrapBackend(be, gopts, restic.NewNoopPrinter())
			if err != nil {
				t.Fatal(err)
			}
			defer be.Close()
			h := backend.Handle{Type: backend.PackFile, Name: "ordinary-contract-pack"}
			if err := be.Save(context.Background(), h, backend.NewByteReader([]byte("data"), be.Hasher())); err != nil {
				t.Fatal(err)
			}
			expected := limit
			if expected == 0 {
				expected = 2
			}
			if be.Properties().Connections != uint(expected) {
				t.Fatalf("connections=%d, want %d", be.Properties().Connections, expected)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			release := make(chan struct{})
			defer close(release)
			entered := make(chan struct{}, expected+1)
			done := make(chan error, expected+1)
			for range expected {
				go func() {
					done <- be.Load(ctx, h, 0, 0, func(io.Reader) error {
						entered <- struct{}{}
						select {
						case <-release:
							return nil
						case <-ctx.Done():
							return ctx.Err()
						}
					})
				}()
			}
			for range expected {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("ordinary permits were not admitted")
				}
			}
			blockedCtx, blockedCancel := context.WithCancel(ctx)
			started := make(chan struct{})
			go func() {
				close(started)
				done <- be.Load(blockedCtx, h, 0, 0, func(io.Reader) error { entered <- struct{}{}; return nil })
			}()
			<-started
			select {
			case <-entered:
				t.Fatal("ordinary load exceeded configured limit")
			case <-time.After(30 * time.Millisecond):
			}
			blockedCancel()
			// The incumbent waits for a semaphore token even after cancellation.
			// Release occupied callbacks via their context, then require a cancelled
			// result and no invocation of the queued callback. Do not require an
			// inherited delay as a contract for a future cancellation improvement.
			cancel()
			cancelled := 0
			for range expected + 1 {
				select {
				case err := <-done:
					if errors.Is(err, context.Canceled) {
						cancelled++
					} else {
						t.Fatalf("cancelled load: %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("loads did not drain after cancellation")
				}
			}
			if cancelled != expected+1 || len(entered) != 0 {
				t.Fatal("cancelled queued callback ran")
			}
		})
	}
}
