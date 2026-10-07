package repository

import (
	"errors"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Complements the predecessor test: calling drain is insufficient if it does
// not block return until a successor's admitted save has actually retired.
func TestRepackOverlapFailureJoinsHeldSuccessor(t *testing.T) {
	queue := make(chan index.PackBlobs, 2)
	for i := 0; i < 2; i++ {
		queue <- index.PackBlobs{Blobs: make(pack.Blobs, 1)}
	}
	close(queue)
	releaseA, releaseB := make(chan struct{}), make(chan struct{})
	defer close(releaseA)
	defer close(releaseB)
	successorStarted, draining := make(chan struct{}), make(chan struct{})
	want := errors.New("predecessor callback failure")
	done := make(chan error, 1)
	go func() {
		calls := 0
		done <- runRepackCompletionOverlap(queue, func(index.PackBlobs) *repackPackCompletion {
			calls++
			if calls == 1 {
				return &repackPackCompletion{descriptors: 1, finish: func() error { <-releaseA; return want }, drain: func() { <-releaseA }}
			}
			close(successorStarted)
			return &repackPackCompletion{descriptors: 1, finish: func() error { <-releaseB; return nil }, drain: func() { close(draining); <-releaseB }}
		}, restic.NoopCounter)
	}()
	select {
	case <-successorStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("successor did not stream during held predecessor")
	}
	releaseA <- struct{}{}
	select {
	case <-draining:
	case <-time.After(5 * time.Second):
		t.Fatal("failure did not drain admitted successor")
	}
	select {
	case err := <-done:
		t.Fatalf("returned while successor callback remained held: %v", err)
	default:
	}
	releaseB <- struct{}{}
	select {
	case err := <-done:
		if !errors.Is(err, want) {
			t.Fatalf("error precedence: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("successor retirement did not release failure")
	}
}
