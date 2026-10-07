# Release checks: the harness's result lines

The tables of release checks C01 to C16 are in `league/docs/SOUNDNESS.md`, section 8. This file quotes every
result line of the harness's status files, which are carried unchanged (path rule aside) in
`league/evidence/release-checks-final3-b8cf439/`: `status.txt` for the judged commit `b8cf439` (label
`final3-b8cf439`, tree `rc-8`) and `published-commit-8f9372b/status.txt` for the published commit. What each
check does is in `league/docs/METHODS.md`, section 10; the check agent's report is
`league/reviews/checks/REPORT.md` and its step log `league/reviews/checks/STEP-LOG.md`. All checks: Linux
x86-64, real binaries, 2026-10-07 (UTC). "stock" is upstream restic, "tree" the commit under test.

## Judged commit `b8cf439`: 16 of 16 checks passed, and 4 diagnostics

- **C01 PASS** (finished 2026-10-07T17:15:22Z, 319 s): amd64 as root 2692 tests in 62 packages: 2662 pass, 3 fail, 27 skip; as uid 65534 3045 tests: 3009 pass, 0 fail; with DEBUG_LOG 1383 tests: 1377 pass, 1 fail; 386 (19 touched packages) 1818 tests: 1807 pass, 2 fail; failures ours=0, also on stock=5

- **C02 PASS** (finished 2026-10-07T17:32:42Z, 1039 s): 2426 tests under -race in 3 runs; packages with DATA RACE: 0; failures ours=0, witness=0, also on stock=3; witness files in=16 left out=2; witness tests pass=53 fail=2

- **C03 PASS** (finished 2026-10-07T17:39:27Z, 1764 s): R1 BROKEN snapshots: before 37/480 (uniform 2/240, aimed 35/240), after 0/480, stock 0/120; after DAMAGED 0 HANG 0; S015 repro after: restore 0/100 ls 0/100 failed

- **C04 PASS** (finished 2026-10-07T18:03:27Z, 1260 s): 400 trials on the tree (397 hit the process while it ran): 0 with a problem; stock control 198 trials, 0 with a problem

- **C05 PASS** (finished 2026-10-07T17:44:50Z, 322 s): 60 damaged repositories (15 kinds x 4 rounds), 1200 route comparisons with stock: 0 differ

- **C06 PASS** (finished 2026-10-07T18:08:10Z, 282 s): live matrix 16 cells (0 with a problem); fixtures 17 compared (0 differ), 2 archives skipped (not repositories or unknown password); repository size tree vs stock, same init, in 11 comparisons: largest difference +0.0005%

- **C07 PASS** (finished 2026-10-07T17:50:42Z, 352 s): 7 of 7 backup steps with equal exit code, tree ID and listing (5120 MiB file; devices yes, ACL yes); unreadable as uid 65534: exit 3/3; restore stock vs tree: 0 differences in 226 entries; R8 cases equal: 3 of 3

- **C08 PASS** (finished 2026-10-07T18:13:07Z, 296 s): 75 restore cases by both binaries: 0 differ; repair packs with one connection: tree rc 0 in 1.3 s (unrepaired tree: rc 0), stock rc 0; prune and copy through REST with 1 and 2 connections: equal to stock

- **C09 PASS** (finished 2026-10-07T17:56:19Z, 334 s): fs+witness as root: 138 pass / 0 fail (witness {'TestWitnessLinuxPathStatEqualsStock': 'pass', 'TestWitnessLinuxMakeReadableEqualsStock': 'pass'}); as uid 65534: {'rc': 0, 'passed': 40, 'failed': [], 'skipped': 0}; 386: 138 pass / 0 fail; wait-work tests: {'TestRestoreFailedLockConfirmationDrainsPreparedIndexBeforeUnlock': 'pass', 'TestRestoreSnapshotLookupErrorDrainsPreparedIndexBeforeUnlock': 'pass', 'TestLsFailedLockConfirmationDiscardsPreparedSnapshotBeforeUnlock': 'pass'}; R6 70 cases, 0 differ from stock; R7 7 names, 0 differ from stock

- **C10 PASS** (finished 2026-10-07T19:40:14Z, 2575 s): A 500 index files, 50 ms: 1 CPU stock 11.82s / tree 11.56s (in flight 5 / 5), 2 CPUs stock 11.8s / tree 11.47s; B 2020200 dirs restore RSS stock 56.2 / tree 58.0 MiB, before 627.9, rc7 58.6 (wall s: stock 221.6, tree 232.3, rc7 261.3); C 50 GiB file restore RSS stock 88 / tree 67 MiB; D largest index 30000000 blobs: backup RSS stock 3288 / tree 3047 MiB

