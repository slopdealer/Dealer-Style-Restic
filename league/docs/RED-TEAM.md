# The independent review before release ("red team")

After the tree had passed the league's judge, the soundness reviews, the repairs and the release checks, it was given
to three reviewers who had seen none of that. This document says what they were given and told, what they found,
what was done about each finding, and how the fixes themselves were reviewed. "Upstream" is restic 0.19.1-dev at
`ba802d42`; the reviewers' reports call it "stock".

The short form:

- Three reviewers of a different model family from the earlier reviewers, about 40 minutes each.
- **13 findings in the three reports, every one reproduced by its reviewer with a test.** One was found by two
  reviewers, so 12 are distinct. The table of section 2 shows them in 11 rows, because the two findings about stored
  encoding share a row.
- **No reviewer found a case of data loss, a wrong restore, or an incomplete backup reported as success.**
- Five findings carry the reviewers' label BLOCKER. The brief defined BLOCKER to include "a missing integrity check".
  All five are of that kind: this build read once, or did not bound, something upstream reads twice or bounds. None is
  a loss of data by the program.
- **Of the 11 rows, nine were fixed**, one commit each, each with the reviewer's test as a regression test. One of the
  nine (row 2) was fixed in part, with the rest documented. **Two rows were documented and not changed**; they are
  differences from upstream that a user must know and are stated in [KNOWN-LIMITS.md](KNOWN-LIMITS.md), sections 5.1
  and 5.2.
- The fixes were then reviewed by an independent reviewer agent: nothing above LOW; 25 mutations of the fixes, 23
  caught by the tree's own tests; the two misses led to one more test, which is in the tree.
- That second review stated one limit of the lock-wait validation more exactly than anyone had before. It is not
  fixed; it is documented ([KNOWN-LIMITS.md](KNOWN-LIMITS.md), section 5.3).

Sources, as scrubbed copies: the triage [`REDTEAM-TRIAGE.md`](../reviews/soundness/REDTEAM-TRIAGE.md); the three
reports [`REDTEAM-seat1.md`](../reviews/soundness/REDTEAM-seat1.md) (locking and concurrent processes),
[`REDTEAM-seat2.md`](../reviews/soundness/REDTEAM-seat2.md) (prune, repack, index, check),
[`REDTEAM-seat3.md`](../reviews/soundness/REDTEAM-seat3.md) (backup write path, encryption, compression, restore);
the review of the fixes, "Addendum 2" of [`REVIEW-final.md`](../reviews/soundness/REVIEW-final.md).

## 1. What the reviewers were given and told

**Given:** two source trees in one folder and nothing else. `stock/` was upstream restic at `ba802d42`; `tree/` was
the league's tree as it stood then (the working commit `d4f7e81`, the second final tree, 37 commits after the
league's tree). No report of an earlier review, no list of repairs, no history, no network.

**Told**, in the words of the brief:

> The modified version is meant to be a drop-in replacement for upstream: the same repository format and the same
> stored bytes; the same user-visible behaviour, output, exit codes and defaults; every integrity check that upstream
> performs is still performed on the same data; correct when several restic processes use one repository at the same
> time; nothing lost if a process stops at any point; no data loss in backup, forget, prune, repair or copy; restores
> are exact.
>
> YOUR TASK: review the changes in your focus area and look for correctness regressions against upstream. That means
> any situation (starting state, commands or inputs, timing, I/O errors) in which the modified version would lose
> data, restore different data, report success for an incomplete backup or a damaged repository, skip an integrity
> check that upstream performs, hang, crash, use memory without bound, or otherwise behave differently from upstream
> in a way a user would notice. Prefer a few well-supported, concrete, checkable findings to many shallow remarks.

