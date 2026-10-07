# Restic League V2: speed map (cost map 5, the S013 incumbent)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-10-01/02 (box 00:00-00:40 CEST) on the league box while S014 ran. **Only CPUs 2-5 were used** (systemd slice
`costmap5.slice`, `AllowedCPUs=2-5` plus `taskset 2-5` on every build, run and analysis). The judge lanes and S014's
research lanes were not touched, and no governor was changed. The instrument is cost-map-4's (judge sandbox, argv,
starting states, `rusage-launch`, GOMAXPROCS=4) on one lane. Raw data is in `data/`, the log in `PROGRESS.md`, closed
ground in `data/closed-ground.md`. The directive built from this map is `runtime/S015-directive.md`.

**Read this caveat first.** CPUs 2-5 run `schedutil`. The judge lanes run `performance` since S013. The same S013 tree
takes **22.36 s here (n=10) and 17.90 s on the judge** (42-round confirmation). The gap is not even: bi 1.02x, ck/fp
1.15x, rs 1.25x, bu/bm 1.52x, ls 1.74x (here / judge). Phases that leave cores idle pay the clock-down here and not on
the judge. So every absolute number below is a schedutil number unless it says "judge", and a lever measured here is
converted to judge seconds with that phase's ratio (marked "~judge"). Paired, interleaved prototype deltas (10 reps)
are still valid comparisons. Their size on the judge is roughly the schedutil delta divided by the phase ratio.

## 0. Headline

1. **S013 on the judge: 17.90 s, 579 MiB summed peaks** (stock 22.29 s; S012 18.47 s). **1 s = 5.59 points; 0.1 s
   per process x7 = 3.9 points.** The memory guard (+2%) is about **11.6 MiB summed over the 7 phase peaks**.
2. **What S013 removed (judge, 42 rounds, vs S012): -0.57 s** = bi -0.13, bu -0.08, bm -0.09, rs -0.19, ck -0.02,
   fp -0.01, ls -0.06. Two mechanisms did it:
   - **No-poller opens** (archiver reads, restore create/write/verify). backup-initial fcntl fell 555k -> 104k and the
     92.6k failing `epoll_ctl` calls are gone in bi and restore. S013 T2 M001 (this alone) measured -0.23 s.
   - **Index load inside the 200 ms lock dwell, backup only.** bu/bm census: the index loads (0.054-0.065 s) and the
     lock is confirmed 0.20 s after creation, with no load after it.
   - The tree change is small: `LoadTree` now uses the parent-tree iterator. It is not S012 T4's direct node decoder.
3. **The dwell is still idle in 4 of 7 phases.** restore, ls (read lock), check and forget-prune (exclusive lock) sit
   200 ms idle and THEN load the index (rs 0.109, ck 0.128, fp 0.065, ls 0.079 s here). On the judge the dwell is
   1.40 s of 17.90 (7.8%). Per process, the fixed cost is still ~0.45 s (KDF 0.20, dwell 0.20, index ~0.05 when
   not hidden, spawn 0.02), x7 = ~3.2 s = 18% of the judge workload. ls on the judge is 0.96 s, so ~45% of ls is fixed.
4. **The largest remaining time** (judge seconds):
   - **bi snapshot ~4.75 s.** CPU-bound at 3.47 cores: 17.5 CPU-s (syscalls 3.6, zstd 2.8, sha256 2.5, chunker 1.7,
     AES 1.1).
   - **Restore download ~2.5 s, metadata second pass ~0.85, first pass ~0.6.** Per file: openat, fallocate, pwrite,
     fchownat, fchmodat, utimensat, llistxattr, close (91k files). In blocking time, openat 0.79 s and fallocate 0.78 s.
   - **Check read-data ~1.7 s** at 2.3 cores with 2 workers: sha256 1.95 CPU-s, zstd 0.97, 0.57 s of send latency.
   - **bu/bm snapshot ~1.4 s each.** 2 lstat per file (183k newfstatat, 0.84 CPU-s) plus 91k llistxattr.
