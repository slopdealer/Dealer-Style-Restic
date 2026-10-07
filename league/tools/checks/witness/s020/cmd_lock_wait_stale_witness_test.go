package main

// WITNESS (readside review): reads taken during the lock settle delay, before
// the lock is confirmed, are used after confirmation even when another restic
// process completed a whole run in between.
//
// Files under test: cmd/restic/wait_index_load.go (startRepositoryIndexLoad*,
// consume), cmd/restic/lock.go, internal/repository/lock_file.go
// (newLockWithWaitWork), cmd/restic/cmd_prune.go, cmd/restic/cmd_restore.go.
//
// Stock protocol: list locks, write own lock, sleep, list locks again, and only
// then read snapshots and the index. A process P2 that slipped in while P1's
// lock file was still being written (its write took longer than the settle
// delay) is either still running at P1's second check (P1 fails with "already
// locked") or has finished (P1 then reads the repository as P2 left it).
// Either way P1 is correct.
//
// Candidate: P1 starts listing snapshots and loading the index as soon as its
// lock file is written. If P2 finishes inside P1's settle delay, P1's second
// check succeeds and P1 goes on with an index that predates P2's changes.
//
// The test scales the settle delay to 1 s (production: 200 ms) so that the
// ordering is robust on a loaded machine; the precondition in production is
// "P1's lock-file write takes longer than 200 ms" (ordinary for SFTP, B2,
// rclone or a WAN link) while P2 is fast (for example local to the storage).
//
//   Scenario A: P1 = prune, P2 = backup. Stock: prune succeeds. Candidate:
//     prune fails ("failed finding blobs"/"not found in repository") because
//     it sees P2's snapshot (listed after confirmation) with the stale index.
//   Scenario B: P1 = restore <snapshot>, P2 = prune that repacks. Stock:
//     restore succeeds. Candidate: restore reports errors, because its stale
//     index points at packs P2 deleted.
//
//   Scenario C: P1 = backup, P2 = prune (cmd/restic/cmd_backup.go:545-601 and
//     backup_index_load.go use the same wait work). The data P1 is about to
//     back up is already in the repository but unused (its snapshot was
//     forgotten), so P2 deletes it. Stock: P1 loads the index after P2 and
//     uploads the data again; check passes. Candidate: P1 deduplicates against
//     the index it loaded before P2 ran, uploads nothing, reports success, and
//     writes a snapshot that points at deleted packs: DATA LOSS (the new
//     backup cannot be restored; check fails).
//
// No data is lost in scenarios A and B (the test re-checks the repository), the
// commands fail where stock succeeds. Scenario C loses the backup.
//
// The file uses only helpers present in the stock test suite, so the same file
// runs in both trees.
// OS: any. Run:
//   go test -run 'TestWitnessLockWaitStale' -count=1 -v ./cmd/restic
// Expected: PASS on stock, FAIL on the candidate.

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	rtest "github.com/restic/restic/internal/test"
)

const (
	witnessSettleDelay   = 1 * time.Second
	witnessLockSaveDelay = 2500 * time.Millisecond
)

type witnessEvents struct {
	t0             time.Time
	lockSaveBegin  chan struct{}
	lockSaved      chan struct{}
	p1IndexListed  chan struct{}
	onceBegin      sync.Once
	onceSaved      sync.Once
	onceListed     sync.Once
	onceGate       sync.Once
	mu             sync.Mutex
	log            []string
	listedAt       time.Duration
	lockSavedAt    time.Duration
	p2GateOpenedAt time.Duration
}

func newWitnessEvents() *witnessEvents {
	return &witnessEvents{
		t0:            time.Now(),
		lockSaveBegin: make(chan struct{}),
		lockSaved:     make(chan struct{}),
		p1IndexListed: make(chan struct{}),
	}
}

func (e *witnessEvents) since() time.Duration { return time.Since(e.t0).Round(time.Millisecond) }

