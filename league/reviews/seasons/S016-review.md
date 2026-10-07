# Restic League V2 — S016 review (four teams; GPT-6 Luna max, then GPT-6.1 Sol medium from mid-season; fourth speed-scored season)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-10-02 18:20 → 2026-10-03 10:20 UTC (16.0 h, including a ~1.7 h controller outage, the three 12-round
rescreens and the confirmation). Incumbent at start: `incumbents/S015` (time +22.58% vs stock). Directive
`runtime/S016-directive.md` (cost-map-6). $0 API.

## Match rows (6 rounds each vs the S015 incumbent; score = time saved %, memory saved % in brackets)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | +1.16 (m −0.31, 6/6) | skipped (= incumbent) | **+2.72 (m −1.61, 6/6)** | +1.18 (m +1.56, 6/6) |
| M002 | +1.83 (m −1.52, 6/6) | +3.57 (m −1.45, 6/6) | **+3.64 (m −0.46, 6/6)** | +1.00 (m −0.28, 6/6) |
| M003 | **+4.20 (m +1.28, 6/6)** | +3.51 (m +1.42, 6/6) | +3.58 (m −1.02, 6/6) | +1.61 (m +1.49, 6/6) |
| M004 | **+4.83 (m +0.86, 6/6)** | +3.86 (m −1.53, 6/6) | +4.04 (m −0.94, 6/6) | +3.13 (m +1.15, 6/6) |

Match points (whole-workload rank + per-phase time cells): M001 T4 22 / T3 13 / T1 6 / T2 0; M002 T2 22 / T3 14 /
T1 13 / T4 6; M003 T1 20 / T4 15 / T2 14 / T3 9; M004 T1 19 / T4 19 / T2 16 / T3 12. Standings: **T4 62, T1 58,
T2 52, T3 48.** Fifteen of sixteen rows were 6/6 time-positive, the strongest season since S008. T4 led the table on
phase cells (backups, ls, forget-prune) while never holding the best whole-workload row.

## Rescreen and confirmation — PROMOTED

12-round rescreens: T1 M004 +4.83 → **+4.67**, T1 M003 +4.20 → +4.51, T3 M004 +4.04 → +3.97. Confirmation went to
T1 M004. 42-round job `9b3da9b7251a4ea3a73293aff741b33a`: **time +4.71% vs S015, 42/42 (90% interval +4.65..+4.80);
memory +0.84% (less memory).** **Vs stock: time +25.75%, memory +42.18%.** Medians: S016 16.67 s, S015 17.48 s, stock
22.47 s. New incumbent `incumbents/S016`.

Per phase vs S015 (time): check-read-data **+22.9%**, restore **+6.3%**, backup-unchanged +1.1%, backup-mutated +0.9%,
backup-initial +0.4%, ls-json +0.3%, forget-prune −0.5%.

Files changed vs `incumbents/S015` (non-test `.go`): `internal/backend/sema/{backend,semaphore}.go`,
`internal/backend/limiter/limiter_backend.go`, `internal/backend/local/config.go`, `internal/global/global.go`,
`internal/repository/checker.go` (a bounded third check worker behind an extra check-only backend slot);
`internal/restorer/restorer.go` (second-pass window 8 → 64) and the new `internal/restorer/node_lookahead.go` with
`internal/data/tree_node_reader.go`, `internal/data/tree.go`, `internal/data/node_json_scalar.go` (first-pass node
lookahead: upcoming nodes are decoded while the ordered walk handles the current files);
`internal/fs/fs_local.go` with the new `path_stat_linux.go` / `path_stat_other.go` (direct Linux path stat, one
allocation less per lstat). `cmd/restic` is unchanged from S015.

## Lessons

- **The corrected directive unlocked the read-worker lever.** With the worker-count ruling stated once and cleanly,
  three teams built a bounded third check worker in M001-M002 (check −0.45 s for +5.6 MiB) and the restore window;
  S015 had left both unbuilt. The season's gain (+4.71) is three times S014's or S015's.
- **Mid-season swap Luna → Sol medium (operator, 2026-10-03).** M001 and T4's M002 ran on GPT-6 Luna max; every later
  row ran on GPT-6.1 Sol medium. T2 shipped an unchanged tree in M001 (as in three of four S015 matches) and then
  +3.57, +3.51, +3.86 after the swap. The season keeps both model ids so Luna receipts stay valid.
- **The swap cost a ~1.7 h outage.** The controller died three times on "Experiment receipt continuation changes
  role identity": one receipt-identity check still compared the model across a continuation chain. The chainer's
  resume budget (3) was spent before a human looked. Fix: model removed from that identity tuple. A model swap touches
  every receipt-identity check, not only the role validator.
- **The incumbent carried a real concurrency bug.** S013's and S015's lock-wait overlap lists the index before the
  snapshots (stock lists snapshots first). With a backup running at the same time, `restore latest` / `ls latest`
  failed about one run in three ("id not found in repository"); the stock `TestRestore` caught it only under CPU load
  (31 of 1000 parallel runs on the S016 tree). The judge's workload never runs two commands at once, so correctness
  passed every season. S016 inherits it unchanged (`cmd/restic` is S015's). Fixed at the season boundary
  (`race-fix/REPORT.md`; installed as `incumbents/S016-racefix`), and the rule goes into the S017 directive: never
  list the index before the snapshots.
- **Gains are fitted to the main workload.** On the other visible workloads S015 held about half of its main-workload
  gain (`judge/SUITE.md`). From S017 the score averages three workloads.
- Restic V2 time vs stock: S013 +19.69 → S014 +21.02 → S015 +22.58 → **S016 +25.75% faster**, memory ~42% less.
