# Restic League V2: speed map (cost map 4, the S012 incumbent)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-10-01 (box time 08:15-08:42 CEST) on the league box with the league paused, using CPUs 2-5 and the research lanes
6-9, 12-15, 19-22 and 42-45 (one `census_run.py` per lane, 4 CPUs each, slice `costmap4.slice`). The judge lanes were not
used. The instrument is cost-map-3's (judge sandbox, argv, starting states, `rusage-launch`, GOMAXPROCS=4), extended with
Go execution traces, full strace, and a stdout target per run. Everything is measured unless marked as a bound or an
estimate. Prototypes are research grade: interleaved reps against s012-plain on the same lane. They are not the judge.
Raw data is in `data/`, the log in `PROGRESS.md`, closed ground in `data/closed-ground.md`. The directive built from this
map is `runtime/S013-directive.md`.

## 0. Headline

1. **S012 takes 23.23 s (n=10 per phase), 592 MiB summed over phase peaks.** Stock takes 27.76 s (1029 MiB), so S012
   saves 16.3% of the time and 42.4% of the memory. **Under the S013 time-only score, 1 s saved = 4.30 points.** A
   per-process constant counts 7 times, so 0.1 s per process = 3.0 points. The memory guard is 2% of mean peak, which
   is about **11.8 MiB summed over the 7 phase peaks**.
2. **Speed has barely moved since S008.** Totals were S006 24.93 s, S008 23.95, S009 23.75, S010 23.39, S011 23.28 and
   S012 23.23. Over the same seasons memory fell from 762 to 592 MiB. S006 to S008 time gains were mostly prune
   (async repack, -1.0 s). Since then each season saved 0.05-0.36 s.
3. **The single clear time regression bought with memory is S012's backup buffer pool.** The 2 MiB assembly pool plus
   the 64 KiB small-file pool make backup-initial +0.14 s slower than S011, and backup-initial now runs 608 GC cycles
   (S011: 420; stock: 328). Reverting the pool as a bound (`bigpool`) gave **backup-initial -0.19 s but +37 MiB**,
   which fails the guard. Other memory changes cost nothing measurable: S008's bounded read buffers (bu +0.05, bm +0.10
   s, both within noise) and S011's double index encode (`idx1enc` null, +0.02 s).
4. **14.8% of the workload (3.4 s) is per-process constant:** the KDF takes 0.20 s at 4 cores, the lock dwell 0.20 s
   idle, and the index load 0.06-0.10 s at 1.6 cores. Overlapping the dwell is the largest legal lever. A prototype
   that preloads the index inside the dwell measured **-0.52 s** (10 reps, every rep positive). The removal bound is
   -1.89 s. The bound exceeds the 1.41 s of sleep it removes, which matches finding 6.
5. **42% of the 4-core budget is idle** (39.5 of 92.9 core-seconds). Only backup-initial is CPU-bound (3.33 cores).
   Restore averages 2.0 cores, check 2.3, prune 2.1, backup-unchanged 1.9, backup-mutated 2.1 and ls 1.4.
6. **New: the box's CPU clock is a large hidden term.** The governor is `schedutil` (acpi-cpufreq, boost on). Idle
   cores sit at 1.4-1.5 GHz and busy ones at 3.8 GHz, and the declared C2 exit latency is 800 us. Every tree, stock
   included, is **bimodal** on backup-unchanged, backup-mutated and ls. The fast mode is about 0.5 s faster and uses
   about 30% less CPU for the same work. For S012: bu 2.2-2.5 s at 4.0 CPU-s vs 2.8-3.2 s at 5.6 CPU-s; ls 1.26 s at
   1.6 CPU-s vs 1.8 s at 2.5 CPU-s. **This explains the ls "FIFO gap" (open since cost-map-2).** It is not the output
   path. The same JSON-decode function costs 0.24 s of CPU with a FIFO, 0.28 s to a file and 0.37 s to /dev/null,
   because the co-running FIFO reader keeps the lane's clocks up. Code that leaves cores idle, or that bounces one
   goroutine's work across cores, pays the clock-down.
