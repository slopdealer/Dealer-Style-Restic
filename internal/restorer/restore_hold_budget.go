package restorer

import (
	"context"
	"sync"

	"github.com/restic/restic/internal/restic"
)

var _ restic.RestoreHoldBudget = (*restoreHoldBudget)(nil)

// restoreHoldBudget is the hold budget of one restore (restic.RestoreHoldBudget).
type restoreHoldBudget struct {
	workers int
	mu      sync.Mutex
	size    int64
	free    int64
	wake    chan struct{} // closed and replaced on every release
}

// newRestoreHoldBudget makes the budget of a restore with the given number of
// configured workers (the additional worker is not counted).
func newRestoreHoldBudget(bytes int64, workers int) *restoreHoldBudget {
	if bytes < 1 {
		bytes = 1
	}
	if workers < 1 {
		workers = 1
	}
	return &restoreHoldBudget{workers: workers, size: bytes, free: bytes, wake: make(chan struct{})}
}

// Workers is the number of configured ordinary workers of the restore.
func (b *restoreHoldBudget) Workers() int { return b.workers }

// Force takes n bytes without waiting; the free count may go below zero.
func (b *restoreHoldBudget) Force(n int64) int64 {
	if n <= 0 {
		return 0
	}
	b.mu.Lock()
	b.free -= n
	b.mu.Unlock()
	return n
}

// Acquire waits until n bytes are free and returns the amount taken. A request
// above half the budget is admitted when half the budget is free, so a large
// window cannot wait for every other worker to be idle at once. The wait
// observes cancellation and does not spin.
func (b *restoreHoldBudget) Acquire(ctx context.Context, n int64) (int64, error) {
	if n <= 0 {
		return 0, ctx.Err()
	}
	if n > b.size/2 {
		n = (b.size + 1) / 2
	}
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		b.mu.Lock()
		if b.free >= n {
			b.free -= n
			b.mu.Unlock()
			return n, nil
		}
		wake := b.wake
		b.mu.Unlock()
		select {
		case <-wake:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

// Release returns bytes taken by Acquire.
func (b *restoreHoldBudget) Release(n int64) {
	if n <= 0 {
		return
	}
	b.mu.Lock()
	b.free += n
	if b.free > b.size {
		b.free = b.size
	}
	close(b.wake)
	b.wake = make(chan struct{})
	b.mu.Unlock()
}
