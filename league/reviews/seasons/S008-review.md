# Restic League V2 — S008 review (four teams, GPT-6 Luna max; first fast-format season, fresh cost map)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-28 19:38 → 2026-09-29 06:14 UTC (~10.6 h including the 42-round confirmation; matches done at ~9.1 h,
vs S007's 13.3 h total). Incumbent at start: `incumbents/S006` (+18.59% vs stock). Directive
`runtime/S008-directive.md` (fresh cost map `cost-map-2`, 11-item measured menu, no assignments). New this season:
`fast_chain`, `async_matches`, `parallel_owners`, `lock_won_changes`. $0 API.

## Match rows (6 rounds each vs the S006 incumbent; score = mean of time and peak-memory saved). All 16 rows won 6/6.

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | +4.81 (t −0.30, m +9.60) | +6.87 (t +3.52, m +9.64) | **+7.96 (t +6.30, m +10.35)** | +4.63 (t −1.08, m +9.86) |
| M002 | +5.42 (t +1.34, m +9.51) | +6.35 (t +2.75, m +9.25) | **+7.16 (t +5.09, m +8.83)** | +6.77 (t +3.98, m +10.41) |
| M003 | +5.10 (t +0.30, m +10.37) | +7.88 (t +3.95, m +11.83) | +7.94 (t +5.69, m +9.72) | **+8.20 (t +6.02, m +10.78)** |
| M004 | +4.97 (t −0.53, m +10.46) | +8.56 (t +4.22, m +12.81) | **+9.12 (t +7.00, m +11.44)** | +8.81 (t +5.99, m +11.70) |

Match points: M001 T3 33 / T2 23 / T1 13 / T4 8; M002 T3 22 / T1 22 / T2 20 / T4 18; M003 T3 35 / T1 28 / T2 24 /
T4 23; M004 T4 30 / T3 29 / T2 22 / T1 19. Standings: **T3 119, T2 89, T1 82, T4 79.**

## Confirmation — PROMOTED (largest in Restic V2)

Nominee T3 M004. 42-round job `73d8c5d15ce0461b8576c3cf350371dc`: **score +8.42% vs S006, 42/42, 90% interval
[+8.16, +8.80]; time +5.92%, peak memory +11.27%.** **Vs stock: +24.98% (time +16.03%, memory +33.49%), 42/42.**
New incumbent `incumbents/S008`. The screen (+9.12) shrank only slightly.

The tree changes three files against S006, each a cost-map-2 menu item:
- `internal/archiver/buffer.go`: bounded backup read buffers (item 1): backups −20 to −27% peak memory;
- `internal/repository/repack.go`: asynchronous repack saves (item 2): forget-prune time −46%, prune memory +5.5%;
- `internal/repository/checker.go`: check memory (item 11): −9%.

## Lessons

- **A fresh measured map beat three seasons of search, again.** S005-S007 added +2.99, +1.11 and 0; one season on
  cost-map-2 added +8.42 (vs S006). All four teams found the buffer lever in M001; the leaders added the prune
  pipeline. The prototypes' stacked ceiling (+8.7) was a good predictor. Same lesson as S004 and the pylint league:
  re-map when gains flatten.
- **Locked changes worked.** Every team built upward on its own M001 tree (T4 +4.63 → +6.77 → +8.20 → +8.81;
  T3 +7.96 → +9.12), the opposite of S007's T3 rework (+2.08 → −0.75).
- **The fast format held up on its first run:** parallel owners (three draft clashes, each fixed by one correction
  turn), concurrent builders, per-team matches (fast teams ran a full match ahead of slow ones) and per-team
  mid-season reviews, with no controller errors or resumes. Matches finished at ~9.1 h vs ~11.5 h in S007; the
  remaining time is the lead-builder integration turn and the 95-110 min confirmation.
- **Big, clean gains confirm; small noisy ones do not.** +9.12 at 6/6 confirmed at +8.42 (42/42); S007's +2.08 at
  4/6 confirmed at +0.01. Row size relative to its interval is what to trust.
- S009 (started 06:14Z, `runtime/S009-directive.md`) targets what S008 left alone: restore passes and per-file
  state, read-side workers, lock-wait overlap, ls output, index representation, backup-initial CPU, and prune peak
  memory (now the one phase worse than S006).
- Restic V2 vs stock, confirmed chain: S001 ~+3 → S004 +8.57 → synthesis +15.07 → S005 +17.82 → S006 +18.59 →
  S007 (none) → **S008 +24.98**.
