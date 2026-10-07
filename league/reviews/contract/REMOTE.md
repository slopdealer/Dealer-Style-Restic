# Remote-backend workload for the restic judge (`remote`): built 2026-10-04, profile v2 DEPLOYED 2026-10-05 for S019

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Question: every measurement so far used a local repository on tmpfs. Do the league's gains (about +26% time vs stock on
`main`) survive when the repository is behind a network? This adds a fourth workload, `remote`: main's corpus, mutation
set, phases and oracle, with the repository reached through restic's REST backend, served by our own minimal REST
server with a simulated network. Build log: `remote-workload/PROGRESS.md`. Measurements: section 6.

**Deployed at the S018/S019 boundary, 2026-10-05 (section 12: judge `a770cec8`, null, reference, canary, the
socket-path fix).** The paragraphs below up to section 11 are the build record as written on 2026-10-04, when
nothing was deployed. Then: the judge change is in the local `restic_judge_fastpath.py` (sha256 in section 8; the box
judge is still `b5e4b9e1`), the server is `judge/restserver/main.go`, the frozen workload dir
`/opt/league/restic-league-v2/workloads-remote/` exists on the box and is inert until a judge that knows the
`remote` block runs with `--workload remote`. A scratch judge copy and all job evidence live in
`/opt/league/restic-remote-dev/`.

**Status after the second pass (2026-10-04, coordinator decisions).** `remote` becomes the FOURTH SCORED suite
workload from S019, with **profile v2: 30 ms per request, 48 MiB/s per connection, 5 connections** (section 2). The
frozen dir on the box is now version `restic-v2-workload-remote-2`. Sections 5 and 6 are the measurements of
**profile v1** (12 MiB/s) and stay as the record of why the profile changed; the v2 numbers are in section 9, the
four-workload suite in section 10, and the ordered boundary steps in **section 11 (S019 boundary checklist)**.
The builders' harness with `remote` support is staged beside the live one; controller support is behind config.

## 1. Design