// witnessSlowLockBackend is P1's backend: its first lock-file write is slow.
type witnessSlowLockBackend struct {
	backend.Backend
	ev *witnessEvents
}

func (b *witnessSlowLockBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if h.Type != backend.LockFile {
		return b.Backend.Save(ctx, h, rd)
	}
	first := false
	b.ev.onceBegin.Do(func() { first = true; close(b.ev.lockSaveBegin) })
	if first {
		time.Sleep(witnessLockSaveDelay)
	}
	err := b.Backend.Save(ctx, h, rd)
	b.ev.onceSaved.Do(func() {
		b.ev.mu.Lock()
		b.ev.lockSavedAt = b.ev.since()
		b.ev.mu.Unlock()
		close(b.ev.lockSaved)
	})
	return err
}

func (b *witnessSlowLockBackend) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	err := b.Backend.List(ctx, t, fn)
	if t == backend.IndexFile {
		b.ev.onceListed.Do(func() {
			b.ev.mu.Lock()
			b.ev.listedAt = b.ev.since()
			b.ev.mu.Unlock()
			close(b.ev.p1IndexListed)
		})
	}
	return err
}

// witnessGatedBackend is P2's backend: its first index write waits until P1's
// lock file exists, then gives P1 a moment (P1 lists the index in that moment
// only if it works during the settle delay), then goes on.
type witnessGatedBackend struct {
	backend.Backend
	ev *witnessEvents
}

func (b *witnessGatedBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if h.Type == backend.IndexFile {
		b.ev.onceGate.Do(func() {
			<-b.ev.lockSaved
			select {
			case <-b.ev.p1IndexListed:
			case <-time.After(300 * time.Millisecond):
			}
			// let P1 finish loading the index files it listed
			time.Sleep(150 * time.Millisecond)
			b.ev.mu.Lock()
			b.ev.p2GateOpenedAt = b.ev.since()
			b.ev.mu.Unlock()
		})
	}
	return b.Backend.Save(ctx, h, rd)
}

func witnessWriteRandomFile(t *testing.T, name string, size uint) {
	t.Helper()
	rtest.OK(t, os.MkdirAll(filepath.Dir(name), 0o700))
	rtest.OK(t, appendRandomData(name, size))
}

func witnessHooks(env *testEnvironment, ev *witnessEvents) (p1, p2 global.Options) {
	p1 = env.gopts
	p1.BackendTestHook = func(r backend.Backend) (backend.Backend, error) {
		return &witnessSlowLockBackend{Backend: r, ev: ev}, nil
	}
	p2 = env.gopts
	p2.BackendTestHook = func(r backend.Backend) (backend.Backend, error) {
		return &witnessGatedBackend{Backend: r, ev: ev}, nil
	}
	return p1, p2
}

func witnessReport(t *testing.T, ev *witnessEvents, settleEnd time.Duration) {
	ev.mu.Lock()
	defer ev.mu.Unlock()
	t.Logf("timeline: P1 lock file written at %v; P1 listed the index at %v; P2 index write released at %v; P1 second lock check not before %v",
		ev.lockSavedAt, ev.listedAt, ev.p2GateOpenedAt, settleEnd)
	if ev.listedAt != 0 && ev.listedAt < settleEnd {
		t.Logf("P1 listed the index BEFORE its lock was confirmed (work during the settle delay)")
	} else {
		t.Logf("P1 listed the index after its lock was confirmed (stock order)")
	}
}

