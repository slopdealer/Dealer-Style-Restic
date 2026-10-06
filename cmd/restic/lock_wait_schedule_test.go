package main

// Randomized-schedule test for the lock wait (repair R1).
//
// Two or three "processes" (backup, restore, prune, forget, forget --prune,
// check; exclusive and non-exclusive) work on one in-memory backend. Each has
// its own repository handle and its own view of the backend, which delays
// every operation by an amount drawn from a seeded generator. The test runs in
// a testing/synctest bubble: time is virtual, so the 200 ms settle delay of
// the real lock code and all injected delays cost no wall time and every
// schedule is reproducible from its seed.
//
// What is real and what is a model:
//   - real: the lock code (repository.LockRepo / LockRepoWithWaitWork), the
//     wait work of each command and what the command does with it after the
//     lock call (backupIndexWaitWork, repositoryIndexWaitWork,
//     checkerIndexWaitWork and their methods), the repository (index, packs,
//     deduplication), prune (runPruneWithRepoAndIndexLoad) and the checker;
//   - model: the rest of each command. backup stores a fixed list of blobs, a
//     tree and a snapshot; restore reads every blob of the newest snapshot;
//     forget removes all but the newest N snapshots.
//
// Every schedule is run twice on identical repositories: once in STOCK ORDER
// (no wait work: everything is read after the lock call returned), and once
// with the wait work of the tree under test. The lock protocol sees the same
// timeline in both runs: lock operations get the same delays, and a process
// makes its changes at a fixed time after its lock was confirmed and unlocks
// at a fixed time after that, however long its reads took.
//
// Oracle, for every process whose lock was confirmed (that is, in every
// schedule where stock's protocol lets the process proceed):
//
//	stale    the snapshot listing or the index it uses contains a file that
//	         was removed before its lock was confirmed;
//	missing  a snapshot or index file that existed when its lock was confirmed
//	         is not in what it uses. Exception: a file written by a process
//	         whose lock does not conflict with ours, after our lock file was
//	         written (stock itself may or may not see such a file);
//	outcome  the command ends differently (ok / failed / locked) than in the
//	         stock-order run;
//	final    after the schedule a snapshot is not fully readable, or the
//	         snapshots differ from those of the stock-order run.
//
// The same checks are applied to the stock-order run itself, which must
// produce none ("control"): that is the test of the oracle.
//
// Knobs (environment):
//
//	RESTIC_R1_SCHEDULES=n      number of schedules. The default is 500, about
//	                           15 s, so that the package's test time stays
//	                           modest; 100 with -short. The repair report used
//	                           3000 per lag mode. Odd seeds have two processes,
//	                           even seeds three.
//	RESTIC_R1_SCHEDULE_SEED=n  first seed (default 1)
//	RESTIC_R1_SCHEDULE_LAG=m   how the backend lists NEW lock files:
//	                             (unset)    as soon as Save has written them
//	                             uniform    a fixed time later (0..190 ms)
//	                             random     up to 190 ms later, drawn per file
//	                             nonatomic  as random, and one listing looks at
//	                                        each lock file at a different moment
//	RESTIC_R1_SCHEDULE_VERBOSE=1  print every failing schedule in full
//
// Run:
//
//	go test -run 'TestLockWaitSchedules' -count=1 -v ./cmd/restic

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"math/rand"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/checker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// schedSettle is the lock's settle delay in the schedules: the production value.
const schedSettle = 200 * time.Millisecond

// schedRetryLock is the --retry-lock value of every process. With the default
// of zero the lock code picks at random, after a refused attempt, between one
// last attempt and another round (two timers that are both due), so the number
// of attempts is not a function of the schedule. With one nanosecond a refused
// first attempt is always followed by exactly one more, at once. A process
// thus makes up to two lock attempts, each with its own wait work.
const schedRetryLock = time.Nanosecond

type schedKind int

const (
	schedBackup schedKind = iota
	schedRestore
	schedPrune
	schedForget
	schedForgetPrune
	schedCheck
	schedKinds
)

func (k schedKind) String() string {
	return [...]string{"backup", "restore", "prune", "forget", "forget--prune", "check"}[k]
}

func (k schedKind) exclusive() bool { return k != schedBackup && k != schedRestore }

// changes reports whether the command removes or adds repository files.
func (k schedKind) changes() bool { return k != schedRestore && k != schedCheck }

type schedLagMode int

const (
	schedLagNone schedLagMode = iota
	schedLagUniform
	schedLagRandom
	schedLagNonAtomic
)

const schedMaxLag = 190 * time.Millisecond

type schedProcSpec struct {
	kind  schedKind
	start time.Duration
	// The lock file is written saveBefore after the Save call starts, and the
	// call returns saveAfter later (a response in flight, a retried request
	// whose first attempt had succeeded).
	saveBefore, saveAfter time.Duration
	// The command makes its changes work after its lock was confirmed, and
	// unlocks hold after that.
	work, hold time.Duration
	// quick: a process next to the storage. Its reads take a fraction of a
	// millisecond, so that its whole run fits into a few tens of milliseconds.
	quick     bool
	blobs     []int  // backup: what it stores (indices into the content pool)
	keep      int    // forget: number of newest snapshots it keeps
	maxUnused string // prune
}

type schedSpec struct {
	seed      int64
	procs     []schedProcSpec
	live      [][]int // snapshots in the repository at the start
	forgotten [][]int // snapshots that were forgotten but not pruned: unused data
	staleLock bool    // a non-exclusive lock left behind by a crashed process
	lagMode   schedLagMode
	lag       time.Duration
}

const schedPool = 8 // distinct blob contents per schedule

func schedBlob(seed int64, i int) []byte {
	buf := make([]byte, 0, 256)
	for len(buf) < 200 {
		sum := sha256.Sum256([]byte(fmt.Sprintf("blob %d %d %d", seed, i, len(buf))))
		buf = append(buf, sum[:]...)
	}
	return buf
}

func schedBetween(rnd *rand.Rand, lo, hi time.Duration) time.Duration {
	return lo + time.Duration(rnd.Int63n(int64(hi-lo)+1))
}

func schedPick(rnd *rand.Rand, n int) []int {
	out := make([]int, 0, n)
	for len(out) < n {
		out = append(out, rnd.Intn(schedPool))
	}
	return out
}

