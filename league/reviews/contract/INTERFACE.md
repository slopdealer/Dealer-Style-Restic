# Restic League V2 — judge ⇄ controller contract (written 2026-09-24; both build agents read this first)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Operator decisions (binding):
- **Objective: time and memory, equal weight.** Rank and gate on `score_pct` = mean of time saved and peak-memory
  saved (definitions below). Correctness is a hard gate. Guard: neither axis may regress by more than
  `AXIS_REGRESSION_CAP_PCT` (default 5.0) at the median, so a trade cannot buy a large loss on one axis.
- **First season: SOLO** (one team, `TA`) on OpenRouter `stealth/space-bunny-alpha`, reasoning effort `max`
  (probe 2026-09-24: $0, 1M context, 512k max output, tool calls work, `effort: max` accepted). The key allows
  **1,000 free-model requests per day**; the runner must pause on the free-tier daily limit and resume at reset.
- Later seasons: four teams on GPT-6 Luna max via the Codex CLI on the league box (operator's subscription). Keep that
  transport working (`--transport codex` / a Luna-6 preset); the box Codex CLI may need an update then.
- The benchmark must be runnable at every match boundary (screen) and after the season (confirmation), by the
  controller automatically and by hand from one CLI.

Lessons carried from the pylint league and the failed restic league (non-negotiable):
- Pinned stock, frozen workloads (generated once, hashed, never regenerated during a season), identical starting
  state for every arm (clone pristine inputs per arm per round), correctness oracle checked BEFORE any time counts.
- Paired, order-rotated rounds (the pylint `orders()` rotation), medians of per-round paired percentages, a win
  count, a null (identical-tree) run to measure noise; gates derived from measured noise, not guessed.
- Linux on the league box only, pinned cores in one CCD, fixed GOMAXPROCS, no GOGC/GOMEMLIMIT tuning in the harness
  (restic's defaults are part of what is measured), tmpfs for corpus/repos/restore targets (disk noise out; state it).
- No historical ledger mixing: every number carries its job id and binary hashes. No rubric points, no creativity
  scoring. Two-part reporting (time, memory) always, plus the composite.
- Every long job checkpoints per unit and publishes a live status file (AGENTS.md §17).

## Box layout
- Root: `/opt/league/restic-league-v2/` — `stock/` (restic source at `ba802d42b7294c98b62c16d1157ea3e80820c019`),
  `toolchain/` (go1.27.0 linux/amd64 + offline GOMODCACHE), `workloads/` (frozen fixtures + manifest + sha),
  `judge/restic_judge.py`, `supervisor-jobs/<id>/`, `inputs/`.
- Candidate / incumbent / stock trees are **restic Go module roots** (a directory containing `go.mod`).

## Judge CLI
```
python3 restic_judge.py --candidate DIR --incumbent DIR --stock DIR --mode {probe|correctness|match|reference|promotion}
                        --out RESULT.json [--status STATUS.json] [--cpus 24-27] [--labels JSON]
```
- `probe` 1 round (diagnostic), `correctness` = build + oracle + Go test subset only (no timing), `match` 6 rounds,
  `reference` 12, `promotion` 42 (constants overridable by env, final values set from the measured null).
- Build: trusted, offline, pinned toolchain, `CGO_ENABLED=0 -trimpath`, fixed tags/ldflags; `go.mod`/`go.sum` must equal
  stock's (no new dependencies) else ineligible. Record binary SHA-256 per arm.
- Promotable files: `*.go` under `cmd/` and `internal/` (tests `*_test.go` may be added/changed; they are run, not timed).

## Result JSON (mirror `tools/pylint_judge.py` output shape for every key the controller reads; add the new keys)
```
{ "schema": "restic-linux-judge-v1", "status": "complete", "mode": ..., "measurement_valid": bool,
  "rounds": N, "job_id": ..., "labels": {...},
  "arms": { "candidate"|"incumbent"|"stock": { "median_total_seconds": s, "median_peak_rss_bytes": b,
            "binary_sha256": ..., "per_phase": {...} } },
  "correctness": { "all_arms_matched_parent_stock_oracle": bool, "verified_units": n, "total_units": n,
                   "candidate_compatibility_tests_passed": bool, "cross_reader_ok": bool, ... },
  "comparisons": { "candidate_vs_incumbent": C, "candidate_vs_stock": C },
  "eligibility": { "eligible": bool, "reasons": [...] }, "rss_measurement_method": ..., ... }
C = { "score_pct": median over rounds of s_r, "score_wins": #rounds s_r > 0,
      "time_saved_pct": median over rounds of 100*(1 - T_cand,r / T_ref,r)   (T = sum of phase wall seconds),
      "rss_saved_pct":  median over rounds of 100*(1 - M_cand,r / M_ref,r)   (M = mean over phases of that phase's peak RSS),
      "runtime_wins": #rounds with time saved > 0, "rss_wins": #rounds with memory saved > 0,
      "rounds": N, "per_phase": { phase: {"time_saved_pct", "rss_saved_pct", "wins", "rounds"} },
      "interval90": [lo, hi] (bootstrap over rounds, of score) }
with s_r = (time_saved_r + rss_saved_r) / 2.
```
- Promotion gate (defaults, re-derive from the null): `score_wins >= 29/42`, `score_pct >= 0.1`, neither axis worse than
  `-AXIS_REGRESSION_CAP_PCT`, candidate beats stock on score, correctness all true, tree differs from incumbent/stock.

## Controller integration
- The controller ranks and screens on an **objective key** taken from the target adapter: `time_saved_pct` for pylint
  (unchanged behaviour), `score_pct` for restic. RSS rules come from the adapter too (pylint: its 2:1 rule; restic:
  the axis caps above). All existing pylint tests must still pass.

## Judge implementation notes (restic-judge agent, 2026-09-24) - binding amendments to the contract above
- **CPUs.** Official judging uses `--cpus 26-29` (not 24-27): CCD4 is 24-29 (one L3, SMT siblings 72-77 left idle); 24 is a
  pylint chiplet lane and 25 is a buffer. Research lanes: `6-9` (CCD1) and `12-15` (CCD2); `--cpus 6-9/12-15` takes whichever
  lane is free (per-lane flock at `shared/cpus-<lane>.lock`). `10-13` would straddle two CCDs; do not use it.
- **--out** is the result JSON path; evidence goes to `<out without .json>-evidence/` (rounds, builds, compat, events.jsonl).
- **Build input** = pinned stock tree + the arm's non-test `*.go` under `cmd/` and `internal/`. Every other file of the arm is
  ignored (listed under `policy.ignored_non_promotable_differences`). `go.mod`/`go.sum` differing from stock = build failure.
- **Compatibility subset** runs the STOCK `*_test.go` files (candidate test files are not run) for internal/{archiver,
  repository, repository/index, repository/pack, repository/hashing, restorer, checker, data, restic, walker, bloblru,
  backend/local, fs} plus the whole `cmd/restic` integration suite, as uid 1000 in a user namespace. Pass = no failure that
  stock does not also have in this sandbox, and every test that passes on stock passes. The internal APIs those stock tests
  call are therefore frozen for candidates.
- **Skip** (match mode): candidate build input identical to incumbent or stock -> `status: "skipped"`, `skip.equivalent_to`.
- **Phases** (fixed order): backup-initial, backup-unchanged, backup-mutated, restore, check-read-data, forget-prune, ls-json.
- **Result additions:** `arms.<arm>.source = {path, sha256, files}` (rule: canonical sha256 of {relpath: sha256} over regular
  files excluding .git, reference-evidence, reference-prototypes, reference-continuity, experiments, __pycache__, .agents),
  `arms.<arm>.build_input_sha256`, `arms.<arm>.binary_sha256`, `arms.<arm>.per_phase.<phase> = {median_seconds,
  median_peak_rss_bytes, round_seconds, round_peak_rss_bytes}`, `comparisons.<C>.pair_changes`, `comparisons.<C>.per_phase`,
  `correctness.build.<arm> = {ok, exit_code, stderr_tail}`, `correctness.compatibility.<arm>`, `eligibility.reasons` (+ `why`).
- **RSS method** `bwrap-as-pid-1-monitor-wait4-ru_maxrss-v1`: a tiny trusted static launcher forks bwrap, restic runs as PID 1
  of a fresh PID namespace, and wait4 returns restic's exact kernel high-water mark (the judge never forks the measured process
  itself, because exec seeds ru_maxrss with the forking process's own peak).
- **Broker:** `remote_broker.judge_restic(candidate, incumbent, mode, labels)` (official, CPUs 26-29, boxagents.slice) and
  `remote_broker.research_restic(candidate, incumbent, mode, labels)` (probe|correctness, lanes 6-9/12-15, resticresearch.slice).
- Gate constants and round counts: see `JUDGE.md` (derived from the measured null).
- **Gates in force (derived from the measured null; JUDGE.md "Gate derivation"):** match 6, reference 12, promotion 42
  rounds; promotion needs `score_wins >= 29`, `score_pct >= 0.25` (was 0.1 placeholder), both axis medians >= -5.0% vs
  incumbent AND vs stock, `candidate_vs_stock.score_pct > 0`, correctness all true, and a build input AND binary machine
  code (`code_sha256`, ELF build IDs zeroed) distinct from incumbent and stock. Match mode also skips a candidate whose
  `code_sha256` equals the incumbent's or stock's.
- **Stock pin bytes:** `stock/` is the true upstream LF tree (`git -c core.autocrlf=false archive`), source identity
  `d3db8049afd5070a73ce0240c095670ce55d767fb0467641344693f7b8b8fa07`; an earlier CRLF archive (autocrlf=true) was replaced
  on 2026-09-24 (identical machine code; only ELF build IDs differed).

## Amendment 2026-09-25 (coordinator): six research lanes
Four-team Luna seasons queued 12 builders' probes on two research lanes (7-10 min per probe), so builders spent
their role time waiting and some timed out mid-call (league_bridge broken pipes). Research lanes are now six 4-core
sets, each inside one CCD: `6-9, 12-15, 19-22, 30-33, 36-39, 42-45` (config `research_cpu_slots`, broker
`RESTIC_RESEARCH_LANES`). Official judging stays on `26-29`. Research jobs run in `resticresearch.slice`
(no slice cap, 14 GiB per job); six jobs + the official judge fit in the box's free memory (~108 GB measured).

## Amendment 2026-09-25 (coordinator, operator-approved): parallel official judging, tighter builders
Diagnosis (S002 receipts): teams already ran in parallel (team_pipeline + judge_as_ready), but official judging ran
one team at a time on one lane (~20 min x 4 per match) because the official judge lived in boxagents.slice (16 GiB)
and one judge peaks ~9.6 GB; the slowest final builder (up to 75 min) gated every match. Change: official lanes
`26-29/30-33/36-39` (CCD4/5/6, the judge takes a free lane by flock; each job still pairs all arms on one lane),
own slice `resticofficial.slice`, `judge_parallel_lanes: 3`; research lanes `6-9, 12-15, 19-22, 42-45` (CCD1/2/3/7);
Luna builder/experiment/final-builder limits 3000 s (were 7200/3600/5400); max_parallel_roles 16. Applied to S002 by
resume at the start of M004.

## Amendment 2026-09-25 (coordinator): unchanged submissions score zero
S002 M004: three of four teams resubmitted the incumbent; `match_table` ranked an unchanged submission as 0.0, above
any real attempt that measured slightly negative, so not trying was the safe play (same quirk as pylint S026).
Restic config now sets `unchanged_scores_zero: True`: an unchanged submission earns what an ineligible row earns
(0 points). Default (pylint) behaviour unchanged. Tests: tests/test_restic_target.py (30 pass). Takes effect at S003.

## Amendment 2026-09-26 (operator): subfactor match points from S005; synthesis step after S004
- `match_ranking: subfactor_points` (pylint_v2.match_table → add_subfactor_points): each phase x axis (time, peak
  memory; 7 x 2 = 14 subfactors) is ranked across teams; a team scores N+1-rank on a subfactor when its best eligible
  row improves it and that phase held in >= `subfactor_min_wins` (4) of its rounds. Added to the whole-workload
  points. Nomination/confirmation/promotion unchanged (whole-workload score). Tests: tests/test_restic_target.py.
- After S004: chainer HOLD; coordinator merges the best S004 trees (T1 startup/time, T2 read-side/memory, T3 if clean)
  onto the incumbent (3-way per file), correctness → 6-round screen → 42-round promotion; installs as incumbent if
  eligible; S005 directive written; hold released.

## Amendment 2026-09-27 (operator): owners choose their own scope from S006
"Rather than assigning areas to teams you should give the owner the full scope of restic and let them decide where
they want to look." From S006 the directive carries the full cost map and the measured areas as a MENU (Areas A-D, ex
T1-T4 territories) — no assignments, no file ownership; teams may overlap; subfactor points reward the winner of each
phase x axis cell. Draft `runtime/S006-directive.md` (section 0 gets S005's result at season end). Chainer HOLD until then.

## Amendment 2026-09-28 (operator: "Seems very lengthy"): faster seasons from S008

Seasons ran ~12 h (about 2.5-3 h per match, 85% of it chained model turns). Three config flags, all on from S008:
- `fast_chain`: no dialectic and no second inventor round. The three builders start together (lead in
  candidate-team, peers in private copies of the starting tree); the lead then integrates. Certification
  (`common_guard.required_stages`) drops the two skipped stages when the flag is on.
- `async_matches`: each team runs its own M001..M004 with no barrier. A match closes (standings, points,
  `match-complete`) when every team's row is in. Official judging is still capped at `judge_parallel_lanes`.
  The mid-season session runs per team, after that team's own M002 row.
- `parallel_owners`: owners draft at once; draft order still settles clashes via a short correction turn.
Tests: `tests/test_async_matches.py`.
- `lock_won_changes` (operator "fix", same day): when a coach starts a match from the team's own judged tree
  with a positive row, coach, inventors and builders are told its changes are locked - build on top, revert
  only with a measured probe. `match_continuity.LOCK_RULE`; tests `tests/test_lock_won_changes.py`.

## Amendment 2026-09-30 (operator): GPT-6.1 Sol from S012
Box Codex CLI updated 0.156.1 -> 0.159.2. New transport `sol61` (model `gpt-6.1-sol`, effort HIGH per operator "Make it 6.1 High", same limits as
luna6). `chain.py` takes `next_transport` from chain-state and applies it at the next season start only.

## Amendment 2026-10-01 (operator "Do so"): rescreen before confirmation; longer Sol builder limits
- `rescreen_top_n: 3`: before the single 42-round confirmation, the top 3 nominees by 6-round screen are
  re-measured in the judge's 12-round `reference` mode (parallel, official lanes) and the confirmation goes to the
  best 12-round reading (`Season.rescreen_nominees`, checkpointed in state `rescreens`; test `tests/test_rescreen.py`).
- `sol61` builder / experiment / final builder limits 3000 -> 4500 s (S012 hit the 3000 s limit 16 times).
- Both take effect at the next season start. Operator also ordered a pause after S012 (chain-state hold).

## Amendment 2026-10-01 (operator: "Return to Luna 6 Max and target speed ... scoring to match"): from S013
- Judge (`restic_judge_fastpath.py`, sha 3421f7f3, deployed via `judge/deploy_fastpath.py`): `score_pct` =
  `TIME_WEIGHT*time + (1-TIME_WEIGHT)*rss` with `RESTIC_JUDGE_TIME_WEIGHT` default **1.0** (time only; S001-S012 were
  0.5). New promotion gate `RESTIC_JUDGE_RSS_GUARD_PCT` = **2.0**: peak memory may not grow more than 2% vs the
  incumbent. Per-phase `wins` follow the same weight. Other gates unchanged (29/42, 0.25%, 5% axis cap, beat stock).
- Controller: `subfactor_axes: ['time']` (7 per-phase time cells), `rss_guard_pct: 2.0` (nomination skips rows past
  the guard), `primary_objective` text. Briefs (PROTOCOL, LEAGUE-CARD, PROBLEM-BRIEF, SME-BRIEF) rewritten.
- Model back to `luna6` (GPT-6 Luna max) via chain-state `next_transport`. Grounding: cost-map-4 speed map ->
  `runtime/S013-directive.md`. Earlier rows are not comparable across the change (scores were equal-weight).
- 2026-10-01 (operator): `performance` CPU governor on the judge lanes 26-39 and SMT siblings 74-87
  (`judge/set_judge_governor.sh`; NOT persistent - rerun after any box reboot). The rest of the box stays `schedutil`.
  Rule kept: burning CPU to hold clocks (busy-waits, dummy goroutines) is never allowed.
- 2026-10-02 (operator): `performance` governor extended to the research lanes 6-9/12-15/19-22/42-45 and SMT
  siblings (+48) via `judge/set_judge_governor.sh` (NOT persistent). Operator ruling: raising the local backend's
  worker count (connections 2 -> 3+) is allowed without a per-team product argument, inside the +2% memory guard.

## Amendment 2026-10-03 (operator: "Swap to using 6.1 Sol Medium mid-season and for all seasons moving forward until I request a stop")
- Transport `sol61m`: `gpt-6.1-sol`, effort medium, Sol limits (builder turns 4500 s). chain-state `transport` = `sol61m`.
- Mid-season swap support: a Season records `model_ids` (every model it has run on, seeded from existing receipts on
  resume); completed roles validate against any of them, and `common_guard.validate_match_execution(models=...)`
  accepts the same list. S016 was resumed with `launch.py --resume --transport sol61m` during M002/M003.

## Amendment 2026-10-03 (operator: "Do all work we want to do"): S016 → S017 boundary
- **S016 promoted** (T1 M004, job 9b3da9b7: time +4.71% vs S015, 42/42; vs stock +25.75%). Review `reviews/S016-review.md`.
- **Judge deployed: sha `8c14d90f`** (`deploy_fastpath.py`; backup `judge/backups/20261003T105748Z-3421f7f30a09`): workload
  suites (`--workloads a,b,c`), the named workloads `big-index` and `large-files`, and the operator-only workload selector
  (the operator's procedure). Without a workload flag the main-workload command and decisions are unchanged.
- **Unit tmpfs inodes.** systemd's `TemporaryFileSystem` defaults to 400k inodes; a large-tree workload ran out (ENOSPC
  on mkdir). `remote_broker._restic_managed` now mounts the scratch with `nr_inodes=4m` for every restic judge unit.
- **Null re-run** (`judge/NULL-20261003.md`, runner `judge/null_rerun.py`): per-round paired SD on the official lanes is
  now **0.27% time** (was 1.11) and 2.48% RSS (was 2.22); research lanes read the same apart from rare ~2.5 s stalls.
  Gates unchanged. Probe bands come from this null through config (`probe_pair_sd_*`, `probe_phase_pair_sd_pct`,
  `probe_null_source`; `launch.py` `NULL_20261003`); the code defaults stay at the 2026-09-24 values.
- **Race fix installed as the incumbent** `incumbents/S016-racefix` (roster `active_cbk`, `current_best_known`, and a
  `maintenance` record; roster backup `roster.json.bak-before-racefix-*`). Evidence `race-fix/REPORT.md` and
  `Leagues/evidence/restic-racefix-20261003/` (runner `race-fix/judge_racefix.py`): judge correctness 15/15 + compat
  subset; TestRestore stress 0/1000 (unpatched S016 31/1000); concurrent restore/ls 0/100 (unpatched 34/100 and 33/100);
  12 rounds patched vs S016 time −0.00% (90% interval −0.18..+0.12). Rule for teams (S017 directive, ruling 7):
  never list the index before the snapshots.
- **Enabled from S017** (`launch.py` base config): `noise_aware_probes`; `season_merge` (band 0.5%, max 3 sources);
  `workload_suite: ['main', 'big-index', 'large-files']` with **`judge_parallel_lanes: 2`** (a three-workload job peaks at
  22.3 GiB of unit memory, tmpfs included; three at once beside four research jobs do not fit in 125 GB). A 6-round
  suite match with three arms takes ~30 min. Official suite canary (S016-racefix vs S015, job 7da6fa2c): mean +5.08
  (main +5.21, big-index +11.07, large-files −1.03); vs stock mean +20.38 (main +26.16, big-index +22.13,
  large-files +12.86).
- **Research brief text fixed** (`league_targets.research_spec`): with `rss_guard_pct` set it now says the score is time
  saved with a memory guard; since S013 it had still described the equal-weight score.
- Transport stays `sol61m` (GPT-6.1 Sol medium). Scores from S017 are suite means and are not comparable with S013-S016
  main-only rows.
- 2026-10-03 (operator: "have the s17 merge also include s16s candidates work"): `season_merge_extra_sources`
  (config list of {candidate, base, team, match, season, gain_pct}) adds carried-over trees to the season merge; each
  is diffed against ITS base (S016 trees: incumbents/S015) and 3-way merged onto the current incumbent; changes
  already in the incumbent contribute nothing, conflicts drop the source. S017 carries T3/T2/T4 S016 M004 trees.

## Amendment 2026-10-04 (operator): builders' fast research tools; role CPU pinning
- `fast_research_tools` (live from S017 M004) and `fast_tools_v2` (from S018): build_check, run_tests (own_tests),
  quick_bench (sequential stopping, pre-measured nulls, `versus="base"`, workloads list), profile_diff, sweep_constant,
  race_check, oracle_check, ablate_changes, phase_timeline, go_bench, closed_ground_search, try_merge, queue_status.
  Contract `research-tools/CONTRACT.md`; harness `research_tools_v2.py`; all diagnostic only, the judge alone scores.
  `probe_budget`: 3 full probes per turn, repeats only after UNRESOLVED. Fifth lane 2-5 for untimed jobs.
- `role_cpus` (from S018): Codex role processes were found running with affinity 6-47,54-95, i.e. able to land on the
  judge and research lanes; the codex runner now pins every role (and whatever it spawns) to
  `10,11,16-18,23,46,47` and their SMT siblings via LEAGUE_ROLE_CPUS.
- KNOWN GAP: Codex roles run as root with sandbox `workspace-write`, so builders can read box paths and run commands
  (they ran the Go toolchain and read supervisor logs in S017 M004). Briefs say "no shell". Held-out workload files
  are readable by a root process. Operator decision pending.

## Amendment 2026-10-04 (operator: "keep the held-out workload safe"): S017 → S018 boundary
- Operator-only material is OFF the box while a season runs: sealed to the storage box, restored only between seasons
  (an operator-only procedure). Boundary order: season ends → leak audit →
  restore → transfer test → seal → release the next season. This answers the KNOWN GAP above for the held-out files;
  roles still run as root.
- Judge `b5e4b9e1` (2026-10-04): comment-only change against `8c14d90f` (four comment/help strings, deployed in two
  steps: `ed7d4945` at 08:37 UTC, then `b5e4b9e1`); smoke correctness and probe jobs passed each time.
- S017 promoted T1 M003 (job 582b1263; suite +1.93% vs S016-racefix, 34/42); incumbent `incumbents/S017`.
- S018 config: directive `runtime/S018-directive.md` (cost-map-8, three-workload speed map); Sol medium builder limit
  4500 → 3300 s; `judge_parallel_lanes` 3; `fast_tools_v2`; `role_cpus`; `season_merge_extra_sources` = the previous
  season's best unpromoted trees with the incumbent they were built on as `base` (rule: refill at every boundary).
- Hand merge (operator 2026-10-04 "Merge first"): S017's unpromoted ideas plus two speed-map prototypes were ported
  onto `incumbents/S017` by hand and confirmed at 42 rounds (job b740c9e2: suite +6.06% vs S017, 33/42; big-index
  +17.90, memory +17.25; vs stock +27.08%). Installed as `incumbents/S017-merge` (roster `active_cbk`,
  `current_best_known` id `S017-merge`, `maintenance` record). Report `merge-S017/REPORT.md`. The automatic season
  merge works file by file and could not combine these trees; a hand merge at the boundary is the fallback when the
  best trees rewrite the same files. `season_merge_extra_sources` is empty for S018.

## Amendment 2026-10-05 (operator 2026-10-04 "Do what you deem best"; 2026-10-05 roster rotation): S018 → S019 boundary
- S018 promoted T1's tree (job 821fc91a: suite +4.00% vs `incumbents/S017-merge`, 42/42; vs stock +29.46%); incumbent
  `incumbents/S018`.
- Judge `a770cec8` (sha256 `a770cec81f9b2344449fc6d4cfd27cface44bc0c8019042340163dc308a22e21`, deployed 05:03 UTC):
  remote-backend workload support (`judge/REMOTE.md`). Nothing changes for `main`, `big-index`, `large-files` (same
  sandbox command, phase path and result keys; smoke correctness and probe passed). `74723d34` was deployed first
  (04:28 UTC) and replaced the same hour: it gave the REST server an absolute socket path, which is over the 107-byte
  unix socket limit on supervisor job paths; `a770cec8` starts the server in the work dir with the relative name.
- From S019 a FOURTH visible, scored suite workload, `remote`: main's corpus, mutation set, phases and oracle, with
  the repository reached through restic's REST backend on a simulated link, profile v2 = 30 ms per request,
  48 MiB/s per connection, 5 connections; workload version `restic-v2-workload-remote-2`. Suite score = mean of the
  four workloads' time saved; the per-workload memory guard (-2%) and time floor (-1%) apply to it. Rounds: match 8,
  reference 12, promotion 44 (11 per workload), still 29 wins and +0.25%. Measured at this boundary on the official
  lanes: null per-round paired time SD 0.42% (48 pairs); `incumbents/S018` vs stock +8.83% [8.70, 8.97], 12/12,
  memory +44.4%, 809 requests per pass against stock's 437 (restore 443 against 88); four-workload canary +24.12%
  vs stock, 8/8 (main +29.12, big-index +40.08, large-files +17.99, remote +9.30), 28 min, unit memory peak 25.5 GiB.
  Tables: `evidence/restic-remote-v2-boundary/` (`NULL-REMOTE-V2.md`, `REFERENCE-REMOTE-V2.md`).
- `probe_workload_pair_sd_pct: {'remote': ...}`: a probe on `remote` gets its own noise bands, taken from the
  incumbent-vs-incumbent pairs of the null because a probe compares two league trees (whole workload 0.95%;
  backup-mutated 5.8, check-read-data 3.85, forget-prune 3.15, restore 2.55); the other workloads keep the main
  values. On `remote` the incumbent's restore takes 10.35 s (435 requests) or 10.67 s (446 requests) and its
  check-read-data 6.70 s or 7.00 s from run to run; stock does neither (`judge/REMOTE.md` section 12).
- `judge_parallel_lanes` 3 → 2: three four-workload jobs (25.5 GiB each) do not fit beside four research jobs.
- Builders' harness `research_tools_v2.py` `998612d1` (remote support: quick_bench, phase_timeline, profile_diff and
  ablate_changes report requests and MiB per phase and arm on `remote`); the pre-remote file is kept beside it.
- Roster rotation (`roster-rotation/ROTATION.md`), config keys applied only to seasons >= S019: `persona_additions`
  (6 owners O13-O18, 6 builders, 8 a coach, 12 inventor lenses I01-I12), `owner_bench_refill`,
  `builder_rotation` (entry order new, unplayed, returning), `coach_rotation`, `inventor_rotation` (no bench before
  two seasons carry idea credit), `inventor_attribution`, `roster_form_tables`. Applied at this boundary through
  `roster-rotation/boundary.py`: T4's owner (relegated after S017) is replaced by a new owner; the owner bench holds ten more; first benching for S019: builders out, B25, B26, B27
  in (B28-B30 in reserve); a coach out, C35 marked entering.
- `season_merge_extra_sources`: S018's unpromoted T2, T3, T4 M004 trees (+2.14, +2.09, +1.09), base
  `incumbents/S017-merge`.
- Operator-only material stays off the box during seasons.

## Amendment 2026-10-06: S019 → S020 boundary
- S019 promoted T2's M004 tree (job 28102fc2, 44 rounds: suite +4.33% vs `incumbents/S018`, 44/44, memory +0.70%;
  main +1.68, big-index +0.89, large-files +5.37, remote +9.37, each 11/11; vs stock suite +28.15%, 44/44: main
  +30.23, big-index +41.29, large-files +22.72, remote +18.37); incumbent `incumbents/S019`. Season points T2 91,
  T4 67, T3 55, T1 47. The automatic season merge was skipped (fewer than two archive trees merge cleanly).