7. **Prototypes (10 interleaved reps, section 7):** `stack` (dwell index preload plus no-poller opens plus single-encode
   index save) **-0.77 s, +3.3 points, +0.6 MiB**. `lockov` -0.52 s (+2.2). `nopoll` -0.49 s (+2.1). The S012 T4 M004
   tree as built **-0.75 s (+3.2) but +24 MiB**: restore -0.36 s and ls -0.20 s from its direct tree-node decoder.
   `connections=3` saves check -0.90 s for +7.9 MiB (this fits the guard alone), restore -0.75 s for +25 MiB and prune
   -0.11 s for +14 MiB.

## 1. Method

- Binaries (`/opt/league/restic-costmap4/bin/`) use the judge toolchain go1.27.0 and judge flags. `s010/s011/
  s012-plain`, `s011/s012-census` (census marks, process CPU, `profile` tag for `--cpu-profile/--trace-profile`) and the
  prototypes were built here. stock, S006, S008 and S009 were reused from cost-map-3. Builds are in `tools/build4.sh`
  and `build_proto4.sh`; prototype edits are in `tools/proto_edit4.py`.
- Runs:
  - `main4` (496 runs, 0 failures): plain stock/S006/S008/S009/S010/S011/S012 x5, interleaved with the binary order
    rotated per rep and one rep per lane; S011/S012 x5 more.
  - Census S012 x3, S011 x2, stock x1. CPU profiles S012 x2, S011 x1. Go execution traces for all three.
  - gctrace, heap at peak, alloc profile, `strace -c` S012/S011, `strace -f -T` for ls (file and FIFO), GOGC=off x2.
  - `local.connections=3`; ls stdout to a FIFO, /dev/null and a disk file, x5 each.
  - Prototypes: `x1` (8 binaries x 7 phases x 5 reps) and `x2` (6 binaries x 7 x 10 reps, plus ls CPU profiles to
    file, FIFO and null).
- Lanes run concurrently, one census process each. Each binary meets each lane equally often, and prototype
  comparisons are paired within a lane. Noise: per-phase medians move ±0.05-0.1 s between rounds. backup-initial has
  rare +2.5-3 s outliers on lane 12-15 for every tree (not code; medians absorb them). Peak memory moves ±3-6 MiB per
  phase between rounds, which is the same size as the guard. **Measure memory with 10+ reps before trusting a delta
  under 10 MiB.**

## 2. Phase baseline (plain medians, seconds and peak MiB; `data/main4/summary/tables.md`)

| phase | stock | S006 | S008 | S009 | S010 | S011 | **S012** | S012 cores | share of S012 |
|---|---|---|---|---|---|---|---|---|---|
| backup-initial | 5.67 (198) | 5.38 (144) | 5.27 (118) | 5.26 (113) | 5.24 (112) | 5.28 (109) | **5.42 (101)** | 3.33 | 23.3% |
| backup-unchanged | 3.47 (139) | 3.01 (98) | 3.06 (78) | 2.91 (77) | 2.89 (77) | 2.94 (77) | **2.88 (65)** | 1.92 | 12.4% |
| backup-mutated | 2.94 (169) | 3.02 (115) | 3.12 (86) | 3.12 (89) | 3.04 (86) | 3.17 (88) | **3.06 (88)** | 2.05 | 13.2% |
| restore | 7.00 (219) | 6.23 (121) | 6.22 (123) | 6.12 (113) | 6.12 (117) | 5.67 (114) | **5.61 (114)** | 1.99 | 24.1% |
| check-read-data | 2.87 (102) | 2.78 (87) | 2.74 (78) | 2.82 (77) | 2.76 (77) | 2.74 (77) | **2.81 (75)** | 2.28 | 12.1% |
| forget-prune | 3.22 (133) | 2.72 (139) | 1.72 (136) | 1.68 (122) | 1.64 (121) | 1.66 (104) | **1.68 (92)** | 2.12 | 7.2% |
| ls-json | 2.60 (68) | 1.79 (58) | 1.83 (58) | 1.83 (58) | 1.69 (58) | 1.83 (58) | **1.78 (58)** | 1.36 | 7.7% |
| **total** | **27.76 (1029)** | **24.93 (762)** | **23.95 (678)** | **23.75 (650)** | **23.39 (648)** | **23.28 (627)** | **23.23 (592)** | | |

