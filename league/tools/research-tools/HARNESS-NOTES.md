> PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.

# Research-tools harness: notes for the controller side (2026-10-04)

Program: `/opt/league/restic-league-v2/research-tools/research_tools.py` (source of truth:
`LEAGUES_HOME\restic-v2\research-tools\research_tools.py`). One file, standard library only. It imports the
deployed judge read-only (`judge/restic_judge_fastpath.py`) for the sandbox, toolchain, build input rule, phase
definitions and the `rusage-launch` launcher. It writes nothing into the judge, the judge cache, season workspaces
or the controller files.

**Status: all seven ops verified on real trees** (candidate `incumbents/S016`, incumbent `incumbents/S016-racefix`,
stock `restic-league-v2/stock`), on CPUs 2-5 under the lane lock. Per-run log: `PROGRESS.md`.

## Answers to the controller's three assumptions

1. **Lane lock: confirmed.** The harness does NOT take `shared/cpus-<lane>.lock`. The caller holds it. `--cpus` takes
   ONE lane (`6-9`, or a list such as `2,3,4,5`); judge-style alternatives (`6-9/12-15`) are not accepted. The harness
   pins itself and every child to those CPUs with `sched_setaffinity` and sets `GOMAXPROCS` to the lane width.
2. **Scratch: confirmed.** If `RESEARCH_TOOLS_SCRATCH` is set, the private scratch dir is created inside it
   (`rt-<team>-<op>-<pid>-<hex>`); it must be tmpfs (the harness checks, and stops with a harness error if not).
   Without the variable it uses `/dev/shm/restic-research-tools/`. The dir is removed on exit, also on SIGTERM
   (verified with `systemctl stop`: result file written with `error_kind: "interrupted"`, scratch gone) and on
   errors. A SIGKILLed run leaves its dir behind: the unit's tmpfs goes away with the unit; in `/dev/shm` the next
   invocation removes dirs whose pid is dead. `RESTIC_JUDGE_SCRATCH` is ignored.
   The unit caps you listed are enough (measured needs below).
