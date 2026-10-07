package repository

import (
	"context"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// holdTestBudget is a non-blocking stand-in: Acquire fails with the context
// error when there is no room (the test only waits with a cancelled context).
type holdTestBudget struct{ size, free int64 }

func (b *holdTestBudget) Force(n int64) int64 { b.free -= n; return n }
func (b *holdTestBudget) Acquire(ctx context.Context, n int64) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if n > b.size/2 {
		n = (b.size + 1) / 2
	}
	if b.free < n {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		panic("holdTestBudget: no room and nothing to wait for")
	}
	b.free -= n
	return n, nil
}
func (b *holdTestBudget) Release(n int64) { b.free += n }
func (b *holdTestBudget) Workers() int    { return 2 }

func holdCtx(b *holdTestBudget, wait bool) context.Context {
	return context.WithValue(context.Background(), restic.RestoreHoldKey{}, restic.RestoreHoldUse{Budget: b, Wait: wait})
}

func holdTestBlobs() pack.Blobs {
	// offsets 0..300: raw 100, compressed 100 (plain 500), raw 100
	return pack.Blobs{
		{BlobHandle: restic.BlobHandle{Type: restic.DataBlob}, Offset: 0, Length: 100},
		{BlobHandle: restic.BlobHandle{Type: restic.DataBlob}, Offset: 100, Length: 100, UncompressedLength: 500},
		{BlobHandle: restic.BlobHandle{Type: restic.DataBlob}, Offset: 200, Length: 100},
	}
}

func TestRestoreWindowHoldAccounting(t *testing.T) {
	blobs := holdTestBlobs()
	if need := restoreWindowNeed(blobs); need != 300+500 {
		t.Fatalf("need %d, want ciphertext range 300 + largest decoded copy 500", need)
	}
	// no budget in the context: nothing is accounted, the buffer is kept
	var none *restoreWindowHold = newRestoreWindowHold(context.Background(), blobs)
	if none != nil {
		t.Fatal("hold without a budget")
	}
	buf := make([]byte, restoreStreamWindow+1)
	none.release(&buf)
	if buf == nil || none.admit(context.Background()) != nil {
		t.Fatal("nil hold must be inert")
	}

	budget := &holdTestBudget{size: 1600, free: 1600}
	ctx := holdCtx(budget, true)
	h := newRestoreWindowHold(ctx, blobs)
	if err := h.admit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := h.admit(ctx); err != nil || h.held != 800 { // a replayed Load takes nothing more
		t.Fatalf("held %d, %v", h.held, err)
	}
	// a second window of the additional worker does not fit beside the first
	waitCtx, cancel := context.WithCancel(ctx)
	cancel()
	if err := newRestoreWindowHold(waitCtx, blobs).admit(waitCtx); err != context.Canceled {
		t.Fatalf("second window: %v", err)
	}
	// a configured worker never waits and keeps its window buffer
	regCtx := holdCtx(budget, false)
	reg := newRestoreWindowHold(regCtx, blobs)
	if err := reg.admit(regCtx); err != nil || reg.held != 800 {
		t.Fatalf("configured worker: %d, %v", reg.held, err)
	}
	kept := make([]byte, restoreStreamWindow+1)
	reg.release(&kept)
	if kept == nil {
		t.Fatal("a configured worker keeps its window buffer")
	}
	// the final window of a response hands its bytes to the decode
	adopted := newRestoreWindowHold(context.WithValue(ctx, restoreHoldTakenKey{}, h), blobs)
	if !adopted.taken || adopted.held != 800 || h.taken || h.held != 0 {
		t.Fatalf("adoption: %+v from %+v", adopted, h)
	}
	h.release(nil) // the giver has nothing left to return
	big := make([]byte, restoreStreamWindow+1)
	small := make([]byte, 16)
	adopted.release(&big)
	if big != nil {
		t.Fatal("a window buffer above the ordinary window must not be retained")
	}
	adopted.release(&small)
	if small == nil {
		t.Fatal("an ordinary window buffer is kept")
	}
	a, _ := budget.Acquire(ctx, 800)
	b, err := budget.Acquire(ctx, 800)
	if err != nil || a+b != 1600 {
		t.Fatalf("budget not whole after release: %d, %v", a+b, err)
	}
}
