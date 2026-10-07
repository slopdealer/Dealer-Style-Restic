# Restic League V2 — S018 review (four teams; GPT-6.1 Sol medium; second suite season; first with the v2 builder tools from the start)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-10-04 12:07 → 2026-10-05 02:33 UTC (14.4 h; S017 took 19.2 h). Incumbent at start: `incumbents/S017-merge`
(S017 + the hand merge). Directive `runtime/S018-directive.md` (cost-map-8, aimed at large-files). Three judge lanes,
builder limit 3300 s, role CPU pinning, v2 fast tools. One provider failure of a role (15:18 UTC, retried in place),
21 role timeout-resumes, no controller restart. $0 API.

## Match rows (6 rounds each vs S017-merge; suite time saved % = main / big-index / large-files; wins)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | **+1.85** = +1.34 / −0.18 / +4.39 (5/6) | +0.14 = −0.13 / −0.19 / +0.73 (3/6) | +0.51 = −0.02 / +0.64 / +0.91 (5/6) | +0.15 = −0.33 / +0.43 / +0.36 (3/6) |
| M002 | **+3.00** = +1.34 / +3.21 / +4.46 (6/6) | +0.33 = −0.05 / −0.24 / +1.29 (2/6) | +1.23 = −0.05 / +0.89 / +2.84 (5/6) | +0.90 = +0.80 / +0.40 / +1.51 (5/6) |
| M003 | **+3.90** = +1.82 / +4.39 / +5.50 (6/6) | +0.87 = −0.11 / −0.64 / +3.36 (2/6) | +1.06 = +0.46 / +0.89 / +1.84 (6/6) | +1.01 = +0.71 / +0.13 / +2.20 (5/6) |
| M004 | **+3.91** = +2.20 / +3.68 / +5.83 (6/6) | +2.14 = +0.53 / +0.84 / +5.07 (6/6) | +2.09 = +0.65 / +1.10 / +4.53 (6/6) | +1.09 = −0.23 / +0.72 / +2.79 (5/6) |

Standings: **T1 114, T3 54, T4 51, T2 37.** T1 held the best row in every match, by a wide margin. All sixteen rows
were positive on the suite mean. T2 is the season bottom (its strike count goes to 1). T3's and T4's M004 rows used
4.05% and 2.86% more memory on main, over the 2% guard: neither could have been promoted as built.

## Season merge — skipped again

Archive sources: T1 M004, T1 M003, T3 M004. "Fewer than two archive trees merge cleanly." The file-level merge has now
found nothing in both seasons it has run.

## Rescreen and confirmation — PROMOTED

12-round rescreens (all three nominees were T1's): M004 +3.91 → **+3.99**, M003 +3.90 → +3.76, M002 +3.00 → +2.82.
Confirmation, 42 rounds, job `821fc91a70a0476f9322adc2f797c0e7`:

| vs | suite | main | big-index | large-files | wins |
|---|---|---|---|---|---|
| S017-merge, time | **+4.00%** (90% interval +3.86..+4.13) | +1.83 (14/14) | +4.04 (14/14) | **+6.15 (14/14)** | 42/42 |
| S017-merge, memory | +13.03 | +0.78 | **+35.53** | +2.80 | |
| stock, time | **+29.46%** | +29.30 | +40.59 | +18.48 | 42/42 |
| stock, memory | +43.09 | +42.69 | +46.49 | +40.11 | |

Median seconds (candidate / incumbent / stock): main 15.79 / 16.08 / 22.31; big-index 19.35 / 20.20 / 32.57;
large-files 13.34 / 14.21 / 16.37. Median phase peak MiB: main 82 / 83 / 143; big-index **226** / 349 / 420;
large-files 76 / 78 / 127. New incumbent `incumbents/S018`.

Per phase vs S017-merge (time): large-files check-read-data **+17.6%**, backup-unchanged **+13.2%**, backup-mutated
+4.0%, backup-initial +3.8%; big-index ls-json +10.7%, backup-unchanged +6.9%, backup-mutated +5.8%, forget-prune
+3.6%, restore +3.3%; main check-read-data +5.1%, backup-unchanged +2.7%, backup-initial +2.0%. Restore on main and
large-files did not move (+0.2%).

Files changed vs `incumbents/S017-merge` (non-test `.go`): new `archiver/file_saver_hash.go`, `parent_cut_long.go`,
`parent_cut_reader.go` (large chunks hashed off the reader; repeat backups re-read at the parent's cuts);
`repository/checker_capacity.go`, `checker_cipher_reader.go`, `checker_large_header.go`, `crypto/open_reader.go`
(check on large packs); `repository/save_verify_compressed.go`, `save_verify_stream.go`,
`index_load_ordinary_stream.go`; `index/index_decode_canonical_stream.go`, `index_hex.go`, `index_reserve_slots.go`,
`index_rewrite_saved.go`, `pack_hash_owned.go`; `pack/owned_tail_entries.go`; edits in `archiver.go`, `file_saver.go`,
`checker.go`, `index.go`, `indexmap.go`, `index_decode_eager.go`, `index_parallel.go`, `master_index.go`,
`repository.go`, `filerestorer.go`. A stray `internal/repository/repository.go.orig` came along (not a Go source file).

## Lessons

- **Aiming the directive at large-files worked.** It had not moved in two steps (S017 −0.10, the merge +0.11); every
  team moved it this season and the promoted tree is +6.15% there (now +18.5% vs stock, from +13.5%).
- **Memory came with the speed.** Big-index peak memory fell from 349 to 226 MiB per phase (stock 420): the index
  decode now streams. The suite is +43% memory vs stock on every workload.
- **The fast tools shortened the season.** 14.4 h against 19.2 h, with three judge lanes and tools from the first
  match. In M001 builders ran 30 full probes against 98 in S017's M001, and 115 build checks, 115 test runs, 64 quick
  benches, 38 output checks and 24 race checks in their place.
- **One team is carrying the league.** T1 has now promoted 8 of the last 11 trees. Its S018 builders
  include one who was on the bench list after S017: line-up effects are large and the form
  table moves quickly.
- **The automatic merge is not working.** Two seasons, two skips. T2, T3 and T4 each hold a judged +1 to +2% tree
  that is not in the incumbent. A hand merge at the boundary is the only route for those ideas today.
- **Two candidates were over the memory guard on main** (T3 M004, T4 M004) while scoring +2.1 and +1.1. The guard is
  checked at promotion, not at the match, so a team can spend a season on a tree that cannot be promoted.
- **Roster rules.** T4's owner stayed in his seat for S018 despite his third strike after S017 (empty bench). Fixed
  for S019 (`roster-rotation/ROTATION.md`).
