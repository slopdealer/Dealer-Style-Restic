# Multi-workload suite for the restic judge (operator 2026-10-03)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

"Can we fix our system optimising against one package type" + an index-scaling workload. Two new VISIBLE workloads
beside the main one, a judge that rotates rounds over a workload list, and a controller flag that sends official
judging to the suite. **Nothing is deployed**: the judge change lives in the local `restic_judge_fastpath.py` (pinned in
`deploy_fastpath.py`), the controller change is behind `workload_suite` (unset = today's behaviour).
Build log: `SUITE-PROGRESS.md`. The held-out workload documented in this folder is not part of any suite, is never
selectable by a role, and is not named in any role-visible text.

## The workloads

| name | box path | what it is |
|---|---|---|
| `main` | `workloads/` | unchanged (JUDGE.md): 91k entries, 1.64 GiB, 90k small files + large/dedup/sparse files |
| `big-index` | `workloads-big-index/` | main's corpus, mutation set and manifests (byte copies), but the pristine repository already holds a **ballast**: 1,500,000 unique 48-byte data blobs (523 packs, 8 index files, +294 MB) referenced by one snapshot tagged `ballast` (path `/ballast`, host `league`, time 2020-01-01). |
| `large-files` | `workloads-large-files/` | 3,217 entries, 2.91 GB apparent: sparse VM image (1.5 GiB apparent, holes + 1-8 MiB random extents), page-structured DB file (256 MiB of 8 KiB pages), 4 x 48 MiB compressible logs, 3 x 256 MiB incompressible media files, a 3,000-file small tree, 8 medium files and the special-entries block |

**big-index phases.** Same seven phases and oracle as main. Every phase that opens the index carries the ballast's 1.5M
entries; S1a / Shist are built on top of the ballast repository, so every arm in every phase and round starts from a
fresh tmpfs copy of a repository that contains it. Only `forget-prune` changes its arguments (frozen in
`workloads.json` `phase_args`): `forget --keep-last 1 --keep-tag ballast --prune`, so the ballast survives and prune
must carry every ballast blob. `restore latest` / `ls --json latest` pick the workload snapshot (the ballast is the
oldest snapshot); `check --read-data` reads everything, ballast included. The judge counts snapshots already in the
pristine repository when it validates the history (main: 0, big-index: 1).

**large-files v1 -> v2** (2,779 declarative ops): 300 scattered 64 KiB in-place writes in the VM image (some into
holes) plus a new 32 MiB extent inside a hole; 2,000 DB page rewrites + 16 MiB of appended pages; 2-6 MiB appended to
each log; one media file truncated 256 -> 200 MiB, one renamed into `archive/` (identical bytes, new path); small tree:
5% rewritten, 2% appended, 1% deleted, 200 new files, 30 chmods; symlink retarget + xattr as main. History: v2, then v3 =
v2 minus `media/media-0.mkv` and `logs/`. The judge's detach step now keeps sparse files sparse (`cp --sparse=always`
for a file with fewer allocated blocks than its size; main's mutated files are never sparse, so main is unchanged).

## Regenerate (never overwrite main or the held-out workload)

On the box as root, with the generator next to a judge copy named `restic_judge.py` (it imports judge helpers):

```
# ballastgen: compile once inside a COPY of pinned stock with the judge toolchain (judge/suite-tools/build_ballast.sh)
cp -a $R/stock ~/suite-dev/ballast-src; cp judge/ballastgen/main.go ~/suite-dev/ballast-src/cmd/ballastgen/
(cd ~/suite-dev/ballast-src && GOFLAGS=-mod=readonly GOPROXY=off CGO_ENABLED=0 $R/toolchain/go/bin/go build -trimpath -o ~/suite-dev/bin/ballastgen ./cmd/ballastgen)
python3 workload_gen.py --profile big-index --ballast-bin ~/suite-dev/bin/ballastgen --out $R/workloads-big-index [--ballast-blobs N]
python3 workload_gen.py --profile large-files --out $R/workloads-large-files [--seed 20261003]
chattr -R +i $R/workloads-big-index $R/workloads-large-files      # optional seal, as main
```

Both take ~20 s on CPUs 2-5 (versions `restic-v2-workload-big-index-1`, `restic-v2-workload-large-files-1`). Results
on different generations are never pooled. Generated 2026-10-03: big-index pristine `tree_sha256` and ballast snapshot
in its `pristine.json`; large-files v1 `ffe1833b...`, v2 `7334252b...`, mutation tree `1d90fa4c...`.

## Judge: suite jobs (`--workloads main,big-index,large-files`)

* `--workload NAME` (one workload) is unchanged from the held-out work; `--workloads a` with one name is the same as
  `--workload a`. Without either flag the judge's command, cache keys and decisions are those of today (tests compare
  `compare()`, constants, `orders()` and phase tables with the pre-suite judge byte for byte).
