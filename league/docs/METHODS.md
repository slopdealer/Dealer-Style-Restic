# Methods

How the numbers and the findings in this repository were produced, in the order a reader needs them:

1. the judge: what every published number is a measurement of;
2. the four workloads;
3. noise, and how a difference is told from it;
4. the binary-layout finding: an effect of code alignment, and how it is controlled;
5. the league: teams, seasons, the ladder from a screen to a promotion;
6. the speed maps that aimed the seasons, and the ledger of closed ground;
7. the hand merges;
8. the held-out workload;
9. the soundness reviews;
10. the release checks;
11. the independent review before release;
12. how to reproduce: build flags, the release binaries, the judge, the bench tools.

The contract the league ran under and the judge's own description are published as scrubbed copies in
[`../reviews/contract/`](../reviews/contract/). This document summarises them and says where each statement can be
checked. Results are in [RESULTS.md](RESULTS.md), findings in [SOUNDNESS.md](SOUNDNESS.md) and
[RED-TEAM.md](RED-TEAM.md), limits in [KNOWN-LIMITS.md](KNOWN-LIMITS.md), the tools in [TOOLS.md](TOOLS.md).

Everything timed in the league was timed with **4 pinned CPUs**.

## 1. The judge

The judge is one Python program, `league/tools/judge/restic_judge.py`. Its own description is
[`../reviews/contract/JUDGE.md`](../reviews/contract/JUDGE.md).

**Three arms in every round.** The candidate, the incumbent and upstream (the arm is called `stock`) are built and
measured in the same job, in the same rounds. So every job gives the candidate against the incumbent and against
upstream, and the incumbent against upstream, under identical conditions. Each arm is snapshotted at the start.

**The build is the judge's, not the team's.** The build input is the pinned upstream tree plus the arm's non-test
`*.go` files under `cmd/` and `internal/`. Nothing else from the arm is used: not its tests, not its `go.mod`. The
build runs offline in a sandbox with a pinned Go 1.27.0:
`CGO_ENABLED=0 GOAMD64=v1 go build -trimpath -buildvcs=false -tags "selfupdate disable_grpc_modules" -ldflags "-s -w" ./cmd/restic`.
Upstream is rebuilt in every job and must be bit-identical to a recorded build.

**The seven phases.** Each phase is one restic process with upstream's defaults, on a fresh copy of a prepared
state. `-r /work/repo --password-file /etc/restic-password` precede every command.

| phase | starts from | command |
|---|---|---|
| backup-initial | an initialised, empty repository | `backup /data/src` (no parent snapshot) |
| backup-unchanged | a repository holding one backup of the corpus | `backup /data/src` (the same corpus; nothing changed) |
| backup-mutated | the same | `backup /data/src` (the corpus with the mutation set applied) |
| restore | the same | `restore latest --target /work/restore` |
| check-read-data | the same | `check --read-data` |
| forget-prune | a repository with three snapshots | `forget --keep-last 1 --prune` (big-index adds `--keep-tag ballast`) |
| ls-json | a repository holding one backup | `ls --json latest` |

The prepared states are written by upstream, unmeasured, at the start of each job, and verified against frozen
manifests before anything runs. The initialised repository is one frozen copy, so every arm has the same config, keys
and chunker polynomial.

**Conditions.**

- **Cold cache.** restic's local cache starts empty in every phase.
- **tmpfs.** Corpus, repositories, restore targets, temporary files and cache are in memory.
- **4 CPUs pinned.** Four physical cores of one chiplet, `GOMAXPROCS=4`, affinity inherited by every child. The
  judge sleeps while restic runs.
- **Sandbox.** Every restic process runs in bubblewrap as PID 1 of new PID, network, IPC, UTS and cgroup
  namespaces, with a read-only root, no network, and the environment reduced to `HOME`, `TMPDIR`, `GOMAXPROCS=4`,
  `TZ=UTC`, `USER=root`. `GOGC` and `GOMEMLIMIT` are unset.
- **The performance governor.** From 2026-10-01 (S013) the judge's CPUs run the `performance` frequency governor;
  the rest of the machine stayed on `schedutil`. With `schedutil` a short, bursty phase was timed on a clock that was
  still ramping. The change cut the per-round time noise between identical arms from 1.11% to 0.27% (section 3).
  The setting is not persistent and is re-applied after a reboot (`league/tools/judge/set_judge_governor.sh`).
  **Every league timing since then, the official 44 rounds included, is therefore a timing at full clock.** Most
  machines let the clock follow the load. What this build does under such a governor was measured only afterwards
  and only by the release-check harness; in one kind of restore it loses to upstream there (KNOWN-LIMITS.md,
  section 2).

**What is measured**, in the judge's own words from the result files:

- time: "parent-side time.monotonic() around the sandboxed process (includes ~8 ms bwrap setup, identical for all
  arms)"; T = "sum of phase wall seconds (per workload round)";
- memory: "wait4() ru_maxrss of the bwrap monitor whose only child is restic (namespace PID 1): max(monitor ~11 MiB,
  restic VmHWM at exit)"; M = "mean over phases of phase peak RSS". The judge never forks the measured process
  itself: a child's `ru_maxrss` starts at the resident size of the process that forked it, and the first version read
  828 MiB for every phase, which was the judge. A small static launcher does the fork.

