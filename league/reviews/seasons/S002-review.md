# Restic League V2 — S002 review (four teams T1–T4, GPT-6 Luna max via Codex CLI)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-24 22:50 → 2026-09-25 17:08 UTC (~18 h). Incumbent: S001's promoted tree (buffer copy in
`internal/archiver/file_saver.go`). $0 API (subscription).

## Match rows (6 rounds each vs the S001 incumbent; score = mean of time and peak-memory saved)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | +0.28 (t +2.74, m −2.29), 3/6 | +0.71 (t −0.22, m +1.59), 4/6 | −1.26 (t +0.59, m −2.30), 2/6 | skipped (= incumbent) |
| M002 | **+1.29 (t +3.23, m −0.17), 5/6** | +0.51 (t +0.25, m +0.20), 4/6 | −0.23 (t −0.58, m −0.97), 1/6 | −1.08 (t +0.36, m −2.22), 2/6 |
| M003 | −0.07 | −0.20 (t −0.75, m +0.07), 2/6 | skipped | +0.49 (t −0.56, m +1.02), 4/6 |
| M004 | skipped | +0.97 (t +0.24, m +2.13), 5/6 | skipped | skipped |

Every 90% interval included zero. Standings: T1 12, T2 12, T4 10, T3 9.

## Confirmation — NOT PROMOTED

Nominee T1's M002 tree (+1.29 screened). 42-round job `3db4fd585ecc457fb7613dabebbd1f8b`: **score −0.53%, 15/42 wins,
time +1.83% (faster), peak memory −2.95% (worse)**; vs stock +3.03%. Ineligible (29 wins required, score below 0.25%).
The time gain was real and held (+3.2 screened → +1.8 confirmed); the screen's near-zero memory cost did not — at 42
rounds the tree gives back ~3% of S001's memory saving. A classic screen-to-confirmation inversion: one 6-round row
with an interval touching zero is not a result. Incumbent stays S001.

## Operational findings (fixed during the season)

1. Research throughput: 12 builders queued 7–10 min probes on 2 lanes → 6 lanes (M001).
2. Official judging serial (one ~9.6 GB judge fit in boxagents.slice) → 3 official lanes in `resticofficial.slice`,
   `judge_parallel_lanes: 3`; builder limits 3000 s (applied at M004).
3. Builders spent ~90% of their clock waiting on judge research tools (repeated stock/incumbent builds, tests, reference
   states, 3-arm oracles) → research fast path in validation (correctness ~3x faster, verdicts identical); deploy
   before S003 (chainer on hold).
4. Scoring quirk: unchanged submissions ranked 0.0 above slightly negative attempts → M004 had three no-shows →
   `unchanged_scores_zero: True` from S003.

## Lessons for S003's brief

- The live lead is T1's time mechanism (+1.8% confirmed time) without its memory cost; T2's small memory gains
  (+1.6/+2.1%) are the complementary piece. A stacked tree needs both axes to hold at 42 rounds.
- Screens are 6 rounds with ~±0.6% SE on the median score; teams should treat a single row whose interval touches
  zero as a lead, and re-measure before building on it.