3. **CLI: as in the contract.** Trees are passed as live paths; the harness snapshots each tree into its scratch
   first (the judge's `build_input` + `materialize`, with a retry if a file changes mid-copy) and never reads the live
   tree again. `bench` returns `result["phases"]` as `{phase: row}`. `baseline` returns `result["card"]` (markdown).

## Command lines that worked

```
R=/opt/league/restic-league-v2; I=$R/league/space-bunny/restic-v2/workspace/incumbents
RT="python3 $R/research-tools/research_tools.py"; L="flock -x $R/shared/cpus-2-5.lock"
C="--candidate $I/S016 --incumbent $I/S016-racefix --stock $R/stock --cpus 2-5 --team DEV --workload main"

$L $RT build    $C --out ~/rt-dev/build.json
$L $RT test     $C --out ~/rt-dev/test.json  --packages internal/restorer
$L $RT test     $C --out ~/rt-dev/test2.json --packages cmd/restic --run '^TestRestore' --count 3 --parallel-copies 4
$L $RT bench    $C --out ~/rt-dev/bench.json --phases restore,ls-json --reps 5
$L $RT profile  $C --out ~/rt-dev/prof.json  --phase restore --top 15
$L $RT sweep    $C --out ~/rt-dev/sweep.json --file internal/restorer/restorer.go \
                   --pattern 'restoreMetadataPendingMax\s+= (\d+)' --values 8,64,256 --phases restore --reps 3
$L $RT race     $C --out ~/rt-dev/race.json  --runs 200
$L $RT baseline --incumbent $I/S016-racefix --cpus 2-5 --team DEV --workload main \
                   --out ~/rt-dev/base.json --out-dir ~/rt-dev/baseline
```

My dev runs used `systemd-run --collect --slice=rtdev.slice -p AllowedCPUs=2-5 -p MemoryMax=14G flock -x ... python3 ...`.
Five invocations at once (build, test, bench, profile, race; three team names) under one lock hold: see `PROGRESS.md`.

## Required environment

- root on the box (bwrap sandboxes, the judge's launcher, `strace` for `profile`/`baseline`).
- No environment variable is required. Optional: `RESEARCH_TOOLS_SCRATCH` (tmpfs dir), `RESTIC_LEAGUE_ROOT`.
- `--stock` defaults to `$R/stock`. `--workload` defaults to `main` and accepts only `main|big-index|large-files`
  (argparse rejects anything else: exit 2, no result file in that one case).
- `baseline` needs no `--candidate` (the incumbent is used). `--out-dir` defaults to the directory of `--out`.
- The judge's prepared repository states must exist for the workload (`/dev/shm/restic-league-v2-cache/...bundle-*`;
  the judge builds them on its first research job). `bench`, `profile`, `sweep`, `baseline` take a SHARED flock on
  the bundle's judge lock while they run, so the judge cannot evict it under them; they read it and only ever write
  to private copies. With no bundle the op ends as a harness error with a plain message (exit 1). The harness never
  builds a bundle itself.
- Persistent state it creates under `research-tools/`: `gocache/<team>/` (Go build cache, ~0.9-1.1 GB per team, Go
  trims it itself), `bincache/` (built binaries keyed by build input + tags, newest 80 kept, ~15 MB each; this makes
  incumbent and stock builds free after the first use, for every team), `null/` (noise bands).

## Exit codes and the result file

- **Exit 0:** the harness did its job, `ok: true`. This includes a candidate that does not build, fails tests,
  crashes in a phase or fails the race check: read the op fields and `summary`.
- **Exit 2:** a bad argument a builder can fix (unknown phase, package not in the tree, pattern matching 0 or 2+
  times, ...). Result: `ok: false`, `error`, `error_kind: "bad-argument"`, `summary` = "Bad argument: ...". Pass
  `summary` back to the builder.
- **Exit 1:** harness fault. `ok: false`, `error`, `error_kind: "harness"` or `"interrupted"`, `traceback`.
- `--out` is written atomically (temp file + rename) with the common fields `ok, op, seconds, workload,
  candidate_sha256, summary, diagnostic_only` plus `harness_version, cpus, team`.
- Side file: `<out>.status.json` (live status: stage, units done/total, timestamps, pid), rewritten atomically after
  every finished unit; the same lines go to stdout. The last stdout line is `{"ok", "op", "seconds", "summary"}`.
- `candidate_sha256` is the judge's source identity of the tree (`canonical_sha(tree_files(...))`, judge's excludes).

## Wall times and memory (CPUs 2-5, schedutil governor; research lanes run the performance governor: faster, quieter)

| op | measured wall | scratch tmpfs peak | result fields beyond the common ones |
|---|---|---|---|
| `build` | 48 s first build of a team (empty Go cache); **1.3 s warm**; 0.4 s for a syntax error | ~35 MB | `build_ok`, `vet_ok` (null when the build failed), `errors` (first 40 lines), `build_seconds`, `vet_seconds`, `vet_packages`, `changed_files_vs_incumbent`, `policy_violations` |
| `test` | **2.6 s** `internal/restorer` (58 tests); 4.1 s `cmd/restic --run '^TestRestore' --count 3 --parallel-copies 4`; 1.8 s with a failing test | ~35 MB + test temp files | `passed` (count), `failed` (names), `failed_counts`, `skipped`, `tolerated_stock_failures`, `build_failed`, `timed_out`, `output_tail`, `packages`, `run`, `count`, `parallel_copies` |
| `bench` | **84 s** `restore,ls-json` x5 on main (restore 66 s, ls-json 17 s). big-index `forget-prune` x5: ~100 s; `check-read-data` x5: ~45 s. First use of a phase adds its null: 16 more runs (see below) | ~3.5 GB (repo copy 1.3-1.6 GB + restore output 1.8 GB) | `phases: {phase: {time_saved_pct, rss_saved_pct, wins, reps, noise_band_pct, rss_noise_band_pct, verdict, cand_median_s, inc_median_s, cand_median_rss_mib, inc_median_rss_mib, null, pairs, seconds}}`, `reps`, `governor`, `identical_build_input` |
| `profile` | **73 s** `restore` on main; 24 s `backup-mutated` on large-files | ~3.5 GB | `cpu_top`, `cpu_top_movers`, `cpu_top_restic_cumulative`, `cpu_total_s`, `heap_at_peak`, `gc`, `syscalls`, `syscalls_total`, `cores_used`, `wall_s`, `process_cpu_s`, `rss_mib`, `note` |
| `sweep` | **131 s** for 3 values x `restore` x 3 reps, two rounds | ~3.5 GB | `values` (rows: `value, time_saved_pct, rss_saved_pct, wins, reps, noise_band_pct, verdict, per_phase_time_saved_pct, dropped_after_round_1`), `recommended`, `note`, `current_value`, `file`, `pattern`, `phases`, `reps_per_round` |
| `race` | **130-133 s** for `--runs 200` (TestRestore part 21 s, concurrent part 105 s) | < 200 MB | `testrestore_failures`, `testrestore_runs`, `concurrent_restore_failures`, `concurrent_ls_failures`, `concurrent_runs`, `concurrent_stock_backups`, `examples` |
| `baseline` | **487 s** on main (7 phases; includes measuring 5 phase nulls, the other 2 were cached). Without nulls ~4.5 min | ~3.5 GB | `card` (markdown), `card_path`, `phases` (per phase: `seconds, rss_mib, cores, runs, cpu_top, cpu_top_restic_cumulative, heap_at_peak, gc, syscalls, syscalls_total, noise_band_pct_at_5_reps`), `total_seconds_all_phases`; files `baseline-<workload>.md` and `.json` in `--out-dir` |

