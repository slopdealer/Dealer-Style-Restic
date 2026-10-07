# Restic League V2 — S010 review (four teams, GPT-6 Luna max; fast format; no new directive)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-29 18:01 → ~2026-09-30 03:30 UTC (~9.5 h including confirmation and two automatic in-controller
restarts). Incumbent at start: `incumbents/S009` (+25.62% vs stock). No S010 directive was attached, so the briefs
carried the S009 block (the controller fact block named the right incumbent). $0 API.

## Match rows (6 rounds each vs the S009 incumbent; score = mean of time and peak-memory saved)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | −0.07 (t +0.58, m +0.35, 2/6) | −0.07 (t −1.26, m +1.65, 3/6) | +0.64 (t +1.34, m −0.76, 3/6) | **+0.96 (t +0.22, m +1.07, 5/6)** |
| M002 | −0.52 (t −0.04, m −1.32, 2/6) | skipped (= incumbent) | +0.47 (t −0.89, m +0.86, 4/6) | **+0.56 (t +0.68, m +0.40, 4/6)** |
| M003 | **+0.57 (t +0.27, m +1.21, 4/6)** | −0.72 (t −0.00, m −1.89, 1/6) | +0.46 (t −0.48, m +1.53, 4/6) | +0.49 (t +0.65, m +0.25, 5/6) |
| M004 | **+0.99 (t −0.15, m +2.31, 6/6)** | −1.07 (t −0.53, m −1.00, 1/6) | +0.52 (t −0.46, m +1.75, 4/6) | +0.58 (t −0.34, m +1.15, 4/6) |

Match points: M001 T1 20 / T4 18 / T3 18 / T2 12; M002 T4 20 / T3 14 / T1 10 / T2 0; M003 T4 26 / T3 22 / T1 7 /
T2 5; M004 T3 23 / T1 21 / T4 21 / T2 7. Standings: **T4 85, T3 77, T1 58, T2 24.**

## Confirmation — PROMOTED

Nominee T1 M004. 42-round job `87861b3ef42e48039d8aa35fa0e36616`: **score +0.84% vs S009, 32/42; time +0.58%,
peak memory +1.06%.** **Vs stock: +26.88% (time +16.67%, memory +37.15%), 42/42.** New incumbent `incumbents/S010`.

## Lessons

- **Gains are down to about +1 a season**, all inside the ±1 noise band on 6 rounds; the promotion held at 32/42
  (the gate is 29). Without a directive teams re-read S009's menu, whose big items were already installed. That is
  why cost-map-3 was commissioned (2026-09-30) and S011 runs on it.
- **Two automatic restarts:** the season died twice (`Unterminated string starting at: line 1 column 181/200`),
  each time right as one team's role finished while other teams' threads were running; the controller's own retry
  resumed from checkpoints with no work lost. No traceback was recorded; `pylint_v2.py` now emits one with the
  `failed` event (deployed with the next season start) so the next occurrence names the file.
- T2 finished last with four rows at or below zero; the only team that scored consistently positive (T4) did not
  produce the confirmed tree - T1's single 6/6 row did.
- Restic V2 vs stock, confirmed chain: S004 +8.57 → +15.07 → S005 +17.82 → S006 +18.59 → S008 +24.98 →
  S009 +25.62 → **S010 +26.88**.
