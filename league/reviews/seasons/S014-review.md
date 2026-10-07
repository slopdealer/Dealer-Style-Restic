# Restic League V2 — S014 review (four teams, GPT-6 Luna max; second speed-scored season)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-10-01 19:56 → ~2026-10-02 07:55 UTC (~12 h including rescreens and confirmation). Incumbent at start:
`incumbents/S013` (time +19.69% vs stock). No new directive was attached at launch, so the briefs carried the S013
speed menu (whose two top items S013 had already installed). Research lanes switched to the `performance` governor
mid-season (2026-10-02). $0 API.

## Match rows (6 rounds each vs the S013 incumbent; score = time saved %, memory in brackets)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | +0.02 (m −0.12, 3/6) | +0.12 (m +1.11, 4/6) | −0.08 (m +0.56, 2/6) | skipped (= incumbent) |
| M002 | +0.23 (m +1.64, 4/6) | +0.18 (m −0.31, 5/6) | +0.31 (m −0.49, 4/6) | skipped (= incumbent) |
| M003 | −0.14 (m +0.53, 1/6) | +0.81 (m +0.23, 6/6) | −0.02 (m −0.28, 3/6) | **+1.30 (m +0.84, 6/6)** |
| M004 | +0.08 (m +0.77, 4/6) | +0.88 (m +2.15, 6/6) | +0.14 (m +0.91, 4/6) | **+1.49 (m +0.52, 6/6)** |

Match points: M001 T1 22 / T3 16 / T2 12 / T4 0; M002 T1 17 / T3 15 / T2 13 / T4 0; M003 T2 20 / T4 16 / T3 9 / T1 9;
M004 T2 24 / T1 12 / T3 11 / T4 10. Standings: **T2 69, T1 60, T3 51, T4 26.**

## Rescreen and confirmation — PROMOTED

12-round rescreens: T4 M003 +1.30 → **+1.44** (higher than T4 M004's reading), T2 +0.81 → +0.40. Confirmation went to
T4 M003. 42-round job `c90c07ce3f12428580abfbf0481409c2`: **time +1.35% vs S013, 42/42; memory +0.20%.** **Vs stock:
time +21.02%, memory +41.19%.** New incumbent `incumbents/S014`.

The tree adds restore tree prefetch (`internal/restorer/tree_prefetch.go`, `restorer.go`): restore −5.6% time, every
other phase within ±0.5% (cost-map-5 item 3, "restore first-pass prefetch").

## Lessons

- **A stale directive cost the first half of the season.** M001-M002 rows were all within ±0.3 (noise): teams were
  working a menu whose top items were already installed. T4 skipped both and then found the one real lever left in
  restore. Every season needs its own directive; S015 has cost-map-5's.
- **The rescreen earned its keep:** it re-ranked T4's two rows (M003 over M004) and cut T2's +0.81 to +0.40, so the
  confirmation slot went to the steadier tree, which then confirmed at +1.35 (42/42).
- Points and promotion diverged: T4 finished last on points (two skipped matches) yet produced the promoted tree.
- Restic V2 on the time axis vs stock: S012 +17.38 → S013 +19.69 → **S014 +21.02% faster**, memory held at ~41% less.