// Scenario A: P1 = prune, P2 = backup.
func TestWitnessLockWaitStalePruneVsBackup(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()

	testRunInit(t, env.gopts)
	witnessWriteRandomFile(t, filepath.Join(env.testdata, "a", "one"), 200*1024)
	testRunBackup(t, "", []string{env.testdata}, BackupOptions{}, env.gopts)

	// P2 will back this up: a new file means a new tree and a new index file
	witnessWriteRandomFile(t, filepath.Join(env.testdata, "a", "two"), 200*1024)

	repository.TestSetLockTimeout(t, witnessSettleDelay)
	defer repository.TestSetLockTimeout(t, 0)

	ev := newWitnessEvents()
	p1opts, p2opts := witnessHooks(env, ev)

	var p2err error
	p2done := make(chan struct{})
	go func() {
		defer close(p2done)
		<-ev.lockSaveBegin // P1 has passed its first lock check and is writing its lock
		p2err = testRunBackupAssumeFailure(t, "", []string{env.testdata}, BackupOptions{}, p2opts)
		t.Logf("P2 backup finished at %v: err=%v", ev.since(), p2err)
	}()

	p1err := withTermStatus(t, p1opts, func(ctx context.Context, gopts global.Options) error {
		return runPrune(context.TODO(), PruneOptions{MaxUnused: "5%"}, gopts, gopts.Term)
	})
	t.Logf("P1 prune finished at %v: err=%v", ev.since(), p1err)
	<-p2done

	ev.mu.Lock()
	settleEnd := ev.lockSavedAt + witnessSettleDelay
	ev.mu.Unlock()
	witnessReport(t, ev, settleEnd)

	if p2err != nil {
		t.Fatalf("orchestration invalid (P2 backup must succeed in both trees): %v", p2err)
	}

	// the repository must be intact whatever P1 did
	repository.TestSetLockTimeout(t, 0)
	env.gopts.BackendTestHook = nil
	testListSnapshots(t, env.gopts, 2)
	testRunCheck(t, env.gopts)
	t.Logf("repository intact after the race: 2 snapshots, check --read-data passes")

	if p1err != nil {
		t.Errorf("WRONG RESULT: prune failed although no other process held a lock when its lock was confirmed (stock succeeds): %v", p1err)
	}
}

// Scenario B: P1 = restore, P2 = prune that repacks.
func TestWitnessLockWaitStaleRestoreVsPrune(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()

	testRunInit(t, env.gopts)
	dirX := filepath.Join(env.testdata, "x")
	dirY := filepath.Join(env.testdata, "y")
	witnessWriteRandomFile(t, filepath.Join(dirX, "keep"), 200*1024)
	witnessWriteRandomFile(t, filepath.Join(dirY, "drop"), 200*1024)

	// snapshot 1 = x + y (one data pack), snapshot 2 = x only
	// relative paths, so that the snapshot holds only x and y (restoring the
	// metadata of the temp directory's parents fails on Windows in any tree)
	testRunBackup(t, env.testdata, []string{"x", "y"}, BackupOptions{}, env.gopts)
	first := testListSnapshots(t, env.gopts, 1)[0]
	testRunBackup(t, env.testdata, []string{"x"}, BackupOptions{}, env.gopts)
	var second string
	for _, id := range testListSnapshots(t, env.gopts, 2) {
		if id != first {
			second = id.String()
		}
	}
	// forget snapshot 1 without pruning: y's data is now unused in a pack that
	// also holds x's data, so a prune with --max-unused 0 repacks that pack
	testRunForget(t, env.gopts, ForgetOptions{}, first.String())

	repository.TestSetLockTimeout(t, witnessSettleDelay)
	defer repository.TestSetLockTimeout(t, 0)

	ev := newWitnessEvents()
	p1opts, p2opts := witnessHooks(env, ev)

	var p2err error
	p2done := make(chan struct{})
	go func() {
		defer close(p2done)
		<-ev.lockSaveBegin
		p2err = withTermStatus(t, p2opts, func(ctx context.Context, gopts global.Options) error {
			return runPrune(context.TODO(), PruneOptions{MaxUnused: "0"}, gopts, gopts.Term)
		})
		t.Logf("P2 prune finished at %v: err=%v", ev.since(), p2err)
	}()

	target := filepath.Join(env.base, "restore")
	p1err := testRunRestoreAssumeFailure(t, second, RestoreOptions{Target: target}, p1opts)
	t.Logf("P1 restore finished at %v: err=%v", ev.since(), p1err)
	<-p2done

	ev.mu.Lock()
	settleEnd := ev.lockSavedAt + witnessSettleDelay
	ev.mu.Unlock()
	witnessReport(t, ev, settleEnd)

	if p2err != nil {
		t.Fatalf("orchestration invalid (P2 prune must succeed in both trees): %v", p2err)
	}

	repository.TestSetLockTimeout(t, 0)
	env.gopts.BackendTestHook = nil
	testRunCheck(t, env.gopts)
	t.Logf("repository intact after the race: check --read-data passes")

	if p1err != nil {
		t.Errorf("WRONG RESULT: restore failed although no other process held a lock when its lock was confirmed (stock succeeds): %v", p1err)
	}
}