**Pairing and order.** Within a round every phase runs all arms back to back; the order of the arms rotates through
the rotations and reverses of the arm list (six patterns for three arms). The positions balance exactly over complete
cycles of the six patterns and approximately over an incomplete one. In the final 44-round job, with 11 rounds per
workload, this build ran first, second and third in 12, 16 and 16 rounds (RESULTS.md, section 1.2, prints the counts
for all three arms). Time saved in a round is 100 x (1 - T of the candidate / T of the reference), and likewise for
memory. The judge reports the median over rounds, the number of rounds won, and a 90% bootstrap interval of the
median.

**A win** is a round in which the candidate's value against the reference is above zero.

**The suite number.** With several workloads the rounds rotate over them (round 1 `main`, round 2 `big-index`, round
3 `large-files`, round 4 `remote`, and again; the arm order changes every four rounds). The suite figure is the mean
over workloads of each workload's median paired value; a round counts as a win when its own workload's value is above
zero. Seconds for the suite are the sum of the workloads' medians; memory is the mean.

**The correctness oracle.** No number counts unless every arm matched upstream:

- before round 1, an unmeasured warm-up runs every phase for every arm with the full oracle: for each backup, upstream
  runs `check --read-data` on the repository the arm wrote and restores the arm's snapshot, and the restored tree
  must equal the expected manifest; after forget-prune the same; for check-read-data the arm must detect a corrupted
  pack (one flipped byte). With the restore phase, in which the arm restores a repository upstream wrote, that is a
  cross-read in both directions;
- in every measured round, for every arm and phase: after a backup, upstream's `snapshots --json` of the arm's
  repository must show the same snapshot count, parent, paths, host, user and summary counters as upstream's own, and
  the same root tree ID (a different ID triggers the slow path: upstream's check and restore and a manifest
  comparison); after restore, the full manifest of the restored tree must equal upstream's restore; check must exit
  0; after forget-prune the kept snapshot IDs must equal upstream's and upstream's `check --read-data-subset=k/5`
  must pass, with k rotating so that every five rounds read every pack; `ls --json` output must parse to the same
  lines as upstream's.

One oracle failure aborts the job. The result files record the count of verified units (1,019 of 1,019 in a 44-round
suite job).

**The compatibility tests.** Upstream's own test files for thirteen internal packages and the whole `cmd/restic`
integration suite are compiled against the arm's code and run in the sandbox. An arm passes if it has no failure that
upstream does not also have there and every test that passes for upstream passes for it. (In that sandbox upstream
itself fails 20 tests for environmental reasons: no FUSE, no `python` binary, user-namespace ownership.) A
consequence for the teams: the internal APIs those tests call are frozen.

**The league's verdict.** Beside the numbers the judge states whether the candidate could be promoted, and why
not. The gates, as the result files record them (`eligibility.policy`): at least 29 winning rounds of 44; a suite
score of at least +0.25% against the incumbent; ahead of upstream; **the memory guard**: no more than 2% more peak
memory than the incumbent, on the suite and on every workload separately; **the per-workload gate**: no workload
more than 1% slower than the incumbent; and neither axis more than 5% worse. The tree in this repository fails
these gates against S020, by design of the repairs, and the judge says so (RESULTS.md, section 1.2).

**What the judge cannot see.** Two commands at once. Behaviour on a real disk or network. Any platform but Linux
x86-64. More than 4 CPUs. A CPU clock that follows the load. Options other than the defaults. The league's worst defect lived in the first of these.

## 2. The four workloads

All are generated once from seeds by `league/tools/judge/workload_gen.py`, frozen, and verified by hash before every
job. Full descriptions: [`JUDGE.md`](../reviews/contract/JUDGE.md), [`SUITE.md`](../reviews/contract/SUITE.md),
[`REMOTE.md`](../reviews/contract/REMOTE.md).

| workload | scored from | what it is | why it exists |
|---|---|---|---|
| `main` | S001 | 91,264 entries, 1.64 GiB: 90,000 small files in about 1,000 directories (sizes log-normal around 2 KiB; 40% random, 40% compressible text, 15% exact duplicates), 160 medium files, three incompressible large files, two deduplication-heavy files, a sparse file, and a block of special entries (odd names, symlinks, hard links, extended attributes, a FIFO, unusual modes, a 30-deep path). The mutation set changes about 4% of it: 4,224 operations | the general case: many small files, per-file and per-process overhead |
| `big-index` | S017 | `main`'s corpus in a repository that already holds 1,500,000 unique 48-byte blobs (523 packs, 8 index files) referenced by one old snapshot | index load, index memory and prune planning at a size where they dominate; `main`'s index is small |
| `large-files` | S017 | 3,217 entries, 2.91 GB apparent: a sparse disk image with scattered in-place writes, a database file of 8 KiB pages with page rewrites, four compressible logs that grow, three incompressible media files, a small tree | chunking, hashing and compression throughput; in-place changes inside large files |
| `remote` | S019 | `main`'s corpus through restic's REST backend to a server with a simulated link | the number and size of requests, which nothing else measures |

**The remote link is simulated.** The server (`league/tools/judge/restserver/main.go`) runs on the same machine and
is reached through a Unix socket. It sleeps 30 ms before it looks at each request, paces request and response bodies
at 48 MiB/s per connection, and admits 5 requests at a time; further requests queue. The delays are sleeps, never a
busy wait. It stores a normal local-layout repository, so upstream can read it directly for the oracle. Not
simulated: TLS, HTTP/2, loss, jitter, the server's own cost of listing, slow start. What that can tell a user: whether
a build makes fewer round trips and uses its connections fully. What it cannot: anything about a particular provider,
a link that is limited as a whole, or a lossy line. When `remote` was first scored (S019), every team found within one
match that the incumbent was issuing five times upstream's requests in restore; nothing had measured it before.

