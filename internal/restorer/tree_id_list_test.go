package restorer

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

func numberedTreeID(i int) (id restic.ID) {
	binary.LittleEndian.PutUint64(id[:], uint64(i)+1)
	return id
}

// The list never holds more IDs than its limit and never allocates room for
// more, whatever is added; what it holds is the first IDs, in order.
func TestRestoreTreeIDListNeverExceedsItsLimit(t *testing.T) {
	for _, limit := range []int{0, 1, 2, 63, 64, 65, 1000, maxPrefetchTreeIDs} {
		list := newRestoreTreeIDList(limit)
		adds := 3*limit + 1000
		for i := 0; i < adds; i++ {
			list.add(numberedTreeID(i))
			if len(list.ids) > limit || cap(list.ids) > limit {
				t.Fatalf("limit %d: after %d additions the list holds %d IDs with room for %d", limit, i+1, len(list.ids), cap(list.ids))
			}
		}
		if len(list.ids) != limit {
			t.Fatalf("limit %d: %d IDs kept after %d additions", limit, len(list.ids), adds)
		}
		for i, id := range list.ids {
			if id != numberedTreeID(i) {
				t.Fatalf("limit %d: position %d does not hold the ID that was added at that position", limit, i)
			}
		}
	}

	// two million directories against the production bound: 2 MiB, not 64
	list := newRestoreTreeIDList(maxPrefetchTreeIDs)
	for i := 0; i < 2_020_200; i++ {
		list.add(numberedTreeID(i))
	}
	if len(list.ids) != maxPrefetchTreeIDs || cap(list.ids) != maxPrefetchTreeIDs {
		t.Fatalf("2,020,200 additions: %d IDs kept with room for %d, the bound is %d", len(list.ids), cap(list.ids), maxPrefetchTreeIDs)
	}
	if bytes := cap(list.ids) * len(restic.ID{}); bytes != 2<<20 {
		t.Fatalf("the full list takes %d bytes, expected 2 MiB", bytes)
	}
}

// The restore must not keep a tree ID for every directory of the snapshot
// (release check F5): the list for the second-pass prefetch
// (Restorer.prefetchTrees) is bounded. Stock keeps nothing per directory.
//
// As in TestRestoreKeepsNoPathPerDirectory the structure is counted from
// SelectFilter, which runs on the traversal's own goroutine for every node of
// both passes, so the largest size seen is exact. The bound is lowered to 100
// so that a snapshot of 12,041 trees is 120 times the bound; with the
// production bound the same structure holds every tree of the snapshot, which
// shows that the test sees it and that an ordinary snapshot is recorded whole.
func TestRestoreKeepsBoundedTreeIDList(t *testing.T) {
	const parents, children = 40, 300
	nodes := map[string]Node{
		// the prefetch that reads the list starts only when files are restored
		"file": File{Data: "content"},
	}
	for i := 0; i < parents; i++ {
		sub := map[string]Node{}
		for j := 0; j < children; j++ {
			sub[fmt.Sprintf("child-%05d", j)] = Dir{Nodes: map[string]Node{}}
		}
		nodes[fmt.Sprintf("parent-%05d", i)] = Dir{Nodes: sub}
	}
	trees := 1 + parents*(children+1)

	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: nodes}, noopGetGenericAttributes)

	for _, tc := range []struct {
		limit       int // 0: the production bound
		wantLargest int
	}{
		{limit: 100, wantLargest: 100},
		{limit: 0, wantLargest: trees},
	} {
		res := NewRestorer(repo, sn, Options{})
		res.prefetchTreeLimit = tc.limit
		bound := tc.limit
		if bound == 0 {
			bound = maxPrefetchTreeIDs
		}
		calls, largest, largestCap := 0, 0, 0
		res.SelectFilter = func(string, bool) (bool, bool) {
			calls++
			largest = max(largest, len(res.prefetchTrees.ids))
			largestCap = max(largestCap, cap(res.prefetchTrees.ids))
			return true, true
		}
		_, err := res.RestoreTo(t.Context(), filepath.Join(rtest.TempDir(t), "target"))
		rtest.OK(t, err)

		if calls < 2*(trees-1) {
			t.Fatalf("limit %d: the filter was asked %d times for %d directories, the test does not see the whole restore", tc.limit, calls, trees-1)
		}
		if largest > bound || largestCap > bound {
			t.Errorf("limit %d: the restore of %d trees held up to %d tree IDs with room for %d; the bound is %d",
				tc.limit, trees, largest, largestCap, bound)
		}
		if largest != tc.wantLargest {
			t.Errorf("limit %d: the list held at most %d tree IDs during the restore of %d trees, expected %d",
				tc.limit, largest, trees, tc.wantLargest)
		}
		if left := len(res.prefetchTrees.ids) + cap(res.prefetchTrees.ids); left != 0 {
			t.Errorf("limit %d: the list is still held after the restore", tc.limit)
		}
	}
}

