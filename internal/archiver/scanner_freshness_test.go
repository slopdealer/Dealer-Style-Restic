package archiver

import (
	"os"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	rtest "github.com/restic/restic/internal/test"
)

// Scanner discovery and archival metadata are separate observations. A change
// after discovery must be archived without retroactively changing scan totals.
func TestArchiveObservesGrowthAfterScanner(t *testing.T) {
	tempdir, repo := prepareTempdirRepoSrc(t, TestDir{
		"testfile": TestFile{Content: "old"},
	})
	restoreDir := rtest.Chdir(t, tempdir)
	defer restoreDir()
	localFS := fs.NewLocal()
	parent, _ := snapshot(t, repo, localFS, nil, "testfile")

	var totals ScanStats
	sc := NewScanner(localFS)
	sc.Result = func(item string, stats ScanStats) {
		if item == "" {
			totals = stats
		}
	}
	rtest.OK(t, sc.Scan(t.Context(), []string{"testfile"}))
	rtest.Equals(t, uint64(3), totals.Bytes)

	contents := []byte("freshly grown contents")
	rtest.OK(t, os.WriteFile("testfile", contents, 0600))
	changed, node := snapshot(t, repo, localFS, parent, "testfile")
	rtest.Equals(t, uint64(len(contents)), node.Size)
	rtest.Equals(t, uint64(3), totals.Bytes)
	rtest.Assert(t, !changed.Tree.Equal(*parent.Tree), "changed content reused the old tree")

	// With no further source change, normal repeated archival must emit exactly
	// the same tree bytes and hence the same tree identity.
	repeated, _ := snapshot(t, repo, localFS, changed, "testfile")
	rtest.Assert(t, repeated.Tree.Equal(*changed.Tree), "unchanged archival changed tree identity")
}

// Selection that depends on metadata must use each walk's own observation.
func TestArchiveReevaluatesSizeExclusionAfterScanner(t *testing.T) {
	tempdir, repo := prepareTempdirRepoSrc(t, TestDir{
		"dir": TestDir{"file": TestFile{Content: "old"}},
	})
	restoreDir := rtest.Chdir(t, tempdir)
	defer restoreDir()
	localFS := fs.NewLocal()
	selectSmall := func(_ string, fi *fs.ExtendedFileInfo, _ fs.FS) bool {
		return !fi.Mode.IsRegular() || fi.Size <= 3
	}
	var totals ScanStats
	sc := NewScanner(localFS)
	sc.Select = selectSmall
	sc.Result = func(item string, stats ScanStats) {
		if item == "" {
			totals = stats
		}
	}
	rtest.OK(t, sc.Scan(t.Context(), []string{"dir"}))
	rtest.Equals(t, uint(1), totals.Files)
	rtest.Equals(t, uint64(3), totals.Bytes)
	rtest.OK(t, os.WriteFile("dir/file", []byte("grown beyond selection limit"), 0600))
	arch := New(repo, localFS, Options{})
	arch.Select = selectSmall
	sn, _, summary, err := arch.Snapshot(t.Context(), []string{"dir"}, SnapshotOptions{})
	rtest.OK(t, err)
	tree, err := data.LoadTree(t.Context(), repo, *sn.Tree)
	rtest.OK(t, err)
	finder := data.NewTreeFinder(tree)
	defer finder.Close()
	dir, err := finder.Find("dir")
	rtest.OK(t, err)
	rtest.Assert(t, dir != nil && dir.Subtree != nil, "directory missing from snapshot")
	children, err := data.LoadTree(t.Context(), repo, *dir.Subtree)
	rtest.OK(t, err)
	for child := range children {
		rtest.OK(t, child.Error)
		t.Fatalf("excluded file appears in snapshot: %+v", child.Node)
	}
	rtest.Equals(t, uint(0), summary.Files.New+summary.Files.Changed+summary.Files.Unchanged)
	rtest.Equals(t, uint(1), totals.Files)
	rtest.Equals(t, uint64(3), totals.Bytes)
}
