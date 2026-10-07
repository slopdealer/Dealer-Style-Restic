package restorer

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"

	"golang.org/x/sync/errgroup"
)

// Restorer is used to restore a snapshot to a directory.
type Restorer struct {
	repo restic.Repository
	sn   *data.Snapshot
	opts Options

	fileList map[string]bool

	Error func(location string, err error) error
	Warn  func(message string)
	Info  func(message string)
	// SelectFilter determines whether the item is selectedForRestore or whether a childMayBeSelected.
	// selectedForRestore must not depend on isDir as `removeUnexpectedFiles` always passes false to isDir.
	SelectFilter func(item string, isDir bool) (selectedForRestore bool, childMayBeSelected bool)

	XattrSelectFilter func(xattrName string) (xattrSelectedForRestore bool)

	// settleBeforeError lets a concurrent restore pass report pending worker
	// outcomes before a later traversal error is passed to Error.
	settleBeforeError func() error

	// concurrentXattrSelectFilter is set only when the configured callback is
	// known to be safe for concurrent calls. Direct assignments to the public
	// callback keep the synchronous restore path.
	concurrentXattrSelectFilter bool
}

// SetConcurrentXattrSelectFilter configures a filter that regular-file
// metadata workers may call concurrently. The caller must provide a
// concurrency-safe filter. Direct assignment to XattrSelectFilter preserves
// the synchronous callback behavior.
func (res *Restorer) SetConcurrentXattrSelectFilter(filter func(xattrName string) bool) {
	res.XattrSelectFilter = filter
	res.concurrentXattrSelectFilter = true
}

var restorerAbortOnAllErrors = func(_ string, err error) error { return err }

type Options struct {
	DryRun          bool
	Sparse          bool
	Progress        ProgressReporter
	Overwrite       OverwriteBehavior
	Delete          bool
	OwnershipByName bool
}

type OverwriteBehavior int

// Constants for different overwrite behavior
const (
	OverwriteAlways OverwriteBehavior = iota
	// OverwriteIfChanged is like OverwriteAlways except that it skips restoring the content
	// of files with matching size&mtime. Metadata is always restored.
	OverwriteIfChanged
	OverwriteIfNewer
	OverwriteNever
	OverwriteInvalid
)

// Set implements the method needed for pflag command flag parsing.
func (c *OverwriteBehavior) Set(s string) error {
	switch s {
	case "always":
		*c = OverwriteAlways
	case "if-changed":
		*c = OverwriteIfChanged
	case "if-newer":
		*c = OverwriteIfNewer
	case "never":
		*c = OverwriteNever
	default:
		*c = OverwriteInvalid
		return fmt.Errorf("invalid overwrite behavior %q, must be one of (always|if-changed|if-newer|never)", s)
	}

	return nil
}

func (c *OverwriteBehavior) String() string {
	switch *c {
	case OverwriteAlways:
		return "always"
	case OverwriteIfChanged:
		return "if-changed"
	case OverwriteIfNewer:
		return "if-newer"
	case OverwriteNever:
		return "never"
	default:
		return "invalid"
	}

}
func (c *OverwriteBehavior) Type() string {
	return "behavior"
}

// NewRestorer creates a restorer preloaded with the content from the snapshot id.
func NewRestorer(repo restic.Repository, sn *data.Snapshot, opts Options) *Restorer {
	opts.Progress = progressOrNoop(opts.Progress)
	r := &Restorer{
		repo:              repo,
		opts:              opts,
		fileList:          make(map[string]bool),
		Error:             restorerAbortOnAllErrors,
		SelectFilter:      func(string, bool) (bool, bool) { return true, true },
		XattrSelectFilter: func(string) bool { return true },
		sn:                sn,
	}

	return r
}

type treeVisitor struct {
	enterDir  func(node *data.Node, target, location string) error
	visitNode func(node *data.Node, target, location string) error
	// 'entries' contains all files the snapshot contains for this node. This also includes files
	// ignored by the SelectFilter.
	leaveDir func(node *data.Node, target, location string, entries []string) error
}

func (res *Restorer) sanitizeError(location string, err error) error {
	switch err {
	case nil, context.Canceled, context.DeadlineExceeded:
		// Context errors are permanent.
		return err
	default:
		if res.settleBeforeError != nil {
			if settleErr := res.settleBeforeError(); settleErr != nil {
				return settleErr
			}
		}
		return res.Error(location, err)
	}
}

// traverseTree traverses a tree from the repo and calls treeVisitor.
// target is the path in the file system, location within the snapshot.
func (res *Restorer) traverseTree(ctx context.Context, target string, treeID restic.ID, visitor treeVisitor) error {
	return res.traverseTreeWithTreeAccess(ctx, target, treeID, nil, nil, visitor)
}

