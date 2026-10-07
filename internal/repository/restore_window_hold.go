package repository

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// restoreWindowHold accounts one range window of an ordinary restore worker
// against the restore's hold budget (restic.RestoreHoldBudget). What a window
// holds is its ciphertext range and the largest decoded copy of a compressed
// blob in it.
//
// The configured workers only report what they hold and never wait. The
// additional worker waits for room before it allocates or reads a window, and
// does not keep a window buffer above the ordinary window afterwards.
//
// The bytes are taken inside the backend Load callback, never before Load: a
// worker that waits for bytes may hold a backend connection, but a worker that
// holds bytes never waits for a connection, so the two waits cannot form a
// cycle. Streams without a budget in their context are untouched.
type restoreWindowHold struct {
	budget restic.RestoreHoldBudget
	wait   bool
	need   int64
	held   int64
	taken  bool
}

type restoreHoldTakenKey struct{}

// restoreWindowNeed is the ciphertext range plus the largest decoded copy.
func restoreWindowNeed(blobs pack.Blobs) int64 {
	last := blobs[len(blobs)-1]
	need := int64(last.Offset + last.Length - blobs[0].Offset)
	var plain uint
	for _, blob := range blobs {
		if blob.IsCompressed() && blob.DataLength() > plain {
			plain = blob.DataLength()
		}
	}
	return need + int64(plain)
}

func newRestoreWindowHold(ctx context.Context, blobs pack.Blobs) *restoreWindowHold {
	use, _ := ctx.Value(restic.RestoreHoldKey{}).(restic.RestoreHoldUse)
	if use.Budget == nil {
		return nil
	}
	h := &restoreWindowHold{budget: use.Budget, wait: use.Wait, need: restoreWindowNeed(blobs)}
	if taken, ok := ctx.Value(restoreHoldTakenKey{}).(*restoreWindowHold); ok && taken != nil {
		// The caller read this window itself and already holds its bytes.
		h.held, h.taken = taken.held, true
		taken.held, taken.taken = 0, false
	}
	return h
}

// admit takes the window's bytes once; a replayed Load keeps what it holds.
func (h *restoreWindowHold) admit(ctx context.Context) error {
	if h == nil || h.taken {
		return nil
	}
	if !h.wait {
		h.held, h.taken = h.budget.Force(h.need), true
		return nil
	}
	held, err := h.budget.Acquire(ctx, h.need)
	if err != nil {
		return err
	}
	h.held, h.taken = held, true
	return nil
}

// release returns the bytes. The additional worker also drops a window buffer
// above the ordinary window, so that between windows it retains one ordinary
// window at most.
func (h *restoreWindowHold) release(data *[]byte) {
	if h == nil {
		return
	}
	if h.wait && data != nil && cap(*data) > restoreStreamWindow {
		*data = nil
	}
	if h.taken {
		h.budget.Release(h.held)
		h.held, h.taken = 0, false
	}
}
