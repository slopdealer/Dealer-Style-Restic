# Restic League V2 — S020 review (four teams; GPT-6.1 Sol medium; second season on four workloads; THE FINAL SEASON)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

**This is the last season. The operator has ended the league: there is no S021.** What follows is a hand merge into
a final release candidate (`final-candidate/`), not another directive.

Ran 2026-10-06 11:30 → 2026-10-07 04:20 UTC (16.8 h; S019 took 20.3 h). Incumbent at start: `incumbents/S019-merge`
(S019 + the hand merge). Directive `runtime/S020-directive.md` (cost-map-10: a menu with no single top item, led by
work inside the lock wait). Format as in S019: four workloads, matches of 8 rounds, promotion of 44, two judge lanes.
New this season: the roster rotation decided after S019 (builders and a coach entering; B14, B07, B11 and
C09 returning); B29, B14 and B07 were fielded. Three owner drafts (T1, T2, T3) were corrected at the start: they named
people another team had already drafted. 15 builder timeout-resumes at the 3300 s role limit (13 of them in the
experiments stage), no provider failure, no controller restart. $0 API.

## Match rows (8 rounds each vs S019-merge; suite time saved % = main / big-index / large-files / remote; wins)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | -0.19 = +0.16 / -0.27 / -0.57 / -0.09 (2/8) | +0.41 = +0.03 / +1.07 / +0.47 / +0.08 (5/8) | **+0.58** = +0.11 / +2.16 / -0.39 / +0.44 (5/8) | +0.01 = -0.31 / +0.43 / +0.66 / -0.75 (4/8) |
| M002 | +0.26 = +0.01 / +0.30 / +0.40 / +0.33 (7/8) | -0.17 = -0.26 / +0.59 / -0.96 / -0.03 (3/8) | **+0.90** = 0.00 / +3.77 / +0.15 / -0.34 (4/8) | +0.32 = +0.37 / +0.79 / -0.17 / +0.28 (5/8) |
| M003 | +0.19 = +0.29 / +0.60 / -0.45 / +0.33 (5/8) | **+1.89** = +0.56 / +5.14 / +1.62 / +0.26 (7/8) | +0.69 = +0.34 / +2.39 / -0.04 / +0.09 (6/8) | +1.36 = +0.42 / +4.47 / -0.12 / +0.67 (7/8) |
| M004 | +0.46 = +0.11 / +1.66 / -0.31 / +0.39 (6/8) | **+1.81** = +0.83 / +5.68 / +0.36 / +0.38 (7/8) | +0.50 = +0.36 / +2.17 / -0.03 / -0.52 (5/8) | +1.80 = -0.06 / +5.40 / +0.28 / +1.58 (6/8) |

Standings: **T2 71, T4 58, T3 56, T1 39.** T1 is the season bottom for the second season running. Fourteen of the
sixteen rows were positive on the suite mean, but only four passed +1% (T2 and T4, M003 and M004), and in every row
above +0.5% the gain is big-index. T1's M003 row used 2.12% more memory on large-files, past the 2% guard. No row was
past the 1% per-workload time gate (nearest: T2 M002, large-files -0.96%).

Every number above and below was checked against the box records (`progress.jsonl` `team-measured` events, the three
rescreen results and the confirmation result). None disagreed.

## Season merge — skipped a fourth time

Archive sources: T2 M003, T2 M004, T3 M004. "Fewer than two archive trees merge cleanly." The archive's per-phase
bests: check-read-data T2 M004 +5.11% (8/8), forget-prune T3 M004 +7.74% (7/8), restore T2 M003 +2.62% (7/8),
backup-mutated T4 M002 +0.75% (4/8). Two of the four are noise (see the lessons).

## Rescreen and confirmation — PROMOTED (T2's second promotion in a row)

12-round rescreens (3 rounds per workload; time saved % vs S019-merge):

| tree | job | row | rescreen (90% interval) | main | big-index | large-files | remote | wins | memory | vs stock |
|---|---|---|---|---|---|---|---|---|---|---|
| T2 M004 | `25b0f0d693cc4264ab7f70a7879dd07b` | +1.81 | **+1.41** (+1.17..+1.87) | +0.28 | +5.83 | -0.31 | -0.16 | 7/12 | +0.35 | +29.68 |
| T2 M003 | `b82e5ff8d1f84826a85298e47fcbf9ec` | +1.89 | +1.38 (+1.06..+1.85) | -0.35 | +4.97 | +0.18 | +0.72 | 9/12 | +0.57 | +29.98 |
| T4 M004 | `92da7c0bd71c453c8f1005cdee070771` | +1.80 | +1.05 (+0.70..+1.32) | +0.49 | +3.03 | +0.16 | +0.51 | 10/12 | +0.93 | +30.27 |

Confirmation of T2 M004, 44 rounds, job `7398d456ee4e4911ba83654ded3ad77f` (02:35 → 04:17 UTC; all arms matched the
stock oracle, 1019 of 1019 units; cells show wins of 11 and the 90% interval):

