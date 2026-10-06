package restorer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

// Hard links are restored from the index of the first pass: the first path of
// every inode that some node reports with more than one link. In the second
// pass every regular file with that inode is linked to this path, whatever
// link count the node itself reports. One inode recorded with one link on one
// path and with more on another is what a backup stores when a link is added
// or removed while it runs.
//
// The file uses only helpers of the stock test suite; every expectation below
// is what stock restores.
func TestRestoreHardlinksWithMixedLinkCounts(t *testing.T) {
	type expect struct {
		// groups of paths that must be one file; all other pairs must differ
		linked [][]string
		// content of each path after the restore
		content map[string]string
	}
	for _, tc := range []struct {
		name     string
		snapshot Snapshot
		expect   expect
	}{
		{
			name: "both paths record two links",
			snapshot: Snapshot{Nodes: map[string]Node{
				"a": File{Data: "content a\n", Links: 2, Inode: 42},
				"b": File{Data: "content a\n", Links: 2, Inode: 42},
			}},
			expect: expect{
				linked:  [][]string{{"a", "b"}},
				content: map[string]string{"a": "content a\n", "b": "content a\n"},
			},
		},
		{
			name: "first path records one link, second two",
			snapshot: Snapshot{Nodes: map[string]Node{
				"a": File{Data: "same\n", Links: 1, Inode: 42},
				"b": File{Data: "same\n", Links: 2, Inode: 42},
			}},
			expect: expect{
				linked:  [][]string{{"a", "b"}},
				content: map[string]string{"a": "same\n", "b": "same\n"},
			},
		},
		{
			name: "first path records two links, second one",
			snapshot: Snapshot{Nodes: map[string]Node{
				"a": File{Data: "same\n", Links: 2, Inode: 42},
				"b": File{Data: "same\n", Links: 1, Inode: 42},
			}},
			expect: expect{
				linked:  [][]string{{"a", "b"}},
				content: map[string]string{"a": "same\n", "b": "same\n"},
			},
		},
		{
			// the indexed path is b: a and c are linked to it and get its content
			name: "three paths, only the middle one records two links, contents differ",
			snapshot: Snapshot{Nodes: map[string]Node{
				"a": File{Data: "content a\n", Links: 1, Inode: 42},
				"b": File{Data: "content b\n", Links: 2, Inode: 42},
				"c": File{Data: "content c\n", Links: 1, Inode: 42},
			}},
			expect: expect{
				linked:  [][]string{{"a", "b", "c"}},
				content: map[string]string{"a": "content b\n", "b": "content b\n", "c": "content b\n"},
			},
		},
		{
			name: "paths in different directories, the later directory records the links",
			snapshot: Snapshot{Nodes: map[string]Node{
				"dir1": Dir{Nodes: map[string]Node{
					"a": File{Data: "content a\n", Links: 1, Inode: 42},
				}},
				"dir2": Dir{Nodes: map[string]Node{
					"b": File{Data: "content b\n", Links: 3, Inode: 42},
					"c": File{Data: "content b\n", Links: 3, Inode: 42},
				}},
			}},
			expect: expect{
				linked: [][]string{{"dir1/a", "dir2/b", "dir2/c"}},
				content: map[string]string{
					"dir1/a": "content b\n", "dir2/b": "content b\n", "dir2/c": "content b\n",
				},
			},
		},
		{
			// no node reports more than one link: nothing is indexed
			name: "same inode, every path records one link",
			snapshot: Snapshot{Nodes: map[string]Node{
				"a": File{Data: "content a\n", Links: 1, Inode: 42},
				"b": File{Data: "content b\n", Links: 1, Inode: 42},
			}},
			expect: expect{
				content: map[string]string{"a": "content a\n", "b": "content b\n"},
			},
		},
		{
			name: "two inodes with links and one file without",
			snapshot: Snapshot{Nodes: map[string]Node{
				"a": File{Data: "one\n", Links: 2, Inode: 42},
				"b": File{Data: "two\n", Links: 2, Inode: 43},
				"c": File{Data: "one\n", Links: 2, Inode: 42},
				"d": File{Data: "two\n", Links: 1, Inode: 43},
				"e": File{Data: "three\n", Links: 1, Inode: 44},
			}},
			expect: expect{
				linked:  [][]string{{"a", "c"}, {"b", "d"}},
				content: map[string]string{"a": "one\n", "b": "two\n", "c": "one\n", "d": "two\n", "e": "three\n"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := repository.TestRepository(t)
			tempdir := filepath.Join(rtest.TempDir(t), "target")
			sn, _ := saveSnapshot(t, repo, tc.snapshot, noopGetGenericAttributes)

			res := NewRestorer(repo, sn, Options{})
			_, err := res.RestoreTo(t.Context(), tempdir)
			rtest.OK(t, err)

			group := make(map[string]int)
			for i, paths := range tc.expect.linked {
				for _, path := range paths {
					group[path] = i + 1
				}
			}
			infos := make(map[string]os.FileInfo)
			for path, want := range tc.expect.content {
				target := filepath.Join(tempdir, filepath.FromSlash(path))
				got, err := os.ReadFile(target)
				rtest.OK(t, err)
				if string(got) != want {
					t.Errorf("%v: content %q, stock restores %q", path, got, want)
				}
				infos[path], err = os.Stat(target)
				rtest.OK(t, err)
			}
			for a := range infos {
				for b := range infos {
					if a >= b {
						continue
					}
					wantLinked := group[a] != 0 && group[a] == group[b]
					if linked := os.SameFile(infos[a], infos[b]); linked != wantLinked {
						t.Errorf("%v and %v restored as one hard-linked file: %v, stock: %v", a, b, linked, wantLinked)
					}
				}
			}
		})
	}
}
