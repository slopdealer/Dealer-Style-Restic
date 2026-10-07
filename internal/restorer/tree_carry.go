package restorer

import (
	"context"
	"time"
	"unsafe"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// This budget is cumulative: handing a node to an iterator or metadata task
// does not return credit. It bounds admitted estimates, not decoder scratch,
// a single oversized node, or RSS. No traversal ever waits for carry space.
const maxRestoreTreeCarryBytes = 512 << 10
const restoreTreeCarryRecordBytes = 256

type restoreTreeCarry struct {
	records map[int]preparedRestoreTree
	next    int
	charged uint64
}

func newRestoreTreeCarry() *restoreTreeCarry {
	return &restoreTreeCarry{records: make(map[int]preparedRestoreTree)}
}

// Wrap is called only by the first ordered traversal. Occurrences, including
// failed loads, align with the existing preorder ID list; repeated IDs are
// intentionally independent. The first-pass visitor reads decoded nodes but
// does not mutate them. Only complete, successful interpretations are reusable.
func (c *restoreTreeCarry) Wrap(load restoreTreeLoader) restoreTreeLoader {
	return func(ctx context.Context, id restic.ID) (data.TreeNodeIterator, error) {
		occurrence := c.next
		c.next++
		tree, err := load(ctx, id)
		if err != nil {
			return nil, err
		}
		if c.charged > maxRestoreTreeCarryBytes-restoreTreeCarryRecordBytes {
			return tree, nil
		}
		c.charged += restoreTreeCarryRecordBytes
		return func(yield func(data.NodeOrError) bool) {
			var nodes []*data.Node
			var bytes uint64
			eligible := true
			defer func() { clear(nodes) }()
			for item := range tree {
				if item.Error != nil || ctx.Err() != nil {
					eligible = false
					clear(nodes)
					nodes = nil
				}
				if eligible {
					n := estimateCarriedNodeBytes(item.Node)
					nextCapacity := cap(nodes)
					if len(nodes) == nextCapacity {
						nextCapacity = max(1, 2*nextCapacity)
						// The node estimate includes one pointer. Charge the new
						// unused slots as well, before allocating their storage.
						n += uint64(nextCapacity-cap(nodes)-1) * uint64(unsafe.Sizeof(item.Node))
					}
					if n > maxPreparedTreeBytes-bytes || n > maxRestoreTreeCarryBytes-c.charged {
						eligible = false
						clear(nodes)
						nodes = nil
					} else {
						bytes += n
						c.charged += n
						if nextCapacity != cap(nodes) {
							grown := make([]*data.Node, len(nodes), nextCapacity)
							copy(grown, nodes)
							clear(nodes)
							nodes = grown
						}
						nodes = append(nodes, item.Node)
					}
				}
				// An early visitor stop must not publish a successful prefix.
				if !yield(item) {
					return
				}
			}
			if eligible && ctx.Err() == nil {
				c.records[occurrence] = preparedRestoreTree{id: id, nodes: nodes}
				nodes = nil // ownership transferred to the shelf
			}
		}, nil
	}
}

// Only the second-pass prefetch producer calls take, after the first pass has
// joined its decoder. Delete transfers ownership without replenishing credit.
func (c *restoreTreeCarry) take(occurrence int, id restic.ID) (preparedRestoreTree, bool) {
	if c == nil {
		return preparedRestoreTree{}, false
	}
	tree, ok := c.records[occurrence]
	delete(c.records, occurrence)
	if !ok {
		return preparedRestoreTree{}, false
	}
	if !tree.id.Equal(id) {
		clear(tree.nodes)
		return preparedRestoreTree{}, false
	}
	return tree, true
}

// Call after the prefetch producer has joined; it may otherwise be taking a
// record. Unused occurrences are released on every restore exit.
func (c *restoreTreeCarry) Close() {
	for occurrence, tree := range c.records {
		clear(tree.nodes)
		delete(c.records, occurrence)
	}
	c.records = nil
}

func estimateCarriedNodeBytes(node *data.Node) uint64 {
	size := estimatePreparedNodeBytes(node)
	// Non-hour offsets can allocate a location per timestamp. Charge even
	// shared fixed zones rather than borrowing a global-cache assumption.
	for _, tm := range [...]time.Time{node.ModTime, node.AccessTime, node.ChangeTime} {
		if loc := tm.Location(); loc != time.UTC && loc != time.Local {
			size += 256
		}
	}
	// Duplicate JSON fields can shorten a reused xattr slice without clearing
	// its backing tail. Those invisible attributes still retain their values.
	attrs := node.ExtendedAttributes
	for _, attr := range attrs[len(attrs):cap(attrs)] {
		size += uint64(len(attr.Name))*2 + uint64(cap(attr.Value))*2
	}
	return size
}