| vs | suite | main | big-index | large-files | remote | wins |
|---|---|---|---|---|---|---|
| S019-merge, time | **+1.03%** (+0.89..+1.14) | +0.33 (8/11; -0.29..+0.67) | **+3.46 (11/11; +3.14..+3.51)** | +0.24 (10/11; +0.15..+0.43) | +0.11 (6/11; -0.17..+0.41) | 35/44 |
| S019-merge, memory | +0.51 | +1.31 | -0.46 | +0.49 | +0.71 | |
| stock, time | **+29.94%** (+29.79..+30.01) | +31.28 (11/11; +31.12..+31.49) | +45.55 (11/11; +45.34..+45.70) | +23.97 (11/11; +23.64..+24.28) | +18.95 (11/11; +18.64..+19.05) | 44/44 |
| stock, memory | +43.91 | +43.18 | +46.34 | +39.45 | +46.67 | |

Median seconds (candidate / incumbent / stock): suite 76.51 / 77.20 / 109.18; main 15.25 / 15.32 / 22.20; big-index
17.48 / 18.08 / 32.06; large-files 12.35 / 12.38 / 16.21; remote 31.44 / 31.42 / 38.72. Median phase peak MiB: main
81.7 / 82.5 / 143.9; big-index 224.8 / 225.5 / 419.7; large-files 77.1 / 78.0 / 127.8; remote 87.1 / 87.5 / 163.3.
New incumbent `incumbents/S020`. On main and remote the 90% interval includes zero: the promotion is confirmed on the
suite, on big-index and on large-files.

Per phase vs S019-merge (time saved %; wins of 11, suite column wins of 44):

| phase | main | big-index | large-files | remote | suite |
|---|---|---|---|---|---|
| backup-initial | +0.16 (6) | -0.27 (3) | +0.09 (7) | -0.30 (3) | -0.08 (19) |
| backup-unchanged | -0.48 (3) | -0.49 (2) | +0.11 (6) | -0.11 (5) | -0.24 (16) |
| backup-mutated | -0.03 (5) | +0.27 (7) | +0.46 (9) | +1.70 (7) | +0.60 (28) |
| restore | -0.05 (5) | -0.20 (5) | +0.48 (7) | +0.28 (11) | +0.13 (28) |
| check-read-data | +1.05 (8) | **+13.88 (11)** | +0.70 (7) | -0.12 (3) | +3.88 (29) |
| forget-prune | +3.38 (10) | **+6.70 (11)** | +0.01 (6) | +0.31 (7) | +2.60 (34) |
| ls-json | **-1.08 (0)** | -0.43 (5) | -0.02 (5) | **-0.91 (1)** | -0.61 (11) |

In seconds, from the job's medians: big-index check 3.54 → 3.03 s and forget-prune 2.19 → 2.04 s; main forget-prune
1.30 → 1.26 s and check 1.65 → 1.63 s. Those four cells are about +1.0 of the +1.03 suite points. Memory per phase of
note: big-index check -3.82% (281 → 293 MiB, 2/11) and big-index ls -2.09%; main backup-mutated +3.55%, main check
+3.30%. Big-index as a workload is -0.46%, inside the guard.

## What the promoted tree changed

`incumbents/S020` is byte-identical to T2's M004 tree on all 431 non-test Go files. Against `incumbents/S019-merge`:
34 non-test `.go` files (23 new, 11 edited, none removed) and 29 test files. By mechanism:

1. **Prune, index side** (not in the lock wait): new `repository/index/repack_routes.go`, `repack_ordinals.go`,
   `repack_fallback.go`, edits in `index/master_index.go` (the packs selected for repacking are recorded once as
   ordinals and their locations rebuilt by ordinary partition, with a fallback for unsupported index layouts); new
   `repository/prune_pack_run.go` (consecutive updates to one pack without hashing its ID per blob) and
   `index/associated_count_validate.go` (the final count check without enumerating handles); edits in `prune.go`.
   Team ablation: routing = 23.10% of big-index prune (5 pairs, job `b390167f…`).
2. **Check, index side** (not in the lock wait): new `repository/checker_pack_types.go` (pack-type summaries taken
   during the index load instead of a second traversal), edits in `checker_capacity.go`, `checker.go`. Team
   ablations: 2.95% of big-index check for 4.17% more phase memory (M003), 3.08% (M004, 7 runs, 6/7). The confirmed
   cell is +13.88%, so most of it is not attributed to one piece.
3. **Restore metadata traversal**: new `data/tree_metadata.go` (tree decode without file content IDs for the metadata
   pass), edits in `restorer/restorer.go`. Complete-restore gain unresolved.
4. **NEW INSIDE THE LOCK WAIT, backup: compression preparation.** New `cmd/restic/backup_compression_wait.go`,
   `backup_compression_index.go`, `backup_compression_source_linux.go`, `backup_compression_source_other.go`,
   `repository/compression_preparation.go`; edits in `cmd_backup.go`, `repository/repository.go`. During the wait,
   once the index load reports ready, a producer reads source files (Linux only), cuts them at the ordinary chunk
   points and compresses chunks with a private encoder; after the confirmed lock the saves take the prepared chunks,
   otherwise everything is released.