func (res *Restorer) traverseTreeWithTreeAccess(ctx context.Context, target string, treeID restic.ID, loadTree restoreTreeLoader, observeTree func(restic.ID), visitor treeVisitor) error {
	location := string(filepath.Separator)

	if visitor.enterDir != nil {
		err := res.sanitizeError(location, visitor.enterDir(nil, target, location))
		if err != nil {
			return err
		}
	}
	childFilenames, hasRestored, err := res.traverseTreeInnerWithTreeAccess(ctx, target, location, treeID, loadTree, observeTree, visitor)
	if err != nil {
		return err
	}
	if hasRestored && visitor.leaveDir != nil {
		err = res.sanitizeError(location, visitor.leaveDir(nil, target, location, childFilenames))
	}

	return err
}

func (res *Restorer) traverseTreeInner(ctx context.Context, target, location string, treeID restic.ID, visitor treeVisitor) (filenames []string, hasRestored bool, err error) {
	return res.traverseTreeInnerWithTreeAccess(ctx, target, location, treeID, nil, nil, visitor)
}

func (res *Restorer) traverseTreeInnerWithTreeAccess(ctx context.Context, target, location string, treeID restic.ID, loadTree restoreTreeLoader, observeTree func(restic.ID), visitor treeVisitor) (filenames []string, hasRestored bool, err error) {
	debug.Log("%v %v %v", target, location, treeID)
	if observeTree != nil {
		observeTree(treeID)
	}
	var tree data.TreeNodeIterator
	if loadTree == nil {
		tree, err = data.LoadTree(ctx, res.repo, treeID)
	} else {
		tree, err = loadTree(ctx, treeID)
	}
	if err != nil {
		debug.Log("error loading tree %v: %v", treeID, err)
		return nil, hasRestored, res.sanitizeError(location, err)
	}

	if res.opts.Delete {
		filenames = make([]string, 0)
	}
	for item := range tree {
		if item.Error != nil {
			debug.Log("error iterating tree %v: %v", treeID, item.Error)
			return nil, hasRestored, res.sanitizeError(location, item.Error)
		}
		node := item.Node
		if ctx.Err() != nil {
			return nil, hasRestored, ctx.Err()
		}

		if res.opts.Delete {
			// just track all files included in the tree node to simplify the control flow.
			// tracking too many files does not matter except for a slightly elevated memory usage
			filenames = append(filenames, node.Name)
		}

		// ensure that the node name does not contain anything that refers to a
		// top-level directory.
		nodeName := filepath.Base(filepath.Join(string(filepath.Separator), node.Name))
		if nodeName != node.Name {
			debug.Log("node %q has invalid name %q", node.Name, nodeName)
			err := res.sanitizeError(location, errors.Errorf("invalid child node name %s", node.Name))
			if err != nil {
				return nil, hasRestored, err
			}
			// force disable deletion to prevent unexpected behavior
			res.opts.Delete = false
			continue
		}

		nodeTarget := filepath.Join(target, nodeName)
		nodeLocation := filepath.Join(location, nodeName)

		if target == nodeTarget || !fs.HasPathPrefix(target, nodeTarget) {
			debug.Log("target: %v %v", target, nodeTarget)
			debug.Log("node %q has invalid target path %q", node.Name, nodeTarget)
			err := res.sanitizeError(nodeLocation, errors.New("node has invalid path"))
			if err != nil {
				return nil, hasRestored, err
			}
			// force disable deletion to prevent unexpected behavior
			res.opts.Delete = false
			continue
		}

		// sockets cannot be restored
		if node.Type == data.NodeTypeSocket {
			continue
		}

		selectedForRestore, childMayBeSelected := res.SelectFilter(nodeLocation, node.Type == data.NodeTypeDir)
		debug.Log("SelectFilter returned %v %v for %q", selectedForRestore, childMayBeSelected, nodeLocation)

		if selectedForRestore {
			hasRestored = true
		}

		if node.Type == data.NodeTypeDir {
			if node.Subtree == nil {
				return nil, hasRestored, errors.Errorf("Dir without subtree in tree %v", treeID.Str())
			}

			if selectedForRestore && visitor.enterDir != nil {
				err = res.sanitizeError(nodeLocation, visitor.enterDir(node, nodeTarget, nodeLocation))
				if err != nil {
					return nil, hasRestored, err
				}
			}

			// keep track of restored child status
			// so metadata of the current directory are restored on leaveDir
			childHasRestored := false
			var childFilenames []string

			if childMayBeSelected {
				childFilenames, childHasRestored, err = res.traverseTreeInnerWithTreeAccess(ctx, nodeTarget, nodeLocation, *node.Subtree, loadTree, observeTree, visitor)
				err = res.sanitizeError(nodeLocation, err)
				if err != nil {
					return nil, hasRestored, err
				}
				// inform the parent directory to restore parent metadata on leaveDir if needed
				if childHasRestored {
					hasRestored = true
				}
			}

			// metadata need to be restore when leaving the directory in both cases
			// selected for restore or any child of any subtree have been restored
			if (selectedForRestore || childHasRestored) && visitor.leaveDir != nil {
				err = res.sanitizeError(nodeLocation, visitor.leaveDir(node, nodeTarget, nodeLocation, childFilenames))
				if err != nil {
					return nil, hasRestored, err
				}
			}

			continue
		}

		if selectedForRestore {
			err = res.sanitizeError(nodeLocation, visitor.visitNode(node, nodeTarget, nodeLocation))
			if err != nil {
				return nil, hasRestored, err
			}
		}
	}

	return filenames, hasRestored, nil
}

