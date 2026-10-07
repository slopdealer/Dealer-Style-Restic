# REVIEW: read side of `incumbents/S019-merge` (restore, ls, check / prune / forget command layer, backend layer)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Reviewer: readside. Date: 2026-10-06. Workstation only (Windows 10, go1.27.0); no box, no API calls.
Trees: stock = `final-candidate/trees/stock` (ba802d42), candidate = `final-candidate/trees/S019-merge`.
All `file:line` references are to the candidate tree unless they say "stock".

## 1. Summary

26 mechanisms judged: the 23 that live in the 27 `readside` files, plus 3 that live in other areas' files but
that the brief names for the restore path (pack-range coalescing, the restore decoders, the tree decoder).

| Verdict | Count | Which |
|---|---|---|
| UNSOUND, BLOCKER | 1 | M1 reads taken during the lock settle delay are used after the lock is confirmed. Reproduced three ways: `backup` beside `prune` silently loses the backup (BLOCKER; the backup command is in the backup area, the mechanism is shared); `prune` and `restore` fail where stock succeeds (HIGH) |
| UNSOUND, MEDIUM | 2 | M17 hard-link index rebuilt in the second restore pass (reproduced); X3 tree decoder skips the name unquote (reproduced, other area's file) |
| UNSOUND, LOW | 1 | X1 pack blobs decoded inside the backend Load callback (reproduced twice, other area's file). LOW for the two behaviours I ran; the storage review reproduced a HIGH consequence of the same design (a deadlock in `repair packs` with one connection), see the cross-reference in X1 |
| SUSPECT, LOW | 1 | M22 per-directory state retained by restore (not reproduced) |
| SOUND-UNDER-CONTRACT | 7 | M3, M8, M12, M15, M16, M18, M20 |
| SOUND | 14 | M2, M4, M5, M6, M7, M9, M10, M11, M13, M14, M19, M21, M23, X2 |

No HIGH finding other than the HIGH face of M1.

What was run here (all on Windows; logs in `review/scratch-readside/`, witnesses in `review/witness-readside/`):

- 7 witness tests in 4 files. All 7 fail on the candidate and pass on stock (or on the stock algorithm kept in the
  candidate binary); they are the four UNSOUND rows.
- The candidate's and stock's own tests of `internal/restorer`, `internal/walker`, `internal/backend/{sema,retry,limiter,local}`:
  the candidate fails exactly the 9 top-level tests stock fails on this machine (symlink privilege, EFS, long paths:
  environment), 1,714 FAIL lines in each tree with subtests counted, identical by name; every other package passes in both.
- `cmd/restic` tests matching restore / ls / check / prune / forget and the candidate's own wait-work tests: the
  candidate passes everything stock passes (17 common passes, 12 own passes). 7 stock tests and 3 of the candidate's
  own tests cannot run here, because the tar fixture needs symlinks. Those 3 are listed under NEEDS-RUN; two of them
  were reported as failing on S015 in `race-fix/REPORT.md`.
- Cross-compilation of `cmd/restic` for linux/amd64, linux/386, darwin/arm64, freebsd/amd64, openbsd/amd64,
  solaris/amd64 and `go vet` of the Linux-only code: see section 6.

Limits of this review. Nothing Linux-only was executed (`internal/fs/file_open_linux.go`, the judge's platform).
No `-race` run (no cgo toolchain here, and the brief reserves it for the box). The restore worker changes, the
metadata pool and the helper goroutines were judged by reading; the reading found no race, but a reading is not a
race detector. The stock test suite does not exercise three of the mechanisms at all: the wait-work path is switched
off when the settle delay is 0 (`internal/repository/lock_file.go:140`), which is what every stock integration test
sets (`cmd/restic/cmd_init_integration_test.go:18`); the metadata pool is only enabled by `cmd_restore.go:260`; the
third restore worker needs the real local backend.

## 2. Findings, most severe first

### M1. Reads taken during the lock settle delay are used after the lock is confirmed. UNSOUND. BLOCKER through `backup`, HIGH for prune / restore / ls / forget / check. Reproduced.

Files: `cmd/restic/wait_index_load.go:34-50, 108-145, 169-195`; `cmd/restic/lock.go:16-50`;
`internal/repository/lock_file.go:138-155`; callers `cmd_restore.go:151-176`, `cmd_ls.go:380-411`,
`cmd_check.go:247-281`, `cmd_forget.go:195-219`, `cmd_prune.go:180-222`. Same mechanism in the backup area:
`cmd/restic/cmd_backup.go:545-601`, `cmd/restic/backup_index_load.go:22-65`.

What it does. As soon as the command's own lock file is written, a goroutine lists the snapshots and loads the index
(check also runs `LoadSnapshots`, the checker's index pass and the index-derived pack sizes). The 200 ms sleep and the
second lock check still happen. If the second check succeeds, the command uses what was read before it.

Contract touched: 3 (locking and concurrent processes). Also operator ruling 7, second sentence ("any work you start
during the lock wait must be safe while another restic process writes to the same repository").

Why stock is safe and the candidate is not. Stock reads nothing before the second check. A process P2 that got its
own lock past P1 (because P1's lock file was not yet visible when P2 did its second check) is either still running at
P1's second check, so P1 fails with "already locked", or has finished, so P1 reads the repository as P2 left it. So
in stock the work of two processes with conflicting locks never overlaps, whatever the latency. In the candidate P1
reads between its lock write and its second check; when P2 finishes inside that interval, P1's second check succeeds
and P1 continues with a snapshot list and an index from before P2's changes. The mutual exclusion of the lock
protocol no longer covers P1's index. The race fix of S015 (snapshots listed before the index) does not help: it
orders the two reads, it does not put them after the confirmation.

Precondition: the time from P1's first lock listing to P1's lock file being visible is longer than P2's lock write
plus the settle delay (200 ms), and P2 finishes its changes after P1's index listing and before P1's second check.
A repository on a local disk does not meet it. A remote one can: a lock-file write above 200 ms is ordinary on SFTP,
B2 or rclone over a WAN, and one retried `Save` (1 s back-off, `backend_retry.go:108-113`), a stalled connection or
a suspended laptop stretches it to seconds. P2 must then be quick or close to the storage. Rare per run; not rare
enough for a backup tool, because the lock protocol exists for exactly this interleaving and stock closes it.

Three scenarios, all reproduced. The test scales the settle delay to 1 s and delays P1's lock write by 2.5 s, so that
the ordering is robust on a loaded machine; the logic is the same at 200 ms.

- **Scenario C, data loss (BLOCKER).** P1 = `backup` of data that is already in the repository but unused (its
  snapshot was forgotten and not yet pruned; think of a restored or re-created directory). P2 = `prune`, which deletes
  that data. Stock: P1 loads the index after P2, uploads the data again, `check` passes. Candidate: P1 deduplicates
  against the index it loaded before P2 ran, uploads nothing, **reports success**, and writes a snapshot whose trees
  and data are gone. `check` afterwards: `error for tree b784b500: id b784b500... not found in repository`,
  `Fatal: repository contains errors`. The backup the user believes in cannot be restored, and nothing told them.
- Scenario A (HIGH). P1 = `prune`, P2 = `backup` of a changed file. Stock: prune succeeds. Candidate: prune fails with
  `Fatal: failed finding blobs: id ... not found in repository` (it lists snapshots after confirmation, sees P2's
  snapshot, and resolves it against the stale index). Repository intact.
- Scenario B (HIGH). P1 = `restore <snapshot>`, P2 = `prune` that repacks. Stock: restore succeeds. Candidate:
  restore prints `ignoring error for \x: ReadFull(<data/...>): ... cannot find the file`, restores nothing below
  `x`, and ends with `Fatal: There were 2 errors` (its stale index points at packs P2 deleted). Repository intact.

By reading, not run: `ls` behaves like scenario B (same code path). `forget --prune` evaluates its policy on the
pre-confirmation snapshot list (`cmd_forget.go:219`): a snapshot P2 just wrote is neither kept nor counted, so
`--keep-last N` keeps one more old snapshot than stock, and the prune that follows aborts as in scenario A when the
index is stale. `check` checks the state before P2 and, when P2's packs are listed after confirmation
(`cmd_check.go:345`), reports them as "additional files ... run `restic prune`".

Severity. Scenario C is silent loss of a backup: BLOCKER. It goes through the `backup` command, whose files belong to
the backup area, but it is this mechanism and this plumbing, and any fix has to be made here
(`lock_file.go:138-155`). For the read-side commands the outcome is a failing or stale result where stock is
correct: HIGH. In scenarios A and B the repository is intact afterwards (`check --read-data` passes), restore fails
loudly with a non-zero exit, and I found no path to a silently wrong restore (every blob is still hash-checked) or to
a prune that deletes needed data (a prune on a stale index stops in `PlanPrune`, because a snapshot tree or a needed
pack is missing). I could not rule out an index that references deleted packs after a three-way race or after a
prune that fails half-way.

Witness: `witness-readside/cmd_lock_wait_stale_witness_test.go` (three tests; uses only stock test helpers; the same
file runs in both trees). Stock: PASS, PASS, PASS. Candidate: FAIL, FAIL, FAIL. Logs:
`cmd_lock_wait_stale_witness.{stock,candidate}.log`. In the candidate log the index is listed 1 ms after the lock
file is written and 1 s before the second lock check; in the stock log it is listed after the check.

Direction for whoever fixes it (not done here): the prepared reads are only safe when the lock file became visible
within the settle delay. Measuring the time from the start of the first lock listing to the end of the lock write,
and discarding the prepared work when that time is not below `waitBeforeLockCheck`, restores stock's guarantee under
stock's own assumption (consistent listings) and costs nothing on the judge, where the write takes a millisecond.

### M17. Hard-link index rebuilt in the second restore pass. UNSOUND against "what stock would produce", MEDIUM (behaviour change). Reproduced.

Files: `internal/restorer/restorer.go:683` (first pass keeps a set without paths), `:768-776`, `:829`, `:859-871`.

What it does. Stock records `(inode, device) -> first path` in the first pass for nodes with `Links > 1`, and in the
second pass links every regular file whose `(inode, device)` is in that index and whose path differs (stock
`restorer.go` second-pass `visitNode`: `idx.Has(...) && idx.Value(...) != location`, with no test of the node's own
link count). The candidate rebuilds the index during the second pass and only looks up nodes that carry `Links > 1`.

Contract touched: 1 (hard links).

Equal on every consistent snapshot. Different when one inode appears with link count 1 on one path and more than 1 on
another, which a backup records when a hard link is added or removed while it runs (rsync `--link-dest` rotation,
package managers). Stock restores one hard-linked file at both paths; the candidate restores two independent files
(twice the space, link lost). Where the two paths recorded different contents, the candidate keeps each path's own
content and stock gives both paths the first one's, so the candidate is arguably the more faithful; it is still not
what stock produces, and the manifests differ in link count and inode.

Second difference, by reading only: the candidate dropped the `!= location` test. A tree that names the same file
twice with `Links > 1` (restic never writes one) makes the candidate call `restoreHardlinkAt` with source = target,
which removes the file and then fails to link it (`restorer.go:352-361`); stock restores metadata only.

Witness: `witness-readside/restorer_hardlink_linkcount_witness_test.go`. Stock: PASS (linked in all three cases).
Candidate: FAIL in the two mixed cases (`a and b restored as one hard-linked file: false`).

### X3. Tree decoder skips the name unquote. UNSOUND, MEDIUM. Reproduced. File belongs to the backup area.

Files: `internal/data/parent_tree.go:62-101` (line 90), used by `LoadTree` (`internal/data/tree.go:145-150`) and by
`LoadTreeNodeReader` (`internal/data/tree_node_reader.go:22-47`), that is by ls, restore, check and prune.

What it does. Decodes a node straight into the struct and runs `strconv.Unquote` on the name only when it contains a
backslash. Stock always unquotes (`Node.UnmarshalJSON`).

Contracts touched: 5 (check coverage of every tree), 6 (ls errors at the same point), 1.

Failure scenario. A tree whose node name, after JSON decoding, holds a raw double quote or a raw newline and no
backslash. Stock: `Unquote: invalid syntax`, so `check` reports `failed to decode tree`, `ls` and `restore` report an
error for that directory. Candidate: accepted; `check` reports no error, `restore` creates a file with that name.
restic never writes such a name (`strconv.Quote` escapes both) and bit rot cannot produce one (the blob hash is checked
first), so this needs a tree written by another tool or by a bug. It is a validation the candidate lost, in the one
command whose job is validation; if any weakening of `check` counts as a BLOCKER under the brief, this is the row to
look at. I rate it MEDIUM because the input cannot arise from restic or from damage.

Witness: `witness-readside/data_tree_name_witness_test.go` (stock decoder `NewTreeNodeIterator` and the candidate
decoders in one binary). Candidate: FAIL for both names (`stock err=Unquote: invalid syntax, candidate err=<nil>`);
the control case (a quote as restic writes it) agrees.

### X1. Pack blobs are decoded inside the backend Load callback. Integrity SOUND; UNSOUND, LOW for two behaviours. Reproduced. File belongs to the storage area.

Files: `internal/repository/stream_pack_coalesced.go:35-204`; `internal/repository/repository.go:1513-1570, 1678-1795`.
Reached by restore, repack and copy.

What it does. Stock reads a pack range of up to 32 MiB into memory inside `Load` and decodes it after `Load` returns
(stock `repository.go` `streamPack`, `maxChunkSize = 2 * DefaultPackSize`). The candidate requests the same range,
but reads it in 2 MiB windows (1 MiB for the bounded worker) and decodes, verifies and delivers every window except
the last inside the `Load` callback; a replayed callback skips the blobs already delivered (`committed`, lines 79-151).

Contracts touched: 1, 2, 4.

By reading: each blob is still decrypted (MAC) and hash-checked before its bytes reach the caller
(`repository.go:1879, 1904-1912`); a blob that fails yields out of the response and is recovered through `LoadBlob`
as in stock (lines 128-131, 158-168); a handler error cancels the request before the retry wrapper can replay it
(lines 133-137); delivery is once per blob on every path I traced (replay, yield, exhausted retries, final window).
The amount re-downloaded after a transport error is the same range as stock.

Reproduced behaviour 1: a non-transport error is retried as a failed download. An index entry whose length is not
larger than the nonce makes `readBlob` return `invalid blob length` (`repository.go:1990-1993`) from inside the
callback. Stock: one request, error reported at once, pack still readable. Candidate: 8 requests in the test's fast
mode, which is up to 15 minutes of retries with warnings in the real binary (`global.go:626`), and then the circuit
breaker is open for that pack (`backend_retry.go:205-226`), so later reads of it fail for the rest of the run. Needs
a damaged index entry.

Reproduced behaviour 2: fail-open. If a `Load` returns nil without having called the consumer, the multi-window path
returns nil with no blob delivered and no error (`stream_pack_coalesced.go:152-203`: nothing checks that `committed`
plus the final window covers the request). Stock, and the candidate's own single-window path, fail closed (the buffer
does not authenticate). Every backend in the tree goes through `util.DefaultLoad`, which always calls the consumer,
so this is a lost safety margin, not a live bug.

Not reproduced, by reading: the HTTP response now stays open while windows are decrypted and written to the target
(restore) or while a repack worker waits for upload room (`repack.go:158`). A stall of 5 minutes trips the stuck
request watchdog (`internal/backend/http_transport.go:145-149`) and the range is fetched again. Stock holds a
response only for the transfer.

Cross-reference, not run by me: the storage review (`REVIEW-storage.md`, finding 2) reproduced a deadlock that follows
from the same design. `repair packs` saves each blob synchronously from the handler
(`internal/repository/repair_pack.go:100-111`), and the handler now runs while the reader holds a backend
connection (`stream_pack_coalesced.go:126-140`). With one connection the uploader waits for the connection the reader
holds and the reader waits for the uploader. Its witness `review/witness-storage/repair_packs_conn1_witness_test.go`
times out on the candidate and passes on stock. I read `repair_pack.go:100-111` and the claim matches the code. The
prune and copy path is guarded (`repack.go:53-55` requires two connections and `:208` keeps one free); restore's
handler makes no backend call, so restore is not affected. The storage review rates this HIGH; the LOW above covers
only the two behaviours in my witness.

Witness: `witness-readside/repository_stream_coalesced_witness_test.go` (two tests; the stock algorithm is the
candidate's frozen `streamPack`). Candidate: FAIL, FAIL. Log: `repository_stream_coalesced_witness.candidate.log`.

### M22. Per-directory state retained by restore. SUSPECT, LOW. Not reproduced.

Files: `internal/restorer/restorer.go:663` (`freshDirs`, one map entry with the full target path per directory the
restore creates), `:721-727` (`restoreTreeIDs`, 32 bytes per tree visited).

Contract touched: 7 (memory bounds that hold only for the benchmark's shapes).

Stock keeps nothing per directory. For a restore of a snapshot with millions of directories into an empty target
this is on the order of 150 to 250 bytes per directory, live through the first pass and, for `restoreTreeIDs`,
through the file restore. Stock already keeps one map entry per restored file (`res.fileList`), so the growth is
relative to that. Deciding experiment: NEEDS-RUN 7.

### SOUND-UNDER-CONTRACT

| # | Mechanism | Files | Contract it stands on | Evidence / deciding experiment |
|---|---|---|---|---|
| M3 | Snapshots are listed before the index inside the wait work; the command reuses that listing (race fix) | `wait_index_load.go:42-48, 73-92`; `cmd_restore.go:167`; `cmd_ls.go:401`; `cmd_forget.go:219` | The first listing succeeds. If it fails, `get` returns the repository and the command lists the snapshots again after the index load has started (`wait_index_load.go:83-92`): stock's order is reversed on that error path only. Also subject to M1 | Reading. The S015 repro (stock backup loop beside `restore latest` / `ls latest`) should be re-run on this tree: NEEDS-RUN 3 |
| M8 | One additional pack-load slot beside the connection semaphore; scoped loads wait on the semaphore with cancellation | `internal/backend/sema/backend.go:19-23, 35-55, 72-96`; `sema/semaphore.go:30-38` | The slot exists only when the wrapped backend reports `AllowAdditionalLoadSlot()`; only `local.Config` has that method and only with the default connection count and no `-o local.connections` (`local/config.go:19-29`, `global.go:546-549`). Every other backend, and local with an explicit setting, keeps exactly `connections` concurrent operations: the extra workers queue on the same semaphore | Reading; `internal/backend/sema` tests pass on Windows. Freeze barrier kept (`backend.go:78-79, 88-89`). Lock files still bypass the limit as in stock |
| M12 | Restore runs one "bounded" worker beside the configured ones on every backend, and one more ordinary worker on local with default connections when `GOMAXPROCS` is larger than the connection count | `internal/restorer/filerestorer.go:41-45, 175-177, 228-318`; `ordinary_extra_worker.go:26-41`; `restorer.go:689-691`; `repository/restore_decoder.go:21-24`; `repository/repository.go:1513-1570` | The loader calls the blob callback once per blob and never concurrently for one pack; the error callback is serialised (`filerestorer.go:384-396`). File creation is still guarded per file (`:459-467`), writes are still `WriteAt` at fixed offsets, so more workers change the order of writes, not their result | Reading: no cycle between permits (`:240-261`), semaphore tokens and the byte budget; with `connections=1` the two workers serialise on the one token. Not run with `-race`, on 1 CPU, or against a remote backend: NEEDS-RUN 1, 5, 6 |
| M15 | Associations are dropped as soon as a blob's callback returns (`defer delete(blobs, h.ID)`); `pack.files = nil` after planning | `filerestorer.go:374-381, 433-436` | Once-per-blob delivery (same as M12). A second callback for the same blob would find no files and write nothing, where stock would write again | Reading of all delivery paths in `stream_pack_coalesced.go` and `repository.go:1678-1795`. NEEDS-RUN 1 and the arm's fault tests, NEEDS-RUN 8 |
| M16 | A file whose parent directory this restore created with an exclusive `Mkdir` is not verified against the target before restore; the last prepared directory is cached | `restorer.go:376-417, 421-462, 663-719, 777-778, 995-1023` | A directory that `Mkdir` just created is empty, and the first pass writes no file data (workers start at `:822`). Then stock's `verifyFile` would fail to open the file and return the same nil state. A directory that existed, or that lost the creation race, keeps verification (`:404-416`). In-place restores are therefore untouched | Reading; `TestEnsureDirWithFreshness` and the stock restorer tests pass on Windows. Case-insensitive name collisions give the same result as stock (the second spelling is not "fresh"). NEEDS-RUN 5 for a manifest comparison over existing targets |
| M18 | Metadata of regular files is applied by 4 workers, at most 64 pending; results, warnings and errors are reported in snapshot order; directories, special files and hard links drain the pool first | `restorer.go:38-56, 150-155, 464-651, 829-837, 848-852, 861-865, 875-879, 886-890, 908-911`; enabled only by `cmd/restic/cmd_restore.go:260` | Metadata calls on distinct regular files are independent, and the xattr filter is safe to call concurrently (it is: `cmd_restore.go:301-333` builds a pure matcher per call). Shared state in `internal/fs` is guarded on every OS (lookup caches under `RWMutex`, `node.go:107-219`; `sync.Map` and `atomic` on Windows, `node_windows.go:25`, `sd_windows.go:13`) | Reading. Never executed by the judge outside Linux, and never by the stock restorer unit tests (they assign the filter directly, which keeps the synchronous path). NEEDS-RUN 1 and 4 |
| M20 | Trees decoded in the first pass are kept (512 KiB in total) and handed to the second pass; a goroutine prefetches the next second-pass trees | `internal/restorer/tree_carry.go:15-119`; `tree_prefetch.go:13-210`; `restorer.go:721-750, 811-819, 840-843` | Nothing mutates a node after the first pass decoded it (true for both visitors and for `traverseTreeInnerWithTreeAccess`, `restorer.go:190-316`), and a prefetched tree is used only when its ID equals the ID the traversal asks for (`tree_prefetch.go:159-167`); any mismatch, load error or iterator error ends the prefetch and falls back to direct loads | Reading. Content is determined by the tree ID and every carried tree came from a hash-checked blob, so a wrong hand-over cannot produce wrong nodes |

### SOUND

| # | Mechanism | Files | Why |
|---|---|---|---|
| M2 | Settle delay, lock type and clean-up around the wait work | `internal/repository/lock_file.go:108-157`; `internal/repository/lock.go:55-129`; `cmd/restic/lock.go:12-78` | `time.Sleep(waitBeforeLockCheck)` is unconditional (`lock_file.go:144`) and every lock request goes through this function. Wait work starts only after the first check passed and the lock file exists (`:131-142`). A failed second check settles the work with `false` before the lock is removed (`:146-151`); `discard` cancels and joins (`wait_index_load.go:134-145, 225-237`). check, prune and forget still ask for an exclusive lock (`cmd_check.go:247`, `cmd_prune.go:180`, `cmd_forget.go:203`, `lock.go:76-78`). `--no-lock`, `DEBUG_LOG` and a zero delay keep the stock path (`lock.go:19-21, 29, 45-47`; `lock_file.go:140`) |
| M4 | ls fetches the one snapshot file it will need while the index loads | `cmd/restic/snapshot_metadata_wait.go:19-81`; `internal/repository/prepared_snapshot.go:13-75`; `cmd_ls.go:382-392, 441` | One extra GET, read-only. The bytes are size- and hash-checked on arrival (`prepared_snapshot.go:50`), offered once to the ordinary load (`:60-74`) and go through the unchanged `LoadRaw` hash check and the MAC. Cancelled and joined before unlock (`cmd_ls.go:392`) |
| M5 | check prepares the index-derived pack sizes during the wait and reuses them for the pack check and for `--read-data` | `wait_index_load.go:187-192`; `internal/repository/checker_pack_sizes.go:13-48`; `cmd_check.go:339-348, 446` | `pack.Size` over the loaded index only, no backend listing (`checker_pack_sizes.go:31-38`). The backend pack listing stays after confirmation (`repository/checker.go` `PacksWithPreparedSizes`). The pack check only reads the shared map; the read-data filter runs after it has finished. Same numbers as stock for the same index |
| M6 | check starts the Structure pass before the pack check and shows its errors and progress at the stock position | `cmd/restic/check_structure_overlap.go:12-41`; `cmd_check.go:326-336, 381-421` | Same `chkr.Structure` on the same index and snapshot list (`internal/checker/checker.go:150-202`, unchanged from stock); errors wait on an unbuffered channel until the stock heading is printed; every send is cancellable, so `discard` cannot hang. Coverage and exit code are unchanged. Off on a terminal with progress, with `--no-lock` and with debug (`cmd_check.go:332-333`) |
| M7 | Progress of the early index load is buffered and replayed at the stock position | `cmd/restic/backup_index_load.go:77-160` (shared type); `wait_index_load.go:117-129, 215-223` | Terminal-only counter. Visible difference: the elapsed time on the "index files loaded" line starts at the replay, not at the load |
| M9 | Local backend config flag and its wiring | `internal/backend/local/config.go:11-36`; `internal/global/global.go:544-549` | `NewConfig` and `ParseConfig` unchanged (stock test pins them); the unexported field has no `option` tag, so `-o` parsing is unaffected. User-visible quirk: passing `-o local.connections=2` (the documented default) turns the extra slot off |
| M10 | Rate limiter forwards the capability | `internal/backend/limiter/limiter_backend.go:89-95` | Only a capability query. Reads and writes still pass the limiter; the limit is a shared byte rate, so one more connection does not raise it |
| M11 | Retry backend can serve one prepared, hash-checked ciphertext | `internal/backend/retry/prepared_load.go:12-47`; `backend_retry.go:215-218` | Applies only to the exact handle, full length, offset 0, same retry backend instance; spent before the consumer runs, so a retry goes to the backend. Circuit breaker, back-off and permanent-error handling untouched |
| M13 | Byte budget for the third restore worker | `internal/restorer/restore_hold_budget.go:13-91`; `internal/restic/restore_hold_budget.go`; `internal/repository/restore_window_hold.go:23-91` | Configured workers never wait (`Force`); only the additional worker waits, and it holds no budget while it waits. Every take is released on every exit (`repository.go:1696-1697`, `stream_pack_coalesced.go:85-86, 105-108, 197-201`); an oversized request is capped at half the budget (`restore_hold_budget.go:55-57`), so it cannot wait for ever. Waits observe cancellation |
| M14 | One-blob files skip the second index lookup; shared zero-offset slice | `internal/restorer/planned_singleton.go:10-15`; `restore_offsets.go:7-14`; `filerestorer.go:345-351` | Membership in `pack.files` already proves "unmatched and first index entry is this pack" (`filerestorer.go:154-174`); the loader re-validates pack membership (`repository.go:1495-1511`). The shared slice has capacity 1 and is only read |
| M19 | First-pass node look-ahead on a helper goroutine | `internal/restorer/node_lookahead.go:13-182`; `restorer.go:735-738, 803` | One request outstanding per frame, none across a directory descent (a directory ends its batch and the frame releases its credit first, lines 66-69, 150-154); the frame joins its pending request before closing the reader (lines 102-110). Order of nodes and position of errors equal the direct iterator |
| M21 | Target files are opened without the runtime poller on Linux | `internal/restorer/fileswriter.go:66, 83, 92, 141`; `restorer.go:1152`; `internal/fs/file.go:58-68`; `internal/fs/file_open_linux.go`; `file_open_other.go` | Linux only, build-tagged, other systems call `os.OpenFile` as before. `unix.Open` adds `O_LARGEFILE` like `syscall.Open`; `O_CLOEXEC` and `O_NOFOLLOW` kept; `EINTR` retried; unsupported flags fall back to `os.OpenFile`. Every caller stats the descriptor and requires a regular file before I/O. Not executed here: NEEDS-RUN 9 |
| M23 | ls decodes tree nodes on a helper goroutine | `internal/walker/listing_preparation.go:10-179`; `listing_walk.go:15-92`; `cmd_ls.go:502` | `walkPrepared` is `walk` line for line apart from the loader. Batches are delivered in order; a directory or an oversized node ends a batch and nothing is in flight across a descent or a callback that can descend (lines 67-70, 136-138, 157-164); a closed pipe or `ErrSkipNode` makes the iterator join its pending batch and close the reader (lines 120-125); `Close` joins the helper (`listing_walk.go:16-17`). Output is produced by the unchanged printers from the same nodes (subject to X3). `internal/walker` tests pass on Windows |
| X2 | Restore decoders: a decoder with as many block decoders as configured workers; a 1 MiB capped decoder for the bounded worker | `internal/repository/restore_decoder.go:26-52`; `repository.go:1561-1600, 1883-1899` | `DecodeAll` as in stock. A refusal by the capped decoder is retried with the ordinary decoder under ordinary admission (`:1887-1895`); the blob hash is checked after either |

## 3. NEEDS-RUN experiments

`$CAND` and `$STOCK` are the two tree roots on the Linux box. None of these could be decided on this machine.

1. Race detector over the read side (Linux, cgo):
   `cd $CAND && go test -race -count=3 ./internal/restorer ./internal/walker ./internal/backend/sema ./internal/backend/retry ./internal/data`
   `cd $CAND && go test -race -count=1 ./internal/repository -run 'Stream|Restore|Hold|Bounded|Prepared|Decoder'`
   `cd $CAND && go test -race -count=1 ./cmd/restic -run 'TestRestore|TestLs' ` (settle delay 0: sequential path, covers the metadata pool, the look-ahead, the prefetch and the third worker on the local backend)
2. The candidate's own wait-work tests, which need the symlink fixture and could not run on Windows. Two of them
   failed on S015 according to `race-fix/REPORT.md`:
   `cd $CAND && go test -count=1 -v ./cmd/restic -run 'TestRestoreFailedLockConfirmationDrainsPreparedIndexBeforeUnlock|TestRestoreSnapshotLookupErrorDrainsPreparedIndexBeforeUnlock|TestLsFailedLockConfirmationDiscardsPreparedSnapshotBeforeUnlock'`
3. M1 on Linux, and the S015 repro on this tree:
   `cp review/witness-readside/cmd_lock_wait_stale_witness_test.go $CAND/cmd/restic/ && cd $CAND && go test -count=5 -v -run TestWitnessLockWaitStale ./cmd/restic` (same in `$STOCK`; expected stock 3 x PASS, candidate 3 x FAIL)
   `~/race-dev/repro.sh` with the arm set to S019-merge (stock backup loop beside 100 x `restore latest` and 100 x `ls latest`; expected 0 failures)
   Real binaries at the real 200 ms, scenario C: serve the repository with rest-server; put a proxy in front of it for
   P1 only that holds `POST /locks/*` for 3 s; run `restic backup d1` (P1, through the proxy), start
   `restic prune --max-unused 0` (P2, direct) 0.3 s later and hold its `POST /index/*` until P1's lock POST has
   returned plus 50 ms; then `restic check`. Expected: stock check passes, candidate check reports the new snapshot's
   tree as missing.
4. Metadata pool and hard links off Linux:
   `go test -count=1 ./internal/restorer ./cmd/restic -run 'TestRestore'` on macOS (APFS) and FreeBSD, and on Windows in an elevated shell (symlink and security-descriptor privileges); then the same with `-race` on macOS.
   `cp review/witness-readside/restorer_hardlink_linkcount_witness_test.go $CAND/internal/restorer/ && go test -count=1 -v -run TestWitnessHardlinkMixedLinkCount ./internal/restorer` on Linux ext4.
5. Restore option matrix against the stock oracle, into (a) an empty target, (b) a target holding an older restore,
   (c) a target with files of the wrong type in the way: `--overwrite always|if-changed|if-newer|never`, `--delete`,
   `--sparse`, `--include`/`--exclude`, `--dry-run`, `--verify`; compare full manifests (content hash, size, blocks,
   mode, owner, mtime, xattrs, link count, inode sharing). Repeat (a) with `GOMAXPROCS=1` and `GOMAXPROCS=64`, and
   with `-o local.connections=1` and `-o local.connections=2`.
6. Remote backends with small limits (watch for a hang; none expected by reading):
   `restic -r rest:http://... -o rest.connections=1 restore latest --target /tmp/r1` ; the same with `--limit-download 512`;
   `restic -r rest:http://... -o rest.connections=2 prune --max-unused 0`; the same three over SFTP.
7. Memory at shapes the judge does not have: restore of a snapshot with 2 million directories and 10 million empty or
   tiny files into an empty target, and of one 50 GiB file; `/usr/bin/time -v` peak RSS, candidate against stock.
8. The arm's own fault tests of the coalesced response, and a live fault run:
   `cd $CAND && go test -count=1 -v ./internal/repository -run 'Stream.*Fault|StreamSocket|StreamRest|StreamPackCoalesced|TestWitnessCoalescedStream'`
   restore through toxiproxy with a connection reset every 20 MiB and with a 10 s stall; compare manifests, error
   output and exit code with stock.
9. Linux-only open path on 32-bit: `GOARCH=386 go test -count=1 ./internal/fs -run 'OpenFile|NoPoller'` on Linux, and
   a restore of a file larger than 4 GiB with the 386 binary.
10. X3 through the commands: write a tree blob with a node name holding a raw quote into a scratch repository
    (`restic cat`-level tooling or a small Go program using `repo.SaveBlob`), reference it from a snapshot, then run
    `check`, `ls` and `restore` with both binaries; expected stock errors, candidate success.

## 4. The changed stock test: `internal/restorer/restorer_test.go`

`git diff --no-index` between the trees: 23 lines added, none removed, none changed (diff in
`review/scratch-readside/diffs/internal_restorer_restorer_test.go.diff`). The addition is one new test,
`TestEnsureDirWithFreshness` (candidate lines 1281-1302), placed between `TestRestoreIfChanged` and
`TestRestoreDryRun`. It checks the new helper: a missing path is created and reported fresh, the second call reports
not fresh, a stale regular file is replaced by a directory and reported fresh. No existing assertion was weakened,
removed or reordered.

What the unchanged stock tests in this package do not reach: the metadata pool (they assign `XattrSelectFilter`
directly or leave it nil, which keeps the synchronous path, `restorer.go:38-56`), the third worker and its budget
(the test repository's memory backend does not grant the slot), and anything behind the lock wait.

## 5. Things that should not ship as they are

- `internal/repository/repository.go.orig`: a 1,998-line patch left-over inside the candidate tree (storage area).
- `internal/restic/restore_hold_budget.go:13-15`: the file "declares types only, on purpose", because adding code to
  that package moves `scrypt.salsaXOR` in the binary and changes the judge's key-derivation time. The split of the
  budget across `restic` and `restorer` exists for binary layout on the judge, not for the design.
- `internal/repository/repository.go:1096`: a `runtime.GC()` before every index load, there to shape peak memory on
  the judge.
- Constants sized against the judge's memory guard and machine, with no relation to the host: 5 MiB hold per worker
  (`ordinary_extra_worker.go:26`), third worker only when `GOMAXPROCS` exceeds the connection count (`:29-34`, the
  judge has 4 CPUs and 2 connections), 4 metadata workers and 64 pending tasks (`restorer.go:489-490`), 2 MiB restore
  window (`repository/restore_stream_budget.go:6`), 512 KiB / 256 KiB tree carry (`tree_carry.go:15`,
  `tree_prefetch.go:13`). They are safe; they are tuning for one machine and should be documented as such.
- `debug.IsEnabled()` selects the stock code path (`cmd/restic/lock.go:19-21`, `cmd_check.go:332`). A user who turns
  on `DEBUG_LOG` to investigate a problem in the overlapped path runs different code and will not see it.
- The wait work is skipped when the settle delay is 0 (`lock_file.go:140`), which is what the stock integration suite
  sets. "The stock suite passes" therefore says nothing about M1 to M7.
- `-o local.connections=2` is not the same as the default any more (`global.go:546-549`); the option's help text
  still reads "(default: 2)" (`local/config.go:14`).
- Comments that still call the extra slot "check-only" although restore uses it (`local/config.go:19-26`,
  `sema/backend.go:72-74`, `limiter_backend.go:90-91`).
- The three own tests under NEEDS-RUN 2, if they still fail on Linux.

## 6. Platform checks done here

- Cross-compilation of `./cmd/restic` from the candidate tree with `CGO_ENABLED=0`: linux/amd64, linux/386,
  darwin/arm64, freebsd/amd64, openbsd/amd64, solaris/amd64 all build. `GOOS=linux go vet` of `internal/restorer`,
  `internal/walker`, `internal/backend/sema`, `internal/backend/retry`, `internal/fs` and `cmd/restic` is clean
  (`review/scratch-readside/xbuild-readside.log`). So no build tag was lost in the read-side files; the only
  OS-specific code they added is `internal/fs/file_open_linux.go` with its `!linux` twin.
- Windows 10, native: package tests and the `cmd/restic` subset as listed in section 1
  (`review/scratch-readside/win_pkgs_{candidate,stock}.log`, `win_cmd_{candidate,stock}.log`). No test fails on the
  candidate that passes on stock.
- Not done: any execution on Linux, macOS or a BSD; any 32-bit execution; `-race`.

## 7. Witness files (`review/witness-readside/`)

| File | Package to copy it into | Shows | OS | Stock | Candidate |
|---|---|---|---|---|---|
| `cmd_lock_wait_stale_witness_test.go` | `cmd/restic` | M1, three scenarios (C = data loss) | any | PASS x 3 | FAIL x 3 |
| `restorer_hardlink_linkcount_witness_test.go` | `internal/restorer` | M17 | any filesystem with hard links | PASS | FAIL (2 of 3 cases) |
| `data_tree_name_witness_test.go` | `internal/data` | X3 | any | n/a (compares the stock decoder kept in the candidate) | FAIL (2 of 3 cases) |
| `repository_stream_coalesced_witness_test.go` | `internal/repository` | X1, two tests | any | n/a (compares the stock algorithm kept in the candidate) | FAIL, FAIL |

Each file carries a header comment with what it shows, where it runs and the command. A failing witness here means
"the divergence was reproduced". Logs of the runs on this machine are beside them.
