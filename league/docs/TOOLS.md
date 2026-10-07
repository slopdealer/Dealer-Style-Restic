# Tools

The tools the league measured and checked with, as published copies under `league/tools/`. This document says what
each is, how it is run, what it needs and what it writes; then how to make one comparison on your own machine.

**About the copies.** They are the files that ran, with these edits and no others: workstation paths replaced by the
placeholder `LEAGUES_HOME`, the league machine's host name replaced by "the league box", line ends normalised to LF,
a first comment line added to every edited file, and the generator of the held-out workload cut out of
`workload_gen.py`. Paths under `/opt/...` are the league box's layout and are left in, with **one rule that holds
for every published copy** (documents, tools, logs and imported result files): the prefix of the league box's
working directory reads `/opt/league/`. Nothing else about a path was changed, so `/opt/league/restic-league-v2` is
the league root and `/opt/league/restic-final` the folder of the final work, as on the box. Most of these paths can
be overridden by an environment variable named below. The tools were written to run on one machine. Expect to edit
constants. Where a copied evidence file was changed by that rule, `league/evidence/carried-files.json` and
`league/evidence/judge/index.json` keep the SHA-256 of the original.

**Passwords.** The repositories these tools create are test fixtures. The check harness uses the fixed password
`chk`; the judge reads its test password from a file in the workload directory. Neither protects anything.

**Not published:** the league's engine, the generator and the tooling of the held-out workload, and the operator's audit
tools. The engine is the program that seats the roles, hands out workspaces, queues judge jobs and applies the gates:
about 14,000 lines, tied to the league's machine and to its role prompts. `league/tools/league/launch.py` is published as
the record of how each season was configured (gates, CPUs, noise bands, which models). It cannot run without the engine.

## 1. Every published tool

Paths are under `league/tools/`. "The league root" is the directory named by `RESTIC_LEAGUE_ROOT` (section 2.1).

### The judge and its workloads

**`judge/restic_judge.py`** is the judge, as deployed: three arms, paired order-rotated rounds, seven phases, the
correctness oracle, the build of every arm, time and peak memory (METHODS.md, section 1). Run:
`python3 restic_judge.py --candidate DIR --incumbent DIR --stock DIR --mode MODE --out RESULT.json`, with
`--workload NAME` or `--workloads a,b,c,d`, `--cpus`, `--scratch`. `python3 restic_judge.py setup WHAT` prepares the
league root. Needs Linux, root, bubblewrap, a tmpfs. Writes one result JSON and an evidence folder. Section 2 walks
through it.

**`judge/workload_gen.py`** generates the frozen workloads from seeds: `--profile v1` (the main corpus, seed
20260924 by default), `big-index`, `large-files`, `remote`; `--out DIR`. It writes the corpus, the mutation set as a
declarative list with payload files, the expected manifests and `workloads.json` with the hashes of all of it, which
the judge re-derives before every job. Run once per workload, as root, beside `restic_judge.py` (it imports helpers
from it). The profile of the held-out workload was cut from the published copy.

