package restorer

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

// The restore must not keep a path for every directory it creates: the
// directories that this restore created itself (Restorer.freshDirs) are
// remembered only while the first traversal is inside them. Stock keeps
// nothing per directory.
//
// The test counts the entries of that structure. SelectFilter is called on
// the traversal's own goroutine for every node of both passes, so the largest
// number of entries seen there is exact, on every platform and under any
// load: with the entry removed when the traversal leaves a directory it is
// the depth of the tree; with a path kept per directory it is the number of
// directories. An earlier form of this test sampled the heap of the running
// restore and failed in about one run of seven for reasons of timing.
func TestRestoreKeepsNoPathPerDirectory(t *testing.T) {
	for _, parents := range []int{10, 40} {
		const children = 300
		// long names, as a path kept per directory would hold them
		childName := strings.Repeat("directory-name-", 6)
		nodes := map[string]Node{}
		for i := 0; i < parents; i++ {
			sub := map[string]Node{}
			for j := 0; j < children; j++ {
				sub[fmt.Sprintf("%s%05d", childName, j)] = Dir{Nodes: map[string]Node{}}
			}
			nodes[fmt.Sprintf("parent-%05d", i)] = Dir{Nodes: sub}
		}
		directories := parents * (children + 1)

		repo := repository.TestRepository(t)
		sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: nodes}, noopGetGenericAttributes)
		tempdir := filepath.Join(rtest.TempDir(t), "target")

		res := NewRestorer(repo, sn, Options{})
		calls, largest := 0, 0
		res.SelectFilter = func(string, bool) (bool, bool) {
			calls++
			largest = max(largest, len(res.freshDirs))
			return true, true
		}
		_, err := res.RestoreTo(t.Context(), tempdir)
		rtest.OK(t, err)

		// both passes ask about every directory
		if calls < 2*directories {
			t.Fatalf("%d directories: the filter was asked %d times, the test does not see the whole restore", directories, calls)
		}
		// the target itself, a parent and one child are open at the same time
		const depth = 3
		if largest > depth {
			t.Errorf("%d directories: up to %d created directories were remembered at once, the tree is %d deep",
				directories, largest, depth)
		}
		if largest == 0 {
			t.Errorf("%d directories: no created directory was ever remembered: the test does not see the structure", directories)
		}
		if left := len(res.freshDirs); left != 0 {
			t.Errorf("%d directories: %d created directories are still remembered after the restore", directories, left)
		}
	}
}
