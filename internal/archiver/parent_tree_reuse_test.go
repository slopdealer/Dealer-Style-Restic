package archiver

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

type parentIDCaptureSaver struct {
	id      restic.ID
	payload []byte
	known   bool
	err     error
}

func (s *parentIDCaptureSaver) SaveBlobAsync(_ context.Context, _ restic.BlobType, buf []byte, id restic.ID, duplicate bool, cb func(restic.ID, bool, int, error)) {
	s.id = id
	s.payload = append([]byte(nil), buf...)
	cb(restic.Hash(buf), s.known, len(buf), s.err)
}

func encodeReuseTree(t *testing.T, nodes ...*data.Node) []byte {
	t.Helper()
	b := data.NewTreeJSONBuilder()
	for _, n := range nodes {
		if err := b.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	buf, err := b.Finalize()
	if err != nil {
		t.Fatal(err)
	}
	return buf
}

func TestParentTreeByteReuse(t *testing.T) {
	original := &data.Node{Name: "file", Type: data.NodeTypeFile, ModTime: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC), ExtendedAttributes: []data.ExtendedAttribute{{Name: "a", Value: []byte("1")}, {Name: "b", Value: []byte("2")}}}
	canonical := encodeReuseTree(t, original)
	changedOffset := *original
	changedOffset.ModTime = original.ModTime.In(time.FixedZone("other", 3600))
	changedOrder := *original
	changedOrder.ExtendedAttributes = []data.ExtendedAttribute{original.ExtendedAttributes[1], original.ExtendedAttributes[0]}
	emptyContent := *original
	emptyContent.Content = restic.IDs{}
	for _, tc := range []struct {
		name   string
		node   *data.Node
		parent []byte
		reuse  bool
		known  bool
	}{
		{"identical-known", original, canonical, true, true},
		{"identical-not-indexed", original, canonical, true, false},
		{"timestamp-offset", &changedOffset, canonical, false, true},
		{"xattr-order", &changedOrder, canonical, false, true},
		{"nil-versus-empty-content", &emptyContent, canonical, false, true},
		{"noncanonical-parent", original, append([]byte(" "), canonical...), false, true},
		{"omitted-child", nil, canonical, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := new(parentTreeBudget)
			parent := budget.retain(restic.Hash(tc.parent), tc.parent)
			saver := &parentIDCaptureSaver{known: tc.known}
			ts := &treeSaver{uploader: saver, errFn: func(_ string, err error) error { return err }}
			var children []futureNode
			if tc.node != nil {
				children = append(children, newFutureNodeWithResult(futureNodeResult{node: tc.node}))
			}
			node, stats, err := ts.save(context.Background(), &saveTreeJob{node: &data.Node{}, nodes: children, parent: parent})
			if err != nil {
				t.Fatal(err)
			}
			if tc.reuse {
				if saver.id != restic.Hash(tc.parent) {
					t.Fatal("parent ID not supplied")
				}
			} else if !saver.id.IsNull() {
				t.Fatal("nonidentical bytes reused")
			}
			if *node.Subtree != restic.Hash(saver.payload) {
				t.Fatal("subtree ID differs from emitted bytes")
			}
			if tc.known && stats.TreeBlobs != 0 {
				t.Fatal("known tree counted as new")
			}
			if !tc.known && (stats.TreeBlobs != 1 || stats.TreeSize != uint64(len(saver.payload))) {
				t.Fatal("new tree accounting lost")
			}
			if budget.used.Load() != 0 {
				t.Fatal("retention not released")
			}
		})
	}
}

func TestParentTreeReuseFailureAndBudget(t *testing.T) {
	budget := new(parentTreeBudget)
	full := make([]byte, parentTreeRetentionMax)
	parent := budget.retain(restic.Hash(full), full)
	if parent == nil || budget.retain(restic.ID{}, []byte{1}) != nil {
		t.Fatal("budget not enforced")
	}
	parent.release()
	oversized := make([]byte, 1, parentTreeRetentionMax+1)
	if budget.retain(restic.ID{}, oversized) != nil {
		t.Fatal("capacity not charged")
	}
	expected := errors.New("save failed")
	payload := encodeReuseTree(t)
	parent = budget.retain(restic.Hash(payload), payload)
	ts := &treeSaver{uploader: &parentIDCaptureSaver{err: expected}, errFn: func(_ string, err error) error { return err }}
	_, _, err := ts.save(context.Background(), &saveTreeJob{node: &data.Node{}, parent: parent})
	if err != expected || budget.used.Load() != 0 {
		t.Fatal("save error or retention changed")
	}
	parent = budget.retain(restic.Hash(payload), payload)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ts.ch = make(chan saveTreeJob)
	ts.saveWithParent(ctx, "/", "/", &data.Node{}, nil, nil, parent)
	if budget.used.Load() != 0 {
		t.Fatal("cancelled dispatch leaked retention")
	}
}

