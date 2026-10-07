# R1 repair report: reads taken during the 200 ms lock wait

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Date 2026-10-06. Workstation only (Windows 10, go1.27.0). API calls: 0. Spend: $0. No box access.
Working copy: `final-candidate\work\lockwait`. Branches, both cut from `base` (3b99f5a):
`r1-validate` (design B, 5 commits, tip 7985159) and `r1-gate` (design A, 6 commits, tip 6e4a1b3).
Patches: `repairs\R1-validate\` (5 files), `repairs\R1-gate\` (6 files). Step log: `repairs\R1-PROGRESS.md`.
Logs of every run quoted here: `repairs\R1-scratch\logs\`.

## 0. Result

| | base (defect) | A, gate (`r1-gate`) | B, validate (`r1-validate`) |
|---|---|---|---|
| the reviewers' witnesses (3 command level, 2 API level, 1 stock-order control) | 5 fail, control passes | 6 pass | 6 pass |
| randomized schedules failing, of 3000 | 163 | 0 | 0 |
| the same on a backend that lists new lock files up to 190 ms late: fixed lag / lag per file / lag per file and listings that are not snapshots (3000 each) | 223 / 183 / 188 | 0 / 0 / **1** | 0 / 0 / 0 |
| extra backend requests per process | 0 | +1 list of lock files, inside the wait | +1 list of snapshots and +1 list of index files, after confirmation (prune: +1; forget without `--prune`: 0) |
| extra wall time per process at 30 ms per request, measured | 0 | -1 to +2 ms (noise) | +29 to +31 ms; +61 to +63 ms for `ls` and `check` |
| what the equivalence with stock rests on | (none) | an argument about every other process and about how the backend lists lock files | file names being content hashes |

- **B is sound.** Its argument does not involve the lock protocol or other processes. One caveat, not modelled in
  the tests (B-2 in section 2.1): a backend whose listing still shows an index file after its removal returned;
  stock fails with an error there, B continues with the content it holds.
- **A as sketched in the brief is not airtight.** It breaks on a backend that lists a new lock file some time after
  `Save` returned, where stock is still safe: 124, 86 and 92 of 3000 schedules fail in the three lag modes (schedule
  in section 2.2). I amended it: the gate listing must also show our OWN lock. As built, A is sound on backends
  whose listings are consistent, and on late listings if a listing is a snapshot. One hole is left and the test
  found it once in 3000: a backend that lists late AND whose listing is not a snapshot.
- **Recommendation: B** (section 10). A is cheaper and defensible on consistent backends. B costs one listing on the
  critical path of every process, two for `ls` and `check`.

## 1. What each design changes

Counts are `git diff --numstat base <branch>`, files that are not tests.

### B, validate: 12 files, +481 -161

`internal/repository/lock_file.go` is **unchanged**. The lock code, the sleep and both lock checks are the
incumbent's, which differs from stock only by the hook that starts the wait work.

| file | +/- | what |
|---|---|---|
| `cmd/restic/wait_index_load.go` | +343 -57 | The comparison after confirmation: `startLockWaitValidation`, `validateLockWaitReads`, `snapshotsRemoved`. `snapshotLister` returns the listing taken AFTER confirmation. `consume` waits for the verdict: keep / extend (load the added index files) / reread (discard, read in stock order). check: any difference rereads, with a new checker |
| `cmd/restic/backup_index_load.go` | +18 -51 | backup's copy of the load is gone: `backupIndexLoad` is an alias of `repositoryIndexLoad` |
| `cmd/restic/cmd_backup.go` `cmd_restore.go` `cmd_forget.go` `cmd_prune.go` `cmd_check.go` `cmd_ls.go` | +22 -43 | call sites use named constructors (`repositoryIndexWaitWork`, `backupIndexWaitWork`, `checkerIndexWaitWork`); check takes its checker after `loadSnapshots`; ls drops its prepared snapshot file when everything was reread |
| `cmd/restic/lock.go`, `check_structure_overlap.go` | +3 -10 | R10, section 6 |
| NEW `internal/repository/index_loaded_ids.go` | 77 lines | `Repository.LoadedIndexIDs()` and `Repository.LoadAddedIndexFiles()` |
| NEW `internal/checker/snapshot_list.go` | 18 lines | `Checker.LoadSnapshotsFromList`: `LoadSnapshots` with a listing the caller already has |

### A, gate: 12 files, +225 -60

| file | +/- | what |
|---|---|---|
| `internal/repository/lock_file.go` | +102 -8 | `gatedWaitWork`: once the own lock file is written, a goroutine lists the lock files once (nothing is loaded). Wait work starts only if the listing shows our own lock and no other entry. The sleep and both lock checks are untouched and run beside it. A gate listing still running when the second check is done is cancelled: no wait work. The comment carries the argument and its limit |
| `cmd/restic/wait_index_load.go`, `backup_index_load.go` | +94 -2 | the same named constructors as in B; after a failed snapshot listing during the wait the index is not listed |
| `cmd/restic/cmd_*.go` (6 files) | +14 -40 | call sites |
| `cmd/restic/lock.go`, `check_structure_overlap.go` | +3 -10 | R10 |
| NEW `internal/repository/index_loaded_ids.go` | 12 lines | `LoadedIndexIDs()`; only the schedule test calls it in this branch |

Found while building, fixed in both branches, all inside the R1 code:

- **Stale pointer after a refused attempt.** restore / ls / check / forget / prune kept a pointer to the discarded
  load of a refused lock attempt. Harmless in base, where every later attempt that succeeds starts a new load. With
  a gate it is a bug: an attempt whose gate stays closed starts nothing and the command would use the old pointer
  (an old snapshot listing, an empty index). The constructors clear it; `lock_wait_duration_test.go` checks it.
- **Order on one error path.** If the snapshot listing of the wait failed, base loaded the index anyway and the
  command listed the snapshots afterwards (index before snapshots; M3 in REVIEW-readside). Now the index is not
  listed after a failed snapshot listing.
- **`cmd_backup.go:543`**, the third debug switch, is removed with the repair because those lines were rewritten.

## 2. Soundness

A lock attempt is: L1 (list locks), W (save own lock file), sleep 200 ms, L2 (list locks). "Wait reads" are what the
incumbent reads between W and L2.

### 2.1 B, validate

1. Nothing read during the wait reaches a command unchecked. `snapshotLister` and `consume` block on the comparison,
   which starts only after L2 has passed.
2. After L2 the code lists the snapshot files, then the index files: the two listings stock makes at that point, in
   stock's order.
3. Commands use the snapshot listing of step 2, never the one of the wait.
4. The index of the wait is kept only if every index file it was built from is in the listing of step 2. An index
   file is named by the SHA-256 of its content and `LoadRaw` checked that hash when the file was read, so the index
   in memory is what loading those names now would give.
5. Index files that were added are loaded, and only those (`LoadAddedIndexFiles`: stock's `ForAllIndexes`,
   `Insert`, `MergeFinalIndexes` over the added files). A second `LoadIndex` gives the same index but reads every
   index file again and only skips inserting the known ones; the verdict test showed that (5 loads where 1 was
   expected) and it would have thrown the gain away whenever another backup runs beside us.
6. A removed snapshot or index file, a failed read during the wait, or a failed listing in step 2: everything from
   the wait is discarded and read again by stock's code in stock's order.
7. So after `consume` a command holds (snapshot listing at t1, content of the index files listed at t2 > t1), both
   times after confirmation: what a stock process with the same confirmation holds.

No step mentions another process. Slow or retried lock saves, three or more processes, stale locks, lock refresh,
`--no-lock` readers, tools that do not lock, lock files that become visible early or late: none of them enters the
argument, because the comparison is with what stock would LIST after the same confirmation.

Assumptions:

- **B-1** A snapshot or index file name determines its content (repository format; checked on load).
- **B-2** A file that the listing of step 2 returns and that we already hold is not fetched again; stock fetches it.
  The two differ only if a listing returns the name of a file that can no longer be loaded, that is a listing that
  still shows a file after its `Remove` returned. Stock then fails with an error; B continues with the content it
  holds. If listings reflect changes in the order they were made this cannot coincide with a passed L2, because the
  other process removes its lock after it removed the file. Not modelled in the tests. It is the one place where B
  is not literally stock.
- **B-3** check rereads on any difference, also an added index file, and builds a new checker then: a checker's
  blob set is bound to the index object it was created on (found while writing the reread path).
- An index file loaded by step 5 has no note for prune's "keep unread" shortcut, so a prune that started this way
  rewrites its indexes through the ordinary loop. Correct, slower, and only when another process added an index
  file during our wait.

### 2.2 A, gate

Claim: if the gate listing shows our own lock and nothing else, reads taken after it are as good as reads taken
after L2.

Proof, for one other process Q whose lock conflicts with ours and that changes the repository. Q changes it only
between the end of its own L2 and the start of its unlock, and its lock file is visible during all of that time.
Q's L2 did not see our lock, so one of two holds:

- our lock was already gone when Q looked: Q works after our whole run, as in stock;
- our lock was not visible yet when Q looked. Q's lock file was written before Q's L2, hence visible before ours,
  hence before our gate listing started (the gate starts after our `Save` returned). The gate did not show it, so Q
  had started to unlock by then: every change of Q precedes every read of our wait work.

So no conflicting change falls between a gated read and our L2, the only interval where such a read differs from
stock's. The argument is per pair, so the number of processes does not matter. Processes whose lock does not
conflict with ours only add files, and snapshots are still listed before the index.

What the brief asked me to consider:

| case | result for A |
|---|---|
| exclusive taker against non-exclusive holder, both directions | covered by the proof (the conflict test is symmetric); in the schedules both roles take every kind of command |
| three processes | the proof is pairwise; 1500 of every 3000 schedules have three |
| slow or retried lock save, either side; file visible BEFORE `Save` returns | harmless: the proof only uses "visible no later than `Save` returns" and "the gate starts after our `Save` returned". In the schedules the aimed process always saves its lock slowly (up to seconds) and 40% of the others do, a quarter of all lock files are visible before the call returns, and a refused first attempt is followed by a second one |
| stale lock | any listed lock closes the gate, also a stale one and a non-exclusive one that would not block stock. Sound; the process then runs in stock order and loses the overlap until `restic unlock`. One schedule in twelve starts with a stale lock |
| `--no-lock` | no lock call, so no wait work in either design; unchanged |
| lock refresh | the holder writes the new lock before it removes the old one. A listing that is not a snapshot can miss both; stock's L2 has the same hole. For A's reads to go stale the holder must in addition finish all its work within 200 ms of that miss. Not exercised (refresh every 5 minutes) |
| file visible AFTER `Save` returns | **the sketch breaks**, below |

**The schedule that breaks the sketch.** Take a backend that lists a new lock file up to d < 200 ms after its `Save`
returned. Stock is safe for every such d: two conflicting processes cannot both miss each other at their L2. The
gate of the sketch has no settle time of its own. Seed 45 of the fixed-lag run, d = 138 ms, gate without the
own-lock rule:

```
118.6 ms  Q (prune) saves its lock             listed from 256.2 ms
243.2 ms  P (prune) saves its lock             listed from 380.8 ms
243 ms    P's gate lists the locks: nothing    wait work starts, P reads the index
319 ms    Q's L2: P's lock is not listed yet   Q confirmed at 323.8 ms
373.3 ms  Q removes three index files and writes one; unlocks about 30 ms later
443 ms    P's L2: no lock                      P confirmed at 445.7 ms, holding the index of 243 ms
          P fails: "failed to rewrite indexes: LoadUnpacked(537fa31a): not found". In stock order both succeed.
