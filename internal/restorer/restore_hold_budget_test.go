package restorer

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

func TestRestoreHoldBudgetBoundsAndWakes(t *testing.T) {
	b := newRestoreHoldBudget(12, 2)
	ctx := context.Background()
	got, err := b.Acquire(ctx, 6)
	if err != nil || got != 6 {
		t.Fatalf("acquire 6: %d, %v", got, err)
	}
	b.Force(2)
	acquired := make(chan int64, 1)
	go func() {
		n, _ := b.Acquire(ctx, 5)
		acquired <- n
	}()
	select {
	case n := <-acquired:
		t.Fatalf("5 admitted beside 8 of 12: %d", n)
	case <-time.After(50 * time.Millisecond):
	}
	b.Release(2)
	b.Release(6)
	select {
	case n := <-acquired:
		if n != 5 {
			t.Fatalf("woken waiter took %d", n)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter was not woken by the release")
	}
	b.Release(5)
	if b.free != 12 {
		t.Fatalf("free after all releases: %d", b.free)
	}
}

func TestRestoreHoldBudgetLargeRequestAndForce(t *testing.T) {
	b := newRestoreHoldBudget(8, 2)
	ctx := context.Background()
	// a configured worker never waits and may take the count below zero
	forced := b.Force(7)
	acquired := make(chan int64, 1)
	go func() {
		n, _ := b.Acquire(ctx, 100)
		acquired <- n
	}()
	select {
	case <-acquired:
		t.Fatal("a large request was admitted with 1 of 8 free")
	case <-time.After(50 * time.Millisecond):
	}
	b.Release(forced)
	select {
	case n := <-acquired:
		if n != 4 {
			t.Fatalf("large request took %d, want half the budget 4", n)
		}
		over := b.Force(9)
		if b.free != -5 {
			t.Fatalf("free after force: %d", b.free)
		}
		b.Release(over)
		b.Release(n)
	case <-time.After(5 * time.Second):
		t.Fatal("large request never admitted")
	}
	if b.free != 8 {
		t.Fatalf("free: %d", b.free)
	}
}

func TestRestoreHoldBudgetCancelAndConcurrency(t *testing.T) {
	b := newRestoreHoldBudget(4, 2)
	ctx, cancel := context.WithCancel(context.Background())
	all := b.Force(4)
	done := make(chan error, 1)
	go func() {
		_, err := b.Acquire(ctx, 1)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("cancelled wait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wait did not observe cancellation")
	}
	b.Release(all)

	// many workers, never more than the budget in use
	var mu sync.Mutex
	inUse, peak := int64(0), int64(0)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				n, err := b.Acquire(context.Background(), int64(1+(w+i)%2))
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				inUse += n
				if inUse > peak {
					peak = inUse
				}
				mu.Unlock()
				mu.Lock()
				inUse -= n
				mu.Unlock()
				b.Release(n)
			}
		}(w)
	}
	wg.Wait()
	if peak > 4 || b.free != 4 {
		t.Fatalf("peak %d of 4, free %d", peak, b.free)
	}
	use, _ := context.WithValue(context.Background(), restic.RestoreHoldKey{}, restic.RestoreHoldUse{Budget: b, Wait: true}).
		Value(restic.RestoreHoldKey{}).(restic.RestoreHoldUse)
	if use.Budget != restic.RestoreHoldBudget(b) || !use.Wait || b.Workers() != 2 {
		t.Fatal("context carriage")
	}
}
