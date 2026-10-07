# Soundness

What the reviews and the release checks found in the league's code, what was done about each finding, how to check
it, and what is still unknown. Labels: R1 to R11 and M22 are findings of the first soundness reviews; F1 to F5 are
failures found by the release checks; C01 to C16 are the release checks; "finding 1" to "finding 11" are those of
the last independent review, in the numbering of its triage ([RED-TEAM.md](RED-TEAM.md)). "Upstream" is restic
0.19.1-dev at `ba802d42`.

## 1. Summary

- For nineteen seasons the league's judge compared every candidate with upstream in every round and passed every
  incumbent. **The judge never caught the defect R1**, because its workloads run one restic process at a time and R1
  needs two.
- **R1 lost data silently.** A backup that ran beside a prune could deduplicate against data the prune had just
  removed, write a snapshot that referenced it, and exit 0. Nothing told the user.
- R1 was found by independent reviewer agents who read the league's tree mechanism by mechanism after season S019.
  Three reviewers, working on three different areas, each found it.
- The same reviews recorded ten more findings (R2 to R11: one HIGH, seven MEDIUM, one LOW, one about test coverage)
  and a memory finding (M22). All are repaired in this tree, each with a test that fails without the repair.
- Release checks with real binaries on Linux then found four failures (F1, F2, F3, F5), all fixed, and raised one
  alarm that was withdrawn because the defect was in the check.
- A last review, by three reviewers of a different model family who were given only upstream and the tree, found no
  case of data loss, of a wrong restore, or of an incomplete backup reported as success. It reported 13 findings
  (12 distinct, one having been found by two reviewers; tabled in 11 rows). **Nine rows were fixed**, each with the
  reviewer's test as a regression test, one of them in part with the rest documented; **two rows were documented and
  not changed**: restore reads each directory tree once, and index and lock files are encoded differently. The fixes were reviewed in turn: nothing above LOW ([RED-TEAM.md](RED-TEAM.md)).
- What is known to remain is in section 10 and, in full, in [KNOWN-LIMITS.md](KNOWN-LIMITS.md). The one that matters
  most: on a backend whose listings can lag behind its deletions, the lock-wait validation can be defeated, in a
  corner that upstream shares when it has the index file in its local cache.

The reviews as a method (what a finding had to show, how tests were mutated) are described in
[METHODS.md](METHODS.md), sections 9 to 11. The reviewers' own reports are under
[`../reviews/soundness/`](../reviews/soundness/).

## 2. R1: reads taken during the lock wait were used after the lock was confirmed

**What upstream does.** To lock a repository restic writes its lock file, waits 200 ms, lists the lock files again,
and continues only if no conflicting lock has appeared. It reads nothing else from the repository before that second
check.

**What the league's tree did.** The 200 ms are idle time in every process, so the league moved work into them. The
snapshot list and the index were read during the wait (for backup from season S013, for restore, check, prune, forget
and ls from S015) and used after the lock was confirmed.

**Why that is wrong.** A process whose own lock was confirmed before ours became visible may change the repository
and unlock inside our wait. What we read during the wait is then stale, and the second lock check cannot tell: the
other lock is gone.

**The failing schedule.** A backup writes its lock file; the write is slow (a remote backend, a retried upload, a
stall). In that time `forget --prune` takes its exclusive lock, sees no conflicting lock yet, removes packs, rewrites
the index and unlocks. The backup has read the old index during its wait. Its lock is confirmed. It deduplicates
against blobs in the removed packs, writes a snapshot that references them and reports success. `check` by upstream
then says `tree ... not found in repository`. The same staleness made a prune beside a backup, and a restore beside a
prune, fail with an error where upstream succeeds.

**Measured with real binaries** (release check C03: a backup whose lock-file write is held back, racing a
`forget --prune` on the same repository, then `check` by upstream; 480 trials per tree, which is four lock delays
times 60 trials with uniform timing and 60 aimed at the window, so 240 uniform and 240 aimed; 120 trials for
upstream; Linux x86-64):

| run of the check | league tree after S019, unrepaired | tree under test | upstream |
|---|---|---|---|
| on the repaired tree | **33 of 480** broken snapshots | 0 of 480 | 0 of 120 |
| on the second final tree, one commit before its last (`final2-3509049`) | **37 of 480** | 0 of 480 | 0 of 120 |
| on the second final tree (`final2-d4f7e81`) | **35 of 480** | 0 of 480 | 0 of 120 |
| on the judged commit (`final3-b8cf439`) | **37 of 480** | 0 of 480 | 0 of 120 |

On the judged commit the Go-level form of the same race, the randomized schedule test, ran 12,000 schedules in four
listing-lag modes with 0 failures.

In the first run, by lock delay of 300 / 700 / 1500 / 3000 ms: 7, 8, 9 and 5 of 60 aimed trials broke on the
unrepaired tree, and 4, 0, 0, 0 of 60 trials with uniform timing: 29 of 240 aimed and 4 of 240 uniform. In the run
on the judged commit the unrepaired tree's 37 were 35 of 240 aimed and 2 of 240 uniform. The tree under test: 0
everywhere, no damaged repository, no hang. Failure signature: the backup exits 0.

**The repair that was chosen** (commit "R1 (validate)"): nothing read during the wait is used unchecked. After the
lock is confirmed the snapshot files and then the index files are listed again. Commands always work with the snapshot
listing taken after confirmation. The index loaded during the wait is kept only if every index file it was built from
is still listed (an index file is named by the hash of its stored bytes). Index files added in the meantime are loaded
in addition. A removed snapshot or index file, or any failed read during the wait, discards everything read during the
wait and reads it again in upstream's order. The wait itself is untouched.