func (res *Restorer) restoreNodeTo(node *data.Node, target, location string) error {
	if !res.opts.DryRun {
		debug.Log("restoreNode %v %v %v", node.Name, target, location)
		if err := fs.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "RemoveNode")
		}

		err := fs.NodeCreateAt(node, target)
		if err != nil {
			debug.Log("node.CreateAt(%s) error %v", target, err)
			return err
		}
	}

	res.opts.Progress.AddProgress(location, ActionOtherRestored, 0, 0)
	return res.restoreNodeMetadataTo(node, target, location)
}

func (res *Restorer) restoreNodeMetadataTo(node *data.Node, target, location string) error {
	return res.restoreNodeMetadataToWithWarn(node, target, location, res.Warn)
}

func (res *Restorer) restoreNodeMetadataToWithWarn(node *data.Node, target, location string, warn func(string)) error {
	if res.opts.DryRun {
		return nil
	}
	debug.Log("restoreNodeMetadata %v %v %v", node.Name, target, location)
	err := fs.NodeRestoreMetadata(node, target, warn, res.XattrSelectFilter, res.opts.OwnershipByName)
	if err != nil {
		debug.Log("node.RestoreMetadata(%s) error %v", target, err)
	}
	return err
}

func (res *Restorer) restoreHardlinkAt(node *data.Node, target, path, location string) error {
	if !res.opts.DryRun {
		if err := fs.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return errors.Wrap(err, "RemoveCreateHardlink")
		}
		err := fs.Link(target, path)
		if err != nil {
			return errors.WithStack(err)
		}
	}

	res.opts.Progress.AddProgress(location, ActionOtherRestored, 0, 0)
	// TODO investigate if hardlinks have separate metadata on any supported system
	return res.restoreNodeMetadataTo(node, path, location)
}

func (res *Restorer) ensureDir(target string) error {
	_, err := res.ensureDirWithFreshness(target)
	return err
}

// ensureDirWithFreshness reports whether target was absent or replaced by a
// directory during this call. A fresh directory is known to contain none of
// the snapshot's files before the second restore pass starts.
func (res *Restorer) ensureDirWithFreshness(target string) (bool, error) {
	if res.opts.DryRun {
		return false, nil
	}

	fi, err := fs.Lstat(target)
	missing := errors.Is(err, os.ErrNotExist)
	if err != nil && !missing {
		return false, fmt.Errorf("failed to check for directory: %w", err)
	}
	if err == nil && !fi.IsDir() {
		// try to cleanup unexpected file
		if err := fs.Remove(target); err != nil {
			return false, fmt.Errorf("failed to remove stale item: %w", err)
		}
		missing = true
	}
	if !missing {
		// Retain MkdirAll's behavior if the existing directory disappears
		// between Lstat and this call.
		if err := fs.MkdirAll(target, 0700); err != nil {
			return false, err
		}
		return false, nil
	}

	// Create parents first, then use exclusive creation for target. If another
	// process creates target after Lstat, the fallback preserves MkdirAll's
	// behavior but does not claim that target is fresh.
	if err := fs.MkdirAll(filepath.Dir(target), 0700); err != nil {
		return false, err
	}
	if err := fs.Mkdir(target, 0700); err == nil {
		return true, nil
	}
	// second pass #leaveDir restores directory metadata after visiting all children
	if err := fs.MkdirAll(target, 0700); err != nil {
		return false, err
	}
	return false, nil
}

// lastPreparedDirectory remembers one directory that was successfully
// prepared during a traversal. Sibling nodes commonly share a parent, so this
// avoids repeating its filesystem check.
type lastPreparedDirectory struct {
	target string
	valid  bool
	fresh  bool
}

func (d *lastPreparedDirectory) ensure(target string, ensureDir func(string) error) error {
	if d.valid && d.target == target {
		return nil
	}
	if err := ensureDir(target); err != nil {
		return err
	}
	d.target = target
	d.valid = true
	d.fresh = false
	return nil
}