**`judge/restserver/main.go`** is the REST server with the simulated link for the `remote` workload: `-listen` (a
Unix socket path), `-root` (a repository directory in restic's local layout), `-latency-ms` (30 in the workload),
`-rate` (bytes per second per connection; 48 MiB/s in the workload), `-max-connections` (5), `-stats`, `-log`. Build
it with `go build` inside a copy of upstream's module. The delays are sleeps only.

**`judge/ballastgen/main.go`** writes the ballast of the `big-index` workload into a repository: 1,500,000 unique
48-byte data blobs referenced by one snapshot tagged `ballast`. It is compiled inside a copy of upstream's tree
(`cmd/ballastgen/`) with the judge's toolchain and called by `workload_gen.py --profile big-index --ballast-bin`.

**`judge/set_judge_governor.sh`** sets the `performance` CPU frequency governor on the league box's judge and
research CPUs and their SMT siblings. The CPU numbers in it are that machine's. The setting does not survive a
reboot.

### The builders' research tools

**`research-tools/research_tools_v2.py`** is what a builder runs during a match, and what every "bench" in the
documents was made with: `build`, `test`, `bench`, `profile`, `sweep`, `race`, `ablate`, `timeline` and more, on a
candidate tree, in the judge's sandbox, on the judge's corpora, on CPUs other than the judge's. A bench reports the
median of paired differences, pairs won and a verdict against a noise band (METHODS.md, section 3). It imports the
judge and needs the league root. **`research-tools/CONTRACT.md`** is its interface as the teams were given it;
**`research-tools/HARNESS-NOTES.md`** is its developer's log with example invocations. Section 4.

### The release-check harness

**`checks/c01_own_tests.py` to `checks/c16_copy.py`** are the 16 release checks; what each does and what counts as a
failure is the table in METHODS.md, section 10. Each is `python3 cNN_name.py TREE LABEL`. **`checks/run_all.py`** runs the
set in two lanes (`run_all.sh` is its older shell form). **`checks/report.py LABEL`** prints the table of a run from the
summary files the checks write (verdict, key numbers, every failure with its reproduction). Section 5.

**`checks/x06_size_anatomy.py`** compares what two binaries store for the same input, from copies of one initialised
repository: blob counts, IDs, stored lengths, pack overhead (it settled the withdrawn repository-size alarm).
**`checks/x08_repair_packs.py`** runs `repair packs` with one connection several times per binary with a time limit
(finding R2). **`checks/x10_dirs_memory.py`** measures peak memory of a restore of one and two million directories through
a small helper process, with a heap profile (failure F5). **`checks/x11_dirs_time.py`** times restores of 606,060 to
2,020,200 directories in rotated order with all check CPUs and with 4 cores (the many-directory finding). `CHK_X11_TOPS`,
`CHK_X11_ROUNDS` and `CHK_EXTRA_TREES=name=TREE` choose sizes, rounds and extra arms. **`checks/x13_single_test.py`** runs
one named test of a tree, once plain and three times under the race detector, and lists the files that differ from another
export: `CHK_X13_TEST=<test name> CHK_X13_COMPARE=<other tree> python3 x13_single_test.py TREE LABEL`. It is how the one
test added by the last commit, `TestAsyncSaveResultSuppressionIsNarrow`, was run on the published source. Each is run like
a check.

**`checks/tools/cpufreq_state.py`** is the tool of the governor test (RESULTS.md, section 6.2): `save FILE` records
the frequency-governor state of the check CPUs, `set GOVERNOR FILE` writes `scaling_governor` on those CPUs and
nothing else, `restore FILE` writes the saved values back and reads them back, `sample OUT SECONDS` logs the clock
of each check CPU and the load on the other CPUs, `quiet` tests for an idle machine. It writes to `/sys` and needs
root; the CPU numbers in it are the league box's. **`checks/tools/x11_clock_table.py`** makes the table of that test
from the run records and the clock samples.

**`checks/lib/chk.py`** is the shared library: CPU pinning, builds with the pinned toolchain, scratch handling,
status files. **`checks/lib/gotest.py`** runs `go test -json` with the pinned toolchain and reduces the event stream to
per-test results. **`checks/lib/bigindex.py`** prepares the 1.5-million-blob starting repository.
**`checks/lib/rusage_exec.py`** is the helper that starts a measured program so that its peak memory is not
inherited from a large parent.

**`checks/server/chkrest/`** is a REST server for fault injection: it can delay, fail, hold back or damage chosen
requests (`-damage-file`, `-damage-trigger` and others); C03, C05, C08, C11, C14 and C16 use it.
**`checks/tools/chktool/`** holds helper commands that are built into a scratch copy of upstream's restic for the
checks; they are never part of a tree under test.
**`checks/witness/`** holds the first reviewers' witness tests as they were written, by area, before they became tests
of the tree; some assert a defect of an unrepaired tree and fail on this tree by construction.

### The merge, bench and release tools

All of these assume the league box's layout; they are published as the record of how each number was made.

**`merge/judge_final.py`** submitted the official jobs of the final merge to the league's job supervisor (the supervisor
is not published; the script shows the job specification). **`merge/summ_judge.py NAME`**, **`merge/tables.py NAME`**,
**`merge/phases.py NAME`** and **`merge/sidebyside.py A B`** print suite, per-workload and per-phase tables from the
result files of finished jobs. The tables of the merge report came from them.

**`merge/nm3.sh` to `nm6.sh`** are the merge agent's one-off invocations from the binary-layout finding: they build
trees with the pinned toolchain and print the addresses of the key-derivation symbols. They call a helper that is
not in the copy; the command that matters is the `go tool nm` line of section 3.
**`merge/box/nmdiff2.py A B`** lists the text symbols placed before the key-derivation loop that are new, gone or of
another size between two builds, which is how the cause of a moved loop was found. Section 3.

