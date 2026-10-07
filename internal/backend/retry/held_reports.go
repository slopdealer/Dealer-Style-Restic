package retry

import (
	"context"
	"sync"
)

// HeldReports keeps what the retries of some operations would report (an
// error with the delay until the next attempt, the final error, a success
// after retries) until the owner of those operations knows whether their
// result is used. Release reports everything that was held, in order, and
// lets later reports through; Drop discards what was held and every later
// report.
//
// It exists for reads which a command starts before it holds the repository
// lock: stock does not make them, so nothing may be heard of them unless
// they take the place of reads that stock makes.
type HeldReports struct {
	mu      sync.Mutex
	state   heldState
	pending []func()
}

type heldState int

const (
	holdingReports heldState = iota
	releasedReports
	droppedReports
)

type heldReportsKey struct{}

// WithHeldReports returns a context on which the reports of this package are
// held, and the holder.
func WithHeldReports(ctx context.Context) (context.Context, *HeldReports) {
	h := &HeldReports{}
	return context.WithValue(ctx, heldReportsKey{}, h), h
}

// report runs fn now, later or never, according to the holder of ctx.
func report(ctx context.Context, fn func()) {
	h, _ := ctx.Value(heldReportsKey{}).(*HeldReports)
	if h == nil {
		fn()
		return
	}
	h.mu.Lock()
	switch h.state {
	case holdingReports:
		h.pending = append(h.pending, fn)
		h.mu.Unlock()
		return
	case droppedReports:
		h.mu.Unlock()
		return
	}
	h.mu.Unlock()
	fn()
}

// Release reports what was held and no longer holds anything back. It does
// nothing after Drop.
func (h *HeldReports) Release() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != holdingReports {
		return
	}
	// reports made meanwhile wait for the lock, so the order is kept
	for _, fn := range h.pending {
		fn()
	}
	h.pending = nil
	h.state = releasedReports
}

// Drop discards what was held and everything reported from now on. It does
// nothing after Release.
func (h *HeldReports) Drop() {
	if h == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.state != holdingReports {
		return
	}
	h.pending = nil
	h.state = droppedReports
}
