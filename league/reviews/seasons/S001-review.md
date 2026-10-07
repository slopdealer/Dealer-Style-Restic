# Restic League V2 — S001 review (solo, team TA, OpenRouter stealth/space-bunny-alpha, effort max)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-09-24 ~11:50–21:08 UTC (9.3 h), 1,541 API requests, $0.00. No free-tier pauses (the key's
`free_model_daily_requests` counter never moved for this model). No controller crashes.

## Match rows (6 rounds each, vs stock; score = mean of time saved and peak-memory saved)

| Match | Score | Memory saved | Time saved | Wins | 90% interval |
|---|---:|---:|---:|---:|---|
| M001 | +4.12% | +9.18% | −1.82% | 6/6 | [2.82, 4.54] |
| M002 | +3.31% | +8.79% | −1.61% | 6/6 | [2.86, 4.02] |
| M003 | +3.64% | +8.87% | −1.69% | 6/6 | [1.56, 4.74] |
| M004 | +2.88% | +7.41% | −1.93% | 6/6 | [1.94, 3.70] |

## Promotion — PROMOTED

42-round promotion job `d5a29fd78d8743ed8d74e50e14263d90`: **score +3.24%, 41/42 wins, peak memory −8.26%, time
+2.16% (slower)**, eligible, no reasons. Incumbent is now `workspace/incumbents/S001`.

Mechanism (one hunk, `internal/archiver/file_saver.go`): after a chunk is read, copy it into an exact-sized
allocation, return the pooled read buffer immediately, and hand the copy to the asynchronous uploader (stock kept
the pooled buffer until the upload callback ran). Same family as the old league's buffer-ownership patch, in a
smaller form (hashing stays where it was). The trade: ~8% less peak memory for ~2% more wall time.

## Lessons

- The judge behaved as designed: all four match rows were consistent, each interval excluded zero, and the 42-round
  number (+3.24%) sits inside the match spread. No inversion, no row that the confirmation contradicted.
- The team did not compound: M002–M004 re-found the M001 mechanism at slightly lower scores. The mid-season review
  did not produce a second, stacked mechanism. The next brief should say what is installed and forbid re-deriving it.
- Space Bunny: fast, tool-competent, read heavily (≈1,300 read/search calls in M001), converged quickly on the memory
  path, but its search stayed narrow.
- Operator order 2026-09-25: S002 onward runs on GPT-6 Luna max with four teams (T1–T4) from this incumbent. A solo
  space-bunny S002 that had started was stopped after a few minutes and set aside (`*.superseded-solo-spacebunny-*`).