**The repair that was rejected** was a gate: after writing its own lock, a process lists the locks, and any foreign
lock means no work during the wait. It costs one listing instead of two. Both designs were built and compared
([`../reviews/soundness/R1-REPORT.md`](../reviews/soundness/R1-REPORT.md)). The gate's correctness rests on an
argument about every other process and about how a backend lists lock files. On a backend that lists a new lock file
late and whose listing is not a snapshot, 1 of 3,000 randomized schedules still failed with the gate as built.
Validation rests only on file names being content hashes, and failed in none.

**The randomized schedule test** (`cmd/restic/lock_wait_schedule_test.go`, `TestLockWaitSchedules`): two or three
processes (backup, restore, prune, forget, `forget --prune`, check) on an in-memory backend with seeded per-operation
delays in virtual time. Every schedule runs in upstream's order and with the tree's wait work on the same lock
timeline. The oracle: after a confirmed lock a command uses no snapshot or index file that was removed before, misses
none that existed, ends as upstream's order ends, and leaves the repository readable and equal. On the unrepaired
tree 163 of 3,000 schedules failed; with the repair 0 of 3,000 (4,043 confirmed locks, 3,402 of them with reads during
the wait). 500 schedules run by default; `RESTIC_R1_SCHEDULES`, `RESTIC_R1_SEED`, `RESTIC_R1_SCHEDULE_LAG` change
that.

**Other tests that guard it:** `lock_wait_stale_witness_test.go` and `lock_wait_dwell_witness_test.go` (the three
reviewers' witnesses), `wait_index_load_validate_test.go` (the three verdicts of the comparison),
`lock_wait_requests_test.go` (backend requests per command), `lock_wait_duration_test.go` (the confirmation scan
starts exactly one settle delay after the lock file was saved). All in `cmd/restic/`.

**What it costs:** one listing of snapshots and one of index files per process after confirmation. At 30 ms per
request that measured +29 to +31 ms per process and +61 to +63 ms for `ls` and `check`
([RESULTS.md](RESULTS.md), section 5.2).

**What it does not cover, and what was changed later.** The comparison was by file name. The last independent
review showed two consequences (findings 2 and 7 in [RED-TEAM.md](RED-TEAM.md)): an index file damaged on storage
inside the wait window was not noticed, so `check` could report a damaged repository as healthy; and the validating
listing was taken earlier than upstream lists, so a restore could fail on a backend with lagging listings. Both were
fixed: the comparison is now by name **and size**, the listings are taken where upstream takes them, and `check`
reads nothing during the wait. What remains is a contract, stated exactly by the review of those fixes: a kept index
is validated by a listing and is not read again, so on a backend whose listing can still show a file after its
deletion, the outcome R1 was repaired for is possible again
([KNOWN-LIMITS.md](KNOWN-LIMITS.md), section 5.3, with the two conditions it needs and what upstream does there).
On backends with consistent listings the randomized schedule test finds nothing: 12,000 schedules in four
listing-lag modes, 16,469 confirmed locks, 0 failures.

**An earlier defect of the same code.** After season S016 the league itself found that the overlap listed the index
before the snapshots. With a backup running beside it, `restore latest` failed in 31 of 100 runs and `ls latest` in 29
of 100 (upstream 0). That was fixed then ([race-fix report](../reviews/merge/RACE-FIX-REPORT.md)). The fix did not
cover R1.

## 3. R2 to R11 and M22

Severity words are the reviews' own. "Guard" names a test or check in this tree that fails without the repair.

| id | severity | what the league's tree did | repair | guard |
|---|---|---|---|---|
| R2 | HIGH | The coalesced pack stream ran the blob handler inside the backend's `Load` callback, holding a connection. `repair packs` with one backend connection deadlocked (upstream passes). A decode error inside `Load` was retried until the retry budget ended (8 requests instead of 1 in the test; 15 minutes in the binary). A `Load` that returned without data returned success. | Handlers that use the backend (repack, copy, repair packs) run after `Load` has returned; decode errors are returned once; a `Load` without data fails closed, as upstream. Restore keeps the coalesced path (its handler makes no backend call). | `TestRepackHandlerRunsOutsideLoad`, `TestRepairPacksOneConnection` (`internal/repository/stream_pack_released_test.go`); check C08: `repair packs` with one connection finished 6 of 6 times in 1.1 s where the unrepaired tree finished 0 of 6 |
| R3 | MEDIUM | The hand-written parallel scrypt did not validate its parameters. A key file with N=6, p=2 crashed the process; N=12 or N=20 derived a key where upstream stops with an error. | The three checks of `scrypt.Key`, same conditions, order and texts, before any lane. | `internal/repository/crypto/scrypt_params_test.go` |
| R4 | MEDIUM | Under `--compression auto` a sampler encoded 16 windows of 2 KiB and stored the blob uncompressed if they did not shrink. One incompressible 64 KiB block repeated sixteen times was stored at 1,048,608 bytes; upstream stores 65,661. | The sampler is removed; every blob is encoded, as in upstream. (The remaining "store raw if not smaller" rule was removed later: F2.) | `TestCompressionAutoStoresWhatStockStores` (`internal/repository/repository_compression_auto_test.go`) |
| R5 | MEDIUM | Index-load workers were capped by the CPU count: on one or two CPUs two index files were downloaded at a time whatever the backend allowed. | Workers follow the backend's connections, never above upstream's count. | `TestIndexLoadAdmissionAndCancellation`; check C10 part A on the judged commit: 500 index files behind 50 ms on 1 CPU: upstream 11.82 s, this tree 11.56 s, unrepaired 19.13 s; downloads in flight 5 / 5 / 2 |
| R6 | MEDIUM | With output not on a terminal, a failed or stalled write kept every later message, errors included, in memory without bound and printed none of them (stdout on a full disk: nothing on stderr). The league's own test file for this package did not compile. | The retry queue is removed. A failed write is reported on stderr and dropped; memory is bounded by one batch. | `internal/ui/termstatus/status_failing_writer_test.go`; check C09 (70 cases against upstream) |
| R7 | MEDIUM | The direct tree decoder accepted a node name with a raw double quote or newline, where upstream rejects the tree (`Unquote: invalid syntax`), in check, ls and restore. | The fast path is taken only for names `strconv.Unquote` returns unchanged; every other name goes through upstream's call. | `internal/data/node_name_unquote_test.go`, `tree_metadata_name_test.go` (37,750 adversarial trees agree with upstream's decoder); check C09 |
| R8 | MEDIUM | Restore rebuilt the hard-link index in its second pass only for nodes that report more than one link. An inode recorded with one link on one path and more on another was restored as two independent files; upstream restores one hard-linked file. | The index is complete before the second pass and every regular file is looked up, as upstream. | `internal/restorer/restorer_hardlink_linkcount_test.go`; check C07 |
| R9 | MEDIUM | A wrong password that needs no prompt ran the whole key search twice (20 keys: 40 key derivations) before the same error. | One search; the second is skipped when it would repeat the first. | `TestOpeningActualPathsMatchOrdinary`, `TestB01OpeningConfigFailureMatrix` (`internal/global/`) |
| R10 | LOW | Left-overs: a 1,998-line `.orig` file; files not formatted with gofmt; switches that ran upstream's code paths whenever the debug log was on, so a debug run did not run what a normal run runs; counters written on every read and read only by tests; a pack-stream function that upstream's tests tested and production no longer called. | Removed or restored, in four commits. | `gofmt -l`, `go vet`, the tree's tests; `TestStreamPackTableOnProductionPaths` |
| R11 | coverage | Upstream's integration tests set the lock wait to zero, which starts no wait work, so none of them ran the R1 path. | `RESTIC_TEST_LOCK_WAIT=200ms go test ./cmd/restic` runs them with the production wait. | `cmd/restic/lock_wait_env_test.go` |
| M22 | memory | Restore's first pass kept the target path of every directory it created until the restore ended: 250 to 320 bytes of live heap per directory (upstream: about 20). | The entry is deleted when the traversal leaves the directory. | `TestRestoreKeepsNoPathPerDirectory` (at most 3 entries for a tree three deep; 3,011 and 12,041 without the repair) |

