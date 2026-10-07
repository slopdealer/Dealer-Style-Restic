# S015 TestRestore race (2026-10-03)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Investigated by agent `fix-restore-race`. Saved by the coordinator, because the subagent harness blocked the write.

## Cause
- **Test assertion:** `cmd/restic/integration_test.go` `listOnceBackend.List` (strictOrder): `if t == SnapshotFile && listedFileType[IndexFile] -> "tried listing type snapshots after index"`. All integration tests use it (`newOrderedListOnceBackend` in `withTestEnvironment`); the prune tests use the unordered variant.
- **Incumbent paths that list snapshots after the background index load has started:**
  - backup → `findParentSnapshot` (S013+)
  - restore → `FindLatest`
  - ls → `MemorizeList`
  - check → `LoadSnapshots`
  - forget --prune → `FindAll` (S015)
- **Real binary vs tests:** with the real 200 ms lock wait, the order is reversed every time. In tests the wait is 0 (`testRunInit` → `TestSetLockTimeout(t,0)`), so it is a goroutine race. The main thread usually wins, partly because S015's `LoadIndex` starts with `runtime.GC()`. Under CPU load the goroutine sometimes wins.
- **Earlier evidence:** earlier S015 candidates failed every time (supervisor-jobs a0a625b6…, d320ea9c…).
- **The reported failure:** `~/suite-dev/runs/bi-correctness` failed in TestRestore's backup step.

## User impact (real bug)
Read and append locks are compatible, and backup writes the index before the snapshot. Listing snapshots first therefore guarantees that every listed snapshot's index is loaded; the incumbent loses that guarantee.

Repro: a stock backup loop runs alongside the arm's `restore latest` and `ls latest`.

| Arm | restore latest failed | ls latest failed |
|---|---|---|
| S015 | 31/100 | 29/100 |
| stock | 0/100 | 0/100 |
| S015 + fix | 0/100 | 0/100 |

The error is `id … not found in repository`. Under the exclusive locks of check and forget the reversed order is harmless for users, but it still trips the test.

## Failure rates (TestRestore; stock tests overlaid as in `build_input`; go1.27.0; CPUs 2-5)

| Arm | One at a time x200 | 4 parallel copies on CPUs 2-5 |
|---|---|---|
| stock | 0 | 0/400, 0/1000 |
| S013 | 0 | 9/1000 (all in backup) |
| S014 | 0 | 8/1000 (all in backup) |
| S015 | 0 | 8/400, 21/1000 (backup 8, restore 10, check 3) |
| S015 + fix | 0 | 0/400, 0/1000 |

Every failure was the snapshots-after-index assertion.

## Fix (`S015-testrestore-race.patch`; 8 non-test files in cmd/restic and internal/repository/lock_file.go)
1. **Snapshots listed first, inside the overlap.** The background goroutine lists snapshots (with `restic.MemorizeList`) before loading the index.
   - Restore, ls and forget --prune use the prepared listing.
   - Backup's parent search uses it through a `snapshotListerRepo` wrapper. Backup only prepares it when stock would search for a parent (`!Stdin && !Force`).
   - Check runs `LoadSnapshots` before `LoadIndex` in the goroutine and returns its error at the same point as before.
   - If the prepared listing fails, the command lists again itself, so error output is unchanged.
   - Standalone prune is unchanged (stock already loads the index first there).
2. **No background work without a wait.** `newLockWithWaitWork` only starts it when `waitBeforeLockCheck > 0`; with no settle delay there is nothing to overlap.
   - Without this, part 1 alone crashed 12/200 times with "concurrent map writes" in the non-thread-safe test backend.
   - The real binary is unaffected (200 ms wait). The lock wait is not removed or shortened.
3. **League rules:** go.mod and go.sum, stock test-called internal APIs, output and exit codes are all unchanged.

## Verification
- **Judge's own `compatibility_tests()`:** passed (1026 pass / 20 tolerated stock failures / 17 skip; 0 new failures, 0 missing; 46 s).
- **`go vet`:** clean.
- **Incumbent's own wait-work tests:** identical before and after the patch. `internal/repository` passes. Two arm-only cmd tests already fail on the unpatched S015: `TestRestoreFailedLockConfirmationDrainsPreparedIndexBeforeUnlock` (nil-pointer panic) and `TestRestoreSnapshotLookupErrorDrainsPreparedIndexBeforeUnlock`.
- **No `-race` run:** the test backend is not thread-safe.

## Timing (big-index pristine repo on /dev/shm, `--no-cache`, 30 rounds, median of paired per-round differences)

| Command | stock | S015 | Fix | Fix vs S015 |
|---|---|---|---|---|
| ls latest | 1.244 s | 0.758 s | 0.765 s | +0.7% |
| restore latest | 1.259 s | 0.753 s | 0.743 s | −0.9% |
| check | 1.942 s | 1.414 s | 1.405 s | −0.9% |
| backup | 1.268 s | 0.755 s | 0.750 s | −0.1% |

All within noise; the overlap's gain is kept in full.

## S016
None existed at the end of the run. If S016 keeps the same overlap code, re-apply the patch and re-run `stress.sh` and `compat.py` (in `~/race-dev`).

## Box artifacts
- `~/race-dev/logs/` (base, base2, stress, stress2, repro, timing, compat.out, owntests*)
- Per-arm `out-*/fail-*.txt`
- Scripts: `inner.sh`, `stress.sh`, `repro.sh`, `timing.sh`, `compat.py`