## 3. Noise and what was done about it

**The null calibration.** The judge can run with identical arms, built and measured separately. The difference
between identical arms is the instrument's noise. The calibration of 2026-10-03 (main workload, 12-round jobs;
[`NULL-20261003.md`](../reviews/contract/NULL-20261003.md)):

| null | paired values | median time difference | SD of the time difference per round | SD of the memory difference |
|---|---|---|---|---|
| judge's CPUs, upstream against upstream | 72 | -0.01% | **0.27%** | 2.48% |
| research CPUs, upstream against upstream | 36 | +0.11% | 2.32% (robust 0.34%) | 2.20% |
| the first calibration, 2026-09-24, before the governor change | 54 | +0.03% | 1.11% | 2.22% |

Standard error of the median time on the judge's CPUs: 0.14% at 6 rounds, 0.10% at 12, 0.05% at 42. Per phase, the
SD of the paired time difference: backup-initial 0.61%, backup-unchanged 0.53%, backup-mutated 0.92%, restore 0.51%,
check-read-data 0.75%, forget-prune 1.19%, ls-json 0.27%. Per-phase memory: 1.29% (ls) to 6.89%.

The remote workload has its own bands, from an identical-arm run on 2026-10-05: whole workload 0.95% for a
research-lane probe; per phase up to 5.8% (backup-mutated) and 3.85% (check). On that workload the league's tree
varies more from run to run than upstream: restore and check each take one of two values.

**Stalls.** On the research CPUs 2 measured runs in 504 took about 2.5 s longer than their siblings while other work
ran on the machine. A single phase reading that is off by seconds is a stall, not a result, and is repeated.

**Why a single screen was never trusted.** The league's own inversions:

| season | what the screen said | what the longer measurement said |
|---|---|---|
| S002 | T1, 6 rounds: score +1.29 | 42 rounds: -0.53 (15 of 42 wins). Not promoted |
| S003 | T1, 6 rounds: +1.47 | 42 rounds: +0.49, 27 wins where 29 are required. Not promoted |
| S007 | T3, 6 rounds: +2.08 | 42 rounds: +0.014 (21 of 42; interval -0.22..+0.71). Not promoted |
| S014 | T2, 6 rounds: +0.81 | 12 rounds: +0.40. The confirmation went to another tree |
| S017 | T1's two best rows: +2.44 and +2.35 | 12 rounds: +2.13 and +2.68. The order reversed; the second confirmed at +1.93 |
| S020 | T2: +1.81 | 12 rounds: +1.41; 44 rounds: +1.03 |
| S020 | a restore cell: large-files +8.54% in two rounds | +0.09% at twelve |

The rule that follows: a row whose interval touches zero is a lead, not a result; rows shrink by a factor of two to
three at confirmation unless their interval was clear of zero by a wide margin (S004's +5.50 confirmed at +5.25,
S008's +9.12 at +8.42).

**Two judge jobs side by side inflated prune cells.** In S020 a big-index forget-prune cell read +22.54% in a match
row and +23.83% at its rescreen, both while a second judge job ran beside it. The cell read +6.70% in the 44-round
job, which ran alone. Another team's cell: +22.51% beside a job, +4.75% alone. The check phase did not show it (+13.57
/ +12.56 / +13.88). Index walking that is bound by memory traffic would behave this way; it was not investigated
further. Consequences: the final 44-round jobs ran with no other judge job and none of the merge agent's benches
beside them, and prune cells in the final merge were repeated on a quiet machine. A bench beside one judge job did not
show the effect (final merge report, finding 5). The release-check harness, pinned to other CPUs, was running on the
same machine during the 44-round jobs; RESULTS.md, section 1, says so.

**Fixed pairs: how a bench is read.** Outside the judge, every timing in this repository that is called a "bench"
comes from the builders' research tool (`league/tools/research-tools/research_tools_v2.py`). It runs the two trees
on the named phases from the same prepared repository states the judge builds, in interleaved A/B order, as pairs;
"8 fixed pairs" means eight such pairs with no early stopping. A result is the median of the paired differences,
the pairs won, and a verdict against a noise band:

- the per-pair spread is the larger of the pre-measured null for that workload, phase and governor and the spread
  of the call itself, both as 1.4826 times the median absolute deviation;
- the band is 2 x 1.2533 x spread / sqrt(pairs), with a floor of 0.3%;
- a verdict ("beyond noise") needs the median outside the band **and** 80% of the pairs on its side. Anything else
  is reported as "unresolved".

A bench is run on the research CPUs, not on the judge's, has no three-arm rotation and checks the oracle once, not in
every round. It is the instrument for taking a difference apart; it is not the instrument that decides one. Every
bench number in RESULTS.md says how many pairs it has and whether it is beyond noise or unresolved.

**Probes with bands.** From S017 the builders' probes report against bands derived from the null: 0.5% for a
whole-workload time difference, 0.3% to 1.35% per phase. A difference inside the band is reported as "unresolved".

## 4. The binary-layout finding

restic derives its repository key with scrypt in every process. The league's tree runs that derivation in parallel
lanes in its own function (`internal/repository/crypto/parallel_scrypt.go`), whose inner loop is
`crypto.parallelSalsaXOR`.

**The effect.** In the judge's build that loop's speed depends on where the linker places it. At an address that is
32 modulo 64 it runs at full speed; at 0 modulo 64 the derivation is about 3% slower, 6 to 7 ms in every restic
process.