The reviews made 81 mechanism assessments. After deduplicating shared findings, R1 to R11 comprise one Blocker, one
High, seven Medium, one Low and one coverage finding; M22 is a separate memory finding. (The totals line of the
repair list counts verdicts, not distinct findings, and reads "1 HIGH, 9 MEDIUM"; it was never reconciled with the
table above, which is the count that holds.) The reviews found no weakened hash, MAC or verification on save, in `check` or in
`check --read-data`, and found repository format, chunk boundaries and tree JSON identical to upstream's. Full list
and the reviewers' witnesses: [`../reviews/soundness/REPAIRS.md`](../reviews/soundness/REPAIRS.md),
`REVIEW-storage.md`, `REVIEW-backup.md`, `REVIEW-readside.md`, `R1-REPORT.md`, `R2-R10-REPORT.md` in the same folder.

## 4. What the release checks found: F1, F2, F3, F5, one withdrawn alarm, and the observation F6

There is no F4: the number was reserved for the alarm that was withdrawn. F1, F2, F3 and F5 are failures and were
fixed. On the judged commit the check report's status reads: F1 fixed; F2 not present; F3's test passed in all four
passes of C01; F5 fixed (a restore of 2,020,200 directories peaks at 58.0 MiB against upstream's 56.2 MiB, where the
unrepaired tree peaks at 627.9 MiB: C10, part B). F6 is a performance observation, not a correctness failure. Its
condition was found; it stays open as a limit.

**F1 (low, test file only).** `GOARCH=386 go test ./internal/repository/index` did not compile: a test file of the
league's built `uint(uint64(1) << 32)` in a branch for 64-bit platforms, and the compiler checks the constant on every
platform. The value is now computed at run time. Production code was not affected.

