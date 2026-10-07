# Hand merge on S019 (2026-10-06)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Operator order ("We merge before s020"). The merge agent's report, saved by the coordinator. Step log with the full
bound search: `PROGRESS.md`. Diff: `merged-vs-S019.diff` (15 non-test `.go` files, 965 lines).

**Verdict: confirmed at 44 rounds.** Promotion job `014cbfda781442b8ba9fde4de80d2fe8`: suite **+1.14%** vs
`incumbents/S019`, 36/44 wins, eligible with no reasons (checked first-hand by the coordinator). Judged tree on the
box: `/opt/league/restic-merge-s019/final`. Ran 03:15 to 07:18 UTC; 2 h 33 min of it judge time. $0 API.

**Installed 2026-10-06 11:20 UTC as `incumbents/S019-merge`** (copy verified file by file against the judged tree,
1,903 files; roster `active_cbk`, `current_best_known` id `S019-merge`, a `maintenance` record; roster backup
`roster.json.bak-before-merge-20261006T112011Z`). It is the S020 incumbent.

## Official judging (judge `a770cec8`, official lanes, launcher `tools/judge_merge.py`)

| job | id | rounds | suite vs S019 | wins | main | big-index | large-files | remote |
|---|---|---|---|---|---|---|---|---|
| reference, `final` | `2fe91117a88149e7842bcc9bc44d177c` | 12 | +0.80% (int +0.46..+1.55) | 11/12 | +1.25 | +0.68 | +0.79 | +0.47 |
| reference, `final-b4` (twin, smaller budget, not promoted) | `d43482668eea49f38857b7762a1de117` | 12 | +0.98% | 10/12 | +0.36 | +0.92 | +2.36 | +0.28 |
| **promotion, `final`** | `014cbfda781442b8ba9fde4de80d2fe8` | 44 | **+1.14%** (int +1.05..+1.27) | **36/44** | +1.29 (11/11) | +1.31 (11/11) | +2.23 (11/11) | −0.25 (3/11) |

| | suite | main | big-index | large-files | remote |
|---|---|---|---|---|---|
| time vs S019 | +1.14% | +1.29% | +1.31% | +2.23% | −0.25% |
| memory saved vs S019 (guard: not below −2%) | −0.81% | −0.74% | −0.88% | −1.36% | −0.26% |
| time vs stock | +28.99% (44/44) | +31.11% | +41.96% | +24.22% | +18.68% |
| memory saved vs stock | +43.16% | +42.30% | +45.73% | +38.20% | +46.41% |
| median total s, merged / S019 / stock | 78.04 / 78.72 / 109.81 | 15.208 / 15.417 / 22.137 | 18.917 / 19.156 / 32.573 | 12.363 / 12.649 / 16.299 | 31.554 / 31.497 / 38.800 |

Correctness: 1,019/1,019 units verified against the stock oracle, compatibility tests passed, cross reader ok.

Per phase vs S019 at 44 rounds, time saved % (wins of 11): restore **+4.97 (11) / +4.60 (11) / +10.44 (11)** / −0.01
(5); ls-json **+4.56 (11) / +4.51 (11)** / −0.12 (2) / **+3.10 (11)**; every other cell is within ±0.7% except
remote check −1.05 (4) with unchanged code. Restore peak, median of 11 rounds: main +6.3 MiB, big-index +8.5,
large-files +3.4, remote +0.5.

Void jobs (stopped at warm-up by the agent after a fix to the tree, no round measured): `7ff9954d`, `bbe7284a`,
`d1a93273`, `35db28ce`.

## What went in

| # | idea | source | files |
|---|---|---|---|
| 1 | Third ordinary restore worker inside a byte budget | prototype `p-rs3b3`, rebuilt (no team had built it) | new `restorer/ordinary_extra_worker.go`, `restorer/restore_hold_budget.go`, `restic/restore_hold_budget.go` (types only), `repository/restore_window_hold.go`, `repository/restore_decoder.go`; hunks in `restorer/filerestorer.go`, `restorer/restorer.go`, `repository/repository.go`, `repository/stream_pack_coalesced.go` |
| 2a | One-blob files skip the second index lookup; shared zero-offset slice | T4 S019 M004 | new `restorer/planned_singleton.go`, `restore_offsets.go`; 3 hunks in `filerestorer.go` |
| 3 | ls: node decode batches on a helper goroutine beside the output callbacks | T4 S019 M004 | new `walker/listing_preparation.go`, `walker/listing_walk.go`, `data/tree_preparation_size.go`; 1 line in `cmd/restic/cmd_ls.go` |

**How idea 1 was finished.** The prototype's per-pack gate is a cliff (third worker limited to packs with no needed
blob above T: T = 2 MiB about +1 to +3% at 0 MiB; T = 3 MiB +6.6 / +8.1 / +12.6% at +13.4 / +18.1 / +7.0 MiB, over
main's guard). Built instead: a byte budget per window. Every ordinary worker reports what a window holds (ciphertext
range plus the largest decoded copy); the two configured workers never wait, as in the incumbent; only the additional
worker waits for room before each window (5 MiB per configured worker = 10 MiB locally). A restore decoder with as
many zstd block decoders as configured workers (the general decoder keeps one per core; that was most of main's
memory cost). The worker runs only on a backend that grants the additional load slot (local backend at its default
connection count; `-o local.connections=N` keeps its cap) and only when a core is free; on remote nothing changes.
No wait cycle: bytes are taken inside the backend `Load` callback and given back before any recovery load.

