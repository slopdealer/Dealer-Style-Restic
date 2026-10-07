# Restic League V2 — S003 review (four teams T1–T4, GPT-6 Luna max via Codex CLI)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-25 20:10 → 2026-09-26 09:20 UTC (~13 h, incl. ~1 h lost to a Codex auth outage). First season with
parallel official judging (3 lanes), 50-minute builders, the research fast path (correctness ~3x faster) and
`unchanged_scores_zero`. Incumbent: S001's tree. $0 API (subscription).

## Match rows (6 rounds each vs the S001 incumbent; score = mean of time and peak-memory saved)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | −0.12 (3/6) | +0.47 (m +1.06, 4/6) | −1.02 (m −2.44, 1/6) | −0.33 (2/6) |
| M002 | **+1.47 (m +2.19, t +0.12, 5/6, int [0.57, 2.50])** | −0.30 (3/6) | +0.37 (3/6) | +1.09 (m +1.90, 4/6) |
| M003 | −0.77 (m −1.90, 2/6) | **+0.52 (m +1.09, 5/6, int [0.11, 1.20])** | +0.17 (3/6) | −0.15 (2/6) |
| M004 | skipped (= incumbent) | −0.04 (3/6) | **+0.46 (m +1.34, 5/6, int [0.14, 1.57])** | +0.72 (t +0.89, 3/6) |

Standings: T2 11, T4 11, T3 9, T1 8. Three rows had 90% intervals clear of zero — the first in any Luna season — all
memory-side.

## Confirmation — NOT PROMOTED (narrowly)

Nominee T1's M002 tree (+1.47 screened). 42-round job `f3d980872121426d805307fd1580dc89`: **score +0.49%, 27/42 wins
(29 required), time +0.58%, peak memory +0.71%**; vs stock +3.64%. Positive on both axes and above the 0.25% score
floor, but two wins short. The screen overstated it ~3x (+1.47 → +0.49), the same shrink pattern as S002.

## Operational

- Codex auth outage ~22:50–23:45 UTC: 57 role runs got 401 (ChatGPT token expiry on the box; auth.json refreshed
  23:25 UTC). Controller died 3x, chainer resumed 3x, no work lost beyond the failed roles; resume counter reset.
- Parallel judging worked: the season ran ~12 h of work vs S002's 18 h.
- One skipped submission (T1 M004) despite zero points for unchanged code.

## Lessons → S004

- Three seasons of unaimed search found only small memory-side gains in the archiver area. The cost map
  (`cost-map/COST-MAP.md`) shows ~20% of the workload is per-process startup (scrypt KDF 0.47 s/phase serial,
  200 ms lock sleep, index load) plus ls-json output and 2-of-4-core read paths — none touched by S001–S003.
- S004 runs with the directive `runtime/S004-directive.md`: four disjoint territories (startup/output; restore-check-
  prune read side; restore metadata/tree walk; backup write path), 24-mechanism closed list, operator rulings
  (lock wait overlap-only; incompressible blobs may be stored uncompressed).
- Screens shrink roughly 2-3x at 42 rounds here; nominate only rows whose interval clears zero comfortably.