**F2 (medium, behaviour differs from upstream; no data at risk).** Under `--compression auto` the tree stored a data
blob of 512 KiB or more uncompressed when the encoder's output was not smaller. Such a blob counts as uncompressed in
the index. So `prune --repack-uncompressed` rewrote it on every run and never came to rest: 16,179 blobs to repack
before and after a real run on the check's repository, where upstream has 0 after its first run. `stats` reported
compression progress 79.84% where upstream's repository reads 100%. On a repository of media or already compressed
files that is most of the data, downloaded and uploaded again on every such prune. Fix: the stored form is upstream's
again; under every mode but `off` the encoder's output is stored whatever its size. Guards:
`TestCompressionAutoStoresWhatStockStores` (80 blobs, 17 of them not shrunk by the encoder, each stored in compressed
form at upstream's size) and `TestPruneRepackUncompressedComesToRest`; check C11 (12 prune pairs equal to upstream,
stored form included) and C06 (repository sizes within 0.0005% of upstream's on the judged commit). What it cost:
[RESULTS.md](RESULTS.md), section 5.4.

**F3 (low, test only).** The test of the M22 repair sampled the live heap of a running restore and failed in 3 of 20
runs alone on four CPUs (7 of 15 under load). The flakiness was the test's. It now counts the entries of the
structure the repair bounds.

**F5 (low to medium).** Restore memory still grew with the number of directories. Peak resident memory of
`restore latest` on snapshots of empty directories:

| directories | upstream | tree before F5 | league tree after S019, unrepaired |
|---|---|---|---|
| 1,010,100 | 63.0 MiB | 131.1 MiB | 332.5 MiB |
| 2,020,200 | 62.2 MiB | 231.2 MiB | 583.2 MiB |
| per additional directory | 0 bytes | 104 bytes | 260 bytes |

A heap profile put 96.85% of the live heap in one slice: the 32-byte ID of every tree visited in the first pass, kept
for the second pass's read-ahead. Fix: the list is bounded. The first form of the fix bounded it at 65,536 IDs; that
was measured afterwards (Linux x86-64, 4 CPUs, restore of 606,060 directories and three files, wall seconds, peak
RSS):

| bound | seconds | peak RSS |
|---|---|---|
| none (before F5) | 52.8 | 80.0 MiB |
| 524,288 IDs (16 MiB) | 53.3, 51.2 | 57.0 MiB |
| 262,144 IDs (8 MiB) | 57.5, 58.4 | 56.2, 58.5 MiB |
| 65,536 IDs (2 MiB) | 74.0, 70.1 | 56.2 MiB |
| upstream | 64.2 | 63.0 MiB |

Behind the end of the list the second pass loads every tree itself, about 40 microseconds more per directory than
the read-ahead. The bound was therefore set at 524,288 IDs: the read-ahead is kept for snapshots of up to 524,288
directories, and a larger snapshot loses it for the trees past the bound and nothing else. With that bound, a restore
of 2,020,200 directories read 57.4 MiB peak (upstream 68.2 MiB, the tree before F5 231.0 MiB), and the three restored
trees were identical entry by entry. Guards: `internal/restorer/tree_id_list_test.go`
(`TestRestoreKeepsBoundedTreeIDList`, `TestRestoreAcrossTreeIDBoundEqualsRestoreBelowIt`: one snapshot restored with
eleven bounds in ten scenarios, with and without faults, every reported error and every restored entry compared).
**That table was measured with 4 CPUs.** What the same restore did with 16 CPUs, and the change that followed, is in [RESULTS.md](RESULTS.md), section 6.2.

**The withdrawn alarm: "the tree's repository is up to 11.969% larger than upstream's".** Not a property of the tree:
a defect of the check. The check let every binary initialise its own repository; `restic init` draws a random chunker
polynomial, and two repositories with different polynomials cut large files at different places. Upstream against
upstream on a second `init` differed by -1.11% on the judge's main corpus (1,304,549,880 against 1,319,177,009 bytes)
and by -3.05% and +11.7% on a generated corpus. From copies of one initialised repository: upstream 1,319,177,009 and
1,319,177,045 bytes in two runs, the final tree 1,319,177,045 twice; every data blob ID present in both, 0 blobs with
a different stored length, 0 duplicate blobs. The check now writes both binaries into copies of one repository.

**F6 (performance observation, not a correctness failure; its condition found, open as a limit).** With the check
harness's 16 CPUs, which ran the load-following governor `schedutil`, a restore of 606,060 directories and 3 files is
10.1% slower than upstream on the judged commit (median 74.0 s against 67.2 s; three rounds in rotated order). With 4
of those cores it is 26.8% faster (63.3 s against 86.5 s). Before the commit that batches restore's hand-overs the
same two CPU sets read 27.2% slower and 10.3% faster. With 16 CPUs the build spends more CPU time than upstream, user
and system both (39.4 + 57.8 s against 28.6 + 41.9 s).

The check agent then tested what the loss depends on. With the governor of the same 16 CPUs set to `performance` the
build is 16.1% faster than upstream (44.7 s against 53.3 s) and on 4 cores 17.6% faster (44.1 s against 53.5 s). Under
`schedutil` again, on a quiet machine, it is 10.7% slower (75.3 s against 68.0 s). The cache layout of the CPUs had
been ruled out by the merge agent. So: the `performance` governor removes the loss, a quiet machine does not. The
report's conclusion: F6 is not a defect of the judged commit on the judge's set-up. It remains true that on a machine
with many idle CPUs under a load-following governor a restore of very many directories can be slower than upstream
(10.1% and 10.7% at 606,060 directories, 7.8% at 1,010,100, 4.8% at 2,020,200), while it is 26.8% faster on four cores
under the same governor. No code was changed for it. The tables: [RESULTS.md](RESULTS.md), section 6.2; as a limit:
[KNOWN-LIMITS.md](KNOWN-LIMITS.md), section 2.

Source: [`../reviews/checks/FAILURES.md`](../reviews/checks/FAILURES.md), the check agent's report
[`../reviews/checks/REPORT.md`](../reviews/checks/REPORT.md) and the step log beside them.

## 5. The last season's three lock-wait mechanisms, and why they were dropped

Season S020 promoted a tree with three new mechanisms inside the lock wait. They are the same class of work as R1,
they were reviewed separately ([`../reviews/soundness/REVIEW-s020.md`](../reviews/soundness/REVIEW-s020.md)), and
none is in this tree:

- **Backup: compression prepared during the wait.** Dropped: R1 again in its loader; it opened every source entry,
  FIFOs and device nodes included, before the lock was confirmed (3 opens of a FIFO or device per backup under
  `strace`, where upstream and this tree have 0: check C13); and it was built on the constants that R4 deletes.
- **Check: one pack read during the wait.** Dropped: a pack was certified from bytes read before the lock, on the
  strength of `stat` and inotify, which see nothing on NFS, SMB or FUSE mounts.
- **Restore: snapshot and trees read during the wait.** The tree preparation was switched off in S020 itself. The
  snapshot preload would have been allowed with the R1 validation and a clear gain; it measured about +0.02 suite
  points and was left out.

Together the three cost time: S020 with them against S020 without them read -0.14 on the suite (10 fixed pairs per
cell, 4 CPUs). The other work of S020 (index-side work in check and prune, a metadata tree decoder for restore's
second pass) was kept, with one repair. S020's new decoder had copied the R7 defect, and the kept form uses the
repaired name rule.

## 6. The reviews of the final commits

**The final merge.** The 16 commits that turn the repaired tree into the first final tree were reviewed by an
independent reviewer agent ([`../reviews/soundness/REVIEW-final.md`](../reviews/soundness/REVIEW-final.md)): 24
non-test files (+955 / -191 lines) read hunk by hunk, the two hardest ports (A16, the repack read-ahead, and A5, the
prepared cipher) line by line with the code they call. Verdict: **nothing above LOW; nothing reproduced that fails.**

The review also mutated the ported code and ran the authors' own tests against each mutant: 17 mutations (a drain
removed, a save error ignored, a key comparison removed, the store-raw rule put back, and so on). All 17 were caught.
The thinnest guard was one mutation caught by a single test. The review asked for a second, and commit "Review of the
ports (A16 test)" adds three tests, two of which also fail on that mutant. The reviewer's own witness for the cipher
(the published Poly1305-AES vectors through the functions production calls, and 60,000 random steps against upstream's
functions copied verbatim) became a test of the tree.

