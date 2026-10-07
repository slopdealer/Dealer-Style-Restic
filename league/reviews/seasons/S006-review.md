# Restic League V2 — S006 review (four teams, GPT-6 Luna max; first OPEN-SCOPE season)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-27 17:53 → 2026-09-28 06:12 UTC (~12.3 h). Incumbent at start: `incumbents/S005` (+17.82% vs stock).
Directive `runtime/S006-directive.md`: operator change 2026-09-27 — no assigned territories; owners get the whole of
restic and choose; areas A–D as a menu; overlap allowed; subfactor match points. $0 API.

## Match rows (6 rounds each vs the S005 incumbent)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | −0.05 (3/6) | −0.37 (2/6) | +0.43 (t +0.75, m +1.11, 3/6) | −0.35 (m −1.89, 2/6) |
| M002 | −0.03 (3/6) | −0.91 (2/6) | **+1.36 (t +0.76, m +1.53, 5/6, int [0.04, 1.95])** | −0.30 (2/6) |
| M003 | +0.30 (4/6) | +0.86 (m +2.81, t −1.17, 3/6) | +0.46 (4/6) | −0.96 (2/6) |
| M004 | +0.52 (3/6) | +0.13 (m +1.44, 3/6) | **+1.35 (t +0.76, m +1.65, 5/6, int [0.10, 1.86])** | skipped (= incumbent) |

Standings (whole-workload + subfactor points): **T3 85, T1 70, T2 52, T4 43.**

## Confirmation — PROMOTED

Nominee T3 M002. (Corrected 2026-09-28 by cost-map-2: the diff is read-side buffer retention plus pack hash-on-write,
not restore metadata; hashing landed in prune's single-worker repack path, which is why prune time fell back −8.9%.) 42-round job `faa13f6bcc2849cc9ffee41f5326686c`: **score +1.11%
vs S005, 32/42, time −0.26%, peak memory +2.30%; vs stock +18.59% (time +11.26%, memory +25.68%)**. New incumbent
`incumbents/S006`. A small, memory-side gain that held; the screen (+1.36) shrank modestly.

## Lessons

- Open scope did not unlock new ground in its first season: coaches' plans all touched the same hot files
  (repository.go, restorer.go, file_saver.go, checker.go), and only the team that stayed in one area (T3, Area C) found
  a repeatable gain. The remaining headroom is small and structural; tuning attempts stayed inside noise.
- Directive hygiene: the launcher keeps the last directive block in the briefs until replaced, so each season needs its
  own directive (S007's section 0 was written outcome-neutral before S006 closed).
- Restic V2 vs stock, confirmed chain: S001 ~+3 → S004 +8.57 → synthesis +15.07 → S005 +17.82 → **S006 +18.59**.
- Gains per season are flattening (+2.99 → +1.11). A fresh cost map on the S006 tree (the S004 map was measured on S001)
  or a new structural lever (e.g. the in-memory index for large repositories, upstream #1988) would be the next step up.