- **C11 PASS** (finished 2026-10-07T18:02:09Z, 349 s): 12 prune pairs on the 1.5M-blob repository (variants ['collector', 'fallback', 'routes']; [3724, 24724, 192233] index entries in the packs to repack): 0 differ from stock; stock check --read-data of every tree result: pass; kept index file damaged before the rewrite: prune exit stock 1 / tree 1, stock check exit 1 / 1

- **C12 PASS** (finished 2026-10-07T18:17:22Z, 255 s): 40 kill -9 trials on the tree (40 hit a running prune; 24724 index entries in the packs to repack): 0 with a problem; stock control 10 trials, 0 with a problem

- **C13 PASS** (finished 2026-10-07T17:42:27Z, 584 s): dropped-mechanism files present: 0 groups; Linux-only tests {"found": 33, "pass": 31, "fail": 0, "skip": 2}; S020 witnesses {"pass": 0, "fail": 0, "files_in": 0, "files_left_out": 9}; race {"race-index-data": {"tests": 425, "failed": 0, "races": 0}, "race-repository-prune": {"tests": 305, "failed": 0, "races": 0}}; R1 tests with 3000 schedules: 10 pass, 0 fail; opt-in tests: {"internal/repository TestLargeCheckRejectsUnrestorableBlob (RESTIC_LARGE_CHECK_TEST=1)": "pass", "internal/repository TestLargeCheckHeaderChangingAttempts (RESTIC_LARGE_CHECK_TEST=1)": "pass", "internal/repository TestLargeCheckHeaderSettlement (RESTIC_LARGE_CHECK_TEST=1)": "pass", "internal/repository TestLargeCheckHeaderDispatchBoundary (RESTIC_LARGE_CHECK_TEST=1)": "pass", "cmd/restic TestSaveVerifyRealLocalPartialUpload (RESTIC_TEST_STOCK_BINARY=<stock binary>)": "pass"}; strace opens of FIFO/device per backup: {"stock": [0, 0, 0, 0, 0, 0], "tree": [0, 0, 0, 0, 0, 0], "S020 incumbent (positive control)": [3, 3, 3, 3, 3, 3]}

- **C14 PASS** (finished 2026-10-07T18:11:28Z, 559 s): 49 disturbed prunes on the tree (49 hit a running prune; 4 kinds of backend failure): 0 with a problem; stock control 24 trials, 0 with a problem

- **C15 PASS** (finished 2026-10-07T18:12:05Z, 36 s): 24 backups compared (4 verbosities x text/json x 3 backups), 1533 stdout lines of stock: 0 differ

- **C16 PASS** (finished 2026-10-07T18:20:01Z, 158 s): copy end to end: 4 of 4 pairs without a problem; failing source pack with a stalled destination: tree returns the error in 6 of 6 runs (stock 6 of 6); stock copy tests passed: {"stock as root": "4/4", "tree as root": "4/4", "tree as uid 65534": "4/4", "tree with the 200 ms lock wait": "4/4"}

- **X08 PASS** (finished 2026-10-07T18:26:41Z, 399 s): repair packs, one connection, 6 runs each: stock finished 6 (median 1.3 s); tree finished 6, hangs 0 (median 1.2 s), stock check failed 0; unrepaired tree finished 0, hangs 6 at the 60 s limit (with two connections: rc 0 in 1.1 s)

- **X11 PASS** (finished 2026-10-07T18:57:08Z, 1805 s): rotated order; median wall time and peak RSS: 606060 dirs, all 16 check CPUs: stock 67.2 s / 57.0 MiB, tree 74.0 s / 58.1 MiB (+10.1%); 606060 dirs, 4 cores: stock 86.5 s / 62.2 MiB, tree 63.3 s / 56.2 MiB (-26.8%)

- **X11 PASS** (finished 2026-10-07T20:33:44Z, 895 s): rotated order; median wall time and peak RSS: 606060 dirs, all 16 check CPUs: stock 53.3 s / 58.5 MiB, tree 44.7 s / 60.9 MiB (-16.1%); 606060 dirs, 4 cores: stock 53.5 s / 62.2 MiB, tree 44.1 s / 57.0 MiB (-17.6%)

- **X11 PASS** (finished 2026-10-07T20:48:09Z, 620 s): rotated order; median wall time and peak RSS: 606060 dirs, all 16 check CPUs: stock 68.0 s / 58.5 MiB, tree 75.3 s / 66.0 MiB (+10.7%)

## Published commit `8f9372b`

- **X13 PASS** (finished 2026-10-07T18:26:56Z, 9 s): TestAsyncSaveResultSuppressionIsNarrow in ['./internal/repository']: plain pass, -race x3 pass; files that differ from the other export: 2