**Addendum 1** covers the next three commits (F5 in its first form and the two test commits): sound, nothing above
LOW.

**Addendum 2** covers the commit that raised the F5 bound from 65,536 to 524,288 tree IDs (only the constant, its
comment and the size its test expects change: sound) and the eleven commits that followed the last independent
review: the nine fixes, the change to restore's hand-overs, the version string. 21 non-test files, +652 / -429
lines. Verdict: **nothing above LOW; keep all eleven.** 25 mutations, 23 caught by the tree's own tests; the two
misses led to one more test. The detail is in [RED-TEAM.md](RED-TEAM.md), section 5.

The last commit of the published source (one comment rewritten, one test added) is the outcome of Addendum 2. It was
made by the merge agent from that review's two recommendations and is not itself covered by a review; it changes no
code.

## 7. The last review: three reviewers of a different model family

The full account is its own document, [RED-TEAM.md](RED-TEAM.md): what the three reviewers were given and told, the
13 findings with the triage, the commit that fixes each and the test that pins it, what was documented instead of
changed, the review of the fixes, and the limits of the exercise. In brief:

| finding (triage number) | reviewers' label | outcome |
|---|---|---|
| 1. `check --read-data` decoded without upstream's ceiling on decoded bytes | BLOCKER | fixed |
| 2. reads of the lock wait validated by name only; `check` could miss an index damaged in the window | BLOCKER (two reviewers) | fixed for `check`; narrowed for the other commands (size is compared too); the rest documented |
| 3. prune's index rewrite kept index files without verifying them again | BLOCKER | fixed |
| 4. restore uses the trees it verified in its first pass | BLOCKER | **documented, not changed** |
| 5. a read error could not cancel a blocked save in repack and copy | HIGH | fixed |
| 6. index notes never released in a long-lived process | HIGH | fixed |
| 7. restore validated the index listing before reading its snapshot | HIGH | fixed |
| 8. diagnostics of the wait reached stderr when the lock was refused | MEDIUM | fixed |
| 9. a message dropped after a write error on a message boundary | MEDIUM | fixed |
| 10. the prefetch budget missed the hidden tail of a slice | MEDIUM | fixed |
| 11. index and lock files encoded differently from upstream's | MEDIUM (two reviewers) | **documented, not changed** |

Thirteen findings were reported; one was found by two reviewers, so 12 are distinct, and the two about stored
encoding share row 11. Of the 11 rows nine were fixed (row 2 in part, with the rest documented) and two were
documented and not changed.

The label BLOCKER is the reviewers' and follows their brief, which defined it to include "a missing integrity
check". **No reviewer found a case of data loss, a wrong restore, or an incomplete backup reported as success.** The
reviewers' original tests fail on the tree they reviewed (16 of 16) and the fixed findings no longer reproduce.

## 8. Release checks

What each check does: [METHODS.md](METHODS.md), section 10. The scripts: `league/tools/checks/`. The check agent's
report: [`../reviews/checks/REPORT.md`](../reviews/checks/REPORT.md); its step log:
[`../reviews/checks/STEP-LOG.md`](../reviews/checks/STEP-LOG.md). The harness's own status files and tables are
carried in `league/evidence/release-checks-final3-b8cf439/`, and every result line of them is quoted in
`league/evidence/checks/final-check-table.md`.

### 8.1 On the judged commit

