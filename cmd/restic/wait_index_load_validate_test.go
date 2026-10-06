package main

// The comparison made after the lock is confirmed (repair R1, design
// "validate"): what the three verdicts keep, load and discard.
//
// Each case starts the wait work of restore (snapshot listing, then index
// load) on its own repository handle, lets another handle change the
// repository once that work has finished, then confirms and consumes as the
// command does. Backend operations after the confirmation are counted.
//
//	go test -run 'TestLockWaitValidation' -count=1 -v ./cmd/restic

import (
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// validationCounts: backend operations after the lock was confirmed.
type validationCounts struct{ listSnapshots, listIndex, loadIndex int }

func TestLockWaitValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		// change is applied by another process after the wait work has read
		// everything and before the lock is confirmed
		change func(t *testing.T, shared backend.Backend, other *repository.Repository, spec *schedSpec)
		// failWaitListing: the snapshot listing of the wait fails
		failWaitListing bool
		wantReread      bool
		want            validationCounts
	}{
		{
			name:   "nothing changed: keep",
			change: func(*testing.T, backend.Backend, *repository.Repository, *schedSpec) {},
			// the two listings of the comparison, nothing loaded
			want: validationCounts{listSnapshots: 1, listIndex: 1},
		},
		{
			name: "backup beside us added an index file and a snapshot: load the added file only",
			change: func(t *testing.T, _ backend.Backend, other *repository.Repository, spec *schedSpec) {
				rtest.OK(t, other.LoadIndex(context.Background(), restic.NoopTerminalCounterFactory))
				_, err := schedStoreSnapshot(context.Background(), other, spec.seed, []int{5, 6}, time.Now())
				rtest.OK(t, err)
			},
			// the two listings of the comparison, and the one added index file
			want: validationCounts{listSnapshots: 1, listIndex: 1, loadIndex: 1},
		},
		{
			name: "prune beside us replaced the index files: discard and read again",
			change: func(t *testing.T, shared backend.Backend, other *repository.Repository, _ *schedSpec) {
				ctx := context.Background()
				rtest.OK(t, other.LoadIndex(ctx, restic.NoopTerminalCounterFactory))
				opts := PruneOptions{MaxUnused: "0"}
				rtest.OK(t, verifyPruneOptions(&opts))
				rtest.OK(t, runPruneWithRepo(ctx, opts, global.Options{}, other, restic.NewIDSet(), restic.NewNoopPrinter()))
			},
			wantReread: true,
		},
		{
			name: "forget beside us removed a snapshot: discard and read again",
			change: func(t *testing.T, _ backend.Backend, other *repository.Repository, _ *schedSpec) {
				ctx := context.Background()
				snapshots, err := schedLoadSnapshots(ctx, other, other)
				rtest.OK(t, err)
				newest := schedNewestFirst(snapshots)
				rtest.OK(t, other.RemoveUnpacked(ctx, restic.WriteableSnapshotFile, newest[len(newest)-1]))
			},
			wantReread: true,
		},
		{
			name:            "the snapshot listing of the wait failed: read again, snapshots first",
			change:          func(*testing.T, backend.Backend, *repository.Repository, *schedSpec) {},
			failWaitListing: true,
			wantReread:      true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			spec := &schedSpec{seed: 11, live: [][]int{{0, 1}, {2}, {1, 3}}, forgotten: [][]int{{4}}}
			shared := schedTemplate(t, schedEmptyRepository(t), spec)
			other, err := schedOpenRepository(ctx, shared)
			rtest.OK(t, err)

			probe := &validationProbe{Backend: shared, failSnapshotListing: test.failWaitListing}
			repo, err := schedOpenRepository(ctx, probe)
			rtest.OK(t, err)

			// the wait work of restore, as the lock code starts it
			var load *repositoryIndexLoad
			settle := repositoryIndexWaitWork(&load, true)(ctx, repo)
			for len(load.result) == 0 {
				time.Sleep(time.Millisecond)
			}
			test.change(t, shared, other, spec)

			// the lock is confirmed; the command asks for the snapshots, then the index
			probe.confirm()
			settle(true)
			snapshots, err := schedLoadSnapshots(ctx, load.snapshotLister(repo), repo)
			rtest.OK(t, err)
			rtest.OK(t, load.consume(restic.NoopTerminalCounterFactory))
			defer load.discard()

			// what the command holds is what a fresh handle reads now
			fresh, err := schedOpenRepository(ctx, shared)
			rtest.OK(t, err)
			rtest.OK(t, fresh.LoadIndex(ctx, restic.NoopTerminalCounterFactory))
			freshSnapshots, err := schedLoadSnapshots(ctx, fresh, fresh)
			rtest.OK(t, err)
			rtest.Assert(t, schedSnapshotIDs(snapshots).Equals(schedSnapshotIDs(freshSnapshots)),
				"snapshots used %v, a fresh read gives %v", schedSnapshotIDs(snapshots), schedSnapshotIDs(freshSnapshots))
			rtest.Assert(t, repo.LoadedIndexIDs().Equals(fresh.LoadedIndexIDs()),
				"index files used %v, a fresh read gives %v", repo.LoadedIndexIDs(), fresh.LoadedIndexIDs())
			for _, sn := range freshSnapshots {
				rtest.OK(t, schedReadTree(ctx, repo, *sn.Tree))
			}

			rtest.Assert(t, load.rereadAfterConfirmation() == test.wantReread, "reread: %v, want %v", load.rereadAfterConfirmation(), test.wantReread)
			got := probe.after()
			if test.wantReread {
				// everything is read again: every index file is loaded once more
				rtest.Assert(t, got.loadIndex == len(fresh.LoadedIndexIDs()), "index files loaded after confirmation: %d, want all %d", got.loadIndex, len(fresh.LoadedIndexIDs()))
				// snapshots are listed before the index is listed for the reload
				rtest.Assert(t, probe.snapshotsListedBeforeLastIndexListing(), "the index was listed for the reload before the snapshots were listed")
			} else {
				rtest.Assert(t, got == test.want, "backend operations after confirmation: %+v, want %+v", got, test.want)
			}
		})
	}
}

