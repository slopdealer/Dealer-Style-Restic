# Hand merge on S017 (2026-10-04)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Operator order ("Merge first"): one integrated candidate built on `incumbents/S017` from the unpromoted ideas of
S017, judged against S017 on the three-workload suite. **Verdict: confirmed at 42 rounds** (job
`b740c9e268c04247af4cfb814d91c7a2`: suite +6.06%, 33/42, every gate met). Judged tree on the box:
`/opt/league/restic-merge-s017/final`; installed 2026-10-04 11:56 UTC as `incumbents/S017-merge` (roster
`active_cbk`, `current_best_known` id `S017-merge`, a `maintenance` record; roster backup
`roster.json.bak-before-merge-20261004T115643Z`). Diff: `merged-vs-S017.diff` (15 non-test `.go` files, 1,235 lines).
Step log: `PROGRESS.md`. This file is the merge agent's report, saved by the coordinator.

## Official judging (official lanes, judge `b5e4b9e1`, launcher `tools/judge_merge.py`)

| job | id | rounds | suite vs S017 | wins | main | big-index | large-files |
|---|---|---|---|---|---|---|---|
| reference | `a31b39cd478145a49f03a186fb0f6cc4` | 12 | +5.79% (int +5.59..+6.04) | 10/12 | +0.19 | +17.05 | +0.13 |
| promotion | `b740c9e268c04247af4cfb814d91c7a2` | 42 | **+6.06%** (int +5.93..+6.18) | **33/42** | +0.16 (10/14) | +17.90 (14/14) | +0.11 (9/14) |

Promotion job:

| | suite | main | big-index | large-files |
|---|---|---|---|---|
| time vs S017 | +6.06% | +0.16% | +17.90% | +0.11% |
| memory saved vs S017 (guard: not below −2%) | +5.20% | −0.64% | +17.25% | −1.00% |
| time vs stock | +27.08% (42/42) | +27.92% | +39.84% | +13.48% |
| memory saved vs stock | +32.72% | +42.83% | +17.11% | +38.21% |
| median total seconds, merged / S017 / stock | 49.71 / 53.97 / 70.97 | 15.995 / 16.025 / 22.190 | 19.446 / 23.654 / 32.286 | 14.267 / 14.293 / 16.492 |

`eligibility.eligible` true, no reasons. Correctness: 955/955 units verified against the stock oracle, compatibility
tests passed, cross reader ok. Big-index forget-prune: +60.2% time, +38.0% memory (14/14).

Per phase vs S017 at 42 rounds, time saved % (wins of 14):

| phase | main | big-index | large-files |
|---|---|---|---|
| backup-initial | −0.27 (5) | +1.93 (14) | −0.22 (6) |
| backup-unchanged | −0.19 (4) | +5.27 (14) | +0.05 (9) |
| backup-mutated | −0.36 (4) | +4.94 (14) | −0.18 (7) |
| restore | −0.41 (5) | +3.10 (14) | +0.26 (10) |
| check-read-data | +0.91 (9) | +1.11 (10) | +0.38 (11) |
| forget-prune | +5.05 (14) | +60.23 (14) | +0.82 (10) |
| ls-json | −0.39 (3) | +7.85 (14) | −0.09 (5) |

## What went in

| # | idea | source | files |
|---|---|---|---|
| 4a | prune planning by position | rewritten from prototype `p-fpseq` | `index/associated_position.go` (new), `index/associated_data.go` (`All`), `repository/prune_position.go` (new), `repository/prune.go` |
| 4b | index rewrite without reloading kept files | rewritten from prototype `p-fprw` | `index/index_rewrite_keep.go` (new), `index/master_index.go` (4 small edits) |
| 1 | direct index decode and adopt | T2 M003 | `index/index_decode_eager.go`, `index/index_adopt.go` (unchanged), `index/index_parallel.go` (1 line), `index/master_index.go` (`Load`) |
| 2 | early check structure pass | T1 M004 | `cmd/restic/check_structure_overlap.go`, `cmd/restic/cmd_check.go` (unchanged) |
| 2 | one-lookup prune count | T1 M004 | `index/associated_count.go`, kept with a position variant; subsumed by 4a |
| 3 | restore tree carry | T3 M003 | `restorer/tree_carry.go`, `restorer/tree_prefetch.go` (unchanged), two hunks in `restorer/restorer.go`; measures null |