Label `final3-b8cf439`, Linux x86-64, real binaries, 2026-10-07; the harness's status file records each result
between 17:15 and 19:40 UTC. **All 16 release checks, C01 to C16, passed on the judged commit `b8cf439`.** The last
to report was C10, the scale check, at 19:40 UTC. The check harness ran on its own CPUs under the `schedutil`
governor, with a memory limit of 16 GiB, while official judge jobs ran on other CPUs of the same machine.

| check | result | key numbers |
|---|---|---|
| C01 the tree's own tests and upstream's | PASS | amd64 as root 2,692 tests, as an unprivileged user 3,045, with the debug log on 1,383, as 386 1,818: 0 failures of the tree's own, 5 shared with upstream in the same environment |
| C02 race detector and the reviewers' witnesses | PASS | 2,426 tests under `-race`, 0 data races |
| C03 the R1 schedule end to end | PASS | unrepaired 37 of 480 broken, judged commit 0 of 480, upstream 0 of 120; the S015 race reproduction 0 of 100 and 0 of 100 |
| C04 kill -9, SIGINT, backend faults | PASS | 400 trials, 397 hit a live process, 0 problems; upstream 198 trials, 0 problems |
| C05 corrupted-pack detection on every route | PASS | 1,200 route comparisons with upstream, REST with 2 connections included, 0 differ |
| C06 cross-read with upstream, both directions; repository size | PASS | 16 matrix cells, 17 upstream fixtures, 11 size comparisons within 0.0005% of upstream's |
| C07 tree IDs on special files; restore | PASS | 7 of 7 steps equal; the 386 binary: same tree ID, restore equal |
| C08 restore option matrix; `repair packs` | PASS | 75 restore cases, 0 differ; restore under 8 backend failure patterns and over SFTP, and prune and copy with 1 and 2 connections, equal to upstream |
| C09 Linux-only code; R6, R7, R9 | PASS | the file-system witness as root, unprivileged and as 386; R6 70 cases, R7 7 names, R9: 0 differ; `restic version` masked and recorded: upstream "0.19.1-dev", this build "0.19.1-dev-dealer.1" |
| C10 scale | PASS (its criterion is memory; timings are single readings) | part A, 500 index files behind 50 ms of latency: on 1 CPU upstream 11.82 s, judged commit 11.56 s, unrepaired 19.13 s, downloads in flight 5 / 5 / 2; on 2 CPUs 11.80 s and 11.47 s. Part B, restore of 2,020,200 directories and 10 files: peak RSS upstream 56.2 MiB, judged commit 58.0 MiB, unrepaired 627.9 MiB, `rc-7` 58.6 MiB; 2,020,211 entries restored with 0 differences from the source; one run each, wall time upstream 221.6 s, judged commit 232.3 s (4.8% slower; section 4, F6). Part C, restore of one 50 GiB **sparse** file: peak RSS 88 MiB upstream, 67 MiB judged commit, content equal, 41.7 s and 41.3 s; with `--sparse` 1.8 s and 1.6 s, 12 MiB allocated by both. Part D, a small backup against a **synthetic** index of 30,000,000 invented blob entries in 600 index files, with no packs behind them: peak RSS upstream 3,288 MiB, judged commit 3,047 MiB, 9.6 s and 6.9 s (10 million entries: 1,308 and 1,017 MiB; 3 million: 598 and 377 MiB) |
| C11 prune differential on a 1.5-million-blob index | PASS | 12 prune pairs equal to upstream; with a kept index file damaged before the rewrite, upstream and the judged commit both exit 1 with the same message, and upstream's `check` exits 1 for both repositories with the same lines |
| C12 kill -9 during repack | PASS | 40 trials, 0 problems |
| C13 what S020 and the later fixes added | PASS | 31 Linux-only tests pass, 2 skip; 730 tests under `-race`, 0 races; 3,000 R1 schedules; the opt-in tests: `TestLargeCheckRejectsUnrestorableBlob` and three more `TestLargeCheck*` pass with `RESTIC_LARGE_CHECK_TEST=1`, `TestSaveVerifyRealLocalPartialUpload` passes against upstream's binary; 0 opens of a FIFO or device during a backup's lock wait |
| C14 repack with a failing backend | PASS | 49 disturbed prunes, 0 problems, among them 7 with a damaged blob while every upload is held: exit 1 by itself each time; upstream 24 trials, 0 problems |
| C15 backup output at verbosity 0 to 3 | PASS | 24 backups, 1,533 lines, 0 differ |
| C16 copy | PASS | 4 copy pairs; 6 of 6 copy runs with a failing source pack and a stalled destination return exit 1 in about 1 s; upstream's 4 copy tests pass in 3 set-ups |
| x08 `repair packs` with one connection | PASS | judged commit 6 of 6 in 1.2 s; unrepaired 0 of 6 |
| x11 restore of 606,060 directories and 3 files, rotated | a measurement, no criterion | under `schedutil` with 16 CPUs 10.1% slower than upstream, on 4 cores 26.8% faster; under `performance` 16.1% and 17.6% faster; under `schedutil` on a quiet machine 10.7% slower (section 4, F6) |

**On the published commit itself** (`8f9372b`, label `final3-8f9372b`, one commit after the judged one) the full
harness was **not** run. What was run there (diagnostic x13): the test the last commit adds,
`TestAsyncSaveResultSuppressionIsNarrow`, plain and three times under the race detector (passes); and a file-by-file
comparison of the two exports by the check agent, which shows exactly two differences,
`cmd/restic/wait_index_load.go` and the new test file `internal/repository/repack_scope_test.go`.