```

With a backup as P this is the silent loss of the BLOCKER again. On 3000 schedules, half of them aimed at this
window, the gate without the own-lock rule fails 124 (fixed lag), 86 (lag drawn per file) and 92 (per file, and
listings that are not snapshots); the stock-order run fails none.

**Amendment, built into `r1-gate`:** the gate listing must show our own lock. If it does not, the backend lists
late and the listing cannot be trusted to show Q's lock either. Why that restores stock's tolerance: Q's L2 did not
see our lock, so our lock became visible more than 200 ms after Q's lock file was saved. A gate listing that shows
our lock was therefore taken more than 200 ms after Q's save, when Q's lock is listed under stock's own assumption.
Result: 0 failures with fixed lag and with lag per file; on such a backend the gate is almost always closed (wait
work ran for 261 and 272 of about 4150 confirmed locks).

**What is left.** If the backend lists late AND a listing is not a snapshot, the gate listing can look at Q's lock
before that is listed and at ours after ours is. 1 of 3000 schedules in that mode fails (seed 1337): a backup saves
its lock at 100 ms and is confirmed at 302 ms; a check saves its lock at 227 ms, its gate opens, it reads the index;
the backup writes an index file at 342 ms and unlocks; the check is confirmed at 433 ms and reports "pack not
referenced in any index" where stock passes. B passes all 3000.

Assumptions of A as built:

- **A-1** Either a lock file is listed as soon as its `Save` has returned (then nothing else is needed), or it is
  listed within 200 ms (stock's assumption) AND a listing of the lock files is a consistent snapshot.
- **A-2** A process that changes the repository holds a visible lock from before its own L2 until after its last
  change, and a listing shows every lock that is held throughout the listing. Stock assumes the same for its L2.
  Where it fails (a lock removed with `restic unlock` while its owner still writes, a miss during refresh, a tool
  that does not lock), stock is exposed to changes made after its L2; A in addition to changes made in the 200 ms
  before it.
- **A-3** The proof is a statement about other processes' behaviour. If it is wrong somewhere, A fails silently.

## 3. Randomized-schedule test

`cmd/restic/lock_wait_schedule_test.go`, `TestLockWaitSchedules`. The same file is in both branches.

- Two or three processes per schedule (odd seeds two, even seeds three) from: backup, restore, prune, forget,
  forget --prune, check. In-memory backend. Every backend operation of every process is delayed by an amount that
  is a function of (seed, process, operation): a seed reproduces its schedule.
- Virtual time (`testing/synctest`): the real lock code sleeps its real 200 ms at no wall-clock cost. 3000
  schedules take about 85 s.
- **Real code**: `repository.LockRepo` / `LockRepoWithWaitWork`; the wait work of each command and what the command
  does with it (the constructors and methods the command files call); the repository (index, packs,
  deduplication); prune (`runPruneWithRepoAndIndexLoad`); the checker. **Model**: the rest of a command. backup
  stores a fixed list of blobs, a tree and a snapshot; restore reads every blob of the newest snapshot; forget
  removes all but the newest N snapshots.
- 70% of the schedules are aimed at the window of the defect: one process writes its lock file slowly, a
  conflicting one is confirmed without seeing it, and the slow lock file lands around the time the other one changes
  the repository. The rest is unaimed. In the lag modes half of the schedules are aimed at the second window
  instead: two lock files written within the lag of each other, one process finishing within tens of milliseconds.
- **Stock-order control.** Every schedule is run twice on identical repositories: in stock order (no wait work) and
  with the tree's wait work. Both runs see the same lock timeline: lock operations get the same delays, a process
  changes the repository at a fixed time after its confirmation and unlocks at a fixed time after that.
- **Oracle**, for every process whose lock was confirmed: *stale* (it uses a snapshot or index file removed before
  its confirmation); *missing* (it does not use one that existed at its confirmation, unless a process whose lock
  does not conflict wrote it after our lock file: stock may or may not see such a file); *outcome* (the command
  ends differently than in the stock-order run: ok / failed / locked); *final* (afterwards a snapshot is not fully
  readable, or the snapshots differ from the stock-order run's); *goroutines* left after the processes. The
  stock-order run is held to the same oracle and must produce nothing.

Failing schedules of 3000 per cell (1500 with two processes, 1500 with three; 4043 to 4204 confirmed locks per
run). Lag modes: lock files listed up to 190 ms after `Save`.

| tree | listed at once | fixed lag | lag per file | lag per file, listing not a snapshot |
|---|---|---|---|---|
| stock-order run, inside every run below | 0 | 0 | 0 | 0 |
| base | 163 (stale 44, missing 68, outcome 147, final 54) | 223 | 183 | 188 |
| gate as sketched: own lock not required (scratch variant) | as A | 124 | 86 | 92 |
| **A, gate as built** | **0** | **0** | **0** | **1** |
| **B, validate** | **0** | **0** | **0** | **0** |

Which trees: A and B listed at once, and all of B's lag cells, are the final commits. A's lag cells were taken on
the tree just before its last change, which touched comments and test files only. base is the scratch branch
`r1-basecheck`. A further 6000 schedules (seeds 3001 to 9000, listed at once) on each final commit: A 0 failing,
B 0 failing, stock-order run 0.

How often the wait work ran, listed at once: B read during the wait for 3402 of 4043 confirmed locks, A for 2444
(the gate was closed for the others: these schedules are full of concurrent processes).

Does the test see what it should? Besides base:

- B with the comparison forced to "keep": 31 of 1000 schedules fail.
- B with the command given the snapshot listing of the wait: 27 of 1000 fail.
- Two defects of the test itself were caught by its own control and fixed before any number above was taken: with
  `--retry-lock 0` the lock code picks at random between "one last attempt" and "another round" after a refusal
  (two timers that are both due), which made outcomes depend on chance, so the processes use 1 ns; and the gate
  listing had been drawing from the delay stream of the lock protocol.

Not covered by the schedules: lock refresh, `--no-lock`, `ls` (the load of restore plus one prepared snapshot
file), a listing that still shows removed files (B-2), real backends.

## 4. Requests per process, and what they cost

`cmd/restic/lock_wait_requests_test.go`, `TestLockWaitRequestCounts`: one command each on the in-memory backend,
counted below the retry layer, no cache (every load is a request). "stock" is the real stock tree (a scratch copy
of `trees\stock` with this one test file added). "stock order" is the incumbent with a settle delay of zero, where
the lock code starts no wait work.

Total backend operations of the process:

| | backup | restore | ls | check | forget | forget --prune | prune |
|---|---|---|---|---|---|---|---|
| stock (real stock tree) | 18 | 20 | 16 | 23 | 12 | 30 | 22 |
| stock order (incumbent, no wait work) | 18 | 18 | 16 | 23 | 12 | 30 | 22 |
| base | 18 | 18 | 16 | 23 | 12 | 30 | 22 |
| A, gate | 19 | 19 | 17 | 24 | 12 | 31 | 23 |
| B, validate | 20 | 20 | 18 | 25 | 12 | 32 | 23 |

By operation and file type, stock order. `data` is pack files.

| operation | backup | restore | ls | check | forget | forget --prune | prune |
|---|---|---|---|---|---|---|---|
| List data | 0 | 0 | 0 | 1 | 0 | 1 | 1 |
| List index | 1 | 1 | 1 | 1 | 0 | 1 | 1 |
| List key | 1 | 1 | 1 | 1 | 1 | 1 | 1 |
| List lock | 2 | 2 | 2 | 2 | 2 | 2 | 2 |
| List snapshot | 1 | 1 | 1 | 1 | 1 | 2 | 1 |
| Load config | 1 | 1 | 1 | 1 | 1 | 1 | 1 |
| Load data | 2 | 4 | 2 | 8 | 0 | 2 | 2 |
| Load index | 1 | 2 | 2 | 2 | 0 | 6 | 4 |
| Load key | 1 | 1 | 1 | 1 | 1 | 1 | 1 |
| Load snapshot | 1 | 2 | 2 | 2 | 2 | 3 | 1 |
| Remove data | 0 | 0 | 0 | 0 | 0 | 2 | 1 |
| Remove index | 0 | 0 | 0 | 0 | 0 | 3 | 2 |
| Remove lock | 1 | 1 | 1 | 1 | 1 | 1 | 1 |
| Remove snapshot | 0 | 0 | 0 | 0 | 1 | 1 | 0 |
| Save data | 2 | 0 | 0 | 0 | 0 | 0 | 0 |
| Save index | 1 | 0 | 0 | 0 | 0 | 1 | 1 |
| Save lock | 1 | 1 | 1 | 1 | 1 | 1 | 1 |
| Save snapshot | 1 | 0 | 0 | 0 | 0 | 0 | 0 |
| Stat config | 1 | 1 | 1 | 1 | 1 | 1 | 1 |

Differences from that table (everything not listed is identical; Save and Remove never differ, which the test
asserts):

- **stock (real stock tree)**: restore `Load data` 6 instead of 4 (the incumbent's pack-range coalescing, not R1).
- **base**: none. Base moves reads into the wait and adds none.
- **A, gate**: `List lock` 2 -> 3 for backup, restore, ls, check, forget --prune, prune. forget without `--prune`
  has no wait work: unchanged.
- **B, validate**: `List snapshot` +1 and `List index` +1 for backup, restore, ls, check, forget --prune;
  `List index` +1 for prune (it prepares no snapshot listing); forget unchanged.

Wall time with 30 ms added to every request (`RESTIC_R1_REQUEST_LATENCY=30ms`), one run each, milliseconds. The
stock-order figure includes the 200 ms settle delay.

| | backup | restore | ls | check | forget | forget --prune | prune |
|---|---|---|---|---|---|---|---|
| stock order, incl. the 200 ms delay | 699-703 | 630-632 | 595-597 | 658-659 | 503-504 | 870-873 | 780-783 |
| base | 607 | 538 | 503 | 565 | 504 | 778 | 717 |
| A, gate | 607 | 540 | 502 | 565 | 505 | 777 | 718 |
| B, validate | 636 | 567 | 564 | 628 | 505 | 809 | 748 |
| A minus base | +0 | +2 | -1 | +0 | +1 | -1 | +1 |
| B minus base | +29 | +29 | +61 | +63 | +1 | +31 | +31 |

A and B are the final commits; an earlier run of the same measurement gave the same picture within 4 ms.

- A: one more request, issued inside the 200 ms wait. The wait work starts one request later and still finishes
  inside the wait. Wall time equals base within 2 ms.
- B: two more requests after confirmation. The snapshot listing is on the critical path. The index listing hides
  behind the loading of snapshot files in backup, restore, forget --prune and prune (their +29 to +31 ms is one
  listing); in `ls` and `check` nothing runs beside it (+61 to +63 ms).
- For 28 processes per pass at 30 ms per request: A about 0 s, B about 0.8 to 1.8 s per pass over base, depending
  on the mix of commands. Of what base gains over stock order at this latency (about 63 ms per process for prune,
  92 to 96 ms for the others), A keeps all; B keeps about two thirds for backup, restore and forget --prune, half
  for prune and a third for ls and check. On a local repository B's two listings cost microseconds and the gain of
  loading the index during the wait is kept.

## 5. The settle delay, goroutines, and the mechanisms of each design

`cmd/restic/lock_wait_duration_test.go`, `TestLockWaitIsNeverShortened`: 4 commands x 11 cases, virtual time.
Cases: wait work finishes early / is still running at confirmation / fails / hangs; the command returns early;
cancellation after confirmation; cancellation during the wait (also with hanging wait work); a conflicting lock
early in the wait and 1 ms before its end (also with hanging wait work).

Checked in every case: a listing of the lock files starts exactly 200 ms after the lock file was saved and before
the lock call returns; the lock call never returns earlier; the wait work never postpones the confirmation; a
conflicting lock that appears at 199 ms refuses the lock; after unlock no goroutine of the command is left and no
lock file of it remains; a refused attempt leaves no prepared state in the command.

- A: 44 of 44 pass. B: 44 of 44 pass.
- Sensitivity: with the sleep shortened by 1 ms (scratch), 44 of 44 fail.
- base: 29 pass; 15 fail, all on "a refused lock attempt left prepared state behind" (the stale pointer of
  section 1), none on the delay or on goroutines.
- The schedule test counts the goroutines left after every run as well: none in any run of A or B.

A only, `internal/repository/lock_file_gate_test.go`, `TestNewLockGate`: 7 cases at the lock API (no other lock;
slow gate listing; a non-exclusive lock of another process; an empty lock file; own lock not listed; gate listing
fails; gate listing slower than the settle delay). The gate opens in the first two only, and in all seven the lock
is granted after exactly 200 ms.

B only, `cmd/restic/wait_index_load_validate_test.go`, `TestLockWaitValidation`: 5 cases, backend operations after
confirmation counted. Nothing changed: 1 snapshot listing, 1 index listing, 0 loads. A backup beside us added an
index file and a snapshot: the same two listings and 1 index load. Index files replaced by a prune, a snapshot
removed by a forget, a failed snapshot listing during the wait: everything read again, snapshots before the index.
In all five the command holds exactly what a fresh repository handle reads.

## 6. Hygiene in my files (REPAIRS.md R10, R11)

**R10, debug switches: removed.** `cmd/restic/lock.go:19-21` dropped the wait work, `cmd_check.go:332` the
overlapped structure pass and `cmd_backup.go:543` the wait work again whenever `DEBUG_LOG` was set. The stated
reason was the order of debug lines. A user who turns the debug log on to look into the overlapped path has to run
that path. Debug lines of the wait work may now interleave with those of the lock code. Also
`check_structure_overlap.go` and `backup_progress_publication_test.go` are gofmt-formatted (no other change);
`gofmt -l cmd/restic` is empty on both branches. The other R10 items are in files I do not own.

**R11, the stock integration tests with the production wait:**

```
RESTIC_TEST_LOCK_WAIT=200ms go test ./cmd/restic
```

- NEW `cmd/restic/lock_wait_env_test.go`: `testLockWait` reads the variable; without it the delay is 0, as in stock.
- `cmd_init_integration_test.go`: `testRunInit` sets that value instead of the constant 0 (one line).
- `integration_test.go`: `listOnceBackend` got a mutex (the wait work lists from another goroutine: the "concurrent
  map writes" of race-fix/REPORT.md). It counts the listings made during the lock wait (between the save of the
  own lock file and the next lock listing) on their own: once per type, snapshots before index. From then on the
  stock rule applies unchanged. In A the gate listing ends that window before any wait work starts, so A's commands
  are held to the stock rule throughout: a command that listed a type during its wait must not list it again. In B
  the listings of the wait and the listings after confirmation are each held to the rule.
- Both edited files are stock test files. A judge that overlays stock's test files replaces them and runs with a
  delay of zero exactly as before; none of the new files depends on the edits.

## 7. Test results on this machine

Environment. The tar fixture of the integration tests holds a symlink. Git's `tar` cannot create it in a
non-elevated shell and 10 tests then fail in every tree, stock included. All `cmd/restic` runs below set
`MSYS=winsymlinks:lnk` (tar writes a shortcut file instead), which lets them run. Two tests fail here in every tree
that has them, with "Access is denied" when restore sets metadata on `C:\Users`: `TestCopy` (stock test; fails in
the stock tree too) and `TestSaveVerifyRealLocalPartialUpload` (incumbent's test; fails on base too).

Counts are top-level tests. Commands are run in the root of the branch's tree; `go` is
`go.exe`; every `go test` had `-p 2 -count=1`.

| run | command | B, validate | A, gate | base | stock tree |
|---|---|---|---|---|---|
| reviewers' witnesses | `go test -run TestWitnessLock -v ./cmd/restic` | 6 pass | 6 pass | 1 pass (the stock-order control), 5 fail | the 3 command-level ones pass (review log) |
| schedules, seeds 1 to 3000 | `RESTIC_R1_SCHEDULES=3000 go test -run TestLockWaitSchedules -v ./cmd/restic` | 0 of 3000 failing | 0 of 3000 | 163 of 3000 | - |
| schedules, seeds 3001 to 9000 | the same with `RESTIC_R1_SCHEDULE_SEED=3001 RESTIC_R1_SCHEDULES=6000` | 0 of 6000 | 0 of 6000 | - | - |
| schedules, late listings | the same with `RESTIC_R1_SCHEDULE_LAG=uniform`, `random`, `nonatomic`, 3000 each | 0 / 0 / 0 | 0 / 0 / 1 | 223 / 183 / 188 | - |
| settle delay, goroutines | `go test -run TestLockWaitIsNeverShortened ./cmd/restic` | 44 of 44 cases | 44 of 44 | 29 of 44 | - |
| verdicts | `go test -run TestLockWaitValidation -v ./cmd/restic` | 5 of 5 cases | (not in this branch) | - | - |
| gate | `go test -run TestNewLockGate -v ./internal/repository` | (not in this branch) | 7 of 7 cases | - | - |
| requests | `go test -run TestLockWaitRequestCounts -v ./cmd/restic`, and with `RESTIC_R1_REQUEST_LATENCY=30ms` | pass | pass | pass | pass |
| `cmd/restic`, whole package | `MSYS=winsymlinks:lnk go test -v ./cmd/restic` | 145 pass, 2 fail, 1 skip | 144 pass, 2 fail, 1 skip | 137 pass, 9 fail, 1 skip | 120 pass, 1 fail, 1 skip |
| the same with the production wait | `MSYS=winsymlinks:lnk RESTIC_TEST_LOCK_WAIT=200ms go test -v ./cmd/restic` | 145 pass, 2 fail, 1 skip | 144 pass, 2 fail, 1 skip | - | - |
| `internal/repository`, `internal/checker` | `go test -v ./internal/repository ./internal/checker` | 216 pass, 0 fail | 217 pass, 0 fail | 216 pass, 0 fail | 77 pass, 0 fail |
| gofmt | `gofmt -l cmd/restic` and the files changed outside it | empty | empty | 2 files in `cmd/restic` | - |
| vet | `go vet ./cmd/restic ./internal/repository ./internal/checker`, and with `GOOS=linux GOARCH=amd64` | clean | clean | - | - |
| builds | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build ./cmd/restic`, the same with `GOARCH=386`, and native | ok | ok | - | - |
| patches | `git am repairs\R1-<design>\*.patch` on `base` | tree equals the branch | tree equals the branch | - | - |