func (d *lastPreparedDirectory) ensureWithFreshness(target string, ensureDir func(string) (bool, error)) (bool, error) {
	if d.valid && d.target == target {
		return d.fresh, nil
	}
	fresh, err := ensureDir(target)
	if err != nil {
		return false, err
	}
	d.target = target
	d.valid = true
	d.fresh = fresh
	return fresh, nil
}

func (d *lastPreparedDirectory) reset() {
	d.target = ""
	d.valid = false
	d.fresh = false
}

// restoreMetadataTask is one independent regular-file metadata update. The
// traversal owns task ordering; workers own only the filesystem call and its
// warnings/error. Keeping the result on the task lets the traversal report
// failures and warnings in snapshot order.
type restoreMetadataTask struct {
	location string
	apply    func(context.Context, func(string)) error
	warnings []string
	done     chan struct{}
	err      error
}

// restoreMetadataPool bounds both active filesystem work and retained task
// state. Directory metadata and hardlink aliases stay in the traversal goroutine
// and drain this pool first, preserving their dependencies on descendants and
// on the canonical hardlink path.
type restoreMetadataPool struct {
	res      *Restorer
	ctx      context.Context
	cancel   context.CancelFunc
	jobs     chan *restoreMetadataTask
	workers  sync.WaitGroup
	pending  []*restoreMetadataTask
	firstErr error
	closed   bool
	settling int
}

const (
	restoreMetadataWorkerCount = 4
	restoreMetadataPendingMax  = 64
)

func newRestoreMetadataPool(ctx context.Context, res *Restorer) *restoreMetadataPool {
	workerCtx, cancel := context.WithCancel(ctx)
	p := &restoreMetadataPool{
		res:    res,
		ctx:    workerCtx,
		cancel: cancel,
		jobs:   make(chan *restoreMetadataTask, restoreMetadataPendingMax),
	}
	for range restoreMetadataWorkerCount {
		p.workers.Add(1)
		go func() {
			defer p.workers.Done()
			for task := range p.jobs {
				if err := p.ctx.Err(); err != nil {
					task.err = err
				} else {
					task.err = task.apply(p.ctx, func(message string) {
						if res.Warn != nil {
							task.warnings = append(task.warnings, message)
						}
					})
				}
				close(task.done)
			}
		}()
	}
	return p
}

func (p *restoreMetadataPool) waitOldest() error {
	if len(p.pending) == 0 {
		return nil
	}
	if p.firstErr != nil {
		return context.Canceled
	}

	task := p.pending[0]
	select {
	case <-task.done:
	case <-p.ctx.Done():
		// Prefer a completed task so an earlier filesystem failure is not
		// hidden by a cancellation that arrived at the same time.
		select {
		case <-task.done:
		default:
			if p.firstErr == nil {
				p.firstErr = p.ctx.Err()
			}
			return p.ctx.Err()
		}
	}
	p.pending[0] = nil
	p.pending = p.pending[1:]

	for _, warning := range task.warnings {
		p.res.Warn(warning)
	}
	if task.err != nil {
		// Suppress a nested drain only while reporting this worker error.
		// Successful metadata tasks stay on the existing fast path.
		p.settling++
		err := p.res.sanitizeError(task.location, task.err)
		p.settling--
		if err != nil {
			p.firstErr = err
			p.cancel()
			return context.Canceled
		}
	}
	return nil
}

func (p *restoreMetadataPool) schedule(location string, apply func(context.Context, func(string)) error) error {
	if p.firstErr != nil {
		return context.Canceled
	}
	if err := p.ctx.Err(); err != nil {
		if p.firstErr == nil {
			p.firstErr = err
		}
		return err
	}
	for len(p.pending) >= restoreMetadataPendingMax {
		if err := p.waitOldest(); err != nil {
			return err
		}
	}

	task := &restoreMetadataTask{
		location: location,
		apply:    apply,
		done:     make(chan struct{}),
	}
	select {
	case <-p.ctx.Done():
		if p.firstErr == nil {
			p.firstErr = p.ctx.Err()
		}
		return p.ctx.Err()
	case p.jobs <- task:
		p.pending = append(p.pending, task)
		return nil
	}
}

func (p *restoreMetadataPool) drain() error {
	for len(p.pending) > 0 {
		if err := p.waitOldest(); err != nil {
			return err
		}
	}
	return nil
}

func (p *restoreMetadataPool) close() {
	if p.closed {
		return
	}
	p.cancel()
	close(p.jobs)
	p.workers.Wait()
	p.pending = nil
	p.closed = true
}

// finish settles accepted metadata tasks before returning a later traversal
// error. The tree walk is synchronous, so every accepted task was visited
// before the traversal reached its error. Reporting those task results first
// preserves the traversal's error and warning order.
func (p *restoreMetadataPool) finish(traversalErr error) error {
	if p.firstErr == nil {
		if poolErr := p.drain(); poolErr != nil && traversalErr == nil {
			traversalErr = poolErr
		}
	}
	p.close()
	if p.firstErr != nil {
		return p.firstErr
	}
	return traversalErr
}