// newSchedSpec draws one schedule. Part of the schedules is aimed at the
// window that matters: one process (the "victim") writes its lock file slowly,
// so that a conflicting process (the "intruder") is confirmed without seeing
// it, and the victim's lock file lands around the time the intruder makes its
// changes. The rest is unaimed.
func newSchedSpec(seed int64, procs int, lag schedLagMode) *schedSpec {
	rnd := rand.New(rand.NewSource(seed))
	spec := &schedSpec{seed: seed, lagMode: lag}
	if lag != schedLagNone {
		spec.lag = schedBetween(rnd, 0, schedMaxLag)
	}
	for i, n := 0, 1+rnd.Intn(3); i < n; i++ {
		spec.live = append(spec.live, schedPick(rnd, 1+rnd.Intn(3)))
	}
	for i, n := 0, rnd.Intn(3); i < n; i++ {
		spec.forgotten = append(spec.forgotten, schedPick(rnd, 1+rnd.Intn(3)))
	}
	spec.staleLock = rnd.Intn(12) == 0

	general := func() schedProcSpec {
		p := schedProcSpec{
			kind:  schedKind(rnd.Intn(int(schedKinds))),
			start: schedBetween(rnd, 0, 3*time.Second),
			work:  schedBetween(rnd, 1500*time.Millisecond, 2500*time.Millisecond),
		}
		if rnd.Intn(10) < 6 {
			p.saveBefore = schedBetween(rnd, 300*time.Microsecond, 10*time.Millisecond)
		} else {
			p.saveBefore = schedBetween(rnd, 200*time.Millisecond, 3*time.Second)
		}
		return p
	}
	finish := func(p *schedProcSpec) {
		if p.quick {
			p.hold = schedBetween(rnd, time.Millisecond, 40*time.Millisecond)
		} else if rnd.Intn(10) < 7 {
			p.hold = schedBetween(rnd, time.Millisecond, 150*time.Millisecond)
		} else {
			p.hold = schedBetween(rnd, 150*time.Millisecond, 1500*time.Millisecond)
		}
		if rnd.Intn(4) == 0 {
			// part of the save latency lies after the file became visible
			p.saveAfter = time.Duration(rnd.Int63n(int64(p.saveBefore) + 1))
			p.saveBefore -= p.saveAfter
		}
		p.keep = rnd.Intn(3)
		p.maxUnused = []string{"0", "5%", "unlimited"}[rnd.Intn(3)]
		// a backup often stores data that is in the repository but unused
		n := 1 + rnd.Intn(3)
		if len(spec.forgotten) > 0 && rnd.Intn(3) > 0 {
			p.blobs = append([]int(nil), spec.forgotten[rnd.Intn(len(spec.forgotten))]...)
		} else {
			p.blobs = schedPick(rnd, n)
		}
	}

	intruderKinds := []schedKind{schedPrune, schedForgetPrune, schedForget, schedBackup}
	victimKindsFor := func(intruder schedKind) []schedKind {
		if !intruder.exclusive() {
			// only an exclusive lock conflicts with a non-exclusive intruder
			return []schedKind{schedPrune, schedForgetPrune, schedCheck}
		}
		return []schedKind{schedBackup, schedRestore, schedPrune, schedForgetPrune, schedCheck}
	}

	// With a backend that lists new lock files late there is a second window:
	// two processes write their lock files within the lag of each other, so
	// that neither file is listed yet when the other process looks, and one of
	// them finishes its whole run within that lag. Half of the schedules of
	// the lag modes are aimed at it. (Without lag these draws are not made, so
	// that a seed names the same schedule as before.)
	if lag != schedLagNone && rnd.Intn(2) == 0 {
		spec.staleLock = false
		intruder := general()
		intruder.kind = intruderKinds[rnd.Intn(len(intruderKinds))]
		intruder.quick = true
		intruder.start = schedBetween(rnd, 80*time.Millisecond, 120*time.Millisecond)
		intruder.saveBefore = schedBetween(rnd, 300*time.Microsecond, 8*time.Millisecond)
		intruder.work = schedBetween(rnd, 5*time.Millisecond, 60*time.Millisecond)

		victim := general()
		kinds := victimKindsFor(intruder.kind)
		victim.kind = kinds[rnd.Intn(len(kinds))]
		victim.start = intruder.start + schedBetween(rnd, -20*time.Millisecond, schedMaxLag)
		victim.saveBefore = schedBetween(rnd, 300*time.Microsecond, 8*time.Millisecond)
		spec.procs = append(spec.procs, victim, intruder)
	}

	aimed := len(spec.procs) == 0 && rnd.Intn(10) < 7
	if aimed {
		// the intruder changes the repository; the victim reads during its wait
		intruder := general()
		intruder.kind = intruderKinds[rnd.Intn(len(intruderKinds))]
		intruder.start = schedBetween(rnd, 80*time.Millisecond, 120*time.Millisecond)
		intruder.saveBefore = schedBetween(rnd, 300*time.Microsecond, 8*time.Millisecond)

		victim := general()
		victimKinds := victimKindsFor(intruder.kind)
		victim.kind = victimKinds[rnd.Intn(len(victimKinds))]
		victim.start = schedBetween(rnd, 0, 110*time.Millisecond)
		// the intruder is confirmed about 210 ms after its start and changes
		// the repository intruder.work later
		changes := intruder.start + 210*time.Millisecond + intruder.work
		lands := changes + schedBetween(rnd, -350*time.Millisecond, 200*time.Millisecond)
		victim.saveBefore = lands - victim.start
		spec.procs = append(spec.procs, victim, intruder)
	}
	for len(spec.procs) < procs {
		spec.procs = append(spec.procs, general())
	}
	for i := range spec.procs {
		finish(&spec.procs[i])
	}
	return spec
}

