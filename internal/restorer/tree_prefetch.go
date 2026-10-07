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

type restoreTreeLoader func(context.Context, restic.ID) (data.TreeNodeIterator, error)

type preparedRestoreTree struct {
	id          restic.ID
	nodes       []*data.Node
	iteratorErr error
	loadErr     error
	tooLarge    bool
}

type restoreTreePrefetch struct {
	cancel context.CancelFunc
	done   chan struct{}
	queue  chan preparedRestoreTree
	load   restoreTreeLoader

	// Only the restore traversal goroutine calls Load, so this does not need
	// synchronization.
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
		queue:  make(chan preparedRestoreTree, 1),
		load:   load,
	}

	go func() {
		defer close(p.done)
		defer close(p.queue)

		for occurrence, id := range ids {
			prepared, carried := carry.take(occurrence, id)
			terminal := false
			if !carried {
				prepared, terminal = p.prepare(prefetchCtx, id)
			}
			select {
			case p.queue <- prepared:
			case <-prefetchCtx.Done():
				clear(prepared.nodes)
				return
			}
			if terminal {
				return
			}
		}
	}()

	return p
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

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case prepared, ok := <-p.queue:
		if err := ctx.Err(); err != nil {
			clear(prepared.nodes)
			return nil, err
		}
		if !ok {
			p.fallback = true
			return p.load(ctx, id)
		}
		if !prepared.id.Equal(id) {
			clear(prepared.nodes)
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
	for prepared := range p.queue {
		clear(prepared.nodes)
	}
}