Memory: the scratch tmpfs is the main consumer; restic itself peaks at 0.1-0.5 GB, a cold Go build at ~1.5 GB.
The caps you set (6G/8G/14G, tmpfs 2G/4G/10G; 300/600/900/600/1200/900/1800 s) fit every op measured.
`test` enforces its own limits inside the 600 s cap: `go test -timeout 5m` (a hung test is reported as a failure
with its goroutine dump) and a hard kill after 420 s (`timed_out: true`).

## What a builder sees (real `summary` lines)

- build: `Build OK in 0.9 s; go vet (with the stock tests overlaid) on 2 changed package(s): clean.`
- build, broken: `The candidate does not build: # github.com/restic/restic/internal/restorer | internal/restorer/restorer.go:1199:14: syntax error: unexpected {, expected )`
- test: `All 58 stock tests passed (internal/restorer).`
- test, failing: `1 test(s) FAILED (internal/restic, internal/restorer): internal/restic::TestFind; 81 passed.`
- bench: `restore: +1.92% time, -2.19% memory, 3/5 wins, band +/-2.66% -> UNRESOLVED; ls-json: -1.34% time, -1.39% memory, 1/5 wins, band +/-2.66% -> UNRESOLVED (candidate vs incumbent, 5 interleaved pairs; diagnostic, the judge alone scores).`
- bench, crashing candidate: `ls-json: CANDIDATE FAILED (exit 1): ... Fatal: open /work/repo/keys/x263a...: no such file or directory`
- profile: `restore: CPU 9.783 s vs incumbent 9.988 s on 2.05 vs 2.05 cores; biggest cost internal/runtime/syscall/linux.Syscall6 (2.89 s vs 3.02 s); biggest change ... (-0.130 s); GC 120 vs 119 cycles; syscalls 1043164 vs 1045941; heap at peak 47.79 vs 59.46 MiB.`
- sweep: `internal/restorer/restorer.go: recommended 64 (the smallest value within noise of the best; current 64). 8: -4.81% UNRESOLVED; 64: +0.89% UNRESOLVED; 256: +1.39% UNRESOLVED (vs incumbent on restore).`
- race, S016 (pre-fix): `RACE FAILURES: TestRestore 3/200 (4 parallel copies), restore latest 31/100 and ls latest 33/100 during stock backups; first: TestRestore: --- FAIL: TestRestore (0.12s) | cmd_backup_integration_test.go:48: Error while backing up: tried listing type snapshots after index`
- race, S016-racefix: `No race failure: TestRestore 0/200 with 4 parallel copies; restore latest 0/100 and ls latest 0/100 during 129 concurrent stock backups.`
- baseline: `Baseline card for main: 20.66 s over 7 phases; slowest backup-initial (5.25 s, 3.36 cores); card at .../baseline-main.md.`
- bad argument (exit 2): `Bad argument: unknown or repeated phase ['nonsense']; the judge's phases are backup-initial,...`