// treeFaultRepository fails or damages the SECOND and every later load of
// chosen trees. The first pass of a restore loads every tree once, so the
// faults hit the second pass only, and they depend on the tree alone, not on
// when or from which goroutine it is loaded.
type treeFaultRepository struct {
	restic.Repository
	mu      sync.Mutex
	loads   map[restic.ID]int
	fail    map[restic.ID]error
	damaged map[restic.ID][]byte
}

func (r *treeFaultRepository) LoadBlob(ctx context.Context, h restic.BlobHandle, buf []byte) ([]byte, error) {
	if h.Type == restic.TreeBlob {
		r.mu.Lock()
		r.loads[h.ID]++
		n := r.loads[h.ID]
		r.mu.Unlock()
		if n >= 2 {
			if err, ok := r.fail[h.ID]; ok {
				return nil, err
			}
			if payload, ok := r.damaged[h.ID]; ok {
				return append([]byte(nil), payload...), nil
			}
		}
	}
	return r.Repository.LoadBlob(ctx, h, buf)
}

type preorderTree struct {
	id       restic.ID
	location string
}

// snapshotPreorder lists the trees of a snapshot in the order in which a
// restore visits them.
func snapshotPreorder(t *testing.T, repo restic.Repository, id restic.ID, location string, out *[]preorderTree) {
	*out = append(*out, preorderTree{id: id, location: location})
	tree, err := data.LoadTree(context.Background(), repo, id)
	rtest.OK(t, err)
	for item := range tree {
		rtest.OK(t, item.Error)
		if item.Node.Type == data.NodeTypeDir {
			snapshotPreorder(t, repo, *item.Node.Subtree, location+"/"+item.Node.Name, out)
		}
	}
}

type restoredEntry struct {
	Kind         string
	Perm         os.FileMode
	Size         int64
	Content      string
	SnapshotTime bool // the modification time is the snapshot's, not the time of the restore
	LinkedTo     string
}

type restoreOutcome struct {
	Reported []string // what Restorer.Error was called with, in order
	Returned string
	Files    uint64
	Entries  map[string]restoredEntry
}

// restoreAndDescribe restores sn with the given bound on the tree-ID list and
// describes everything a caller can observe: the errors reported and their
// order, the returned error and file count, and every entry of the target.
func restoreAndDescribe(t *testing.T, repo restic.Repository, sn *data.Snapshot, limit int, abortAt int, snapshotTime time.Time) restoreOutcome {
	t.Helper()
	target := filepath.Join(rtest.TempDir(t), "target")
	res := NewRestorer(repo, sn, Options{})
	res.prefetchTreeLimit = limit
	var out restoreOutcome
	res.Error = func(location string, err error) error {
		out.Reported = append(out.Reported, filepath.ToSlash(location)+": "+err.Error())
		if abortAt != 0 && len(out.Reported) >= abortAt {
			return err
		}
		return nil
	}
	files, err := res.RestoreTo(t.Context(), target)
	out.Files = files
	if err != nil {
		out.Returned = err.Error()
	}

	out.Entries = map[string]restoredEntry{}
	firstPathOfFile := []string{}
	rtest.OK(t, filepath.Walk(target, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(target, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		e := restoredEntry{Perm: fi.Mode().Perm(), SnapshotTime: fi.ModTime().Equal(snapshotTime)}
		switch {
		case fi.IsDir():
			e.Kind = "dir"
		case fi.Mode().IsRegular():
			e.Kind = "file"
			e.Size = fi.Size()
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			e.Content = string(content)
			// hard links: the first path (in walk order) that is the same file
			for _, other := range firstPathOfFile {
				ofi, err := os.Stat(filepath.Join(target, filepath.FromSlash(other)))
				if err != nil {
					return err
				}
				if os.SameFile(fi, ofi) {
					e.LinkedTo = other
					break
				}
			}
			if e.LinkedTo == "" {
				firstPathOfFile = append(firstPathOfFile, rel)
			}
		case fi.Mode()&os.ModeSymlink != 0:
			e.Kind = "symlink"
			link, err := os.Readlink(path)
			if err != nil {
				return err
			}
			e.Content = link
		default:
			e.Kind = fi.Mode().Type().String()
		}
		out.Entries[rel] = e
		return nil
	}))
	return out
}

