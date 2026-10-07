package restorer

import (
	"context"
	"errors"
	"iter"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

func TestRestoreNodeLookaheadOverlapsAndPreservesError(t *testing.T) {
	p := newRestoreNodeLookahead(nil)
	defer p.Close()
	preparedNext := make(chan struct{})
	nodeBytes := estimatePreparedNodeBytes(&data.Node{Name: "file", Type: data.NodeTypeFile})
	firstBatchNodes := min(restoreLookaheadNodes, int((restoreLookaheadBytes+nodeBytes-1)/nodeBytes))
	totalNodes := 2*restoreLookaheadNodes + 8
	decodeErr := errors.New("later decode failure")
	tree := func(yield func(data.NodeOrError) bool) {
		for i := 0; i < totalNodes; i++ {
			if i == firstBatchNodes {
				close(preparedNext)
			}
			if !yield(data.NodeOrError{Node: &data.Node{Name: "file", Type: data.NodeTypeFile, Inode: uint64(i)}}) {
				return
			}
		}
		yield(data.NodeOrError{Error: decodeErr})
	}
	count := 0
	for item := range p.wrap(tree) {
		if count == 0 {
			select {
			case <-preparedNext:
			case <-time.After(5 * time.Second):
				t.Fatal("decoder did not advance while first visitor was occupied")
			}
		}
		if item.Error != nil {
			if item.Error != decodeErr || count != totalNodes {
				t.Fatalf("error %v at position %d", item.Error, count)
			}
			break
		}
		if item.Node.Inode != uint64(count) {
			t.Fatalf("node %q at %d", item.Node.Name, count)
		}
		count++
	}
	if len(p.credits) != 0 {
		t.Fatal("credits retained after decode error")
	}
}

func TestRestoreNodeLookaheadDirectoryBoundaryAndDeepDescent(t *testing.T) {
	p := newRestoreNodeLookahead(nil)
	defer p.Close()
	var walk func(int)
	walk = func(depth int) {
		var afterDirectory atomic.Bool
		tree := func(yield func(data.NodeOrError) bool) {
			if !yield(data.NodeOrError{Node: &data.Node{Name: "dir", Type: data.NodeTypeDir}}) {
				return
			}
			afterDirectory.Store(true)
			yield(data.NodeOrError{Node: &data.Node{Name: "file", Type: data.NodeTypeFile}})
		}
		for item := range p.wrap(tree) {
			if item.Node.Type == data.NodeTypeDir {
				if afterDirectory.Load() || len(p.credits) != 0 {
					t.Fatal("prepared state survives directory descent")
				}
				if depth != 0 {
					walk(depth - 1)
				}
			}
		}
	}
	walk(64)
	if len(p.credits) != 0 {
		t.Fatal("credits retained after nested traversal")
	}
}

func TestRestoreNodeLookaheadEarlyStopJoinsAndFallsBack(t *testing.T) {
	p := newRestoreNodeLookahead(nil)
	defer p.Close()
	var stopped atomic.Int32
	tree := func(yield func(data.NodeOrError) bool) {
		defer stopped.Add(1)
		for i := 0; i < 100; i++ {
			if !yield(data.NodeOrError{Node: &data.Node{Name: "file", Type: data.NodeTypeFile}}) {
				return
			}
		}
	}
	for range p.wrap(tree) {
		break
	}
	if stopped.Load() != 1 || len(p.credits) != 0 {
		t.Fatalf("early stop: stopped=%d credits=%d", stopped.Load(), len(p.credits))
	}
	// Occupied global credits force synchronous progress, rather than a wait
	// behind an ancestor's prepared state.
	p.credits <- struct{}{}
	p.credits <- struct{}{}
	count := 0
	for range p.wrap(tree) {
		count++
	}
	if count != 100 || stopped.Load() != 2 {
		t.Fatalf("fallback: count=%d stopped=%d", count, stopped.Load())
	}
	<-p.credits
	<-p.credits
}

func TestRestoreNodeLookaheadOversizedNodeDoesNotReadAhead(t *testing.T) {
	p := newRestoreNodeLookahead(nil)
	defer p.Close()
	var advanced atomic.Bool
	tree := func(yield func(data.NodeOrError) bool) {
		if !yield(data.NodeOrError{Node: &data.Node{Name: strings.Repeat("x", restoreLookaheadBytes), Type: data.NodeTypeFile}}) {
			return
		}
		advanced.Store(true)
		yield(data.NodeOrError{Node: &data.Node{Name: "next", Type: data.NodeTypeFile}})
	}
	for item := range p.wrap(tree) {
		if len(item.Node.Name) > restoreLookaheadBytes/2 && advanced.Load() {
			t.Fatal("read beyond oversized node before it was consumed")
		}
	}
}

func TestRestoreNodeLookaheadCloseBeforeWorkerStarts(t *testing.T) {
	for i := 0; i < 64; i++ {
		p := newRestoreNodeLookahead(nil)
		p.Close()
		p.Close()
	}
}

func TestRestoreNodeLookaheadCancellationAndVisitorPanicJoin(t *testing.T) {
	for _, panicVisitor := range []bool{false, true} {
		p := newRestoreNodeLookahead(nil)
		ctx, cancel := context.WithCancel(context.Background())
		var stopped atomic.Bool
		tree := func(yield func(data.NodeOrError) bool) {
			defer stopped.Store(true)
			for i := 0; i < 100; i++ {
				if !yield(data.NodeOrError{Node: &data.Node{Name: "file", Type: data.NodeTypeFile}}) {
					return
				}
			}
		}
		func() {
			defer func() {
				got := recover()
				if panicVisitor && got != "visitor" {
					t.Fatalf("panic = %v", got)
				}
			}()
			for range p.wrap(tree) {
				cancel()
				if panicVisitor {
					panic("visitor")
				}
				if ctx.Err() != nil {
					break
				}
			}
		}()
		cancel()
		if !stopped.Load() || len(p.credits) != 0 {
			t.Fatal("cancellation/panic did not join decoding")
		}
		p.Close()
	}
}

type lookaheadTestBlobLoader []byte

func (b lookaheadTestBlobLoader) LoadBlob(context.Context, restic.BlobHandle, []byte) ([]byte, error) {
	return b, nil
}

func TestRestoreNodeLookaheadDirectReaderLockedCaller(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	p := newRestoreNodeLookahead(func(ctx context.Context, id restic.ID) (*data.TreeNodeReader, error) {
		return data.LoadTreeNodeReader(ctx, lookaheadTestBlobLoader(`{"nodes":[{"name":"a","type":"file"},{"name":"b","type":"file"}]}`), id)
	})
	defer p.Close()
	tree, err := p.Load(context.Background(), restic.ID{})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for item := range tree {
		if item.Error != nil {
			t.Fatal(item.Error)
		}
		count++
	}
	if count != 2 || len(p.credits) != 0 {
		t.Fatal("locked caller failed")
	}
}

// Synthetic iterators can use this adapter in tests. Production Load uses the
// direct reader, avoiding one coroutine per tree and per-node switches.
func (p *restoreNodeLookahead) wrap(tree data.TreeNodeIterator) data.TreeNodeIterator {
	return p.wrapSource(func() (func() (data.NodeOrError, bool), func()) {
		return iter.Pull(tree)
	})
}