## Deviations from the contract and details the controller should know

- **`build` vets against the incumbent, not stock.** "Changed packages" = packages holding a promotable file that
  differs from `--incumbent`. With no difference nothing is vetted and `vet_ok` is true. `go vet` runs with the
  stock tests overlaid, so it also catches a stock test that no longer compiles against the candidate.
- **`test`:** `passed` is a COUNT (tests that passed in every run); `failed` is a list of names
  (`internal/restorer::TestX`). Failures that stock itself has in this sandbox (the judge's `compat-baseline.json`)
  are not counted; they are listed in `tolerated_stock_failures`. Packages must be under `cmd/` or `internal/`.
  `--count` is clamped to 1-20, `--parallel-copies` to 1-4. Tests run in the judge's unprivileged sandbox.
- **`bench`:** a verdict needs the median beyond the band AND at least 80% of the pairs on that side; otherwise
  `UNRESOLVED`. Two more verdict strings exist: `CANDIDATE FAILED` (non-zero exit in the phase; `exit_code`,
  `stderr_tail`) and `INCUMBENT FAILED`. If the candidate does not build: `build_ok: false`, `errors`, `phases: {}`.
  One unmeasured warm-up run of the candidate precedes the pairs. There is NO correctness oracle in `bench`:
  `check_parity` stays the tool for that. `--reps` is clamped to 1-9.
- **Read-only phases share one state copy.** For `restore`, `check-read-data` and `ls-json` all runs of an
  invocation share one private copy of the prepared repository (the judge copies it per run, outside its timed
  region). After every run the copy is checked (file count + total size); a changed copy or a failed run gets a
  fresh copy. The other four phases copy the state per run as the judge does. This cut `bench restore,ls-json`
  from 113 s to 84 s.
- **Noise band:** per workload + phase + CPU governor, measured as 8 incumbent-vs-incumbent pairs (not "the same
  reps") and cached in `research-tools/null/<workload>__<phase>__<governor>.json`; re-measured when the incumbent's
  build input changes or after 14 days. `noise_band_pct` = 2 x 1.2533 x (RMS of the null pair differences) /
  sqrt(reps), floor 0.3%. On CPUs 2-5 (schedutil): pair RMS 2.1-2.4%, band +/-2.4-2.7% at 5 reps. A first bench of
  a phase pays for its null (16 extra runs: +100 s for main restore, +150 s for big-index forget-prune). `baseline`
  measures the null of every phase, so running `baseline` per workload on a performance-governor lane at season
  start makes every later `bench`/`sweep` on those lanes hit the cache. The 2-5 lane (schedutil) has its own
  entries: main is complete, big-index has forget-prune and check-read-data.
- **`baseline` takes ~8 min, not 3-6**, because it front-loads the nulls (your 1800 s cap covers it).
- **`profile`:** three instrumented runs per arm (CPU profile + GC trace; heap-at-peak sampler; `strace -c` on the
  plain binary). The profile binary is built with the extra tag `profile` and one injected file
  (`cmd/restic/zz_rtprof.go`, in the scratch copy only). `cpu_top` is sorted by the larger of the two flat times;
  `cpu_top_movers` by absolute change; `cpu_top_restic_cumulative` lists restic's own functions by cumulative time.
  `heap_at_peak` = `{owners: [{owner, cand_mib, inc_mib, delta_mib}], cand_total_mib, inc_total_mib, cand, inc}`.
  `gc`, `cores_used`, `wall_s`, `rss_mib`, `cpu_total_s` are `{cand, inc}`; `syscalls` rows are
  `{syscall, cand, inc, delta}`. One run per arm: heap owners move by several MiB between near-identical trees;
  read large differences only. `--top` is clamped to 3-60.
- **`sweep`:** `--file` must be a non-test `.go` file under `cmd/` or `internal/`; the pattern must match exactly
  once. Each round runs the incumbent once per rep and every value once (rotated order), so values share the
  incumbent runs. `time_saved_pct` is on the summed time of the named phases. Values more than one band behind the
  best after round 1 are dropped; survivors get a second round (their rows show `reps` doubled). A value that does
  not build or crashes gets `verdict: "DOES NOT BUILD"` / `"FAILED"`. `recommended` is the smallest value within
  one band of the best (numeric order when the values are numbers), or null.
- **`race`:** `--runs N` (clamped 4-1000) is the TestRestore run count (4 parallel copies, one process per run,
  as `~/race-dev/stress.sh`). The concurrent part runs `max(20, min(200, N/2))` iterations of `restore latest`
  + `ls latest` against a stock backup loop (as `repro.sh`), all inside the judge's sandbox. `--workload` is not
  used by `race`.
- **Queueing hint:** the first `build` for a team with an empty Go cache takes ~50 s; everything after is seconds.
- **Timings need an exclusive lane.** Five invocations at once on the SAME four CPUs all completed correctly
  (no shared scratch, no cache conflicts), but that run's bench read "+21.77% gain beyond noise" on two trees of
  equal speed. `bench`, `sweep`, `profile` and `baseline` numbers mean something only while the caller holds the
  lane lock for that one invocation. `build`, `test` and `race` do not care.
- **CPUs 2-5 run the schedutil governor** (the research lanes run performance). Functionally identical; phases are
  ~1.4x slower there and the noise band is wider. The null cache is keyed by governor, so the two never mix.

## v2 harness (2026-10-04): `research_tools_v2.py`

Deployed beside v1 at `/opt/league/restic-league-v2/research-tools/research_tools_v2.py` (source of truth
`LEAGUES_HOME\restic-v2\research-tools\research_tools_v2.py`, `HARNESS_VERSION = "rt-2"`). The v1 file is
unchanged (sha256 `7e121b2f...`). Everything in the sections above still holds for v2 unless stated here. All v2
dev runs: CPUs 2-5 under `flock -x shared/cpus-2-5.lock`, `rtdev.slice`, results in `~/rt2-dev/` on the box.

**Command lines that worked** (`C="--incumbent $I/S016-racefix --team DEV --workload main"`, `T=.../workspace/teams`,
`B=.../evidence/seasons/space-bunny/restic-v2/S017/continuity`):

```
RT2="python3 $R/research-tools/research_tools_v2.py"
$L $RT2 bench    --candidate $T/T1/S017/M004/experiments-Bnn $C --phases restore,forget-prune
$L $RT2 bench    --candidate $T/T1/S017/M004/experiments-Bnn --incumbent $I/S016-racefix --team DEV --workloads main,large-files --phases ls-json,restore
$L $RT2 bench    --candidate $T/T3/S017/M004/experiments-Bnn $C --base $B/T3/M003-4 --versus base --phases restore
$L $RT2 test     --candidate $T/T3/S017/M004/experiments-Bnn $C --packages ./internal/repository/index --run M004 --own-tests --verbose
$L $RT2 race     --candidate $I/S016 $C                      # quick; --full for 200 runs
$L $RT2 oracle   --candidate $I/S016 $C --phases restore,ls-json,backup-unchanged,forget-prune,check-read-data
$L $RT2 ablate   --candidate $T/T3/S017/M004/experiments-Bnn $C --base $B/T3/M003-4 --phases restore
$L $RT2 timeline --candidate $T/T1/S017/M004/experiments-Bnn $C --phase restore
$L $RT2 gobench  --candidate $I/S016 $C --packages ./internal/repository/crypto --bench 'BenchmarkEncrypt|BenchmarkDecrypt' --benchtime 200x --count 2
$L $RT2 baseline --incumbent $I/S016-racefix --team DEV --workload main --phases ls-json --reps 1
```

**Real `summary` lines (v2):**

- bench, a clear gain: `restore: +10.04% time, -3.15% memory, 3/3 wins, band +/-5.37% -> gain beyond noise; forget-prune: +1.95% time, +1.19% memory, 3/5 wins, band +/-8.57% -> UNRESOLVED. Worth on the main score: +2.27 points from the resolved phases (+2.41 if every reading is taken at face value; phases not benched count as 0): restore +10.04% x 23% of the time = +2.27; forget-prune +1.95% x 7% of the time = +0.14 (candidate vs incumbent; paired, each phase stops when its verdict is clear: 8 pairs in all, limit 9 per phase; schedutil governor; diagnostic, the judge alone scores).`
- bench, two workloads: `[main] ls-json: ... UNRESOLVED; restore: +13.96% time, ... 3/3 wins, band +/-1.97% -> gain beyond noise. Worth on the main score: +3.23 points ... | [large-files] ls-json: ... UNRESOLVED; restore: -1.85% ... UNRESOLVED. Worth on the large-files score: +0.00 points ...`
- bench versus base: `restore: -2.85% time, -1.30% memory, 1/6 wins, band +/-2.24% -> loss beyond noise. ... (candidate vs base (the source this match started from); ...)`
- test, nothing matched: `No test matched (internal/restorer -run TestDoesNotExistXYZ); nothing failed to compile. run_tests runs the STOCK tests of the named packages; your own new *_test.go files are compiled in only with own_tests=true. Check the -run regex and the package list.`
- test, own tests: `All 1 tests passed (stock tests + your 62 own test file(s); internal/repository/index -run M004).`
- race, quick: `No race failure: TestRestore 0/40 with 4 parallel copies; restore latest 0/20 and ls latest 0/20 during 26 concurrent stock backups. Quick check; full=true runs 200 before a handoff that touched locking.`
- race, quick, on S016 (pre-fix): `RACE FAILURES: TestRestore 0/40 (4 parallel copies), restore latest 7/20 and ls latest 6/20 during stock backups; first: ...`
- oracle: `Output oracle PASSED for the named phases. restore: OK (restore: the restored tree: 91264 entries equal stock's (content, modes, owners, file and directory mtimes, xattrs)); ls-json: OK (ls --json: 91265 parsed lines equal stock's) (the judge's per-phase oracle against stock; check_parity still runs the whole oracle, the cross-reader and the compatibility tests before a handoff).`
- ablate: `restore vs base (the source this match started from): all changes +0.95% time, -2.67% memory (UNRESOLVED); internal/restorer/blob_mapping_release.go: does not build without it; internal/restorer/filerestorer.go carries +0.34% time, -0.36% memory (UNRESOLVED) (...)`
- ablate, 17 changed files against the incumbent: `restore vs incumbent: all changes +7.20% time, +1.95% memory (gain beyond noise); internal/backend/sema/ (1 files) carries +7.17% ... (gain beyond noise); internal/data/ (5 files): does not build without it; internal/repository/index/ (3 files) carries +2.99% ...; internal/restorer/ (5 files) carries +7.85% ... Note: 17 changed files were grouped by package directory; name groups to split a package.`
- timeline: `restore: 4.162 s wall, 9.38 s CPU vs incumbent 4.75 s, 9.772 s on 4 cores; longest stage 'restore:files-begin -> restore:files-end' 2.256 s at 2.54 cores (incumbent 2.859 s); biggest change 'restore:files-begin -> restore:files-end' -0.603 s; most idle core time in 'restore:files-begin -> restore:files-end' (3.302 core-s).`
- gobench: `2 benchmark(s) on both trees (internal/repository/crypto -bench BenchmarkEncrypt|BenchmarkDecrypt -benchtime 200x x2), candidate vs incumbent, positive = candidate faster: BenchmarkEncrypt 8513009 vs 7708644 ns/op (-10.4%); BenchmarkDecrypt 3289193 vs 3329303 ns/op (+1.2%). Micro-benchmarks say where a function moved, not what the suite score does.` (identical code in both trees: this is the 200x noise; the row carries `spread_pct` and the line now says "inside the run-to-run spread")

**Details the controller should know (v2):**

- **Sequential bench.** Constants `SEQ_MIN_PAIRS=3`, `SEQ_TARGET_PAIRS=5`, `SEQ_MAX_PAIRS=9` (default limit),
  `SEQ_HARD_MAX=15`, `SEQ_EARLY_MARGIN=1.5`, `SEQ_CONTINUE_RATIO=0.7`. `--reps N` is the limit; `--fixed-reps` runs
  exactly N pairs. The rule is `seq_decision()`; `judge_pairs()` gives the verdict and its `reason`. The pair order
  alternates per pair. `units_total` in the status file is an estimate (target pairs), so `units_done` can pass it.
- **No null inside a call.** `bench`, `sweep` and `ablate` only READ nulls (`read_null`: `null2/`, then v1's `null/`
  re-read robustly, any incumbent identity, 14-day age limit). Only `baseline` measures (`measure_null`, 8 pairs).
  With no null on record the band comes from the call's own pairs and `null.origin` says so.
- **Several workloads.** `--workloads` walks them in order; the shared read-only state copy of the previous
  workload is dropped first, so the scratch peak stays that of one workload. The top-level `phases` of a
  multi-workload result is keyed `workload/phase`, so a caller that scans `phases` for UNRESOLVED still works;
  `workloads[name]` holds the per-workload rows and worth line.
- **`--versus base` / `--base`.** The reference arm is named `base` in the scratch and in `versus`. `ablate` uses
  `--base` when given unless `--versus incumbent`.
- **`test --own-tests`** copies only test files whose path stock does not have, into the scratch build input of the
  candidate; a candidate cannot replace a stock test. A tree that carries old own tests which no longer compile
  fails as a whole package: the summary then names the compiler error.
- **`oracle`** needs the stock binary (cached) and the prepared states. Stock's digests go to
  `research-tools/oracle-ref/<bundle>__<phase>[__deep].json`; a manifest mismatch reruns stock to print a diff of
  the first 5 differing entries. It is the judge's per-phase oracle, not the whole correctness mode (no
  cross-reader, no compatibility tests): `check_parity` stays the gate.
- **`ablate`** shares the candidate's scratch source: each variant is built with the group's files rewritten to the
  base's bytes and restored afterwards. Arms run in rotated rounds (`paired_runs`), `--reps` rounds, no sequential
  stopping. Runs = (groups + 2) x reps x phases.
- **`timeline`** marks: `MARK_ANCHORS` (soft; from the cost-map census patch) in `cmd/restic/main.go`, `lock.go`,
  `cmd_backup.go`, `cmd_restore.go`, `cmd_check.go`, `cmd_prune.go`, `cmd_ls.go`, `internal/global/global.go`,
  `internal/repository/repository.go`, `internal/restorer/restorer.go`. The marks package `internal/rtmarks` exists
  in the scratch copy only and appends to `/work/rtmarks.tsv`. If the marked build fails the op falls back to the
  plain binary (one stage) and returns `marks_build_error`. Stages shorter than 2 ms in both arms are left out.
- **`gobench`** runs the candidate first, then the reference, each with `go test -timeout 2m` and a hard kill at
  150 s. `BenchmarkMasterIndex*` in `internal/repository/index` have minutes of setup: such a regex returns the
  "did not finish" summary; name one benchmark exactly.
- **New persistent state** under `research-tools/`: `null2/`, `oracle-ref/`.
- **Not exercised on the box:** timed v2 jobs on the four research lanes through the broker (those lanes belong to
  S017 until the boundary). The harness ops themselves all ran on CPUs 2-5; the broker's v2 lane policy is covered
  by unit tests and by an end-to-end run of the untimed path (`~/rt2-dev/e2e.log`).

## Box paths

- Harness: `/opt/league/restic-league-v2/research-tools/research_tools.py`
- Caches: `research-tools/gocache/<team>/`, `research-tools/bincache/`, `research-tools/null/`
- Dev evidence (results, logs, baseline card): `~/rt-dev/` (`*.json`, `*.log`, `baseline/baseline-main.md`)

## Workload `remote` (2026-10-04, staged for S019)

`research_tools_v2.py` (local sha256 `998612d1...`, staged on the box as `research_tools_v2_remote.py`; the live
file is untouched until the boundary) also takes `--workload remote`: the workload's REST server is started and
stopped around every restic process, and bench / timeline / profile / ablate / baseline report requests and MiB per
phase and arm. Interface: CONTRACT.md, section "Workload `remote`"; measurements and the switch: `judge/REMOTE.md`
sections 9 and 11. Dev runs used `RESEARCH_TOOLS_JUDGE` (scratch judge file) and `RESEARCH_TOOLS_STATE` (scratch state
dir); command lines: `judge/remote-workload/tools/sanity_run.sh`, `sanity_run3.sh`.
