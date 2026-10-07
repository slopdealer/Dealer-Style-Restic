# Repository locking and concurrent-process review

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

The candidate fails the drop-in requirements in three reproduced cases: one BLOCKER, one HIGH and one MEDIUM. Scope: speculative snapshot/index reads during lock settlement, validation after confirmation, command integration, lock retries/refresh/cancellation, caches and repository/backend compatibility. Baseline: `stock/`, restic 0.19.1-dev at ba802d42; candidate: `tree/`.

All reproductions and checks were offline. Production files were not edited. Reviewer tests are in `tree/cmd/restic/review_lock_wait_test.go`; failing tests deliberately assert the required upstream behavior. The comparisons use upstream's source and fresh loads or the candidate command with speculation disabled. A separate stock binary was not run.

## Confirmed findings

### R1 — BLOCKER: name-only validation accepts an index damaged before lock confirmation

**Locations:** `tree/cmd/restic/wait_index_load.go:89-108` (`validateLockWaitReads`), `:500-512` (`checkerIndexLoad.consume`); `tree/internal/repository/index_loaded_ids.go:11-16` states the assumption that equal names prove equal current content. Candidate command integration: `tree/cmd/restic/cmd_check.go:247-278`.

**Scenario:**

1. Start with a healthy repository, either with `--no-cache` or the default fresh temporary check cache. `check` creates its exclusive lock file and reads/authenticates an index during the settlement delay.
2. Before the second lock scan and validation, the stored index becomes truncated, or one byte changes while its filename stays the same. This can be storage corruption or replacement/torn writing under the original filename. No snapshot or pack needs to change.
3. The exclusive lock is confirmed. The validation listing returns the same index ID; even a changed file size is ignored. `validateLockWaitReads` returns `keepLockWaitReads`.
4. `check` uses the earlier valid index and never reads/authenticates the damaged index. Pack metadata and snapshot/tree structure remain healthy, so all of these checks return no errors. Even `check --read-data` prints `no errors were found` and returns success: the pack data remains healthy, and rereading packs does not check the damaged index file.

**Why upstream differs:** upstream starts loading indexes only after confirmation (`stock/cmd/restic/cmd_check.go:245-258`). Its `LoadRaw` verifies the bytes read against their filename hash and rejects this damaged file. A content-addressed *name* does not guarantee that the bytes currently stored under that name still have that content.

**Severity:** BLOCKER, missing integrity check / damaged repository accepted. **Confidence:** high. **Reproduced:** yes. Four lifecycle cases cover versions 1 and 2, truncation and same-size bit flip. Two additional cases exercise `runCheck` with the production 200 ms wait and `--read-data`, with and without a cache. The whole command returns a nil error and empty stderr; the post-confirmation control returns `Fatal: repository contains errors`. Damage is injected before the second lock scan returns, not during the later checking pass. This does not require any well-behaved restic writer to deliberately overwrite an index: storage damage is sufficient.

**Regression tests:** `tree/cmd/restic/review_lock_wait_test.go:24`, `TestReviewIndexCorruptionBeforeConfirmation`, and `:143`, `TestReviewCheckCommandAcceptsIndexDamagedDuringWait`. The lifecycle test deliberately fails because the candidate reports no check errors; the fresh control reports `LoadRaw(...): invalid data returned`. Truncation examples reduced index sizes from 436 to 218 bytes (v1) and 313 to 156 bytes (v2); the same-size cases also fail validation of actual bytes in the control. The command test truncates the index to one byte before confirmation and still obtains success.

Checking sizes would catch the truncation case, but does not resolve the equal-size case. Reuse must not substitute filename equality for the integrity check on the post-confirmation object.

### R2 — HIGH: restore can freeze index visibility before loading the selected snapshot

**Locations:** `tree/cmd/restic/wait_index_load.go:58-68`, `:89-108`, `:297-320`; `tree/cmd/restic/cmd_restore.go:163-171`. The validation runs independently after confirmation and can finish its index listing before `FindLatest` finishes loading snapshot contents.

**Scenario:**

1. Another backup has published snapshot B, its packs and a new index J. The repository is intact. On an eventually consistent backend, the snapshot listing already contains B but the early index listings temporarily omit J.
2. Restore's speculative pass loads only the old index I. After lock confirmation, validation lists both snapshots and lists indexes while J is still absent. All loaded IDs are present, so it chooses `keepLockWaitReads`.
3. A slow download of B's snapshot contents completes after that validation listing. During this interval, index listing visibility catches up: a listing at this point would return I and J.
4. `FindLatest` selects B, but `consume` uses the earlier verdict without listing indexes again. Restore cannot find B's tree in its in-memory index and fails on a healthy repository.