- The 2 failures of B and A in `cmd/restic`, at both delays, are `TestCopy` and
  `TestSaveVerifyRealLocalPartialUpload`: the environment failures named above. Every test that passes with a delay
  of 0 passes with 200 ms. The skip is `TestBackupVSS`.
- base's 9 failures are those 2 and the 7 R1 tests (5 witnesses, the schedules, and the settle-delay test on the
  stale pointer). The base column is the scratch branch `r1-basecheck`.
- The stock column of `cmd/restic` has fewer tests because the incumbent added its own; `TestCopy` fails there too.
- B has one test more than A in `cmd/restic` (`TestLockWaitValidation`), A one more in `internal/repository`
  (`TestNewLockGate`).
- The whole `cmd/restic` package takes about 90 s here on both branches (stock tree: 47 s).

## 8. Not run here; owed on the Linux box

1. `go test -race -run 'TestLockWait|TestWitnessLock|Wait|Prepared|Confirmation' ./cmd/restic` and
   `go test -race -run Lock ./internal/repository`, both branches. No cgo toolchain here.
2. `go test ./cmd/restic` and `RESTIC_TEST_LOCK_WAIT=200ms go test ./cmd/restic` with real symlinks.
3. The judge's `compatibility_tests()` (stock test files overlaid) on both branches. The new test files add about
   35 s to the package (schedules 15 s at the default of 500, witnesses 17 s).
