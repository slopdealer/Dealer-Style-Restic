# Restic League V2 — S019 review (four teams; GPT-6.1 Sol medium; first season on four workloads, with the remote workload and the roster rotation)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-10-05 06:19 → 2026-10-06 02:37 UTC (20.3 h). Incumbent at start: `incumbents/S018`. Directive
`runtime/S019-directive.md` (cost-map-9). New this season: the fourth scored workload `remote` (main's corpus through
the REST backend over a simulated link: 30 ms per request, 48 MiB/s per connection, 5 connections); matches of 8
rounds, promotion of 44; two judge lanes; roster rotation (a new owner for T4; three builders
and a coach benched; new builders, a coach and inventor lenses I01-I12 in the draft); inventor
idea credit. Two provider failures and one controller stop in the first three hours (resumed by the chainer at 09:43
UTC, about 1.5 h lost); two later role failures were retried in place. The workstation rebooted at 10:58 UTC and the
chainer was down until 12:57 UTC; nothing failed in that window. $0 API.

## Match rows (8 rounds each vs S018; suite time saved % = main / big-index / large-files / remote; wins)

| Match | T1 | T2 | T3 (B26 new, B10, B04) | T4 (B13, B25 new, B22) |
|---|---|---|---|---|
| M001 | +2.01 = −0.23 / −0.22 / +0.26 / +8.21 (4/8) | **+2.27** = +0.03 / +0.29 / +0.20 / +8.58 (6/8) | +2.27 = +0.16 / +1.94 / −1.31 / +8.30 (4/8) | +1.65 = +0.59 / +0.07 / −0.14 / +6.07 (5/8) |
| M002 | +2.23 = −0.22 / −0.03 / −0.72 / +9.90 (3/8) | +2.48 = −0.03 / +0.02 / +0.38 / +9.56 (5/8) | +1.96 = −0.31 / −1.18 / −0.14 / +9.48 (4/8) | **+2.88** = +1.17 / +1.51 / +0.27 / +8.57 (7/8) |
| M003 | +2.23 = +0.18 / −0.49 / +0.38 / +8.86 (4/8) | **+3.27** = +0.83 / +0.86 / +1.76 / +9.63 (8/8) | +2.49 = +0.31 / −0.23 / −0.43 / +10.32 (5/8) | +2.67 = +0.96 / +1.14 / −0.09 / +8.66 (7/8) |
| M004 | +2.82 = +0.28 / −0.35 / +1.49 / +9.87 (7/8) | **+4.50** = +1.54 / +1.66 / +6.01 / +8.81 (8/8) | +2.84 = +0.50 / +0.48 / +0.91 / +9.48 (7/8) | +2.91 = +1.11 / +0.80 / −0.13 / +9.85 (6/8) |

Standings: **T2 91, T4 67, T3 55, T1 47.** T1 is the season bottom (its owner's second strike). All sixteen rows
were positive on the suite mean, but twelve of them are the remote gain and little else.

## Season merge — skipped a third time

Sources offered: T2 M004, T2 M003, T4 M004 and the three S018 leftovers. "Fewer than two archive trees merge cleanly."

## Rescreen and confirmation — PROMOTED (T2's first promotion in eighteen seasons)

12-round rescreens: T2 M004 +4.50 → **+4.24**; T4 M004 +2.91 → +3.49; T2 M003 +3.27 → +2.88. Confirmation, 44 rounds,
job `28102fc215214b378965829b01c5b68e`:

| vs | suite | main | big-index | large-files | remote | wins |
|---|---|---|---|---|---|---|
| S018, time | **+4.33%** (90% interval +4.18..+4.59) | +1.68 (11/11) | +0.89 (11/11) | +5.37 (11/11) | **+9.37 (11/11)** | 44/44 |
| S018, memory | +0.70 | −0.77 | +0.19 | +1.37 | +2.03 | |
| stock, time | **+28.15%** | +30.23 | +41.29 | +22.72 | +18.37 | 44/44 |
| stock, memory | +43.97 | +43.09 | +46.14 | +40.21 | +46.45 | |

New incumbent `incumbents/S019`. Against stock the remote workload went from +8.8% to +18.4% in one season, and
large-files from +18.5% to +22.7%.

## Lessons

- **Scoring the remote workload paid at once.** The incumbent had been issuing five times stock's requests in
  restore for several seasons without anyone seeing it. Every team removed that in the first match.
- **But every team built the same thing first.** The speed map put one lever at the top with a measured size, and
  all four teams took it in M001: twelve of sixteen rows are that lever. Three teams' seasons largely overlapped.
  Only T2 then went on to move a second workload (large-files +6%). The directive needs to spread teams out: say
  that the top lever will be built by everyone and that the season is won on what comes after it.
- **T2 won with a new line-up.** Three builders under a new coach: T2's first promotion. T1, which had
  promoted 8 of the previous 11 trees, was flat at +2.2% for three matches and finished bottom.
- **The new owner did well.** T4 under its new owner finished second and held the best row of M002, positive on all
  four workloads.
- **New roster pieces were used.** Two of the three new builders were fielded (B26 on T3, B25 on T4), T1 drafted a
  new inventor lens (I07, built), and all four M001 handoffs carried an idea-credit line.
- **Rows past the per-workload gates keep appearing** (T3 M001 large-files −1.31%, T3 M002 big-index −1.18%, T4 M004
  memory −2.1% on large-files). The gates are only checked at promotion.
- **The automatic merge has now failed three seasons in three.** The hand merge at the boundary is the working
  route; the file-level merge should be retired or rebuilt.
- **Season time is back up:** 20.3 h (S018: 14.4 h). Four workloads on two judge lanes make each match about three
  hours end to end, and the morning's provider failures cost 1.5 h.
- **The chainer does not survive a workstation reboot.** It had to be restarted by hand.