5. **Prototypes (section 7, 10 interleaved reps here, converted to ~judge):**
   - **check with a third read worker: -0.785 s here (10/10), ~-0.68 judge (+3.8 points), +9.1-9.7 MiB.** It fits
     the guard alone.
   - Dwell overlap for restore/ls/prune (`p-dwell`): -0.23 s here, ~-0.17 judge (+0.95), memory flat.
   - Direct tree-node decoder (`p-t4dec`): -0.14 here (bu -0.08, ls -0.05), ~-0.09 judge.
   - No fallocate for small restored files (`p-falloc`): rs -0.07 here (6/10).
   - Skipping restore's `llistxattr` (bound): null.
   - Everything except the third worker is within ~2x of this lane's noise. S014's two judged rows so far (the ls
     pull walk, the parent lookup in the dwell) were null.

## 1. Method

- Binaries in `/opt/league/restic-costmap5/bin/`: `s013-plain` and `s013-census` were built here with the judge
  toolchain go1.27.0 and judge flags (`tools/build5.sh`). stock and S012 binaries are cost-map-4's. Prototypes are
  `p-*` (`tools/build_proto5.sh`, edits in `tools/proto_edit5.py`).
- `main5` (212 runs, 0 failures, one lane): plain S013 and S012 x10 plus stock x4, interleaved and rotated per rep;
  S013 census x2 (stage marks with process CPU); CPU profile, Go execution trace, `strace -c`; trivial commands x3.
- `x5` (200 runs): prototypes vs `s013-plain`, 10 interleaved reps, run only on the phases each one touches. The other
  phases execute identical code, so their delta is 0 by construction.
- Noise on this lane: per-phase S013 ranges were bi 5.16-5.29, bu 2.64-2.95, bm 2.83-3.25, rs 5.08-5.46, ck 2.59-2.79,
  fp 1.55-1.71 and ls 1.52-1.71 s. bu, bm and ls are still bimodal here (clock modes). On the judge they are not:
  the judge's 12-round per-phase spread on bm was 1.99-2.01 s.

## 2. Phase baseline

| phase | stock (judge) | S012 (judge) | **S013 (judge)** | S013 here (MiB) | here/judge | S013 cores here | judge share |
|---|---|---|---|---|---|---|---|
| backup-initial | 5.503 | 5.260 | **5.127** | 5.238 (100) | 1.02 | 3.34 | 28.6% |
| backup-unchanged | 2.224 | 1.933 | **1.851** | 2.824 (65) | 1.53 | 1.94 | 10.3% |
| backup-mutated | 2.351 | 2.037 | **1.949** | 2.973 (86) | 1.53 | 2.08 | 10.9% |
| restore | 6.069 | 4.453 | **4.267** | 5.347 (108) | 1.25 | 1.95 | 23.8% |
| check-read-data | 2.537 | 2.371 | **2.348** | 2.709 (74) | 1.15 | 2.27 | 13.1% |
| forget-prune | 2.126 | 1.397 | **1.388** | 1.602 (89) | 1.15 | 2.19 | 7.8% |
| ls-json | 1.473 | 1.018 | **0.957** | 1.668 (58) | 1.74 | 1.39 | 5.3% |
| **total** | **22.29** | **18.47** | **17.90** | **22.36 (579)** | 1.25 | | |

The judge columns are from the S013 42-round confirmation (job `651a07c9...`). The two S014 incumbent arms (6 rounds
each, S013 tree) read 18.26 and 18.10 s. "Here" is the n=10 median on CPUs 2-5. Here, S013 vs S012 paired is
+4.57% (n=10, +2.73..+5.43); on the judge it was +3.11%.

## 3. Where the time goes (S013 census, n=2, schedutil; process CPU per stage)

