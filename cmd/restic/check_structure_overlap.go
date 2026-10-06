package main

import (
	"context"

	"github.com/restic/restic/internal/restic"
)

// checkStructureOverlap owns a post-confirmation Structure call. No diagnostics
// are consumed before the command reaches its ordinary Structure heading.
// Backpressure bounds queued diagnostics, not StreamTrees' live memory.
type checkStructureOverlap struct {
	cancel   context.CancelFunc
	errors   chan error
	done     chan struct{}
	progress delayedTerminalCounter
}

func startCheckStructureOverlap(ctx context.Context, run func(context.Context, restic.Counter, chan<- error)) *checkStructureOverlap {
	ctx, cancel := context.WithCancel(ctx)
	work := &checkStructureOverlap{cancel: cancel, errors: make(chan error), done: make(chan struct{})}
	go func() {
		defer close(work.done)
		defer work.progress.Done()
		// The existing Structure implementation owns closing the error channel.
		run(ctx, &work.progress, work.errors)
	}()
	return work
}

func (work *checkStructureOverlap) wait() {
	<-work.done
}

// Cancellation must precede the join: a worker may be blocked publishing an
// error which the command will never consume on an early return. This runs
// before repository unlock and cache cleanup, including after successful use.
func (work *checkStructureOverlap) discard() {
	work.cancel()
	work.wait()
}
