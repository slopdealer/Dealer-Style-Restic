# Restic League V2 — S007 review (four teams, GPT-6 Luna max; second open-scope season)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-28 06:20 → 19:38 UTC (~13.3 h, the last season on the old chained format). Incumbent at start:
`incumbents/S006` (+18.59% vs stock). Directive `runtime/S007-directive.md` (owner-chosen scope, menu, subfactor
points). $0 API.

## Match rows (6 rounds each vs the S006 incumbent; score = mean of time and peak-memory saved)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | −0.46 (t +0.17, m −0.63, 2/6) | +0.06 (t −0.10, m +0.22, 3/6) | **+2.08 (t +0.73, m +2.53, 4/6; 90% int [−0.95, +2.51])** | −0.24 (t +0.70, m −1.95, 2/6) |
| M002 | +0.63 (t −0.64, m +2.36, 5/6) | +0.09 (t +0.17, m +0.31, 3/6) | −0.75 (t −1.06, m −0.29, 2/6) | −1.01 (t +1.14, m −2.14, 2/6) |
| M003 | +0.27 (t −0.11, m +0.05, 3/6) | +0.53 (t +1.09, m −0.17, 4/6) | −0.24 (t −0.28, m −0.09, 3/6) | +0.65 (t +0.91, m +0.53, 4/6) |
| M004 | skipped (= incumbent) | +0.24 (t +0.76, m −0.30, 4/6) | +0.92 (t +0.24, m +1.81, 5/6) | −0.47 (t −0.16, m −0.47, 2/6) |

Match points (whole-workload + subfactor cells): M001 T4 26 / T2 24 / T3 20 / T1 1; M002 T1 24 / T2 18 / T3 10 /
T4 10; M003 T4 24 / T2 14 / T1 14 / T3 5; M004 T2 18 / T4 18 / T3 16 / T1 0.
Standings: **T4 78, T2 74, T3 51, T1 39.**

## Confirmation — NOT PROMOTED

Nominee T3 M001 (in-flight known-blob check in `file_saver.go` / `repository.go` / `master_index.go`; screened
+2.08). 42-round job `da1cf1e51cbd4af086245e433ad9ccdf`: **median score +0.014%, 21/42 wins, 90% interval
[−0.22, +0.71]** — fails the 29/42 and 0.25% gates. Incumbent stays `incumbents/S006` (+18.59% vs stock).

## Lessons

- **The 6-round screen picked a lucky row.** +2.08 with an interval spanning zero confirmed at +0.01. The single
  fresh confirmation goes to the highest screen, which systematically favours the luckiest draw; T3's M004 row
  (+0.92, 5/6, memory +1.81) was never confirmed. Worth changing: re-screen the top 2-3 rows at 12 rounds, or
  nominate on the interval's lower bound, before spending the 42-round slot.
- **Teams reworked their own winning code.** T3 started M002 from its M001 tree and re-edited the same three files
  (prune time +6.6 → −2.8). Fixed for S008: `lock_won_changes` (operator 2026-09-28).
- **No season gain for the first time since S003.** The S006 tree is near what the old areas can give; S008 runs on
  the fresh cost map (`cost-map-2`, measured prototypes +8.7 stacked) with a menu of new structural levers.
- **Season length ~13 h** drove the operator's "Seems very lengthy": S008 is the first season with `fast_chain`,
  `async_matches` and `parallel_owners` (target ~6-7 h).
- Restic V2 vs stock, confirmed chain unchanged: S001 ~+3 → S004 +8.57 → synthesis +15.07 → S005 +17.82 →
  **S006 +18.59** (S007 no promotion).
