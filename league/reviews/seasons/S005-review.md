# Restic League V2 — S005 review (four teams, GPT-6 Luna max; first season with subfactor match points)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-27 04:48 → 17:40 UTC (~13 h). Incumbent at start: the S004 synthesis tree (`incumbents/S004-synth`,
+15.07% vs stock). Directive `runtime/S005-directive.md` (S004 territories kept, installed items listed). $0 API.

## Match rows (6 rounds each vs the synthesis incumbent)

| Match | T1 startup/output | T2 read side | T3 restore metadata | T4 backup write path |
|---|---|---|---|---|
| M001 | −0.28 (3/6) | −0.77 (2/6) | +0.90 (m +1.73, 5/6) | **+3.64 (t +3.30, m +4.28, 6/6)** |
| M002 | −0.21 (2/6) | −0.23 (3/6) | +0.28 (4/6) | +2.88 (t +2.69, m +2.85, 6/6) |
| M003 | skipped (= incumbent) | +0.29 (3/6) | −0.06 (3/6) | +2.83 (t +3.24, m +2.84, 6/6) |
| M004 | skipped (= incumbent) | −0.94 (1/6) | +0.84 (4/6) | +2.53 (t +3.38, m +1.79, 6/6) |

Standings (whole-workload + subfactor points, first season scored this way): **T4 123, T3 85, T2 48, T1 16.**

## Confirmation — PROMOTED

Nominee T4 M001. 42-round job `cce6a759de3247138de39b51ef8ef012`: **score +2.99% vs the synthesis incumbent, 41/42,
time +3.49%, peak memory +3.24%; vs stock +17.82% (time +11.47%, memory +24.11%)**. New incumbent `incumbents/S005`.
It lifts the one phase that was slower than stock (backup-initial, a cost of S001's buffer copy).

## Lessons

- The assigned territories ran dry for their holders: T1 (startup) skipped two matches after S004 + synthesis
  consumed its ground; T2 (read side) found nothing new. T4's area still had headroom.
- Operator decision for S006: no assigned territories — owners get the full scope of restic and choose (directive
  `runtime/S006-directive.md`, areas A-D as a menu, overlap allowed).
- Subfactor points separated the table sharply (123/85/48/16) and rewarded T3's consistent small both-axis gains.
- Restic V2 vs stock, confirmed chain: S001 ~+3 → S004 +8.57 → S004 synthesis +15.07 → S005 +17.82.