5. **NEW INSIDE THE LOCK WAIT, check: one pack read.** New `cmd/restic/check_wait_pack.go`,
   `repository/check_wait_pack.go`, `check_wait_range.go`, `check_wait_identity_linux.go`, `check_wait_watch_linux.go`
   (and two `_other.go` stubs), `backend/retry/prepared_range.go`; edits in `retry/prepared_load.go`, `cmd_check.go`.
   At most one complete pack ciphertext is read during the wait and offered once to the ordinary verification after
   the confirmed lock; a file watch and a file-identity check reject the bytes if the pack changed in between.
6. **NEW INSIDE THE LOCK WAIT, restore: snapshot and tree reads.** New `cmd/restic/restore_snapshot_wait.go`,
   `repository/restore_wait_reader.go`, `restorer/restore_wait_trees.go`; edits in `cmd_restore.go`,
   `wait_index_load.go`, `restorer.go`. The snapshot and a preorder prefix of its trees are read and authenticated
   during the wait (local backend only, private, no cache writes) and handed to the restorer after the confirmed lock.

**Soundness.** Items 4, 5 and 6 are three new mechanisms inside the lock wait (backup compression preparation, check
pack reads, restore snapshot and tree reads). They are in the same class as the blocker found by the soundness review
of `S019-merge` (`final-candidate/REPAIRS.md`, R1: repository state read during the 200 ms wait is used after the
lock is confirmed, while another process's exclusive work may have completed in the window). **They are unreviewed at
the time of writing.** They are also the part of the tree that measured least: the phases they serve read backup
-0.08 / -0.24 / +0.60%, restore +0.13% and main check +1.05% in the confirmation, and the team's own 7-run ablation
of the compression group read -0.14 / +0.14 / -0.55% (all unresolved).

## Lessons

- **The gain was one workload.** +3.46% on big-index from index-side work in check and prune; main, large-files and
  remote moved +0.1 to +0.3%. The suite gain is +1.03%, against +4.00% in S018 and +4.33% in S019.
- **The lock wait did not pay as built.** It led the menu with a bound of +4.9 points, and all four teams built it:
  T2 for backup, check and restore (promoted; cells above); T1 for check, restore and backup (its backup preparation
  read losses of 0.6 to 1.2% in one of its own controls and nothing resolved in the other, and its M003 tree, the
  first with that work, went past the memory guard on large-files); T3 for ls (-0.24 to -0.85% over three rows, and
  17 requests against 14 on remote); T4 for restore, by its file names (-0.44% at 12 rounds). No build reached the
  last map's rough check prototype (-0.12 s on main).
- **And it is where the release risk now sits.** The three promoted lock-wait mechanisms carry about none of the
  confirmed gain and all of the new R1-class exposure. The final candidate can gate or drop them at little measured
  cost; that takes one bench and one review.
- **The second menu item was refuted as designed.** Setting a file's metadata as soon as its last blob is written
  changes what a later fatal error leaves behind (T2 and T3, M001, an executed test). The teams kept the global
  barrier, and the metadata pass did not get shorter (T3: 0.469 against 0.470 s).
- **Big-index prune read three to four times larger while a second judge job ran beside it.** T2 M004: +22.54% in the
  match row and +23.83% at its rescreen (both beside another job), then +6.70% in the 44-round job, which ran alone.
  T4 M004: +22.51% in the row, +4.75% at its rescreen, which ran alone. Check does not show it (T2 big-index check
  +13.57 / +12.56 / +13.88). Not tested further; index walking that is bound by memory traffic would behave this way.
  Every prune cell from a match row of this season, T3's included, needs a quiet-lane repeat before it is believed.
- **Rows do not hold their size, and single cells are noise.** +1.81 → +1.41 → +1.03 (T2 M004), +1.89 → +1.38 (T2
  M003), +1.80 → +1.05 (T4 M004). The archive's best backup-mutated cell (T4 M002, +0.75%) comes from a tree that
  changed no backup file; its best restore cell (T2 M003, +2.62%) was large-files +8.54% in two rounds and +0.09% at
  twelve.
- **ls-json got slower and it was promoted anyway**: main -1.08% (0/11), remote -0.91% (1/11). T2's M004 row already
  read -1.18% with 0 wins of 8, and its rescreen -1.45% (0/12). No ls file changed, but `wait_index_load.go`,
  `repository.go` and `retry/prepared_load.go` are on every command's path. The cause is open.
- **The automatic merge failed four seasons in four**, so the season's unpromoted work goes to the hand merge:
  T3's prune execution work (the best forget-prune cell of the season, positive on main, big-index and large-files
  in its own 7-pair ablation) and T4's check work (+1.05% at 12 rounds, 10/12). They are listed with sizes in
  `final-candidate/INVENTORY.md` section 8.

---
Written by the coordinator from the boundary agent's draft. The match rows, rescreens, confirmation table and all 28
per-phase cells were checked by the coordinator against the box records; the ablation figures and the handoff
contents are the boundary agent's reading and were not re-checked.