| decision | what | why |
|---|---|---|
| backend | restic's REST backend, `-r rest:http+unix:///work/rest.sock:/` | The pinned restic (`ba802d42`) registers `peterbourgon/unixtransport` on its HTTP transport (`internal/backend/http_transport.go`), so `http+unix://` works with no code or module change. |
| route into the sandbox | a unix socket file in the phase work dir, which is already bound at `/work` | The sandbox keeps `--unshare-net`: its loopback is its own, so host TCP is unreachable, and nothing about the box's network is touched. The sandbox command differs from main's in ONE argument (the `-r` value). |
| server | `judge/restserver/main.go`, Go standard library only, HTTP/1.1, compiled once with the frozen toolchain, frozen as `workloads-remote/bin/restserver` (sha256 in `workloads.json`, checked by `workload_identity`) | No downloads, no new modules; same pattern as `ballastgen`. |
| server lifetime | one server per measured restic process; the judge starts it before the clock and stops it (SIGTERM) after | Start and stop are outside the measured wall time. Its statistics file is the per-phase request log. |
| storage | a local-layout repository in the unit's tmpfs at `<scratch>/work/<arm>.remote-store`, OUTSIDE the sandbox's `/work`; after the server has stopped it is renamed to `<work>/repo` | The measured restic cannot open the repository as files (it only has the socket). The oracle is unchanged: stock reads `/work/repo` as a local repository. |
| states | S1a / Shist and the expected restore manifests are built by stock on the LOCAL path, exactly as main | Unmeasured; identical bytes; saves ~2 min of simulated transfer per job. Research bundles and their cache keys work as for any named workload. |
| network | simulated inside the server with sleeps (never a busy wait, no `tc`/netem) | Other jobs share the machine. Deterministic: a fixed delay and a byte schedule, no randomness. |
| server CPUs | on the lane (inherits the judge unit's cpuset, which IS the lane) | The unit has no other CPUs. Measured cost: section 5. |
| memory | peak RSS is restic's only | The server is not a child of the launcher's bwrap monitor. |

**Protocol implemented** (what restic at the pin calls, `internal/backend/rest/rest.go`): `POST /?create=true`;
`HEAD|GET|POST|DELETE /config`; `GET /{data,keys,locks,snapshots,index}/` (v2 list `[{"name","size"}]` when the
request accepts `application/vnd.x.restic.rest.v2`, v1 names otherwise); `HEAD|GET|POST|DELETE /{type}/{name}` with
`Range: bytes=a-` / `bytes=a-b` (206/416 by `net/http`). `POST` refuses to overwrite (403), as rest-server does. Files
are stored where restic's local backend stores them (`data/<2 hex>/<name>`, other types flat), mode 0400, directories
0700.

**Proof with restic's own behaviour** (stock binary, outside the judge, real profile; `remote-workload/tools/smoke_inner.sh`):
`init`, `backup` x3, `check --read-data`, `forget --keep-last 1 --prune`, `restore`, `ls --json`, `snapshots` all exit 0
through the server; the server-written repository has the same directory set as a `restic init` local repository
(root + `data/00..ff` + `index keys locks snapshots`), the same modes and owners (files 0400, directories 0700,
root:root); stock opens it as a LOCAL repository and `check --read-data` finds no errors; the restore is identical to
the source. Inside the judge, every warm-up and round oracle of the three jobs in section 6 passed (stock reads every
arm-written repository locally: snapshots, `check --read-data`, cross restores).

## 2. The frozen profile (part of the workload identity)

`workloads-remote/workloads.json` → `"remote"`:

| key | value | meaning |
|---|---|---|
| `latency_ms` | **30** | fixed first-byte delay of every request, slept before the request is looked at |
| `rate_bytes_per_second` | **50,331,648** (48 MiB/s; v1 was 12,582,912 = 12 MiB/s) | per connection, request and response bodies; byte n is not delivered before `start + n/rate` (a schedule, so sleep overshoot does not accumulate; 64 KiB steps) |
| `max_connections` | **5** | requests in flight at once; more queue (FIFO) before their latency starts |
| `server_sha256` | `5817b11d36577b107cf96df19771edc6862b07af2a5a34008b620bd6116d53ba` | the frozen server binary |
| `server_source_sha256` | `60f5fbb827dd3bb559a99cba4c8a837416713f1c281f87b49cb4e939003e6d1f` | `restserver/main.go` |

Version **`restic-v2-workload-remote-2`** (generated 2026-10-04 13:21:51Z; `workloads.json` sha256 `7e4a1bd7...`).
About 400 Mbit/s per connection, 30 ms per round trip, at most 240 MiB/s over the five connections.

**Why v2 (coordinator, 2026-10-04).** v1 (`restic-v2-workload-remote-1`, 12 MiB/s) put 73% of a pass on a transfer
floor no code change can touch, made a round 5.4 minutes long and a saved second worth half of what it is on main. At
48 MiB/s the floor is a quarter of that (17.4 s of a 44 s stock pass = 39%, section 9), so latency x request count,
connection use and CPU are the terms a team can move, and a round is about half as long. A profile change is a new
workload version: the version string is in the workload identity and in every research cache key, so v1 and v2
results are never pooled. The v1 dir was moved to `restic-remote-dev/workloads-remote-v1-retired` (inert).

**The connection limit stays** (5 = restic's own REST default, `rest.connections`): stock never queues, and without
it a team could multiply its throughput by raising one constant in `internal/backend/rest/config.go`.

Not simulated: TLS, HTTP/2, loss, jitter, per-request server work (listing cost), a slow start.

## 3. Judge changes (all behind the workload's `remote` block)

* `workload_settings()` gains `remote` (None for a workload without the block).
* `restic_sandbox(..., repository="/work/repo")`: the default produces the same argv as before.
* `Judge.restic(..., remote_store=None)`: with a store, starts the server on `<work>/rest.sock`, runs the launcher,
  stops the server, attaches its statistics as `run["remote"]` (so every warm-up and round row carries them).
* `Judge.run_phase`: a remote workload copies the state into the store, measures through the server, then renames the
  store to `<work>/repo` for the oracle. Other workloads run the previous two lines unchanged.
* `workload_identity()`: for a remote workload, checks the server sha and adds the `remote` block.
* Results: `remote_backend` (`profile`, `arms.<arm>.<phase>`: median requests, bytes in/out, latency seconds, queue
  seconds, server CPU, and the first round's table by method and type) only when rows carry remote statistics; suite
  results get `remote_backend.<workload>`.
* **The warm-up's corrupted-pack check goes through the server** on a remote workload (second pass): the corrupted
  copy of S1a is the server's storage (`<scratch>/work/corrupt-<arm>.remote-store`) and the arm runs
  `check --read-data` against `rest:http+unix://...`, so it must detect the bad pack through the backend it is
  measured on. Every other workload makes the same call as before (`self.restic(arm, [...], work, None,
  "corrupt-check")` on `work/repo`); a test compares both paths.
* Not routed through the server: state building and every oracle READ by stock (stock opens what the arm wrote as a
  local repository).

With `--workload main` (the default), `big-index`, `large-files` and their suites nothing changes: no server, the same
sandbox argv, the same phase path, the same result keys. Tests (`Leagues/tests/test_remote_workload.py`, 15) compare
the sandbox argv, constants, `orders()`, `suite_schedule()`, phase tables, `compare()` and `compare_suite()` with the
deployed judge (`tests/fixtures/restic_judge_b5e4b9e1.py`), and drive `run_phase` both ways. As with any judge edit,
research caches miss once after a deploy (keys hold the judge sha).

## 4. Generate, deploy, add to the suite

Generate (v1 on 2026-10-04 11:34Z; **v2 on 2026-10-04 13:21Z**, the generator's defaults are now the v2 profile and
version; a deployed workload is never regenerated, a new profile is a new version):

```
R=/opt/league/restic-league-v2; D=/opt/league/restic-remote-dev
# 1. server: judge/restserver/main.go + a 3-line go.mod (module restserver, go 1.25), frozen toolchain, offline
(cd $D/src/restserver && GOROOT=$R/toolchain/go GOTOOLCHAIN=local GOFLAGS=-mod=readonly GOPROXY=off CGO_ENABLED=0 \
   $R/toolchain/go/bin/go build -trimpath -buildvcs=false -ldflags '-s -w' -o $D/bin/restserver .)
# 2. workload dir: byte copies of main + bin/restserver + the profile (run next to a judge copy named restic_judge.py)
python3 workload_gen.py --profile remote --out $R/workloads-remote --server-bin $D/bin/restserver \
   --server-source $D/src/restserver/main.go [--latency-ms 30 --rate-mib 48 --max-connections 5]
```

Deploy: **superseded by section 11** (the S019 boundary checklist). The first-pass notes, for the record:

1. Copy the local `restic_judge_fastpath.py` to the box, put its sha256 in `deploy_fastpath.py`, run it (as SUITE.md
   step 1). `--workload remote` then works for official and research jobs.
2. Controller: add `'remote'` to `VISIBLE_SUITE_WORKLOADS` and a one-line description in `tools/league_targets.py`
   (the suite validator refuses any other name today), sync `tools/` to the box.
3. Suite: `workload_suite: ['main', 'big-index', 'large-files', 'remote']`. Rounds of a 4-workload suite:
   probe 4, match 8, reference 12, **promotion 44** (the mode's rounds rounded up to a multiple of 4).
4. Seal the dir if wanted (`chattr -R +i`), as main.

## 5. What a job costs at PROFILE V1 (measured on the three jobs of section 6; v2: sections 9 and 10)

| | remote | main (for scale) |
|---|---|---|
| one stock pass (7 phases) | **94.5 s** (SD 0.29 s over 36 passes) | 22.2 s |
| one round, 3 arms / 2 arms | 5.4 min / 3.5 min | ~1.6 min / ~1.1 min |
| 12-round reference job, 3 arms / 2 arms | **77 min / 50 min** (first round after 12 / 8 min of build, compat, states, warm-up) | |
| unit memory peak (10 GiB tmpfs, 14 GiB cap, as the broker gives a single-workload job) | **9.0 GiB** (all three jobs) | ~9.6 GB |
| server CPU per pass | 3.7-3.9 s (1.0-1.2 s in each bulk phase; restic itself 43-45 s) | none |

* **Server on the lane.** In the bulk phases the server uses 4-5% of one core, 1.1% of the lane's four; restic uses under
  one core on average there (17.8 CPU-s in 23.5 s) because it is waiting for the link. Both arms pay the same. The null
  (section 6.1) is the evidence that it does not disturb the clock.
* **A 4-workload suite** (not run): tmpfs `10 + 7 x 3 = 31 GiB`, unit memory 35 GiB by the broker's formula (the
  3-workload cgroup peak was 22.3 GiB; remote adds one more set of states, ~4.5 GB, so expect ~27 GiB). Promotion =
  44 rounds, 11 per workload: about `11 x (1.6 + 2.2 + 1.3 + 5.4) + setup` = **~140 min against 89 min today**
  (job `582b1263`); an 8-round match ~50 min against 30. Three official suite jobs side by side would need ~81 GiB.
* A research probe on `remote` was not run; from the round times expect ~7 min warm (2 arms, 1 round, candidate
  warm-up with full oracle) against ~2 min on main.

## 6. Measurements at PROFILE V1 (2026-10-04, scratch judge `426b7704`, research lanes, 12 rounds each, `--mode reference`)

| job | lane | arms | rounds | wall | oracle units | evidence |
|---|---|---|---:|---|---:|---|
| `null` | 6-9 | stock x3 (`--calibration`) | 12 | 11:34:39-12:51:47Z | 281/281 | `Leagues/evidence/restic-remote-20261004/null/` |
| `s017` | 12-15 | S017 / S016-racefix / stock (`--calibration`) | 12 | 11:34:39-12:50:28Z | 281/281 | `.../s017/` |
| `merge` | 19-22 | `restic-merge-s017/final` / stock | 12 | 11:34:39-12:24:46Z | 188/188 | `.../merge/` |

Every oracle passed in every job (compat subset, warm-up full oracle with stock reading each arm-written repository
locally, per-round oracle). Each folder holds `result.json` (the judge's result, with `remote_backend`),
`analysis.md` (all tables below plus the per-kind request tables for every arm) and `analysis.json`; raw round files
stay on the box under `/opt/league/restic-remote-dev/jobs/<job>/evidence/result-evidence/`.
Tool: `remote-workload/tools/analyze.py`.

**Season overlap (coordinator order KEEP, 11:37Z).** No season was alive at launch. S018's controller was first seen
at **12:02:47Z**. Rounds completed before it: null 1-3, s017 1-3, merge 1-5. Rounds that overlapped the live season:
**null 4-12 (12:08:07-12:51:41Z), s017 4-12 (12:07:39-12:50:22Z), merge 6-12 (12:03:34-12:24:41Z)**. Each job held
its lane's lock file (`shared/cpus-<lane>.lock`, the files the deployed judge and the research tools take), so season
jobs queued or took another lane; 42-45 and 2-5 were never used. The null reads 0.19% per round before the season (9
values) and 0.37% during it (27 values).

### 6.1 The null (stock vs stock, 36 identical-arm pairs)

| | remote | main official lanes (NULL-20261003) | main research lane, robust |
|---|---:|---:|---:|
| per-round paired time SD | **0.33%** (robust 0.30%; median -0.02%; range -0.91..+0.76) | 0.27% | 0.34% |
| per-round paired peak-RSS SD | 2.44% | 2.48% | |
| SE of the median time: 6 / 12 / 42 rounds | 0.17 / 0.12 / 0.06% | 0.14 / 0.10 / 0.05% | |

**The sleeps do not make the whole-workload clock worse: 0.33% against main's 0.27% (1.2x), no stalled run in 252.**
A stock pass is 94.51 s, SD 0.29 s. Per phase it is different: the phases that are pure schedule are far quieter than
on main, the two that upload a handful of packs are noisier, because when each pack fills and which connection it
gets decides whether uploads overlap.

| phase | stock s | per-round paired time SD | main's SD | absolute SD | requests | MiB moved |
|---|---:|---:|---:|---:|---|---:|
| backup-initial | 23.52 | 0.82% | 0.61 | 0.15 s | 88-89 | 1248.5 |
| backup-unchanged | 3.93 | 0.32% | 0.53 | 0.01 s | 17 | 15.9 |
| backup-mutated | 4.79 | **4.59%** | 0.92 | 0.14 s (4.52-5.07) | 23 | 103.3 |
| restore | 26.98 | **0.06%** | 0.51 | 0.01 s | 89 | 1245.4 |
| check-read-data | 23.68 | 0.81% | 0.75 | 0.18 s | 90 | 1248.4 |
| forget-prune | 8.98 | **2.47%** | 1.19 | 0.16 s | 120-121 | 312.7 |
| ls-json | 2.60 | 0.10% | 0.27 | 0.002 s | 14 | 9.9 |

Request counts and bytes are the same in every one of the 36 stock passes (one request of difference in two
phases). Peak-RSS SD per phase: 6.4 / 6.4 / 4.1 / 5.3 / 7.5 / 3.4 / 1.2%, as on main. Probe bands for this workload
would need its own per-phase values (time, rounded up: 0.9 / 0.4 / 5.0 / 0.1 / 0.9 / 2.8 / 0.15; whole workload 0.4).

### 6.2 Do the local gains survive? No: about half the seconds, an eighth of the percentage.

| comparison | remote: time saved, 90% interval, wins | remote seconds | local main (job `582b1263`, 14 rounds) |
|---|---|---|---|
| S017 vs stock | **+3.25% [+3.03, +3.51], 12/12** | 94.77 -> 91.52 (-3.1 s) | +27.61%, 22.21 -> 16.08 (-6.1 s) |
| S016-racefix vs stock | **+3.86% [+3.49, +4.57], 12/12** | 94.77 -> 90.81 (-3.7 s) | +26.2% (canary), 22.21 -> 16.45 (-5.8 s) |
| S017 vs S016-racefix (last season's gain) | **-0.63% [-1.27, -0.43], 2/12: S017 is slower** | 90.81 -> 91.52 | +2.18%, 14/14 |
| merged tree (`restic-merge-s017/final`) vs stock | **+4.28% [+3.98, +4.48], 12/12** | 93.14 -> 89.07 (-4.1 s) | not measured here |

Peak memory survives whole: S017 -46.0%, S016-racefix -45.7%, merged tree -45.2% against stock (166 -> 90 MiB mean
of phase peaks; local main -42.7%). Memory is restic's own and does not depend on the link.

**S017 vs stock per phase** (remote medians of 12 rounds; local = the S017 promotion job's main rounds):

| phase | stock s | S017 s | time saved | 90% interval | wins | remote s saved | local: stock -> S017 | local s saved | peak MiB stock / S017 | requests stock / S017 |
|---|---:|---:|---:|---|---:|---:|---|---:|---|---|
| backup-initial | 23.57 | 22.98 | +2.55% | [+2.06, +3.37] | 12/12 | 0.59 | 5.47 -> 5.06 (+7.3%) | 0.41 | 203 / 101 | 89 / 89 |
| backup-unchanged | 3.93 | 3.32 | +15.32% | [+15.26, +15.41] | 12/12 | 0.61 | 2.22 -> 1.82 (+18.1%) | 0.40 | 132 / 68 | 17 / 17 |
| backup-mutated | 4.76 | 4.23 | +10.57% | [+9.58, +11.16] | 12/12 | 0.53 | 2.34 -> 1.91 (+18.3%) | 0.43 | 171 / 81 | 23 / 23 |
| restore | 26.90 | 26.88 | **+0.11%** | [-0.06, +0.67] | 8/12 | 0.02 | 5.93 -> 3.27 (+44.8%) | **2.66** | 301 / 131 | 89 / **444** |
| check-read-data | 23.59 | 23.28 | +1.15% | [-1.49, +2.25] | 7/12 | 0.31 | 2.53 -> 1.76 (+30.3%) | 0.77 | 140 / 90 | 90 / 90 |
| forget-prune | 9.35 | 8.96 | +4.77% | [+3.48, +6.25] | 12/12 | 0.39 | 2.23 -> 1.35 (+39.5%) | 0.88 | 146 / 105 | 119 / 141 |
| ls-json | 2.59 | 1.85 | +28.79% | [+28.63, +28.84] | 12/12 | 0.74 | 1.47 -> 0.90 (+39.0%) | 0.57 | 68 / 58 | 14 / 14 |

* **Kept:** the three backups and `ls-json` keep all of their seconds (0.5-0.75 s each, more than locally). That is the
  per-process start-up gain (key derivation, lock wait, index load), which no link slows down.
* **Lost:** `restore` loses everything (2.66 s locally, 0.02 s here), `check-read-data` and `forget-prune` keep less
  than half. Their local gains were CPU and tree work inside phases that are now waiting for 1.25 GiB at 60 MiB/s.
* **Reversed:** S017's own step (restore +10% over S016-racefix locally) is **-1.6% on remote restore, 0/12**
  (26.46 -> 26.88 s): it issues 444 requests where S016-racefix issues 408 and stock 89.

**S017 vs S016-racefix per phase** (does last season's gain show up?): backup-initial -0.25% (4/12), backup-unchanged
+0.54% (12/12), backup-mutated -0.93% (5/12), restore **-1.60% [-1.74, -0.97] (0/12)**, check-read-data -0.08%
(6/12), forget-prune -1.88% (4/12), ls-json -0.40% (1/12). Under the suite's per-workload gate (-1.0% vs the
incumbent) S017 would have passed on this workload, by 0.4 points.

**Merged tree vs stock per phase:** backup-initial +2.10% (23.48 -> 22.91), backup-unchanged +15.60% (3.93 -> 3.31),
backup-mutated +12.62% (4.73 -> 4.16), restore +1.95% [+1.22, +2.03] (26.87 -> 26.35; 457 requests against 89),
check-read-data +2.41% (23.59 -> 23.04), forget-prune +7.41% (7.89 -> 7.38), ls-json +29.33% (2.59 -> 1.83); 12/12 in
every phase; peak MiB 99 / 67 / 82 / 133 / 88 / 98 / 58 against stock's 200 / 133 / 171 / 305 / 133 / 132 / 67. The
forget-prune seconds differ between jobs (7.9 / 9.0 / 9.4 s for stock) because each job builds its own history state;
within a job every arm starts from the same one.

### 6.3 Requests and bytes per phase, per arm (medians; what teams would aim at)

| phase | stock: requests, MiB up / down | S016-racefix | S017 | merged tree |
|---|---|---|---|---|
| backup-initial | 89, 1248.5 / 0 | 88, 1248.4 / 0 | 89, 1248.4 / 0 | 89, 1248.4 / 0 |
| backup-unchanged | 17, 6.1 / 9.9 | 17, 6.1 / 9.9 | 17, 6.1 / 9.9 | 17, 6.1 / 9.9 |
| backup-mutated | 23, 93.5 / 9.9 | 23, 93.5 / 9.9 | 23, 93.5 / 9.9 | 23, 93.5 / 9.9 |
| restore | **89**, 0 / 1245.4 | **408**, 0 / 1245.4 | **444**, 0 / 1245.4 | **457**, 0 / 1245.4 |
| check-read-data | 90, 0 / 1248.4 | 90, 0 / 1248.4 | 90, 0 / 1248.4 | 90, 0 / 1248.4 |
| forget-prune | 119, 153.7 / 179.6 (s017 job); 109, 119.2 / 137.6 (merge job) | 141, 153.7 / 176.0 | 141, 153.7 / 176.0 | 124, 119.2 / 134.6 |
| ls-json | 14, 0 / 9.9 | 14, 0 / 9.9 | 14, 0 / 9.9 | 14, 0 / 9.9 |
| **one pass** | **441 requests**, 1502 / 2703 MiB | 782 | 818 | 814 (merge job: stock 430) |

No arm moves fewer bytes than stock in any phase; the league's trees differ from stock only in request count (more,
in restore and prune) and in how many connections they keep busy.

**What a phase is made of** (stock, merge job; connection-seconds = time the link is occupied, summed over
connections; floor = body seconds / 5 connections):

| phase | wall s | body conn-s (bytes / rate) | floor s | latency conn-s (requests x 30 ms) | restic CPU s | mean connections busy (of 5) | dominated by |
|---|---:|---:|---:|---:|---:|---:|---|
| backup-initial | 23.48 | 104.0 | 20.8 | 2.7 | 17.8 | 4.55 | **transfer rate** (floor = 89% of the wall) |
| backup-unchanged | 3.93 | 1.3 | | 0.5 | 3.7 | 0.47 | **local CPU**, plus a serial chain of 17 round trips and 16 MiB (~1.8 s) |
| backup-mutated | 4.73 | 8.6 | 1.7 | 0.7 | 4.3 | 1.97 | local CPU and seven pack uploads |
| restore | 26.87 | 103.8 | 20.8 | 2.7 | 9.2 | **3.96** | **transfer rate**, and 6 s above the floor because a fifth of the link idles |
| check-read-data | 23.59 | 104.0 | 20.8 | 2.7 | 5.0 | 4.53 | **transfer rate** |
| forget-prune | 7.89 | 21.4 | 4.3 | 3.3 | 2.8 | 3.15 | mixed: transfer, **109 round trips** (40 of them deletes), CPU |
| ls-json | 2.59 | 0.8 | | 0.4 | 1.5 | 0.48 | **local CPU**, plus a serial chain of 14 round trips and 10 MiB (~1.2 s) |

The transfer floor is 68.4 s of stock's 93.1 s (**73%**): at this profile no code change can touch it without moving
fewer bytes. One second saved is worth 1.06 points on this workload (main: 4.5).

**Where the reachable seconds are** (read off the tables above; none prototyped):

1. **Restore: keep five connections busy with few requests** (up to ~5.5 s, 6% of the pass). Stock restores with 76
   pack GETs at 3.96 connections busy and sits 6 s above the floor. The league's trees get to 4.4-4.5 busy but pay for
   it with 320-370 extra ranged GETs (10-11 connection-seconds of latency), so they land within 0.5 s of stock.
   S017's restore step added 36 requests over S016-racefix and lost 0.42 s here.
2. **check-read-data and backup-initial: the last half connection** (~2.5 s and ~2 s). Both run at 4.5 of 5; the gap
   is the start (nothing moves until the first pack is ready or the index is in) and the tail.
3. **The control chain every process pays** (~0.6 s x 7 phases). 13 requests one after another before any work:
   `HEAD config`, `GET config`, `GET keys/`, `GET keys`, `GET locks/` twice, `POST` and `DELETE locks`, `GET index/`,
   two `GET index` (3.5 MiB), `GET snapshots/`, `GET snapshots`. Round trips that could overlap, or not happen twice.
4. **One 6.3 MiB tree pack fetched as one request on one connection** in backup-unchanged, backup-mutated and
   ls-json: 0.53 s while four connections idle. Split over the connections it is ~0.15 s.
5. **forget-prune's deletes**: 30-33 data, 5 index, 2 snapshot deletes, 30 ms each; the league's trees issue 141
   requests against stock's 119 on the same state.

## 7. Decisions (settled by the coordinator 2026-10-04; the first-pass list is kept below)

| # | question | decision | where |
|---|---|---|---|
| 1 | profile | **v2: 30 ms, 48 MiB/s per connection, 5 connections**; new version string | section 2 |
| 2 | connection limit | stays at 5 in the server | section 2 |
| 3 | scored or report-only | **scored from S019**, fourth equal-weight suite workload; the per-workload memory guard and the -1% time floor apply to it | section 10 |
| 4 | server placement | stays on the measured lane; no broker change | section 5 |
| 5 | memory and lanes | 31 GiB tmpfs / 35 GiB cap per four-workload job (broker formula, unchanged); two official lanes recommended | section 10 |
| 6 | controller side | built behind config (`workload_suite`, `probe_workload_pair_sd_pct`) | section 10 |
| 7 | warm-up gap | closed: the corrupted-pack check goes through the server on this workload | section 3 |

First-pass list (2026-10-04, before the decisions):

1. **The profile.** At 30 ms / 12 MiB/s / 5 connections, 73% of the workload is a floor nobody can optimise, so the
   score moves ~1 point per second and the three bulk phases reward one thing (link occupancy). A faster link (say
   48 MiB/s per connection: floors of ~5 s) would let CPU and round trips weigh several times more; a longer latency
   (100 ms) would make request count the main term (S017's 355 extra restore requests would cost it ~5 s against stock instead of drawing).
   Changing it means a new workload version (`remote-2`) and a new null.
2. **The connection limit of 5** is my addition (section 2). Without it one constant in the REST config multiplies
   throughput. If the operator wants connection count to be a free lever, set `max_connections` high and add an
   aggregate link rate instead (not built).
3. **Scored or report-only.** As a fourth equal-weight suite workload it is a quarter of the score with a ~27%
   ceiling, costs +60% promotion time and ~5 GiB per job, and S017 (promoted at +1.93%) would have scored
   `(2.18 + 3.72 - 0.10 - 0.63) / 4` = +1.29%. Report-only (one 12-round `reference` job on `--workload remote` per
   season boundary, 77 min on one lane) costs nothing during a season and still shows whether a season's gain holds
   behind a network.
4. **Server placement.** It runs on the measured lane (section 5). Giving it its own CPUs needs the broker to add them
   to the unit's `AllowedCPUs` and the judge to pin restic and the server apart; the null says it is not needed.
5. **Memory and lanes for a 4-workload suite**: ~27 GiB per official job; three in parallel (S018's
   `judge_parallel_lanes` 3) do not fit beside research. Two lanes, or `remote` as its own job.
6. **Controller side is not touched**: `VISIBLE_SUITE_WORKLOADS`, the workload description teams read, probe bands for
   this workload (section 6.1) and the broker's suite memory formula would all need the fourth name.
7. Not routed through the server: the warm-up's corrupted-pack check (the arm reads a local repository there). A tree
   that verified packs differently by backend would not be caught by it; the measured `check --read-data` exit code and
   stock's own checks of every arm-written repository are.

## 8. Files

* Judge: `restic-v2/judge/restic_judge_fastpath.py`, sha256
  **`a770cec81f9b2344449fc6d4cfd27cface44bc0c8019042340163dc308a22e21`** (raw bytes, CRLF), +102 lines against
  `b5e4b9e1` (first pass `426b7704`, +85; second pass `74723d34`: the corrupted-pack check; 2026-10-05 `a770cec8`: the
  server is started with cwd = the work dir and the relative socket name, section 12). `deploy_fastpath.py` pins it.
* Server: `restic-v2/judge/restserver/main.go` (sha256 `60f5fbb8...`), binary `workloads-remote/bin/restserver`
  (`5817b11d...`); unchanged by profile v2 (the profile is arguments, read from `workloads.json`).
* Generator: `restic-v2/judge/workload_gen.py --profile remote` (sha256 `b86d347f...`; v1's was `36387321...`).
* Builders' harness: `restic-v2/research-tools/research_tools_v2.py`, sha256 `998612d135e786e173e0bacc8239fa8b11fb6ea96f7f4eb20c8d154cae5595e3`;
  STAGED on the box as `research-tools/research_tools_v2_remote.py` (same bytes). The live
  `research-tools/research_tools_v2.py` is untouched (`e19a384c...`). Contract: `research-tools/CONTRACT.md`, section
  "Workload `remote`".
* Controller: `tools/league_targets.py` (`VISIBLE_SUITE_WORKLOADS`, `SUITE_WORKLOAD_TEXT['remote']`,
  `probe_workload_pair_sd_pct`, `remote_tools_text`), `tools/remote_broker.py` (`RESTIC_TOOL_WORKLOADS`,
  `restic_job_sizing`). `restic-v2/launch.py` and `deploy_fastpath.py` are NOT edited (section 11 holds the edits).
* Boundary script: `restic-v2/judge/remote_boundary.py` (`null`, `reference`, `bundle`, `status`, `report`).
* Tests: `Leagues/tests/test_remote_workload.py` (15), `test_workload_suite.py` (28), `test_fast_research_tools_v2.py`
  (32); with `test_restic_target.py`, `test_noise_aware_probes.py`, `test_fast_research_tools.py`: **148 pass**
  (131 before this pass). `tools/tests`: 112 pass, 2 fail in `test_v2_selection.py` (pylint selection; they fail
  without these changes too). Fixture `tests/fixtures/restic_judge_b5e4b9e1.py` = the deployed judge.
* Box: `/opt/league/restic-league-v2/workloads-remote/` (2.0 GB, version remote-2, not sealed), the staged
  harness file above, scratch `/opt/league/restic-remote-dev/` (dev judges `judge/` = 426b7704 and `judge2/` =
  74723d34, server source and binary, the three v1 jobs' evidence, `workloads-remote-v1-retired/` 2.0 GB, `sanity/`,
  `rt-state/` 1.2 GB of scratch harness state; safe to delete once the evidence is no longer wanted). Nothing else
  under the league root was created or changed.
* Helpers: `restic-v2/judge/remote-workload/tools/` (`box.py`, `launch_job.sh`, `analyze.py`, `smoke_inner.sh`,
  `watchdog.sh`; second pass: `sanity_v2.py`, `sanity_run.sh`, `sanity_inner.sh`, `sanity_run3.sh`), log
  `remote-workload/PROGRESS.md`.

## 9. Profile v2 sanity check (2026-10-04, during S018; DIAGNOSTIC ONLY)

Three runs on CPUs 2-5 under `flock shared/cpus-2-5.lock` (the builders' untimed lane; schedutil governor, about 1.2-1.4x
slower than a judge lane on CPU-bound work), scratch judge copy `judge2/restic_judge.py` (`74723d34`), the STAGED
harness, scratch harness state and cache under `restic-remote-dev/`. One pair per phase: a reading, not a
measurement. Lane held 69 s + 138 s + 93 s. Evidence: `Leagues/evidence/restic-remote-20261004/sanity-v2/`.

| run | what | lane held | result |
|---|---|---:|---|
| 1 `prep` | stock-made states of remote v2 (scratch tmpfs bundle), binaries from the live bincache (read-only copy) | 69 s | states in 67 s, 4.7 GB of tmpfs |
| 2 `bench` | staged harness, `bench --workload remote`, seven phases, 1 pair, incumbent `S017-merge` vs stock | 138 s | table below |
| 3 `timeline` + `oracle` | `timeline --phase restore`; `oracle --phases check-read-data,forget-prune` | 93 s | requests per stage; oracle PASSED (corrupted pack detected through the server; stock reads the pruned repository locally) |

| phase | stock s | incumbent s | time saved | requests stock / incumbent | MiB moved stock / incumbent | transfer floor s | latency connection-s stock / incumbent |
|---|---:|---:|---:|---|---|---:|---|
| backup-initial | 8.80 | 7.97 | +9.4% | 88 / 89 | 1248.5 / 1248.4 | 5.20 | 2.6 / 2.7 |
| backup-unchanged | 4.30 | 3.61 | +16.1% | 17 / 17 | 16.0 / 16.0 | 0.07 | 0.5 / 0.5 |
| backup-mutated | 4.06 | 3.52 | +13.3% | 23 / 23 | 103.4 / 103.4 | 0.43 | 0.7 / 0.7 |
| restore | 11.84 | 12.03 | **-1.6%** | 88 / **446** | 1245.4 / 1245.4 | 5.19 | 2.6 / **13.4** |
| check-read-data | 7.46 | 6.91 | +7.4% | 89 / 89 | 1248.4 / 1248.4 | 5.20 | 2.7 / 2.7 |
| forget-prune | 4.58 | 3.84 | +16.2% | 121 / 136 | 294.6 / 291.7 | 1.23 | 3.6 / 4.1 |
| ls-json | 3.24 | 2.04 | +37.0% | 14 / 14 | 9.9 / 9.9 | 0.04 | 0.4 / 0.4 |
| **one pass** | **44.28** | **39.91** | **+9.86%** (4.37 s) | **440 / 814** | 4166 / 4163 | **17.36** | 13.2 / 24.4 |

* **Floor share: 39% of stock's pass, 43% of the incumbent's** (v1: 73%). One second is worth 2.3 points on this
  workload (v1: 1.06; main: 4.5).
* **The incumbent keeps +9.9% here** (v1: +4.3%; local main: +27%). Everything the league won in process start-up,
  CPU and tree work now shows; what it did to the request count shows too.
* **Restore is the phase to aim at.** Both arms sit 6.6-6.8 s above the 5.19 s floor. The incumbent sends 446
  requests where stock sends 88 (433 ranged `GET data` against 75 whole-pack GETs): 10.7 more connection-seconds of
  latency, and it ends 0.19 s behind stock. Timeline (one instrumented run): the file stage takes 9.45 s / 432
  requests on the incumbent against 7.36 s / 74 on stock (+2.08 s), while key derivation (0.32 vs 0.60 s), the stage
  before the files (1.12 vs 1.79 s) and the second pass (0.60 vs 1.53 s) are faster.
* forget-prune: 136 requests against 121 (15 more `GET data`), still +16% on CPU and start-up.
* Peak memory survives as before: 60-132 MiB against stock's 67-294 MiB per phase.

## 10. The four-workload suite (S019): rounds, job times, memory, bands

**Score.** `workload_suite: ['main', 'big-index', 'large-files', 'remote']`: suite score = mean over the four
workloads of each workload's median paired time saved. The per-workload gates apply to `remote` like the others: peak
memory vs the incumbent no worse than -2.0% (`RESTIC_JUDGE_RSS_GUARD_PCT`) and time vs the incumbent no worse than
-1.0% (`RESTIC_JUDGE_WORKLOAD_REGRESSION_PCT`). No judge constant changes.

**Rounds (the judge's own rule: the mode's rounds rounded up to a multiple of the workload count; no setting needed).**

| mode | rounds, 3 workloads (S018) | rounds, 4 workloads (S019) | per workload | estimated job time (S018 measured) |
|---|---:|---:|---:|---|
| match | 6 | **8** | 2 | ~37 min (29.8 min) |
| reference (rescreen) | 12 | **12** | 3 (was 4) | ~45 min (~38 min) |
| promotion | 42 | **44** | 11 (was 14) | ~105 min (89 min) |

Promotion still needs 29 score wins (`RESTIC_JUDGE_PROMOTION_WINS`, unchanged), now of 44, and a suite score of at
least +0.25%. Time estimate: a three-arm round is 1.6 min on main, 2.2 on big-index, 1.3 on large-files (S017
promotion job `582b1263`) and **about 2.4 min on remote v2** (three passes of 36-40 s on a judge lane plus copies
and the per-round oracle; v1 measured 5.4 min); set-up grows from ~18 to ~22 min (one more set of states, 67 s, and
one more warm-up with the full oracle for every arm). The remote round time is an estimate from the sanity run on a
slower lane: the boundary `canary` and `reference` jobs give the real numbers.

**Memory.** The broker's formula is unchanged and already covers N workloads (`remote_broker.restic_job_sizing`):

| job | tmpfs | unit memory cap | expected peak |
|---|---:|---:|---|
| three-workload suite (S018) | 24 GiB | 28 GiB | 22.3 GiB measured |
| **four-workload suite (S019)** | **31 GiB** | **35 GiB** | **~27 GiB** (22.3 + remote's states, 4.7 GiB measured) |
| one named workload (research probe, boundary null) | 10 GiB | 14 GiB | 9.0 GiB measured on remote v1 |

Time caps double for a suite as before (match 6 h, reference 10 h, promotion 32 h). **Lanes: two official lanes
recommended** (`judge_parallel_lanes: 2`): 2 x 27 = 54 GiB, less than S018's 3 x 22.3 = 67 GiB; three lanes would be
81 GiB, 14 GiB more than S018 holds today, on a box that showed 85-93 GB available while S018 ran. The research
bundle of `remote` adds 4.7 GB to `/dev/shm` once. The server stays on the measured lane (no broker change).

**Probe bands.** `probe_workload_pair_sd_pct: {'remote': {...}}` (new config key; absent = every workload uses the
main values, as today). A `benchmark_candidate` result measured on `remote` gets its own whole-workload and
per-phase bands and names its null source; other workloads and suite results are untouched. The values come from
the boundary null (`remote_boundary.py report` prints the block); section 11 holds placeholders.

**What roles read** (only in a season whose suite names `remote`; an S018 config produces the same text as before):

> remote = the main corpus and the same seven phases, with the repository behind a simulated network link: restic
> reaches it through its REST backend, every request costs 30 ms before its first byte, each connection carries
> 48 MiB/s, and at most 5 requests are in flight at once (a sixth waits). The bytes a phase must move set a floor
> (bytes / 240 MiB/s) that no change goes under without moving fewer bytes; above that floor the time is
> requests x 30 ms, connections left idle while data is waiting, and CPU. Request count and how many of the 5
> connections stay busy are what a tree can move here: small ranged reads that are free on a local disk cost a
> round trip each

and, in the fast-tools block: "On workload remote ... quick_bench, phase_timeline, profile_diff and ablate_changes
also report, per phase and arm, the REQUESTS sent and the MiB moved up and down, with the request table by method
and file type (phase_timeline: per stage too). Aim with those numbers ...".

**Builders' tools on `remote`** (`research-tools/CONTRACT.md`, section "Workload `remote`"): `quick_bench`,
`phase_timeline`, `oracle_check`, `profile_diff`, `ablate_changes`, `sweep_constant` and the baseline card all work
through the same harness path (`restic_run`), which starts and stops the workload's REST server around every arm.
Measured on CPUs 2-5: bench of seven phases x 1 pair 138 s, timeline of restore 32 s, oracle of two phases 60 s.
The unit caps of the tools are unchanged.

**Not exercised on the box yet** (first run = the boundary steps of section 11): the judge's own corrupted-pack
check through the server (unit-tested with stubs; the harness's twin of it passed in sanity run 3), any
four-workload suite job, a research probe on `remote` through the broker, `profile` / `ablate` / `baseline` on
`remote` (same run path as the bench and timeline that ran).

## 11. S019 boundary checklist (coordinator; in this order, with no season alive)

Shell: Git Bash in `Leagues`, `export MSYS_NO_PATHCONV=1`; `BOX="python restic-v2/judge/remote-workload/tools/box.py"`;
`R=/opt/league/restic-league-v2`.

1. **S018 is over**: no `run_season.py` on the box, no `restic-v2-*` unit active (`deploy_fastpath.py` refuses
   otherwise). Do the usual boundary work first (promotion or not, incumbent installed, roster `active_cbk` current):
   the steps below read the incumbent from the roster.
2. **Pin and deploy the judge.**
   * `$BOX up restic-v2/judge/restic_judge_fastpath.py $R/judge/restic_judge_fastpath.py` (prints the box sha256;
     it must be `a770cec81f9b2344449fc6d4cfd27cface44bc0c8019042340163dc308a22e21`, the raw bytes of the local file).
   * `restic-v2/judge/deploy_fastpath.py`, line 33: `EXPECTED_SHA = "a770cec81f9b2344449fc6d4cfd27cface44bc0c8019042340163dc308a22e21"`
     (keep the history comment; add "2026-10-04 remote-backend workload, REMOTE.md").
   * `python restic-v2/judge/deploy_fastpath.py` (preflight, backup, install, isolation re-proved, smoke on main).
     Research caches miss once (their keys hold the judge sha).
3. **Workload dir**: already in place. Check: `$BOX sh "sha256sum $R/workloads-remote/workloads.json"` =
   `7e4a1bd7958d5b87aaef4d32034791062e04fa0b5b97753770107114055bb6f0` (version `restic-v2-workload-remote-2`).
   Optional seal as main: `chattr -R +i $R/workloads-remote`.
4. **Switch the builders' harness** (one copy; the broker's path does not change):
   `$BOX sh "cd $R/research-tools && cp -p research_tools_v2.py research_tools_v2.pre-remote.py && cp research_tools_v2_remote.py research_tools_v2.py && sha256sum research_tools_v2.py"`
   must print `998612d135e786e173e0bacc8239fa8b11fb6ea96f7f4eb20c8d154cae5595e3` (= the local
   `restic-v2/research-tools/research_tools_v2.py`; if the local file changed since, upload that file instead and
   compare with its sha). Rollback: copy `research_tools_v2.pre-remote.py` back.
5. **First real run of the remote path, and the tool states**: `python restic-v2/judge/remote_boundary.py bundle`
   (one research probe, incumbent vs itself, ~6 min). It must end `complete` with every oracle passing: this is the
   first time the judge's own corrupted-pack check runs through the server. It leaves the reference states in
   `/dev/shm/restic-league-v2-cache/workload-remote/`, which the fast tools and the baseline card need.
6. **Null and reference** (official lanes; run the two commands in two terminals, they take three lanes together):
   * `python restic-v2/judge/remote_boundary.py null` (stock x3 and incumbent x2, 12 rounds each, ~45 min)
   * `python restic-v2/judge/remote_boundary.py reference` (incumbent vs stock, 12 rounds, ~30 min)
   * any time: `python restic-v2/judge/remote_boundary.py status` (also `evidence/restic-remote-v2-boundary/status.json`);
     after an interruption rerun the same command, it resumes.
   * `python restic-v2/judge/remote_boundary.py report` writes `NULL-REMOTE-V2.md` (per-round and per-phase SDs, the
     `NULL_REMOTE_V2` block) and `REFERENCE-REMOTE-V2.md` (time saved, wins, per-phase requests and bytes per arm, the
     request tables by method and type). Copy the reference table into the season directive or knowledge folder.
7. **Four-workload canary** (recommended, ~37 min, one official lane): `python restic-v2/judge/remote_boundary.py canary`.
   While it runs: `$BOX sh "systemctl show restic-v2-<job id>.service -p MemoryPeak"` (expect ~27 GiB, cap 35 GiB).
8. **Edit `restic-v2/launch.py`** (four places; nothing else):
   * after the `NULL_20261003 = {...}` block (ends at line 142), paste the block printed by step 6. Until then the
     placeholder is (v1's absolute SDs over the v2 phase seconds, x1.5; NOT measured):
     ```python
     NULL_REMOTE_V2 = {
         'time': 1.0, 'rss': 2.5,
         'phase': {'backup-initial': {'time_saved_pct': 3.0, 'rss_saved_pct': 6.4},
                   'backup-unchanged': {'time_saved_pct': 0.6, 'rss_saved_pct': 6.4},
                   'backup-mutated': {'time_saved_pct': 6.0, 'rss_saved_pct': 4.1},
                   'restore': {'time_saved_pct': 0.5, 'rss_saved_pct': 5.3},
                   'check-read-data': {'time_saved_pct': 4.0, 'rss_saved_pct': 7.5},
                   'forget-prune': {'time_saved_pct': 6.0, 'rss_saved_pct': 3.4},
                   'ls-json': {'time_saved_pct': 0.3, 'rss_saved_pct': 1.2}},
         'source': 'PLACEHOLDER (remote v1 null scaled to the v2 phase seconds); replace with remote_boundary.py report',
     }
     ```
   * line 162: `'promotion_policy': {'wins': 29, 'rounds': 44, 'minimum_score_pct': 0.25},` (the judge runs 44 rounds
     for four workloads; this value is what roles read and what the win-rate check uses).
   * line 170: `'judge_parallel_lanes': 2,` (section 10; 3 only if the canary's MemoryPeak and `free -g` leave room).
   * line 209: `'workload_suite': ['main', 'big-index', 'large-files', 'remote'],` and, on the next line,
     `'probe_workload_pair_sd_pct': {'remote': NULL_REMOTE_V2},`
   The test `tests/test_workload_suite.py::FourWorkloadSuiteTests::test_launch_config_names_remote_only_together_with_its_bands_and_rounds`
   then checks the four edits agree and refuses the PLACEHOLDER source.
9. **Tests**: `python -m pytest tests/test_remote_workload.py tests/test_workload_suite.py tests/test_restic_target.py tests/test_noise_aware_probes.py tests/test_fast_research_tools.py tests/test_fast_research_tools_v2.py -q`
   (148 pass today).
10. **Tell the teams.** The brief paragraph and the fast-tools paragraph of section 10 appear by themselves once the
    suite names `remote`. Add to the S019 directive: "The suite has a fourth scored workload, `remote`: the main
    corpus behind a simulated link (30 ms per request, 48 MiB/s per connection, 5 connections). It weighs a quarter
    of the score. The incumbent is +X% against stock there (reference table attached) and sends about 810 requests
    per pass where stock sends 440; restore is 0.2 s SLOWER than stock on it because of ~360 extra ranged reads. A
    promotion may not lose more than 1% of time or grow memory more than 2% on any workload, remote included.
    quick_bench and phase_timeline report requests and MiB per phase and arm on it." (X and the table: step 6.)
    `restic-v2/INTERFACE.md`, one amendment: "2026-10-XX (from S019): fourth visible, scored suite workload `remote`
    (judge/REMOTE.md): main's corpus through the REST backend on a simulated link, profile v2 (30 ms per request,
    48 MiB/s per connection, 5 connections), workload version `restic-v2-workload-remote-2`; suite score = mean of
    four workloads; match 8 / reference 12 / promotion 44 rounds, 29 wins; per-workload memory guard and -1% time
    floor apply; judge `a770cec8`; builders' tools report requests and bytes on it."
11. **Start S019 as usual** (`launch.py --season S019 ... --start` ships `tools/` with the controller changes). The
    first fast-tool call of the season starts the baseline cards; the `remote` card carries the link table
    (requests, MiB, latency, floor per phase).
12. **Rollback** at any point: `workload_suite` back to three names (the workload disappears from every role text,
    schema and judge call); harness: step 4's backup; judge: `python restic-v2/judge/deploy_fastpath.py --rollback`.

**Parking lot (for the coordinator's eyes; nothing here was acted on).**
* The promotion win count stays 29 while the rounds go 42 -> 44 (66% instead of 69% of the pairs). The +0.25% score
  floor is what holds the false-positive rate; keeping the ratio would need the broker to pass
  `RESTIC_JUDGE_PROMOTION_WINS=30` to the judge unit (not built).
* On this workload a team can lose restore time and still pass (the gate is the workload's total, -1%): S017's
  restore step would have been -1.6% on restore and inside the gate on the workload.
* `restic-v2/league.json` and the local `restic-v2/runtime/S018-config.json` were rewritten by a `launch.py --dry-run`
  I ran as a check (it writes both). The S018 config was restored byte for byte from the box copy
  (sha256 `1a4b9e7b...`); `league.json` is `base_config()` of the unedited `launch.py`. Nothing on the box changed.

## 12. S019 boundary results (2026-10-05; judge `a770cec8`, incumbent `incumbents/S018`)

**What went wrong first.** Judge `74723d34` was deployed at 04:28 UTC (smoke passed on main). The first three remote
jobs on the real supervisor layout (bundle `252ba51a`, null-stock `057d9801`, null-incumbent `246b48a1`) all failed
at the first server start: `restserver: listen unix <league>/supervisor-jobs/<32 hex>/scratch/<16 hex>/work/<arm>/rest.sock:
bind: invalid argument`. That path is 130 bytes (134 for `candidate` / `incumbent`); a unix socket path holds 107. The
2026-10-04 jobs ran under `restic-remote-dev/jobs/<name>/` (96 bytes) and never met the limit. Fix (`a770cec8`,
deployed 05:03 UTC; harness `998612d1`): the server is started with cwd = the work dir and `-listen rest.sock`; its
store, statistics and log paths are made absolute. restic's end is `/work/rest.sock` inside the sandbox and never
changed. Tests: `test_server_binds_a_relative_socket_name_from_the_work_dir` (judge) and
`test_the_server_binds_a_relative_socket_name_from_the_work_dir` (harness), both with a 140+ byte work path. The
failed jobs' records are in `evidence/restic-remote-v2-boundary/failed-first-attempt-74723d34/`.

| job | id | what | wall | result |
|---|---|---|---|---|
| bundle | `60e80af0` | research probe, incumbent vs itself | 05:04:17-05:14:11 (9.9 min, cold cache) | complete, 47/47 oracle units, corrupted-pack check through the server passed; states in `/dev/shm/restic-league-v2-cache/workload-remote/` |
| null-stock | `a76aa132` | stock x3, 12 rounds | 05:07:04-05:48 (41 min) | 36 identical-arm pairs |
| null-incumbent | `bd62a706` | incumbent x2 (+ stock), 12 rounds | 05:07:10-05:48 (41 min) | 12 identical-arm pairs |
| reference | `909b344f` | incumbent vs stock, 12 rounds, two arms | 05:07:04-05:34:12 (27 min) | +8.83% |
| canary | `c5b0d1c3` | four-workload match, incumbent vs stock, 8 rounds, two arms | 05:34:43-06:02:45 (28 min; 18 min of it set-up) | +24.12%, unit memory peak 25.5 GiB of 35 |

**Null (official lanes, 48 pairs).** Per-round paired time SD **0.42%** (robust 0.43%; stock pairs alone 0.37%,
incumbent pairs 0.58% on 12 values), RSS SD 2.38%. SE of the median time: 8 rounds 0.19%, 11 rounds 0.16%, 12 rounds
0.15%, 44 rounds 0.08%. A stock pass is 38.95-39.02 s, an incumbent pass 35.08 s.

| phase | time SD % | robust | absolute SD s | RSS SD % | probe band (x1.5, rounded up) |
|---|---:|---:|---:|---:|---:|
| backup-initial | 0.88 | 0.85 | 0.072 | 4.67 | 1.35 |
| backup-unchanged | 0.39 | 0.35 | 0.011 | 6.91 | 0.6 |
| backup-mutated | 2.69 | 2.28 | 0.080 | 6.86 | 4.05 |
| restore | 0.84 | 0.21 | 0.088 | 4.23 | 1.3 |
| check-read-data | 1.42 | 0.53 | 0.096 | 6.08 | 2.15 |
| forget-prune | 1.99 | 1.87 | 0.080 | 7.26 | 3.0 |
| ls-json | 0.38 | 0.26 | 0.006 | 1.51 | 0.6 |

Pooled whole-workload probe band 0.7% time, 2.4% RSS (what the report prints; see the next paragraph for the bands
`launch.py` carries). In absolute terms every phase is within
0.1 s; the percentages are large where the phase is short. Full tables:
`evidence/restic-remote-v2-boundary/NULL-REMOTE-V2.md`.

**The incumbent is two-valued in restore and check-read-data; stock is not.** In the null-incumbent job both identical
arms take either 10.35 s or 10.67 s for restore and either 6.70 s or 7.00 s for check-read-data, from round to round
(stock: 10.79-10.86 s and 7.22-7.27 s). So identical-arm pairs of the league tree read up to +/-3.2% (restore) and
+/-4.7% (check-read-data), where stock pairs have SDs of 0.20% and 0.49%. The pooled table above mixes 36 stock pairs
with 12 incumbent pairs. From the incumbent pairs alone (12 values, same x1.5 rule) the probe bands would be: whole
workload 0.95, backup-initial 1.40, backup-unchanged 0.60, backup-mutated 5.80, restore 2.55, check-read-data 3.85,
forget-prune 3.15, ls-json 1.05. A probe compares two league trees, so **`launch.py` carries these incumbent-pair
time bands** (coordinator 2026-10-05; each is at least the pooled value; RSS bands are the pooled ones). Official
judging takes medians over 11 rounds per workload and is not affected.

What the per-round rows show (null-incumbent job, 24 runs of the same tree; no further investigation):
* restore: **8 of 24 runs took 10.33-10.39 s and sent 435 requests; 16 of 24 took 10.65-10.70 s and sent 446.** The
  11 extra requests x 30 ms = 0.33 s is the whole gap, so they are round trips made one after another. Bytes, CPU
  (8.2-8.4 s) and connections in use are the same in both. The count is fixed by nothing the judge controls: both
  values occur at every position in the round and in both arms. In the reference job (its own stock-built state) the
  same tree sent 443 requests in all 12 rounds and took 10.61-10.70 s; in the bundle job 430. So the number of ranged
  reads depends on the repository state a job builds and, within one state, on run-to-run timing inside restic.
* check-read-data: **19 of 24 runs took 6.67-6.76 s, 5 of 24 took 6.95-7.01 s**; in the reference job 6 of 12 were
  6.73-6.78 s, 3 were 6.99-7.02 s and 3 fell between (6.88-6.93 s). Requests (89), bytes, link-occupied seconds
  (28.8) and CPU (4.85-4.89 s) are identical in fast and slow runs, so the 0.3 s is time in which nothing is in
  flight; the server statistics do not say where. Stock: 7.22-7.30 s in every run.

**Reference: `incumbents/S018` vs stock, +8.83% [8.70, 8.97], 12/12; peak memory +44.4%.** Pass 38.85 s -> 35.44 s.

| phase | stock s | incumbent s | time saved | peak MiB stock / inc | requests stock / inc | MiB moved (both) | transfer floor s |
|---|---:|---:|---:|---|---|---:|---:|
| backup-initial | 8.40 | 7.83 | +6.62% | 191 / 96 | 89 / 89 | 1248 | 5.20 |
| backup-unchanged | 2.99 | 2.41 | +19.35% | 134 / 68 | 17 / 17 | 16 | 0.07 |
| backup-mutated | 3.24 | 2.68 | +17.51% | 170 / 81 | 23 / 23 | 103 | 0.43 |
| restore | 10.80 | 10.62 | +1.62% | 289 / 126 | 88 / **443** | 1245 | 5.19 |
| check-read-data | 7.27 | 6.83 | +6.12% | 136 / 91 | 89 / 89 | 1248 | 5.20 |
| forget-prune | 4.10 | 3.68 | +10.98% | 134 / 104 | 117 / 134 | 309 / 305 | 1.29 |
| ls-json | 2.04 | 1.33 | +34.39% | 67 / 57 | 14 / 14 | 10 | 0.04 |

All phases 12/12. Stock sends 437 requests per pass, the incumbent 809; the transfer floor is 17.4 s = 45% of stock's
pass and 49% of the incumbent's. Against the sanity reading of section 9 (S017-merge on a slower lane: +9.9%, restore
-1.6%): restore is now +1.6% (0.18 s) ahead of stock, with the same ~355 extra ranged reads. Request tables by
method and type: `evidence/restic-remote-v2-boundary/REFERENCE-REMOTE-V2.md` (this is the table for the directive).

**Canary: the first four-workload suite job.** Suite score +24.12% vs stock, 8/8; main +29.12% (memory +40.9%),
big-index +40.08% (+46.5%), large-files +17.99% (+37.6%), remote +9.30% (+45.0%); 177/177 oracle units on every
workload. Job time 28.0 min for an 8-round two-arm match (set-up 18 min: four sets of states and warm-ups; rounds
1.25 min each). A season job has three arms: from these round times expect about 37 min for a match and about 105 min
for a 44-round promotion (estimates, as section 10). Unit memory peak **25.5 GiB**
(sampled 9.5 GiB during main's set-up, 16.0 big-index, 23.5 large-files, 24.9 remote, 25.5 in the rounds); a
single-workload remote job peaks at 9.0 GiB (cap 14).

**Lanes: 2** (`judge_parallel_lanes`). The box shows about 88 GB available with nothing running and the research
cache loaded (31 GB in `/dev/shm`). Three four-workload jobs are 77 GiB; with four research jobs (9 GiB measured each,
14 cap) that is 113-133 GiB. Two are 51 GiB, which leaves room for the four research jobs at their measured size.
The research cache holds stale bundles after a judge change (keys hold the judge sha): 4.8 GB for main under
`74723d34`, and the big-index (12 GB) and large-files (4.9 GB) bundles built under `b5e4b9e1`; they go when the next
probe on each workload rebuilds them.

**Done at this boundary** (checklist steps 1-9; step 10's directive text and step 11 are the coordinator's):
`deploy_fastpath.EXPECTED_SHA`, harness switch (`research_tools_v2.pre-remote.py` = `e19a384c` kept), `launch.py`
(`NULL_REMOTE_V2` with the incumbent-pair time bands, promotion rounds 44, lanes 2, `workload_suite` with `remote`, `probe_workload_pair_sd_pct`), tests
(187 pass in the seven files), `INTERFACE.md` amendment 2026-10-05. `workloads-remote` is not sealed with `chattr`.