| element | here s | cores | CPU-s | judge s (est.) | what it is |
|---|---|---|---|---|---|
| spawn, runtime init, exit | 0.02 x 7 | - | - | 0.14 | fixed |
| **KDF** (scrypt, 4 cores) | 0.20-0.21 x 7 = **1.43** | 3.95 | 0.81 each | ~1.4 | repository-defined. Closed |
| **lock dwell** (200 ms) | 0.20 x 7 = **1.40** | 0.05 | 0 | **1.40** | bu/bm: index load inside it (0.054-0.065 s). bi: nothing to load. **rs, ck, fp, ls: idle** |
| index load after the dwell | rs .109, ck .128, fp .065, ls .079 = **0.38** | 1.6 | 0.11-0.21 | ~0.25 | on the critical path in 4 phases |
| bi snapshot | **4.85** | 3.47 | 16.8 | ~4.75 | CPU-bound |
| bu / bm snapshot | 2.46 / 2.38 | 1.90 / 2.06 | 4.7 / 4.9 | ~1.4 / ~1.5 | lstat x2 per file, llistxattr, parent-tree decode, serial dir walk |
| restore first pass | **0.79** | 1.18 | 0.94 | ~0.6 | serial tree walk, mkdir, planning |
| restore download (2 workers) | **3.14** | 2.15 | 6.74 | ~2.5 | per-file create/fallocate/pwrite; sha256, zstd |
| restore second pass (metadata) | **1.08** | 1.92 | 2.08 | ~0.85 | fchownat, fchmodat, utimensat, llistxattr per file |
| check structure / read-data | 0.13 / **1.95** | 3.7 / 2.32 | 0.47 / 4.51 | ~0.1 / ~1.7 | 2 read workers |
| prune plan / execute | 0.19 / **0.91** | 2.6 / 2.26 | 0.49 / 2.06 | ~0.16 / ~0.8 | plan could run in the dwell |
| ls walk + output | **1.18** | 1.22 | 1.44 | ~0.5 | serial tree load, decode, encode |

The memory floor is unchanged: `cat config` 56 MiB, `version` 23 MiB. `cat config` takes 0.42 s, the KDF floor plus
the read-only dwell.

**Waiting (Go execution traces, S013; summed over goroutines):**
- restore sched latency: 2.08 s in `selectgo`, almost all under `traverseTreeInner` (the walk feeding workers).
  Blocking syscalls: openat 0.79, fallocate 0.78, fchownat 0.40, pwrite 0.34, fchmodat 0.33, utimensat 0.32,
  llistxattr 0.29 s.
- bu blocking syscalls: fstatat 0.67 s, llistxattr 0.38 s. Sched: 0.42 s in `selectgo`.
- check: 0.57 s of `chansend1` latency (read-data handoff to verification), 0.27 s in read.
- ls: 0.20 s `goschedIfBusy` (GC assist), 0.03 s in write. The output path is not the problem.

**Syscalls (`strace -c`, S013):**
- bi: futex 218k, read 404k, newfstatat 183k (2 per file), getrandom 155k (a nonce per blob), close/openat/llistxattr
  91-93k each, fcntl 104k (was 555k on S012).
- bu: newfstatat 183k, llistxattr 91k. bm: 173k, 87k.
- restore: openat 93k, fallocate 89k, fchownat/fchmodat/utimensat/llistxattr 91k each, pwrite 90k, close 93k.
- check: read 2.3k, futex 30k. ls: write 1.1k.

## 4. CPU by category (S013, CPU-s per run, one profile; `data/main5/summary/cpu-categories-s013.json`)

| phase | CPU-s | top categories |
|---|---|---|
| backup-initial | 17.5 | syscalls 3.6, zstd compress 2.8, sha256 2.5, chunker 1.7, AES 1.1, zstd decompress (save verify) 0.64, memmove 0.49, memclr 0.34 |
| restore | 10.1 | syscalls 3.3, sha256 1.15, zstd decompress 0.79, GC 0.55, AES 0.54, tree JSON 0.31 |
| check-read-data | 6.5 | sha256 1.95, zstd decompress 0.97, AES 0.61, syscalls 0.40, KDF 0.8 |
| bu / bm | 5.6 / 5.8 | syscalls 1.4 (lstat 0.84 of it in bu), chunker 0.6, sha256 0.3-0.6, JSON 0.27-0.32, KDF 0.8 |
| forget-prune | 3.5 | sha256 0.36, syscalls 0.35, zstd 0.29+0.17, AES 0.23, JSON 0.20, index 0.15, KDF 0.8 |
| ls-json | 2.2 | KDF 0.8, tree JSON decode/encode 0.57 |

