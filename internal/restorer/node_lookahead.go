package restorer

import (
	"context"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// One helper serves the entire first traversal. The two credits cover both the
// batch being visited and the next batch being decoded, including ancestors.
// The byte target is an estimate, not a bound on a single decoded node.
const restoreLookaheadNodes = 128
const restoreLookaheadBytes = 64 << 10

type restoreNodeBatch struct {
	items    [restoreLookaheadNodes]data.NodeOrError
	count    int
	end      bool
	boundary bool
}

type restoreNodeRequest struct {
	next   func() (data.NodeOrError, bool)
	result chan restoreNodeBatch
}

type restoreNodeReaderLoader func(context.Context, restic.ID) (*data.TreeNodeReader, error)

type restoreNodeLookahead struct {
	load    restoreNodeReaderLoader
	jobs    chan restoreNodeRequest
	done    chan struct{}
	credits chan struct{}
	closed  bool
}

func newRestoreNodeLookahead(load restoreNodeReaderLoader) *restoreNodeLookahead {
	p := &restoreNodeLookahead{
		load:    load,
		jobs:    make(chan restoreNodeRequest),
		done:    make(chan struct{}),
		credits: make(chan struct{}, 2),
	}
	go func() {
		defer close(p.done)
		for job := range p.jobs {
			var batch restoreNodeBatch
			var bytes uint64
			for batch.count < len(batch.items) {
				item, ok := job.next()
				if !ok {
					batch.end = true
					break
				}
				batch.items[batch.count] = item
				batch.count++
				if item.Error != nil {
					batch.end = true
					break
				}
				nodeBytes := estimatePreparedNodeBytes(item.Node)
				bytes += nodeBytes
				// Do not prepare a following batch across directory descent or
				// while an unusually large node is still owned by the caller.
				if item.Node.Type == data.NodeTypeDir || nodeBytes > restoreLookaheadBytes {
					batch.boundary = true
					break
				}
				if bytes >= restoreLookaheadBytes {
					break
				}
			}
			job.result <- batch
			batch = restoreNodeBatch{}
			job = restoreNodeRequest{}
		}
	}()
	return p
}

func (p *restoreNodeLookahead) Load(ctx context.Context, id restic.ID) (data.TreeNodeIterator, error) {
	reader, err := p.load(ctx, id)
	if err != nil {
		return nil, err
	}
	return p.wrapSource(func() (func() (data.NodeOrError, bool), func()) {
		return reader.Next, reader.Close
	}), nil
}

func (p *restoreNodeLookahead) wrapSource(open func() (func() (data.NodeOrError, bool), func())) data.TreeNodeIterator {
	started := false
	return func(yield func(data.NodeOrError) bool) {
		if started {
			panic("tree iterator is single use only")
		}
		started = true
		next, stop := open()
		result := make(chan restoreNodeBatch, 1)
		pending := false
		defer func() {
			// Early termination (including a panic in the visitor) must join
			// the only possible outstanding decoder request before stop.
			if pending {
				<-result
				<-p.credits
			}
			stop()
		}()
		start := func() bool {
			select {
			case p.credits <- struct{}{}:
				p.jobs <- restoreNodeRequest{next: next, result: result}
				pending = true
				return true
			default:
				return false
			}
		}
		for {
			if !pending {
				// A directory ends a batch, so the helper returns a run of
				// directories one node at a time, and an empty tree as an
				// empty batch: a request and a reply each time, with nothing
				// prepared ahead. Such nodes are decoded here instead. The
				// helper is asked from the first node on that is no directory.
				for {
					item, ok := next()
					if !ok {
						return
					}
					dir := item.Error == nil && item.Node != nil && item.Node.Type == data.NodeTypeDir
					if !dir && item.Error == nil && item.Node != nil && estimatePreparedNodeBytes(item.Node) <= restoreLookaheadBytes {
						// the nodes after it are prepared while this one is
						// visited, as they are within a batch
						start()
					}
					if !yield(item) || item.Error != nil {
						return
					}
					if !dir {
						break
					}
				}
				if !pending && !start() {
					for {
						item, ok := next()
						if !ok || !yield(item) || item.Error != nil {
							return
						}
					}
				}
			}
			batch := <-result
			pending = false
			owned := true
			release := func() {
				if owned {
					<-p.credits
					owned = false
				}
			}
			// The current batch is bounded by its reserved credit. Only one
			// additional request may run while its ordinary files are visited.
			if !batch.end && !batch.boundary {
				start()
			}
			keepGoing := true
			func() {
				defer release()
				for i := 0; i < batch.count; i++ {
					item := batch.items[i]
					batch.items[i] = data.NodeOrError{}
					if i == batch.count-1 {
						// Release before descending: no prepared ancestor batch
						// survives in the child traversal.
						release()
					}
					if !yield(item) || item.Error != nil {
						keepGoing = false
						return
					}
				}
			}()
			if !keepGoing || batch.end {
				return
			}
		}
	}
}

func (p *restoreNodeLookahead) Close() {
	if !p.closed {
		p.closed = true
		close(p.jobs)
	}
	<-p.done
}