func TestParentTreeReuseOrderedErrorSettlement(t *testing.T) {
	budget := new(parentTreeBudget)
	payload := encodeReuseTree(t)
	parent := budget.retain(restic.Hash(payload), payload)
	first, second := errors.New("first"), errors.New("second")
	var seen []error
	saver := &parentIDCaptureSaver{}
	ts := &treeSaver{uploader: saver, errFn: func(_ string, err error) error { seen = append(seen, err); return err }}
	_, _, err := ts.save(context.Background(), &saveTreeJob{node: &data.Node{}, parent: parent, nodes: []futureNode{
		newFutureNodeWithResult(futureNodeResult{err: first}),
		newFutureNodeWithResult(futureNodeResult{err: second}),
	}})
	if err != first || len(seen) != 1 || seen[0] != first {
		t.Fatal("error settlement changed")
	}
	if saver.payload != nil || budget.used.Load() != 0 {
		t.Fatal("failed tree saved or retention leaked")
	}
	parent = budget.retain(restic.Hash(payload), payload)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ch := make(chan futureNodeResult)
	_, _, err = ts.save(ctx, &saveTreeJob{node: &data.Node{}, parent: parent, nodes: []futureNode{{ch: ch}}})
	if err != context.Canceled || budget.used.Load() != 0 {
		t.Fatal("child wait cancellation leaked retention")
	}
}

// Exercise completion through the worker, after the same child settlement that
// backup uses. Reuse must neither skip the completion callback nor count a known
// tree as newly stored when exclusions and ignored errors omit children.
func TestParentTreeReuseWorkerCompletion(t *testing.T) {
	for _, known := range []bool{false, true} {
		for _, reuse := range []bool{false, true} {
			n := &data.Node{Name: "a", Type: data.NodeTypeFile}
			payload := encodeReuseTree(t, n)
			budget := new(parentTreeBudget)
			var parent *parentTreeBytes
			if reuse {
				parent = budget.retain(restic.Hash(payload), payload)
			}
			ignored := errors.New("ignored child error")
			var warnings []error
			saver := &parentIDCaptureSaver{known: known}
			ts := &treeSaver{uploader: saver, errFn: func(_ string, err error) error {
				warnings = append(warnings, err)
				return nil
			}}
			future, result := newFutureNode()
			jobs := make(chan saveTreeJob, 1)
			completions := 0
			var completed ItemStats
			jobs <- saveTreeJob{node: &data.Node{}, parent: parent, ch: result,
				complete: func(node *data.Node, stats ItemStats) {
					completions++
					completed = stats
					if node.Subtree == nil || *node.Subtree != restic.Hash(payload) {
						t.Fatal("completion saw a different subtree")
					}
				}, nodes: []futureNode{
					newFutureNodeWithResult(futureNodeResult{node: n}),
					newFutureNodeWithResult(futureNodeResult{node: n}),
					newFutureNodeWithResult(futureNodeResult{}),
					newFutureNodeWithResult(futureNodeResult{err: ignored}),
				}}
			close(jobs)
			if err := ts.worker(context.Background(), jobs); err != nil {
				t.Fatal(err)
			}
			got := future.take(context.Background())
			if got.err != nil || completions != 1 || got.stats != completed {
				t.Fatal("completion or future accounting changed")
			}
			if len(warnings) != 2 || !errors.Is(warnings[0], data.ErrTreeNotOrdered) || warnings[1] != ignored {
				t.Fatal("duplicate warning or ignored error order changed")
			}
			if string(saver.payload) != string(payload) || budget.used.Load() != 0 {
				t.Fatal("emitted bytes or ownership changed")
			}
			if known && got.stats.TreeBlobs != 0 || !known && (got.stats.TreeBlobs != 1 || got.stats.TreeSize != uint64(len(payload))) {
				t.Fatal("known/new tree statistics changed")
			}
		}
	}
}

type pendingParentSave struct {
	payload  []byte
	complete func(restic.ID, bool, int, error)
}
type pendingParentSaver struct{ entered chan pendingParentSave }

func (s *pendingParentSaver) SaveBlobAsync(_ context.Context, _ restic.BlobType, buf []byte, _ restic.ID, _ bool, cb func(restic.ID, bool, int, error)) {
	s.entered <- pendingParentSave{buf, cb}
}

func TestParentTreeRetentionThroughCanceledSave(t *testing.T) {
	for _, trailing := range []bool{false, true} {
		t.Run(map[bool]string{false: "equal", true: "reference-prefix"}[trailing], func(t *testing.T) {
			n := &data.Node{Name: "a", Type: data.NodeTypeFile}
			expected := encodeReuseTree(t, n)
			payload := append([]byte(nil), expected...)
			if trailing {
				payload = append(payload, ' ')
			}
			budget := new(parentTreeBudget)
			parent := budget.retain(restic.Hash(payload), payload)
			charge := budget.used.Load()
			saver := &pendingParentSaver{make(chan pendingParentSave, 1)}
			ts := &treeSaver{uploader: saver, errFn: func(_ string, err error) error { return err }}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				_, _, err := ts.save(ctx, &saveTreeJob{node: &data.Node{}, parent: parent, nodes: []futureNode{newFutureNodeWithResult(futureNodeResult{node: n})}})
				done <- err
			}()
			var pending pendingParentSave
			select {
			case pending = <-saver.entered:
			case <-time.After(5 * time.Second):
				t.Fatal("save did not start")
			}
			cancel()
			select {
			case err := <-done:
				if err != context.Canceled {
					t.Fatalf("save: %v", err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("save did not cancel")
			}
			if budget.used.Load() != charge {
				t.Fatal("in-flight bytes lost retention charge")
			}
			if &pending.payload[0] != &payload[0] {
				t.Fatal("test did not exercise reference-backed output")
			}
			if string(pending.payload) != string(expected) {
				t.Fatal("in-flight bytes changed")
			}
			pending.complete(restic.Hash(pending.payload), false, len(pending.payload), nil)
			if budget.used.Load() != 0 {
				t.Fatal("completion did not release retention")
			}
		})
	}
}