Season deltas (s): S008 vs S006 bi -0.12, bu +0.05, bm +0.10, fp -1.00. S009: bu -0.14, rs -0.10, ck +0.08. S010: bm
-0.08, ls -0.14. S011: rs -0.45 (verify-open skip), bm +0.13, ls +0.14. **S012: bi +0.14**, bm -0.12, ck +0.08. Anything
under 0.1 s is noise.

## 3. Where the 23.2 s goes (S012 census marks, n=3; process CPU per stage)

| element | seconds | share | cores | CPU-s | removable? |
|---|---|---|---|---|---|
| spawn, runtime init, exit | 0.02 x 7 = 0.14 | 0.6% | - | - | no |
| **KDF** (scrypt, repository params, parallel) | 0.20-0.22 x 7 = **1.43** | 6.2% | 3.96 | 0.80 each | closed (repository-defined). The cores are clocked up afterwards |
| **lock dwell** (`waitBeforeLockCheck` 200 ms) | 0.201 x 7 = **1.41** | 6.1% | 0.05 | 0 | overlap only (ruling). Bound -1.89 s |
| index load (+2 forced `runtime.GC()`) | bu .077, bm .063, rs .104, ck .087, fp .066, ls .065 = **0.46** | 2.0% | 1.6 | 0.12 each | overlap into the dwell (-0.52 s m) and decode faster |
| bi snapshot | **4.97** | 21.4% | 3.44 | 17.1 | CPU work (section 4) |
| bu / bm snapshot | 2.39 / 2.28 | 10.3 / 9.8% | 1.9 / 2.1 | 4.6 / 4.7 | idle 50%; parent tree loads; per-file stat/xattr |
| restore first pass (tree walk, mkdir, plan) | **0.83** | 3.6% | 1.19 | 0.99 | serial; trees decoded |
| restore download (2 workers) | **3.26** | 14.0% | 2.14 | 6.99 | worker count, per-file syscalls |
| restore second pass (metadata, 4 workers) | **1.23** | 5.3% | 1.90 | 2.34 | fchownat/fchmodat/utimensat per file; trees decoded again |
| check structure / read-data (2 workers) | 0.16 / **1.90** | 0.7 / 8.2% | 3.1 / 2.32 | 0.5 / 4.4 | read worker count; sha256 1.6 CPU-s |
| prune plan / execute | 0.23 / **0.90** | 1.0 / 3.9% | 2.8 / 2.24 | 0.6 / 2.0 | repack, index rewrite |
| ls walk + output | **1.28** | 5.5% | 1.21 | 1.55 | serial tree load + decode; per-line goroutine handoff |

**Per process: 0.02 + 0.20 + 0.20 + ~0.08 = 0.50 s, x7 = 3.4 s (14.8%).** The memory floor after the KDF is ~56 MiB
(`cat config` 56.2 MiB, `version` 23 MiB). Idle core-seconds (4 x wall - CPU): bi 3.7, bu 6.0, bm 5.9, rs 11.2, ck 4.8,
fp 3.2, ls 4.7, **total 39.5 of 92.9 (42%)**.

**Waiting** (Go execution traces, `data/main4/summary/trace/`):
- **Scheduling latency** (runnable but not running): restore 3.2 s cumulative in `selectgo` (worker pools woken by
  channel sends), bi 2.6 s plus 1.1 s system-stack switches (CPU saturation). Every phase shows 0.2-0.9 s in
  `goschedIfBusy` (GC background work).
