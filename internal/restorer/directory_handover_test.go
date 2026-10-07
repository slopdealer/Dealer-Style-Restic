package restorer

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// goroutineID returns the number of the calling goroutine.
func goroutineID() string {
	var buf [64]byte
	fields := strings.Fields(string(buf[:runtime.Stack(buf[:], false)]))
	return fields[1]
}

// lookaheadSource returns a node source for wrapSource which records how many
// of its nodes were decoded on another goroutine than the one given.
func lookaheadSource(nodes []*data.Node, visitor string, elsewhere *atomic.Int64) func() (func() (data.NodeOrError, bool), func()) {
	return func() (func() (data.NodeOrError, bool), func()) {
		i := 0
		return func() (data.NodeOrError, bool) {
			if i == len(nodes) {
				return data.NodeOrError{}, false
			}
			if goroutineID() != visitor {
				elsewhere.Add(1)
			}
			i++
			return data.NodeOrError{Node: nodes[i-1]}, true
		}, func() {}
	}
}

// A run of directories, and an empty tree, must not go through the helper of
// the first pass: every directory would be one request and one reply, with
// nothing prepared ahead. Files after the first one still are prepared ahead.
func TestNodeLookaheadDecodesDirectoryRunsInline(t *testing.T) {
	dir := func(i int) *data.Node { return &data.Node{Name: fmt.Sprintf("d%03d", i), Type: data.NodeTypeDir} }
	file := func(i int) *data.Node { return &data.Node{Name: fmt.Sprintf("f%03d", i), Type: data.NodeTypeFile} }
	var dirs, files, mixed []*data.Node
	for i := 0; i < 300; i++ {
		dirs = append(dirs, dir(i))
		files = append(files, file(i))
	}
	for i := 0; i < 50; i++ {
		mixed = append(mixed, dir(i))
	}
	for i := 0; i < 200; i++ {
		mixed = append(mixed, file(i))
	}
	for i := 50; i < 100; i++ {
		mixed = append(mixed, dir(i))
	}

	for _, test := range []struct {
		name  string
		nodes []*data.Node
		// the nodes that may be decoded by the helper at most, and at least
		most, least int64
	}{
		{"empty", nil, 0, 0},
		{"directories", dirs, 0, 0},
		{"files", files, 299, 290},
		// 50 directories inline, the first file inline, 199 files and the
		// directory that ends their run by the helper, 49 directories inline
		{"mixed", mixed, 200, 190},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := newRestoreNodeLookahead(nil)
			defer p.Close()
			var elsewhere atomic.Int64
			var got []*data.Node
			for item := range p.wrapSource(lookaheadSource(test.nodes, goroutineID(), &elsewhere)) {
				if item.Error != nil {
					t.Fatal(item.Error)
				}
				got = append(got, item.Node)
			}
			if len(got) != len(test.nodes) {
				t.Fatalf("%d nodes, want %d", len(got), len(test.nodes))
			}
			for i := range got {
				if got[i] != test.nodes[i] {
					t.Fatalf("node %d is %q, want %q", i, got[i].Name, test.nodes[i].Name)
				}
			}
			if n := elsewhere.Load(); n > test.most || n < test.least {
				t.Fatalf("%d nodes were decoded by the helper, want %d to %d", n, test.least, test.most)
			}
			if len(p.credits) != 0 {
				t.Fatalf("%d credits are held after the traversal", len(p.credits))
			}
		})
	}
}

// Nested traversals of random trees through the lookahead deliver the nodes
// of a plain traversal, in its order, whatever the mix of files and
// directories.
func TestNodeLookaheadRandomTreesKeepOrder(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261008))
	type tree struct {
		nodes    []*data.Node
		children map[*data.Node]*tree
	}
	var build func(depth int) *tree
	build = func(depth int) *tree {
		tr := &tree{children: map[*data.Node]*tree{}}
		for i, n := 0, rnd.Intn(12); i < n; i++ {
			node := &data.Node{Name: fmt.Sprintf("n%d-%d", depth, i), Type: data.NodeTypeFile}
			if rnd.Intn(3) != 0 && depth < 4 {
				node.Type = data.NodeTypeDir
				tr.children[node] = build(depth + 1)
			}
			tr.nodes = append(tr.nodes, node)
		}
		return tr
	}
	for round := 0; round < 200; round++ {
		root := build(0)
		var want []string
		var plain func(tr *tree, prefix string)
		plain = func(tr *tree, prefix string) {
			for _, node := range tr.nodes {
				want = append(want, prefix+node.Name)
				if child := tr.children[node]; child != nil {
					plain(child, prefix+node.Name+"/")
				}
			}
		}
		plain(root, "")

		p := newRestoreNodeLookahead(nil)
		var got []string
		var walk func(tr *tree, prefix string)
		walk = func(tr *tree, prefix string) {
			var elsewhere atomic.Int64
			for item := range p.wrapSource(lookaheadSource(tr.nodes, "", &elsewhere)) {
				if item.Error != nil {
					t.Fatal(item.Error)
				}
				got = append(got, prefix+item.Node.Name)
				if child := tr.children[item.Node]; child != nil {
					walk(child, prefix+item.Node.Name+"/")
				}
			}
		}
		walk(root, "")
		p.Close()
		if strings.Join(got, "\n") != strings.Join(want, "\n") {
			t.Fatalf("round %d: the traversal through the lookahead differs from the plain one", round)
		}
	}
}

// countingTreeLoader serves small trees and counts how many were loaded. With
// gates, the load of tree i waits for gates[i].
type countingTreeLoader struct {
	ids    []restic.ID
	index  map[restic.ID]int
	loaded atomic.Int64
	gates  []chan struct{}
	size   int
}