**Why upstream differs:** `stock/cmd/restic/cmd_restore.go:155-160` finishes snapshot selection/loading *before* starting `LoadIndex`. On the same visibility sequence its index listing sees J, and the restore succeeds. Listing snapshot filenames before the index does not preserve upstream's stronger ordering of snapshot-content reads before index loading. The confirmed lock does not flush a backend's delayed listings.

**Severity:** HIGH, wrong result under the explicitly supported eventually consistent/slow-backend conditions. **Confidence:** high in the demonstrated schedule; it does not require or claim failure on a strongly consistent backend. **Reproduced:** yes, both the lifecycle and the actual `runRestore` command with the production 200 ms wait. The command restored zero files and returned `Fatal: There were 2 errors`; the synchronous control restored one file, and its bytes matched the expected 224-byte content exactly.

**Regression tests:** `tree/cmd/restic/review_lock_wait_test.go:337`, `TestReviewRestoreCommandIndexVisibilityDuringSnapshotRead`, and `:402`, `TestReviewRestoreIndexVisibilityDuringSnapshotRead`. The wrapper hides only the newly added index in the early listings and exposes it when the snapshot read completes; the command test holds that read until validation's listing has finished. These are deterministic models of a listing propagation/download timing window, not claims about a particular network service measured here. Both fail on the candidate and pass their upstream-order controls.

Restore must preserve the snapshot-content-read/index-load ordering, including any reuse decision, to obtain upstream's result in this window. This finding does not assert the same regression for `ls`, whose upstream command already loads the index before loading the selected snapshot's contents.

### R3 — MEDIUM: errors from unconfirmed work escape to stderr even when locking fails

**Locations:** `tree/cmd/restic/wait_index_load.go:170-180`, `:271-279`, `:329-338`; `tree/cmd/restic/backup_index_load.go:44-73`; `tree/internal/global/global.go:616-626`; `tree/internal/backend/retry/backend_retry.go:64-66`.

**Scenario:**

1. A competing exclusive lock is absent from the first listing but visible on the confirmation scan. The attempted restore will consequently be rejected in both versions.
2. During the candidate's 200 ms wait, listing the index directory fails, for example with a permission/backend error. This speculative read uses the ordinary retry backend, whose error reporter writes directly through `printer.E`.
3. The candidate prints the index-list failure immediately. The confirmation scan then rejects its lock and `discard` joins/discards the prepared state, but cannot retract the already published diagnostic.

**Why upstream differs:** upstream reaches no snapshot/index operation until its lock is confirmed; on this schedule it only returns the competing-lock error. The delayed terminal counter buffers progress counters, not the backend's retry/error/success messages.

**Severity:** MEDIUM, observable stderr/diagnostic-order change; no data loss demonstrated. **Confidence:** high. **Reproduced:** yes through `runRestore`. Both calls reject the competing lock. Candidate stderr is `List(index) failed: injected unconfirmed index listing failure\n`; upstream-order control stderr is empty.

**Regression test:** `tree/cmd/restic/review_lock_wait_test.go:240`, `TestReviewFailedLockLeaksSpeculativeError`. It deliberately fails on the stderr mismatch. The backend error is injected below the ordinary retry wrapper, so the diagnostic travels through the production reporter.

## Examined and judged correct within the stated bounds