* A suite job builds and compatibility-tests once, then builds every workload's stock-made states and runs the
  warm-up full oracle of every arm on every workload, then measures. **Round r measures workload (r-1) mod N**, each
  workload with its own `orders()` rotation (its k-th round uses the k-th pattern). Rounds = the mode's rounds rounded
  up to a multiple of N (probe = one round per workload; 6/12/42 stay 6/12/42 for 3 workloads). Every round file
  records its workload; resume refuses a round from another workload.
* Suite jobs always run the full path (also for probe/correctness): the research cache is never read or written by a
  suite job. Single named workloads in research modes keep the cache, keyed by that workload's identity in its own
  bundle root (as the held-out work introduced).
* **Score** = mean over workloads of each workload's median paired score (TIME_WEIGHT 1.0 = time saved). Every workload
  weighs the same whatever its seconds. **Aggregate wins** = rounds whose own-workload score > 0. `time_saved_pct` /
  `rss_saved_pct` = mean of workload medians; `per_phase` = mean over workloads of the phase medians, wins/rounds
  summed (so `subfactor_min_wins` keeps its scale); `interval90` = stratified bootstrap (each workload resampled on its
  own) of the mean of medians. `comparisons.<C>.per_workload.<name>` holds each workload's full single-workload block;
  `pair_changes` carry `workload`. `arms.<arm>.per_workload` holds per-workload medians; top-level
  `median_total_seconds` / `per_phase.median_seconds` are summed over workloads, peak RSS averaged.
* **Promotion** (suite): the existing gates on the aggregate (`score_wins >= 29`, score >= 0.25%, 5% axis caps vs
  incumbent and stock, beat stock, +2% memory guard, distinct build input and machine code) PLUS, per workload: peak
  memory vs incumbent >= -`RESTIC_JUDGE_RSS_GUARD_PCT` (2.0) and time vs incumbent >= -`RESTIC_JUDGE_WORKLOAD_REGRESSION_PCT`
  (default **1.0**). The result's `eligibility.reasons` names the workload that failed.

## Broker and controller