func describeDifference(want, got restoreOutcome) string {
	if !reflect.DeepEqual(want.Reported, got.Reported) {
		return fmt.Sprintf("errors reported:\n  below the bound: %q\n  across it:       %q", want.Reported, got.Reported)
	}
	if want.Returned != got.Returned || want.Files != got.Files {
		return fmt.Sprintf("returned (%q, %d files) below the bound, (%q, %d files) across it", want.Returned, want.Files, got.Returned, got.Files)
	}
	paths := map[string]struct{}{}
	for p := range want.Entries {
		paths[p] = struct{}{}
	}
	for p := range got.Entries {
		paths[p] = struct{}{}
	}
	sorted := make([]string, 0, len(paths))
	for p := range paths {
		sorted = append(sorted, p)
	}
	sort.Strings(sorted)
	for _, p := range sorted {
		w, wok := want.Entries[p]
		g, gok := got.Entries[p]
		if wok != gok || w != g {
			return fmt.Sprintf("entry %q: below the bound %+v (present %v), across it %+v (present %v)", p, w, wok, g, gok)
		}
	}
	return ""
}

// A restore whose snapshot has more trees than the bound on the tree-ID list
// must be indistinguishable from one that stays below the bound: the same
// content, metadata and hard links in the target, the same errors reported in
// the same order, the same returned error. The snapshot is restored with the
// production bound (31 trees: far below it, the list holds them all) and with
// bounds from 1 to one above the number of trees, without faults and with
// trees that fail to load or decode in the second pass, before and behind
// each bound.
func TestRestoreAcrossTreeIDBoundEqualsRestoreBelowIt(t *testing.T) {
	snapshotTime := time.Date(2020, 3, 4, 5, 6, 7, 0, time.UTC)
	// every directory differs from every other one, so that every tree has
	// an ID of its own
	leaf := func(tag string) Dir {
		return Dir{ModTime: snapshotTime, Mode: 0o750, Nodes: map[string]Node{
			"a-" + tag: File{Data: "first file of " + tag, ModTime: snapshotTime, Mode: 0o640},
			"b-" + tag: File{DataParts: []string{"second ", "file ", "of ", tag}, ModTime: snapshotTime},
			"c-" + tag: File{ModTime: snapshotTime}, // empty
			"d-" + tag: Dir{ModTime: snapshotTime, Nodes: map[string]Node{"inner-" + tag: File{Data: "inner " + tag, ModTime: snapshotTime}}},
			"e-" + tag: File{Data: "last file of " + tag, ModTime: snapshotTime},
		}}
	}
	top := map[string]Node{
		"root-file": File{Data: "root", ModTime: snapshotTime},
		// two hard-linked pairs, one inside a directory and one across two
		"link-1": File{Data: "linked content", Links: 2, Inode: 4242, ModTime: snapshotTime},
	}
	for i := 0; i < 6; i++ {
		group := map[string]Node{}
		for j := 0; j < 2; j++ {
			group[fmt.Sprintf("leaf-%d-%d", i, j)] = leaf(fmt.Sprintf("%d-%d", i, j))
		}
		group[fmt.Sprintf("file-%d", i)] = File{Data: fmt.Sprintf("group file %d", i), ModTime: snapshotTime}
		if i == 4 {
			group["link-2"] = File{Data: "linked content", Links: 2, Inode: 4242, ModTime: snapshotTime}
			group["pair-a"] = File{Data: "pair", Links: 2, Inode: 777, ModTime: snapshotTime}
			group["pair-b"] = File{Data: "pair", Links: 2, Inode: 777, ModTime: snapshotTime}
		}
		if i == 5 && runtime.GOOS != "windows" {
			// restored by the second pass (not a regular file)
			group["symlink"] = Symlink{Target: "file-5", ModTime: snapshotTime}
		}
		top[fmt.Sprintf("group-%d", i)] = Dir{ModTime: snapshotTime, Nodes: group}
	}

	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: top}, noopGetGenericAttributes)

	var order []preorderTree
	snapshotPreorder(t, repo, *sn.Tree, "", &order)
	trees := len(order)
	seen := map[restic.ID]struct{}{}
	for _, tr := range order {
		if _, dup := seen[tr.id]; dup {
			t.Fatalf("two directories share the tree %v: the faults below would not be per directory", tr.id.Str())
		}
		seen[tr.id] = struct{}{}
	}
	if trees != 1+6*(1+2*2) {
		t.Fatalf("the snapshot has %d trees", trees)
	}

	damage := func(id restic.ID) []byte {
		payload, err := repo.LoadBlob(context.Background(), restic.BlobHandle{Type: restic.TreeBlob, ID: id}, nil)
		rtest.OK(t, err)
		// behind the second node: the decoder returns two nodes, then fails
		second := strings.Index(string(payload), `},{"name"`)
		third := second + 1 + strings.Index(string(payload[second+1:]), `},{"name"`)
		if second < 0 || third <= second {
			t.Fatalf("tree %v has fewer than three nodes", id.Str())
		}
		return payload[:third+12]
	}

	type scenario struct {
		name    string
		wrapped bool // through treeFaultRepository (which also switches the tree carry off)
		fail    []int
		damaged []int
		abortAt int
	}
	// by position in the order of the restore: 2, 4, 14, 19, 27 are leaf
	// directories (five nodes each), 15 and 28 are their inner directories
	early, middle, late := 2, 15, 28
	for _, i := range []int{2, 4, 14, 19, 27} {
		if !strings.Contains(order[i].location, "/leaf-") || strings.Contains(order[i].location, "/d-") {
			t.Fatalf("tree %d is %s, not a leaf directory", i, order[i].location)
		}
	}
	scenarios := []scenario{
		{name: "no fault"},
		{name: "no fault, without the tree carry", wrapped: true},
		{name: "one tree fails to load, restore continues", wrapped: true, fail: []int{middle}},
		{name: "three trees fail to load, restore continues", wrapped: true, fail: []int{early, middle, late}},
		{name: "three trees fail to load, restore stops at the second error", wrapped: true, fail: []int{early, middle, late}, abortAt: 2},
		{name: "a tree fails to load, restore stops at once", wrapped: true, fail: []int{late}, abortAt: 1},
		{name: "two trees are damaged behind their second node, restore continues", wrapped: true, damaged: []int{4, 27}},
		{name: "a damaged tree and a failing one, restore continues", wrapped: true, damaged: []int{14}, fail: []int{19}},
		{name: "a damaged tree, restore stops at once", wrapped: true, damaged: []int{27}, abortAt: 1},
		{name: "the root tree fails to load", wrapped: true, fail: []int{0}},
	}
	limits := []int{1, 2, 3, 5, middle, middle + 1, late, trees - 1, trees, trees + 1}

	for _, sc := range scenarios {
		restore := func(limit int) restoreOutcome {
			var r restic.Repository = repo
			if sc.wrapped {
				f := &treeFaultRepository{Repository: repo, loads: map[restic.ID]int{}, fail: map[restic.ID]error{}, damaged: map[restic.ID][]byte{}}
				for _, i := range sc.fail {
					f.fail[order[i].id] = errors.New("injected failure of tree " + order[i].location)
				}
				for _, i := range sc.damaged {
					f.damaged[order[i].id] = damage(order[i].id)
				}
				r = f
			}
			return restoreAndDescribe(t, r, sn, limit, sc.abortAt, snapshotTime)
		}

		below := restore(0) // the production bound: the list holds all trees
		faults := len(sc.fail) + len(sc.damaged)
		// when the restore continues, every faulty tree is reported once; when
		// it stops, the error is reported again on the way up
		countOK := len(below.Reported) == faults
		if sc.abortAt != 0 {
			countOK = len(below.Reported) >= sc.abortAt
		}
		if !countOK || (below.Returned != "") != (sc.abortAt != 0) {
			t.Fatalf("%s: the reference restore reported %q and returned %q: the scenario does not do what its name says",
				sc.name, below.Reported, below.Returned)
		}
		if len(sc.fail)+len(sc.damaged) == 0 {
			if below.Files == 0 || below.Entries["link-1"].Content != "linked content" ||
				below.Entries["link-1"].LinkedTo != "group-4/link-2" || below.Entries["group-4/pair-b"].LinkedTo != "group-4/pair-a" ||
				!below.Entries["group-3/leaf-3-1"].SnapshotTime || !below.Entries["group-3/leaf-3-1/a-3-1"].SnapshotTime {
				t.Fatalf("%s: the reference restore is not what the snapshot holds: %+v", sc.name, below.Entries)
			}
		}

		for _, limit := range limits {
			across := restore(limit)
			if diff := describeDifference(below, across); diff != "" {
				t.Errorf("%s, bound %d of %d trees: the restore differs from the one below the bound: %s", sc.name, limit, trees, diff)
			}
		}
	}
}
