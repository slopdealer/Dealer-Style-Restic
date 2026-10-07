# Restic League V2 — S009 review (four teams, GPT-6 Luna max; fast format, second season on cost-map-2)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-29 06:14 → ~17:55 UTC (~11.7 h including the 42-round confirmation). Incumbent at start:
`incumbents/S008` (+24.98% vs stock). Directive `runtime/S009-directive.md` (S008's installed items marked; aimed at
restore, read-side workers, lock-wait overlap, ls, index, backup-initial CPU, prune peak). $0 API.

## Match rows (6 rounds each vs the S008 incumbent; score = mean of time and peak-memory saved)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | **+1.47 (t +0.69, m +2.25, 6/6)** | −0.24 (t +0.14, m −1.16, 1/6) | skipped (= incumbent) | +0.75 (t +0.96, m +0.40, 5/6) |
| M002 | **+1.71 (t −0.92, m +4.03, 6/6)** | +1.17 (t +0.62, m +1.40, 4/6) | +1.05 (t +0.53, m +0.84, 5/6) | +0.13 (t +0.22, m +0.91, 3/6) |
| M003 | **+1.89 (t +0.40, m +1.50, 5/6)** | +0.71 (t −0.12, m +1.15, 5/6) | +0.49 (t +0.42, m +0.68, 5/6) | +0.98 (t +2.18, m +1.07, 6/6) |
| M004 | **+1.76 (t −0.35, m +4.06, 6/6)** | +1.07 (t +0.16, m +1.36, 4/6) | +0.38 (t −0.20, m +0.79, 4/6) | +0.85 (t +0.90, m +0.20, 4/6) |

Match points: M001 T1 30 / T4 23 / T2 2 / T3 0; M002 T3 31 / T2 19 / T4 18 / T1 12; M003 T1 32 / T3 30 / T4 21 /
T2 6; M004 T1 25 / T4 21 / T3 15 / T2 8. Standings: **T1 99, T4 83, T3 76, T2 35.**

## Confirmation — PROMOTED

Nominee T1 M003. 42-round job `7c206f5a801d47f6b9b7339edd136795`: **score +1.30% vs S008, 38/42; time +0.52%,
peak memory +1.94%.** **Vs stock: +25.62% (time +15.94%, memory +35.50%), 42/42.** New incumbent `incumbents/S009`.
The tree changes three files against S008: `internal/restorer/restorer.go`, `cmd/restic/cmd_restore.go` and
`internal/repository/index/index.go` (restore path and index; menu items 4/8/10 ground).

## Lessons

- **Back to small, real gains.** With the big cost-map levers installed in S008, S009's rows sat at +0.1 to +1.9.
  One team (T1) found a repeatable restore/index line and led every match it scored in; it confirmed at +1.30 with
  38/42, so small gains still confirm when they are consistent (T1 won 23 of 24 rounds across its four rows).
- **Locked changes kept compounding:** T1 built M002-M004 on its own positive trees and never regressed below +1.47.
- **Duration crept up** (11.7 h vs S008's 10.6 h): the slowest team, not a barrier, now sets the tail, plus the
  95-110 min confirmation.
- Restic V2 vs stock, confirmed chain: S001 ~+3 → S004 +8.57 → synthesis +15.07 → S005 +17.82 → S006 +18.59 →
  S007 (none) → S008 +24.98 → **S009 +25.62**.
- S010 started 18:01Z with no new directive, so its briefs still carry the S009 block (the controller fact block,
  which names `incumbents/S009`, is authoritative). A fresh cost map on the S009 tree is the lever when gains stall.