## 5. What S013 removed, and what is left of cost-map-4's menu

| cost-map-4 item | S013 status | what is left (judge) |
|---|---|---|
| 1 dwell overlap | **backup only** (index load) | rs/ls/fp/ck still idle 200 ms then load: ~0.25 s of index, plus ~0.1 s of further dwell room per phase |
| 2 third read worker | not built | check -0.9 s here for +7.9 MiB (cost-map-4); re-measured in section 7 |
| 3 tree decode | parent iterator only | S012 T4 direct node decoder: re-measured in section 7 |
| 4 no-poller opens | **done** (archiver, restorer) | local-backend pack opens are a few hundred: nothing (S013 T3/T4 M002 null) |
| 5 restore per-file syscalls | not built | fallocate 0.78 s, llistxattr 0.29, fchownat 0.40 blocking |
| 6 restore passes | not built | pass 1 serial 0.79 s here at 1.18 cores; 2.08 s of walk select latency |
| 7 bi CPU | partly (fcntl) | 2 lstat/file, getrandom per blob, 608 GC cycles |
| 8 backup pool time | not built | bigpool bound bi -0.19 s for +37 MiB (fails the guard) |
| 9 index load decode | hidden in backups only | rs/ck/fp/ls decode on the critical path; check loads its own |
| 10 ls critical path | small gain (-0.06 judge) | S014 T1's pull-iterator walk was null (ls -0.007 s) |
| 11 prune | not built | plan 0.19 s could move into the dwell |
| 12 bu/bm snapshot | not built | lstat twice per file; S014 T3's parent snapshot lookup in the dwell was null |

## 6. ls and the clock (unchanged finding, now resolved for the judge)

cost-map-4's "ls FIFO gap" was the schedutil clock-down. On the judge's performance governor, ls takes 0.96 s (here
1.67 s). There ~0.42 s is KDF plus dwell, ~0.06 s index, and the walk is ~0.45 s. The remaining ls lever is the dwell
(index and first tree reads), then faster decode. Burning CPU to hold clocks is still banned, and it is pointless on
the judge.

## 7. Prototypes (x5, 10 interleaved reps vs s013-plain, CPUs 2-5)

Paired per-rep deltas within phase (median of 10), wins out of 10, peak MiB delta (median; paired median in brackets).
"~judge" divides by the phase's here/judge ratio from section 2. Diffs vs s013-plain are in `data/protos/`; the table
is `data/x5/table.md`.

| prototype | edit (on s013-plain) | per-phase time here (wins/10) | sum here | ~judge s | ~points | memory MiB |
|---|---|---|---|---|---|---|
| **`conn3` on check** | `-o local.connections=3`, check only (a knob used as a bound; not a candidate) | ck **-0.785 (10/10)** | **-0.79** | **-0.68** | **+3.8** | **+9.7 (+9.1)**: fits the 11.6 guard alone, ~2 MiB left |
| **`p-dwell`** | S013's backup dwell hook generalised: restore, ls and forget-prune start `LoadIndex` after `createLock` and consume it after the confirmed check (`cmd/restic/lock.go`, `cmd_restore.go`, `cmd_ls.go`, `cmd_prune.go`, `cmd_forget.go`). check excluded (it loads its own index) | rs -0.060 (7), fp -0.085 (7), **ls -0.084 (10/10)** | **-0.23** | -0.17 | +0.95 | rs -1.0, fp +0.8, ls -0.4 (flat) |
| `p-t4dec` | S012 T4 M004's direct tree-node decoder (`data/tree_decode.go`) for `LoadTree` and parent trees | bu -0.079 (6), rs -0.016 (5), ls -0.045 (9) | -0.14 | -0.09 | +0.5 | bu -0.8, rs +2.1, ls +0.4 |
| `p-falloc` | restore: no `fallocate` for files <= 4 MiB (`fileswriter.go` `ensureSize`) | rs -0.070 (6) | -0.07 | -0.06 | +0.3 | flat |
| `p-stack` | dwell + t4dec + falloc | bu -0.039 (7), bm +0.034 (3), rs -0.106 (8), ck +0.060 (4), fp -0.067 (8), ls -0.090 (10) | -0.21 | -0.15 | +0.8 | -1.5 summed |
| `p-noxl` (x6, bound) | restore: skip `llistxattr` when the node has no xattrs (unsound in general; bound only) | rs -0.011 (6) | -0.01 | 0 | 0 | +0.6 |

