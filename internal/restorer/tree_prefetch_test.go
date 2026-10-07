package restorer

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

func restoreTreeSequence(nodes []*data.Node, iteratorErr error) data.TreeNodeIterator {
	return func(yield func(data.NodeOrError) bool) {
		for _, node := range nodes {
			if !yield(data.NodeOrError{Node: node}) {
				return
			}
		}
		if iteratorErr != nil {
			yield(data.NodeOrError{Error: iteratorErr})
		}
	}
}

func TestRestoreTreePrefetchPreservesNodeOrderAndDecodeError(t *testing.T) {
	firstID := restic.NewRandomID()
	secondID := restic.NewRandomID()
	decodeErr := errors.New("decode failed")
	loader := func(_ context.Context, id restic.ID) (data.TreeNodeIterator, error) {
		if id.Equal(firstID) {
			return restoreTreeSequence([]*data.Node{{Name: "first"}, {Name: "second"}}, nil), nil
		}
		return restoreTreeSequence([]*data.Node{{Name: "third"}}, decodeErr), nil
	}

	prefetch := newRestoreTreePrefetch(context.Background(), []restic.ID{firstID, secondID}, loader)
	defer prefetch.Close()

	var names []string
	first, err := prefetch.Load(context.Background(), firstID)
	if err != nil {
		t.Fatal(err)
	}
	for item := range first {
		if item.Error != nil {
			t.Fatal(item.Error)
		}
		names = append(names, item.Node.Name)
	}
	if got, want := strings.Join(names, ","), "first,second"; got != want {
		t.Fatalf("first tree nodes = %q, want %q", got, want)
	}

	second, err := prefetch.Load(context.Background(), secondID)
	if err != nil {
		t.Fatal(err)
	}
	names = nil
	var gotErr error
	for item := range second {
		if item.Error != nil {
			gotErr = item.Error
			break
		}
		names = append(names, item.Node.Name)
	}
	if got, want := strings.Join(names, ","), "third"; got != want {
		t.Fatalf("second tree nodes before error = %q, want %q", got, want)
	}
	if gotErr != decodeErr {
		t.Fatalf("second tree error = %v, want %v", gotErr, decodeErr)
	}
}

func TestRestoreTreePrefetchFallsBackForOversizedTree(t *testing.T) {
	id := restic.NewRandomID()
	var calls atomic.Int32
	loader := func(_ context.Context, _ restic.ID) (data.TreeNodeIterator, error) {
		if calls.Add(1) == 1 {
			return restoreTreeSequence([]*data.Node{{Name: strings.Repeat("x", maxPreparedTreeBytes)}}, nil), nil
		}
		return restoreTreeSequence([]*data.Node{{Name: "fallback"}}, nil), nil
	}

	prefetch := newRestoreTreePrefetch(context.Background(), []restic.ID{id}, loader)
	defer prefetch.Close()

	tree, err := prefetch.Load(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for item := range tree {
		if item.Error != nil {
			t.Fatal(item.Error)
		}
		names = append(names, item.Node.Name)
	}
	if got, want := strings.Join(names, ","), "fallback"; got != want {
		t.Fatalf("oversized tree nodes = %q, want %q", got, want)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("loader calls = %d, want prefetch plus fallback", got)
	}
}

func TestRestoreTreePrefetchFallsBackWhenTraversalOrderChanges(t *testing.T) {
	firstID := restic.NewRandomID()
	secondID := restic.NewRandomID()
	loader := func(_ context.Context, id restic.ID) (data.TreeNodeIterator, error) {
		return restoreTreeSequence([]*data.Node{{Name: id.Str()}}, nil), nil
	}

	prefetch := newRestoreTreePrefetch(context.Background(), []restic.ID{firstID}, loader)
	defer prefetch.Close()

	tree, err := prefetch.Load(context.Background(), secondID)
	if err != nil {
		t.Fatal(err)
	}
	for item := range tree {
		if item.Error != nil {
			t.Fatal(item.Error)
		}
		if item.Node.Name != secondID.Str() {
			t.Fatalf("loaded node = %q, want requested tree %q", item.Node.Name, secondID.Str())
		}
	}
}

func TestRestoreTreePrefetchHonorsCanceledContextWithQueuedEmptyTree(t *testing.T) {
	for i := 0; i < 32; i++ {
		id := restic.NewRandomID()
		loader := func(_ context.Context, _ restic.ID) (data.TreeNodeIterator, error) {
			return restoreTreeSequence(nil, nil), nil
		}
		prefetch := newRestoreTreePrefetch(context.Background(), []restic.ID{id}, loader)

		// Both the canceled context and the ready queue would be selectable.
		// Wait until preparation is complete, then require Load to honor ctx.
		<-prefetch.done
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		tree, err := prefetch.Load(ctx, id)
		prefetch.Close()
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Load error = %v, want context canceled", err)
		}
		if tree != nil {
			t.Fatal("Load returned a prepared tree for a canceled context")
		}
	}
}