// drainBeforeTraversalError keeps an earlier worker result from being
// reported after a later traversal diagnostic. waitOldest also calls
// sanitizeError, so settling must be reentrant: worker failures are reported
// by the normal callback without starting another drain.
func (p *restoreMetadataPool) drainBeforeTraversalError() error {
	if p.settling > 0 {
		return nil
	}
	p.settling++
	defer func() { p.settling-- }()
	return p.drain()
}

// RestoreTo creates the directories and files in the snapshot below dst.
// Before an item is created, res.Filter is called.
func (res *Restorer) RestoreTo(ctx context.Context, dst string) (uint64, error) {
	restoredFileCount := uint64(0)
	var err error
	if !filepath.IsAbs(dst) {
		dst, err = filepath.Abs(dst)
		if err != nil {
			return restoredFileCount, errors.Wrap(err, "Abs")
		}
	}

	// Only paths created by an exclusive Mkdir are fresh. A directory that
	// already exists (or wins a creation race) must retain file verification.
	freshDirs := make(map[string]struct{})
	var preparedDir lastPreparedDirectory
	if !res.opts.DryRun {
		// ensure that the target directory exists and is actually a directory
		// Using ensureDir is too aggressive here as it also removes unexpected files
		dstFresh := fs.Mkdir(dst, 0700) == nil
		if !dstFresh {
			if err := fs.MkdirAll(dst, 0700); err != nil {
				return restoredFileCount, fmt.Errorf("cannot create target directory: %w", err)
			}
		}
		if dstFresh {
			freshDirs[dst] = struct{}{}
		}
	}

	// The first pass only needs to recognize duplicate hardlinks so their
	// contents are not restored more than once. Keep no canonical path here:
	// the path-bearing index is rebuilt in the second pass, after file workers
	// have finished.
	seenLinks := data.NewHardlinkIndex[struct{}]()
	filerestorer := newFileRestorer(dst, res.repo.LoadBlobsFromPack, res.repo.LookupBlob,
		res.repo.Connections(), res.opts.Sparse, res.opts.Delete, res.repo.StartWarmup, res.opts.Progress,
		res.repo.ChunkerFactory().ZeroChunk())
	filerestorer.Error = res.Error
	filerestorer.Info = res.Info
	if extra, ok := res.repo.(interface{ AdditionalRestoreWorker() bool }); ok {
		filerestorer.additionalWorker = extra.AdditionalRestoreWorker()
	}

	debug.Log("first pass for %q", dst)
	prepareDirOnce := func(target string) (bool, error) {
		if res.opts.DryRun {
			return false, nil
		}
		if preparedDir.valid && preparedDir.target == target {
			return preparedDir.fresh, nil
		}
		if _, knownFresh := freshDirs[target]; knownFresh {
			// The target was created earlier in this traversal or before it
			// started. Keep the usual directory check, but retain that proof.
			if err := res.ensureDir(target); err != nil {
				return false, err
			}
			preparedDir = lastPreparedDirectory{target: target, valid: true, fresh: true}
			return true, nil
		}
		fresh, err := preparedDir.ensureWithFreshness(target, res.ensureDirWithFreshness)
		if err != nil {
			return false, err
		}
		if fresh {
			freshDirs[target] = struct{}{}
		}
		return fresh, nil
	}

	var buf []byte
	var restoreTreeIDs []restic.ID
	var observeTree func(restic.ID)
	if !res.opts.DryRun {
		observeTree = func(id restic.ID) {
			restoreTreeIDs = append(restoreTreeIDs, id)
		}
	}

	var treeCarry *restoreTreeCarry
	if _, concrete := res.repo.(*repository.Repository); concrete && !res.opts.DryRun {
		treeCarry = newRestoreTreeCarry()
		defer treeCarry.Close()
	}

	nodeLookahead := newRestoreNodeLookahead(func(ctx context.Context, id restic.ID) (*data.TreeNodeReader, error) {
		return data.LoadTreeNodeReader(ctx, res.repo, id)
	})
	defer nodeLookahead.Close()

	firstPassTreeLoader := restoreTreeLoader(nodeLookahead.Load)
	if treeCarry != nil {
		firstPassTreeLoader = treeCarry.Wrap(firstPassTreeLoader)
	}

	// first tree pass: create directories and collect all files to restore
	err = res.traverseTreeWithTreeAccess(ctx, dst, *res.sn.Tree, firstPassTreeLoader, observeTree, treeVisitor{
		enterDir: func(_ *data.Node, target, location string) error {
			debug.Log("first pass, enterDir: mkdir %q, leaveDir should restore metadata", location)
			if location != string(filepath.Separator) {
				res.opts.Progress.AddFile(0)
			}
			_, err := prepareDirOnce(target)
			return err
		},

		visitNode: func(node *data.Node, target, location string) error {
			debug.Log("first pass, visitNode: mkdir %q, leaveDir on second pass should restore metadata", location)
			parentDir := filepath.Dir(target)
			if _, err := prepareDirOnce(parentDir); err != nil {
				return err
			}

			if node.Type != data.NodeTypeFile {
				res.opts.Progress.AddFile(0)
				return nil
			}

			if node.Links > 1 {
				if seenLinks.Has(node.Inode, node.DeviceID) {
					// a hardlinked file does not increase the restore size
					res.opts.Progress.AddFile(0)
					return nil
				}
				seenLinks.Add(node.Inode, node.DeviceID, struct{}{})
			}

			_, parentFresh := freshDirs[parentDir]
			buf, err = res.withOverwriteCheckInFreshDirectory(ctx, node, target, location, false, parentFresh, buf, func(updateMetadataOnly bool, matches *fileState) error {
				if updateMetadataOnly {
					res.opts.Progress.AddSkippedFile(location, node.Size)
				} else {
					res.opts.Progress.AddFile(node.Size)
					if !res.opts.DryRun {
						filerestorer.addFile(location, node.Content, int64(node.Size), matches)
					} else {
						action := ActionFileUpdated
						if matches == nil {
							action = ActionFileRestored
						}
						// immediately mark as completed
						res.opts.Progress.AddProgress(location, action, node.Size, node.Size)
					}
				}
				res.trackFile(location, updateMetadataOnly)
				if !updateMetadataOnly {
					restoredFileCount++
				}
				return nil
			})
			return err
		},
	})
	nodeLookahead.Close()
	// File restoration starts below, after this traversal. Release the cached
	// path before workers begin restoring file contents.
	preparedDir.reset()
	if err != nil {
		return 0, err
	}
	seenLinks = nil

	var treePrefetch *restoreTreePrefetch
	if !res.opts.DryRun && len(filerestorer.files) != 0 && len(restoreTreeIDs) != 0 {
		loadTree := restoreTreeLoader(func(ctx context.Context, id restic.ID) (data.TreeNodeIterator, error) {
			return data.LoadTree(ctx, res.repo, id)
		})
		treePrefetch = newRestoreTreePrefetchWithCarry(ctx, restoreTreeIDs, loadTree, treeCarry)
		defer treePrefetch.Close()
	}

	if !res.opts.DryRun {
		err = filerestorer.restoreFiles(ctx)
		if err != nil {
			return 0, err
		}
	}

	debug.Log("second pass for %q", dst)
	idx := data.NewHardlinkIndex[string]()
	var metadataPool *restoreMetadataPool
	traverseCtx := ctx
	previousSettleBeforeError := res.settleBeforeError
	if res.concurrentXattrSelectFilter && !res.opts.DryRun {
		metadataPool = newRestoreMetadataPool(ctx, res)
		traverseCtx = metadataPool.ctx
		res.settleBeforeError = metadataPool.drainBeforeTraversalError
	}

	// second tree pass: restore special files and filesystem metadata
	var secondPassTreeLoader restoreTreeLoader
	if treePrefetch != nil {
		secondPassTreeLoader = treePrefetch.Load
	}
	err = res.traverseTreeWithTreeAccess(traverseCtx, dst, *res.sn.Tree, secondPassTreeLoader, nil, treeVisitor{
		visitNode: func(node *data.Node, target, location string) error {
			debug.Log("second pass, visitNode: restore node %q", location)
			if node.Type != data.NodeTypeFile {
				if metadataPool != nil {
					if err := metadataPool.drain(); err != nil {
						return err
					}
				}
				_, err := res.withOverwriteCheck(ctx, node, target, location, false, nil, func(_ bool, _ *fileState) error {
					return res.restoreNodeTo(node, target, location)
				})
				return err
			}

			if node.Links > 1 {
				if idx.Has(node.Inode, node.DeviceID) {
					if metadataPool != nil {
						if err := metadataPool.drain(); err != nil {
							return err
						}
					}
					_, err := res.withOverwriteCheck(ctx, node, target, location, true, nil, func(_ bool, _ *fileState) error {
						return res.restoreHardlinkAt(node, filerestorer.targetPath(idx.Value(node.Inode, node.DeviceID)), target, location)
					})
					return err
				}
				idx.Add(node.Inode, node.DeviceID, location)
			}

			if _, ok := res.hasRestoredFile(location); ok {
				if metadataPool != nil {
					return metadataPool.schedule(location, func(_ context.Context, warn func(string)) error {
						return res.restoreNodeMetadataToWithWarn(node, target, location, warn)
					})
				}
				return res.restoreNodeMetadataTo(node, target, location)
			}
			// don't touch skipped files
			return nil
		},
		leaveDir: func(node *data.Node, target, location string, expectedFilenames []string) error {
			if metadataPool != nil {
				if err := metadataPool.drain(); err != nil {
					return err
				}
			}
			if res.opts.Delete {
				if err := res.removeUnexpectedFiles(ctx, target, location, expectedFilenames); err != nil {
					return err
				}
			}

			if node == nil {
				return nil
			}

			err := res.restoreNodeMetadataTo(node, target, location)
			if err == nil {
				res.opts.Progress.AddProgress(location, ActionDirRestored, 0, 0)
			}
			return err
		},
	})
	if metadataPool != nil {
		res.settleBeforeError = previousSettleBeforeError
		err = metadataPool.finish(err)
	}
	return restoredFileCount, err
}