- **Blocking syscalls:** restore `fallocate` 0.78 s, `openat` 0.72, `pwrite` 0.37, `fchownat` 0.28, `fcntl` 0.24,
  `fchmodat` 0.21, `utimensat` 0.21 (summed over goroutines). ls writes 29 ms to a file and 143-193 ms to a FIFO or
  /dev/null (unbatched).
- **Lock wait:** 1.41 s idle, 7 x 200 ms. **fsync:** none on the measured path (local backend on tmpfs).
- **Channel/worker starvation:** check read-data and restore download have 2 workers on 4 cores (2.3 and 2.1 cores
  used). `connections=3` cuts check by 0.90 s and restore by 0.75 s.
- **GC:** GC CPU 4% in bi (608 cycles, 0.93 CPU-s), 1-2% elsewhere (restore 0.76 CPU-s). GOGC=off is slower in every
  phase except ls (bi +0.68 s: page faults on a 6.6 GB heap), so GC is not a time lever. It is context only; the knob
  is closed.

**Syscalls per phase (`strace -c`, counts):**
- backup-initial: futex 475k, fcntl 555k (6 per file), read 401k, nanosleep 154k (runtime spinning/usleep),
  newfstatat 183k (2 per file), getrandom 155k (a nonce per blob), write 84k, close/openat/fstat/llistxattr 91-93k each,
  epoll_ctl 92.6k (all failing: poller registration of regular files).
- restore: fcntl 371k, openat 93k, fallocate 89k, fchownat/fchmodat/utimensat/llistxattr 91k each, pwrite 90k, epoll_ctl
  92.7k (all failing), futex 57k.
- bu: newfstatat 183k, llistxattr 91k, fcntl 64k. check: read 2.2k, futex 31k.

## 4. CPU by category (S012, CPU-s per run; `data/main4/summary/cpu-categories-s012.json`)

| phase | CPU-s | categories |
|---|---|---|
| backup-initial | 17.9 | syscalls (incl. futex) 4.0, zstd compress 3.0, sha256 2.5, chunker 1.55, AES 1.1, scrypt ~0.8, zstd decompress (save verify) 0.58, memmove 0.53, GC 0.45 |
| restore | 11.1 | syscalls 3.8, sha256 1.1, zstd decompress 0.70, tree JSON 0.68, AES 0.6, GC 0.5, scrypt 0.8 |
| check-read-data | 5.8 | sha256 1.6, zstd decompress 0.6, AES 0.5, syscalls 0.4, JSON 0.24, scrypt 0.8 |
| bu / bm | 5.6 / 5.4 | syscalls 1.5 / 1.3, chunker 0.5, sha256 0.4-0.5, JSON 0.3 / 0.2, scrypt 0.8 |
| forget-prune | 3.6 | syscalls 0.4, sha256 0.35, JSON 0.3, zstd 0.25+0.19, AES 0.2, scrypt 0.8 |
| ls-json | 2.5 | scrypt 0.8 (`parallelSalsaXOR`), tree JSON decode 0.55, encode ~0.3, syscalls 0.09 |

On backup-initial (3.33-3.44 cores) one CPU-second removed is worth about 0.29 s of wall. On the other phases CPU
matters only on the serial critical path: ls walk, restore first pass, the index load.

## 5. What S008-S012 memory work cost in time

