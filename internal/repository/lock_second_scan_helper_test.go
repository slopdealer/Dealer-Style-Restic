package repository

import (
	"context"
	"github.com/restic/restic/internal/backend"
	"sync"
)

// secondScanLockBackend pauses the second lock listing so a competing lock can
// be inserted after the new lock is written but before it is confirmed.
type secondScanLockBackend struct {
	backend.Backend

	mu             sync.Mutex
	armed          bool
	lockLists      int
	secondScan     chan struct{}
	continueScan   chan struct{}
	savedLockFiles chan backend.Handle
}

func newSecondScanLockBackend(be backend.Backend) *secondScanLockBackend {
	return &secondScanLockBackend{
		Backend:        be,
		secondScan:     make(chan struct{}),
		continueScan:   make(chan struct{}),
		savedLockFiles: make(chan backend.Handle, 1),
	}
}

func (be *secondScanLockBackend) arm() {
	be.mu.Lock()
	be.armed = true
	be.lockLists = 0
	be.mu.Unlock()
}

func (be *secondScanLockBackend) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	block := false
	if t == backend.LockFile {
		be.mu.Lock()
		if be.armed {
			be.lockLists++
			block = be.lockLists == 2
		}
		be.mu.Unlock()
	}

	if block {
		close(be.secondScan)
		select {
		case <-be.continueScan:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return be.Backend.List(ctx, t, fn)
}

func (be *secondScanLockBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if err := be.Backend.Save(ctx, h, rd); err != nil {
		return err
	}
	if h.Type == backend.LockFile {
		be.mu.Lock()
		armed := be.armed
		be.mu.Unlock()
		if armed {
			select {
			case be.savedLockFiles <- h:
			default:
			}
		}
	}
	return nil
}