**`merge/box/genjobs.py`** and **`merge/box/worker3.sh`** are the small queue the merge agent's benches ran on:
job files, lanes, and "quiet" jobs that run one at a time only while nothing else of the merge and no judge job is
active. **`merge/box/fullchk.sh NAME`** runs the tree's own tests over every package as root and compares failing
test names with upstream's in the same environment, then `go vet`, a 386 build and the race detector on the new
code. **`merge/box/r1chk.sh NAME`** runs the lock-wait witnesses, 3,000 randomized schedules and the `cmd/restic`
tests at lock wait 0 and 200 ms. **`merge/box/rtchk.sh`** runs the independent reviewers' original test files,
unchanged, on the tree they reviewed, and the production-size test of the decode ceiling on the fixed tree.

**`merge/box/killtest.sh NAME N`** kills a `prune --max-unused 0` with -9 at a random moment, N times. After every kill
upstream must pass `check --read-data` and restore the kept snapshot byte-identically, then the prune is completed and
checked again. **`merge/box/dirsmem.sh`**, **`dirstime.sh`** and **`dirsbound.sh`** are the directory-restore measurements
of failure F5 (peak memory; wall time in alternating order; the choice of the bound).

**`merge/box/mc2.sh TREE TAG`** (and `mc.sh`, its first form) is the many-core bench: upstream against a tree with 8
and with 16 whole physical cores, 7 phases on 4 workloads, 5 fixed pairs, one bench at a time on an otherwise idle
machine. **`merge/box/mcbench.py`** is the builders' harness with the user and system CPU seconds of every run
added; **`merge/box/mcsum.py`** summarises its output. **`merge/box/l3dirs.sh`** is the cache-layout test of the
many-directory restore: the same restore on neighbouring cores and on cores spread over four L3 domains (its log is
`league/evidence/many-core-rc9/l3dirs.log`). **`merge/box/mcdirs.sh`**, **`mcdirs2.sh`** and
**`h10.sh NAME`** are the 606,060-directory restores on tmpfs and on ext4 at 4, 8 and 16 cores, with profiles.

**`merge/box/rel.sh TREE VERSION`** and **`rel2.sh TREE VERSION JUDGED`** build the release binaries: the judge's flags
and environment plus the version flag, five targets, `SHA256SUMS`. They also build the judged binary, compare it with the
release binary and read the key-derivation address (METHODS.md, section 12.2). **`merge/box/relbench.py VERSION`** times
the linux/amd64 release binary against the judged build. **`merge/box/proof.sh JUDGED PUBLISHED`** builds two trees as the
judge builds its candidate and compares the binaries byte by byte and section by section (RESULTS.md, section 2).

### The league launcher

**`league/launch.py`** configured and started each season: gates, rounds, CPUs for judge and research lanes, noise
bands, which models the teams ran on, the directive attached. It drives the league's engine, which is not
published, so it cannot be run; it is here as the record of each season's configuration.

### The tables

**`make_tables.py`** rewrites every generated table in `README.md`, `RESULTS.md`, `CHANGES.md` and `RED-TEAM.md`
from the result files, the identity record, the many-core data file and the patch files; `--check` fails if a
document is stale. **`import_result.py SOURCE NAME [BOX_PREFIX]`** copies a judge result into the evidence folder,
applies the path rule when the box's prefix is given, and records the SHA-256 of the source file. Section 6.

## 2. Reproduce one comparison on your own machine

This is the path from the tools' interfaces. **It was not executed on a clean machine by the assembly of this
repository**: the judge has only ever run on the league box. Read it as a guide to what the judge needs, not as a
tested recipe. If you only want to know whether the build is faster for you, section 2.4 is the short way.

### 2.1 What the judge needs

- Linux x86-64, root, Python 3, bubblewrap (`bwrap`), a tmpfs with about 8 GB free for the main workload (the judge
  uses `/dev/shm/restic-league-v2` unless `--scratch` or `RESTIC_JUDGE_SCRATCH` names another tmpfs directory).