4. Real binaries at the real 200 ms: REPAIRS.md box experiment 1, REVIEW-storage NEEDS-RUN 1, REVIEW-backup N4,
   REVIEW-readside NEEDS-RUN 3 (a proxy that holds the lock upload). Expected: base broken, both branches clean.
   And `~/race-dev/repro.sh` (the S015 race) on both branches.
5. The price on the real benchmarks: remote at 30 ms per request and the local big-index workload, base against A
   against B.
6. `GOARCH=386 go test -run 'TestLockWait|TestWitnessLock' ./cmd/restic` (cross-built here, not run).
7. Neither repair has OS-specific code; there is no Linux-only path in them to run.

## 9. Files touched outside my ownership

B, `r1-validate`: two NEW files, no existing file edited.

- `internal/repository/index_loaded_ids.go`, 77 lines. `LoadedIndexIDs` returns `r.idx.IDs()`.
  `LoadAddedIndexFiles` loads a given set of index files into the loaded index with stock's `ForAllIndexes`,
  `Insert` and `MergeFinalIndexes`, then repeats the two checks `loadIndexWithCallback` makes after a load (the
  repository-version-1 check and `prepareCache`): 12 lines duplicated from `repository.go`, which I did not edit.
- `internal/checker/snapshot_list.go`, 18 lines. `LoadSnapshotsFromList`, so that check does not list the
  snapshots a third time.