Notes.
- **The third read worker is the one large lever left.** On S013 it fits the guard alone: +9.1-9.7 MiB of 11.6.
  cost-map-4 measured restore -0.75 s for +25 MiB and prune -0.11 s for +14 MiB on S012. Those fit only after
  per-worker memory is bounded. Connections is a product default, so the owner's product argument is still required.
- `p-dwell` hides most of the post-dwell index load (ls 0.079 s -> ~0, fp 0.065 -> ~0, restore about half). After it
  the dwell still has ~0.1-0.14 s idle per phase for the next read-only work: prune planning (0.19 s), the snapshot's
  root and first trees for restore and ls, and check's own index load (0.128 s).
- Everything except `conn3` is at or near this lane's noise (±0.05 s per phase), and the judge shrinks it further.
  `p-stack` came out smaller than the sum of its parts. Treat `p-t4dec` and `p-falloc` as small and unconfirmed.
- Restore's `llistxattr` (91k calls, 0.29 s blocking) is not on the critical path: the 4-worker second pass absorbs
  it. Skipping it gains nothing.

## 8. Remaining speed levers, ranked (judge seconds; 1 s = 5.59 points)

1 s = 5.59 points on the judge. "(m)" = measured here (section 7) and converted with the phase ratio. Other ceilings
are a measured share times a plausible reduction. Memory = the change in summed peaks; the guard is 11.6 MiB.