func (res *Restorer) removeUnexpectedFiles(ctx context.Context, target, location string, expectedFilenames []string) error {
	if !res.opts.Delete {
		panic("internal error")
	}

	entries, err := fs.Readdirnames(fs.NewLocal(), target, fs.O_NOFOLLOW)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}

	keep := map[string]struct{}{}
	for _, name := range expectedFilenames {
		keep[toComparableFilename(name)] = struct{}{}
	}

	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		if _, ok := keep[toComparableFilename(entry)]; ok {
			continue
		}

		nodeTarget := filepath.Join(target, entry)
		nodeLocation := filepath.Join(location, entry)

		if target == nodeTarget || !fs.HasPathPrefix(target, nodeTarget) {
			return fmt.Errorf("skipping deletion due to invalid filename: %v", entry)
		}

		// TODO pass a proper value to the isDir parameter once this becomes relevant for the filters
		selectedForRestore, _ := res.SelectFilter(nodeLocation, false)
		// only delete files that were selected for restore
		if selectedForRestore {
			// First collect all files that will be deleted
			var filesToDelete []string
			err := filepath.Walk(nodeTarget, func(path string, _ os.FileInfo, err error) error {
				if err != nil {
					return err
				}
				filesToDelete = append(filesToDelete, path)
				return nil
			})
			if err != nil {
				return err
			}

			if !res.opts.DryRun {
				// Perform the deletion
				if err := fs.RemoveAll(nodeTarget); err != nil {
					return err
				}
			}

			// Report paths as deleted only after successful removal
			for _, f := range slices.Backward(filesToDelete) {
				res.opts.Progress.ReportDeletion(f)
			}
		}
	}

	return nil
}

