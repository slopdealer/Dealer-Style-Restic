# Restic League V2 — S015 review (four teams, GPT-6 Luna max; third speed-scored season)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-10-02 08:04 → ~18:10 UTC (~10.1 h including rescreens and confirmation). Incumbent at start:
`incumbents/S014` (time +21.02% vs stock). Directive `runtime/S015-directive.md` (cost-map-5). Research lanes on the
judge's `performance` governor for the whole season. $0 API.

## Match rows (6 rounds each vs the S014 incumbent; score = time saved %, memory in brackets)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | +0.59 (m −0.30, 5/6) | skipped (= incumbent) | +0.44 (m −0.51, 5/6) | **+1.38 (m −0.60, 6/6)** |
| M002 | +0.84 (m −0.55, 6/6) | +0.03 (m −0.76, 3/6) | +0.41 (m +0.16, 5/6) | **+1.21 (m +0.45, 6/6)** |
| M003 | **+1.03 (m +0.34, 6/6)** | skipped (= incumbent) | +0.44 (m +1.12, 4/6) | +0.82 (m +0.27, 6/6) |
| M004 | +0.49 (m −0.71, 6/6) | skipped (= incumbent) | +0.10 (m +0.61, 3/6) | **+1.37 (m −1.26, 6/6)** |

Match points: M001 T4 26 / T3 17 / T1 13 / T2 0; M002 T4 22 / T3 14 / T1 11 / T2 11; M003 T1 24 / T4 16 / T3 11 /
T2 0; M004 T4 28 / T1 11 / T3 7 / T2 0. Standings: **T4 92, T1 59, T3 49, T2 11.**

## Rescreen and confirmation — PROMOTED

12-round rescreens (all T4): M001 +1.38 → +1.51, M002 +1.21 → +1.12, M004 +1.37 → **+1.54**. Confirmation went to
T4 M004. 42-round job `f3628902f87a4b8c9d1729472849a2d9`: **time +1.47% vs S014, 42/42; memory −0.29% (inside the
guard).** **Vs stock: time +22.58%, memory +42.26%.** New incumbent `incumbents/S015`.

The tree extends the lock-dwell overlap to every command: `cmd/restic/wait_index_load.go` plus `lock.go`,
`cmd_restore.go`, `cmd_check.go`, `cmd_prune.go`, `cmd_forget.go`, `cmd_ls.go` (index load runs inside the 200 ms wait
for restore, check, prune and ls; S013 had it for backup only), and a `parent_tree.go` change.

## Lessons

- **The biggest lever went unbuilt because of a directive error (coordinator's).** The operator ruling allowing a third
  read worker was added to the S015 directive, but an older line requiring the owner's product argument was left in
  the ground rules; teams read both and declined. Also the stock `backend/local/config_test.go` pins
  `ParseConfig` at `Connections: 2`, so the limit must be raised downstream. Both are fixed in the S016 directive.
- **T2 shipped nothing in three of four matches.** Its lead builder reverts on single-round probes (for example a
  +0.77% time prototype dropped because one round read +2.2% memory, which is inside per-round noise) and on
  speculative behaviour concerns. One-round readings should go to the six-round judge, not decide a revert.
- Gains are steady but small (+1.35, +1.47 in the last two seasons): dwell overlap and prefetch are now mostly
  harvested. Remaining size is in read-side workers (cost-map-6: check −0.55 s, restore −0.66 s with memory bounded)
  and restore's second-pass window (−0.33 s).
- Restic V2 time vs stock: S012 +17.38 → S013 +19.69 → S014 +21.02 → **S015 +22.58% faster**, memory ~42% less.
