package archiver

import (
	"bytes"
	"context"
	"errors"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/restic"
	"golang.org/x/sync/errgroup"
)

// treeSaver concurrently saves incoming trees to the repo.
type treeSaver struct {
	uploader restic.BlobSaverAsync
	errFn    ErrorFunc

	ch chan<- saveTreeJob
}

// newTreeSaver returns a new tree saver. A worker pool with treeWorkers is
// started, it is stopped when ctx is cancelled.
func newTreeSaver(ctx context.Context, wg *errgroup.Group, treeWorkers uint, uploader restic.BlobSaverAsync, errFn ErrorFunc) *treeSaver {
	ch := make(chan saveTreeJob)

	s := &treeSaver{
		ch:       ch,
		uploader: uploader,
		errFn:    errFn,
	}

	for range treeWorkers {
		wg.Go(func() error {
			return s.worker(ctx, ch)
		})
	}

	return s
}

func (s *treeSaver) TriggerShutdown() {
	close(s.ch)
}

// Save stores the dir d and returns the data once it has been completed.
func (s *treeSaver) Save(ctx context.Context, snPath string, target string, node *data.Node, nodes []futureNode, complete fileCompleteFunc) futureNode {
	return s.saveWithParent(ctx, snPath, target, node, nodes, complete, nil)
}

func (s *treeSaver) saveWithParent(ctx context.Context, snPath string, target string, node *data.Node, nodes []futureNode, complete fileCompleteFunc, parent *parentTreeBytes) futureNode {
	fn, ch := newFutureNode()
	job := saveTreeJob{
		snPath:   snPath,
		target:   target,
		node:     node,
		nodes:    nodes,
		ch:       ch,
		complete: complete,
		parent:   parent,
	}
	select {
	case s.ch <- job:
	case <-ctx.Done():
		parent.release()
		debug.Log("not saving tree, context is cancelled")
		close(ch)
	}

	return fn
}

type saveTreeJob struct {
	parent   *parentTreeBytes
	snPath   string
	target   string
	node     *data.Node
	nodes    []futureNode
	ch       chan<- futureNodeResult
	complete fileCompleteFunc
}

// save stores the nodes as a tree in the repo.
func (s *treeSaver) save(ctx context.Context, job *saveTreeJob) (*data.Node, ItemStats, error) {
	defer func() { job.parent.release() }()
	var stats ItemStats
	node := job.node
	nodes := job.nodes
	// allow GC of nodes array once the loop is finished
	job.nodes = nil

	builder := data.NewTreeJSONBuilder()
	if job.parent != nil {
		builder = data.NewTreeJSONBuilderWithReference(job.parent.payload)
	}
	var lastNode *data.Node

	for i, fn := range nodes {
		// fn is a copy, so clear the original value explicitly
		nodes[i] = futureNode{}
		fnr := fn.take(ctx)

		// return the error if it wasn't ignored
		if fnr.err != nil {
			debug.Log("err for %v: %v", fnr.snPath, fnr.err)
			if fnr.err == context.Canceled {
				return nil, stats, fnr.err
			}

			fnr.err = s.errFn(fnr.target, fnr.err)
			if fnr.err == nil {
				// ignore error
				continue
			}

			return nil, stats, fnr.err
		}

		// when the error is ignored, the node could not be saved, so ignore it
		if fnr.node == nil {
			debug.Log("%v excluded: %v", fnr.snPath, fnr.target)
			continue
		}

		err := builder.AddNode(fnr.node)
		if err != nil && errors.Is(err, data.ErrTreeNotOrdered) && lastNode != nil && fnr.node.Equals(*lastNode) {
			debug.Log("insert %v failed: %v", fnr.node.Name, err)
			// ignore error if an _identical_ node already exists, but nevertheless issue a warning
			_ = s.errFn(fnr.target, err)
			err = nil
		}
		if err != nil {
			debug.Log("insert %v failed: %v", fnr.node.Name, err)
			return nil, stats, err
		}
		lastNode = fnr.node
	}

	buf, err := builder.Finalize()
	if err != nil {
		return nil, stats, err
	}

	var (
		known      bool
		length     int
		sizeInRepo int
		id         restic.ID
	)

	// Equality is over the complete emitted bytes, including framing. The normal
	// save path still decides whether the blob is known and performs verification.
	var parentID restic.ID
	if job.parent != nil && bytes.Equal(buf, job.parent.payload) {
		parentID = job.parent.id
	}
	ch := make(chan struct{}, 1)
	// Save may outlive this job after cancellation. Transfer the retention
	// charge to completion because buf can alias the authenticated parent.
	parent := job.parent
	job.parent = nil
	s.uploader.SaveBlobAsync(ctx, restic.TreeBlob, buf, parentID, false, func(newID restic.ID, cbKnown bool, cbSizeInRepo int, cbErr error) {
		known = cbKnown
		length = len(buf)
		sizeInRepo = cbSizeInRepo
		id = newID
		err = cbErr
		parent.release()
		ch <- struct{}{}
	})

	select {
	case <-ch:
		if err != nil {
			return nil, stats, err
		}
		if !known {
			stats.TreeBlobs++
			stats.TreeSize += uint64(length)
			stats.TreeSizeInRepo += uint64(sizeInRepo)
		}

		node.Subtree = &id
		return node, stats, nil
	case <-ctx.Done():
		return nil, stats, ctx.Err()
	}
}

func (s *treeSaver) worker(ctx context.Context, jobs <-chan saveTreeJob) error {
	for {
		var job saveTreeJob
		var ok bool
		select {
		case <-ctx.Done():
			return nil
		case job, ok = <-jobs:
			if !ok {
				return nil
			}
		}

		node, stats, err := s.save(ctx, &job)
		if err != nil {
			debug.Log("error saving tree blob: %v", err)
			close(job.ch)
			return err
		}

		if job.complete != nil {
			job.complete(node, stats)
		}
		job.ch <- futureNodeResult{
			snPath: job.snPath,
			target: job.target,
			node:   node,
			stats:  stats,
		}
		close(job.ch)
	}
}