func (res *Restorer) trackFile(location string, metadataOnly bool) {
	res.fileList[location] = metadataOnly
}

func (res *Restorer) hasRestoredFile(location string) (metadataOnly bool, ok bool) {
	metadataOnly, ok = res.fileList[location]
	return metadataOnly, ok
}

func (res *Restorer) withOverwriteCheck(ctx context.Context, node *data.Node, target, location string, isHardlink bool, buf []byte, cb func(updateMetadataOnly bool, matches *fileState) error) ([]byte, error) {
	return res.withOverwriteCheckInFreshDirectory(ctx, node, target, location, isHardlink, false, buf, cb)
}

func (res *Restorer) withOverwriteCheckInFreshDirectory(ctx context.Context, node *data.Node, target, location string, isHardlink, parentFresh bool, buf []byte, cb func(updateMetadataOnly bool, matches *fileState) error) ([]byte, error) {
	overwrite, err := shouldOverwrite(res.opts.Overwrite, node, target)
	if err != nil {
		return buf, err
	} else if !overwrite {
		size := node.Size
		if isHardlink {
			size = 0
		}
		res.opts.Progress.AddSkippedFile(location, size)
		return buf, nil
	}

	var matches *fileState
	updateMetadataOnly := false
	if node.Type == data.NodeTypeFile && !isHardlink {
		// A newly created parent directory proves the file could not have been
		// present when the first pass checked it. File workers start only after
		// that pass, so no restore worker can create it before this decision.
		if !parentFresh {
			// if a file fails to verify, then matches is nil which results in restoring from scratch
			matches, buf, _ = res.verifyFile(ctx, target, node, false, res.opts.Overwrite == OverwriteIfChanged, buf)
		}
		// skip files that are already correct completely
		updateMetadataOnly = !matches.NeedsRestore()
	}

	return buf, cb(updateMetadataOnly, matches)
}

func shouldOverwrite(overwrite OverwriteBehavior, node *data.Node, destination string) (bool, error) {
	if overwrite == OverwriteAlways || overwrite == OverwriteIfChanged {
		return true, nil
	}

	fi, err := fs.Lstat(destination)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}

	switch overwrite {
	case OverwriteIfNewer:
		// return if node is newer
		return node.ModTime.After(fi.ModTime()), nil
	case OverwriteNever:
		// file exists
		return false, nil
	}
	panic("unknown overwrite behavior")
}

