# Restic League V2 final candidate: behaviour on more than four cores

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Measured 2026-10-07 on the league's compute box (AMD EPYC 9454, 48 cores / 96 threads, L3 caches
of six cores each).

**Every league timing in this file and in the league's results, the official 44 rounds
included, ran on CPUs set to the `performance` frequency governor.** Most machines do not run
that way: they let the clock follow the load. The only timings taken under a load-following
governor (`schedutil`) are those of the release-check harness in sections 2.3 and 3. Section 2.3
shows one kind of restore, a tree that is almost only directories, for which the governor
decides the sign of the result against stock: slower than stock with 16 mostly idle CPUs under
`schedutil`, faster with the same CPUs at full clock.

Commits: the league judged `b8cf439` (tree `rc-8`). The published commit is `8f9372b` (tree
`rc-9`), which differs from it by one comment and one test file and builds to the same machine
code (proof: `Leagues\evidence\restic-final-20261007\proof-rc9\PROOF.txt`).

## 1. The four league workloads, all seven phases, against stock

Time saved against stock restic, in percent (positive = faster than stock).

- **4 cores** (governor: performance): the official 44-round `promotion` job
  `50388f4ecf434b0d8663aa4a2a740d95` on the judged commit `b8cf439`, 11 rounds per workload,
  every cell 11/11.
- **8 cores**: CPUs 26-33. **16 cores**: CPUs 26-33, 36-39, 42-45 (governor: performance). Whole
  physical cores, no SMT siblings, `GOMAXPROCS` equal to the width of the set, no other league
  job running (the release-check harness was at work on its own cores, see 2.3). The builders'
  bench tool with fixed pairs, **5 pairs per cell**; every one of the 56
  cells is 5 of 5 at both core counts. The candidate is the published commit `8f9372b` built with
  the judge's flags (the same machine code as the judged binary), against the same stock.
- The **pass** rows at 8 and 16 cores are the bench tool's estimate (each phase's saving weighted
  by stock's share of the time). They are **not a judge score**; only the 4-core pass figures are.

| workload | phase | 4 cores | 8 cores | 16 cores |
| --- | --- | ---: | ---: | ---: |
| main | backup-initial | +7.17 | +6.03 | +11.38 |
| main | backup-unchanged | +23.96 | +24.77 | +25.27 |
| main | backup-mutated | +22.36 | +24.19 | +24.41 |
| main | restore | +48.76 | +46.72 | +45.95 |
| main | check-read-data | +32.45 | +33.85 | +34.19 |
| main | forget-prune | +44.60 | +49.04 | +50.16 |
| main | ls-json | +42.33 | +41.88 | +42.38 |
| main | **pass** | **+30.63** | +31.47 | +33.03 |
| big-index | backup-initial | +17.33 | +15.43 | +15.86 |
| big-index | backup-unchanged | +34.23 | +31.32 | +31.82 |
| big-index | backup-mutated | +31.59 | +29.59 | +29.81 |
| big-index | restore | +52.06 | +49.85 | +49.61 |
| big-index | check-read-data | +39.78 | +40.39 | +43.74 |
| big-index | forget-prune | +70.75 | +71.62 | +72.33 |
| big-index | ls-json | +53.00 | +50.35 | +51.75 |
| big-index | **pass** | **+45.13** | +44.99 | +46.27 |
| large-files | backup-initial | +11.15 | +17.83 | +28.28 |
| large-files | backup-unchanged | +26.96 | +36.33 | +38.36 |
| large-files | backup-mutated | +16.20 | +26.73 | +31.81 |
| large-files | restore | +20.73 | +18.73 | +18.96 |
| large-files | check-read-data | +27.61 | +28.78 | +30.08 |
| large-files | forget-prune | +45.80 | +50.13 | +48.67 |
| large-files | ls-json | +38.78 | +38.66 | +38.56 |
| large-files | **pass** | **+22.96** | +28.86 | +32.11 |
| remote | backup-initial | +6.83 | +6.58 | +8.57 |
| remote | backup-unchanged | +21.15 | +21.67 | +22.48 |
| remote | backup-mutated | +18.44 | +19.75 | +22.37 |
| remote | restore | +30.18 | +30.07 | +30.76 |
| remote | check-read-data | +5.55 | +6.16 | +5.51 |
| remote | forget-prune | +11.98 | +13.24 | +13.38 |
| remote | ls-json | +36.92 | +37.08 | +37.41 |
| remote | **pass** | **+17.26** | +17.64 | +18.51 |