func (s *schedSpec) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "seed %d: %d live snapshot(s) %v, %d forgotten %v", s.seed, len(s.live), s.live, len(s.forgotten), s.forgotten)
	if s.staleLock {
		b.WriteString(", stale non-exclusive lock")
	}
	if s.lagMode != schedLagNone {
		fmt.Fprintf(&b, ", lock listing lag mode %d (%v)", s.lagMode, s.lag)
	}
	for i, p := range s.procs {
		fmt.Fprintf(&b, "\n      P%d %-13s start %v, lock save %v+%v, changes %v after confirmation, unlock %v later", i, p.kind, p.start, p.saveBefore, p.saveAfter, p.work, p.hold)
		if p.quick {
			b.WriteString(", quick reads")
		}
		switch p.kind {
		case schedBackup:
			fmt.Fprintf(&b, ", stores %v", p.blobs)
		case schedForget, schedForgetPrune:
			fmt.Fprintf(&b, ", keeps %d", p.keep)
		}
	}
	return b.String()
}

const (
	schedPhaseOpen int32 = iota // opening the repository: no delays
	schedPhaseRead              // locking and reading: every operation is delayed
	schedPhaseWork              // the command's changes: one instant
)

type schedFile struct {
	creator int // process index, -1: in the repository at the start
	added   time.Duration
	removed time.Duration
	gone    bool
}

type schedLock struct {
	owner     int
	size      int64
	visibleAt time.Duration
	removedAt time.Duration
	gone      bool
}

type schedRun struct {
	spec  *schedSpec
	stock bool // stock order: no wait work
	be    backend.Backend
	t0    time.Time

	mu    sync.Mutex
	files map[backend.Handle]*schedFile // snapshot, index and pack files
	locks map[string]*schedLock
	procs []*schedProc
	notes []string

	final      []string // tree IDs of the snapshots after the schedule, sorted
	broken     []string
	goroutines int // goroutines that outlived the processes
}

type schedProc struct {
	id    int
	spec  *schedProcSpec
	run   *schedRun
	phase atomic.Int32

	mu        sync.Mutex
	ordinals  map[string]int
	lockSaved time.Duration
	hasLock   bool
	waitRead  bool // read snapshot or index files before its lock was confirmed
	done      bool // the lock call returned

	locked        bool
	confirmed     time.Duration
	lateRead      bool
	usedSnapshots restic.IDSet // nil: the command takes no snapshot listing before its changes
	usedIndex     restic.IDSet
	outcome       string // "ok", "failed", "locked"
	detail        string
}

func (r *schedRun) now() time.Duration { return time.Since(r.t0) }

func (r *schedRun) sleepUntil(at time.Duration) {
	if d := at - r.now(); d > 0 {
		time.Sleep(d)
	}
}

func (r *schedRun) notef(format string, args ...any) {
	r.mu.Lock()
	r.notes = append(r.notes, fmt.Sprintf("%v ", r.now())+fmt.Sprintf(format, args...))
	r.mu.Unlock()
}

func schedTracked(t backend.FileType) bool {
	return t == backend.SnapshotFile || t == backend.IndexFile || t == backend.PackFile
}

// draw returns a number that depends only on the seed, the process and the
// identity of the operation, not on which goroutine comes first.
func (p *schedProc) draw(stream, key string) uint64 {
	p.mu.Lock()
	if p.ordinals == nil {
		p.ordinals = make(map[string]int)
	}
	n := p.ordinals[stream+"/"+key]
	p.ordinals[stream+"/"+key] = n + 1
	p.mu.Unlock()
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d/%s/%s/%d", p.run.spec.seed, p.id, stream, key, n)))
	return binary.LittleEndian.Uint64(sum[:8])
}

func schedSpread(v uint64, lo, hi time.Duration) time.Duration {
	return lo + time.Duration(v%uint64(hi-lo+1))
}

// lockDelay: operations of the lock protocol. They depend on nothing that
// differs between the stock-order run and the run under test.
func (p *schedProc) lockDelay(key string) time.Duration {
	return schedSpread(p.draw("lock", key), 300*time.Microsecond, 6*time.Millisecond)
}

func (p *schedProc) gateDelay() time.Duration {
	v := p.draw("gate", "list")
	if v%10 == 0 {
		// slower than the settle delay now and then
		return schedSpread(v>>8, 6*time.Millisecond, 300*time.Millisecond)
	}
	return schedSpread(v>>8, 300*time.Microsecond, 6*time.Millisecond)
}

func (p *schedProc) readDelay(key string) time.Duration {
	v := p.draw("read", key)
	if p.spec.quick {
		return schedSpread(v>>8, 50*time.Microsecond, 300*time.Microsecond)
	}
	if v%100 < 15 {
		return schedSpread(v>>8, 6*time.Millisecond, 150*time.Millisecond)
	}
	return schedSpread(v>>8, 200*time.Microsecond, 6*time.Millisecond)
}

// schedView is one process's connection to the shared backend.
type schedView struct {
	backend.Backend
	run  *schedRun
	proc *schedProc
}

func schedSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (v *schedView) noteRead(t backend.FileType) {
	if t != backend.SnapshotFile && t != backend.IndexFile {
		return
	}
	v.proc.mu.Lock()
	if !v.proc.done {
		v.proc.waitRead = true
	}
	v.proc.mu.Unlock()
}

func (v *schedView) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	p, r := v.proc, v.run
	if h.Type != backend.LockFile {
		err := v.Backend.Save(ctx, h, rd)
		if err == nil && schedTracked(h.Type) {
			h.IsMetadata = false
			r.mu.Lock()
			r.files[h] = &schedFile{creator: p.id, added: r.now()}
			r.mu.Unlock()
		}
		return err
	}

	p.mu.Lock()
	first := !p.hasLock
	p.mu.Unlock()
	before, after := p.lockDelay("save"), time.Duration(0)
	if first {
		before, after = p.spec.saveBefore, p.spec.saveAfter
	}
	if err := schedSleep(ctx, before); err != nil {
		return err
	}
	err := v.Backend.Save(ctx, h, rd)
	if err == nil {
		r.mu.Lock()
		r.locks[h.Name] = &schedLock{owner: p.id, size: rd.Length(), visibleAt: r.now() + r.lagFor(p.id)}
		r.mu.Unlock()
	}
	time.Sleep(after)
	p.mu.Lock()
	p.hasLock = true
	p.lockSaved = r.now()
	p.mu.Unlock()
	return err
}