Each reviewer had one focus area with a list of files to start from and a list of questions (for the first:
"can any command use state it read before its lock was confirmed when another process ... changed the repository in
between?"). Each was asked to give, for every finding, files and lines, the scenario step by step, why upstream does
not have the problem, a severity, a confidence, whether it was reproduced, and where possible a Go test.

**The severity scale of the brief:** "BLOCKER = data loss, silent wrong data, an incomplete backup reported as
success, or a missing integrity check; HIGH = wrong result, crash or hang under plausible conditions; MEDIUM = a
regression or behaviour change; LOW = code hygiene". The label BLOCKER in the table below is therefore the brief's,
and covers a skipped check as well as lost data. The contract in the brief was written deliberately strict ("the same
stored bytes", "every integrity check ... on the same data"). Two of the findings are exactly the places where the
tree did not meet that wording, and the published claim was changed to what is true (section 4).

The brief allowed about 60 minutes; the reviewers took about 40 each.

## 2. The findings and the triage

Numbering is the triage's. "Fix N" is the name the fixing commit carries ("Red team fix N"). The numbers of the fixes
follow the order in which they were made and are not the numbers of the findings. Section 3 maps fixes to commits and
patch files. Regression tests for the repaired findings are included and named below. The reviewers' original
witnesses for the two documented differences remain in the review reports; adapted tests and the reasons for
adaptation are recorded in the final review.

| # | reviewer, their label | finding | what was done | test that pins it |
|---|---|---|---|---|
| 1 | seat 2, BLOCKER | `check --read-data` decoded compressed blobs as a stream without upstream's ceiling on total decoded bytes. A crafted blob that expands past the limit (64 GiB + 1 byte in the test) passed check, while restore and upstream's check reject it. | **Fixed** (fix 1). The stream's output is counted against the same limit, 64 GiB; output beyond it fails with the decoder's own error in upstream's wrapping; a first frame that declares more than the limit is refused before any of it is decoded. | `TestCheckStreamKeepsCumulativeDecodeLimit`, `TestCheckStreamRefusesDeclaredOversize`; at production size, opt-in: `RESTIC_LARGE_CHECK_TEST=1`, `TestLargeCheckRejectsUnrestorableBlob` (`internal/repository/check_decode_limit_test.go`) |
| 2 | seat 1 BLOCKER; seat 2 BLOCKER | What a command read during the 200 ms lock wait was validated after confirmation by file NAME only. An index file damaged on storage inside the wait window, after this build had read it, was not noticed by that run; `check`, also with `--read-data`, reported a damaged repository as healthy. Upstream reads after confirmation and notices. No restic process can cause it (index names are content hashes); it needs storage damage in that window. | **Fixed in part, the rest documented** (fix 6). Every file kept from the wait must be listed after confirmation with the size it had when it was read. `check` starts no reads during the wait any more: it takes upstream's lock call and reads in upstream's order. For the other commands a residue remains: damage in place that leaves the length alone, inside the window (KNOWN-LIMITS.md, section 5.4). | `TestCheckReportsIndexDamagedBeforeLockConfirmation` (an index cut short and a same-size bit flip, with and without cache, production 200 ms delay), `TestLockWaitReadsRejectResizedIndexFile`, `TestPreparedSnapshotKeptOnlyWithListedSize` (`cmd/restic/lock_wait_damage_test.go` and beside it) |
| 3 | seat 2, BLOCKER | Prune's index rewrite kept index files that need no change without the re-read upstream performs. An index that became unreadable or damaged between the load and the rewrite was not noticed, and prune went on to delete packs. | **Fixed** (fix 7). Before a file is kept unread it is read raw and its SHA-256 is compared with its name, by upstream's `LoadRaw`; a failure aborts the rewrite with upstream's message, before anything is removed. | `TestRewriteRejectsDamagedKeptIndex`, `TestRewriteFailsOnChangedKeptFile`, `TestRewriteKeepsUnread` |
| 4 | seat 3, BLOCKER | Restore keeps the directory trees it loaded and verified in its first pass and uses them in its second pass; upstream loads them again. A tree damaged on storage between the two passes fails upstream's restore and not this build's (the witness runs with the local cache disabled). | **Documented, not changed.** What is restored comes from data that was authenticated and hash-checked when read, and the result is the snapshot's content. The difference: upstream reports storage damage to these objects that occurs during the restore, and this build does not. KNOWN-LIMITS.md, section 5.1. | the reviewer's `TestReviewSeat3RestoreRechecksTreeIntegrity` still fails on this tree, as triaged; it is not in the tree |
| 5 | seat 2, HIGH | Repack and copy submitted saves with the outer context instead of the worker group's, so a read error could not cancel a save that was blocked behind stalled uploads: the command hung where upstream returns the error. | **Fixed** (fix 2). Saves run on the context of the repack's workers, as in upstream. | `TestRepackReadErrorCancelsQueuedSave`, `TestAsyncSaveResultSuppressionIsNarrow` |
| 6 | seat 2, HIGH | The index kept per-file notes that were never released when the index was cleared and loaded again: memory grew in a long-lived process that sees many index replacements (`mount --no-lock`). | **Fixed** (fix 3). | `TestIndexNotesReleasedOnReload` |
| 7 | seat 1, HIGH | After confirmation the validation listed the index before `restore` had read the selected snapshot; upstream lists the index after. On a backend whose listings lag, a restore of a just-written snapshot failed ("There were 2 errors", nothing restored) where upstream succeeds. | **Fixed** (fix 4). For every command the validating listing is taken no earlier than the point where upstream lists that directory; nothing is listed at confirmation any more. | `TestRestoreListsIndexAfterSnapshotRead`, `TestLockWaitIndexListedAfterSnapshotRead` (`cmd/restic/lock_wait_visibility_test.go`) |
| 8 | seat 1, MEDIUM | Diagnostics from work done during the wait (for example a failed index listing) reached stderr even when the lock was then refused; upstream prints only the lock error. | **Fixed** (fix 5). The retrying backend holds its reports for the reads of the wait; they are shown when those reads are used and dropped otherwise. One diagnostic of the cache layer is not held (KNOWN-LIMITS.md, section 5.5). | `TestRefusedLockShowsNoWaitDiagnostics`, `TestWaitDiagnosticsFollowTheUseOfTheReads`, `TestHeldReports` |
| 9 | seat 3, MEDIUM | Batched terminal output dropped the next message when a write error landed exactly on a message boundary; upstream still writes it. | **Fixed** (fix 8). | `TestBatchErrorAtMessageBoundary`, `TestBatchOnFullDiskFollowsMessages`, `TestCollectedOutputFailsLikeLineByLineOutput` |
| 10 | seat 3, MEDIUM | The byte budget of restore's tree prefetch counted only the visible entries of a node's extended attributes, not the backing tail of the slice; non-canonical tree JSON could hide megabytes from the budget. | **Fixed** (fix 9). | `TestPrefetchBudgetCountsHiddenAttributeTail` |
| 11 | seat 2 MEDIUM; seat 3 MEDIUM | Index files are compressed by a streaming encoder and lock files by a hand-written raw-block frame: valid Zstandard, readable by upstream, but a few bytes different from what upstream's encoder writes for the same content. The lock frame ignores the chosen compression level. | **Documented, not changed.** Data and tree blobs are stored exactly as upstream stores them (checked by reading stored entries back). The published claim is "identical chunking, blob IDs and blob storage; index and lock files decode to the same content", not "the same stored bytes". KNOWN-LIMITS.md, section 5.2. | the three stored-bytes parity tests of the reviewers still fail on this tree, as triaged; they are not in the tree |

**Counting.** Seat 1 reported 3 findings, seat 2 reported 6, seat 3 reported 4: 13. One defect was found by two
seats (row 2: seat 1's first and seat 2's second), so 12 findings are distinct. They are shown in 11 rows because
the two findings about stored encoding, index files from seat 2 and lock files from seat 3, share row 11.
Labels: BLOCKER five times (rows 1, 2 twice, 3, 4), HIGH three times (rows 5, 6, 7), MEDIUM five times (rows 8, 9,
10, 11 twice).

**What the reviewers examined and judged correct** is listed in each report: lock settle timing in 44 cases; index
removal and addition under current listings; prune's keep, repack and delete decisions; repack completion and
ownership; the chunkers against upstream for polynomial degrees 8 to 53; save verification; key derivation and its
parameter checks; the prepared cipher state; the tree decoders on 20,000 payloads; restore offsets, hard links and
sparse files; the restore workers and their budgets; the terminal batching.

## 3. The commits

<!-- BEGIN GENERATED:redteam-commits -->
| commit | patch | subject | non-test files | test files |
|---|---|---|---|---|
| 40 | `0038` | Red team fix 9: the restore prefetch budget counts the hidden tail of a node's extended attributes | `internal/restorer/tree_carry.go`, `internal/restorer/tree_prefetch.go` | 1 |
| 41 | `0039` | Red team fix 8: collected terminal output keeps the message boundaries | `internal/ui/termstatus/status.go`, `internal/ui/termstatus/stdio_wrapper.go` | 1 |
| 42 | `0040` | Red team fix 3: the index releases its per-file notes when it is cleared | `internal/repository/index/master_index.go` | 1 |
| 43 | `0041` | Red team fix 7: an index file the rewrite keeps unread is first compared with its name | `internal/repository/index/index_rewrite_keep.go`, `internal/repository/index/master_index.go` | 2 |
| 44 | `0042` | Red team fix 2: repack and copy save on the context of their workers | `internal/repository/repack.go`, `internal/repository/repack_completion_overlap.go`, `internal/repository/repack_scope.go`, `internal/repository/repository.go` | 1 |
| 45 | `0043` | Red team fix 1: check's streaming decode keeps upstream's ceiling on decoded bytes | `internal/repository/check_decode_limit.go`, `internal/repository/repository.go` | 1 |
| 46 | `0044` | Red team fix 6: files kept from the lock wait are compared by size too, and check keeps nothing | `cmd/restic/cmd_check.go`, `cmd/restic/cmd_ls.go`, `cmd/restic/snapshot_metadata_wait.go`, `cmd/restic/wait_index_load.go`, internal/checker/snapshot_list.go (removed), `internal/repository/index/index_rewrite_keep.go`, `internal/repository/index/master_index.go`, `internal/repository/index_loaded_ids.go` | 9 |
| 47 | `0045` | Red team fix 4: the listings that validate lock-wait reads are taken where stock lists | `cmd/restic/wait_index_load.go` | 2 |
| 48 | `0046` | Red team fix 5: nothing is heard of the reads of the lock wait unless they are used | `cmd/restic/wait_index_load.go`, `internal/backend/retry/backend_retry.go`, `internal/backend/retry/held_reports.go` | 2 |
| 49 | `0047` | Many directories: restore hands work between its goroutines per batch, not per directory | `internal/restorer/node_lookahead.go`, `internal/restorer/tree_prefetch.go` | 1 |
| 50 | `0048` | Release: the build names itself, and a release build can set the version string | `internal/global/global.go` | 0 |
| 51 | `0049` | Review of final3: the kept index's contract stated exactly, and a witness for fix 2's suppression | `cmd/restic/wait_index_load.go` | 1 |
<!-- END GENERATED:redteam-commits -->

Three of these are not fixes of findings:

- **"Many directories"** follows from a release check, not from the red team. Both passes of restore passed every
  directory through another goroutine and back, which cost CPU time that grew with the number of CPUs
  ([RESULTS.md](RESULTS.md), section 6.2). Work is now handed over per batch.
- **"Release"** makes the version string a variable with the value `0.19.1-dev-dealer.1 (compiled manually)`, so that
  a build from this source says which build it is and a release build can set the string. Two consequences a user
  will see are in KNOWN-LIMITS.md, section 5.6.
- **"Review of final3"** is the outcome of the second-stage review (section 5): one comment rewritten and one test
  added. No code changes. The official judge jobs measured the commit before it
  ([RESULTS.md](RESULTS.md), section 2).

One sentence in a commit message is wrong and was left as written, because commit messages are replayed unchanged. The
message of "Red team fix 4" ends "With any backend this build knows at each point what stock knows there". The
second-stage review showed that this claims more than the code does (it knows what upstream's *listing* knows;
upstream also reads the files). The last commit corrects the same sentence in the source comment and says so.

What each fix cost in time is in [RESULTS.md](RESULTS.md), section 5.

## 4. What was documented instead of changed

**Finding 4, restore's trees.** The reviewer's scenario, from the report: restore loads, authenticates, hashes and
decodes a tree in its first traversal and this build retains the decoded nodes. While the file contents are being
restored, a disk fault or a concurrent change damages one byte of that stored tree blob. This build's second pass
takes the retained record and does its metadata traversal without loading the stored tree again, and returns success.
Upstream calls its tree loader in both traversals and its second traversal rejects the ciphertext. The reviewer adds:
"the test does not claim the already authenticated file bytes were restored incorrectly. The retained copy was valid
when first read, which is why this finding specifically requires damage between the traversals."

Why it is kept: the retained trees are what makes the second pass cheap, and what they hold was verified. The
retention is bounded (a cumulative budget of 512 KiB of decoded nodes per restore, `internal/restorer/tree_carry.go`);
a tree that was not retained is loaded and verified again in the second pass, as upstream does. So the difference
from upstream is: for the retained trees, storage damage that happens *during* one restore is reported by upstream,
when its second load reaches the damaged storage, and not by this build. The reviewer's witness runs with the local
cache disabled. With a local cache, a valid cached copy of the pack that holds the tree hides the same damage from
upstream as well ([KNOWN-LIMITS.md](KNOWN-LIMITS.md), section 5.1). What this build restores is the content of the
snapshot.

**Finding 11, the encoding of index and lock files.** These two kinds of file are written by encoders that produce
valid Zstandard which decodes to the same content, and upstream reads them. They are not byte for byte what
upstream's encoder writes. Data blobs and tree blobs are not affected: they go through the same encoder with the
same options as upstream, and release check C06 and the reviewers' own read-back tests found them stored as upstream
stores them. The headline claim was changed because of this finding: "identical chunking, blob IDs and blob storage;
index and lock files decode to the same content".

## 5. The review of the fixes

The eleven commits from the reviewed tree to the judged commit were reviewed by an independent reviewer agent, in
the manner of the earlier soundness reviews ([METHODS.md](METHODS.md), section 9). 21 non-test files, +652 / -429
lines. Verdict: **nothing above LOW; keep all eleven.**

What was run:

- **The reviewers' original tests, unchanged, on the tree they reviewed:** every finding reproduces, 16 test
  functions fail as the reports say.
- **The same tests on the fixed tree:** the tests of the fixed findings pass where they can still be compiled and
  run as written; the tests of the two documented findings still fail, as triaged. Seven original tests cannot show
  their fix as written, each for a stated reason that is the fix or the harness (for example: two drive a function
  of `check`'s wait work that no longer exists; one waits for an early index listing that fix 4 removed). For each of
  those a carried test covers the same scenario.
- **The lock-wait repair R1 after fixes 4 and 6:** the six witnesses pass; the randomized schedule test ran 3,000
  schedules in each of four listing-lag modes: 12,000 schedules, 16,469 confirmed locks, 12,289 of them with reads
  during the wait, 0 failures of any kind.
- **Restore before and after the batching commit:** 300 random scenarios (faulty trees, stop or continue,
  cancellation, `--delete`, overwrite modes, filters, hard links); for each a digest of the ordered trace of the
  restore, its errors, its file count and its target was written by both builds: 300 of 300 identical.
- **Mutations:** 25 deliberate breakages of the fixes. 23 were caught by the tree's own tests. **Two were not**: both
  widen the one place where fix 2 withholds an error from the uploader's group (to every cancelled save; to every
  error of a repack save). Neither loses an error by itself, because the save's callback still delivers it, but each
  removes a second line of defence. Only the reviewer's own witness caught them. That witness is now in the tree as
  `TestAsyncSaveResultSuppressionIsNarrow` (`internal/repository/repack_scope_test.go`).

The LOW observations of that review, all of them known and kept: the decode ceiling of fix 1 is the constant 64
GiB, upstream's default, and would not follow a decoder that was given another limit; one diagnostic of the cache
layer during the wait is not held back by fix 5; a message that spans a flush of the output collector counts as two
or three messages when a write fails inside it; a performance detail of the batching; and the two consequences of
the version string.

**The contract it stated.** For the commands other than `check`, an index kept from the lock wait is validated after
confirmation by a listing, name and size, taken where upstream lists. The files are not read again. In the review's
words: "a listing taken after confirmation shows an index file under its old name and size only if that file can still
be read with the content it had." On a backend whose listing can still show a file after it was deleted, that does not
hold, and the outcome the R1 repair was made for is possible again in that corner. This is the design of the R1 repair
since it was made; the fixes only narrowed it. It was not in the first triage; the triage now states it under "After
the fixes". In this build the outcome needs two things at once (a lock-file write slow enough for a prune to finish
inside the window, and a listing that lags the deletion). Whether upstream differs there depends on its local cache.
It is a known limit of this release: [KNOWN-LIMITS.md](KNOWN-LIMITS.md), section 5.3.

## 6. Limits of this review

As the triage and the second review state them:

- One workstation, Windows, no race detector. (The race detector was run on the fixed tree on Linux afterwards, as
  part of the merge agent's checks, over the new concurrent code: clean.)
- No real backend with lagging listings: findings 2 and 7 and the contract of section 5 use modelled backends.
- No runs with several real processes. The real-binary test of the lock wait is release check C03, which ran on
  the judged commit after the fixes: 0 broken snapshots in 480 trials ([SOUNDNESS.md](SOUNDNESS.md), section 8.1).
  The release checks on the judged commit also exercised three of the fixes: with real binaries, a kept index file
  damaged before prune's rewrite (C11) and a damaged blob while every upload is held (C14); and, as a test at
  production size, check's decode ceiling (C13).
- About 40 minutes per reviewer. Three reviewers found twelve distinct things in two hours; that is evidence
  about the tree before the fixes and no proof about the tree after them.
- What the reviewers judged correct was taken as written by the second review; it was not re-examined.
- The second review ran on a memory backend on Windows. Its list of things to run on Linux is in its section B6, and
  the merge agent's log records the race detector, the schedules and the production-size test of fix 1 as run there.