**No phase is slower than stock at 8 or at 16 cores.** The smallest cell is remote check-read-data at 16 cores,
+5.51%.

### CPU time

The candidate finishes sooner than stock but, in restore, forget-prune and ls, it uses **more
user CPU than stock**, and the gap widens with the number of cores (mean of the five pairs,
candidate / stock, seconds; governor: performance):

| cell | user 8 cores | user 16 cores | system 8 cores | system 16 cores |
| --- | ---: | ---: | ---: | ---: |
| main restore | 6.3 / 5.6 | 6.9 / 5.7 | 2.8 / 3.6 | 3.0 / 3.7 |
| large-files restore | 3.4 / 2.6 | 3.5 / 2.7 | 1.8 / 1.8 | 1.8 / 1.7 |
| remote restore | 6.1 / 5.5 | 6.4 / 5.7 | 3.0 / 3.8 | 3.1 / 3.9 |
| main forget-prune | 3.0 / 2.6 | 3.3 / 2.8 | 0.6 / 0.6 | 0.6 / 0.7 |
| main ls-json | 1.4 / 1.3 | 1.5 / 1.3 | 0.1 / 0.2 | 0.1 / 0.2 |

In the league's workloads this costs nothing visible, because those restores are dominated by
file data. It is the small form of what section 2 describes.

Evidence on the box, `/opt/league/restic-final/out/`: `mc8-rc9-main.json`,
`mc8-rc9-big-index.json`, `mc8-rc9-large-files.json`, `mc8-rc9-remote.json`,
`mc16-rc9-main.json`, `mc16-rc9-big-index.json`, `mc16-rc9-large-files.json`,
`mc16-rc9-remote.json`. Each pair row carries wall, user and system seconds for both binaries
(`cand_s`, `inc_s`, `cand_user_s`, `inc_user_s`, `cand_sys_s`, `inc_sys_s`). The 4-core figures
are in the evidence folder of job `50388f4e` (`candidate_vs_stock`).