A, `r1-gate`: two NEW files, three test files edited.

- `internal/repository/index_loaded_ids.go`, 12 lines, `LoadedIndexIDs` only; called by the schedule test alone.
- `internal/repository/lock_file_gate_test.go`, new test of the gate.
- `internal/repository/lock_second_scan_helper_test.go` (+18 -3), `lock_file_test.go` (+3 -2),
  `lock_file_wait_work_test.go` (+3 -2): the helper that paused "the second lock listing" pauses the third when
  the lock is taken with wait work, and the two tests that use it that way run with a 200 ms delay so that the gate
  listing reliably comes first.

In A six existing tests of `cmd/restic` (my area) needed the same adaptation, one of them hung without it:
`check_pack_sizes_wait_test.go`, `check_completing_repair_test.go`, `check_pack_sizes_lifetime_test.go`,
`check_ordinary_wait_test.go`, `wait_index_load_integration_test.go`, `snapshot_metadata_integration_test.go`.
In B no existing test changed apart from the two stock helpers of R11.

Scratch, not deliverables: branch `r1-basecheck` in the working copy (base plus the R1 tests and a test-only shim, to
see them fail); `repairs\R1-scratch\` (2.4 MB: the logs of every run, and the scripts that ran the lag modes, the
final verification and the tables). The stock column came from a copy of `trees\stock` with
`lock_wait_requests_test.go` added; the copy and the extra worktrees are deleted, `trees\` was never written to.

## 10. Recommendation: B

- B's equivalence with stock is local. It compares what it holds with what stock would list at the same moment and
  needs no statement about other processes, about how a backend lists lock files, or about the lock protocol beyond
  what stock itself relies on after confirmation. That is the property to want for the one finding that loses data
  silently.
- A's argument is short and holds on backends with consistent listings. But the sketch was wrong in one place until
  the schedule test with late listings showed it; the amended version still has one hole that the test hit (late
  listing that is not a snapshot); and it depends on every other writer behaving as the proof assumes (A-2). A
  wrong assumption there fails silently, which is the failure this repair exists to remove.
- A also switches the overlap off whenever any other lock is listed. That is the normal state of a repository that
  several machines back up to, so those users get stock's speed.
- The price of B is measured: +29 to +63 ms per process at 30 ms per request, nothing measurable on a local
  repository. If that is too much on the remote benchmark, A is the fallback, with A-1 and A-2 stated in the
  release notes. Shipping both together gains nothing: with B's comparison in place the gate saves no request in
  the normal case.

Parking lot (not acted on):

- B's cost in `ls` and `check` could drop from two listings to one on the critical path by letting the index
  listing overlap the snapshot load (ls) or the pack listing (check). That is an optimisation, out of scope here.
- A could keep the overlap beside non-exclusive locks by loading the listed locks and closing the gate only for
  conflicting ones, as L1 and L2 do. More requests in that case; not built.
- B-2 could be closed with one `Stat` per kept index file, at one request per index file in every process. Not
  built.
- With `--retry-lock 0` (the default) the stock lock code makes a random number of immediate attempts after a
  refusal (`lock.go:75-104`: two timers that are both due). Stock behaviour, not changed; it only matters to anyone
  who writes a deterministic test of it.
