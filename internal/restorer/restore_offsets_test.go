package restorer

import (
	"context"
	"os"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestRestoreOffsetsIndependentAppend(t *testing.T) {
	a := appendRestoreOffset(nil, 0)
	b := appendRestoreOffset(nil, 0)
	if cap(a) != 1 || cap(b) != 1 {
		t.Fatal("shared first offsets must force allocation on append")
	}
	a = appendRestoreOffset(a, 4)
	b = appendRestoreOffset(b, 7)
	a[0] = 9
	if !reflect.DeepEqual(b, []int64{0, 7}) || zeroRestoreOffset[0] != 0 {
		t.Fatalf("append mutated another association: %v, %v", a, b)
	}
	c := appendRestoreOffset(nil, 12)
	c = appendRestoreOffset(c, 0)
	if !reflect.DeepEqual(c, []int64{12, 0}) {
		t.Fatalf("nonzero first offset changed: %v", c)
	}
}

func TestRestoreOffsetsRepeatedContent(t *testing.T) {
	repo := newTestRepo([]TestFile{
		{name: "single", blobs: []TestBlob{{data: "aa", pack: "p"}}},
		{name: "repeated", blobs: []TestBlob{{data: "aa", pack: "p"}, {data: "bb", pack: "q"}, {data: "aa", pack: "p"}}},
		{name: "nonzero", blobs: []TestBlob{{data: "bb", pack: "q"}, {data: "aa", pack: "p"}}},
	})
	r := newFileRestorer(t.TempDir(), repo.loader, repo.Lookup, 2, false, false, nil, nil, restic.ID{})
	r.files = repo.files
	if err := r.restoreFiles(context.Background()); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"single": "aa", "repeated": "aabbaa", "nonzero": "bbaa"} {
		got, err := os.ReadFile(r.targetPath(name))
		if err != nil || string(got) != want {
			t.Fatalf("%s: %q, %v", name, got, err)
		}
	}
}

var restoreOffsetAllocationSink []int64

func TestRestoreOffsetsAllocation(t *testing.T) {
	ordinary := testing.AllocsPerRun(100, func() {
		restoreOffsetAllocationSink = append([]int64(nil), 0)
	})
	reused := testing.AllocsPerRun(100, func() {
		restoreOffsetAllocationSink = appendRestoreOffset(nil, 0)
	})
	if ordinary != 1 || reused != 0 {
		t.Fatalf("first offset allocations: ordinary=%v reused=%v", ordinary, reused)
	}
	restoreOffsetAllocationSink = nil
}