// Scenario C: P1 = backup, P2 = prune.
func TestWitnessLockWaitStaleBackupVsPrune(t *testing.T) {
	env, cleanup := withTestEnvironment(t)
	defer cleanup()

	testRunInit(t, env.gopts)
	dir0 := filepath.Join(env.testdata, "d0")
	dir1 := filepath.Join(env.testdata, "d1")
	witnessWriteRandomFile(t, filepath.Join(dir0, "other"), 200*1024)
	witnessWriteRandomFile(t, filepath.Join(dir1, "precious"), 200*1024)

	// snapshot 0 = d0 (stays), snapshot 1 = d1 (forgotten below, not pruned)
	testRunBackup(t, "", []string{dir0}, BackupOptions{}, env.gopts)
	keep := testListSnapshots(t, env.gopts, 1)[0]
	testRunBackup(t, "", []string{dir1}, BackupOptions{}, env.gopts)
	for _, id := range testListSnapshots(t, env.gopts, 2) {
		if id != keep {
			testRunForget(t, env.gopts, ForgetOptions{}, id.String())
		}
	}
	// d1's data and trees are now in the repository but unused: the next prune
	// deletes them, the next backup of d1 needs them.

	repository.TestSetLockTimeout(t, witnessSettleDelay)
	defer repository.TestSetLockTimeout(t, 0)

	ev := newWitnessEvents()
	p1opts, p2opts := witnessHooks(env, ev)

	var p2err error
	p2done := make(chan struct{})
	go func() {
		defer close(p2done)
		<-ev.lockSaveBegin
		p2err = withTermStatus(t, p2opts, func(ctx context.Context, gopts global.Options) error {
			return runPrune(context.TODO(), PruneOptions{MaxUnused: "0"}, gopts, gopts.Term)
		})
		t.Logf("P2 prune finished at %v: err=%v", ev.since(), p2err)
	}()

	p1err := testRunBackupAssumeFailure(t, "", []string{dir1}, BackupOptions{}, p1opts)
	t.Logf("P1 backup finished at %v: err=%v", ev.since(), p1err)
	<-p2done

	ev.mu.Lock()
	settleEnd := ev.lockSavedAt + witnessSettleDelay
	ev.mu.Unlock()
	witnessReport(t, ev, settleEnd)

	if p2err != nil {
		t.Fatalf("orchestration invalid (P2 prune must succeed in both trees): %v", p2err)
	}
	if p1err != nil {
		t.Fatalf("P1 backup failed (not the scenario under test): %v", p1err)
	}

	repository.TestSetLockTimeout(t, 0)
	env.gopts.BackendTestHook = nil
	testListSnapshots(t, env.gopts, 2)
	stdout, stderr, checkErr := testRunCheckOutput(t, env.gopts, false)
	t.Logf("check --read-data after the race: err=%v", checkErr)
	if checkErr != nil {
		t.Logf("check stdout: %s", stdout)
		t.Logf("check stderr: %s", stderr)
		t.Errorf("DATA LOSS: the backup reported success, but its snapshot references data the concurrent prune deleted (stock uploads the data again and check passes): %v", checkErr)
	}
}