// lagFor: how long after its Save a lock file of process owner is listed.
func (r *schedRun) lagFor(owner int) time.Duration {
	switch r.spec.lagMode {
	case schedLagUniform:
		return r.spec.lag
	case schedLagRandom, schedLagNonAtomic:
		sum := sha256.Sum256([]byte(fmt.Sprintf("%d/lag/%d", r.spec.seed, owner)))
		return schedSpread(binary.LittleEndian.Uint64(sum[:8]), 0, schedMaxLag)
	}
	return 0
}

func (v *schedView) Remove(ctx context.Context, h backend.Handle) error {
	p, r := v.proc, v.run
	if h.Type == backend.LockFile {
		if err := schedSleep(ctx, p.lockDelay("remove")); err != nil {
			return err
		}
		err := v.Backend.Remove(ctx, h)
		if err == nil {
			r.mu.Lock()
			if l := r.locks[h.Name]; l != nil {
				l.gone, l.removedAt = true, r.now()
			}
			r.mu.Unlock()
		}
		return err
	}
	err := v.Backend.Remove(ctx, h)
	if err == nil && schedTracked(h.Type) {
		h.IsMetadata = false
		r.mu.Lock()
		if f := r.files[h]; f != nil {
			f.gone, f.removed = true, r.now()
		}
		r.mu.Unlock()
	}
	return err
}

func (v *schedView) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	p, r := v.proc, v.run
	switch {
	case h.Type == backend.LockFile:
		owner := -1
		r.mu.Lock()
		if l := r.locks[h.Name]; l != nil {
			owner = l.owner
		}
		r.mu.Unlock()
		if err := schedSleep(ctx, p.lockDelay("load of P"+strconv.Itoa(owner))); err != nil {
			return err
		}
	case p.phase.Load() == schedPhaseRead:
		v.noteRead(h.Type)
		if err := schedSleep(ctx, p.readDelay("load "+h.Type.String()+" "+h.Name)); err != nil {
			return err
		}
	}
	return v.Backend.Load(ctx, h, length, offset, fn)
}

func (v *schedView) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	p, r := v.proc, v.run
	if t != backend.LockFile {
		if p.phase.Load() == schedPhaseRead {
			v.noteRead(t)
			if err := schedSleep(ctx, p.readDelay("list "+t.String())); err != nil {
				return err
			}
		}
		return v.Backend.List(ctx, t, fn)
	}

	// A lock listing issued sooner after our lock file was saved than the
	// settle delay is the gate listing of the tree under test, if it has one.
	// The listings of the lock protocol itself come at or after that delay.
	p.mu.Lock()
	gate := p.hasLock && r.now()-p.lockSaved < schedSettle
	p.mu.Unlock()
	var delay time.Duration
	if gate {
		// its own stream: the lock protocol's listings must get the same
		// delays as in the stock-order run
		delay = p.gateDelay()
	} else {
		delay = p.lockDelay("list")
	}
	begin := r.now()
	if err := schedSleep(ctx, delay); err != nil {
		return err
	}
	end := r.now()

	// What the listing shows is decided here, from the history of the lock
	// files, so that late and non-atomic listings can be modelled.
	type entry struct {
		name string
		size int64
	}
	var listed []entry
	r.mu.Lock()
	for name, l := range r.locks {
		at := end
		if r.spec.lagMode == schedLagNonAtomic {
			sum := sha256.Sum256([]byte(fmt.Sprintf("%d/%d/look/%s/%d", r.spec.seed, p.id, strconv.Itoa(l.owner), begin)))
			at = begin + time.Duration(binary.LittleEndian.Uint64(sum[:8])%uint64(end-begin+1))
		}
		if l.visibleAt <= at && !(l.gone && l.removedAt <= at) {
			listed = append(listed, entry{name, l.size})
		}
	}
	r.mu.Unlock()
	for _, e := range listed {
		if err := fn(backend.FileInfo{Name: e.name, Size: e.size}); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func schedOpenRepository(ctx context.Context, be backend.Backend) (*repository.Repository, error) {
	repo, err := repository.New(be, repository.Options{Compression: repository.CompressionFastest})
	if err != nil {
		return nil, err
	}
	if err := repo.SearchKey(ctx, rtest.TestPassword, 10, ""); err != nil {
		return nil, err
	}
	return repo, nil
}

// schedStoreSnapshot is the model of what a backup writes: the blobs, a tree
// that lists them, and a snapshot.
func schedStoreSnapshot(ctx context.Context, repo *repository.Repository, seed int64, blobs []int, at time.Time) (restic.ID, error) {
	var tree restic.ID
	err := repo.WithBlobUploader(ctx, func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		writer := data.NewTreeWriter(up)
		for i, b := range blobs {
			content := schedBlob(seed, b)
			id, _, _, err := up.SaveBlob(ctx, restic.DataBlob, content, restic.ID{}, false)
			if err != nil {
				return err
			}
			err = writer.AddNode(&data.Node{
				Name:    fmt.Sprintf("file-%02d", i),
				Type:    data.NodeTypeFile,
				Mode:    0o644,
				Size:    uint64(len(content)),
				Content: restic.IDs{id},
			})
			if err != nil {
				return err
			}
		}
		var err error
		tree, err = writer.Finalize(ctx)
		return err
	})
	if err != nil {
		return restic.ID{}, err
	}
	sn, err := data.NewSnapshot([]string{"/model"}, nil, "model", at)
	if err != nil {
		return restic.ID{}, err
	}
	sn.Tree = &tree
	return data.SaveSnapshot(ctx, repo, sn)
}

func schedLoadSnapshots(ctx context.Context, lister restic.Lister, repo *repository.Repository) (map[restic.ID]*data.Snapshot, error) {
	var mu sync.Mutex
	out := make(map[restic.ID]*data.Snapshot)
	err := data.ForAllSnapshots(ctx, lister, repo, nil, func(id restic.ID, sn *data.Snapshot, err error) error {
		if err != nil {
			return err
		}
		mu.Lock()
		out[id] = sn
		mu.Unlock()
		return nil
	})
	return out, err
}

func schedSnapshotIDs(snapshots map[restic.ID]*data.Snapshot) restic.IDSet {
	ids := restic.NewIDSet()
	for id := range snapshots {
		ids.Insert(id)
	}
	return ids
}