| change | season | time cost measured here | memory it bought |
|---|---|---|---|
| 2 MiB assembly pool + 64 KiB small pool, copy on growth | S012 | **bi +0.14 s vs S011; reverting = bi -0.19 s (`bigpool`, 5 reps) but +37 MiB**; bi GC cycles 420 -> 608 | bi -8, bu -12 MiB |
| bounded backup read buffers | S008 | bu +0.05, bm +0.10 s (noise level) | bu/bm/bi -20 to -26 MiB |
| checker stream reader 1 MiB | S008 | ck -0.04 (none) | ck -9 |
| streamed index save, verify by second encode | S011 | `idx1enc` (hash instead of re-encode): +0.02 s, null | fp -13 |
| streamed index verify comparator | S010 | none (S010 ls -0.14) | fp -8 |
| parallel 2nd restore pass | S009 | none (time gain) | rs -7 |

Net: memory work has not taxed time much except the S012 backup pool, about 0.15-0.2 s on backup-initial. **The time
that S008-S012 left behind is idle cores and serial stretches, not memory overhead.**

## 6. ls: the output gap is the CPU clock

| ls stdout | wall | user+sys | voluntary switches | JSON `ConsumeSimpleString` CPU |
|---|---|---|---|---|
| regular file, tmpfs (judge-like) | 1.78-1.84 | 2.45 | 5.2k | 0.28 s |
| regular file, disk | 1.77 | 2.45 | - | - |
| FIFO with reader | 1.36-1.41 | 2.02 | 30k | 0.24 s |
| /dev/null | 2.16-2.27 | 3.29 | 49k | 0.37 s |

- The batched file path makes 242 writes in total, and the profile shows no extra function. The same code takes 15-30%
  more CPU to /dev/null than to a FIFO. In the FIFO case the co-running reader keeps the lane busy, so schedutil holds
  the clock. In the census build, a 2 ms sampler goroutine had the same effect: stock ls ran 1.87 s in census and 2.60
  s plain. 2 of 15 plain S012 ls runs landed in the fast mode (1.26 s at 1.6 CPU-s) with no change at all.
- Unbatched output (null and FIFO) costs a channel handoff to the termstatus goroutine per line (91k), with 748 ms of
  scheduling latency in `lineWriter.Write`. The batching installed in S004 is right for regular files. Keep it.
