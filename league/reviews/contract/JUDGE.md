# Restic League V2: the benchmark judge

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

This document describes the instrument that scores every restic match and season. It covers what is measured, how, on what,
how correctness is enforced, how noisy the instrument is, and why the gates are set where they are.
Contract: `INTERFACE.md` (plus its "Judge implementation notes" section). Build log: `JUDGE-PROGRESS.md`.

## Where it lives

| What | Path (the league box) |
|---|---|
| Judge (trusted, one file) | `/opt/league/restic-league-v2/judge/restic_judge.py` (mirror: `Leagues/restic-v2/judge/restic_judge.py`) |
| Workload generator | `judge/workload_gen.py` (run once; never during a season) |
| RSS launcher | `judge/bin/rusage-launch` (static Go, built by `setup launcher`; sha in `judge/launcher.json`) |
| Isolation proof | `judge/isolation.json` (`verified: true`) |
| Pinned stock | `stock/` = `git -c core.autocrlf=false archive ba802d42b7294c98b62c16d1157ea3e80820c019` (true upstream LF bytes; source_sha256 `d3db8049...fa07`, 1393 files) in `stock.json` |
| Reproducible stock build | `stock-build.json` (built twice, bit-identical: binary `7d39ed17...f054`, code_sha256 `7fe92fc8...eac9`) |
| Toolchain | `toolchain/go` (go1.27.0 linux/amd64), `toolchain/gomodcache` (offline, `go mod verify` clean) |
| Frozen workloads | `workloads/` (corpus-v1, mutation/, manifest-v1/v2, pristine-repo, etc/, password; `chattr +i`) |
| Compatibility baseline | `compat-baseline.json` (stock's own go test results in the sandbox) |
| Broker entry points | `Leagues/tools/remote_broker.py`: `judge_restic(...)`, `research_restic(...)` |
| Calibration evidence | `Leagues/restic-v2/calibration/` (null + sensitivity result JSONs, analysis) |

## What the old league got wrong, and what replaced it

| Old flaw | This judge |
|---|---|
| One pair per match, no statistical gate | 6 paired rounds per match (every `orders()` pattern once); 42 for promotion; wins count + median + bootstrap interval; gates set from a measured 18-round null |
| Phases changed file count, history and concurrency together | Seven phases, each a single restic process on a fixed input, every arm cloned from the same stock-made state; concurrency fixed (GOMAXPROCS = 4 pinned cores), history fixed per phase |
| 1.6-4 s runs dominated by startup and polling noise | Phases run 2.6-7 s (T ~28 s per arm per round); wall clock is taken inside a tiny launcher around one process, with no polling |
| GOGC/GOMEMLIMIT tuning dominated RSS | No GC environment at all (GOGC/GOMEMLIMIT unset); restic's defaults are what is measured |
| Windows desktop scoring | Linux on the league box only; pinned cores in one CCD; tmpfs everywhere |
| Rubric points | Measurement only: time and peak RSS, equal weight, plus a hard correctness gate |
| Correctness limited to regular files | Oracle covers content, modes incl. setuid/setgid/sticky, uid/gid (incl. uid 1000), mtimes (files, dirs, symlinks), symlink targets (relative, absolute, dangling, to-dir, raw-byte), hardlink groups, a FIFO, user xattrs on files and dirs, invalid-UTF-8 / newline / 255-byte / 30-deep names, sparse and empty files |

## Design in one page

* **Arms.** candidate, incumbent, stock (all restic Go module roots). Each arm is snapshotted at the start of the run
  (full file map + copies of files that differ from stock), so live team workspaces can change underneath without effect.
  If the incumbent's build input equals stock's, it is aliased to stock and only two arms are measured (pylint semantics).
* **Trusted build.** Build input = pinned stock tree + the arm's non-test `*.go` under `cmd/` and `internal/`. Nothing else
  from the arm is used. `go.mod`/`go.sum` must equal stock. Build runs offline in bwrap: go1.27.0, read-only GOROOT and
  GOMODCACHE, `GOPROXY=off GOFLAGS=-mod=readonly CGO_ENABLED=0 GOAMD64=v1`, `go build -trimpath -buildvcs=false -tags
  "selfupdate disable_grpc_modules" -ldflags "-s -w" ./cmd/restic`. Stock rebuilt every run must be bit-identical to
  `stock-build.json`. Each binary gets `binary_sha256` and `code_sha256` (ELF build-ID notes zeroed: the build IDs hash
  the source bytes, so a CRLF->LF rewrite changes them without changing one instruction).
* **Skip.** In match mode a candidate whose build input, or whose code_sha256, equals the incumbent's or stock's is not
  measured: `status: "skipped"`, `skip.equivalent_to`.
* **Compatibility subset (correctness/match/reference/promotion).** The STOCK `*_test.go` files of internal/{archiver,
  repository, repository/index, repository/pack, repository/hashing, restorer, checker, data, restic, walker, bloblru,
  backend/local, fs} and the whole `cmd/restic` integration suite, compiled against the arm's code, run as uid 1000 in a
  user namespace, no network. Pass = no failure stock does not also have here, and every stock-passing test passes.
  Stock in this sandbox: 1026 pass / 20 fail / 17 skip; the 20 are environmental (no FUSE for `mount`, no `python`
  binary for `--stdin-from-command`, user-namespace ownership in `fs.TestNodeRestoreAt`). ~45 s per arm.
  Consequence for builders: the internal APIs these stock tests call are frozen.
* **Workloads (frozen, `chattr +i`).** Generated once by `workload_gen.py --seed 20260924` (two independent generations,
  on ext4 and on tmpfs, produced identical manifests). corpus-v1: 91,264 entries, 1.64 GiB (1,761,106,835 bytes):
  90,000 small files in a ~1,000-directory project-like tree (depth <= 5) (log-normal sizes around 2 KiB, 1% empty, 40% random,
  40% compressible text slices, 15% exact duplicates), 160 medium files 256 KiB-8 MiB, three incompressible large files
  (192+192+128 MiB), two dedup-heavy 128 MiB files built from shifted slices of big-0 plus insertions, a 64 MiB sparse
  file, and `special/` (odd names, symlinks, hardlink trio + cross-dir pair, xattrs, uid 1000 file, 0000/4755/2755/1777/
  0700 modes, FIFO, empty/sticky/setgid dirs, a 30-deep path). Mutation set (4,224 declarative ops + payloads): modify 2%
  of small files in place, append to 1%, delete 1%, rmtree one package, rename one package, add 600 files and a 64 MiB
  file, patch 1 MiB inside a large file, truncate a dedup file, chmod 60 files, retarget a symlink, add an xattr.
  v2: 86,595 entries. A frozen pristine initialized repository (stock `restic init`, repo v2, fixed password) gives every
  arm identical config, keys and chunker polynomial.
* **States (per judge invocation, by stock, unmeasured).** corpusA = `cp -a` of corpus-v1 onto tmpfs; corpusB = `cp -al`
  hardlink farm of A with the mutation set applied (every touched file is detached first, so unchanged files share
  inode+ctime with A and a stock snapshot of A is the natural parent for backing up B). S1a = stock backup of A into a
  copy of the pristine repo. Shist = S1a + backup of B (v2, parent v1, ~72k files unmodified) + backup of B with three
  excludes (v3). Expected restore manifests v1/v2/v3 are produced by stock restores. Both copies are verified against
  the frozen manifests before anything else runs.
* **Sandbox for every restic process.** bwrap: new PID/net/IPC/UTS/cgroup namespaces, restic is PID 1 (`--as-pid-1`),
  hostname `league`, capabilities dropped to CHOWN/FOWNER/DAC_OVERRIDE/FSETID, read-only `/`, read-only binary,
  password file, passwd/group and corpus at `/data`, read-write `/work` (repo clone, HOME, TMPDIR, cache, restore
  target). Env: HOME, TMPDIR, GOMAXPROCS=4, TZ=UTC, USER=root and nothing else. Proof: `judge/isolation.json`.

## The seven phases (each ONE restic process, stock defaults, fresh clone of the named state)

| Phase | State cloned | Command | Median seconds (stock) | Median peak RSS (stock) |
|---|---|---|---:|---:|
| backup-initial | pristine repo | `backup /data/src` (corpus A, 90,197 files, 1.64 GiB, no parent) | 5.55 | 194.3 MiB |
| backup-unchanged | S1a | `backup /data/src` (corpus A again, parent = S1a's snapshot; 0 changed) | 3.40 | 130.6 MiB |
| backup-mutated | S1a | `backup /data/src` (corpus B = v2: 10,856 new, 2,292 changed, 72,428 unmodified) | 3.35 | 164.1 MiB |
| restore | S1a | `restore latest --target /work/restore` (full v1 tree onto tmpfs) | 6.91 | 216.6 MiB |
| check-read-data | S1a | `check --read-data` (1.21 GiB stored) | 2.92 | 99.6 MiB |
| forget-prune | Shist (v1, v2, v3) | `forget --keep-last 1 --prune` | 3.42 | 128.9 MiB |
| ls-json | S1a | `ls --json latest` (91k nodes) | 2.61 | 68.2 MiB |
| **T per arm per round** | | | **28.2** | M = 143.2 MiB |

(`-r /work/repo --password-file /etc/restic-password` precede every command. Local repository cache under `/work/home`
starts empty in every phase, i.e. a cold cache.)

## Correctness oracle (checked before any number counts)

* **Warm-up round (unmeasured), full oracle for every arm, before round 1** (and repeated on every resume; stock goes
  first and establishes the references): every phase runs once per arm; for each backup phase stock runs
  `check --read-data` on the arm-written repository and restores the arm's snapshot, and the restored tree must equal the
  expected manifest (v1 or v2); for forget-prune stock runs `check --read-data` on the arm-pruned repository and restores
  it (== v3); for check-read-data the arm must detect a corrupted pack (one flipped byte, non-zero exit). Together with
  the restore phase (arm restores a stock-written repository) this is the cross-reader check in both directions.
* **Every measured round, every arm, every phase:** backup phases - stock `snapshots --json` of the arm's repository:
  snapshot count, parent, paths, host, username and summary counters (files new/changed/unmodified, dirs, data_blobs,
  tree_blobs, data_added, totals) must equal stock's, and the root tree id must equal stock's (a different tree id triggers
  the slow path: stock `check --read-data` + stock restore + manifest equality, so a semantically equal but differently
  serialized tree is not wrongly failed); restore - the restored tree's full manifest must equal stock's restore of the
  same snapshot; check-read-data - exit 0; forget-prune - kept snapshot ids equal stock's, and stock
  `check --read-data-subset=k/5` (k rotates with the round, so every 5 rounds read every pack) must pass;
  ls-json - the parsed JSON lines must equal stock's.
* A single oracle failure aborts the run (`status: "failed"`, `correctness.all_arms_matched_parent_stock_oracle: false`).
* Stock's restore versus the frozen source manifest: identical for v1 and v2 (no fields dropped), recorded per run in
  `correctness.stock_restore_vs_source_differences`.

## Measurement

* **Time:** wall seconds of the sandboxed process, `time.Since` (Go monotonic) inside the launcher from fork to wait4
  return. Includes ~8 ms bwrap setup, identical for all arms. Per round, T = sum over the seven phases.
* **Peak RSS:** `wait4()` `ru_maxrss` of the bwrap monitor whose only child is restic (PID 1 of its namespace). The
  kernel reports max(monitor, restic) and the monitor is ~2 MiB, so it is restic's exact VmHWM at exit, with no sampling.
  The judge itself never forks the measured process: exec() seeds a child's `ru_maxrss` with the forking image's own
  high-water mark (the first probe read 828 MiB for every phase = the Python judge). The tiny static launcher
  (`judge/bin/rusage-launch`, a few MiB) is the only process the judge forks. Probe: allocations of 0/64/300 MiB read
  9.75/73.5/309 MiB. Per round, M = mean over the seven phases of the phase peak.
* **Paired score per round:** time_saved_r = 100(1 - T_cand/T_ref), rss_saved_r = 100(1 - M_cand/M_ref),
  s_r = (time_saved_r + rss_saved_r)/2. Reported: medians over rounds, wins (rounds with s_r > 0), per-axis wins,
  per-phase medians, `pair_changes`, and a 90% bootstrap interval of the median score (4,000 resamples, seed 0).
* **CPUs:** 26-29 = four physical cores of CCD4 (L3 shared by 24-29 and SMT siblings 72-77), GOMAXPROCS=4, affinity set
  on the judge and inherited by every child; the unit's `AllowedCPUs=26-29`. The judge sleeps in wait4 while restic runs.
  Core 24 is a pylint chiplet lane (shares this L3 when pylint judges run concurrently), 25 is a buffer. Research lanes:
  6-9 (CCD1) and 12-15 (CCD2).
* **Pairing:** within a round every phase runs all arms back to back in the `orders()` rotation (rotations + reverses of
  the arm list; six patterns for three arms, two for two arms), so each arm sits equally often in each position.
* **Storage:** tmpfs for corpus, repositories, restore targets, restic TMPDIR and cache; nothing touches disk while timed.
  Peak tmpfs use ~7.7 GB (corpus A 1.76 G + B delta + S1a 1.2 G + Shist 1.35 G + one clone + one restore target).

## Noise calibration (the measured null)

Job `supervisor-jobs/calibration-null` (2026-09-24, 47 min): `--mode reference --calibration`, 18 rounds (three full
`orders()` cycles), candidate = incumbent = stock, all three built separately (bit-identical `8fe492f3...`; same machine
code as the final LF stock `7d39ed17...`, `code_sha256 7fe92fc8...`), measured as three separate arms. Every oracle passed.
Every per-round paired percentage between two identical arms is pure instrument noise; the three pairs give 54 values
(correlated within a round, so each pair is also shown alone). Raw: `calibration/null-result.json`; analysis:
`calibration/analyze_calibration.py` -> `calibration/null-analysis.txt`.

| pair (identical binaries) | time median | time SD | RSS median | RSS SD | score median | score SD | score range |
|---|---:|---:|---:|---:|---:|---:|---|
| candidate-incumbent | -0.023% | 1.107% | +0.526% | 2.761% | +0.015% | 1.539% | -3.95..+2.21 |
| candidate-stock | +0.199% | 1.271% | -0.495% | 1.892% | +0.223% | 1.181% | -2.15..+2.03 |
| incumbent-stock | -0.043% | 1.000% | +0.065% | 2.018% | -0.052% | 0.894% | -1.72..+1.64 |
| **pooled** | **+0.027%** | **1.110%** | **+0.046%** | **2.224%** | **+0.017%** | **1.215%** | |

Per phase (pooled SD of per-round paired %; single-phase numbers are diagnostic, the composite is what gates):

| phase | median s | time SD | median peak RSS | RSS SD |
|---|---:|---:|---:|---:|
| backup-initial | 5.55 | 0.82% | 194.3 MiB | 6.15% |
| backup-unchanged | 3.40 | 2.50% | 130.6 MiB | 7.39% |
| backup-mutated | 3.35 | 4.35% | 164.1 MiB | 5.27% |
| restore | 6.91 | 2.27% | 216.6 MiB | 5.22% |
| check-read-data | 2.92 | 2.98% | 99.6 MiB | 5.93% |
| forget-prune | 3.42 | 4.59% | 128.9 MiB | 4.50% |
| ls-json | 2.61 | 1.25% | 68.2 MiB | 1.73% |

* Peak RSS of a Go program is not deterministic: GC pacing makes the same binary peak 5-7% apart run to run. That is
  part of what is measured (no GC tuning), so the composite M (mean over 7 phases) and the median over rounds absorb it.
* Position: mean T by position in the round order 28.13-28.34 s (<= 0.7%); the rotation balances it.
* Siblings 72-77 and cores 24/25 were 100% idle during the run (mpstat); load average ~2.
* **Standard error of the median score** (1.2533 x SD / sqrt(R)): 6 rounds 0.62%, 12 rounds 0.44%, 42 rounds 0.235%.
  Axis medians at 42 rounds: time 0.22%, RSS 0.43%. A 1-round probe has ~1.2% SD on the score: diagnostic only.

## Sensitivity (the judge sees a known effect of known size)

Job `supervisor-jobs/calibration-sensitivity`: one ordinary 6-round **match** (incumbent = stock, aliased; two arms)
of `calibration-trees/perturbed-restore` = stock + `defer calibrationPerturb()()` in `runRestore` only: +150 ms sleep and
+24 MiB of touched off-heap anonymous mmap held until restore returns. Raw: `calibration/sensitivity-perturbed-result.json`.

| quantity | expected by construction | measured (6 rounds) |
|---|---:|---:|
| restore phase time | -2.2% (0.15 s of 6.9 s) | -2.04% |
| restore phase peak RSS | -11.3% (24 MiB of 216.6) | -13.7% |
| composite time_saved | -0.54% (0.15 s of 27.75 s) | -0.74% |
| composite rss_saved | -2.42% (24/7 MiB of 141.6) | -3.39% |
| score | -1.48% | **-2.61%, 0/6 wins, 90% CI [-3.23, -1.48]** |
| other six phases | 0 | all within their noise band |

Every measured value is within ~1.6 SE of its construction value, and a single match already separates a ~1.5% composite
effect from zero. The same comparison shows that the RSS axis is the noisier one (the expected -2.4% read as -3.4%).

## End-to-end validation on a real change (official path)

`remote_broker.judge_restic(...)` from the workstation, mode `match`, job `3af722e6480348a3bea7ecbea5eaeb5d`
(`calibration/validation-dealer-match-result.json`): candidate = pinned stock + the earlier league's selected buffer-ownership
patch (`internal/archiver/file_saver.go` only; its added `*_test.go` was ignored by design), incumbent = stock (aliased).
Build, compatibility subset (1026/20/17, identical to stock), warm-up full oracle and six rounds all passed.

| | time saved | RSS saved | score wins |
|---|---:|---:|---:|
| composite (score +2.98%, 90% CI [+2.24, +4.12]) | -2.88% | +8.99% | 6/6 |
| backup-initial | -9.33% | +19.75% | 6/6 |
| backup-unchanged | -3.42% | +16.79% | 6/6 |
| backup-mutated | -5.82% | +17.91% | 5/6 |
| restore / check-read-data / forget-prune / ls-json | +0.47 / -0.45 / -1.03 / -0.38% | +0.40 / +2.71 / +0.92 / +0.55% | noise |

The change only touches the backup path, and exactly the three backup phases move (less memory, slower), in the direction
the earlier league reported. The judge localises effects per phase, and the composite rewards the trade only because its
memory gain is three times its time loss; both axes stay inside the 5% cap except backup-initial time, which the cap does
not see (it applies to the composite axis medians).

## Gate derivation and the constants in force

Simulation: resample the 54 null scores (centred), add a true effect delta, apply "wins >= W of R AND median >= floor".

| R | W | floor | false positive | power 0.25% | 0.5% | 0.75% | 1.0% | 1.5% |
|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| 6 | 5 | 0.1 | 9.9% | 0.25 | 0.42 | 0.51 | 0.55 | 0.86 |
| 12 | 10 | 0.1 | 2.1% | 0.10 | 0.26 | 0.36 | 0.42 | 0.85 |
| **42** | **29** | **0.25** | **1.2%** | 0.18 | 0.65 | 0.83 | 0.89 | 1.00 |
| 42 | 30 | 0.2 | 0.5% | 0.12 | 0.51 | 0.72 | 0.81 | 1.00 |
| 42 | 28 | 0.25 | 2.4% | 0.26 | 0.76 | 0.89 | 0.94 | 1.00 |

| constant | value | why |
|---|---|---|
| `MATCH_ROUNDS` | 6 | every `orders()` pattern of three arms once (each arm 2/2/2 per position); a ranked screen with 0.62% SE, not a gate. Match differences under ~1.2% (2 SE) are not distinguishable. |
| `REFERENCE_ROUNDS` | 12 | two cycles; incumbent-vs-stock to +/-0.44% SE for season calibration; never promotes |
| `PROMOTION_ROUNDS` | 42 | seven cycles (each arm 14/14/14 per position); SE 0.235% |
| `PROMOTION_WINS` | 29 | FP 1.2% under the empirical null; power 83% at a true 0.75% score gain, 89% at 1.0%, ~100% at 1.5% |
| `MINIMUM_SCORE_PCT` | 0.25 | ~1 SE above zero, well below the ~1% effect the rule is sized for (INTERFACE's placeholder was 0.1; the win count dominates either way: identical FP/power at 0.1 and 0.25) |
| `AXIS_REGRESSION_CAP_PCT` | 5.0 | operator policy; a null tree cannot trip it (axis-median SE 0.22%/0.43% at 42 rounds, > 11 SE away) |
| must beat stock on score | `candidate_vs_stock.score_pct > 0` | INTERFACE |
| distinct | build input AND machine code (`code_sha256`) differ from incumbent and stock | INTERFACE + CRLF lesson |

All are env-overridable (`RESTIC_JUDGE_MATCH_ROUNDS`, `..._REFERENCE_ROUNDS`, `..._PROMOTION_ROUNDS`, `..._PROMOTION_WINS`,
`..._MIN_SCORE_PCT`, `..._AXIS_REGRESSION_CAP_PCT`) for experiments; official runs use the defaults. A promotion
result is eligible only when every reason list is empty; `eligibility.reasons` says exactly which gate failed.

## Runtime per mode (measured)

| mode | rounds | 2 arms (incumbent == stock, e.g. S001) | 3 arms |
|---|---:|---:|---:|
| fixed cost (builds, compat tests, states, warm-up + full oracle, tamper) | - | ~6 min (44 s + 50 s + 67 s + 198 s + 6 s) | ~9 min (66 + 88 + 68 + 299 + 6 s) |
| probe (no compat tests) | 1 | ~7 min | ~10 min |
| correctness (build + compat + full oracle, no timing) | 0 | ~6 min | ~9 min |
| match | 6 | ~15 min | ~22 min |
| reference | 12 | ~23 min | ~35 min |
| promotion | 42 | ~66 min | ~98 min |

Per round: ~85 s with two arms, ~128 s with three (21 measured restic runs ~84 s + clones + per-round oracle).
Memory: the unit peaked at 9.6 GB (tmpfs ~7.7 GB + page cache + judge); the broker caps the unit at 14 GiB with a
10 GiB tmpfs.

## How to run it

**From the controller (automatic):** `remote_broker.judge_restic(candidate, incumbent, mode, labels)` for official jobs
(CPUs 26-29, boxagents.slice) and `remote_broker.research_restic(candidate, incumbent, 'probe'|'correctness', labels)`
for builders (lanes 6-9/12-15, resticresearch.slice, diagnostic only). Both recover an interrupted controller: the same
labels/mode/trees reuse a finished result or resume the job (`--resume`, checkpointed rounds are kept).

**By hand after any match or post-season (on the league box, as root):**
```bash
R=/opt/league/restic-league-v2
W=$R/supervisor-jobs/manual-$(date -u +%Y%m%dT%H%M%S); mkdir -p $W/evidence $W/scratch
systemd-run --unit=restic-v2-manual-$(basename $W) --slice=boxagents.slice --collect \
  --setenv=RESTIC_JUDGE_SCRATCH=$W/scratch --property=AllowedCPUs=26-29 --property=MemoryMax=14G \
  --property=MemorySwapMax=0 --property=TasksMax=4096 --property=TemporaryFileSystem=$W/scratch:rw,size=10G \
  --property=StandardOutput=append:$W/supervisor.log --property=StandardError=append:$W/supervisor.log \
  /usr/bin/python3 $R/judge/restic_judge.py --candidate /path/to/candidate --incumbent /path/to/incumbent \
  --stock $R/stock --mode promotion --out $W/evidence/result.json --status $W/evidence/status.json --cpus 26-29 \
  --labels '{"season": "S001", "why": "post-season confirmation"}'
watch cat $W/evidence/status.json          # units_done/units_total, stage; liveness = systemctl is-active
```
Or directly in a root shell (uses /dev/shm for scratch): `python3 $R/judge/restic_judge.py --candidate ... --incumbent
... --stock $R/stock --mode match --out /some/dir/result.json`. Re-run the same command with `--resume` after any kill:
builds, compat receipts, the full-oracle receipt and every completed round are reused; states are rebuilt and the warm-up
re-run on every invocation. `calibration/launch_calibration.sh NAME MODE CANDIDATE [--calibration]` is the same launcher
used for the calibration runs. Evidence per run: `<out>-evidence/` (config.json, builds/, compat/, sources/,
warmup-NN.json, round-NN.json, expected-restore-v*.json, events.jsonl, invocation-NN.json).

**Re-deriving the gates** (after any change to the judge, workloads, box, or CPUs): run a null with
`ROUNDS=18 calibration/launch_calibration.sh null reference $R/stock --calibration`, copy its result.json locally, run
`python calibration/analyze_calibration.py null-result.json [sensitivity-result.json]`, and set the constants above.

**One-time setup commands** (already done; listed for audit): `setup stock-build` (two bit-identical builds ->
stock-build.json), `setup launcher`, `workload_gen.py --out workloads`, `setup pristine`, `setup compat-baseline`,
`setup isolation`.

## Known limits and open items

* The workload is local-filesystem only (tmpfs); no cloud backend, network or disk-latency behaviour is measured.
* Cold repository cache in every phase (restic's default cache dir starts empty); warm-cache incremental backups are not
  a separate phase.
* Peak RSS noise (5-7% per phase) is intrinsic to Go GC; the composite and 42-round medians absorb it, but single-phase
  and single-round numbers are noisy.
* The boxagents.slice cap is 16 GiB and it held 5-12 GiB of other load while calibrating; one official restic judge
  (~9.6 GB peak incl. reclaimable cache) fits, two concurrent ones may not. Research jobs run in the separate transient
  resticresearch.slice (14 GiB cap per unit), outside the box manager's accounting.
* No pprof/profile mode yet (builders get probe + correctness only).
* Device nodes are not in the corpus (need CAP_MKNOD); everything else restic stores is.