// Snapshot returns the snapshot this restorer is configured to use.
func (res *Restorer) Snapshot() *data.Snapshot {
	return res.sn
}

// Number of workers in VerifyFiles.
const nVerifyWorkers = 8

// VerifyFiles checks whether all regular files in the snapshot res.sn
// have been successfully written to dst. It stops when it encounters an
// error. It returns that error and the number of files it has successfully
// verified.
func (res *Restorer) VerifyFiles(ctx context.Context, dst string, countRestoredFiles uint64, p restic.Counter) (int, error) {
	type mustCheck struct {
		node *data.Node
		path string
	}

	var (
		nchecked uint64
		work     = make(chan mustCheck, 2*nVerifyWorkers)
	)

	p.SetMax(countRestoredFiles)
	defer p.Done()

	g, ctx := errgroup.WithContext(ctx)

	// Traverse tree and send jobs to work.
	g.Go(func() error {
		defer close(work)

		err := res.traverseTree(ctx, dst, *res.sn.Tree, treeVisitor{
			visitNode: func(node *data.Node, target, location string) error {
				if node.Type != data.NodeTypeFile {
					return nil
				}
				if metadataOnly, ok := res.hasRestoredFile(location); !ok || metadataOnly {
					return nil
				}
				select {
				case <-ctx.Done():
					return ctx.Err()
				case work <- mustCheck{node, target}:
					return nil
				}
			},
		})
		return err
	})

	for range nVerifyWorkers {
		g.Go(func() (err error) {
			var buf []byte
			for job := range work {
				_, buf, err = res.verifyFile(ctx, job.path, job.node, true, false, buf)
				err = res.sanitizeError(job.path, err)
				if err != nil || ctx.Err() != nil {
					break
				}
				p.Add(1)
				atomic.AddUint64(&nchecked, 1)
			}
			return err
		})
	}

	return int(nchecked), g.Wait()
}

type fileState struct {
	blobMatches []bool
	sizeMatches bool
}

func (s *fileState) NeedsRestore() bool {
	if s == nil {
		return true
	}
	if !s.sizeMatches {
		return true
	}
	for _, match := range s.blobMatches {
		if !match {
			return true
		}
	}
	return false
}

func (s *fileState) HasMatchingBlob(i int) bool {
	if s == nil || s.blobMatches == nil {
		return false
	}
	return i < len(s.blobMatches) && s.blobMatches[i]
}

// Verify that the file target has the contents of node.
//
// buf and the first return value are scratch space, passed around for reuse.
// Reusing buffers prevents the verifier goroutines allocating all of RAM and
// flushing the filesystem cache (at least on Linux).
func (res *Restorer) verifyFile(ctx context.Context, target string, node *data.Node, failFast bool, trustMtime bool, buf []byte) (*fileState, []byte, error) {
	f, err := fs.OpenRegularFileNoPoller(target, fs.O_RDONLY|fs.O_NOFOLLOW, 0)
	if err != nil {
		return nil, buf, err
	}
	defer func() {
		_ = f.Close()
	}()

	fi, err := f.Stat()
	sizeMatches := true
	switch {
	case err != nil:
		return nil, buf, err
	case !fi.Mode().IsRegular():
		return nil, buf, errors.Errorf("Expected %s to be a regular file", target)
	case int64(node.Size) != fi.Size():
		if failFast {
			return nil, buf, errors.Errorf("Invalid file size for %s: expected %d, got %d",
				target, node.Size, fi.Size())
		}
		sizeMatches = false
	}

	if trustMtime && fi.ModTime().Equal(node.ModTime) && sizeMatches {
		return &fileState{nil, sizeMatches}, buf, nil
	}

	matches := make([]bool, len(node.Content))
	var offset int64
	for i, blobID := range node.Content {
		if ctx.Err() != nil {
			return nil, buf, ctx.Err()
		}
		length, found := res.repo.LookupBlobSize(restic.BlobHandle{Type: restic.DataBlob, ID: blobID})
		if !found {
			return nil, buf, errors.Errorf("Unable to fetch blob %s", blobID)
		}

		if length > uint(cap(buf)) {
			buf = make([]byte, 2*length)
		}
		buf = buf[:length]

		_, err = f.ReadAt(buf, offset)
		if err == io.EOF && !failFast {
			sizeMatches = false
			break
		}
		if err != nil {
			return nil, buf, err
		}
		matches[i] = blobID.Equal(restic.Hash(buf))
		if failFast && !matches[i] {
			return nil, buf, errors.Errorf(
				"Unexpected content in %s, starting at offset %d",
				target, offset)
		}
		offset += int64(length)
	}

	return &fileState{matches, sizeMatches}, buf, nil
}
