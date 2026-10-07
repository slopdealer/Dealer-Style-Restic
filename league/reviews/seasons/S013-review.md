# Restic League V2 — S013 review (four teams, GPT-6 Luna max; FIRST SPEED-SCORED SEASON)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-10-01 09:00 → ~19:50 UTC (~10.8 h including the 12-round rescreen and confirmation). Incumbent at start:
`incumbents/S012`. Operator 2026-10-01: "Return to Luna 6 Max and target speed". Judge `3421f7f3`: score = workload
TIME saved, peak memory a +2% guard vs incumbent; subfactor points from the 7 per-phase time cells; performance CPU
governor on judge lanes 26-39 (+ siblings 74-87). Directive `runtime/S013-directive.md` (cost-map-4 speed map).
$0 API. Scores are NOT comparable with S001-S012 (equal-weight time + memory).

## Match rows (6 rounds each vs the S012 incumbent; score = time saved %, memory in brackets). All 15 rows won 6/6.

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | **+3.00 (m +1.64)** | +1.12 (m −0.05) | +2.09 (m +1.65) | skipped (= incumbent) |
| M002 | **+2.81 (m −0.56)** | +2.72 (m −0.02) | +2.29 (m −1.25) | +2.45 (m +1.28) |
| M003 | **+3.28 (m −0.16)** | +2.74 (m +0.45) | +2.18 (m −0.21) | +2.14 (m −1.13) |
| M004 | **+2.88 (m −1.26)** | +2.75 (m −0.60) | +2.26 (m +0.60) | +2.62 (m +1.38) |

Match points: M001 T1 31 / T3 22 / T2 15 / T4 0; M002 T1 24 / T2 24 / T4 18 / T3 13; M003 T1 24 / T2 21 / T4 18 /
T3 13; M004 T1 24 / T2 23 / T4 20 / T3 13. Standings: **T1 103, T2 83, T3 61, T4 56.**

## Rescreen (first season with it) and confirmation — PROMOTED

12-round rescreens of the top three nominees: T1 M001 +3.00 → **+2.92**, T1 M003 (or M004) 2.81 → 2.77, T2 2.74 →
2.43. The best 12-round reading (T1 M001) went to confirmation.
42-round job `651a07c937bf4b568e3022af48da10eb`: **time +3.11% vs S012, 42/42; memory +0.05% (flat).** **Vs stock:
time +19.69%, memory +41.64%, 42/42.** New incumbent `incumbents/S013`.

T1's tree touches the speed map's top levers: index loading started inside the lock dwell (`cmd/restic/
backup_index_load.go`, `repository/lock.go`, `lock_file.go`), poller-free opens (`internal/fs/file_open_linux.go`,
`fs_local.go`, `file.go`), and tree decode / restore writing (`internal/data/tree.go`, `parent_tree.go`,
`restorer/restorer.go`, `fileswriter.go`).

## Lessons

- **Speed scoring worked at once.** Every row was a real time gain (+1.1 to +3.3%, all 6/6), against S012 where 14 of
  16 rows were slower than their incumbent. Time vs stock moved +17.38% → **+19.69%** in one season, more than in
  S008-S012 combined, with memory held flat by the guard.
- **The speed map predicted it.** Its two measured top levers stacked to −0.77 s (+3.3); the confirmed tree is +3.11%
  (~0.72 s).
- **Rescreens behaved:** 6-round screens held within 0.1-0.3 at 12 rounds; the order was unchanged this time.
- **Directive hygiene:** S014 started 19:56Z without a new directive, so its briefs carry the S013 block (incumbent named
  as S012 there; the controller fact block names S013 and is authoritative). Write S015's before S014 ends.
- Restic V2 vs stock, confirmed chain (old equal-weight score to S012: +29.68); from S013 on the time axis: **+19.69%
  faster than stock** (S012 was +17.38%).