**How it was found.** Twice, the same way: a phase read slower although none of its code had changed. In the hand
merge after S019, code added in front of the derivation in seven packages moved it. In the repairs, R3's parameter
checks were put at the top of the derivation function and inlined there: the function grew from 1,061 to 1,605 bytes
and the loop behind it moved by 17 steps of 32 bytes. The official job on the repaired tree (`3c21cf80`) read
`ls --json` on large-files at -1.65% (0 of 3 rounds) with no `ls` code changed; `ls --json` is nearly all start-up.
The symbol tables of the builds confirmed it: the league tree after S019 had the loop at `0x823ba0` and S020 at
`0x7effa0`, both 32 modulo 64; the repaired tree at `0x823d80`, 0 modulo 64.

**How it was controlled.** The lanes function was restored byte for byte and the checks moved into a wrapper in
another file, in front of the lanes (commit "R3 (layout)"). The checks, their order and their error texts are
unchanged. After that the same phase read +1.36% (10 of 10 pairs) against the tree before the commit. The first final
tree's address: `parallelSalsaXOR` at `0x823d20`, 32 modulo 64. It was checked again after every later change to the
files in front of the loop. In the build of the judged commit it is at `0x8241a0`, 32 modulo 64. The release procedure
reads it from an unstripped twin of the release binary each time (section 12.2).

**What it is not.** It is not a property of the code. It restores one build's layout. A binary for another platform,
another Go version or other flags lands where it lands, and a later edit to the files in front of the loop can move
it again. To check a build, make an **unstripped twin** of it: the same source, environment and flags (section
12.1), without `-s -w`, the two linker flags that remove the symbol table. For a twin of a release binary keep the
version flag in `-ldflags` (section 12.2).

```
go build -trimpath -buildvcs=false -tags "selfupdate disable_grpc_modules" -o restic-twin ./cmd/restic
go tool nm -n -size ./restic-twin | grep 'crypto\.parallelSalsaXOR$'
```

and take the address modulo 64. `go tool nm` on the judge's binary or on a release binary itself is not this check:
those files are stripped and have no symbol table to read. The tools used for the finding are in `league/tools/merge/`
(`nm3.sh` to `nm6.sh`, `box/nmdiff2.py`). A related reading of S020: its `ls --json` read -1.08% on main and -0.91% on
remote against its predecessor with no `ls` file changed. The merge report traces that to the binary as a whole being
laid out differently, not to a code path (final merge report, finding 2).

## 5. How the league works

**The task.** Make restic faster on a fixed judge without changing what it does: same repository format, same
commands, defaults, output and exit codes, same chunking and blob IDs, no change to the 200 ms lock wait, no
detection of the workload, no busy-waiting to hold the CPU clock up. Only non-test `*.go` files under `cmd/` and
`internal/` may change; `go.mod` and `go.sum` must equal upstream's. The judge enforces what it can
(section 1); rulings by the operator settled the rest and are recorded in the contract
([`INTERFACE.md`](../reviews/contract/INTERFACE.md)).

**Teams and roles.** From season S002 there were four teams, T1 to T4. Each team seats the same roles, every one of
them an AI agent with its own brief:

- an **owner**, who chooses the team's direction for the season and drafts the rest of the team;
- a **subject-matter expert**, who briefs the owner and the coach on restic and on what is already installed;
- a **coach**, who turns the direction into a plan for each match and assigns questions;
- three **inventors**, who propose mechanisms;
- three **builders**, who experiment with the team's research tools, then build the candidate together, one of them
  integrating.

The order of work in a match is: expert and owner, owner's draft, expert and coach, coach's plan, inventors,
builders' experiments, the candidate build, the judge.

**Seasons and matches.** A season starts from the incumbent, the last promoted tree. It has four matches. In a match
every team submits one candidate tree, which the judge measures against the incumbent for 6 rounds (8 rounds from
S019, so that each of four workloads is measured twice). A team keeps building on its own earlier trees within a
season; from S008 a change that won a match is locked, so that a team cannot rework its own winning code into a loss.
A candidate identical to the incumbent scores zero (from S003; before that, unchanged trees ranked above slightly
negative attempts and teams stopped submitting). Match points come from the whole-workload rank and, from S005, from
per-phase cells.