Not covered by the harness on any tree, in the report's words: macOS and the BSDs; Windows, volume snapshots
included; the FUSE mount tests (switched off in both arms, because the checks were not allowed to mount anything on
a machine that was running judge jobs); an index written by rustic; real remote backends (the remote routes used are
a REST server on a Unix socket with injected latency and faults, and SFTP through a local `sftp-server` process);
the race detector with a count of 3 over the whole suite (a count of 1 was used).

The harness was changed for this run, and the log records how: `restic version` is no longer compared with upstream's
(the build prints its own version string; C09 masks the token and records both strings). C11 has a kept index file
damaged by the server between the load and the rewrite, for upstream and for the tree. C14 has a fourth failure kind,
a damaged blob in a pack to repack while every upload is held (the command must return the read error within 90 s).
C16 copies with damaged or missing source packs while the destination holds every upload. C13 runs the tree's opt-in
tests, among them the production-size test of the decode ceiling. A smoke run of the changed checks at small sizes
passed: the damaged kept index file makes upstream and the tree abort with the same message
(`Fatal: failed to rewrite indexes: LoadUnpacked(...): LoadRaw(...): invalid data returned`, exit 1); a damaged blob
with stalled uploads makes both prunes return exit 1 in under a second.

What the merge agent ran on the judged commit and on the commit before its last (Linux x86-64, the league box;
[`../reviews/merge/FINAL-MERGE-STEP-LOG.md`](../reviews/merge/FINAL-MERGE-STEP-LOG.md), entries of 2026-10-07 16:30Z
to 16:44Z):

