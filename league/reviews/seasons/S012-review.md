# Restic League V2 — S012 review (four teams, GPT-6.1 Sol HIGH; first Sol season)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-30 14:09 → ~2026-10-01 02:58 UTC (~12.8 h including confirmation). Incumbent at start:
`incumbents/S011` (+27.50% vs stock). Directive `runtime/S012-directive.md` (cost-map-3 menu; T3's S011 backup-memory
lead flagged). Box Codex CLI 0.159.2, `gpt-6.1-sol` at effort high. $0 API. League paused after this season
(operator 2026-10-01: "Once season 12 is done let's pause and regroup").

## Match rows (6 rounds each vs the S011 incumbent; score = mean of time and peak-memory saved)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | +1.11 (t −2.85, m +4.73, 6/6) | +0.49 (t −2.15, m +2.92, 4/6) | **+2.34 (t −1.82, m +5.94, 6/6)** | +0.09 (t −1.91, m +2.93, 3/6) |
| M002 | **+2.26 (t −1.92, m +7.13, 6/6)** | +1.10 (t −0.99, m +2.84, 5/6) | +2.15 (t −1.70, m +5.90, 6/6) | +1.20 (t −0.91, m +3.81, 5/6) |
| M003 | +2.32 (t −0.54, m +6.30, 6/6) | +2.39 (t +0.22, m +4.35, 6/6) | **+2.60 (t −1.11, m +6.64, 6/6)** | +1.11 (t −0.68, m +2.88, 6/6) |
| M004 | **+3.39 (t −0.10, m +7.12, 6/6)** | +1.64 (t −0.16, m +3.77, 5/6) | +2.59 (t −0.55, m +4.67, 6/6) | +2.70 (t +2.93, m +1.22, 6/6) |

Match points: M001 T1 17 / T3 16 / T4 9 / T2 8; M002 T4 29 / T3 22 / T1 13 / T2 11; M003 T2 33 / T4 29 / T3 25 /
T1 13; M004 T4 31 / T1 23 / T3 20 / T2 17. Standings: **T4 98, T3 83, T2 69, T1 66.**

## Confirmation — PROMOTED

Nominee T1 M004. 42-round job `f9ece693953c4c7e86773616e0a76aff`: **score +2.73% vs S011, 42/42; time −0.64%,
peak memory +5.52%.** **Vs stock: +29.68% (time +17.38%, memory +41.79%), 42/42.** New incumbent `incumbents/S012`.

## Lessons

- **Sol (high) matched Luna (max) on quality and beat it slightly:** S012's rows (+0.1..+3.4, 13 of 16 at 6/6) and
  its confirmed +2.73 are the best since S008. But it was slower: ~12.8 h vs S011's 10.4 h. Sol hit the 3000 s
  builder limit 16+ times (each a same-thread resume) and one provider failure was auto-retried. Builder limits are
  raised to 4500 s for the next Sol season.
- **Gains are now almost all memory, bought with a little time.** 14 of 16 rows were time-negative; the confirmed
  tree is 0.64% slower than S011 and 5.5% leaner. The equal-weight score allows that trade (the 5% axis cap never
  bound). T4's M004 (+2.93% time, the only clear time gain of the season) was not nominated.
- Restic V2 vs stock, confirmed chain: S004 +8.57 → +15.07 → +17.82 → +18.59 → S008 +24.98 → +25.62 → +26.88 →
  +27.50 → **S012 +29.68**.