- Judge unchanged (`a770cec8`). The S020 config equals S019's except the season-named fields and
  `season_merge_extra_sources`, which is empty until the coordinator fills it after the hand merge of S019's
  unpromoted trees.
- Roster rotation, first season with the keys on. S019 recorded every lineup and idea credit (16 of 16 handoffs
  carried an `IDEA-CREDIT:` line) and its own end-of-season step decided for S020 (roster `rotation`,
  `S019/rotation.json`); nothing was applied by hand at this boundary. Builders: B24, B15, B08 benched; B28, B29, B30
  enter; B14, B07, B11 return from the bench; nobody retired; no reserve left. Coaches: C08 benched, C36 marked
  entering, C09 returns. Inventors: no bench yet (one of the two seasons of idea credit that the rule needs). Owners:
  T1 finished bottom, so T1's owner has a second strike (T3's owner also has two); the bench is unchanged.
- Known defect in `roster-rotation/boundary.py`: after a season that decided its own rotation, `report` and `apply`
  decide a second time on top of the stored record (they no longer see the builders who just sat out, and would
  bench them again and retire them). At such a boundary use `report` for the form tables only and do not run
  `apply --builders` or `apply --coaches`.
- Operator-only material stays off the box during seasons.
- Roster incident and repair. A manual `boundary.py apply --builders --coaches --write` run at 04:04 UTC (the
  coordinator's mistake, while testing a guard) wrote that second decision over the season's own; only the
  `rotation` key changed. Restored 11:18 UTC from the tool's own backup `roster.before-rotation-1791259470.json`
  with the operator's approval; the wrong version is kept beside it as
  `roster.json.bak-wrong-rotation-20261006T111808Z`. `boundary.py` now refuses a manual `apply --write` for builders,
  coaches or inventors without `--force`.
- Hand merge installed 11:20 UTC as `incumbents/S019-merge` (job 014cbfda, 44 rounds: suite +1.14% vs
  `incumbents/S019`, 36/44, eligible; main +1.29, big-index +1.31, large-files +2.23, remote -0.25; memory -0.81%;
  vs stock +28.99%). `season_merge_extra_sources` stays empty for S020: the unpromoted S018 and S019 ideas went
  through the merge (`merge-S019/REPORT.md`). League knowledge on the box now carries the S019 team review and
  closed-ground section T (the merge's installed, closed and left-out restore work, and the binary layout rule).
- S020 released 11:24 UTC on `sol61m` with `runtime/S020-directive.md` (cost-map-10), incumbent
  `incumbents/S019-merge`.
