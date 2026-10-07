# Release checks on Linux: final report (2026-10-07 19:50 UTC)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Judged commit: b8cf439 (branch final3, export trees/rc-8). Published commit: 8f9372b (export trees/rc-9).
The checks ran on the JUDGED commit, label `final3-b8cf439`. On the published commit only two things ran (label
`final3-8f9372b`): the export diff and the one new test (x13). `diff -rq rc-8 rc-9` gives exactly two differences:
`cmd/restic/wait_index_load.go` differs (a comment) and `internal/repository/repack_scope_test.go` exists only in rc-9.

Stock = restic 0.19.1-dev, commit ba802d42. "Unrepaired" = `incumbents/S019-merge`. Harness on the box:
`/opt/league/restic-final/checks/`; copies of the scripts in `checks\scripts\`; step log `checks\PROGRESS.md`;
failures with repro commands in `checks\FAILURES.md`. $0 API. Written by the coordinator from the checks agent's
hand-back (its session does not write report files). The status file, the generated table, the short x11 table and
C10's summary are copied to `Leagues\evidence\restic-final-20261007\release-checks-final3-b8cf439\`.

CPU set: cores 10, 11, 16, 17, 18, 23, 46, 47 with their SMT siblings 58, 59, 64, 65, 66, 71, 94, 95 (two cores in each
of four L3 domains: 6-11, 12-17, 18-23, 42-47), memory limit 16 GiB. The official judge jobs were running on other CPUs
of the same machine during parts of the run, so wall times carry that neighbour.

## Result on the judged commit: every check PASS, no correctness failure

| check | verdict | key numbers |
|---|---|---|
| C01 the tree's own tests | PASS | amd64 as root 2,692 tests: 2,662 pass, 3 fail, 27 skip; as uid 65534 3,045 tests, 0 fail; with DEBUG_LOG 1,383 tests, 1 fail; 386 (19 touched packages) 1,818 tests, 2 fail. Failures of ours 0; the same failures on stock 5 |
| C02 race detector + reviewers' witnesses | PASS | 2,426 tests in 3 runs, 0 packages with a data race, 0 failures of ours (3 also on stock); 53 witness tests pass, 2 fail by construction (they play the old caller of the lock wait) |
| C03 the blocker (R1), real binaries | PASS | broken snapshots: unrepaired 37 of 480 (uniform timing 2 of 240, aimed timing 35 of 240), rc-8 0 of 480, stock 0 of 120; rc-8 0 damaged repositories, 0 hangs; S015 race repro 0 of 100 restores and 0 of 100 ls failed |
| C04 kill -9, SIGINT, backend faults | PASS | 400 trials (397 hit a live process), 0 with a problem; stock 198 trials, 0 |
| C05 corrupted-pack detection | PASS | 60 damaged repositories (15 kinds x 4 rounds), 1,200 route comparisons with stock incl. REST with 2 connections, 0 differ |
| C06 cross-read with stock, both directions | PASS | 16 live cells, 0 with a problem; 17 stock fixtures, 0 differ (2 archives skipped: not repositories or unknown password); repository size from the same init, 11 comparisons, largest difference +0.0005% |
| C07 tree IDs on special files; restore | PASS | 7 of 7 backup steps equal in exit code, tree ID and listing (5,120 MiB file, devices, ACL); unreadable as uid 65534: exit 3 for both; restore 0 differences in 226 entries; hard links (R8) 3 of 3; 386 binary equal |
| C08 restore option matrix; repair packs (R2) | PASS | 75 restore cases by both binaries, 0 differ; `repair packs` with one connection: rc 0 in 1.3 s; prune and copy through REST with 1 and 2 connections equal to stock |
| C09 Linux-only code; R6; R7; R9 | PASS | fs tests + witness as root 138 pass, as uid 65534 40 pass, on 386 138 pass, 0 fail; wait-work tests pass; R6 70 cases, 0 differ; R7 7 names, 0 differ; wrong password 0.279 s (stock 0.482 s), same exit 12 and message |
| C10 scale | PASS | A, 500 index files behind 50 ms: 1 CPU stock 11.82 s / rc-8 11.56 s / unrepaired 19.13 s, downloads in flight 5 / 5 / 2. B, 2,020,200 directories: peak RSS stock 56.2 / rc-8 58.0 / unrepaired 627.9 MiB. C, 50 GiB file restore: stock 41.7 s, 88 MiB / rc-8 41.3 s, 67 MiB, content equal. D, index of 30,000,000 blobs, small backup: stock 9.6 s, 3,288 MiB / rc-8 6.9 s, 3,047 MiB |
| C11 prune differential, 1.5-million-blob index | PASS | 12 prune pairs (3,724 / 24,724 / 192,233 index entries in the packs to repack), 0 differ; stock `check --read-data` passes on every result; kept index file damaged before the rewrite: stock and rc-8 both exit 1 with the same error, stock check exit 1 for both |
| C12 kill -9 on the repack route | PASS | 40 trials, 40 hit a running prune, 0 with a problem; stock 10 trials, 0 |
| C13 final-tree delta | PASS | 0 dropped-mechanism files; Linux-only tests 31 pass, 2 skip, 0 fail; under -race 425 + 305 tests, 0 races; 10 R1 tests with 3,000 schedules pass; 5 opt-in tests pass (4 TestLargeCheck*, TestSaveVerifyRealLocalPartialUpload against the stock binary); strace: 0 opens of a FIFO or device during a backup's lock wait in 6 backups (stock 0; S020 incumbent as control: 3 per backup) |
| C14 backend failure in the middle of a repack | PASS | 49 disturbed prunes, 49 hit a running prune, 4 kinds of failure, 0 with a problem; stock 24 trials, 0 |
| C15 backup output, verbosity 0 to 3 | PASS | 24 backups (4 verbosities x text/JSON x 3), 1,533 stdout lines of stock, 0 differ |
| C16 copy between two repositories | PASS | 4 of 4 pairs without a problem; failing source pack with a stalled destination: returns the read error in 6 of 6 runs (stock 6 of 6); stock's 4 copy tests pass as root, as uid 65534 and with the 200 ms lock wait |
| x08 repair packs, one connection, 6 runs each | PASS | stock finished 6 (median 1.3 s); rc-8 finished 6, 0 hangs (1.2 s); unrepaired finished 0, 6 hangs at the 60 s limit |
| x11 short: restore of 606,060 directories, rotated | PASS (measurement) | 16 CPUs: stock 67.2 s, rc-8 74.0 s (+10.1%); 4 cores: stock 86.5 s, rc-8 63.3 s (-26.8%); peak RSS within 1.1 MiB of stock or below. See F6 |
| x13 on the published commit 8f9372b | PASS | TestAsyncSaveResultSuppressionIsNarrow (internal/repository): pass plain, pass 3 times under -race; 2 files differ from rc-8 |

Comparisons of `restic version` output and of snapshot JSON ignore the program version: the tree reports
0.19.1-dev-dealer.1, stock 0.19.1-dev.

In this run C08's single unrepaired control finished `repair packs` (rc 0) instead of hanging. The hang is a race;
x08's six runs show the unrepaired tree finishing 0 of 6. It does not touch the verdict on the judged commit.

## F6: restore of many directories (performance observation, not a correctness failure)

606,060 directories and 3 files, snapshot written by stock, three rounds in rotated order (stock, rc-8, rc-7), target
removed and `sync -f` before every restore. From `results/final3-b8cf439/x11-606060/table.md`:

| CPUs | binary | wall seconds per round | median s | against stock | CPU s (median) | user + system s | peak RSS MiB |
|---|---|---|---|---|---|---|---|
| all 16 check CPUs | stock | 66.5, 68.0, 67.2 | 67.2 | | 70.5 | 28.6 + 41.9 | 57.0 |
| all 16 check CPUs | rc-8 | 73.0, 74.7, 74.0 | 74.0 | +10.1% | 97.1 | 39.4 + 57.8 | 58.1 |
| all 16 check CPUs | rc-7 | 85.5, 85.6, 84.1 | 85.5 | +27.2% | 118.4 | 52.5 + 65.8 | 57.5 |
| 4 cores | stock | 86.5, 86.7, 86.5 | 86.5 | | 92.8 | 39.0 + 53.9 | 62.2 |
| 4 cores | rc-8 | 63.4, 63.3, 63.0 | 63.3 | -26.8% | 79.3 | 31.7 + 47.6 | 56.2 |
| 4 cores | rc-7 | 76.1, 78.8, 77.6 | 77.6 | -10.3% | 102.2 | 43.6 + 58.6 | 57.0 |

Beside it, C10 part B, 2,020,200 directories and 10 files with all 16 check CPUs, each binary ONCE, in the order stock,
rc-8, rc-7, unrepaired, no rotation: stock 221.6 s, rc-8 232.3 s (4.8% slower than stock), rc-7 261.3 s (17.9% slower),
unrepaired 225.4 s (1.7% slower). rc-8 restored 2,020,211 entries with 0 differences from the source.

The rounds agree within 2.5%, so the order of the runs is not the cause. rc-8 removed most of rc-7's loss. Memory is
not affected: peak RSS equals stock within 2 MiB at 0.6 and 2.0 million directories. The rotated runs at the larger
sizes were stopped at 20:10Z to free the CPUs for the governor test below; kept in `x11-large-partial/`: 1,010,100
directories with 16 CPUs, all three rounds: stock 110.7 s, rc-8 119.3 s (+7.8%), rc-7 134.5 s (+21.5%); 4 cores,
round 1 only: stock 143.7 s, rc-8 100.4 s. Nothing at 2,020,200 beyond C10 part B.

## F6 settled: the frequency governor (2026-10-07 20:50 UTC)

The 16 check CPUs run the schedutil governor (driver acpi-cpufreq, 1,500 to 2,750 MHz, boost on); the judge and
research lanes run performance. Test, by the operator's order: the saved state of the 16 CPUs was recorded,
scaling_governor was set to performance on those 16 CPUs only (nothing else written), the short run was repeated,
and the saved state was written back and read back equal (20:33:58Z; again 20:48:19Z and 20:50:11Z). Then the short
run was repeated once more on schedutil with a quiet box. 606,060 directories and 3 files, snapshot by stock, three
rotated rounds, sync before each restore; stock against rc-8 (judged commit b8cf439).

| governor | neighbour | CPUs | binary | wall s per round | median | against stock | user + system s | observed clock |
|---|---|---|---|---|---|---|---|---|
| schedutil | official judge job | 16 | stock | 66.5 / 68.0 / 67.2 | 67.2 | | 28.6 + 41.9 | not sampled |
| schedutil | official judge job | 16 | rc-8 | 73.0 / 74.7 / 74.0 | 74.0 | +10.1% | 39.4 + 57.8 | not sampled |
| schedutil | official judge job | 4 cores | stock | 86.5 / 86.7 / 86.5 | 86.5 | | 39.0 + 53.9 | not sampled |
| schedutil | official judge job | 4 cores | rc-8 | 63.4 / 63.3 / 63.0 | 63.3 | -26.8% | 31.7 + 47.6 | not sampled |
| performance | merge agent's bench, light | 16 | stock | 53.2 / 53.7 / 53.3 | 53.3 | | 22.3 + 32.6 | 3,798 MHz |
| performance | merge agent's bench, light | 16 | rc-8 | 44.7 / 44.3 / 44.7 | 44.7 | -16.1% | 23.6 + 32.9 | 3,798 MHz |
| performance | none | 4 cores | stock | 53.1 / 53.7 / 53.5 | 53.5 | | 23.4 + 33.1 | 3,799 MHz |
| performance | none | 4 cores | rc-8 | 44.1 / 43.6 / 44.1 | 44.1 | -17.6% | 22.1 + 31.9 | 3,798 MHz |
| schedutil | none | 16 | stock | 68.2 / 68.0 / 67.8 | 68.0 | | 29.1 + 42.2 | 2,551 MHz; 61% of readings at 1,500 |
| schedutil | none | 16 | rc-8 | 75.3 / 74.7 / 75.6 | 75.3 | +10.7% | 40.5 + 58.2 | 2,020 MHz; 58% of readings at 1,500 |

Observed clock: mean of the scaling_cur_freq readings taken on a running CPU, every 5 s. Load on the CPUs outside the
check set: 1.7 to 1.8 busy CPUs in the rows marked "light", 0.2 to 0.4 in the rows marked "none".

- The performance governor removes the 16-CPU loss: rc-8 is 16.1% faster than stock with 16 CPUs and 17.6% faster on
  4 cores, and the two CPU sets give the same times. This agrees with the merge agent's reading on the judge lanes.
- A quiet box does not remove it: on schedutil with nothing beside it rc-8 is 10.7% slower than stock, as it was with
  the judge job beside it (+10.1%).
- The restore keeps about one CPU busy (stock 1.04, rc-8 1.31 of 16). Under schedutil rc-8's running CPUs were
  clocked lower than stock's (2.0 against 2.6 GHz) and it used 98.7 CPU seconds against 71.4; at a fixed clock both
  use about 55. That rc-8 spreads the work over more threads and so gives schedutil less load per CPU is an
  inference from these numbers, not a measurement.
- Consequence: F6 is not a defect of the judged commit on the judge's set-up, and not cache layout or a neighbour.
  It remains true that on a machine with many idle CPUs under schedutil a restore of very many directories can be
  about 10% slower than stock (4.8% at 2,020,200 directories in C10; 7.8% at 1,010,100 in the partial larger run),
  while it is 27% faster on four cores under the same governor.

Notes on the run: the first attempt to set the governor (20:15:46Z) wrote nothing, because the harness's own guard
refused on the firmware table `freqdomain_cpus` (it pairs 18/19, 22/23, 66/67, 70/71); the guard was narrowed to the
kernel policy (one CPU each) and the governor set at 20:18:13Z. CPUs 19, 22, 67, 70 were never written and were
already on performance. The boost switch was never written.

Evidence: results/final3-b8cf439/x11-606060/ (18:27Z), x11-606060-performance/, x11-606060-schedutil-quiet/,
x11-large-partial/, cpufreq/ (saved state, set and restore records, clock samples); copies in
`Leagues\evidence\restic-final-20261007\release-checks-final3-b8cf439\`. Tools: checks/tools/cpufreq_state.py,
checks/tools/x11_clock_table.py.

## What the harness does not cover

- macOS and the BSDs; Windows, including volume snapshots (VSS). Not possible on the box.
- FUSE mount tests: switched off in both arms (RESTIC_TEST_FUSE=0), because the checks were not allowed to mount
  anything or change host state on a machine that was running judge jobs.
- An index written by rustic.
- The full harness on the published commit itself (only the export diff and the one new test; the binaries of rc-8
  and rc-9 are identical apart from build-ID notes).
- Real remote backends (S3, B2, Azure, GCS, rclone, a real network). The remote routes used are a REST server on a
  unix socket with injected latency and faults, and SFTP through a local sftp-server process.
- `-race` with count 3 (count 1 used). The 9 S020 witness files were not installed in C13: the mechanism they exercise
  is not in this tree, which C13 confirms by the file check and the strace.

## Stock behaviours met on the way (stock does the same; not ours)

- `check` on a tree whose node name holds a raw quote panics with "tree was not read completely".
- A restore over a FIFO that is in the way of a file waits forever and ignores SIGTERM (3 of the 75 C08 cases, both
  binaries).
- Stock's `cmd/restic` tests do not compile for 386.
- 5 own-test failures and 3 failures under -race occur on stock in the same set-up.

## History: earlier trees

| tree | label | result |
|---|---|---|
| `repaired`, commit 01d1452 (S019-merge + repairs R1 to R11) | `repaired` | interim report of 06:31Z. Found F1 (386 compile error in a test file), F2 (`prune --repack-uncompressed` never came to rest), F3 (flaky own test). R1: unrepaired 33 of 480 broken, repaired 0 of 480 |
| first final tree, commit d708925 | `final-d708925` | C01 to C09, C11 to C13 PASS; C10 FAIL on part B = F5: restore kept 104 bytes of peak RSS per directory (2,020,200 directories: 230 MiB against stock 57 MiB). F1, F2 gone. C05 had 14 kinds then (896 comparisons, 0 differ) |
| rc-6, commit 3509049 | `final2-3509049` | C01 to C09, C11 to C13 PASS (C03 unrepaired 37 of 480, rc-6 0 of 480); the run was stopped before C10, C14 to C16 when the tree moved by one commit |
| rc-7, commit d4f7e81 | `final2-d4f7e81` | every check C01 to C16 PASS: C01 2,664 / 3,017 / 1,360 / 1,796 tests, 0 failures of ours; C02 2,392 tests, 0 races; C03 unrepaired 35 of 480, rc-7 0 of 480; C04 400 trials, 0 problems; C05 1,200 comparisons, 0 differ; C14 42 disturbed prunes, 0 problems. F5 fixed (2,020,200 directories 59.7 MiB against stock 56.2 MiB). Directory restore with 16 CPUs slower than stock: 263.4 s against 226.8 s at 2,020,200, 85.3 s against 67.8 s at 606,060 rotated (+25.8%); on 4 cores 10.5% faster |

Status of the numbered failures on the judged commit: F1 fixed; F2 not present; F3 passed in all four passes of C01;
F5 fixed; F6 open, performance only. There is no F4 (a C06 size alarm was withdrawn as a harness defect).

## Re-run

`bash /opt/league/restic-final/checks/run_all.sh <tree> <label>`: two lanes of about 77 minutes, then C10 alone
for about 43 minutes. Diagnostics by name, for example
`CHK_X11_TOPS=60 CHK_X11_ROUNDS=3 CHK_EXTRA_TREES=rc7=<rc-7> bash run_all.sh <tree> <label> x11`.
One status line per check in `results/<label>/status.txt`, table in `results/<label>/table.md`, logs beside them.