## What was left out, and why

- **T2's `index.go` hunk:** S017 already holds T1's equivalent reservation in `DecodeIndex`.
- **T2's other files** (archiver tree reuse, `repack_small`, `restore_small`, `filerestorer`, ui): not the named idea,
  and they overlap S017's installed changes.
- **T3's small-restore loader** (`restore_small.go`, `filerestorer.go`, the `smallLoader` hunk): it rewrites the same
  code as S017's bounded third restore worker. The tree carry alone reads null on S017, so T3's judged restore gain
  most likely sits in this loader.
- **T3's other files** (`data/node_json_*`, `tree_json_buffer`, `index_decode_reserve`, archiver edits): not the named idea.
- **The prototypes' own code** (`cm8_seq.go`, `cm8_rw.go`, `SequentialHint`): both mechanisms were rewritten.

## How the conflicts were settled

- **`index/master_index.go`:** S017's file is the base. Added: one field, `fileNotes`; in `Load`, the note of each
  loaded file plus T2's `firstOwned` / `adoptLoadedIndex`; in `Rewrite`, the skip of files kept unread; in
  `saveIndex`, the note of each file this process writes. The note is taken before `Insert` and copies pack IDs and
  blob counts, so T2's adoption cannot change it.
- **`index/index.go`:** S017's file, unchanged.
- **`repository/prune.go`:** S017's file plus the positional walk at five call sites. T1 M004's count edit is replaced
  by `IncrementAssociatedCountAt`.
- **`cmd_check.go`, `tree_prefetch.go`:** S017 had not changed them, so the team versions apply as they are. In
  `restorer.go` only the two tree-carry hunks were taken.

## How the two prototype gaps were closed

**1. The cursor in `p-fpseq` was not safe for concurrent callers.** The cursor is gone.

- The slot of a handle is `firstIndex(id)`. The new walk `MasterIndex.EachSlot` reports every entry in the order of
  `Values` together with its slot, computed from the index alone.
- Every hash chain is ordered by descending position (`add`, `addIfMissing` and the rehash all prepend), so an
  earlier entry with the same ID can only follow the entry in its chain.
- The bloom bits in the entry's `next` word cover exactly those following entries. When they exclude the ID, the slot
  is the position itself; otherwise the rest of the chain is followed.
- Nothing is remembered between calls; no table and no extra memory.
- `GetAt` / `SetAt` / `DeleteAt` / `IncrementAssociatedCountAt` take the slot. `SlotUnknown` (another lister, or an
  index that is not merged) falls back to the hash lookup.
- `All` uses the walk with the same handles, values and order. Locks are those of `Values`.
- Own test `TestEachSlotMatchesLookup`: 7,200 entries over 3,000 IDs with crowded bloom bits. Slot equals `blobIndex`
  for every entry, `All` equals the lookup form, and eight concurrent walks agree.

**2. `p-fprw` only checked that no pack is listed twice.** Stock compares the content of every pack entry
(`PackBlobsHash`, which covers the pack ID) with the files processed before it, in arbitrary order. The merged tree
keeps an index file unread only when all four hold:

- (a) every index file to process is known to this process, noted at load or at write (a file is named by its
  content hash);
- (b) it lists no excluded pack;
- (c) stock's own `Full` and `Oversized` say full and not oversized on its blob counts, evaluated at rewrite time on a
  fresh `Index`;
- (d) every pack ID it lists occurs exactly once over all index files to process.

Under (d) no entry of that file can equal an entry of another file in any order, so stock keeps it and its hashes are
never needed. A pack ID that occurs twice (old-repository duplicates, an interrupted prune, `repair index` leftovers)
sends every file listing it through the unchanged stock loop, which reads it and compares content. An unknown index
file disables the skip for the whole rewrite. Index files written by this run's repack are noted in `saveIndex`.

Own test `TestRewriteKeepsUnread`: six files (two full with their own packs, two sharing an identical pack entry, one
with an excluded pack, one not full). Two are not reread, and the surviving files, pack list and blob counts equal a
full-reload rewrite.

## Rules check

