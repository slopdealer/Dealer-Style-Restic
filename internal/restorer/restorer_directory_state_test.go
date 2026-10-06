package restorer

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

// restoreDirectoriesPeak restores a snapshot of parents*(children+1) empty
// directories into an empty target and returns the number of directories and
// by how much the live heap grew while the restore ran.
func restoreDirectoriesPeak(t *testing.T, parents, children int) (dirs int, growth int64) {
	// long names: a path that is kept per directory shows clearly
	childName := strings.Repeat("directory-name-", 6)
	nodes := map[string]Node{}
	for i := 0; i < parents; i++ {
		sub := map[string]Node{}
		for j := 0; j < children; j++ {
			sub[fmt.Sprintf("%s%05d", childName, j)] = Dir{Nodes: map[string]Node{}}
		}
		nodes[fmt.Sprintf("parent-%05d", i)] = Dir{Nodes: sub}
	}
	repo := repository.TestRepository(t)
	sn, _ := saveSnapshot(t, repo, Snapshot{Nodes: nodes}, noopGetGenericAttributes)
	nodes = nil
	tempdir := filepath.Join(rtest.TempDir(t), "target")

	liveHeap := func() uint64 {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		return m.HeapAlloc
	}
	before := liveHeap()

	var peak atomic.Uint64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			if live := liveHeap(); live > peak.Load() {
				peak.Store(live)
			}
		}
	}()

	res := NewRestorer(repo, sn, Options{})
	_, err := res.RestoreTo(t.Context(), tempdir)
	close(stop)
	<-done
	rtest.OK(t, err)
	return parents * (children + 1), int64(peak.Load()) - int64(before)
}

// The restore must not keep a path for every directory it creates: the
// directories that were created by this restore are remembered only while the
// traversal is inside them. What remains per directory is the tree ID that
// the second pass loads ahead (32 bytes). Stock keeps nothing per directory.
//
// The bound is on the growth per additional directory, measured between a
// small and a large snapshot of directories with names of 95 bytes, so that
// constant costs cancel. With a path per directory it is above 250 bytes.
func TestRestoreKeepsNoPathPerDirectory(t *testing.T) {
	if testing.Short() {
		t.Skip("restores 15,000 directories")
	}
	smallDirs, smallGrowth := restoreDirectoriesPeak(t, 10, 300)
	largeDirs, largeGrowth := restoreDirectoriesPeak(t, 40, 300)

	perDirectory := (largeGrowth - smallGrowth) / int64(largeDirs-smallDirs)
	t.Logf("%d directories: live heap grows by %d KiB; %d directories: by %d KiB; %d bytes per additional directory",
		smallDirs, smallGrowth>>10, largeDirs, largeGrowth>>10, perDirectory)
	if perDirectory > 150 {
		t.Fatalf("the restore keeps %d bytes per directory", perDirectory)
	}
}