| Mechanism | Evaluation and evidence |
| --- | --- |
| Lock-file save and second competing-lock scan | The production `time.Sleep(200 ms)` remains between successful save and the second scan. No work starts after a failed save or a first-scan conflict. Wait work is not awaited before the second scan. Repository lock tests and all 44 duration/cancellation/conflict cases passed. |
| Error/cancellation during settlement | The full delay still occurs after successful save; errors or cancellation do not turn an unconfirmed lock into success. Failed attempts cancel/join work before removing their lock. Pending checker preparation does not postpone confirmation. Exercised duration and blocked-index cancellation tests passed. |
| Retry and refresh lifetime | A fresh load is allocated for each attempt; failed targets are cleared. The load inherits the context canceled by unlock/refresh failure. Refresh, stale-refresh handling and backend freeze/unfreeze remain upstream's logic apart from using that context. Lock and refresh tests passed. |
| Index removal/replacement under current listings | A missing loaded ID discards the old index and reloads; replacement with a new content ID therefore cannot silently preserve a pruned pack route. The three backup/prune lock-dwell witnesses passed, including saving and reading back a formerly pruned blob. This judgment excludes same-name damage (R1) and stale listings (R2). |
| Index additions under current listings | Repository commands load the listed additions; checker discards and reruns its per-file integrity pass instead of incorrectly extending its bookkeeping. Addition and replacement validation tests passed; incremental/index-stream compatibility tests passed. `LoadAddedIndexFiles` also retains the v1 compressed-blob compatibility rejection and cache preparation. |
| Snapshot additions/removals | Commands receive the listing made after confirmation. A removed snapshot causes full fallback. Existing validation tests passed for added and removed snapshots. The listing/content timing gap in restore is R2. |
| Duplicate names and listing errors | The CLI's retry backend deduplicates names, including across retried listings. Validation's maps do not bypass an additional duplicate check upstream performs. Partial/failed speculative listings are rejected, and partial/failed validation listings cause reread. Diagnostics from those reads are R3. |
| Prepared pack sizes and rewritten/removed packs | Preparation derives expected sizes only from the index. Actual pack listing stays after confirmation, and missing/orphaned/truncated packs are still checked. Tests for later pack removal/addition, completed repair, cancellation and size-map reuse passed. |
| `ls` snapshot prefetch | At most one snapshot is fetched, within a 64 KiB limit; ambiguous targets fall back. Preloaded bytes still enter the ordinary hash/authentication/decode path. Metadata workers cancel/join before failed-lock removal or command unlock. Target-bound and lifetime tests passed. Same-name object damage is subject to the same assumption as R1; no separate finding is claimed without an additional reproduction. |
| Command teardown and memory | Inspected backup/restore/ls/check/forget/prune defers, result-channel ownership and discard/consume paths. Workers are joined before normal unlock; pending unused preparation is canceled. Result/error buffering and retained snapshot payload are bounded; index/snapshot maps scale with repository metadata, not an open-ended retry log. No deadlock, worker leak or race was found by inspection or the exercised schedules. No race-detector claim is made. |
| `--no-lock`, dry run, read-only backends | Skipping a lock starts no wait work and leaves synchronous nil-load paths. A backend that rejects the lock save starts no speculative reads. Existing `TestCheckRestoreNoLock` passed. The independent `TestReviewRepositoryFileReadOnlyNoLock` also passed: check with full data reading, exact restore, ls, dry-run prune and dry-run forget/prune all succeed against a backend that rejects every write; zero writes and zero lock listings were observed. Forget/prune retain the upstream restriction of `--no-lock` to dry-run use. |
| `--repository-file` | The repository is resolved/opened once in `global.OpenRepository`, before the callback is installed. Wait work uses that handle; it does not independently reread the repository-location file. The independent command compatibility test above uses this option throughout and passed in all four v1/v2 and cache/no-cache combinations. |
| Repository version 1 and caches | Ordinary and added-index loading retain the version-1 compatibility check. Ordinary/incremental loader tests passed. R1 was independently reproduced on v1 and v2 and with the default temporary check cache as well as `--no-cache`; cache presence is not a fix. Cache cleanup can run during speculative work, but removes only local cached objects and falls back to repository reads. |
| Other repository commands | Inspected unchanged lock entry points for copy, repair index/packs/snapshots, unlock, key operations and other readers. They receive the revised lock lifetime context but do not start this speculative index work. Their broader implementations are outside this review. |

## Validation and limits

Reviewer reproductions, from `tree/`:

```powershell
$env:GOPROXY = 'off'
$env:GOSUMDB = 'off'
$env:GOTOOLCHAIN = 'local'
$env:GOCACHE = Join-Path $env:TEMP 'restic-seat1-go-cache'
go test -run '^TestReview(IndexCorruptionBeforeConfirmation|CheckCommandAcceptsIndexDamagedDuringWait|RestoreCommandIndexVisibilityDuringSnapshotRead|RestoreIndexVisibilityDuringSnapshotRead|FailedLockLeaksSpeculativeError)$' -count=1 -v ./cmd/restic
go test -run '^TestReviewRepositoryFileReadOnlyNoLock$' -count=1 -v ./cmd/restic
```

Expected result on this candidate: the first command's five regression test functions fail for the reasons above; their controls succeed or detect corruption as required. The second command's four repository-version/cache compatibility cases pass.

Other checks passed: repository lock/refresh tests; ordinary streaming and incremental index tests; prepared-pack-size tests; snapshot metadata target/cancellation/lifetime tests; delayed counter replay; validation removal/addition/replacement/error tests; lock dwell witnesses; all 44 virtual-time duration/teardown cases; 100 immediate-visibility and 50 non-atomic lock-listing/visibility-lag schedules with two or three processes. The 150 schedules reported zero stale/missing/outcome/final-state failures or goroutine leaks. These existing models do not cover the newly demonstrated index-content damage or snapshot-content/listing ordering windows.

Three existing local-fixture teardown integration tests could not reach their assertions because Windows `tar` failed to create a fixture symlink. Separate reviewer command tests use generated repository fixtures and do run. The race detector was unavailable: `CGO_ENABLED=0`, with no gcc, clang or cl on PATH; nothing was downloaded or installed. Circuit-breaker state was inspected as a possible side effect of failed speculative loads, but no separate correctness finding is claimed without an upstream-differential reproduction.

Changed files: this report, `PROGRESS.md`, and `tree/cmd/restic/review_lock_wait_test.go`. No production edits, API calls, network requests or scope additions.
