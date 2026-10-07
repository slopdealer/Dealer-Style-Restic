# Restic League V2 — S017 review (four teams; GPT-6.1 Sol medium; first season scored on the three-workload suite)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Ran 2026-10-03 13:14 → 2026-10-04 08:24 UTC (19.2 h, including two early role-failure resumes, one operator restart
in M004 to switch the fast builder tools on, the three 12-round rescreens and the confirmation). Incumbent at start:
`incumbents/S016-racefix`. Directive `runtime/S017-directive.md` (cost-map-7 + suite scoring). $0 API.

New this season: score = mean time saved over `main`, `big-index`, `large-files` (rounds rotate, so a 6-round match
measures each workload twice); per-workload memory guard (+2%) and per-workload time floor (−1%); noise-aware probes;
the season merge; from M004 the builders' fast research tools and three judge lanes.

## Match rows (6 rounds each vs S016-racefix; suite score = time saved %; main / big-index / large-files; memory saved % in brackets)

| Match | T1 | T2 | T3 | T4 |
|---|---|---|---|---|
| M001 | **+0.91** = +1.99 / +0.72 / +0.02 (m +0.36, 4/6) | +0.48 = +1.05 / +0.42 / −0.04 (m +0.38, 4/6) | +0.34 = +0.78 / +0.68 / −0.44 (m −0.09, 4/6) | +0.36 = +0.28 / +0.52 / +0.29 (m +0.29, 5/6) |
| M002 | **+1.88** = +2.08 / +3.68 / −0.12 (m +1.65, 5/6) | +1.76 = +1.54 / +3.34 / +0.38 (m +2.33, 5/6) | +0.68 = +0.95 / +1.17 / −0.08 (m −0.42, 5/6) | +0.39 = +0.14 / +0.93 / +0.10 (m +0.38, 5/6) |
| M003 | **+2.35** = +3.15 / +3.79 / +0.10 (m −0.68, 5/6) | +1.91 = +1.54 / +3.64 / +0.55 (m +5.04, 6/6) | +1.64 = +1.82 / +2.39 / +0.71 (m +2.34, 5/6) | +0.64 = −0.06 / +1.22 / +0.77 (m −0.52, 4/6) |
| M004 | **+2.44** = +2.75 / +4.22 / +0.33 (m −0.44, 6/6) | +1.73 = +1.70 / +3.65 / −0.16 (m +3.01, 5/6) | +1.48 = +1.16 / +3.16 / +0.10 (m +0.76, 5/6) | +0.03 = +0.45 / +0.19 / −0.56 (m +1.19, 4/6) |

Standings: **T1 77, T2 66, T3 58, T4 50.** All sixteen rows were positive on the suite mean. T1 held the best row in
every match. No row moved large-files by more than +0.8%.

## Season merge — skipped

Archive (best tree per phase): restore and check T1 M003, the three backup phases T2 (M003/M004), forget-prune T3 M003,
ls T2 M002. Sources offered to the merge: T1 M003, T2 M003, T2 M002, plus the three carried-over S016 trees (T3, T2, T4
M004). Result: "fewer than two archive trees merge cleanly". T1's and T2's trees both rewrite `index/index.go` and
`index/master_index.go`; the S016 trees overlap the restore and check code the incumbent already changed.

## Rescreen and confirmation — PROMOTED

12-round rescreens: T1 M004 +2.44 → +2.13; **T1 M003 +2.35 → +2.68**; T2 M003 +1.91 → +1.95. The rescreen reordered
the top two, so the confirmation went to T1 M003. 42-round job `582b1263aed34f9fbff64f861dbefd4a`:

| vs | suite | main | big-index | large-files | wins |
|---|---|---|---|---|---|
| S016-racefix, time | **+1.93%** (90% interval +1.79..+2.05) | +2.18 (14/14) | +3.72 (14/14) | **−0.10 (6/14)** | 34/42 |
| S016-racefix, memory | −0.33 | −1.03 | −0.74 | +0.79 | |
| stock, time | **+21.89%** | +27.61 | +25.19 | +12.87 | 42/42 |
| stock, memory | +26.71 | +42.68 | **−0.78** | +38.22 | |

Median seconds (candidate / incumbent / stock): main 16.08 / 16.45 / 22.21; big-index 24.34 / 25.24 / 32.50;
large-files 14.25 / 14.24 / 16.36. New incumbent `incumbents/S017`.

Per phase vs S016-racefix (time): restore **+10.0% main, +10.6% big-index** (large-files −0.4); check-read-data +5.7%
big-index (main −0.4); backup-mutated and backup-unchanged about +2% on big-index, +0.6% on main; forget-prune +1.8%
big-index; ls-json −1.1% on main, +1.9% on big-index.

