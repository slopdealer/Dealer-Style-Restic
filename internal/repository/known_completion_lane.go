package repository

import (
	"context"
	"sync"

	"github.com/restic/restic/internal/restic"
	"golang.org/x/sync/errgroup"
)

// This queue owns descriptors only. Callback closures must follow the saver
// contract: they must not block and must not retain blob payloads. Accepted
// descriptors settle even after cancellation, before the repository is flushed.
const knownCompletionQueueSize = 16

type knownCompletion struct {
	ctx      context.Context
	id       restic.ID
	callback func(restic.ID, bool, int, error)
}

type knownCompletionLane struct {
	jobs      chan knownCompletion
	done      chan struct{}
	closeOnce sync.Once
}

func newKnownCompletionLane(wg *errgroup.Group) *knownCompletionLane {
	lane := &knownCompletionLane{
		jobs: make(chan knownCompletion, knownCompletionQueueSize),
		done: make(chan struct{}),
	}
	// A publisher owns its group slot before payload admission can saturate
	// the group. Publishing from the completion worker with another wg.Go
	// could block behind savers that themselves wait for this cancellation.
	errors := make(chan error, 1)
	wg.Go(func() error {
		select {
		case err := <-errors:
			return err
		case <-lane.done:
			// A completed drain and its pending error can be ready together.
			// Always report that error rather than losing it to select order.
			select {
			case err := <-errors:
				return err
			default:
				return nil
			}
		}
	})
	wg.Go(func() error {
		defer close(lane.done)
		reportedError := false
		for job := range lane.jobs {
			if err := job.ctx.Err(); err != nil {
				job.callback(restic.ID{}, false, 0, err)
				if !reportedError {
					errors <- err
					reportedError = true
				}
			} else {
				job.callback(job.id, true, 0, nil)
			}
		}
		return nil
	})
	return lane
}

func (l *knownCompletionLane) submit(ctx context.Context, id restic.ID, cb func(restic.ID, bool, int, error)) {
	// The worker remains alive through cancellation. Enqueuing even a cancelled
	// job keeps callback execution asynchronous without spawning unbounded jobs.
	l.jobs <- knownCompletion{ctx: ctx, id: id, callback: cb}
}

func (l *knownCompletionLane) finish() {
	l.closeOnce.Do(func() { close(l.jobs) })
	<-l.done
}