* `remote_broker.judge_restic(..., workloads=None)` / `research_restic(..., workloads=None)`: None or `['main']` = today's
  command; one other name = `--workload NAME`; 2+ = `--workloads a,b,c`. The workload arguments are part of the reuse
  key (absent = today's key). A suite job gets `RESTIC_SUITE_EXTRA_TMPFS_GIB` (7) more tmpfs and unit memory per extra
  workload and twice the mode's time cap.
* Config `workload_suite: ['main', 'big-index', 'large-files']` (league config; unset = today). Only `main`,
  `big-index`, `large-files` are accepted (anything else raises at Season start). With 2+ names:
  * every official judge call (match, merge row, rescreen `reference`, promotion, final comparison) goes through
    `ResticTarget.judge` with `workloads=suite`;
  * `benchmark_candidate` / `check_parity` default to main; their schema gains an optional `workload` enum of the suite
    names (`agent_api_runner.target_research_schemas`, used by the API runner and the Codex bridge); anything else is
    refused;
  * the research brief describes the suite and the per-workload gates;
  * `phase_breakdown` gains `workloads` (per-workload score/time/memory/wins), match-table rows gain `workloads`,
    continuity records carry `workloads` and the verdict sentence lists each workload; subfactor time cells use the
    judge's aggregated per-phase cells.
* Tests: `tests/test_workload_suite.py` (21).

## Smoke (2026-10-03, CPUs 2-5, dev judge copy `~/suite-dev/judge`, scratch cache, idle-priority spinners)

Every job completed with every oracle passing, except one compat-subset flake noted below. Readings are from 1 probe
round (diagnostic: per-round SD ~1.2%). Candidate = S015 incumbent, incumbent = stock (aliased, 2 arms).

| job | wall | result |
|---|---:|---|
| big-index correctness | 2.7 min | FAILED in the compat subset, not on the workload: S015 failed stock `cmd/restic TestRestore` ("tried listing type snapshots after index"). The same S015 passed the subset in large-files correctness 12 min later = a race in S015 that the stock test catches only sometimes |
| big-index probe | 7.5 min | complete (bundle built: states 5.7 GB tmpfs) |
| large-files correctness | 4.1 min | complete (build, compat subset passed, full oracle) |
| large-files probe | 2.0 min | complete (bundle: states 4.9 GB tmpfs) |
| 3-workload suite probe | 16.7 min | complete, 3 rounds (one per workload), every warm-up full oracle and per-round oracle passed |

**Stock pass per workload** (suite probe; CPUs 2-5 clock-held, which read main ~20% faster than JUDGE.md's original
28.2 s table; time in s / peak RSS in MiB):

| workload | total s | mean peak MiB | backup-initial | backup-unchanged | backup-mutated | restore | check-read-data | forget-prune | ls-json |
|---|---:|---:|---|---|---|---|---|---|---|
| main | 22.69 | 143.8 | 5.70 / 189 | 2.26 / 133 | 2.38 / 173 | 6.09 / 208 | 2.55 / 103 | 2.20 / 132 | 1.51 / 69 |
| big-index | 33.21 | 427.8 | 6.01 / 442 | 2.77 / 406 | 2.90 / 439 | 6.68 / 451 | 5.19 / 431 | 7.68 / 409 | 1.99 / 418 |
| large-files | 17.00 | 129.0 | 4.50 / 204 | 1.94 / 99 | 2.54 / 184 | 2.97 / 165 | 2.18 / 98 | 2.14 / 94 | 0.73 / 59 |

The single-workload probes agree with these readings: big-index 33.42 s / 424.6 MiB, large-files 17.02 s / 131.4 MiB.

**S015 vs stock per workload** (suite probe): main +21.16% time (+42.6% memory), big-index **+11.81%** (memory
-1.7%), large-files +13.00% (+38.8%); suite score +15.32%, 3/3 wins. The league's gains are fitted to main: on big-index,
check-read-data runs **-33% (slower than stock)** and forget-prune peaks at 502-575 MiB against stock's 409; on
large-files the backup phases gain ~0-6% where main gains ~6-20%.

**Who owns the peak in big-index** (`suite-tools/peakheap.py`: cost-map-2's `stock-census` binary, heap profile at the
RSS high-water mark, one run per phase, in the judge sandbox on the big-index states). In-use heap whose stack passes
through `internal/repository/index`, as a share of sampled in-use heap at the peak:

| phase | peak RSS MiB | in-use heap MiB | index MiB | index share |
|---|---:|---:|---:|---:|
| backup-initial | 379 | 204 | 195 | 95.6% |
| backup-unchanged | 420 | 206 | 196 | 95.1% |
| backup-mutated | 415 | 217 | 207 | 95.6% |
| restore | 438 | 275 | 264 | 96.0% |
| check-read-data | 442 | 222 | 214 | 96.5% |
| forget-prune | 420 | 260 | 251 | 96.7% |
| ls-json | 395 | 251 | 240 | 95.7% |

The index is the largest peak owner in **all seven** phases, and the peak occurs while the index is loaded: the top
owners are zstd `DecodeAll` of the index files (102-153 MB), JSON decode `reflect.growslice` (35-61 MB) and
`bytes.growSlice` (32-48 MB), then the in-memory index (`hashedArrayTree.grow` and friends). Loading the index costs
more memory at the peak than holding it.

**tmpfs per suite job.** States: main ~4.5 GB, big-index 5.7 GB, large-files 4.9 GB, all resident at once, plus one
phase clone and one restore target (large-files restores 2.9 GB, the VM image is written dense) = ~20 GB peak. The
broker gives a suite job `10 + 7 x (N-1)` GiB of tmpfs and `14 + 7 x (N-1)` GiB of unit memory (24 / 28 GiB for 3).
Three suite jobs in parallel on the official lanes need ~60 GB; the box had ~90 GB available during S016.

## What the coordinator does at the boundary

1. Deploy the judge (unlocks suites, big-index, large-files and the held-out selector): copy the local
   `restic_judge_fastpath.py` to the box's `judge/restic_judge_fastpath.py`, then `python deploy_fastpath.py` (pinned
   sha `8c14d90f...`). All research caches miss once (keys hold the judge sha).