## What was left out, and why

- T4 restore dispatch look-ahead: main restore +1.86% (10/10) at +5.1 MiB, big-index +1.05% (unresolved),
  large-files −0.47%: about +0.1 suite points for memory on main, where the guard binds.
- T1's bounded worker on compressed packs: subsumed by idea 1.
- T3's decode output carried across windows (and T2 S018's): the peak went up (+15.6 / +20.1 / +7.8 MiB).
- T3 S018 zero-write skip after fallocate (−0.064 s on large-files restore): not ported (time). Still open.
- T3 S018 cold-parent reader: overlaps the incumbent's exact-cut scanner. T2 S018 two-tile scan: already in the
  incumbent. T4 S018 prepare-ahead and range prefetch: +69 MiB on big-index.
- Prototype `p-ctl` and every team's startup-request work: the incumbent's own is equal or ahead.
- Save-verify variants, T1's ordinary chunker, T4's auxiliary hash: the incumbent's backup rows are ahead.
- T4 successor repack, T3 repack consolidation: inside noise or negative on big-index.
- T4 index decoder recycler / cipher preparation: not looked at (time).

## Finding: binary layout moves the key derivation by 3%

`scrypt.salsaXOR` sits at 0x81f160 in S018 and S019 (start = 32 mod 64). Seven restic packages are placed before it
in the binary: `internal/errors`, `debug`, `feature`, `backend`, `backend/sema`, `repository/hashing`,
`internal/restic`. Text added to those shifts it in steps of 32 bytes; an odd number of steps puts it at 0 mod 64 and
the key derivation runs 3% slower: 6 ms per process, 28 processes per pass, about 0.17 s per workload = about 0.9
suite points, unrelated to what the code does. Evidence: the agent's first trees read ls-json on large-files −1.33%
to −1.53% with no ls code involved; S019 against a copy of itself +0.03%; the stage timeline puts the gap in
`open:backend-opened -> open:key-done` (0.208 vs 0.202 s). Fix in the merged tree: no function bodies added to those
packages; salsaXOR at 0x81f160, delta 0. **S019 was affected:** T1 M004 has salsaXOR at 0x81ffc0 (slow) and was
judged about 6 ms per process behind its own code; T3 M004, T4 M004, T2 M003, S017-merge and stock are on the fast
layout. Tools: `tools/nm.sh TREE NAME`, `tools/nmdiff.py`. The 11-second probe: `quick_bench` ls-json on large-files,
10 reps: −1.5% means the slow layout.

## Before installing

- The tree is a copy of `incumbents/S019` plus 10 new non-test files, 5 edited ones, 7 new own test files, and one
  adapted incumbent own test (`restorer/bounded_restore_test.go`). It carries S019's evidence folders and its
  `repository.go.orig`.
- Memory is now the limit on restore: main −0.74%, large-files −1.36% of the 2% guard.
- Remote reads −0.25% (3/11) although its only changed phase, ls, is +3.10% (11/11): check −1.05% (4/11),
  backup-mutated −0.68% (2/11) with unchanged code and equal requests. Inside the 1% floor. Research-lane repeats read
  null. Not attributed (remote check is two-valued; see cost-map-10).
- big-index check peak reads +12.7 MiB with unchanged check code; the 12-round job read the opposite sign.
- `final-b4` (8 MiB budget) is not the judged tree; delete it with `steps/`, `nm/`, `work/` (3.1 GiB).

## New closed ground (restore, measured on S019)

A per-pack blob-size gate for a third worker; one budget on which every worker waits (8 MiB: −11 / −14 / −22%);
dropping window buffers above 2 MiB after each window; decode output carried across windows or pack streams (+2 to +8
MiB); zstd low-memory mode on the restore decoder; an additional worker on remote (+0.3% for +5 to +8 MiB).

## Rules check, tests

Only non-test `*.go` under `cmd/` and `internal/` changed, plus own tests; `go.mod`/`go.sum` untouched; no name or
signature used by the stock tests changed; no integrity check touched; output, order and exit codes unchanged; list
order and the lock wait untouched; no busy-waiting. Stock tests on all 53 packages: 1,683 pass. Own tests: 1,151 pass.
Oracle: all 7 phases on all 4 workloads pass. `race_check --full`: TestRestore 0/200 with 4 parallel copies;
`restore latest` 0/100 and `ls latest` 0/100 during 127 concurrent stock backups.

## Files

Local `merge-S019\`: `PROGRESS.md`, `merged-vs-S019.diff`, `tools\`, `src\`, `src-s019\`,
`diffs\`, `fetch\`. Evidence `evidence\restic-merge-s019-20261006\`. Box
`/opt/league/restic-merge-s019/`: `final` (the judged tree), `final-b4`, `steps/*`, `out/*.json`, `nm/bin/*`.
