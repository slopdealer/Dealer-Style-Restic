> PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.

# Builder fast tools: interface contract (2026-10-04)

Operator order: "How can we maximally assist and speed up the builders? ... build new tools they can use to test
specific things quickly" / "let's do it now and get it into the existing season asap".

Two parts are built in parallel against this contract:
- the **harness**: a box-side program;
- the **controller integration**: role tools, broker and queueing.

The harness is NOT the judge. Every result is diagnostic only. The judge alone scores.

## Harness CLI (box): `/opt/league/restic-league-v2/research-tools/research_tools.py`

```
python3 research_tools.py <op> --candidate DIR --incumbent DIR [--stock DIR] --cpus 6-9 --team T1
        --workload main|big-index|large-files --out /path/result.json [op options]
```

Rules for every op:
- It runs inside the same kind of sandbox the judge uses for builds and runs: no network, candidate code never
  runs outside it.
- It uses the pinned offline Go toolchain.
- It keeps a persistent per-team Go build cache: `--team` selects
  `/opt/league/restic-league-v2/research-tools/gocache/<team>`.
- It writes ONE JSON object to `--out` atomically.
- It exits 0 even when the candidate fails. A harness error is non-zero plus `{"ok": false, "error": ...}`.
- It never touches the held-out workload, and never accepts a workload name outside the three above.

Common result fields:
- `ok`
- `op`
- `seconds` (wall)
- `workload`
- `candidate_sha256`
- `summary`: one plain sentence a builder can act on
- `diagnostic_only: true`