2. Sync `Leagues/tools` (`remote_broker.py`, `league_targets.py`, `agent_api_runner.py`, `pylint_v2.py`,
   `match_continuity.py`) to the box copy the controller runs from.
3. Optional before turning it on: one 6-round suite `match` of the incumbent vs stock on an official lane (~45-60 min)
   and a null if the gates are to be re-derived for the suite (the per-workload gates use the main null's guard values).
4. Set `workload_suite: ['main', 'big-index', 'large-files']` in the league config (launch.py or the season config;
   not edited here) at a season start. Unset = today's behaviour.
5. Pre-existing S015 compat flake (`TestRestore`, see the smoke table): decide whether the compat subset should retry
   once, or whether S015's concurrent snapshot listing should be fixed before teams build on it.

All smoke work used CPUs 2-5 only (freed 2026-10-03T07:16Z); `/dev/shm/suite-dev` was removed; the dev copy and logs
stay in `~/suite-dev` (924 MB, mostly the ballastgen build tree).

## Deployed 2026-10-03 (S016 → S017 boundary)

Judge `8c14d90f` is live; `workload_suite` is on from S017 with `judge_parallel_lanes: 2`.

* **Official-path canary** (broker unit, official lane, job `7da6fa2c89704b91aae096076045a1ef`, evidence
  `Leagues/evidence/restic-racefix-20261003/`): 6-round `match`, three arms (S016-racefix, S015, stock), 199/199 oracle
  units, **29.8 min**. S016-racefix vs S015: mean **+5.08** (main +5.21, big-index +11.07, large-files **−1.03**);
  vs stock: mean **+20.38** (main +26.16, big-index +22.13, large-files +12.86); memory vs stock main +43.0%,
  big-index **−4.1%** (forget-prune 541 vs 411 MiB), large-files +40.4%.
* **Memory.** The unit's cgroup peak was **22.3 GiB** (cap 28 GiB; the 24 GiB tmpfs is charged to it). The scratch is a
  private unit tmpfs, not `/dev/shm`, so the limit is RAM: three suite jobs (67 GiB) beside four research jobs, the
  research bundles in `/dev/shm` and the role processes do not fit in 125 GB. Two official lanes do.
* **Inodes.** The unit tmpfs had systemd's default 400k inodes; the broker now sets `nr_inodes=4m`.
* **Research probes on a named workload** run inside the default 10 GiB research unit: big-index probe 8 min,
  large-files probe 6 min (cold bundle), both complete with every oracle passing.
* Not done: a null for big-index and large-files (probe bands use main's per-phase values).