Files changed vs `incumbents/S016-racefix` (non-test `.go`): `internal/restic/restore_load.go` (new),
`internal/restorer/filerestorer.go`, `internal/backend/sema/backend.go`, `internal/repository/repository.go` (a bounded
third restore worker for packs of small blobs); `internal/repository/checker_pack_sizes.go` (new),
`internal/repository/checker.go`, `internal/checker/checker.go`, `cmd/restic/cmd_check.go` (check pack sizes prepared
early); `internal/repository/index/{index_merge,index_check_groups,list_packs_bounded,repack_values}.go` (new) with
`index.go`, `master_index.go`; `internal/repository/{prune.go,prune_header_overlap.go,repack.go}`;
`internal/archiver/{parent_finder,parent_tree_reuse}.go` (new) with `archiver.go`, `tree_saver.go`,
`internal/data/{parent_finder.go,parent_tree.go,tree.go}` (parent tree reuse in repeat backups);
`cmd/restic/wait_index_load.go`; `internal/ui/backup/progress.go`.

## Lessons

- **The suite did what it was built for: the gain is no longer fitted to one workload, and the weak one is visible.**
  big-index gained most (+3.72), main +2.18, and large-files is flat (−0.10, 6 of 14 rounds). Against stock the three
  stand at +27.6 / +25.2 / +12.9. Large-files is the open ground; every lever that moves it (cost-map-8) is over the
  memory guard as built and needs a bound first.
- **The rescreen changed the winner.** T1 M004 led the 6-round table (+2.44) and read +2.13 at 12 rounds; T1 M003 read
  +2.35 then +2.68 and confirmed at +1.93. With each workload measured only twice per match, the top rows are within
  noise of each other. Without the rescreen the confirmation would have gone to the weaker tree.
- **The merge found nothing to merge.** Teams converge on the same files (`index.go`, `master_index.go`), so the best
  trees per phase conflict. The S018 directive now says: put a new mechanism in a new file and keep edits to shared
  files small. The carried-over S016 trees did not merge either; their changes sit in code the incumbent has since
  rewritten.
- **big-index memory is still at stock's level** (424 MiB vs stock 423, −0.78%). The prune peak is the cause
  (cost-map-8: the index rewrite's four loaders); the two prune prototypes in the S018 menu cut it by ~189 MiB.
- **Season time: 19.2 h.** M001 took five hours (two role-failure resumes, a cold research cache and 3-round probes on
  two judge lanes). Builders spent 83% of their wall time waiting on tools (`research-tools/USAGE-STUDY-S017-M004.md`).
  For S018: tools v2 (a bench 154 s → 69 s), three judge lanes, the Sol builder limit 4500 → 3300 s.
- **Roles run as root and looked around the box.** The transcript audit found builders listing the league root and
  reading judge source. No operator-only material was opened. From S018 role processes are pinned off the judge and
  research CPUs (`role_cpus`), and operator-only material is kept off the box during seasons.
- **T4 fell behind on the suite** (+0.03 in M004, large-files −0.56): its phase-cell strategy from S016 does not pay
  when each cell is averaged over three workloads.

## Boundary (2026-10-04)

- **The gain transfers.** On the operator-only held-out workload (12 rounds vs stock): S017 **+28.75%** time
  (interval +28.65..+28.90, 12/12), memory +31.3%; S016-racefix +26.59%. The step of about +2.2 points equals the
  step on main (+2.18). Restore +43.2 → +48.8, check +29.7 → +32.9.
- Leak audit of the season's role transcripts: clean. Operator-only material is now kept off the box during seasons.
- Judge redeployed with comment-only changes (`b5e4b9e1`); S018 directive (cost-map-8) attached; Sol medium builder
  limit 3300 s; S017's unpromoted trees (T1 M004, T2 M003, T3 M003) are extra merge sources for S018.
- **Hand merge (operator: "Merge first").** The unpromoted ideas (T2 M003 index decode, T1 M004 early check pass, T3
  M003 tree carry) and two speed-map prototypes (prune planning by position, index rewrite without reload) were ported
  onto S017 by hand and confirmed at 42 rounds: job `b740c9e2`, suite **+6.06%** vs S017 (33/42) = main +0.16 /
  big-index +17.90 / large-files +0.11; memory +5.20% (big-index +17.25%). **Vs stock: +27.08%** = +27.92 / +39.84 /
  +13.48; memory +32.72%. Installed as `incumbents/S017-merge`; S018 starts from it. Report `merge-S017/REPORT.md`.