**From screens to a promotion.** A match row is a screen, not a result (section 3 gives the league's own failures).
At the end of a season:

1. the three best rows are measured again in a **12-round rescreen** (from S013);
2. the best rescreened tree gets one **confirmation**: 42 rounds, or 44 from S019 (11 per workload);
3. it is promoted if it passes the gates: at least 29 winning rounds, a median score of at least +0.25% against the
   incumbent, ahead of upstream, and (from S013) no more than 2% more peak memory than the incumbent. From S017 the
   memory guard and a time floor of -1% apply to every workload separately.

Three seasons promoted nothing (S002, S003, S007), and in two more (S014, S017) the rescreen changed which tree went
to the confirmation; the table is in RESULTS.md, section 7.

**Aiming.** From S004 each season had a directive written from a measured map of where the time goes (section 6):
a menu of mechanisms with measured bounds, what is already installed, and the rulings in force.

**Models.** The teams ran on the models the season reviews record: one team on a provider's unnamed preview model
in S001; GPT-6 Luna at maximum effort in S002 to S011 and S013 to the middle of S016; GPT-6.1 Sol at high effort in
S012; GPT-6.1 Sol at medium effort from the middle of S016 to S020. The S012 review compares the two families: Sol at
high effort matched or slightly beat Luna at maximum effort on quality and took longer (12.8 h against 10.4 h).
Seasons took 9.3 to 20.3 hours. Every season review records $0 of metered API spend: the agents ran on a
subscription or a free tier.

**Research tools.** Builders do not wait for the judge to learn whether an idea works. They have a set of tools
that run their tree on separate CPUs of the same machine in the judge's sandbox: build, test, a quick bench of chosen
phases, a profile, an ablation against a base tree, a race check, an output comparison
(`league/tools/research-tools/`). From S017 a probe reports its result against a noise band ("resolved" or
"unresolved"), so that a one-round reading inside the noise cannot decide a design.

**Roster rotation.** From S019 the seats were rotated between seasons. A team that finishes last gives its owner a
strike, and an owner with three strikes is replaced from a bench of owners. Builders and coaches are benched by a form
table built from past seasons and replaced by new ones. Inventors are credited when an idea of theirs is built, and
that record decides their rotation. The S019 review credits the rotation with T2's first promotion in eighteen seasons
under a new line-up; one season is not evidence either way.

**The engine** that runs all this (seats the roles, hands out workspaces, queues judge jobs, applies the gates) is
not published; `league/tools/league/launch.py` is the record of how each season was configured.

**How this compares with other optimiser loops.** The league is a population search with an expensive, trusted
evaluator and a gate that a candidate must pass at a longer measurement than the one that selected it. Its
distinguishing choices are the measured map before each season, the three-stage evaluation, and keeping one arm of
every measurement on unmodified upstream.

## 6. The speed maps

A map is a measurement of the incumbent, made before a season: where the time (and, until S012, the memory) goes, per
phase and per workload, by profile and by counting requests and system calls; then, for each place where time could
be removed, a **prototype** that removes it crudely and a measurement of what that buys. The map ranks levers by
measured removable cost, not by how interesting they look, and says what each would cost in memory. The season's
directive is written from it.

Ten maps were made ([`../reviews/maps/`](../reviews/maps/)): the first for S004 (measured on the S001 tree), the
second for S008, the third for S011, then seven more between S013 and S020. S014 ran without a new one, and its review
counts that as the reason its first two matches were noise. The first three are cost maps (time and memory, matching
the equal-weight score); from the fourth they are speed maps.

What the maps did:

- S001 to S003 searched without a map and circled one corner (buffer ownership in the backup path). The first map
  showed that about 20% of the workload was per-process start-up (the key derivation on one core, the fixed lock
  wait, the index load), that read paths used two of four cores, and that `ls --json` wrote line by line. S004
  confirmed +5.25%, the largest gain to that point.
- The second map's prototypes stacked to +8.7; S008 confirmed +8.42%.
- The fourth map's two top levers stacked to 0.77 s; S013 confirmed 0.72 s.
- A map entry that was wrong to follow: the tenth map led with work inside the lock wait, with a bound of +4.9
  points. All four teams built it in S020. It did not pay (the three promoted mechanisms cost 0.14 points together)
  and it was unsound (SOUNDNESS.md, section 5).
- A cost of maps: in S019 the map put one lever at the top with a measured size, and all four teams built the same
  thing first; twelve of sixteen rows were that lever.

**The ledger of closed ground.** Beside each map the league kept one file of everything that had been tried and
closed, with the measurement that closed it and the scope it was closed for (a mechanism closed "on the main
workload under the memory guard" is not closed elsewhere). Each season's directive carried it and the builders could
search it from their tools. Its purpose is the one
S001 to S003 showed the need for: without it, teams rebuild what was already measured. Its final form is the
inventory published as [`../reviews/INVENTORY.md`](../reviews/INVENTORY.md) (section 4, "Closed", in fourteen
groups), from which RESULTS.md, section 11, is drawn.

## 7. The hand merges

Only one tree is promoted per season, and the teams' gains are often in different phases. From S017 the league tried
to merge the best trees per phase automatically at the end of a season, by file. It found nothing to merge in any of
four seasons: the teams edit the same files. So the unpromoted work was merged by hand, by a merge agent, and each
merge was confirmed like a season's nominee:

- after S004, a synthesis of two teams' memory work onto the promoted tree (score against upstream +8.57% to +15.07%);
- after S017: three unpromoted ideas and two map prototypes; job `b740c9e2`, 42 rounds: +6.06% against S017 (33 of
  42), nearly all of it big-index (+17.90%);
- after S019: job `014cbfda`, 44 rounds: +1.14% against S019 (36 of 44; interval +1.05..+1.27);
- **the race fix** after S016 was a repair outside the season cycle: the overlap in the lock wait listed the index
  before the snapshots, and a restore beside a backup failed about one run in three. It was installed as the
  incumbent without a season.

**The final merge** joined the repaired tree with what was sound in S020 and with ports of unpromoted work. Its
rules: nothing new inside the lock wait; every piece measured alone before it went in, with fixed pairs and the
bands of section 3; every port with a statement of which of upstream's contracts it touches and a test against a
verbatim copy of upstream's function where one exists; a kill test with real binaries for the port that touches
prune; then official judging at 12 rounds and at 44 rounds with no other judge job or bench beside it. What went in, what was left out and
why: [`../reviews/merge/FINAL-MERGE-REPORT.md`](../reviews/merge/FINAL-MERGE-REPORT.md) and RESULTS.md, sections 5
and 11.

## 8. The held-out workload

**What it is.** A second corpus that differs from `main` on every axis `main` fixes: many more and much smaller
files in deep and very wide directories, a sparse image with scattered in-place rewrites, a page-structured file,
append-heavy logs, directory moves, time-stamp-only changes, and a history of seven snapshots so that forget and
prune do real work. Same seven phases, same oracle. It was never scored and never part of a suite.

**How it was kept from the teams.** Teams could not select the held-out workload. The transcript audit found seven
directory-name sightings in S017, and no recorded reading of its contents. After S017 it was kept off the judge
machine during seasons. In detail: it was not selectable by any role and not named in any brief or contract text given
to a role. The teams' roles ran with broad access on the league box (section 1 of KNOWN-LIMITS.md), so from the S017
boundary the operator kept the corpus and its tooling off the box during seasons. At a boundary the season ends, the
role transcripts are audited, the corpus is restored and verified against its manifest, the transfer job runs, and the
corpus is sealed and removed again.

**The audits.** Before each transfer test every file a role could have seen was searched for any name of the set. At
the S018 boundary: 31,721 files, 7 hits, all of them the folder's name in directory listings that builders made of the
league's root in the last match of S017, before the corpus was first removed from the box. No role opened the folder
or named it in a command. During S018 itself, 0 hits. At the S019 boundary: 40,951 files, 1 hit, a false positive on a
search word in a directory listing; a second scan of every line of the same files found nothing more. So the teams
could have seen that a folder existed, for part of one season. Nothing in the record shows that any of them read it.

**The results** are in RESULTS.md, section 9: five incumbents, S015 to S019, each within about one point of its gain
on `main`. **The tree in this repository has not been run on it**, and neither has S020.

Its seed, its location and its tools are not published, so that it can be used again.

## 9. The soundness reviews as a method

The judge's oracle is a differential test against upstream on fixed inputs. It cannot show that a mechanism is
correct on other inputs or other schedules. After S019 the league's tree was therefore reviewed, and the method was
fixed in advance:

- **Scope by file, not by sample.** Every non-test Go file that differs from upstream (116 files, about 24,000
  lines) was assigned to one of three areas: storage (57 files), backup (32) and the read side (27). One independent
  reviewer agent per area.
- **Mechanism by mechanism, against a stated contract.** For each mechanism the reviewer writes what upstream
  guarantees at that point (stored bytes, the order of listings, error texts, exit codes, what is verified before
  what is used, memory bounds) and gives a verdict: sound, sound under a stated contract, or a finding. 81 verdicts.
- **A finding needs a witness.** A test or program that fails on the tree and passes on upstream. A suspicion
  without a witness is listed as "needs a run", not as a finding.
- **The authors' tests are mutated.** A test is reported if it still passes when the mechanism it guards is broken.
  The league's own test file for one package did not compile at all (R6).
- **A repair is one patch with a regression test** that fails before it and passes after. For the blocker, two designs
  were built and compared on a randomized schedule test and on measured cost before one was chosen.
- **The reviewers' witnesses become tests of the tree.**
- **Experiments the reviewers could not run** (they worked without the Linux box) were listed and became the release
  checks.

The same method was applied to S020's additions, then to the 16 commits of the final merge (with 17 mutations of the
ported code), then in an addendum to three later commits.

**A second family of reviewers** made the last review, from upstream's source and the tree alone. That method is section 11.

**What the reviews did not cover.** They are code review with tests, by AI agents. No formal verification. No review
by a restic maintainer. Platforms other than those the reviewers ran on were inspected, not executed. The last commit
of this tree came after the last addendum.

## 10. The release checks as a method

The release checks are 16 scripts that run real binaries on Linux x86-64 and compare the tree with upstream. Where a
finding is being checked, they compare the tree with the unrepaired tree as a positive control. They ran pinned to 16
logical CPUs away from the judge's, under a 16 GiB memory limit. Each is `python3 cNN_name.py <tree> <label>`;
`run_all.py` runs the set in two lanes. Results for this tree: SOUNDNESS.md, section 8.

| check | what it does | a failure is |
|---|---|---|
| C01 | the tree's own tests and upstream's: amd64 as root and as an unprivileged user, with the debug log on, and as 386 | a failing test that does not also fail for upstream in the same environment |
| C02 | the tests under the race detector; the reviewers' witnesses | a data race; a witness that fails where it should pass |
| C03 | the R1 schedule end to end: a backup whose lock-file write is delayed, racing `forget --prune`, judged by upstream's `check`; 480 trials per tree; also the S015 race reproduction | one broken snapshot from the tree under test |
| C04 | kill -9, SIGINT and backend faults during backup, prune and repair; then upstream's `check --read-data`; 400 trials | a repository upstream rejects, or a hang |
| C05 | a corrupted pack through every route of `check --read-data`, local and through REST with 2 connections | a route that does not report what upstream reports |
| C06 | cross-read in both directions over repository versions, compression modes and pack sizes; upstream's fixtures; repository size from one initialised repository | any difference; a size outside 0.1% + 64 KiB |
| C07 | tree IDs for special files (sparse, hard links, FIFOs, devices, extended attributes, ACLs, non-UTF-8 names, a 5 GiB file, an unreadable file) and restore; the 386 binary | a tree ID or a restored entry that differs from upstream's |
| C08 | 75 restore cases over the option matrix; restore under 8 backend failure patterns and over SFTP; `repair packs` with one connection; prune through REST and SFTP | a different result or exit code; a hang |
| C09 | the Linux-only code paths as root, unprivileged and as 386; output on failing writers (R6); tree names (R7); wrong passwords (R9) | a difference from upstream |
| C10 | scale, with synthetic sizes: A, loading 500 index files behind 50 ms of latency on 1 and 2 CPUs; B, restores of 606,060 to 2,020,200 directories; C, a restore of one 50 GiB **sparse** file (a few written regions), with and without `--sparse`; D, a small backup that loads a **synthetic** index of 3, 10 and 30 million invented blob entries with no packs behind them. C and D do not test a 50 GiB file of incompressible data or a fully populated repository of that size | memory: peak above 1.3 times upstream's plus 64 MiB. Part A also fails above 1.25 times upstream's time plus 0.5 s, or when fewer index files are downloaded at once than upstream downloads. The sparse restore also compares the space allocated. Other timings are reported, not judged |
| C11 | prune against upstream on a 1.5-million-blob index: 12 pairs over 7 option sets | a different plan, different packs kept or removed, different blobs, a stored form that differs, a repository upstream rejects |
| C12 | kill -9 during the repack of 24,724 index entries; 40 trials | a repository upstream rejects; a second prune that does not finish |
| C13 | what S020 added: its Linux-only tests, 3,000 randomized R1 schedules, the race detector, and `strace` for opens of a FIFO or device during a backup's lock wait | any failure; any such open |
| C14 | prune with a backend that fails in the middle of a repack: 49 disturbed prunes on the judged commit `b8cf439` (42 in the earlier run on `rc-7`) | a repository upstream rejects |
| C15 | `backup` output at verbosity 0 to 3 against upstream, line by line | a differing line |
| C16 | `copy` end to end and upstream's copy tests in three set-ups | a difference |

**The checks had defects of their own**, and the log records each:

- C10 read peak memory with `wait4` on a child of the check script, and on Linux a child's `ru_maxrss` starts at the
  resident size of the process that forked it. After building a manifest of 2 million directories the script was 1.8
  GiB, so every later reading was at least 1,814 MiB. Those cells were declared void and a 10 MiB helper process now
  starts the measured program. (The judge had met the same trap and solved it the same way.)
- C06 let each binary initialise its own repository and compared sizes; `init` draws a random chunker polynomial.
  The alarm it raised was withdrawn (SOUNDNESS.md, section 4).
- C08's FIFO cases are killed at a time limit and left lock files behind; the steps after them that need an
  exclusive lock were void until the check removed the files.
- C09 counted status lines whose number depends on timing.

**For the judged commit the harness was extended**: a kept index file damaged between load and rewrite (C11), a
damaged blob while every upload is held (C14), a copy with damaged or missing source packs and a stalled destination
(C16), the tree's opt-in tests (C13), and the many-directory diagnostic at three sizes with 16 CPUs and with 4
cores. `restic version` is no longer compared with upstream's, because the build names itself.
([SOUNDNESS.md](SOUNDNESS.md), section 8.1.)

**Where they ran.** All 16 checks passed on the judged commit `b8cf439`. On the published commit `8f9372b` only the
comparison of the two exports and the one added test were run (diagnostic x13). The harness's CPUs ran the `schedutil`
frequency governor, not the judge's `performance`. Its timings are the only ones in this repository taken under a
load-following governor, and they are incidental readings, not benchmarks (RESULTS.md, section 6.3).

**What the checks did not do** is listed in KNOWN-LIMITS.md, section 8.

## 11. The independent review before release

The soundness reviews of section 9 were made by reviewer agents of one model family, who worked from the league's
own description of its mechanisms. Reviewers who share a description share its blind spots. So the tree that had
passed everything else was given once more to reviewers chosen to share as little as possible:

- **a different model family** from all earlier reviewers;
- **only two source trees**: upstream and the tree. No report, no list of mechanisms, no repairs, no history;
- **a contract stricter than the project's own claim** ("the same stored bytes", "every integrity check that
  upstream performs is still performed on the same data"), so that every place where the tree fell short of the
  strictest reading would be reported and could then be either fixed or stated;
- **one focus area each** (locking and concurrent processes; prune, repack, index and check; the backup write path,
  encryption, compression and restore), with files to start from and questions to answer;
- **a severity scale fixed in advance**, in which a missing integrity check counts as the highest level;
- **a test for every finding**, written by the reviewer inside the tree, so that a finding is a failing program and
  not an opinion;
- a limit of about one hour each; they took about 40 minutes.

The triage was made by the coordinator, finding by finding, with one rule: a finding is fixed unless the fix would
remove a mechanism whose difference from upstream can be stated exactly and accepted knowingly; then it is
documented, and the published claim is changed to what is true. Each fix is one commit with a regression test for
the repaired finding, seen failing before and passing after. The reviewers' original witnesses for the two
documented differences remain in the review reports; where a witness had to be adapted to become a test of the tree,
the final review records the adaptation and its reason.

The fixes were then reviewed by an independent reviewer agent as in section 9, with two additions: the reviewers'
original tests were run unchanged on the tree they had reviewed, to confirm that every finding reproduces before
anything was fixed; and the fixes were mutated (25 mutations) to find tests that would not notice a broken fix.

What was found, what was done and what the limits of the exercise are: [RED-TEAM.md](RED-TEAM.md).

## 12. How to reproduce

### 12.1 The build

Every binary the judge measured was built by the judge, from upstream's tree plus the arm's non-test Go files, with
a pinned toolchain and no network:

```
export CGO_ENABLED=0 GOAMD64=v1 GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local
go build -trimpath -buildvcs=false -tags "selfupdate disable_grpc_modules" -ldflags "-s -w" ./cmd/restic
```

Go 1.27.0, linux/amd64. With the same toolchain, flags and source, the build is reproducible bit for bit. The merge
agent's release procedure rebuilt the judge's binaries of the second final tree and of upstream and got the SHA-256
values recorded in the judge's result file (`arms.<arm>.binary_sha256` in
`league/evidence/judge/final2-rc7-44r.result.json`).