| # | lever | mechanism and files | measured share (judge) | ceiling s (judge) | points | memory |
|---|---|---|---|---|---|---|
| 1 | **Third read worker, bounded** | `backend/local` Connections, `checker.go` read-data, `filerestorer.go`, `repack.go`, `streamPack` | ck read-data ~1.7 s at 2.3 cores (2 workers) | **-0.68 (m, check alone)**; -1.2 with restore | **+3.8**; +6.7 | ck **+9.1-9.7 (m)**; restore +25 on S012 unless bounded. Product ruling |
| 2 | **Dwell overlap for restore, ls, prune, check** | generalise S013's `LockRepoWithWaitWork` hook (`cmd/restic/lock.go`, `backup_index_load.go`) to the `openWithReadLock` / `openWithExclusiveLock` callers | index after the dwell: rs .09, ck .11, fp .06, ls .05; the dwell idles 0.2 s x 4 | **-0.17 (m, index only)** to -0.4 (plus prune plan, check index, first tree reads) | +0.9 to +2.2 | 0 (m) |
| 3 | **Restore first pass** | `restorer.go` `traverseTree*`: prefetch and decode subtrees ahead of the serial walk on idle cores; batch mkdir | ~0.6 s at 1.2 cores; 2.08 s of `selectgo` latency under the walk | -0.2 to -0.3 | +1.1 to +1.7 | small (bounded prefetch) |
| 4 | **Restore per-file syscalls** | `fileswriter.go` (fallocate), `restorer.go` metadata (fchownat when the owner already matches), one open per file | blocking: openat 0.79, fallocate 0.78, fchownat 0.40 s (summed over goroutines) | -0.06 (m, fallocate) to -0.25 | +0.3 to +1.4 | 0 |
| 5 | **Backup-initial CPU** | archiver: lstat once per file; `crypto` nonce per blob (155k `getrandom`); futex/nanosleep handoffs; 608 GCs | 17.5 CPU-s at 3.47 cores; 1 CPU-s = ~0.29 s wall | -0.1 to -0.3 | +0.6 to +1.7 | must not regrow S012's pool |
| 6 | **bu/bm snapshot** | archiver scan + save: two `lstat` per file (183k), `llistxattr`, parent tree decode (`p-t4dec` bu -0.08 here) | ~1.4 s each at ~2 cores; lstat 0.84 CPU-s per run | -0.1 to -0.2 (both phases) | +0.6 to +1.1 | 0 |
| 7 | **Tree decode** | S012 T4's `data/tree_decode.go` direct node decoder | rs/ls/bu decode on serial paths | **-0.09 (m)** to -0.15 | +0.5 to +0.8 | +1.7 (m, noise) |
| 8 | **Check pipeline** | `checker.go` ReadPacks: 0.57 s of `chansend1` latency between pack read and blob verify | read-data ~1.7 s | -0.1 to -0.2 (beyond item 1) | +0.6 to +1.1 | 0 |
| 9 | **ls walk** | `cmd_ls.go`, `walker`: prefetch subtrees in parallel and keep output order; the dwell can take the root tree | walk ~0.45 s at 1.2 cores | -0.1 to -0.2 | +0.6 to +1.1 | <= 2 MiB |
| 10 | **Backup pool time without the memory** | `archiver/file_saver.go`, `buffer.go`: copy-on-growth past 2 MiB | bigpool bound bi -0.19 s for +37 MiB (cost-map-4) | -0.1 to -0.15 | +0.6 to +0.8 | no bigger pool |
| 11 | **Prune execute** | `prune.go`, `repack.go`, index rewrite | ~0.8 s at 2.26 cores | -0.05 to -0.15 | +0.3 to +0.8 | the prune peak is the index rewrite |
| 12 | **Restore download workers** | covered by item 1 (restore part) plus per-worker memory bounds | ~2.5 s at 2.15 cores | in item 1 | | |

**Closed / not worth it for speed (new in this map):**
- restore `llistxattr` skip: null.
- ls pull-iterator walk without the yield callback (S014 T1 M001): ls -0.007 s.
- parent snapshot lookup in the dwell (S014 T3 M001): bu -0.004 s.
- local-backend pack opens without the poller (S013 T3/T4 M002): a few hundred opens, nothing.

Still closed from cost-map-4: KDF, GOGC, the save verify, idx1enc, reverting S012's pool, ls output batching.

**Stacking envelope.** Item 1 alone (check) is ~0.7 s (+3.8). Items 2, 3 and 4 together add another 0.4-0.9 s. A
realistic S015 envelope over S013 is **-0.8 to -1.5 s on the judge (+4.5 to +8.5 points)**, if memory stays inside
11.6 MiB summed. The third worker alone uses most of the guard.

## 9. Data, binaries, tools

- **Local** `cost-map-5/data/`: `main5/summary/` (tables.md, pprof flat/cum per phase, cpu-categories-s013.json, Go-trace
  sched/syscall/sync tops, strace -c heads), `x5/` (results + table), `protos/` (diffs vs s013-plain:
  `p-dwell`, `p-t4dec`, `p-falloc`, `p-stack`), `S013-vs-S012.diff`, `closed-ground.md`.
- **Box** `/opt/league/restic-costmap5/`: `bin/`, `src/`, `runs/{main5,x5}/` (results.jsonl, status.json,
  artifacts), `tools/`.
- **Tools** `cost-map-5/tools/`: `build5.sh`, `build_proto5.sh`, `proto_edit5.py` (`dwellall t4dec nofalloc nogc`),
  `gen_main5.py`, `gen_x5.py`, `launch5.sh` (one unit on CPUs 2-5), `tables5.py main5|x5`, `analyze5.sh`, `box.py`
  (`sh`, `cm` = confined to CPUs 2-5, `py`, `put`, `get`). `census_run.py`, `census.go`, `patch_census.py`,
  `categorize.py` are cost-map-4's.