- One root directory, `RESTIC_LEAGUE_ROOT` (default `/opt/league/restic-league-v2` in the published copy), holding:
  `stock/` (upstream restic at `ba802d42b7294c98b62c16d1157ea3e80820c019`), `toolchain/go` (Go 1.27.0) and
  `toolchain/gomodcache` (a module cache that already holds restic's dependencies: the build runs with
  `GOPROXY=off`), `judge/` (the judge's files), and the workload directories `workloads/`, `workloads-big-index/`,
  `workloads-large-files/`, `workloads-remote/`.
- Four CPUs to pin to: `--cpus`, default `26-29` (the league box's). Pick four physical cores of one cache domain.
- The `performance` governor on those CPUs if you want the league's noise level; without it expect per-round time
  noise near 1% instead of 0.3% (METHODS.md, section 3).

### 2.2 One-time set-up

```
export RESTIC_LEAGUE_ROOT=/srv/restic-league          # your choice
J=$RESTIC_LEAGUE_ROOT/judge                           # copy league/tools/judge/* here

python3 $J/workload_gen.py --out $RESTIC_LEAGUE_ROOT/workloads --seed 20260924    # 1. the main corpus; as root
python3 $J/restic_judge.py setup launcher --cpus 2-5        # 2. builds the small static launcher that forks restic
python3 $J/restic_judge.py setup stock-build --cpus 2-5     # 3. builds upstream twice and records that it is reproducible
python3 $J/restic_judge.py setup pristine --cpus 2-5        # 4. upstream `restic init` once; frozen afterwards
python3 $J/restic_judge.py setup isolation --cpus 2-5       # 5. proves the sandbox has no network and a private PID space
python3 $J/restic_judge.py setup compat-baseline --cpus 2-5 # 6. runs upstream's tests in the sandbox: the baseline
```

**The order matters.** The main corpus comes first: the generator writes `workloads/etc/passwd` and `workloads/etc/group`.
The judge binds those two files into every build sandbox, so `setup launcher` and `setup stock-build` cannot run before
they exist. `workload_gen.py` must sit beside `restic_judge.py` (it imports helpers from it) and must run as root, because
the corpus contains files with other owners, extended attributes and special modes.

The other workloads are generated after step 4, because each copies the sandbox files, the password and the
initialised repository from the main workload's directory. Name that directory explicitly with `--base`: its default
is an absolute path of the league box and does not follow `RESTIC_LEAGUE_ROOT`.

```
B=$RESTIC_LEAGUE_ROOT/workloads
python3 $J/workload_gen.py --profile big-index   --base $B --out $RESTIC_LEAGUE_ROOT/workloads-big-index \
        --ballast-bin <compiled ballastgen>
python3 $J/workload_gen.py --profile large-files --base $B --out $RESTIC_LEAGUE_ROOT/workloads-large-files
python3 $J/workload_gen.py --profile remote      --base $B --out $RESTIC_LEAGUE_ROOT/workloads-remote \
        --server-bin <compiled restserver>      # --latency-ms, --rate-mib, --max-connections default to 30, 48, 5
```

### 2.3 A job

```
python3 $J/restic_judge.py \
    --candidate /path/to/Dealer-Style-Restic \
    --incumbent $RESTIC_LEAGUE_ROOT/stock \
    --stock     $RESTIC_LEAGUE_ROOT/stock \
    --mode reference --workload main --cpus 2-5 \
    --out /tmp/result.json
```

- `--mode`: `correctness` (no timing, the oracle only), `probe` (1 round), `match` (6 rounds), `reference` (12),
  `promotion` (42, or as `RESTIC_JUDGE_PROMOTION_ROUNDS` says; the league's suite jobs used 44).
- There is no two-arm flag. Pass upstream as `--incumbent`: when the incumbent's build input equals upstream's, the
  judge measures two arms and aliases the incumbent to `stock`.
- `--workloads main,big-index,large-files,remote` runs a suite; rounds rotate over the workloads.
- `--calibration` measures identical arms separately: the null run of METHODS.md, section 3.
- Only the candidate's non-test `*.go` files under `cmd/` and `internal/` are used; everything else comes from
  `stock/`.

**What it writes.** `--out` is one JSON file, schema `restic-linux-judge-v1`; evidence (logs, per-run records) goes
to a directory beside it. The keys to read:

| key | content |
|---|---|
| `comparisons.candidate_vs_stock.time_saved_pct`, `.rss_saved_pct` | the headline figures |
| `.runtime_wins`, `.rounds`, `.interval90` | wins, rounds, the 90% interval |
| `.per_workload.<name>`, `.per_phase.<phase>` | the same per workload and per phase |
| `arms.<arm>.per_workload.<name>.per_phase.<phase>.round_seconds` | every measured value |
| `correctness.verified_units`, `.total_units` | the oracle |
| `measurement` | how time and memory were taken, which CPUs |
| `eligibility` | the league's promotion verdict and its reasons |

The published results are such files: `league/evidence/judge/*.result.json`.

**What your numbers can be compared with.** With each other, on your machine. Not with the published numbers:
another CPU, another memory system, another kernel and another governor give another ratio. If your result differs
from ours by much, both can be right.

### 2.4 The short way

No sandbox, no oracle, no pinning; enough to see the size of the effect on your own data:

```
go build -o restic-dealer ./cmd/restic                  # in this repository
# build upstream at ba802d42 the same way as restic-upstream
export RESTIC_PASSWORD=test
for b in restic-upstream restic-dealer; do
  rm -rf /tmp/repo-$b; ./$b init -r /tmp/repo-$b
  /usr/bin/time -v ./$b backup -r /tmp/repo-$b /some/data
  /usr/bin/time -v ./$b backup -r /tmp/repo-$b /some/data
  /usr/bin/time -v ./$b check --read-data -r /tmp/repo-$b
  /usr/bin/time -v ./$b restore latest -r /tmp/repo-$b --target /tmp/out-$b
done
```

Alternate the order and repeat; one run of each proves nothing (METHODS.md, section 3). Two repositories made by two
`init` runs cut large files at different places and differ in size by a percent or more for that reason alone. To compare
sizes, copy one initialised repository for both binaries.

## 3. The layout check

The key derivation's inner loop is fast at one code alignment and about 3% slower at another (METHODS.md, section
4). To see where a build has it, make an **unstripped twin**: the same source, the same environment and the same
flags as the build in question, without `-s -w`. Those two linker flags remove the symbol table, so `go tool nm`
cannot read the judge's binary or a release binary itself; nm on a stripped file is not this check.

```
export CGO_ENABLED=0 GOAMD64=v1 GOFLAGS=-mod=readonly GOTOOLCHAIN=local
go build -trimpath -buildvcs=false -tags "selfupdate disable_grpc_modules" -o restic-twin ./cmd/restic
go tool nm -n -size ./restic-twin | grep 'crypto\.parallelSalsaXOR$'
```

For the twin of a release binary add `-ldflags "-X github.com/restic/restic/internal/global.Version=0.19.1-dev-dealer.1"`;
this is what `merge/box/rel2.sh` does. The first column is the address in hexadecimal. Its value modulo 64 was 32 in the
builds that measured fast and 0 in the build that measured slow. `merge/nm3.sh` to `nm6.sh` are the league's own
invocations (they build several trees with the pinned toolchain and print the three symbols of the derivation).
`merge/box/nmdiff2.py` compares the symbol tables of two builds to show what moved the loop. This says something about one
binary only.

## 4. The research tools

`research-tools/research_tools_v2.py` is what a builder runs during a match: `build`, `test`, `bench`, `profile`,
`sweep`, `race`, `ablate`, `timeline` and others on a candidate tree, on the research CPUs, in the judge's sandbox,
with the judge's corpora. `CONTRACT.md` is its interface as the teams were given it; `HARNESS-NOTES.md` is its
developer's log, with example invocations. It imports the judge and shares its root directory, so it needs
everything section 2.1 lists. It never touches the held-out workload. A bench is
`research_tools_v2.py bench --candidate TREE --phases a,b --workloads x,y --reps N`, with `--fixed-reps` for a fixed
number of pairs (without it the tool stops early when a phase is clearly resolved) and `--versus base` to compare
with another tree than the incumbent. Its timing is a screen: a result is reported with the noise band of its cell
and a verdict, "resolved" (beyond noise) or "unresolved".

## 5. The release checks

`checks/` is the harness of METHODS.md, section 10. Every check is one script:

```
python3 league/tools/checks/c11_prune_differential.py <tree> <label>
python3 league/tools/checks/run_all.py <tree> <label>          # the whole set, two lanes
```

`<tree>` is a restic source tree; it is copied to scratch and never written to. `<label>` names the run. Results go
to `checks/results/<label>/<check>/` (`progress.log`, `status.json`, `summary.json`, evidence files), and one line per
finished check is appended to `checks/results/<label>/status.txt`; the last line per check counts. `report.py` makes
the table.

To run it elsewhere, edit the top of `checks/lib/chk.py`:

| name | league box value | meaning |
|---|---|---|
| `LEAGUE` | `/opt/league/restic-league-v2` | root that holds `stock/` (upstream) and `toolchain/` (the pinned Go and module cache) |
| `ALLOWED_CPUS` | 16 CPU numbers of the league box | the checks pin themselves to these at import; `CHK_CPUS` can narrow the set, never widen it |
| `CHK_BEFORE_TREE` (environment) | the league tree after S019 | the unrepaired tree used as the positive control; commit 2 of this repository |
| `CHK_SCRATCH` (environment) | `/home/restic-final-checks` | bulk data; needs tens of GB for the scale checks |
| `CHK_QUICK=1` (environment) | | small sizes, for a smoke run |

Several checks need root (ownership, devices, extended attributes), `strace`, an SFTP server on the machine, and the
`big-index` workload's repository as a starting point (`lib/bigindex.py`). C10 needs hours, scratch space for a 50 GiB
file, and several GB of memory for its largest index. `checks/server/chkrest/` is a REST server that can delay, fail, hold
back or damage chosen requests; C03, C05, C08, C11, C14 and C16 use it. `checks/witness/` holds the reviewers' witness
tests as they were written, before they became tests of the tree; some assert a defect of an unrepaired tree and fail on
this tree by construction.

## 6. The tables in the documents

`league/tools/make_tables.py` reads the result files under `league/evidence/judge/`, the file `roles.json` there (which
job is the headline, which jobs the arms table lists, whether the numbers are provisional), the identity record
`league/evidence/source-identity.json`, the bench data `league/evidence/many-core.json`, the run records of the
release-check harness under `league/evidence/release-checks-final3-b8cf439/` (`x11-*/runs.json`, `c10/summary.json`), the
checksum list `league/evidence/SHA256SUMS`, and the patch files in `league/series/`. It rewrites the text between the
marker comments `<!-- BEGIN GENERATED:name -->` and `<!-- END GENERATED:name -->` in `README.md`,
`league/docs/RESULTS.md`, `league/docs/CHANGES.md` and `league/docs/RED-TEAM.md`. The 90% intervals of the per-phase cells
are computed by it from the per-round values in the result file with the judge's own function. It first recomputes the
judge's per-workload intervals the same way and stops if they do not reproduce. From the repository root:

```
python league/tools/make_tables.py            # rewrite
python league/tools/make_tables.py --check    # exit 1 if a block is stale
```

It is idempotent. `league/tools/import_result.py SOURCE NAME [BOX_PREFIX]` copies a judge result into the evidence
folder, removes the one key that holds a workstation path, replaces the box's working prefix by `/opt/league/` when
that prefix is given, and records the SHA-256 of the source file and the number of replacements in `index.json`.

## 7. Verifying a release binary

A release binary is the published source built with the judge's flags plus one linker flag that sets the version string.
Its machine code has the judged binary's symbols at the same addresses but is not byte-identical to it (METHODS.md,
section 12.2). To verify one: build the published source with the same Go version, flags and environment for that target
and compare the SHA-256 of your file with the list in the README. `merge/box/rel.sh` is the procedure the binaries were
made with; it also builds the judged binary beside the release binary and compares the two.

`helpers/verify-release-binaries.sh` at the top of this repository is upstream's script for upstream's releases. It
does not apply to this build.

## 8. The many-core bench

`merge/box/mc2.sh TREE TAG` runs the builders' bench (section 4) for upstream against a tree with 8 and then 16 whole
physical cores, `GOMAXPROCS` following, the seven phases on the four workloads, 5 fixed pairs per cell, one bench at a
time on an otherwise idle machine. `mcbench.py` adds user and system CPU seconds to every pair. The tables in RESULTS.md,
section 6.1, are generated from `league/evidence/many-core.json`, which holds those figures; the per-pair files of the
bench on the published machine code are in `league/evidence/many-core-rc9/`. It is a bench: no oracle per round, no
rotation of three arms, and the `performance` governor. `mcdirs.sh`, `mcdirs2.sh` and `h10.sh` are the directory restores
of RESULTS.md, section 6.2, on the bench lanes, and `l3dirs.sh` is the cache-layout test. The many-directory restores of
the release-check harness, the governor test among them, were made with `checks/x11_dirs_time.py` and
`checks/tools/cpufreq_state.py` (section 1).