// schedNewestFirst orders snapshots as a command picking "latest" would.
func schedNewestFirst(snapshots map[restic.ID]*data.Snapshot) []restic.ID {
	ids := make([]restic.ID, 0, len(snapshots))
	for id := range snapshots {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := snapshots[ids[i]], snapshots[ids[j]]
		if !a.Time.Equal(b.Time) {
			return a.Time.After(b.Time)
		}
		return a.Tree.String() < b.Tree.String()
	})
	return ids
}

// schedReadTree is the model of a restore: every blob of the snapshot's tree.
func schedReadTree(ctx context.Context, repo *repository.Repository, tree restic.ID) error {
	nodes, err := data.LoadTree(ctx, repo, tree)
	if err != nil {
		return err
	}
	for item := range nodes {
		if item.Error != nil {
			return item.Error
		}
		for _, id := range item.Node.Content {
			if _, err := repo.LoadBlob(ctx, restic.BlobHandle{Type: restic.DataBlob, ID: id}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}

// schedCommand holds what one command keeps between its lock call, its reads
// and its changes: the same variables the command functions keep.
type schedCommand struct {
	p    *schedProc
	repo *repository.Repository

	backupLoad *backupIndexLoad
	indexLoad  *repositoryIndexLoad
	checkLoad  *checkerIndexLoad

	snapshots map[restic.ID]*data.Snapshot
	chkr      *checker.Checker
	checkErrs []error
}

// waitWork returns what the command passes to the lock call.
func (c *schedCommand) waitWork() func(context.Context, *repository.Repository) func(bool) {
	switch c.p.spec.kind {
	case schedBackup:
		return backupIndexWaitWork(&c.backupLoad, true) // cmd_backup.go
	case schedRestore, schedForgetPrune:
		return repositoryIndexWaitWork(&c.indexLoad, true) // cmd_restore.go, cmd_forget.go
	case schedPrune:
		return repositoryIndexWaitWork(&c.indexLoad, false) // cmd_prune.go
	case schedCheck:
		return checkerIndexWaitWork(&c.checkLoad, false, &data.SnapshotFilter{}, nil) // cmd_check.go
	}
	return nil // forget without --prune
}

// read is what the command reads between the lock call and its changes, in
// the command's order.
func (c *schedCommand) read(ctx context.Context) error {
	p, repo := c.p, c.repo
	noProgress := restic.NoopTerminalCounterFactory
	var err error
	switch p.spec.kind {
	case schedBackup:
		// parent snapshot search, then the index
		var lister restic.Lister = repo
		if c.backupLoad != nil {
			lister = c.backupLoad.parentSearchRepo(repo)
		}
		if c.snapshots, err = schedLoadSnapshots(ctx, lister, repo); err != nil {
			return fmt.Errorf("parent snapshot search: %w", err)
		}
		p.usedSnapshots = schedSnapshotIDs(c.snapshots)
		if c.backupLoad != nil {
			err = c.backupLoad.wait(noProgress)
		} else {
			err = repo.LoadIndex(ctx, noProgress)
		}
	case schedRestore, schedForgetPrune:
		if c.snapshots, err = schedLoadSnapshots(ctx, c.indexLoad.snapshotLister(repo), repo); err != nil {
			return fmt.Errorf("finding snapshots: %w", err)
		}
		p.usedSnapshots = schedSnapshotIDs(c.snapshots)
		if c.indexLoad != nil {
			err = c.indexLoad.consume(noProgress)
		} else {
			err = repo.LoadIndex(ctx, noProgress)
		}
	case schedForget:
		if c.snapshots, err = schedLoadSnapshots(ctx, repo, repo); err != nil {
			return fmt.Errorf("finding snapshots: %w", err)
		}
		p.usedSnapshots = schedSnapshotIDs(c.snapshots)
		return nil
	case schedPrune:
		if c.indexLoad != nil {
			err = c.indexLoad.consume(noProgress)
		} else {
			err = repo.LoadIndex(ctx, noProgress)
		}
	case schedCheck:
		if c.checkLoad != nil {
			err = c.checkLoad.loadSnapshots()
			c.chkr = c.checkLoad.checkerForSnapshots()
		} else {
			c.chkr = checker.New(repo, false)
			err = c.chkr.LoadSnapshots(ctx, &data.SnapshotFilter{}, nil)
		}
		if err != nil {
			return fmt.Errorf("loading snapshots: %w", err)
		}
		var errs []error
		if c.checkLoad != nil {
			_, errs = c.checkLoad.consume(noProgress)
		} else {
			_, errs = c.chkr.LoadIndex(ctx, noProgress)
		}
		if len(errs) > 0 {
			err = fmt.Errorf("loading the index: %v", errs)
		}
	}
	if err != nil {
		return fmt.Errorf("loading the index: %w", err)
	}
	p.usedIndex = repo.LoadedIndexIDs()
	return nil
}

func (c *schedCommand) prune(ctx context.Context, ignore restic.IDSet) error {
	opts := PruneOptions{MaxUnused: c.p.spec.maxUnused}
	if err := verifyPruneOptions(&opts); err != nil {
		return err
	}
	return runPruneWithRepoAndIndexLoad(ctx, opts, global.Options{}, c.repo, ignore, restic.NewNoopPrinter(), c.indexLoad)
}

// change is what the command does with what it read.
func (c *schedCommand) change(ctx context.Context) error {
	p, repo := c.p, c.repo
	switch p.spec.kind {
	case schedBackup:
		_, err := schedStoreSnapshot(ctx, repo, p.run.spec.seed, p.spec.blobs, time.Now())
		return err
	case schedRestore:
		newest := schedNewestFirst(c.snapshots)
		if len(newest) == 0 {
			return nil
		}
		return schedReadTree(ctx, repo, *c.snapshots[newest[0]].Tree)
	case schedPrune:
		return c.prune(ctx, restic.NewIDSet())
	case schedForget, schedForgetPrune:
		removed := restic.NewIDSet()
		for i, id := range schedNewestFirst(c.snapshots) {
			if i < p.spec.keep {
				continue
			}
			if err := repo.RemoveUnpacked(ctx, restic.WriteableSnapshotFile, id); err != nil {
				return err
			}
			removed.Insert(id)
		}
		if p.spec.kind == schedForgetPrune && len(removed) > 0 {
			return c.prune(ctx, removed)
		}
		return nil
	case schedCheck:
		errChan := make(chan error)
		if c.checkLoad != nil {
			packs := c.checkLoad.packs
			c.checkLoad.packs = nil
			go c.chkr.PacksWithPreparedSizes(ctx, packs, errChan)
		} else {
			go c.chkr.Packs(ctx, errChan)
		}
		for err := range errChan {
			c.checkErrs = append(c.checkErrs, err)
		}
		errChan = make(chan error)
		go c.chkr.Structure(ctx, restic.NewNoopPrinter().NewCounter("snapshots"), errChan)
		for err := range errChan {
			c.checkErrs = append(c.checkErrs, err)
		}
		if len(c.checkErrs) > 0 {
			return fmt.Errorf("check reported %d problem(s), first: %v", len(c.checkErrs), c.checkErrs[0])
		}
	}
	return nil
}

// discard is the command's deferred clean-up before it unlocks.
func (c *schedCommand) discard() {
	if c.backupLoad != nil {
		c.backupLoad.discard()
	}
	if c.indexLoad != nil && !c.indexLoad.used {
		c.indexLoad.discard()
	}
	if c.checkLoad != nil && !c.checkLoad.used {
		c.checkLoad.discard()
	}
}

func (r *schedRun) runProc(ctx context.Context, p *schedProc) {
	time.Sleep(p.spec.start)
	repo, err := schedOpenRepository(ctx, &schedView{Backend: r.be, run: r, proc: p})
	if err != nil {
		p.outcome, p.detail = "failed", "opening the repository: "+err.Error()
		return
	}
	p.phase.Store(schedPhaseRead)

	c := &schedCommand{p: p, repo: repo}
	var work func(context.Context, *repository.Repository) func(bool)
	if !r.stock {
		work = c.waitWork()
	}
	noRetryMessage := func(string) {}
	logf := func(format string, args ...any) { r.notef("P%d: "+format, append([]any{p.id}, args...)...) }
	var unlock func()
	var lockCtx context.Context
	if work == nil {
		unlock, lockCtx, err = repository.LockRepo(ctx, repo, p.spec.kind.exclusive(), schedRetryLock, noRetryMessage, logf)
	} else {
		// exactly what internalOpenWithLockedWaitWork does
		unlock, lockCtx, err = repository.LockRepoWithWaitWork(ctx, repo, p.spec.kind.exclusive(), schedRetryLock, noRetryMessage, logf, func(lockCtx context.Context) func(bool) {
			return work(lockCtx, repo)
		})
	}
	p.mu.Lock()
	p.done = true
	p.mu.Unlock()
	if err != nil {
		p.outcome, p.detail = "failed", err.Error()
		if repository.IsAlreadyLocked(err) {
			p.outcome = "locked"
		}
		return
	}
	p.locked = true
	p.confirmed = r.now()
	changeAt := p.confirmed + p.spec.work
	unlockAt := changeAt + p.spec.hold

	err = c.read(lockCtx)
	if r.now() > changeAt {
		p.lateRead = true
	}
	r.sleepUntil(changeAt)
	p.phase.Store(schedPhaseWork)
	if err == nil {
		err = c.change(lockCtx)
	}
	p.outcome = "ok"
	if err != nil {
		p.outcome, p.detail = "failed", err.Error()
	}
	r.sleepUntil(unlockAt)
	c.discard()
	unlock()
}

// schedQuiet keeps the set-up helpers from logging once per schedule.
type schedQuiet struct{ testing.TB }

func (schedQuiet) Logf(string, ...any) {}

// schedEmptyRepository returns a backend that holds an initialized repository
// and nothing else.
func schedEmptyRepository(t *testing.T) backend.Backend {
	be := mem.New()
	repository.TestRepositoryWithBackend(schedQuiet{t}, be, 0, repository.Options{})
	return be
}

// schedTemplate builds the repository a schedule starts with.
func schedTemplate(t *testing.T, empty backend.Backend, spec *schedSpec) backend.Backend {
	ctx := context.Background()
	be := schedClone(t, empty)
	at := time.Date(1999, 1, 1, 0, 0, 0, 0, time.UTC)
	store := func(blobs []int, forget bool) {
		repo, err := schedOpenRepository(ctx, be)
		rtest.OK(t, err)
		rtest.OK(t, repo.LoadIndex(ctx, restic.NoopTerminalCounterFactory))
		at = at.Add(time.Hour)
		id, err := schedStoreSnapshot(ctx, repo, spec.seed, blobs, at)
		rtest.OK(t, err)
		if forget {
			rtest.OK(t, repo.RemoveUnpacked(ctx, restic.WriteableSnapshotFile, id))
		}
	}
	for _, blobs := range spec.forgotten {
		store(blobs, true)
	}
	for _, blobs := range spec.live {
		store(blobs, false)
	}
	if spec.staleLock {
		// a lock file whose owner is gone: take a lock, keep its file
		repo, err := schedOpenRepository(ctx, be)
		rtest.OK(t, err)
		unlock, _, err := repository.LockRepo(ctx, repo, false, 0, func(string) {}, func(string, ...any) {})
		rtest.OK(t, err)
		var name string
		var content []byte
		rtest.OK(t, be.List(ctx, backend.LockFile, func(fi backend.FileInfo) error {
			name = fi.Name
			return nil
		}))
		h := backend.Handle{Type: backend.LockFile, Name: name}
		rtest.OK(t, be.Load(ctx, h, 0, 0, func(rd io.Reader) (err error) {
			content, err = io.ReadAll(rd)
			return err
		}))
		unlock()
		rtest.OK(t, be.Save(ctx, h, backend.NewByteReader(content, be.Hasher())))
	}
	return be
}

var schedFileTypes = []backend.FileType{
	backend.ConfigFile, backend.KeyFile, backend.SnapshotFile, backend.IndexFile, backend.PackFile, backend.LockFile,
}

func schedClone(t *testing.T, src backend.Backend) backend.Backend {
	ctx := context.Background()
	dst := mem.New()
	for _, tpe := range schedFileTypes {
		rtest.OK(t, src.List(ctx, tpe, func(fi backend.FileInfo) error {
			h := backend.Handle{Type: tpe, Name: fi.Name}
			var content []byte
			err := src.Load(ctx, h, 0, 0, func(rd io.Reader) (err error) {
				content, err = io.ReadAll(rd)
				return err
			})
			if err != nil {
				return err
			}
			return dst.Save(ctx, h, backend.NewByteReader(content, dst.Hasher()))
		}))
	}
	return dst
}

// execute runs the schedule once. It must be called inside a synctest bubble.
func schedExecute(t *testing.T, spec *schedSpec, template backend.Backend, stock bool) *schedRun {
	ctx := context.Background()
	r := &schedRun{
		spec:  spec,
		stock: stock,
		be:    schedClone(t, template),
		files: make(map[backend.Handle]*schedFile),
		locks: make(map[string]*schedLock),
	}
	for _, tpe := range []backend.FileType{backend.SnapshotFile, backend.IndexFile, backend.PackFile} {
		rtest.OK(t, r.be.List(ctx, tpe, func(fi backend.FileInfo) error {
			r.files[backend.Handle{Type: tpe, Name: fi.Name}] = &schedFile{creator: -1, added: -1}
			return nil
		}))
	}
	rtest.OK(t, r.be.List(ctx, backend.LockFile, func(fi backend.FileInfo) error {
		r.locks[fi.Name] = &schedLock{owner: -1, size: fi.Size, visibleAt: -1}
		return nil
	}))
	for i := range spec.procs {
		r.procs = append(r.procs, &schedProc{id: i, spec: &spec.procs[i], run: r})
	}

	r.t0 = time.Now()
	var wg sync.WaitGroup
	for _, p := range r.procs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r.runProc(ctx, p)
		}()
	}
	wg.Wait()
	synctest.Wait()
	var stacks string
	r.goroutines, stacks = schedOtherGoroutines()
	if r.goroutines != 0 && os.Getenv("RESTIC_R1_SCHEDULE_VERBOSE") != "" {
		r.notef("goroutines after the processes ended: %s", stacks)
	}

	// what is in the repository now, and is all of it readable
	repo, err := schedOpenRepository(ctx, r.be)
	rtest.OK(t, err)
	rtest.OK(t, repo.LoadIndex(ctx, restic.NoopTerminalCounterFactory))
	snapshots, err := schedLoadSnapshots(ctx, repo, repo)
	rtest.OK(t, err)
	for id, sn := range snapshots {
		r.final = append(r.final, sn.Tree.String())
		if err := schedReadTree(ctx, repo, *sn.Tree); err != nil {
			r.broken = append(r.broken, fmt.Sprintf("snapshot %v: %v", id.Str(), err))
		}
	}
	sort.Strings(r.final)
	sort.Strings(r.broken)
	return r
}

// schedOtherGoroutines returns the number of goroutines of the caller's
// synctest bubble besides the caller and the two goroutines every bubble
// consists of, and the stacks of all goroutines. Call it after synctest.Wait:
// what is still there then is blocked for good or asleep.
// runtime.NumGoroutine is of no use for this: it also counts the runtime's
// finalizer goroutine while that runs.
func schedOtherGoroutines() (int, string) {
	buf := make([]byte, 1<<20)
	stacks := string(buf[:runtime.Stack(buf, true)])
	others := 0
	for _, goroutine := range strings.Split(stacks, "\n\n") {
		header, _, _ := strings.Cut(goroutine, "\n")
		if !strings.HasPrefix(header, "goroutine ") || !strings.Contains(header, "synctest bubble") {
			continue // not in the bubble
		}
		if strings.Contains(header, "[running") ||
			strings.Contains(goroutine, "internal/synctest.Run(") ||
			strings.Contains(goroutine, "synctest.testingSynctestTest(") {
			continue
		}
		others++
	}
	return others, stacks
}

func (r *schedRun) conflicts(p *schedProc, other int) bool {
	if other < 0 {
		return true // in the repository at the start: everybody must see it
	}
	return p.spec.kind.exclusive() || r.procs[other].spec.kind.exclusive()
}

// violations applies the in-run part of the oracle: stale and missing.
func (r *schedRun) violations() (stale, missing []string) {
	for _, p := range r.procs {
		if !p.locked {
			continue
		}
		check := func(tpe backend.FileType, used restic.IDSet) {
			if used == nil {
				return
			}
			for id := range used {
				f := r.files[backend.Handle{Type: tpe, Name: id.String()}]
				if f == nil {
					stale = append(stale, fmt.Sprintf("P%d %s uses %s file %s that never existed", p.id, p.spec.kind, tpe, id.Str()))
				} else if f.gone && f.removed <= p.confirmed {
					stale = append(stale, fmt.Sprintf("P%d %s uses %s file %s, removed at %v, before its lock was confirmed at %v",
						p.id, p.spec.kind, tpe, id.Str(), f.removed, p.confirmed))
				}
			}
			for h, f := range r.files {
				if h.Type != tpe || f.added > p.confirmed || (f.gone && f.removed <= p.confirmed) {
					continue
				}
				id, err := restic.ParseID(h.Name)
				if err != nil || used.Has(id) {
					continue
				}
				if !r.conflicts(p, f.creator) && f.added > p.lockSaved {
					continue // stock may or may not see this one either
				}
				missing = append(missing, fmt.Sprintf("P%d %s does not use %s file %s (written by P%d at %v), which existed when its lock was confirmed at %v",
					p.id, p.spec.kind, tpe, id.Str(), f.creator, f.added, p.confirmed))
			}
		}
		check(backend.SnapshotFile, p.usedSnapshots)
		check(backend.IndexFile, p.usedIndex)
	}
	sort.Strings(stale)
	sort.Strings(missing)
	return stale, missing
}

func (r *schedRun) describe() string {
	var b strings.Builder
	for _, p := range r.procs {
		fmt.Fprintf(&b, "\n      P%d %-13s %-6s", p.id, p.spec.kind, p.outcome)
		if p.locked {
			fmt.Fprintf(&b, " lock file saved %v, confirmed %v, read during the wait: %v", p.lockSaved, p.confirmed, p.waitRead)
		}
		if p.detail != "" {
			fmt.Fprintf(&b, " (%s)", p.detail)
		}
	}
	for _, n := range r.notes {
		fmt.Fprintf(&b, "\n      note: %s", n)
	}
	return b.String()
}

type schedTotals struct {
	schedules, twoProc, threeProc int
	confirmed, waitReads          int // processes, in the run under test
	// schedules with at least one violation of the kind, in the run under test
	stale, missing, outcome, final, leak int
	failed                               int // schedules with any of the above
	// the same for the stock-order run: must stay 0
	control int
	// schedules left out of the comparison of outcomes: a read was still
	// running when the command was due to make its changes
	late int
}

func schedEnvInt(t *testing.T, name string, fallback int) int {
	v := os.Getenv(name)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return n
}

func schedEnvLag(t *testing.T) schedLagMode {
	switch v := os.Getenv("RESTIC_R1_SCHEDULE_LAG"); v {
	case "":
		return schedLagNone
	case "uniform":
		return schedLagUniform
	case "random":
		return schedLagRandom
	case "nonatomic":
		return schedLagNonAtomic
	default:
		t.Fatalf("RESTIC_R1_SCHEDULE_LAG: unknown mode %q", v)
		return schedLagNone
	}
}

// runLockWaitSchedules runs count schedules starting at seed and returns the
// totals. Failing schedules are reported with t.Errorf.
func runLockWaitSchedules(t *testing.T, seed int64, count int, lag schedLagMode) schedTotals {
	repository.TestSetLockTimeout(t, schedSettle)
	defer repository.TestSetLockTimeout(t, 0)
	verbose := os.Getenv("RESTIC_R1_SCHEDULE_VERBOSE") != ""

	var totals schedTotals
	reported := 0
	synctest.Test(t, func(t *testing.T) {
		empty := schedEmptyRepository(t)
		// printed here as well: a goroutine that never ends makes the bubble
		// panic when it is left
		defer func() { t.Logf("R1 schedule test, lag mode %d: %v", lag, totals) }()
		for i := range count {
			// two processes for odd seeds, three for even ones, so that a
			// seed alone names its schedule
			procs := 3 - int((seed+int64(i))%2)
			spec := newSchedSpec(seed+int64(i), procs, lag)
			template := schedTemplate(t, empty, spec)
			control := schedExecute(t, spec, template, true)
			tested := schedExecute(t, spec, template, false)

			totals.schedules++
			if procs == 2 {
				totals.twoProc++
			} else {
				totals.threeProc++
			}
			var problems []string
			add := func(counter *int, lines ...string) {
				if len(lines) > 0 {
					*counter++
					problems = append(problems, lines...)
				}
			}

			// the stock-order run must satisfy the oracle itself
			var controlProblems []string
			stale, missing := control.violations()
			controlProblems = append(controlProblems, stale...)
			controlProblems = append(controlProblems, missing...)
			controlProblems = append(controlProblems, control.broken...)
			if control.goroutines != 0 {
				controlProblems = append(controlProblems, fmt.Sprintf("%d goroutine(s) outlived the processes", control.goroutines))
			}
			for _, p := range control.procs {
				if p.waitRead {
					controlProblems = append(controlProblems, fmt.Sprintf("P%d read during its wait in the stock-order run", p.id))
				}
			}
			if len(controlProblems) > 0 {
				totals.control++
				t.Errorf("STOCK-ORDER RUN violates the oracle (test defect or schedule outside the lock protocol)\n    %v\n    %s%s",
					spec, strings.Join(controlProblems, "\n    "), control.describe())
			}

			stale, missing = tested.violations()
			add(&totals.stale, stale...)
			add(&totals.missing, missing...)
			late := false
			for i, p := range tested.procs {
				if p.locked {
					totals.confirmed++
					if p.waitRead {
						totals.waitReads++
					}
				}
				late = late || p.lateRead || control.procs[i].lateRead
			}
			if late {
				totals.late++
			} else {
				var differ []string
				for i, p := range tested.procs {
					if c := control.procs[i]; p.outcome != c.outcome {
						differ = append(differ, fmt.Sprintf("P%d %s ends %q (%s), in stock order %q (%s)", i, p.spec.kind, p.outcome, p.detail, c.outcome, c.detail))
					}
				}
				add(&totals.outcome, differ...)
				var final []string
				final = append(final, tested.broken...)
				if strings.Join(tested.final, ",") != strings.Join(control.final, ",") {
					final = append(final, fmt.Sprintf("snapshots after the schedule differ from the stock-order run: trees %v, stock order %v", tested.final, control.final))
				}
				add(&totals.final, final...)
			}
			if tested.goroutines != 0 {
				add(&totals.leak, fmt.Sprintf("%d goroutine(s) outlived the processes", tested.goroutines))
			}
			if len(problems) > 0 {
				totals.failed++
				reported++
				if verbose || reported <= 5 {
					t.Errorf("schedule fails\n    %v\n    %s\n    run under test:%s\n    stock-order run:%s",
						spec, strings.Join(problems, "\n    "), tested.describe(), control.describe())
				} else {
					t.Fail()
				}
			}
		}
	})
	return totals
}

func (s schedTotals) String() string {
	return fmt.Sprintf("schedules=%d (two processes %d, three %d) confirmed-locks=%d of-which-read-during-wait=%d | "+
		"failing-schedules=%d: stale=%d missing=%d outcome=%d final=%d goroutine-leak=%d | stock-order-run-failures=%d not-compared(late read)=%d",
		s.schedules, s.twoProc, s.threeProc, s.confirmed, s.waitReads,
		s.failed, s.stale, s.missing, s.outcome, s.final, s.leak, s.control, s.late)
}

func TestLockWaitSchedules(t *testing.T) {
	seed := int64(schedEnvInt(t, "RESTIC_R1_SCHEDULE_SEED", 1))
	count := schedEnvInt(t, "RESTIC_R1_SCHEDULES", 500)
	lag := schedEnvLag(t)
	if testing.Short() && os.Getenv("RESTIC_R1_SCHEDULES") == "" {
		count = 100
	}
	totals := runLockWaitSchedules(t, seed, count, lag)
	if totals.waitReads == 0 {
		t.Logf("note: no process read anything during its lock wait in this tree")
	}
}