func newCountingTreeLoader(n, nodeNameBytes int) *countingTreeLoader {
	l := &countingTreeLoader{index: map[restic.ID]int{}, size: nodeNameBytes}
	for i := 0; i < n; i++ {
		id := restic.Hash([]byte(fmt.Sprintf("tree %d", i)))
		l.ids = append(l.ids, id)
		l.index[id] = i
	}
	return l
}

func (l *countingTreeLoader) load(ctx context.Context, id restic.ID) (data.TreeNodeIterator, error) {
	i := l.index[id]
	if l.gates != nil {
		select {
		case <-l.gates[i]:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	l.loaded.Add(1)
	return restoreTreeSequence([]*data.Node{{Name: fmt.Sprintf("%06d-%s", i, strings.Repeat("x", l.size)), Type: data.NodeTypeDir}}, nil), nil
}

func (l *countingTreeLoader) settled(t *testing.T) int64 {
	t.Helper()
	last, same := int64(-1), 0
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		now := l.loaded.Load()
		if now == last {
			same++
			if same == 40 {
				return now
			}
		} else {
			last, same = now, 0
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the prefetch does not settle")
	return 0
}

// The trees of the second pass are handed over in batches: with a traversal
// that is slower than the prefetch, one hand-over carries many small trees,
// and what is prepared ahead stays bounded in trees and in bytes.
func TestTreePrefetchHandsOverBatches(t *testing.T) {
	l := newCountingTreeLoader(1000, 8)
	p := newRestoreTreePrefetch(context.Background(), l.ids, l.load)
	defer p.Close()

	// nobody asks: the first tree goes to the queue alone, a full batch waits
	if got, want := l.settled(t), int64(1+maxPreparedTreeBatch); got != want {
		t.Fatalf("%d trees were prepared ahead of an idle traversal, want %d", got, want)
	}
	next := 0
	loadOne := func() {
		t.Helper()
		tree, err := p.Load(context.Background(), l.ids[next])
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for item := range tree {
			if item.Error != nil || !strings.HasPrefix(item.Node.Name, fmt.Sprintf("%06d-", next)) {
				t.Fatalf("tree %d: got %+v", next, item)
			}
			count++
		}
		if count != 1 {
			t.Fatalf("tree %d: %d nodes", next, count)
		}
		next++
	}
	var sizes []int
	for next < len(l.ids) {
		handover := len(p.batch) == 0
		if handover {
			// the prefetch runs ahead as far as it may: one batch in the
			// queue and one that waits for the queue
			l.settled(t)
			if ahead := l.loaded.Load() - int64(next); ahead > 2*maxPreparedTreeBatch {
				t.Fatalf("tree %d: %d trees are prepared ahead, more than two batches", next, ahead)
			}
		}
		loadOne()
		if handover {
			sizes = append(sizes, len(p.batch)+1)
		}
	}
	// the first tree was handed over alone, the last hand-over carries the rest
	if len(sizes) < 3 || sizes[0] != 1 {
		t.Fatalf("hand-overs of %v trees", sizes)
	}
	for _, size := range sizes[1 : len(sizes)-1] {
		if size != maxPreparedTreeBatch {
			t.Fatalf("hand-overs of %v trees, want full batches of %d for a slow traversal", sizes, maxPreparedTreeBatch)
		}
	}
	if p.fallback {
		t.Fatal("the traversal fell back to loading the trees itself")
	}
}

// A batch never holds more than the byte budget of prepared nodes.
func TestTreePrefetchBatchKeepsByteBudget(t *testing.T) {
	// two trees fit the budget, three do not
	l := newCountingTreeLoader(40, maxPreparedTreeBytes/5)
	one := estimatePreparedNodeBytes(&data.Node{Name: fmt.Sprintf("%06d-%s", 0, strings.Repeat("x", l.size)), Type: data.NodeTypeDir})
	if 3*one <= maxPreparedTreeBytes || 2*one > maxPreparedTreeBytes {
		t.Fatalf("fixture: one tree is estimated at %d bytes", one)
	}
	p := newRestoreTreePrefetch(context.Background(), l.ids, l.load)
	defer p.Close()
	// one tree in the queue, two in the batch, and the one that did not fit
	if got := l.settled(t); got != 4 {
		t.Fatalf("%d trees were prepared ahead of an idle traversal, want 4", got)
	}
	for i, id := range l.ids {
		tree, err := p.Load(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		var held uint64
		for _, prepared := range p.batch {
			held += prepared.bytes
		}
		if held > maxPreparedTreeBytes {
			t.Fatalf("tree %d: the batch in use holds %d bytes of nodes", i, held)
		}
		for item := range tree {
			if item.Error != nil {
				t.Fatal(item.Error)
			}
		}
	}
}

// A traversal that waits is served tree by tree: it never waits for a batch
// to fill.
func TestTreePrefetchDoesNotDelayAWaitingTraversal(t *testing.T) {
	l := newCountingTreeLoader(20, 8)
	for range l.ids {
		l.gates = append(l.gates, make(chan struct{}))
	}
	p := newRestoreTreePrefetch(context.Background(), l.ids, l.load)
	defer p.Close()
	var wg sync.WaitGroup
	for i, id := range l.ids {
		done := make(chan error, 1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			tree, err := p.Load(context.Background(), id)
			if err == nil {
				for range tree {
				}
			}
			done <- err
		}()
		// only tree i becomes available; the later ones stay closed
		close(l.gates[i])
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("tree %d was not handed over although it was prepared and the traversal was waiting", i)
		}
		wg.Wait()
	}
}