| check | result |
|---|---|
| build and `go vet` | clean |
| the tree's own tests of the touched packages | 35 packages ok; one test fails, `TestArchiverErrorReporting`, which fails for upstream too when run as root |
| all packages against upstream in the same environment (at the commit before the version string) | 51 packages ok; nothing fails that does not fail for upstream |
| the judge's oracle, 7 phases on 4 workloads | 28 of 28 pass, with the new program version |
| upstream's tests overlaid on the tree's code (the judge's rule) | 1,683 of 1,683 pass |
| the R1 witnesses; the randomized schedule test with 3,000 schedules | 6 of 6; 0 failing (4,043 confirmed locks, 3,032 with reads during the wait) |
| `cmd/restic` tests with the lock wait at 0 and at 200 ms | only the three tests that fail as root for upstream too |
| race detector: terminal output, retrying backend, index (whole packages); repository (repack cancellation, check stream, rewrite, prune); the lock-wait tests | clean |
| linux/386 | builds; tests pass |
| the reviewers' original tests, unchanged, on the tree they reviewed | 12 of 12 tests of the nine fixed findings fail there |
| the production-size test of the decode ceiling (64 GiB + 1 byte) | passes in 37 s; both controls reject |
| key-derivation loop | `0x8241a0`, 32 modulo 64, as in the tree before |

### 8.2 On the second final tree (`rc-7`), eleven commits before the judged commit

All 16 checks, Linux x86-64, real binaries, label `final2-d4f7e81`, 2026-10-07. The judged commit differs from this
tree by the nine fixes, the change to restore's hand-overs and the version string. The numbers in this table are
this earlier tree's; the scale check's sizes are synthetic, as in section 8.1.

| check | result | key numbers |
|---|---|---|
| C01 the tree's own tests and upstream's | PASS | amd64 as root 2,664 tests, as an unprivileged user 3,017, with the debug log on 1,360, as 386 1,796: 0 failures of the tree's own, 5 shared with upstream in the same environment |
| C02 race detector and the reviewers' witnesses | PASS | 2,392 tests under `-race`, 0 data races |
| C03 the R1 schedule end to end | PASS | unrepaired 35 of 480 broken, tree 0 of 480, upstream 0 of 120; the S015 race reproduction 0 of 100 and 0 of 100 |
| C04 kill -9, SIGINT, backend faults | PASS | 400 trials, 392 hit a live process, 0 problems |
| C05 corrupted-pack detection on every route | PASS | 1,200 route comparisons with upstream, 0 differ |
| C06 cross-read with upstream, both directions; repository size | PASS | 16 matrix cells, 17 upstream fixtures, 11 size comparisons within 0.0002% |
| C07 tree IDs on special files; restore | PASS | 7 of 7 steps equal; the 386 binary: same tree ID, restore equal |
| C08 restore option matrix; `repair packs` | PASS | 75 restore cases, 0 differ; restore under 8 backend failure patterns and over SFTP equal to upstream; `repair packs` with one connection 1.2 s |
| C09 Linux-only code; R6, R7, R9 | PASS (second run) | R6 70 cases, R7 7 names, R9: 0 differ. The first run reported one R6 difference that was an artifact of the harness (a count of status ticks that depends on timing) |
| C10 scale | PASS on its criterion, which is memory; **a timing regression with 16 CPUs** | part A (500 index files behind 50 ms): upstream 11.82 s, tree 11.56 s on 1 CPU (unrepaired 19.10 s). Part B, restore peak RSS, upstream / tree: 606,060 directories 58.5 / 59.6 MiB, 1,010,100 directories 57.8 / 59.3 MiB, 2,020,200 directories 56.2 / 59.7 MiB (the first final tree 233.1, the unrepaired tree 625.2). **Part B wall time with 16 CPUs, upstream / tree: 66.8 / 85.0 s, 113.5 / 137.0 s, 226.8 / 263.4 s**; this led to the change of restore's hand-overs ([RESULTS.md](RESULTS.md), section 6.2). Part C, a 50 GiB file restore: peak 88 / 69 MiB. Part D, backup against an index of 30,000,000 blobs: peak 3,304 / 3,064 MiB |
| C11 prune differential on a 1.5-million-blob index | PASS | 12 prune pairs equal to upstream, stored form included |
| C12 kill -9 during repack | PASS | 40 trials, 0 problems |
| C13 what S020 added | PASS | 31 Linux-only tests, 3,000 R1 schedules, 0 races, 0 opens of a FIFO or device during a backup's lock wait |
| C14 repack with a failing backend | PASS | 42 disturbed prunes, 0 problems; upstream 21, 0 |
| C15 backup output at verbosity 0 to 3 | PASS | 24 backups, 1,533 lines, 0 differ |
| C16 copy | PASS | 4 copy pairs; upstream's 4 copy tests pass in 3 set-ups |

Behaviours of upstream that the checks met on the way, and that this tree shares: `check` on a tree whose node name
holds a raw quote stops with `panic: tree was not read completely`; a restore over a FIFO that is in the way of a file
waits forever and ignores SIGTERM; upstream's `cmd/restic` tests do not compile for 386.

## 9. Reproduce the R1 result

The harness is published under `league/tools/checks/`. The check is one script, on Linux:

```
CHK_BEFORE_TREE=<the unrepaired tree> CHK_SCRATCH=<a scratch directory> \
  python3 league/tools/checks/c03_lock_race.py <tree to test> <label>
```

It builds three binaries: upstream, the tree under test, and as the positive control the unrepaired tree, which is
commit 2 of this repository (`git log --reverse --format=%H | sed -n 2p` gives its id; check it out into a second
worktree). A small REST server (`league/tools/checks/server/chkrest/`) holds back only the backup's lock-file upload
for a set delay. A `forget --prune` on the same repository through the local path races it. Afterwards upstream's
`check --read-data` judges the trial: BROKEN if the backup exited 0 and upstream rejects the repository. The script
header describes the two start-time distributions (uniform and aimed). Four constants at the top of
`league/tools/checks/lib/chk.py` are the league box's and must be changed first: the upstream tree, the pinned Go
toolchain, the CPUs the checks pin themselves to, and the scratch default; see [TOOLS.md](TOOLS.md), section 5.

Without the harness the same defect shows in Go tests alone. The tests in `cmd/restic/lock_wait_stale_witness_test.go`
and `lock_wait_dwell_witness_test.go` are the reviewers' witnesses; they pass in this tree, and their header comments
say what they assert. `TestWitnessLockDwellStockOrder` is the control in upstream's order.

## 10. What remains unknown

- **The lock-wait contract on backends with lagging listings** ([KNOWN-LIMITS.md](KNOWN-LIMITS.md), section 5.3).
  It is known, documented and not closed. It was shown with a modelled backend; no real backend with lagging
  listings was used in any test.
- **The full harness on the published commit.** All 16 checks passed on the judged commit `b8cf439`. On the
  published commit `8f9372b`, which differs from it in a comment and a test, only the export comparison and that
  test were run (section 8.1).
- **More than 4 CPUs, and a clock that follows the load.** Every league timing was taken with 4 CPUs and the
  `performance` frequency governor. The four workloads were benched at 8 and 16 cores on the published machine code
  (all 56 cells ahead of upstream; 5 pairs per cell; the `performance` governor; not a judge score). On a machine with
  many idle CPUs under a load-following governor, a restore that is almost only directories was 4.8% to 10.7% slower
  than upstream with 16 CPUs (four readings). With 4 cores under the same governor it was 26.8% faster, and with the
  CPUs held at full clock 16% to 18% faster at both counts. Why the governor has this effect is an inference, not a
  measurement, and nothing was measured on a second machine ([RESULTS.md](RESULTS.md), section 6.2).
- **Never run on any tree:** macOS, BSD, Windows with VSS or elevated; the FUSE `mount` tests; a repository index
  written by rustic. The full race-detector suite was not repeated with a count greater than one; the added test
  `TestAsyncSaveResultSuppressionIsNarrow` was run with `-race -count=3` on the published source.
- **Scale.** Release check C10 measures synthetic sizes: a 50 GiB sparse-file restore, and the loading of a
  synthetic index with up to 30 million invented entries, without packs behind them, during a small backup. Neither
  tests a 50 GiB file of incompressible data or a fully populated repository of that size. Its criterion is memory;
  its timings are single readings.
- **A16 (repack reads one pack ahead):** blocked upload queues with a source read failure were tested through the
  injected REST backend in checks C14 and C16 on the judged commit (section 8.1). Several simultaneous upload
  failures on a real provider, and long stalls in production, remain untested.
- **Large-files memory:** possibly about 4 MiB over the league's own 2% allowance by one arithmetic
  ([RESULTS.md](RESULTS.md), section 5.5); nothing in the tree is known to cause it.
- **One small unattributed reading:** `ls --json` -0.6 to -0.7% on two workloads in the repairs-only tree.
- **The held-out workload** has not been run on this tree, so the checks that the gains transfer end at S019.
- **The release binaries were not themselves judged or checked.** The judge and the release checks ran builds
  without the version flag; the release binaries are built with it ([RESULTS.md](RESULTS.md), section 2).
- **Real backends.** S3, B2, Azure, GCS and rclone were not exercised by any check. REST and SFTP were, on one
  machine.
- The reviews were made by AI reviewer agents. They read every non-test hunk they list and wrote failing tests for
  what they claim, and four rounds found different things each time. That is evidence that no round was exhaustive.