| op | options | what it does | target wall time | result fields |
|---|---|---|---|---|
| `build` | - | `go build ./cmd/restic` and `go vet` on changed packages | 10-25 s warm | `build_ok`, `vet_ok`, `errors` (first 40 lines) |
| `test` | `--packages internal/restorer,...` `--run REGEX` `--count N` (default 1, max 20) `--parallel-copies K` (default 1, max 4) | stock `*_test.go` overlaid as the judge does, on the named packages only | 10-90 s | `passed`, `failed` (names), `output_tail` |
| `bench` | `--phases restore,check-read-data` (1-7 of the judge's phase names) `--reps N` (default 5, max 9) | Paired candidate vs incumbent on the named phases only. Uses prepared repository states (the same states the judge builds), interleaved A/B order, wall time and peak RSS per rep. | 20-60 s per phase | per phase: `time_saved_pct` (median of pairs), `rss_saved_pct`, `wins`, `reps`, `noise_band_pct`, `verdict` ('gain beyond noise' / 'loss beyond noise' / 'UNRESOLVED'), raw `pairs` |
| `profile` | `--phase restore` `--top N` (default 15) | One instrumented run each of candidate and incumbent on that phase: CPU profile top-N diff, heap in use at peak by owner, GC count and pause total, syscall counts by name, cores used | 30-90 s | `cpu_top` (rows: function, cand_s, inc_s, delta_s), `heap_at_peak`, `gc`, `syscalls`, `cores_used` |
| `sweep` | `--file internal/x/y.go` `--pattern 'const window = (\d+)'` (one capture group) `--values 8,32,64,256` (max 6) `--phases ...` `--reps N` (default 3) | Builds one variant per value by rewriting the captured text, benches each against the incumbent on the named phases, drops values clearly worse, re-measures survivors | 1-4 min | `values` (rows: value, time_saved_pct, rss_saved_pct, verdict), `recommended` (the smallest value within noise of the best), `note` |
| `race` | `--runs N` (default 200, max 1000) | Concurrency stress: stock `cmd/restic` `TestRestore` with 4 parallel copies, plus restore/ls `latest` during a stock backup loop (the S015 bug reproduction) | 1-3 min | `testrestore_failures`, `concurrent_restore_failures`, `concurrent_ls_failures`, `examples` |
| `baseline` | `--out-dir DIR` (candidate = the incumbent) | Baseline card of the incumbent on one workload: per-phase time, RSS, cores, CPU top, heap owners, syscalls | 3-6 min once per season | `card` (markdown) plus the same data as JSON |

Noise bands: the harness measures its own null (incumbent vs incumbent with the same reps) once per workload and phase.
It caches the null under `research-tools/null/` and reports the band it used.

## Controller side

Role tools (builders only; the same names appear in tool schemas, the MCP bridge and briefs): `build_check`,
`run_tests`, `quick_bench`, `profile_diff`, `sweep_constant`, `race_check`. `check_parity` and `benchmark_candidate` stay.

- **Dispatch:** `ResticTarget.research_callback` to a new `remote_broker.research_tool(op, workspace, incumbent, options, labels)`.
  This launches the harness as a systemd unit in the research slice on an allocated lane and returns the result JSON.
- **Lanes:** research lanes 6-9, 12-15, 19-22, 42-45, plus a fifth lane 2-5 (taken under
  `flock /opt/league/restic-league-v2/shared/cpus-2-5.lock`).
- **Queueing:** short ops (`build`, `test`, `bench`, `profile`) go ahead of long ones (`benchmark_candidate`, `check_parity`, `sweep`, `race`).
  At most 2 concurrent jobs per team.
- **Probe budget** (config `probe_budget`; default: `benchmark_candidate` at most 3 calls per role turn; `rounds` > 1 only
  when the previous reading for the same tree was UNRESOLVED): a refused call returns a plain message that points at
  `quick_bench`.
- **Config flag:** `fast_research_tools` (default off; the coordinator turns it on). With it off, nothing changes.
- **Role-visible text:** one short block that describes each tool in a line and the intended loop. That loop is:
  `build_check`, then `run_tests`, then `quick_bench` or `profile_diff` on the phases you touched; `race_check` if you
  touched locking or concurrency; ONE `benchmark_candidate` and `check_parity` before handoff.
- **Baseline card:** at season start (or first use) the controller runs `baseline` per suite workload and places the cards
  in every team workspace under `reference-evidence/baseline/`.

---

# v2 (2026-10-04): changes from real usage, and seven new tools

Operator order: "let's improve current tools and look into if we could make other useful tools." The evidence is
`USAGE-STUDY-S017-M004.md` (53 builder jobs, 14 builder turns). v2 is **off by default**: config key `fast_tools_v2`
(needs `fast_research_tools` too). With the key absent the six v1 tools, their text, their lanes and the v1 harness
are exactly as above. v2 runs a second harness file beside the first:
`/opt/league/restic-league-v2/research-tools/research_tools_v2.py` (deployed; v1 file untouched).

## Harness CLI additions (`research_tools_v2.py`)

```
python3 research_tools_v2.py <op> --candidate DIR --incumbent DIR [--base DIR] [--stock DIR] --cpus 6-9 --team T1
        --workload NAME | --workloads a,b,c --out /path/result.json [op options]
```

`--base DIR` is the tree the workspace started this match from (the team's retained base). `--versus base` compares
against it instead of the incumbent (`bench`, `timeline`, `gobench`); `ablate` reverts to it by default.

| op | options (new in bold) | what changed or what it does | measured wall time (CPUs 2-5, schedutil) | new result fields |
|---|---|---|---|---|
| `bench` | `--phases`, `--reps N` (**upper limit of pairs, default 9, at most 15**), **`--workloads a,b`**, **`--versus base`**, **`--fixed-reps`** | **Sequential**: a phase is judged after 3 pairs and after every pair from then on. It stops early when the median is 1.5 bands clear, at 5 pairs when resolved or too small to resolve, and runs on to the limit only while it could still resolve. **No null is measured inside a call.** | restore + forget-prune on a +10% tree: **69 s (v1, same call at 9 reps: 154 s)**; a clear phase alone: 40 s for 3 pairs | per phase `reason`, `stopped`, `pair_sigma_pct {own, null}`, `share_of_workload_time_pct`, `worth_on_workload_score`; `worth` (`resolved_points`, `point_estimate`, `text`); `workloads {name: {phases, worth}}`; `versus`; `reps_target`, `reps_limit`; `notes` |
| `test` | **`--own-tests`**, **`--verbose`** | Own tests: the candidate's NEW `*_test.go` files (names stock does not have) are compiled in; stock test files stay stock. A regex that matches nothing now says so (v1 said "do not compile"). | 1.4-2.5 s | `no_test_matched`, `own_test_files` |
| `race` | `--runs N`, **`--full`** | Default is now the quick check: 40 TestRestore runs and 20 concurrent restore/ls iterations. `--full` = 200 / 100. | **31-36 s** (v1 default: 130-143 s); the quick run still catches the S015 race (7/20, 6/20 on S016) | `quick` |
| `baseline` | - | Measures the v2 null of every phase it covers and stores it in `research-tools/null2/`. | 38 s for one phase with its null | `null_time_pair_sigma_pct` |
| **`oracle`** | `--phases`, `--workloads` | The judge's per-phase oracle against stock, for the named phases only. restore: exact manifest equality (content, modes, owners, file and directory mtimes, xattrs). ls-json: parsed lines equal. backup-*: snapshot count and normalised metadata/summary equal, tree id equal (else the slow path: stock `check --read-data` + stock restore manifest). forget-prune: same kept snapshots, stock `check --read-data`, stock restore manifest. check-read-data: exits 0, and detects a pack with one flipped byte. Stock's reference digests are cached per prepared state. | **78 s for five phases, first call; 13 s for restore + ls-json with cached references** | `oracle_ok`, per phase `ok`, `checks`, `failures`, `stock_reference` |
| **`ablate`** | `--phases`, `--groups a.go+b.go,c.go`, `--reps N` (2-7, default 3), `--max-variants N` (default 6, at most 8), `--base DIR`, `--versus incumbent` | Builds the candidate with each changed file (or named group) reverted to the base, benches every variant with the candidate in rotated rounds. A group's number is candidate versus candidate-without-it. Variants that do not build are reported. More than 6 changed files are grouped by package directory. | **55 s** (2 changed files, restore); 117 s (17 files in 6 groups) | `changed_files`, `groups [{group, files, changed_lines, builds_without_it, per_phase}]`, `all_changes`, `versus` |
| **`timeline`** | `--phase`, `--reps N` (runs per arm, default 3), `--versus base` | Builds both trees with stage marks (scratch copy only; an anchor that is not in a tree is skipped and named) and reports per stage the wall seconds, CPU seconds, cores used and idle core seconds. Returns the last stderr lines of the candidate. | **38 s** | `stages [{stage, cand, ref, wall_delta_s}]`, `marks_missing`, `candidate_stderr_lines`, `candidate_exit` |
| **`gobench`** | `--packages`, `--bench REGEX`, `--benchtime 1s\|200x` (at most 5s / 1000000x), `--count N` (1-6, default 3), `--own-tests`, `--versus base` | `go test -run ^$ -bench REGEX -benchmem` on both trees, 2-minute cap per tree. | **10 s** (two crypto benchmarks, 200x, count 2) | `benchmarks [{benchmark, cand_ns_op, ref_ns_op, time_saved_pct, spread_pct, *_bytes_op, *_allocs_op}]`, `timed_out` |

**Noise band (v2).** Per-pair spread = the larger of (a) the pre-measured null of that workload, phase and governor
and (b) this call's own spread, both as 1.4826 x the median absolute deviation (range-based below 5 pairs). Band =
2 x 1.2533 x spread / sqrt(pairs), floor 0.3%. A verdict needs the median outside the band and 80% of the pairs on
its side; `reason` states which of the two failed. The null store is `research-tools/null2/`; v1's files in `null/`
are read as a fallback (their eight pairs re-read with the robust estimator). A null is keyed by workload, phase and
governor only: any incumbent's null serves.

**Worth line.** `worth_on_workload_score` = phase time saved x the phase's share of the reference's time on that
workload (the judge's workload score is the time saved over the sum of the seven phases; the suite score is the mean
over its workloads).

## Controller side (v2)

Role tools: the six v1 names plus `oracle_check`, `ablate_changes`, `phase_timeline`, `go_bench` (box jobs) and
`closed_ground_search`, `try_merge`, `queue_status` (answered by the controller, no box job).

| tool | arguments | notes |
|---|---|---|
| `build_check` | `tree` | `tree: "merged"` = the scratch tree left by `try_merge keep=true` (also on `run_tests`, `quick_bench`, `oracle_check`) |
| `run_tests` | `packages`, `run`, `count`, `parallel_copies`, `own_tests`, `verbose`, `tree` | a leading `./` on a package is accepted |
| `quick_bench` | `phases`, `reps` (limit; above 15 is lowered to 15 with a note), `workload` or `workloads`, `versus` (`incumbent`/`base`), `tree` | |
| `race_check` | `runs`, `full` | |
| `oracle_check` | `phases`, `workload` or `workloads`, `tree` | |
| `ablate_changes` | `phases`, `groups` (each item: files joined with `+`), `reps`, `workload`, `versus` | reverts to the match's starting source unless `versus: "incumbent"` |
| `phase_timeline` | `phase`, `workload`, `versus` | |
| `go_bench` | `packages`, `bench`, `benchtime`, `count`, `own_tests`, `versus` | |
| `closed_ground_search` | `query`, `file`, `limit` | searches `<league home>/knowledge/closed-ground/*.md`, `knowledge/closed-ground*.md`, `knowledge/S*.md` (config `closed_ground_sources` overrides); units are table rows, list items and paragraphs; a unit naming a held-out workload is never returned (config `held_out_workloads` adds names) |
| `try_merge` | `branches`, `keep` | lead builder (`candidate-team`) only; three-way file merge (the season merge's `merge_text`) against the match's starting source; the scratch copy is `league/evidence/restic-try-merge/<team>-<season>-<match>/tree`, never a season workspace |
| `queue_status` | - | lanes held (one read-only `lslocks` call), jobs waiting, the caller's running jobs with their stage, a rough expected wait |

- **Dispatch:** `remote_broker.research_tool(op, workspace, incumbent, options, labels)` as before; `options['harness']
  = 'v2'` selects the v2 harness, option table and lane policy, `options['base']` is the starting source.
- **Lanes (v2):** untimed jobs (`build`, `test`, `race`, `oracle`) run on CPUs 2-5 under a SHARED flock of
  `cpus-2-5.lock` (any number at once, never beside an exclusive holder). Timed jobs (`bench`, `profile`, `sweep`,
  `ablate`, `timeline`, `gobench`, `baseline`) take ONE of the four research lanes exclusively. No timed job runs on
  the schedutil lane any more.
- **Queueing (v2):** untimed jobs use their own queue (8 at once, 4 per team) and never wait behind a benchmark; the
  two-per-team cap applies to timed jobs and the two judge tools only.
- **Baselines and nulls:** the season's baseline job runs the v2 harness on a research lane, so every workload x
  phase null is measured once on the performance governor together with the cards.
- **Starting source:** `match_continuity.source_for(season, team, match)`; with none recorded `versus: "base"` is
  refused in plain words and `ablate_changes` reverts to the incumbent.
- **Role-visible loop:** `build_check` after an edit, `run_tests` on the changed packages (`own_tests` for new
  tests), `oracle_check` and `quick_bench` on ONLY the phases touched (`versus="base"` measures the builder's own
  addition), `ablate_changes` when several changes are in the tree, `phase_timeline` or `profile_diff` to see where
  the time is, `race_check` if locking or concurrency was touched, then ONE `benchmark_candidate` and ONE
  `check_parity` on the handoff tree.

## Switch-over (coordinator, at a season boundary)

1. Harness: nothing to move. `research_tools_v2.py` is deployed beside `research_tools.py` and is selected by the
   config key. Source of truth: `LEAGUES_HOME\restic-v2\research-tools\research_tools_v2.py`.
2. `restic-v2/launch.py`, in the season config beside line 186 (`'fast_research_tools': True,`): add
   `'fast_tools_v2': True,`. Nothing else in `launch.py` changes: `TOOLS` (lines 44-48) already uploads
   `league_targets.py`, `remote_broker.py` and `agent_api_runner.py`.
3. Closed-ground search sources: copy `restic-v2/cost-map-7/data/closed-ground.md` (or its successor) and
   `restic-v2/reviews/S*-review.md` to `<league home>/knowledge/closed-ground/` on the box
   (`/opt/league/restic-league-v2/league/space-bunny/restic-v2/knowledge/closed-ground/`). Without them the
   tool searches the season digests only.
4. First fast-tool call of the season starts the baseline job per workload on a research lane (about 8 minutes
   each, background): it writes the cards and the v2 nulls. Until a null exists a bench uses v1's stored pairs or
   its own spread and says so in `null.origin`.
5. Rollback: remove the config key. The v1 harness, tables and lanes are untouched.
6. `restic-v2/INTERFACE.md`: add one amendment line at the boundary, for example: "2026-10-04 (from S018): builder
   fast tools v2 (`research-tools/CONTRACT.md`, section v2; config `fast_tools_v2`): sequential `quick_bench`, robust
   noise bands, `versus: base`, own tests, and the tools `oracle_check`, `ablate_changes`, `phase_timeline`,
   `go_bench`, `closed_ground_search`, `try_merge`, `queue_status`. Diagnostic only; the judge is unchanged."

---

# Workload `remote` (2026-10-04; scored from S019; judge/REMOTE.md)

`remote` is main's corpus and phases with the repository behind a simulated network link (restic's REST backend,
30 ms per request, 48 MiB/s per connection, 5 connections; frozen in `workloads-remote/workloads.json`). The harness
accepts `--workload remote` (and `remote` inside `--workloads`). It needs a judge with remote-backend support
(`a770cec8` or later); with an older judge the op ends as a harness error that says so.

**How an arm runs.** For every restic process of every arm the harness starts the workload's frozen REST server
(`workloads-remote/bin/restserver`, the profile from `workloads.json`) on a unix socket in the run's work dir, runs
restic with `-r rest:http+unix:///work/rest.sock:/` in the judge's sandbox, then stops the server: outside the
sandbox and outside the timed region, exactly as the judge does. The server's storage is a private copy of the
prepared state outside the sandbox's `/work` (read-only phases: the shared state copy); after the server has stopped
it becomes `work/repo`, so stock's oracle reads what the arm wrote as a local repository. The server inherits the
lane. Local workloads take the same code path as before (no server, same sandbox command).

**What the tools report on this workload** (from the server's own statistics; absent on every other workload):

| op (role tool) | added result fields |
|---|---|
| `bench` (`quick_bench`) | per phase `remote: {cand, ref}`, each `{requests, mib_up, mib_down, latency_s, queue_s, max_in_flight, transfer_floor_s, runs, by_kind: {"GET data": {requests, mib_up, mib_down}, ...}}` (medians over the arm's runs); per pair `cand_requests`, `inc_requests`, `cand_mib_up`, `cand_mib_down`, `inc_mib_up`, `inc_mib_down`; top level `remote_profile` (the profile and a note). The `summary` sentence of every phase ends with `requests C vs R, MiB up C vs R, down C vs R`. |
| `timeline` (`phase_timeline`) | `remote: {cand, ref}` as above for the phase; every row of `stages` carries `requests`, `mib_up`, `mib_down` of the requests that ARRIVED in that stage (from the server's request log; a request within ~0.03 s of a stage edge may land on either side). The summary names requests and MiB per arm. |
| `profile` (`profile_diff`) | `remote: {cand, inc}` of the CPU-profile run; the summary ends with the request sentence. |
| `ablate` (`ablate_changes`) | per group and phase `remote: {with_it, without_it}`, each `{requests, mib_up, mib_down, latency_s}`. |
| `baseline` | per phase `remote` (as bench's arm cell); the card gets a section "The link (REST backend)": requests, MiB up and down, latency, transfer floor and most connections busy per phase, and the request table by method and type. |
| `oracle` (`oracle_check`) | no new fields. The corrupted-pack check of `check-read-data` goes through the server; stock's reads of the arm-written repository stay local. |

`latency_s` = requests x the profile's latency (connection-seconds). `transfer_floor_s` = MiB moved / (rate x
connections): no code change goes below it without moving fewer bytes. `sweep` works unchanged (same run path) and
reports no link fields.

**Measured (CPUs 2-5, schedutil; incumbent `S017-merge` vs stock; REMOTE.md section 9):** `bench` of all seven
phases at 1 pair: 138 s; `timeline --phase restore --reps 1`: 32 s; `oracle --phases check-read-data,forget-prune`:
60 s. A real summary line: `restore: -1.60% time, +55.25% memory, 0/1 wins, band +/-0.3% -> loss beyond noise;
requests 446 vs 88, MiB up 0 vs 0, down 1245.4 vs 1245.4`.

**Prepared states.** As for every workload the harness reads the judge's research bundle
(`/dev/shm/restic-league-v2-cache/workload-remote/bundle-*`) and never builds it: one research probe on `remote`
makes it (`judge/remote_boundary.py bundle` at the boundary).

**Controller side.** `remote` is offered by a tool schema only when the season's `workload_suite` names it and
`fast_tools_v2` is on (the v1 harness has no REST support). `remote_broker.RESTIC_TOOL_WORKLOADS` holds the fourth
name; lanes, queues and unit caps are unchanged. The role-visible block gains one paragraph (requests and MiB per
phase and arm; what to aim with).

**Dev-only environment** (unset on the season path): `RESEARCH_TOOLS_JUDGE` = a judge FILE to import instead of
`<root>/judge/restic_judge_fastpath.py`; `RESEARCH_TOOLS_STATE` = a directory for the persistent harness state
instead of `<root>/research-tools/`.

**Staging and the switch (coordinator, S019 boundary; REMOTE.md section 11, step 4).** The harness with this
support is the local `research_tools_v2.py` (sha256 `998612d135e786e173e0bacc8239fa8b11fb6ea96f7f4eb20c8d154cae5595e3`),
staged on the box as `research-tools/research_tools_v2_remote.py`. The season in progress keeps running the live
`research_tools_v2.py` (`e19a384c...`), which is untouched. Switch, with no season alive and after the judge deploy:
`cp -p research_tools_v2.py research_tools_v2.pre-remote.py && cp research_tools_v2_remote.py research_tools_v2.py`,
then check the sha256. The broker's harness path, lanes and option tables do not change. Rollback: copy the backup back.
