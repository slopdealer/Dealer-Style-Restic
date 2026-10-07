# Restic League V2 — S011 review (four teams, GPT-6 Luna max; fast format; cost-map-3 directive)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-30 03:37 → ~14:00 UTC (~10.4 h including confirmation). Incumbent at start: `incumbents/S010`
(+26.88% vs stock). Directive `runtime/S011-directive.md` (cost-map-3 on S009, 11-item measured menu). Last Luna
season: S012 onward runs on GPT-6.1 Sol (high). $0 API.

## Match rows (6 rounds each vs the S010 incumbent; score = mean of time and peak-memory saved)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | +0.66 (t +2.54, m −1.05, 4/6) | +0.57 (t +0.58, m −0.08, 4/6) | **+1.06 (t −0.39, m +2.33, 6/6)** | +0.86 (t +0.08, m +1.27, 5/6) |
| M002 | **+2.68 (t +2.85, m +1.85, 6/6)** | +0.54 (t +0.31, m +0.58, 4/6) | +2.25 (t −1.11, m +5.61, 6/6) | +1.39 (t −1.06, m +3.47, 6/6) |
| M003 | +1.68 (t −0.11, m +3.20, 6/6) | +1.29 (t +1.18, m +1.28, 5/6) | **+2.46 (t −2.00, m +6.54, 6/6)** | +1.77 (t −1.08, m +5.36, 6/6) |
| M004 | +2.20 (t +1.11, m +2.70, 6/6) | +1.54 (t −3.93, m +5.87, 4/6) | **+2.53 (t −1.65, m +7.21, 6/6)** | +2.47 (t −1.22, m +5.94, 5/6) |

Match points: M001 T3 29 / T4 22 / T1 18 / T2 14; M002 T4 24 / T3 23 / T1 17 / T2 15; M003 T3 35 / T4 24 / T2 12 /
T1 10; M004 T3 27 / T4 23 / T2 18 / T1 14. Standings: **T3 114, T4 93, T1 59, T2 59.**

## Confirmation — PROMOTED

Nominee T1 M002 (highest screen). 42-round job `f6e33b96d0fc4659b0b593de173d6fa9`: **score +2.10% vs S010, 42/42;
time +1.64%, peak memory +2.43%.** **Vs stock: +27.50% (time +16.91%, memory +38.30%), 42/42.** New incumbent
`incumbents/S011`. Changes against S010: `internal/fs/file.go`, `internal/repository/index/index.go`,
`internal/repository/repository.go`, `internal/restorer/restorer.go` (restore time −7.7%, prune peak memory −9.8%:
cost-map-3 items 1 and 4).

## Lessons

- **cost-map-3 paid again**: rows rose from S010's −1..+1 to +1.3..+2.7, and the confirmed gain (+2.10) is the
  largest since S008. Re-mapping when gains flatten remains the best lever this league has.
- **Nomination still takes the highest screen, not the most solid row.** T3's M004 (+2.53, 90% lower bound +1.92,
  the season's highest) was not confirmed; T1's M002 (lower bound +0.97) was, and held. T3's backup-memory mechanism
  is largely disjoint from T1's restore work, so it is the headline lead in the S012 directive.
- Most S011 gains were memory bought with some time (T3, T4 rows are time-negative); only T1 gained on both axes.
- Restic V2 vs stock, confirmed chain: S004 +8.57 → +15.07 → +17.82 → +18.59 → S008 +24.98 → +25.62 → +26.88 →
  **S011 +27.50**.
