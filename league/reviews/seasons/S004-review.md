# Restic League V2 — S004 review (four teams, GPT-6 Luna max; first season with the cost-map directive)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-26 13:19 → 2026-09-27 01:56 UTC (~12.6 h). Incumbent at start: S001. Directive `runtime/S004-directive.md`
(cost map, four disjoint territories, 24-mechanism closed list, operator rulings: lock wait overlap-only; incompressible
blobs may be stored uncompressed). $0 API.

## Match rows (6 rounds each vs the S001 incumbent; score = mean of time and peak-memory saved)

| Match | T1 startup/output | T2 restore-check-prune read side | T3 restore metadata/tree walk | T4 backup write path |
|---|---|---|---|---|
| M001 | +3.30 (t +6.81, m −0.68, 6/6) | +2.13 (m +4.59, 6/6) | +1.64 (t +1.95, m +2.20, 6/6) | −1.99 (t +2.36, m −6.33, over cap) |
| M002 | +5.31 (t +9.20, m +2.29, 6/6) | **+5.14 (m +10.13, 6/6)** | +1.48 (4/6) | −0.37 |
| M003 | **+5.50 (t +9.05, m +1.94, 6/6)** | +4.08 (m +8.18, 6/6) | +1.14 (5/6) | −0.04 |
| M004 | +4.65 (t +7.91, m +0.76, 6/6) | +4.27 (m +8.39, 6/6) | +0.48 (4/6) | +1.96 (m +3.55, 5/6) |

Standings: T1 16, T2 12, T3 7, T4 4.

## Confirmation — PROMOTED (largest in Restic V2)

Nominee T1 M003. 42-round job `704f4cc7fa764a2cb8fd834739c6047a`: **score +5.25%, 42/42 wins, time +8.15%, peak memory
+2.14%** vs the S001 incumbent; **vs stock +8.57% (time +6.52%, memory +10.83%)**. Screen +5.50 → confirmed +5.25: no
shrink this time (S002/S003 screens shrank 2-3x — their intervals touched zero; this one sat at [4.62, 5.89]).
New incumbent: `workspace/incumbents/S004`.

## What changed the outcome

S001–S003 searched unaimed and circled one corner (archiver buffer ownership). The cost map showed ~20% of the workload
was per-process startup (scrypt KDF run serially on one core, fixed lock sleep, index load), 2-of-4-core read paths
with fresh per-pack buffers, and ls-json per-line writes. Assigned disjoint territories with measured ceilings, three of
four teams posted clean 6/6 rows in M001. Lesson (same as the pylint league): a measured profile map of the target beats
more search seasons.

## Next

Operator-approved synthesis before S005: merge T2's read-side memory work (M002 +5.14, memory +10.1%) and T4's
backup-write memory work (M004, memory +3.6%) — separate code from T1's startup work — onto the new incumbent,
correctness → 6-round screen → 42-round promotion. S005 then runs with subfactor match points (14 phase × axis cells).
