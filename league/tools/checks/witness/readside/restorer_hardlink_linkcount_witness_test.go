package restorer

// WITNESS (readside review): internal/restorer/restorer.go:683-776 (first
// pass keeps only a "seen" set) and :829-871 (second pass rebuilds the
// hard-link index and asks node.Links > 1 before looking a node up).
//
// Stock builds the hard-link index in the first pass from nodes with
// Links > 1 and, in the second pass, links EVERY regular file whose
// (inode, device) is in that index and whose path differs from the indexed
// one, whatever that node's own link count says. The candidate only looks up
// nodes that themselves carry Links > 1.
//
// The two agree on every consistent snapshot. They differ when one inode
// appears with a link count of 1 on one path and more than 1 on another,
// which a backup records when a hard link is added or removed while it runs
// (for example an rsync --link-dest rotation or a package manager): stock
// restores the two paths as one hard-linked file, the candidate restores two
// independent files (same bytes when the contents agree, twice the space, no
// link).
//
// The file uses only helpers present in the stock test suite, so the same file
// runs in both trees.
// OS: any filesystem with hard links (NTFS, ext4, APFS ...). Run:
//   go test -run TestWitnessHardlinkMixedLinkCount -count=1 -v ./internal/restorer
// Expected: PASS on stock, FAIL on the candidate (two mixed cases).

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

func TestWitnessHardlinkMixedLinkCount(t *testing.T) {
	for _, tc := range []struct {
		name          string
		first, second uint64
	}{
		{"control: both paths record 2 links", 2, 2},
		{"first path records 1 link, second 2", 1, 2},
		{"first path records 2 links, second 1", 2, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			snapshot := Snapshot{
				Nodes: map[string]Node{
					"a": File{Data: "same content\n", Links: tc.first, Inode: 42},
					"b": File{Data: "same content\n", Links: tc.second, Inode: 42},
				},
			}

			repo := repository.TestRepository(t)
			tempdir := filepath.Join(rtest.TempDir(t), "target")
			sn, _ := saveSnapshot(t, repo, snapshot, noopGetGenericAttributes)

			res := NewRestorer(repo, sn, Options{})
			_, err := res.RestoreTo(t.Context(), tempdir)
			rtest.OK(t, err)

			fa, err := os.Stat(filepath.Join(tempdir, "a"))
			rtest.OK(t, err)
			fb, err := os.Stat(filepath.Join(tempdir, "b"))
			rtest.OK(t, err)
			linked := os.SameFile(fa, fb)
			t.Logf("a and b restored as one hard-linked file: %v", linked)
			if !linked {
				t.Errorf("DIVERGENCE from stock: a and b are two independent files (stock restores them as hard links)")
			}
		})
	}
}