// validationProbe counts what is read after the lock was confirmed.
type validationProbe struct {
	backend.Backend
	failSnapshotListing bool

	mu        sync.Mutex
	confirmed bool
	counts    validationCounts
	order     []backend.FileType // listings after confirmation
}

func (p *validationProbe) confirm() {
	p.mu.Lock()
	p.confirmed = true
	p.mu.Unlock()
}

func (p *validationProbe) after() validationCounts {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.counts
}

func (p *validationProbe) snapshotsListedBeforeLastIndexListing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	snapshots := false
	ok := false
	for _, t := range p.order {
		switch t {
		case backend.SnapshotFile:
			snapshots = true
		case backend.IndexFile:
			ok = snapshots
		}
	}
	return ok
}

func (p *validationProbe) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	p.mu.Lock()
	confirmed := p.confirmed
	if confirmed {
		switch t {
		case backend.SnapshotFile:
			p.counts.listSnapshots++
			p.order = append(p.order, t)
		case backend.IndexFile:
			p.counts.listIndex++
			p.order = append(p.order, t)
		}
	}
	p.mu.Unlock()
	if !confirmed && p.failSnapshotListing && t == backend.SnapshotFile {
		return errors.New("injected failure of the snapshot listing during the wait")
	}
	return p.Backend.List(ctx, t, fn)
}

func (p *validationProbe) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	p.mu.Lock()
	if p.confirmed && h.Type == backend.IndexFile {
		p.counts.loadIndex++
	}
	p.mu.Unlock()
	return p.Backend.Load(ctx, h, length, offset, fn)
}
