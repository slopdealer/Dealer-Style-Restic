package repository

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type reverseCorruptBackend struct {
	backend.Backend
	mu      sync.Mutex
	first   string
	loads   map[string]int
	release chan struct{}
}

func (b *reverseCorruptBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, consume func(io.Reader) error) error {
	if h.Type != backend.PackFile {
		return b.Backend.Load(ctx, h, length, offset, consume)
	}
	b.mu.Lock()
	if b.first == "" {
		b.first = h.Name
	}
	wait := h.Name == b.first
	b.loads[h.Name]++
	b.mu.Unlock()
	if wait {
		select {
		case <-b.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return b.Backend.Load(ctx, h, length, offset, func(rd io.Reader) error {
		buf, err := io.ReadAll(rd)
		if err != nil {
			return err
		}
		if len(buf) != 0 {
			buf[len(buf)-1] ^= 0xff
		}
		return consume(bytes.NewReader(buf))
	})
}

// No FIFO obligation: a later corrupt pack must be reported while the first
// pack is still blocked. Three packs avoid hiding all later work behind the
// extra worker's one-entry queue. All packs must be retried and classified as
// data errors. This test runs against the incumbent and checker additions.
func TestCheckerReverseCorruptCompletion(t *testing.T) {
	TestInjectKey(t, restic.TestParseID("7bb3065bfb17da7430dc4dde4741d6db3dd83fdb0829500cf105755e067f879a"), `{"mac":{"k":"W1Y8bmQNJg6TAmuDt7lbpQ==","r":"r43DBmAdmwtQneoBTGAABQ=="},"encrypt":"JuZGBs6joRiLzqkyMWhmbZMLHe8+5oH6MDE5I6M8R/I="}`)
	repo, be := TestFromFixture(t, checkerTestData)
	_ = repo
	b := &reverseCorruptBackend{Backend: be, loads: make(map[string]int), release: make(chan struct{})}
	r := TestOpenBackend(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := r.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
		t.Fatal(err)
	}
	sizes, err := pack.Size(ctx, r, false)
	if err != nil {
		t.Fatal(err)
	}
	chosen := make(map[restic.ID]int64)
	for id, size := range sizes {
		chosen[id] = size
		if len(chosen) == 3 {
			break
		}
	}
	if len(chosen) != 3 {
		t.Fatal("fixture needs three packs")
	}
	errs := make(chan error)
	go newChecker(r).ReadPacks(ctx, func(map[restic.ID]int64) map[restic.ID]int64 { return chosen }, restic.NewNoopPrinter(), errs)
	var first error
	select {
	case first = <-errs:
	case <-ctx.Done():
		close(b.release)
		t.Fatal("later corrupt pack was not reported while first was held")
	}
	b.mu.Lock()
	held := b.first
	b.mu.Unlock()
	var pe *ErrPackData
	if !errors.As(first, &pe) || pe.PackID.String() == held {
		close(b.release)
		t.Fatalf("wrong first error: %v; held %s", first, held)
	}
	close(b.release)
	seen := restic.NewIDSet(pe.PackID)
	for e := range errs {
		if !errors.As(e, &pe) {
			t.Fatalf("corruption classification changed: %T %v", e, e)
		}
		seen.Insert(pe.PackID)
	}
	if len(seen) != 3 {
		t.Fatalf("missing corrupt-pack errors: %v", seen)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	for id := range chosen {
		if b.loads[id.String()] != 2 {
			t.Fatalf("pack %s loads=%d, want verification retry", id, b.loads[id.String()])
		}
	}
}
