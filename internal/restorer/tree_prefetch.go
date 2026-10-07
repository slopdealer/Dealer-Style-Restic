package restorer

import (
	"context"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// Keep only a small amount of decoded tree state while file contents are
// restored. Oversized trees are loaded normally in the second traversal.
const maxPreparedTreeBytes = 256 << 10

// The prepared trees are handed to the traversal in batches. One tree per
// hand-over wakes the other goroutine for every directory, and with many small
// directories the hand-overs cost more than the preparation saves; they cost
// more the more CPUs there are to wake. A batch is handed over as soon as the
// traversal has room for it, so the traversal never waits for a batch to
// fill; while it is busy the batch grows, to at most maxPreparedTreeBatch
// trees and maxPreparedTreeBytes of prepared nodes.
const maxPreparedTreeBatch = 64

type restoreTreeLoader func(context.Context, restic.ID) (data.TreeNodeIterator, error)

type preparedRestoreTree struct {
	id          restic.ID
	nodes       []*data.Node
	iteratorErr error
	loadErr     error
	tooLarge    bool
	// bytes is the estimate for nodes, as far as this prefetch prepared them
	bytes uint64
}

type restoreTreePrefetch struct {
	cancel context.CancelFunc
	done   chan struct{}
	queue  chan []preparedRestoreTree
	load   restoreTreeLoader

	// Only the restore traversal goroutine calls Load, so these do not need
	// synchronization. batch holds the trees received and not yet asked for.
	batch    []preparedRestoreTree
	fallback bool
}

func newRestoreTreePrefetch(ctx context.Context, ids []restic.ID, load restoreTreeLoader) *restoreTreePrefetch {
	return newRestoreTreePrefetchWithCarry(ctx, ids, load, nil)
}

func newRestoreTreePrefetchWithCarry(ctx context.Context, ids []restic.ID, load restoreTreeLoader, carry *restoreTreeCarry) *restoreTreePrefetch {
	prefetchCtx, cancel := context.WithCancel(ctx)
	p := &restoreTreePrefetch{
		cancel: cancel,
		done:   make(chan struct{}),
		queue:  make(chan []preparedRestoreTree, 1),
		load:   load,
	}

	go func() {
		defer close(p.done)
		defer close(p.queue)

		var batch []preparedRestoreTree
		var batchBytes uint64
		// send hands the batch over and reports whether the prefetch goes on
		send := func() bool {
			if len(batch) == 0 {
				return true
			}
			select {
			case p.queue <- batch:
				batch, batchBytes = nil, 0
				return true
			case <-prefetchCtx.Done():
				clearPreparedTrees(batch)
				return false
			}
		}
		for occurrence, id := range ids {
			prepared, carried := carry.take(occurrence, id)
			terminal := false
			if !carried {
				prepared, terminal = p.prepare(prefetchCtx, id)
			}
			if prepared.bytes > maxPreparedTreeBytes-batchBytes && !send() {
				clear(prepared.nodes)
				return
			}
			batch = append(batch, prepared)
			batchBytes += prepared.bytes
			if terminal || len(batch) >= maxPreparedTreeBatch {
				if !send() || terminal {
					return
				}
				continue
			}
			// hand over what is ready if the traversal has room for it
			select {
			case p.queue <- batch:
				batch, batchBytes = nil, 0
			default:
			}
		}
		send()
	}()

	return p
}

func clearPreparedTrees(trees []preparedRestoreTree) {
	for i := range trees {
		clear(trees[i].nodes)
		trees[i] = preparedRestoreTree{}
	}
}

func (p *restoreTreePrefetch) prepare(ctx context.Context, id restic.ID) (preparedRestoreTree, bool) {
	prepared := preparedRestoreTree{id: id}
	tree, err := p.load(ctx, id)
	if err != nil {
		prepared.loadErr = err
		return prepared, true
	}

	var retainedBytes uint64
	for item := range tree {
		if item.Error != nil {
			prepared.iteratorErr = item.Error
			return prepared, true
		}

		nodeBytes := estimatePreparedNodeBytes(item.Node)
		if nodeBytes > maxPreparedTreeBytes-retainedBytes {
			// Do not keep a partial tree. Re-read it through the regular
			// iterator when the ordered traversal reaches this ID.
			prepared.nodes = nil
			prepared.tooLarge = true
			return prepared, false
		}

		retainedBytes += nodeBytes
		prepared.nodes = append(prepared.nodes, item.Node)
	}

	prepared.bytes = retainedBytes
	return prepared, false
}

func estimatePreparedNodeBytes(node *data.Node) uint64 {
	// Account for the Node and its pointer in the slice, then estimate the
	// backing storage decoded from strings, slices, and maps. Doubling the
	// variable byte fields allows for decoder allocation capacity slack.
	size := uint64(unsafe.Sizeof(*node) + unsafe.Sizeof(node) + 64)
	addString := func(value string) {
		size += uint64(len(value)) * 2
	}
	addString(node.Name)
	size += uint64(len(node.Type)) * 2
	addString(node.User)
	addString(node.Group)
	addString(node.LinkTarget)
	addString(node.Error)
	size += uint64(cap(node.LinkTargetRaw)) * 2
	size += uint64(cap(node.Content)) * uint64(unsafe.Sizeof(restic.ID{}))
	if node.Subtree != nil {
		size += uint64(unsafe.Sizeof(*node.Subtree))
	}
	size += uint64(cap(node.ExtendedAttributes)) * uint64(unsafe.Sizeof(data.ExtendedAttribute{}))
	// Duplicate JSON fields can shorten a reused xattr slice without clearing
	// its backing tail. Those invisible attributes still retain their values,
	// so the whole backing array is counted, not only its visible part.
	attrs := node.ExtendedAttributes
	for _, attr := range attrs[:cap(attrs)] {
		addString(attr.Name)
		size += uint64(cap(attr.Value)) * 2
	}
	if node.GenericAttributes != nil {
		size += uint64(len(node.GenericAttributes)) * 128
		for name, value := range node.GenericAttributes {
			size += uint64(len(name)) * 2
			size += uint64(cap(value)) * 2
		}
	}
	return size
}

func (p *restoreTreePrefetch) Load(ctx context.Context, id restic.ID) (data.TreeNodeIterator, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p.fallback {
		return p.load(ctx, id)
	}

	if len(p.batch) == 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case batch, ok := <-p.queue:
			if err := ctx.Err(); err != nil {
				clearPreparedTrees(batch)
				return nil, err
			}
			if !ok {
				p.fallback = true
				return p.load(ctx, id)
			}
			p.batch = batch
		}
	}
	prepared := p.batch[0]
	p.batch[0] = preparedRestoreTree{}
	p.batch = p.batch[1:]
	if !prepared.id.Equal(id) {
		clear(prepared.nodes)
		clearPreparedTrees(p.batch)
		p.batch = nil
		// A caller-supplied selection filter can change its answer between
		// traversals. Abandon the prefetched sequence and preserve the
		// ordinary loader's behavior from this point on.
		p.fallback = true
		p.cancel()
		return p.load(ctx, id)
	}
	if prepared.loadErr != nil {
		return nil, prepared.loadErr
	}
	if prepared.tooLarge {
		return p.load(ctx, id)
	}
	return preparedTreeIterator(prepared.nodes, prepared.iteratorErr), nil
}

func preparedTreeIterator(nodes []*data.Node, iteratorErr error) data.TreeNodeIterator {
	started := false
	return func(yield func(data.NodeOrError) bool) {
		if started {
			panic("tree iterator is single use only")
		}
		started = true
		defer func() {
			clear(nodes)
			nodes = nil
		}()
		for i, node := range nodes {
			// The visitor owns this node now. In particular, directory descent
			// must not retain completed siblings through the ancestor slice.
			nodes[i] = nil
			if !yield(data.NodeOrError{Node: node}) {
				return
			}
		}
		if iteratorErr != nil {
			yield(data.NodeOrError{Error: iteratorErr})
		}
	}
}

func (p *restoreTreePrefetch) Close() {
	p.cancel()
	<-p.done
	// Joining the sole producer makes draining safe, including repeated Close.
	for batch := range p.queue {
		clearPreparedTrees(batch)
	}
	clearPreparedTrees(p.batch)
	p.batch = nil
}
