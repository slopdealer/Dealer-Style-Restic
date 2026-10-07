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
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/options"
	"github.com/restic/restic/internal/restic"
)

// A marked checker load must reach the local capability through all production
// wrappers, while explicit connection settings remain hard operation caps.
func TestCheckerSlotProductionExplicitLimits(t *testing.T) {
	for _, explicit := range []int{0, 1, 2, 3} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			registry := location.NewRegistry()
			registry.Register(local.NewFactory())
			opts := options.Options{}
			limit := explicit
			if explicit == 0 {
				limit = 2
			} else {
				opts["local.connections"] = fmt.Sprint(explicit)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			be, err := innerOpenBackend(ctx, "local:"+t.TempDir(), Options{Backends: registry}, opts, true, restic.NewNoopPrinter())
			if err != nil {
				t.Fatal(err)
			}
			defer be.Close()
			h := backend.Handle{Type: backend.PackFile, Name: "checker-slot-contract"}
			if err := be.Save(ctx, h, backend.NewByteReader([]byte("data"), be.Hasher())); err != nil {
				t.Fatal(err)
			}
			entered := make(chan struct{}, limit+2)
			done := make(chan error, limit+2)
			launch := func(loadCtx context.Context) {
				go func() {
					done <- be.Load(loadCtx, h, 0, 0, func(io.Reader) error {
						entered <- struct{}{}
						<-ctx.Done()
						return ctx.Err()
					})
				}()
			}
			await := func() {
				select {
				case <-entered:
				case <-ctx.Done():
					t.Fatal("expected load was not admitted")
				}
			}
			for range limit {
				launch(ctx)
				await()
			}
			marked, markedCancel := context.WithCancel(sema.WithAdditionalLoadSlot(ctx))
			defer markedCancel()
			launch(marked)
			if explicit == 0 {
				await()
				// The extra slot is unique: another marked request must queue.
				launch(marked)
			}
			select {
			case <-entered:
				t.Fatal("marked load exceeded permitted capacity")
			case <-time.After(30 * time.Millisecond):
			}
			markedCancel()
			cancel()
			requests := limit + 1
			if explicit == 0 {
				requests++
			}
			for range requests {
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("canceled load returned %v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("canceled loads did not drain")
				}
			}
			if len(entered) != 0 {
				t.Fatal("queued canceled consumer ran")
			}
		})
	}
}
