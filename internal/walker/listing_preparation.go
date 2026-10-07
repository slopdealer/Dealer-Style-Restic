package walker

import (
	"context"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

const listingBatchNodes = 128
const listingBatchJSONBytes = 64 << 10
const listingNodeJSONBytes = 2 << 10

type listingBatch struct {
	items                      [listingBatchNodes]data.NodeOrError
	count                      int
	end, boundary, synchronous bool
}

type listingSource struct {
	next  func() (data.NodeOrError, bool)
	size  func(int) (int, bool)
	close func()
}

type listingRequest struct {
	source listingSource
	result chan listingBatch
}

// One helper belongs to one traversal. There is at most one current and one
// pending batch, and neither survives directory descent. Raw input is bounded
// before decoding, including content lists and attributes; a large node never
// occupies a batch beside another decoded node. The authenticated payload and
// decoder are the ordinary reader's, rather than a second copy or observation.
type listingPreparation struct {
	loader restic.BlobLoader
	jobs   chan listingRequest
	done   chan struct{}
	closed bool
}

func readListingBatch(source listingSource) listingBatch {
	var batch listingBatch
	bytes := 0
	for batch.count < len(batch.items) {
		size, small := source.size(listingNodeJSONBytes)
		if !small {
			batch.synchronous = true
			break
		}
		if bytes+size > listingBatchJSONBytes {
			break
		}
		item, ok := source.next()
		if !ok {
			batch.end = true
			break
		}
		batch.items[batch.count] = item
		batch.count++
		bytes += size
		if item.Error != nil {
			batch.end = true
			break
		}
		if item.Node.Type == data.NodeTypeDir {
			batch.boundary = true
			break
		}
	}
	return batch
}

func newListingPreparation(loader restic.BlobLoader) *listingPreparation {
	return &listingPreparation{loader: loader}
}

// A worker is needed only after a full ordinary first batch exposes useful
// decode work beside callbacks. Short traversals never acquire a goroutine.
func (p *listingPreparation) startWorker() {
	if p.jobs != nil {
		return
	}
	p.jobs = make(chan listingRequest)
	p.done = make(chan struct{})
	go func() {
		defer close(p.done)
		for job := range p.jobs {
			job.result <- readListingBatch(job.source)
		}
	}()
}

func (p *listingPreparation) Load(ctx context.Context, id restic.ID) (data.TreeNodeIterator, error) {
	tree, _, err := p.loadOwned(ctx, id)
	return tree, err
}

// A successful ordinary load has an owner even if its visitor rejects the
// iterator before the first Next. The walker closes that frame; iteration also
// closes it after joining pending work. TreeNodeReader.Close is idempotent.
func (p *listingPreparation) loadOwned(ctx context.Context, id restic.ID) (data.TreeNodeIterator, func(), error) {
	r, err := data.LoadTreeNodeReader(ctx, p.loader, id)
	if err != nil {
		return nil, func() {}, err
	}
	return p.wrap(ctx, listingSource{next: r.Next, size: r.PreparationSize, close: r.Close}), r.Close, nil
}

func (p *listingPreparation) wrap(ctx context.Context, source listingSource) data.TreeNodeIterator {
	started := false
	return func(yield func(data.NodeOrError) bool) {
		if started {
			panic("tree iterator is single use only")
		}
		started = true
		var result chan listingBatch
		pending := false
		defer func() {
			if pending {
				<-result
			}
			source.close()
		}()
		start := func() {
			if result == nil {
				result = make(chan listingBatch, 1)
			}
			p.startWorker()
			p.jobs <- listingRequest{source: source, result: result}
			pending = true
		}
		batch := readListingBatch(source)
		for {
			if !batch.end && !batch.boundary && !batch.synchronous && ctx.Err() == nil {
				start()
			}
			for i := 0; i < batch.count; i++ {
				item := batch.items[i]
				batch.items[i] = data.NodeOrError{}
				if !yield(item) || item.Error != nil {
					return
				}
			}
			if batch.end {
				return
			}
			if batch.synchronous {
				// No batch or helper decode is retained across this potentially
				// oversized node, directory callback, framing or parse error.
				item, ok := source.next()
				if !ok || !yield(item) || item.Error != nil {
					return
				}
			}
			if !pending {
				// Directory and oversized boundaries cannot overlap. Keep
				// their following batch on the caller until it is ready.
				batch = readListingBatch(source)
			} else {
				batch = <-result
				pending = false
			}
		}
	}
}

func (p *listingPreparation) Close() {
	if !p.closed {
		p.closed = true
		if p.jobs != nil {
			close(p.jobs)
		}
	}
	if p.done != nil {
		<-p.done
	}
}
