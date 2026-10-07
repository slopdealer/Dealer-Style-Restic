package repository

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// pruneHeaderSizes owns a private index-only calculation. The index is already
// loaded and command lock confirmation has finished before this owner starts.
// No repository availability observations, diagnostics or writes are moved.
type pruneHeaderSizes struct {
	cancel context.CancelFunc
	done   chan struct{}
	sizes  map[restic.ID]int64
	err    error
}

func startPruneHeaderSizes(ctx context.Context, idx restic.ListBlobser) *pruneHeaderSizes {
	return startPruneHeaderSizesWithCalculator(ctx, func(ctx context.Context) (map[restic.ID]int64, error) {
		return pack.Size(ctx, idx, true)
	})
}

func startPruneHeaderSizesWithCalculator(ctx context.Context, calculate func(context.Context) (map[restic.ID]int64, error)) *pruneHeaderSizes {
	ctx, cancel := context.WithCancel(ctx)
	p := &pruneHeaderSizes{cancel: cancel, done: make(chan struct{})}
	go func() {
		p.sizes, p.err = calculate(ctx)
		if p.err != nil {
			p.sizes = nil
		}
		close(p.done)
	}()
	return p
}

func (p *pruneHeaderSizes) take() (map[restic.ID]int64, error) {
	<-p.done
	p.cancel()
	sizes, err := p.sizes, p.err
	p.sizes = nil
	return sizes, err
}

// discard joins before the caller can dispose of the index or unlock. It is
// also safe after take: the closed completion channel permits another receive.
func (p *pruneHeaderSizes) discard() {
	p.cancel()
	<-p.done
	p.sizes = nil
}