To build the commit that was judged rather than the published source, check out the parent of the last source
commit (`git checkout HEAD~2` in this repository; RESULTS.md, section 2).

### 12.2 The release binaries

A release binary is built from the published source with the judge's flags and one more linker flag, which sets the
version string:

```
go build -trimpath -buildvcs=false -tags "selfupdate disable_grpc_modules" \
   -ldflags "-s -w -X github.com/restic/restic/internal/global.Version=0.19.1-dev-dealer.1" ./cmd/restic
```

with `GOOS` and `GOARCH` set for the target and the environment of section 12.1. The procedure is
`league/tools/merge/box/rel.sh` (and `rel2.sh`), which also builds the judged binary beside the release binary,
compares the two byte by byte, reads the address of the key-derivation loop from an unstripped twin (section 4),
and writes `SHA256SUMS`. To verify a downloaded binary: build it yourself this way and compare its SHA-256 with the
published list (README, "Downloads").

**A release binary is not the judged binary, byte for byte.** What the extra flag changes was recorded for the
linux/amd64 binary built from the published commit (merge step log, entry of 2026-10-07 19:26Z):

- its `.text` section has the size of the judged binary's (14,722,353 bytes), and all 37,880 text symbols are at the
  same addresses with and without the flag;
- its `.text` bytes are **not** equal to the judged binary's: the version string the flag sets is 20 bytes shorter
  than the one in the source, which moves read-only data and so changes address operands in the code;