- **Lever:** shorten the serial walk. That means cheaper tree decode (S012 T4's direct decoder: ls -0.20 s) and
  prefetching or decoding trees on the idle cores, with the output order unchanged. Fewer cross-core handoffs on the
  critical path also help. **Not a lever:** spinning or dummy work to hold clocks. That games the host, not restic
  (see the directive's ground rules).

## 7. Prototypes (`data/protos/`, `data/runs/x2/prototypes.md`; 10 interleaved reps unless noted)

| prototype | edit (on s012-plain) | per-phase time (s) | total | points | memory |
|---|---|---|---|---|---|
| `lock0` (x1, 5 reps) | dwell 0 ms. **Bound only; removal is not allowed** | every phase -0.21 to -0.36 | **-1.89** | +8.1 | +1 |
| **`lockov`** | index load started by a hook after `createLock`, inside the unchanged dwell (cost-map-3 lockov2 port; check excluded) | bi -0.06, bu -0.10, bm -0.06, rs -0.09, ck -0.04, fp -0.07, ls -0.10 | **-0.52** | **+2.2** (all 10 reps positive) | -9 (noise) |
| **`nopoll`** | `fs.OpenFile` and `newLocalFile` via `syscall.Open` + `os.NewFile` (no poller attempt: -3 fcntl, -1 failing epoll_ctl per open) | bi -0.15, bu -0.12, bm -0.05, rs -0.14 | **-0.49** | **+2.1** | -7 (noise) |
| `idx1enc` | index save: SHA-256 of the JSON while compressing; verify = decrypt + decompress + hash compare (no second encode) | fp +0.01 | +0.02 | -0.1 | -6 |
| **`stack`** | lockov + nopoll + idx1enc | bi -0.15, bu -0.09, bm -0.11, rs -0.22, ck -0.04, fp -0.08, ls -0.08 | **-0.77** | **+3.3** (paired +3.7) | **+0.6** |
| `bigpool` (x1, 5 reps) | S012's 2 MiB pool back to MaxChunkSize (bound of S012's time cost) | bi -0.19 | -0.21 | +0.9 | **+37 (fails guard)** |
| `t4m004` | S012 T4 M004 candidate tree, unmodified (based on S011) | rs -0.36, ls -0.20, fp -0.10, bm -0.09, bu -0.07, ck -0.06, **bi +0.13** | **-0.75** | **+3.2** (paired +3.6) | **+24 (fails guard)**: fp +14, ls +5, bm +4 |
| `connections=3` (main4) | knob, not a candidate | ck -0.90 (+7.9 MiB), rs -0.75 (+25), fp -0.11 (+14) | -1.77 | +7.6 | +47 |

Notes.
- `lockov` must start after the lock file exists (cost-map-3: starting before `LockRepo` gained nothing). The dwell has
  ~0.1 s of headroom left per phase.
- `nopoll` is a sound mechanism (same open flags; errors as `*os.PathError`). A real version must keep Windows and
  other platforms on the existing path, keep O_CREATE permission semantics, and cover every hot open: archiver reads,
  restore writes, pack reads.
- `t4m004` is the best measured time tree in the league. Its memory cost comes from held index plaintext (ls, prune)
  and from lacking S012's own memory work. A port onto S012 that keeps the tree decoder and the dwell preparation but
  bounds the held plaintext is the top lead.

## 8. Remaining speed headroom: ranked levers

1 s = 4.30 points. "(m)" = measured above. A ceiling is a measured share times a plausible reduction.

| # | lever | mechanism and files | measured share | ceiling s | points | memory |
|---|---|---|---|---|---|---|
| 1 | **Lock-dwell overlap, more work** | `repository/lock_file.go` (`createLock` -> dwell), `cmd/restic/lock.go`, commands. In the dwell: index load (m), snapshot list/load, the parent tree for bu/bm, prune planning, the first tree reads for restore/ls, index plaintext preparation (T4) | 1.41 s idle; bound -1.89 (m) | **-0.5 (m) to -1.0** | +2.2 to +4.3 | must hold no more than the work would anyway |
| 2 | **Third read worker, bounded** | `backend/local` Connections, `checker.go`, `filerestorer.go`, `repack.go`, `streamPack` | ck -0.90 for +7.9 MiB; rs -0.75 for +25 | **-0.9 (check alone) to -1.7** | +3.9 to +7.3 | ck fits the guard alone; restore needs the per-worker part + `DecodeAll` + bufio bounded to <= ~4 MiB. **Product ruling needed** |
| 3 | **Tree decode speed** | `data/tree.go`, S012 T4's `data/tree_decode.go` (direct node decoder) | rs 2 x 0.34 CPU-s decode, ls 0.55 CPU-s on the serial walk | **-0.4 to -0.6** (T4 m: rs -0.36, ls -0.20) | +1.7 to +2.6 | neutral if streamed |
| 4 | **Opens without the poller** | `fs/file.go`, `fs/fs_local.go` (`setFlags` adds 2 fcntl), restorer `fileswriter.go` | 4 fcntl + 1 failing epoll_ctl per open; 555k / 371k fcntl in bi / rs | **-0.49 (m)** | +2.1 | 0 |
| 5 | **Restore per-file syscalls** | `restorer/fileswriter.go` (fallocate), `restorer.go` (`restoreNodeMetadata`: fchownat/fchmodat/utimensat/llistxattr) | ~12 syscalls per file x 91k; fallocate 0.78 s, openat 0.72 s blocking | -0.3 to -0.5 | +1.3 to +2.2 | 0 |
| 6 | **Restore first and second pass** | `restorer.go` (`traverseTree`, the S009 metadata pool) | pass 1 0.83 s at 1.19 cores; pass 2 1.23 s at 1.90; trees decoded in both | -0.3 to -0.6 | +1.3 to +2.6 | small (bounded prefetch) |
| 7 | **Backup-initial CPU** | archiver (`fileSaver`, scanner), `repository.saveAndEncrypt`, `crypto` (nonce), `fs` (stat) | 17.9 CPU-s at 3.4 cores: 2 stats per file, 155k getrandom, 475k futex + 154k nanosleep (handoffs), 608 GCs | -0.3 to -0.6 | +1.3 to +2.6 | must not regrow the pool (bigpool +37) |
| 8 | **Backup buffer pool, the time it costs** | `archiver/file_saver.go`, `buffer.go` | bigpool bound bi -0.19 s for +37 MiB | -0.1 to -0.2 | +0.4 to +0.8 | find the copy-on-growth path; no bigger pool |
| 9 | **Index load decode** | `index/index.go` decode, S012 T4's `decode_canonical.go`, the forced `runtime.GC()` pair in `LoadIndex` | 0.46 s at 1.6 cores over 6 processes | -0.1 to -0.2 beyond item 1 | +0.4 to +0.9 | 0 |
| 10 | **ls critical path** | `cmd_ls.go`, `walker`, termstatus | walk 1.28 s at 1.21 cores; clock-down (section 6) | -0.2 to -0.4 | +0.9 to +1.7 | <= 2 MiB |
| 11 | **Check read-data CPU** | `checker.go` (sha256 per blob + pack hash; both must stay) | 1.90 s at 2.32 cores | with item 2 only | | |
| 12 | **Prune execute** | `prune.go`, `repack.go`, index rewrite | 0.90 s at 2.24 cores; plan 0.23 s (could move into the dwell, item 1) | -0.1 to -0.2 | +0.4 to +0.9 | prune peak is the index rewrite: watch it |

**Closed / not worth it for speed:** KDF (repository-defined, already parallel). GOGC either way (slower). The extra
verify on save (must stay). Single-encode index verify (`idx1enc` null). Reverting S012's pool (fails the guard). ls
output batching (it is the right path; the gap is the clock).

**Stacking envelope.** Items 1+4 measured together -0.77 s (+3.3). Adding a bounded check worker (-0.9) and T4's tree
decoder (-0.4 to -0.6) gives a realistic S013 envelope of **-1.5 to -2.5 s (+6 to +11 points)** over S012, if the
memory side stays inside 11.8 MiB summed.

## 9. Data, binaries, tools

- **Local** `cost-map-4/data/`: `main4/summary/` (tables.md, summary.json/txt, `pprof/` CPU/alloc/heap-at-peak for
  S012/S011/stock, `trace/` sched/sync/syscall blocking profiles per phase, `strace/` per phase plus full ls
  file/FIFO), `runs/x2/` (prototype table, ls profiles), `protos/` (diffs vs s012-plain:
  lock0/lockov/nopoll/idx1enc/bigpool/stack, `t4m004-vs-s012.diff`, x1 table), incumbent diffs
  `S010-vs-S009.diff` .. `S012-vs-S011.diff`, `S012-vs-S008.diff`, `closed-ground.md`.
- **Box** `/opt/league/restic-costmap4/`: `bin/` (plain, census and prototype binaries), `src/`,
  `runs/{main4,x1,x2}-l{0..4}/` (results.jsonl, status.json, artifacts: cpu.pprof, trace.out, marks.json, strace),
  merged `runs/{main4,x1,x2}/`, `tools/`.
- **Tools** `cost-map-4/tools/`: `census_run.py` (lane-aware: `COSTMAP_LANE`, `COSTMAP_CPUS`; `strace_full`, `stdout`
  target), `launch.sh` (one unit per lane), `build4.sh`, `build_proto4.sh`, `proto_edit4.py` (`lock0 lockov bigpool
  idx1enc nopoll`), `gen_main4.py`, `gen_x.py`, `gen_x2.py`, `tables4.py RUN`, `analyze4.sh`, `lsprof.sh`,
  `bimodal.py`, `bx.py` (box helper).