A copy of these eight files for readers, with the scripts that produced them (`mc2.sh`,
`mcbench.py`, `mcsum.py`) and the table as the generator read it (`mc-rc9-table.txt`):
`Leagues\evidence\restic-final-20261007\many-core-rc9\`, with `SHA256SUMS`; every file was
checked against the box after download.

### The earlier reading, on `rc-7`

The same measurement was first taken on `rc-7` (commit `d4f7e81`, before the nine red-team fixes
and the hand-over change of section 2). 4 cores: the official 44-round job
`940c4e64034e4e18a740e4f33537b1e8` on `rc-7`; 8 and 16 cores: same CPU sets, same governor
(performance), 5 fixed pairs per cell, all 56 cells 5 of 5. Evidence `out/mc8-main.json`,
`out/mc8-big-index.json`, `out/mc8-large-files.json`, `out/mc8-remote.json` and the four
`out/mc16-*.json` without `rc9` in the name.

| workload | phase | 4 cores | 8 cores | 16 cores |
| --- | --- | ---: | ---: | ---: |
| main | backup-initial | +7.22 | +3.38 | +12.92 |
| main | backup-unchanged | +23.98 | +24.95 | +25.03 |
| main | backup-mutated | +22.12 | +23.96 | +24.31 |
| main | restore | +47.51 | +45.14 | +44.41 |
| main | check-read-data | +35.88 | +36.03 | +35.87 |
| main | forget-prune | +45.39 | +48.13 | +50.30 |
| main | ls-json | +42.59 | +42.42 | +42.75 |
| main | **pass** | **+30.73** | +30.87 | +33.10 |
| big-index | backup-initial | +17.55 | +11.06 | +16.31 |
| big-index | backup-unchanged | +34.16 | +31.26 | +31.41 |
| big-index | backup-mutated | +32.21 | +30.44 | +29.65 |
| big-index | restore | +51.49 | +48.48 | +47.45 |
| big-index | check-read-data | +42.24 | +43.57 | +44.29 |
| big-index | forget-prune | +71.61 | +71.95 | +72.31 |
| big-index | ls-json | +52.84 | +51.05 | +52.14 |
| big-index | **pass** | **+45.53** | +44.75 | +45.92 |
| large-files | backup-initial | +10.91 | +16.66 | +28.01 |
| large-files | backup-unchanged | +27.28 | +36.81 | +38.62 |
| large-files | backup-mutated | +16.07 | +27.11 | +31.91 |
| large-files | restore | +21.10 | +18.79 | +18.89 |
| large-files | check-read-data | +22.13 | +31.35 | +30.93 |
| large-files | forget-prune | +46.79 | +50.30 | +48.89 |
| large-files | ls-json | +38.79 | +38.65 | +38.51 |
| large-files | **pass** | **+22.55** | +29.10 | +32.19 |
| remote | backup-initial | +6.05 | +6.26 | +7.07 |
| remote | backup-unchanged | +22.40 | +22.85 | +23.53 |
| remote | backup-mutated | +20.44 | +20.41 | +22.02 |
| remote | restore | +29.50 | +29.96 | +30.11 |
| remote | check-read-data | +6.56 | +7.47 | +6.37 |
| remote | forget-prune | +13.75 | +14.80 | +15.03 |
| remote | ls-json | +37.11 | +37.38 | +37.34 |
| remote | **pass** | **+17.50** | +18.10 | +18.40 |

CPU time on `rc-7` (candidate / stock, seconds):

| cell | user 8 cores | user 16 cores | system 8 cores | system 16 cores |
| --- | ---: | ---: | ---: | ---: |
| main restore | 6.3 / 5.6 | 6.9 / 5.8 | 2.9 / 3.6 | 3.0 / 3.7 |
| large-files restore | 3.4 / 2.6 | 3.6 / 2.7 | 1.9 / 1.8 | 1.9 / 1.8 |
| remote restore | 6.2 / 5.6 | 6.5 / 5.7 | 3.0 / 3.8 | 3.1 / 3.9 |
| main forget-prune | 3.0 / 2.6 | 3.4 / 2.8 | 0.6 / 0.6 | 0.6 / 0.6 |
| main ls-json | 1.4 / 1.3 | 1.5 / 1.3 | 0.1 / 0.2 | 0.1 / 0.2 |

## 2. A restore of very many directories

A release check restored a snapshot of 606,060 directories with `rc-7` on its own CPUs
(governor: schedutil) and found the candidate 10% faster than stock on 4 cores but 27% slower
with 16 CPUs. This section is what was found, what was changed, and what is left.

### 2.1 The mechanism, found on `rc-7`

The same shape of tree (606,060 directories, three small files, snapshot written by stock),
`rc-7` against stock, two runs each in alternating order, on the bench lanes (governor:
performance). The 27% loss did not appear there: the candidate stayed ahead of stock at every
core count. What did appear is the same effect at smaller size: the candidate **lost 10% going
from 4 to 16 cores while stock stayed flat**, and its user CPU grew with the core count while
stock's did not.

Restore target on tmpfs, whole cores (wall s / user s / system s / voluntary context switches):

| cores (governor: performance) | rc-7 | stock |
| --- | --- | --- |
| 4 (26-29) | 28.9 / 19.8 / 19.7 / 1.30M | 34.4 / 17.9 / 18.4 / 0.09M |
| 8 (26-33) | 30.6 / 22.3 / 20.3 / 1.33M | 35.0 / 18.4 / 18.5 / 0.09M |
| 16 (26-33, 36-39, 42-45) | 31.8 / 24.1 / 20.7 / 1.30M | 35.1 / 18.0 / 18.6 / 0.08M |

Restore target on ext4, repository on tmpfs:

| CPUs (governor: performance) | rc-7 | stock |
| --- | --- | --- |
| 4 (26-29) | 48.6 / 23.2 / 36.9 / 1.81M | 52.0 / 19.3 / 33.2 / 0.14M |
| 16 logical: 8 cores with their SMT siblings (26-33, 74-81) | 50.4 / 27.0 / 38.6 / 1.75M | 52.6 / 19.5 / 33.2 / 0.12M |

Both passes of restore passed **every directory** through another goroutine and back.

- First pass: the node lookahead ends a batch at every directory, so a run of directories comes
  back one node per request, and an empty tree costs a request and a reply for nothing.
- Second pass: the tree prefetch handed over one prepared tree at a time through a queue of one.

Each hand-over puts one goroutine to sleep and wakes another, and every wake-up sets idle CPUs
searching for work. That is the 1.3 million voluntary context switches against stock's 0.09
million (about two per directory), and the user CPU that grows with the number of CPUs.

### 2.2 The change (commit `8669c54`, part of the judged and the published commit)

One contained change, two files in `internal/restorer`, five tests. First pass: the traversal
decodes nodes itself until one is not a directory; from that node on the helper prepares ahead
as before. Second pass: prepared trees are handed over in batches of up to 64 trees and 256 KiB
of prepared nodes; a batch goes as soon as the traversal has room for it, so a traversal that
waits is still served tree by tree. Order, errors, the fallback and the byte budget of a prepared
tree are unchanged.

Same tree, two runs each, bench lanes (governor: performance; wall s / user s / voluntary
context switches):

| set-up | before | after | stock |
| --- | --- | --- | --- |
| tmpfs, 4 cores | 28.7 / 19.9 / 1.30M | 25.8 / 16.5 / 0.09M | 34.4 / 17.8 / 0.09M |
| tmpfs, 8 cores | 30.5 / 22.5 / 1.32M | 26.7 / 18.4 / 0.11M | 35.0 / 18.4 / 0.09M |
| tmpfs, 16 cores | 31.6 / 23.6 / 1.28M | 27.4 / 19.1 / 0.11M | 35.6 / 18.2 / 0.08M |
| ext4, 4 CPUs | 47.6 / 22.5 / 1.86M | 41.8 / 17.5 / 0.11M | 51.7 / 18.9 / 0.14M |
| ext4, 16 logical CPUs (8 cores with SMT) | 50.4 / 27.0 / 1.75M (rc-7) | 43.0 / 20.0 / 0.14M | 51.7 / 18.8 / 0.12M |

"Before" is the tree with the nine red-team fixes and without this commit; for the last row it
is `rc-7`, which has the same restore code. On these lanes the restore is, after the change, 17%
to 25% faster than stock at 4, 8 and 16 cores, uses about stock's user CPU, and makes about
stock's number of context switches. A rise with the core count remains (25.8 s to 27.4 s on
tmpfs), much smaller than before (28.7 s to 31.6 s).

Restore cells of the four league workloads at 4 cores (governor: performance), tree with the
change against the tree without it, 8 fixed pairs: main +1.01% (7/8, inside the noise band),
big-index +1.66% (6/8, unresolved), large-files -0.35% (4/8, unresolved), remote +0.83% (8/8,
beyond noise). These pairs ran beside the directory runs; the official jobs of section 1 are the
measurement that counts.

### 2.3 What was measured after the change, and the limit that remains

**(a) On the release-check harness** (governor: schedutil; cores 10, 11, 16, 17, 18, 23, 46, 47
with their SMT siblings, two cores in each of four L3 cache domains; run beside the official
44-round job, which ran on other cores of the same machine), the judged commit `b8cf439` against
upstream:

| directories | CPUs | upstream | judged commit | rc-7 | rounds |
| --- | --- | --- | --- | --- | --- |
| 606,060 | 16 | 67.2 s (user 28.6 + system 41.9) | 74.0 s (39.4 + 57.8), **10.1% slower** | 85.5 s (52.5 + 65.8), 27.2% slower | three rotated rounds, median |
| 606,060 | 4 cores (10, 11, 16, 17) | 86.5 s (39.0 + 53.9) | 63.3 s (31.7 + 47.6), **26.8% faster** | 77.6 s (43.6 + 58.6), 10.3% faster | three rotated rounds, median |
| 2,020,200 | 16 | 221.6 s | 232.3 s, **4.8% slower** | 261.3 s, 17.9% slower | one run per build |

So the change helped there as well (85.5 s to 74.0 s with 16 CPUs, 77.6 s to 63.3 s on 4 cores),
but with all 16 CPUs allowed the judged commit is still slower than upstream on that harness.

**(b) On the bench lanes** (governor: performance), the published machine code (`rc-9` built with
the judge's flags) against upstream, 606,060 directories, restore target on ext4, `sync` before
each run, `GOMAXPROCS=16` on 8 cores with their SMT siblings, three rotated rounds each (wall s;
user s / system s / voluntary context switches of the median run):

| 8 cores with their SMT siblings (governor: performance) | upstream | rc-9 | rc-9 against upstream |
| --- | --- | --- | --- |
| compact: 4 + 4 cores of two L3 domains (26-29, 30-33) | 52.7, 52.8, 54.9 (19.7 / 33.6 / 0.12M) | 42.8, 43.7, 44.0 (20.1 / 33.3 / 0.14M) | 17% faster |
| spread: 2 cores from each of four L3 domains (12, 13, 19, 20, 26, 27, 36, 37) | 51.0, 52.6, 52.6 (19.6 / 33.0 / 0.12M) | 42.9, 43.5, 43.6 (20.7 / 33.0 / 0.14M) | 17% faster |
| spread, with the repository on ext4 and `--no-cache`, as the check runs it | 56.1, 56.2, 56.8 (22.7 / 34.7 / 0.13M) | 47.0, 47.5, 47.7 (24.3 / 34.3 / 0.15M) | 16% faster |

The L3 domains were read from `/sys/devices/system/cpu/cpu*/cache/index3/shared_cpu_list`: six
cores each (0-5, 6-11, 12-17, 18-23, 24-29, 30-35, 36-41, 42-47).

**(c) What (b) settles and what it left.** The cache layout is ruled out by (b): spread over
four L3 domains exactly as the check's CPUs are, the restore is as fast as on neighbouring cores
(43.5 s against 43.7 s), and the check's way of reading the repository does not change the sign
either. Two differences between the harnesses were left: the frequency governor (the bench lanes
and the judge run `performance`; the check's CPUs run `schedutil`, which clocks a CPU up only
under sustained load), and the neighbouring job (the check's run, 18:27 to 18:57 UTC, overlapped
the official 44-round job, 17:39 to 19:24 UTC, on other cores of the same machine and the same
ext4 file system). Read on 2026-10-07 at 19:49 UTC from `/sys/devices/system/cpu/cpu*/cpufreq/`:
check CPUs 23 and 58 (idle) 1.52 and 1.50 GHz, CPU 10 1.81 GHz, CPUs 16 and 46 (busy) 3.79 and
3.80 GHz; lane CPUs 12, 26 and 30 (busy) 3.79 to 3.80 GHz, lane CPUs 19, 36 and 42 (idle) 2.75
GHz; minimum 1.5 GHz, nominal maximum 2.75 GHz, boost on. Upstream itself burned 70.5 CPU
seconds for this restore on the check's 16 CPUs against 52.7 on the lanes (1.34 times), and 92.9
with 4 cores against 52.5 (1.77 times). Both differences were then tested, in (d).

**(d) The governor test** (2026-10-07, 20:18 to 20:50 UTC, by the operator's order, run by the
release-check harness on its own 16 CPUs). The governor of those 16 CPUs only was set to
`performance`, the 606,060-directory restore was repeated, and the saved state was written back
and read back equal; then the restore was repeated once more under `schedutil` with nothing
beside it. Three rotated rounds each, `sync` before each restore, judged commit `b8cf439` against
upstream (wall seconds per round; "clock" is the mean of the frequency readings taken every 5 s
on a running CPU):

| governor | beside it | CPUs | upstream | judged commit | against upstream | user + system s, upstream / judged | clock, upstream / judged |
| --- | --- | --- | --- | --- | --- | --- | --- |
| schedutil | the official 44-round job | 16 | 66.5, 68.0, 67.2 | 73.0, 74.7, 74.0 | **10.1% slower** | 28.6 + 41.9 / 39.4 + 57.8 | not sampled |
| schedutil | the official 44-round job | 4 cores | 86.5, 86.7, 86.5 | 63.4, 63.3, 63.0 | 26.8% faster | 39.0 + 53.9 / 31.7 + 47.6 | not sampled |
| performance | the last bench of section 1 (1.7 to 1.8 busy CPUs outside the set) | 16 | 53.2, 53.7, 53.3 | 44.7, 44.3, 44.7 | **16.1% faster** | 22.3 + 32.6 / 23.6 + 32.9 | 3.80 GHz / 3.80 GHz |
| performance | nothing | 4 cores | 53.1, 53.7, 53.5 | 44.1, 43.6, 44.1 | 17.6% faster | 23.4 + 33.1 / 22.1 + 31.9 | 3.80 GHz / 3.80 GHz |
| schedutil | nothing | 16 | 68.2, 68.0, 67.8 | 75.3, 74.7, 75.6 | **10.7% slower** | 29.1 + 42.2 / 40.5 + 58.2 | 2.55 GHz / 2.02 GHz; 61% / 58% of readings at 1.5 GHz |

A larger run under `schedutil`, stopped before its other sizes: 1,010,100 directories, 16 CPUs,
three rounds, upstream 110.7 s, judged commit 119.3 s (7.8% slower).

What this settles, in this order:

1. **The performance governor removes the loss.** On the check's own CPUs the judged commit is
   16.1% faster than upstream with 16 CPUs and 17.6% faster on 4 cores, the same margin as on the
   bench lanes in (b), and the two CPU sets give the same times.
2. **A quiet box does not remove it.** Under `schedutil` with nothing beside it the judged commit
   is 10.7% slower than upstream, as it was beside the official job (10.1%). The neighbouring job
   is ruled out, with the cache layout.
3. **The restore keeps about one CPU busy**: 1.04 of the 16 for upstream, 1.31 for the judged
   commit. Under `schedutil` the judged commit's running CPUs were clocked lower than upstream's
   (2.02 against 2.55 GHz) and it used 98.7 CPU seconds against 71.4 (medians of the per-round
   totals); at the fixed clock both use about 55.
4. **An inference, not a measurement:** that this build spreads the work over more threads and
   so gives a load-following governor less load per CPU. It is drawn from the clock readings and
   the CPU seconds of point 3; no experiment isolated it.

Smaller differences between the harnesses, not tested: the check's tree is named differently
(same counts), it runs without `-q`, and its CPU set is given by the check's process limits, not
by `taskset`.

**The limit, as published.** On a machine with many idle CPUs under a load-following governor,
a restore that is almost only directories was 4.8% to 10.7% slower than upstream with 16 CPUs
(10.1% and 10.7% at 606,060 directories, 7.8% at 1,010,100, 4.8% at 2,020,200 in one run per
build). Under the same governor it was 26.8% faster on 4 cores, and with the CPUs at full clock
it was 16% to 18% faster on 4 and on 16. No code change follows.

Evidence on the box, `/opt/league/restic-final/`: `logs/mcdirs.log` and `logs/mcdirs2.log`
(per-run lines of the last run of each script), `PROGRESS.log` lines `mcdirs`, `mcdirs2` and
`l3dirs` (all runs, with the binaries named), `logs/l3dirs.log`, `logs/mcdirs2-perf-rc-7.txt` and
`logs/mcdirs2-perf-stock.txt` (flat profiles on the 16 logical CPUs), `out/bn-h10rs-*.json`
(the restore cells), scripts `tools/mcdirs.sh`, `tools/mcdirs2.sh`, `tools/h10.sh`,
`tools/l3dirs.sh`. The check harness: script `checks/x11_dirs_time.py`, results
`checks/results/final3-b8cf439/x11-606060/` (`runs.json`, `table.md`). The governor test of (d):
`checks/results/final3-b8cf439/x11-606060-performance/`, `x11-606060-schedutil-quiet/`,
`x11-large-partial/` and `cpufreq/` (the saved state, the set and restore records, the clock
samples), with copies in `Leagues\evidence\restic-final-20261007\release-checks-final3-b8cf439\`;
it is written up in `final-candidate\checks\REPORT.md`, section "F6 settled: the frequency
governor".

The files of the L3 test of (b) are also in the readers' copy,
`Leagues\evidence\restic-final-20261007\many-core-rc9\`: `l3dirs.log` (every run),
`l3dirs-progress-lines.txt` (one line per CPU set), `l3dirs.sh` (the script).

## 3. Timings under a load-following governor: the release-check harness

These are **incidental timings of correctness checks**, one to three runs each and not paired:
they are not benchmarks. They are listed because, with the governor test of 2.3 (d), they are
the only numbers taken with the `schedutil` governor (check CPUs as in 2.3 (a), memory limit 16
GiB, beside the official 44-round job). Judged commit `b8cf439` against upstream.

| check | what ran | upstream | judged commit | runs |
| --- | --- | --- | --- | --- |
| C10 part A | load 500 index files through 50 ms of latency, 1 CPU | 11.82 s | 11.56 s | three each, all within 0.01 s |
| C10 part A | the same, 2 CPUs | 11.80 s | 11.47 s | three each |
| C10 part B | restore 2,020,200 directories and 10 files, 16 CPUs | 221.6 s | 232.3 s (4.8% slower) | one each |
| X11 | restore 606,060 directories and 3 files, 16 CPUs | 67.2 s | 74.0 s (10.1% slower) | three rotated, median |
| X11 | the same, 4 cores | 86.5 s | 63.3 s (26.8% faster) | three rotated, median |
| C10 part C | restore one 50 GiB file | 41.7 s | 41.3 s | one each |
| C10 part C | the same with `--sparse` | 1.8 s | 1.6 s | one each |
| C10 part D | backup against an index of 30 million blobs (600 index files) | 9.6 s | 6.9 s | one each |
| C10 part D | the same, 10 million blobs | 3.4 s | 2.2 s | one each |
| C10 part D | the same, 3 million blobs | 1.4 s | 1.0 s | one each |

Source: `Leagues\evidence\restic-final-20261007\release-checks-final3-b8cf439\` (`status.txt`,
`c10/summary.json`, `x11-606060/table.md`) and `final-candidate\checks\REPORT.md`.