- the same tree built without the flag has the judged `.text` exactly (RESULTS.md, section 2);
- the key-derivation loop is at `0x8241a0`, 32 modulo 64, as in the judged build (section 4);
- timed against the judged binary, `ls --json latest` of a 90,000-file snapshot, 20 fixed pairs: median paired
  difference -1.78 ms (-0.19%), 12 of 20 pairs. No difference was resolved.

So the published numbers are those of the judged binary; the release binaries carry the same machine instructions
at the same addresses with different address operands and were not themselves judged. No difference was resolved in
this one-phase, 20-pair measurement; it does not establish that there is none. The other targets (linux/arm64, darwin, windows) were built and not timed. RESULTS.md, section 2,
sets the three builds side by side.

**Rebuilding a release binary.** The merge agent's log records that this build procedure reproduces the judge's
own binaries bit for bit (it rebuilt the judge's binaries of the second final tree and of upstream and got the
SHA-256 values in the judge's result file). On that basis a reader who builds the published commit with the command
above, the environment of section 12.1 and the same Go toolchain should get the published file bit for bit.

**An independent rebuild reproduced the five release binaries bit for bit.** One of the two reviewers who checked this
write-up before publication rebuilt all five offline from the published source with the command above (Go 1.27.0,
`CGO_ENABLED=0`, `GOAMD64=v1`, `GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, read-only modules) and got the five
published SHA-256 values. The same reviewer rebuilt the judged commit and the published commit with the judge's flags
and got the two hashes and the 60 differing offsets of RESULTS.md, section 2. The records carried here are
`league/evidence/independent-rebuild/offline-targets.json` (four of the five targets, rebuilt and published hash side
by side) and `offline-proof.json`. The reviewer's report, which lists the fifth target (linux/amd64) with the
published hash, is not part of this repository.

**The release** is tag `v0.19.1-dev-dealer.1` on this repository's main branch. It carries seven files: the five
binaries, `SHA256SUMS`, and the unchanged `LICENSE` of this repository, which the licence requires to accompany a
binary.

`helpers/verify-release-binaries.sh` in this repository is **upstream's** script, unchanged. It verifies upstream's
official releases (it downloads upstream's signed checksum list and rebuilds upstream's source in upstream's
builder image). It does not know this build and cannot verify it; it is kept because the source subset is
upstream's.

### 12.3 Judged commit against published source

The official jobs measured the commit before the last source commit.
`league/tools/merge/box/proof.sh JUDGED PUBLISHED` builds both trees as the judge builds its candidate and compares
the binaries byte by byte and section by section. RESULTS.md, section 2, gives what it found and the commands to
repeat it by hand.

### 12.4 A judge job, a bench, the checks

- One comparison with the judge on your own machine: [TOOLS.md](TOOLS.md), section 2. A job on another machine is
  comparable with itself only.
- A bench with fixed pairs: `research_tools_v2.py bench` with `--fixed-reps` (TOOLS.md, section 4). The many-core
  tables of RESULTS.md, section 6, were made by `league/tools/merge/box/mc2.sh` (the bench at 8 and 16 cores) and
  `mcdirs.sh`, `mcdirs2.sh` and `h10.sh` (the directory restores).
- The release checks: TOOLS.md, section 5. The lock-wait race with real binaries is `c03_lock_race.py`
  ([SOUNDNESS.md](SOUNDNESS.md), section 9).
- The tables: `python league/tools/make_tables.py --check` recomputes every generated table from the result files
  and fails if a document does not match.
- The tree's own tests, with the production lock wait and the long form of the schedule test:

```
go test ./cmd/... ./internal/...
RESTIC_TEST_LOCK_WAIT=200ms go test ./cmd/restic
RESTIC_R1_SCHEDULES=3000 go test -run TestLockWaitSchedules -count=1 ./cmd/restic
RESTIC_LARGE_CHECK_TEST=1 go test -run TestLargeCheckRejectsUnrestorableBlob -count=1 ./internal/repository
```