- Only non-test `*.go` under `cmd/` and `internal/` changed, plus one own test file
  (`internal/repository/index/associated_position_merge_test.go`). `go.mod` and `go.sum` untouched.
- No name or signature used by the stock tests changed.
- No integrity check was removed. Files kept unread were read and authenticated by this same process at index load.
- List order and the lock wait are untouched. Nothing new starts during the lock wait.

## Per-idea benches (research lanes, paired, 5 pairs unless noted)

Steps: `fp` = S017 + idea 4; `fpdec` = + idea 1; `fpdecck` = + idea 2; `all` = + idea 3 = final.

| step | versus | workload | result |
|---|---|---|---|
| `fp` | S017 | big-index forget-prune | +53.2% (6.74 → 3.18 s), 5/5; peak 504 → 402 MiB |
| `fp` | S017 | main forget-prune | +5.1%, 5/5 |
| `fp` | S017 | large-files forget-prune | +0.6%, unresolved |
| `fpdec` | S017 | big-index, all 7 | bi +2.75, bu +4.62, bm +4.20, restore +2.14, check +3.06, prune +54.7 (6.72 → 3.00 s), ls +8.62; all 5/5; peak lower in every phase |
| `fpdec` | S017 | main, all 7 | prune +5.0 (5/5), ls +0.45 (4/5); the rest inside noise (−1.1 to +0.6) |
| `fpdec` | S017 | large-files, all 7 | all inside noise (−1.2 to +0.8) |
| `fpdecck` | `fpdec` | check-read-data | main +1.93 (4/5, gain); big-index +0.68, 10 pairs +0.78 (6/10); large-files +0.34 |
| `all` | `fpdecck` | restore | main +0.58 (4/5), big-index −0.72 (1/5), large-files +0.07; 10 pairs: main +0.37 (7/10), big-index −0.17 (3/10): null |

Final tree vs S017, seven phases, 5 pairs (research lanes): main 16.33 → 16.26 s (+0.44%), summed phase peaks 573 →
576 MiB; big-index 24.74 → 20.46 s (+17.30%), peaks 3,007 → 2,443 MiB; large-files 14.57 → 14.59 s (−0.11%, all inside
noise), peaks 550 → 556 MiB. Stage timeline, big-index forget-prune: total 6.73 → 3.03 s wall (11.57 → 6.02 CPU-s);
index load after the lock 0.27 → 0.15 s; plan 3.24 → 1.08 s; execute 2.80 → 1.38 s; peak 561 → 319 MiB.

## Tests, oracle, race (final tree)

- Stock tests, all 53 packages with tests: 1,683 pass.
- Own tests: the two new ones pass. Over the five changed packages 434 pass and 2 fail; both fail identically on
  unmodified S017 (cmd/restic `TestRestoreFailedLockConfirmationDrainsPreparedIndexBeforeUnlock`, nil pointer;
  `internal/repository/lock_file_wait_work_test.go` does not compile against the stock-overlaid helpers).
- Oracle: the judge's per-phase oracle against stock passes on all 7 phases of all 3 workloads.
- `race_check --full`: TestRestore 0/200 with 4 parallel copies; `restore latest` 0/100 and `ls latest` 0/100 during
  128 concurrent stock backups.

## Open points

- **Main's non-prune phases read −0.2% to −0.4% each at 42 rounds** (3–13 ms per process); forget-prune (+5.05%) and
  check (+0.91%) carry main's +0.16%. The research-lane benches did not show it; no attribution was run.
- **Memory on main and large-files is slightly up** (+0.64% and +1.00%), inside the 2% guard; source not isolated.
- **The tree carry is worth nothing by itself on S017.**
- The final tree carries S017's evidence folders (it is a copy of `incumbents/S017`) and one new own test file.

## Files

Local, `merge-S017\`: `PROGRESS.md`, `merged-vs-S017.diff`, `tools\` (`judge_merge.py`,
`summ_judge.py`, box scripts), `src\` (edited sources), `src-s017\` (pristine). Evidence:
`evidence\restic-merge-s017-20261004\<job id>\result.json`.
Box, `/opt/league/restic-merge-s017/`: `final` (the judged tree), `steps/{fp,fpdec,fpdecck,all}`, `out/*.json`
(bench, oracle, race, test results), `PROGRESS.log`.
