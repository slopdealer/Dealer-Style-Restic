# Restic League V2: speed map (cost map 7, the S015 incumbent)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-10-02 21:30-22:30Z on the league box while S016 ran. **Only CPUs 2-5 were used** (systemd slice
`costmap7.slice`, `AllowedCPUs=2-5` plus `taskset 2-5` on every build, run and analysis). The judge lanes and S016's
research lanes were not touched, no governor was changed, and S016's workspace was read only. The instrument is
cost-map-6's (judge sandbox, argv, starting states, `rusage-launch`, GOMAXPROCS=4) on one lane. Raw data is in `data/`,
the log in `PROGRESS.md`, closed ground in `data/closed-ground.md`. The directive built from this map is
`runtime/S017-directive.md`.

**One clock.** CPUs 2-5 run `schedutil`; the judge and research lanes run `performance`. Every timed run in this map
is **clock-held**: four idle-priority (`SCHED_IDLE`) spinners pinned to CPUs 2-5 keep the clock at the top while the
measured process preempts them at once (cost-map-6's method; no governor change). Plain S015 then reads within -4.3%
to +5.7% of the judge on every phase (section 2), and S016 T3's judged tree reads check -0.457 s here against -0.451 s
on the judge. Deltas in this map are judge seconds. The spinners are an instrument on the research side only and stay
banned in candidates.

## 0. Headline

1. **S015 on the judge: 17.51 s, 575.8 MiB summed** (stock 22.60 s; S014 17.75 s; job `f3628902...`, 42 rounds).
   **1 s = 5.71 points; 0.1 s per process x7 = 4.00 points.** The memory guard (+2%) is **11.5 MiB summed over the 7
   phase peaks**.
2. **What S015 removed (judge, 42 rounds, vs S014): -0.242 s.** The index load moved inside the 200 ms lock wait in
   restore (-0.064 s), check (-0.064), forget-prune (-0.058) and ls (-0.061). Backups unchanged (they had it since S013).
3. **The largest remaining time** (judge seconds): bi snapshot ~4.7 (CPU-bound, but 3.3 of 4 cores); restore download
   ~2.35 at 2.2 cores, second pass ~0.76, first pass ~0.42 at 1.1 cores; check read-data ~1.8 at 2.3 cores; bu/bm
   snapshot ~1.43-1.53 each; **KDF 1.44 and dwell 1.40 over the 7 processes, of which 1.05 s of the dwell is now idle**
   (the index load fills only its first 0.055-0.072 s). Nothing is left between the dwell and the work.
4. **The restore worker's memory owner is found: large blobs.** Heap dumps at the RSS peak show the two workers
   holding 16 MiB of range buffers (a single blob is never split, so a pack of ~8 MiB blobs is read one whole blob at a
   time) plus 7 MiB of decoded copies, under a collector set to 50% (1 live MiB = 1.5 MiB of peak). A third
   unrestricted worker adds ~16 MiB live. cost-map-6's smaller ranges could not help: the range was already one blob.
5. **Prototypes, clock-held (judge seconds, 8 interleaved reps, every row 8/8 unless stated):**
   - **Bounded third restore worker (`p-rs3s`: small-blob packs only, 1 MiB ranges): -0.42 s (+2.4 points),
     +1.6 MiB.** Unbounded: -0.648 s, +19.6 MiB.
   - **Restore second-pass window: -0.305 s at 64, -0.305 at 256, -0.313 at 1024 (+1.7 to +1.8 points), memory flat.**
     Flat from 64 up. S016's judged rows: 64 = -0.240 s, 32 = -0.164 and -0.209 s.
   - **Bounded first-pass tree prefetch (`p-pf1b`): -0.154 s (+0.9), memory flat.** With the window at 256
     (`p-rs2b`): **-0.474 s (+2.7), memory flat.**
   - **Full restore stack (`p-rsall` = window 256 + first-pass prefetch + bounded third worker): -0.88 s
     (+5.0 points), +2.0 to +3.0 MiB.** Without the worker slot (2 connections): -0.495 s.
   - **Third check worker: -0.549 s (+3.1), +10.3-11.4 MiB unbounded; S016 T3's bounded tree -0.457 s (+2.6),
     +4.3-4.8 MiB; cost-map-6's `p-ck3b` -0.389 s, +4.4-6.0 MiB.**
   - Second repack worker: -0.115 s (+0.7), +8.3-10.5 MiB. `connections=3` on ls: null.
   - Backup knobs: `--no-scan` reads -0.053 / -0.014 / -0.018 s on bi / bu / bm (-0.085 s, +0.5 points), memory flat or lower; `--read-concurrency=4` on backup-initial reads -0.278 s (+1.6 points) for +24.9-27.0 MiB: over the guard twice.
6. **Surprises.**
   - **The restore memory owner was hiding in plain sight**: the "1/4 pack" range limit does not apply to a blob larger
     than the limit, and the workload's large files are stored as ~8 MiB blobs.
   - **The second-pass window saturates at 64.** cost-map-6 only tried 256. T1's 64 already takes all of it; T4's 32
     takes about two thirds.
   - **S016 T3 built the bounded check worker without touching `ParseConfig`** (a check-only slot in `backend/sema`),
     and it reads the same here as on the judge. The clock-held lane is a good predictor.
   - **1.05 s of the 1.40 s dwell is idle** after S015. It is the largest per-process item left, and it is free of
     memory cost.
   - The S014 comparison arm of the main run was lost to a rename bug in the setup script; S015 vs S014 comes from the
     judge's own 42 rounds and from `x9b`.

## 1. Method

- Binaries in `/opt/league/restic-costmap7/bin/`: `s015-plain` and `s015-census` built here with the judge
  toolchain go1.27.0 and judge flags (`tools/build7.sh`); `stock-plain` and `s014-plain` are cost-map-6's. Prototypes
  are `p-*` (`tools/build_proto7.sh`, edits in `tools/proto_edit7.py`). `t3m001-plain` is S016 T3's M001 team tree,
  built read-only from the workspace.
- `main7` (142 runs, 0 failures, clock-held): plain S015 x10 and stock x4, interleaved and rotated; S015 census x2
  (stage marks with process CPU); CPU profile, Go execution trace, `strace -c`; trivial commands x3.
- `mem7` (12 runs): census heap dumps at the RSS peak (`RESTIC_CENSUS_HEAP=1`, `GODEBUG=memprofilerate=16384`) and
  `gctrace`, restore and check, connections 2 vs 3.
- `x9a` (168 runs, clock-held, 8 interleaved reps): window 64/256/1024, `p-pf1b`, `p-rs2b`, `conn3` on rs/ck/fp/ls,
  `p-ck3b`, T3's tree. 24 `--no-scan` runs failed on argument order and were rerun in `x9b`.
- `x9b` (144 runs, clock-held, 8 interleaved reps): `p-rs3s`, `p-rsall` with and without the third slot, backup
  `--no-scan=true` and `--read-concurrency=4`, and `s014-plain` on rs/ck/fp/ls.
- Stock compatibility tests: `go test ./internal/restorer` passes on `p-md1024`, `p-rs2b` and `p-rsall`;
  `./internal/repository` passes on `p-rsall`.
- Noise here (clock-held, S015 per-phase range over 10): bi 5.34-5.49, bu 1.89-1.91, bm 1.98-2.02, rs 4.11-4.18,
  ck 2.29-2.32, fp 1.39-1.42, ls 0.90-0.92 s. On the judge the 42-round S015 ranges were bi 5.06-5.19, bu 1.84-1.86,
  bm 1.92-1.96, rs 3.94-4.00, ck 2.25-2.31, fp 1.44-1.50, ls 0.89-0.91.

## 2. Phase baseline

| phase | stock (judge) | S014 (judge) | **S015 (judge)** | S015 - S014 (judge) | S015 MiB (judge) | S015 clock-held | held/judge | stock clock-held | cores (held) | judge share |
|---|---|---|---|---|---|---|---|---|---|---|
| backup-initial | 5.502 | 5.106 | **5.122** | +0.016 (19/42) | 99.7 | 5.412 | 1.057 | 5.807 | 3.22 | 29.3% |
| backup-unchanged | 2.217 | 1.852 | **1.847** | -0.005 | 64.1 | 1.900 | 1.029 | 2.289 | 2.02 | 10.6% |
| backup-mutated | 2.345 | 1.945 | **1.939** | -0.006 | 83.8 | 1.999 | 1.031 | 2.407 | 2.18 | 11.1% |
| restore | 6.067 | 4.022 | **3.958** | **-0.064** | 110.7 | 4.138 | 1.045 | 6.158 | 2.00 | 22.6% |
| check-read-data | 2.537 | 2.345 | **2.281** | **-0.064** | 74.7 | 2.307 | 1.011 | 2.569 | 2.36 | 13.0% |
| forget-prune | 2.460 | 1.523 | **1.465** | **-0.058** | 85.9 | 1.402 | 0.957 | 2.296 | 2.23 | 8.4% |
| ls-json | 1.472 | 0.956 | **0.895** | **-0.061** | 56.9 | 0.908 | 1.015 | 1.498 | 1.63 | 5.1% |
| **total** | **22.60** | **17.75** | **17.51** | **-0.242** | **575.8** | **18.07** | 1.032 | 23.02 | | |

The judge columns are the S015 42-round confirmation (job `f3628902f87a4b8c9d1729472849a2d9`): S015 vs S014 time
+1.47% (42/42), memory 0.29% higher; vs stock time +22.58%, memory +42.26%. S016's incumbent arms (S015 tree, 6 rounds
each) read 17.54-17.62 s. Clock-held S014 vs S015 on this lane (`x9b`): S014 is slower by restore +0.063 s, check +0.076, forget-prune +0.071, ls +0.054 = +0.264 s (judge +0.247), 0/8 in every phase.

**Score arithmetic on S015 (judge): 1 s = 5.71 points; 0.1 s per process x7 = 4.00 points; the +2% memory guard is
11.5 MiB summed over the 7 phase peaks** (1.64 MiB on the mean). The guard is relative to the season's incumbent, so it
resets after a promotion.

## 3. Where the time goes (S015 census, n=2, clock-held; judge estimates scale by the phase ratio in section 2)

| element | held s | cores | CPU-s | judge s (est.) | what it is |
|---|---|---|---|---|---|
| spawn, runtime init, exit | 0.004 x 7 | - | - | 0.03 | fixed |
| **KDF** (scrypt, 4 cores) | 0.205 x 7 = **1.44** | 3.9 | 0.80 each | ~1.44 | repository-defined. Closed |
| **lock dwell** (200 ms) | 0.200 x 7 = **1.40** | ~0 | 0 | **1.40** | see the next row |
| index load, now INSIDE the dwell | bu .055, bm .054, rs .056, ck .072, fp .055, ls .061 | 1.5-1.7 | 0.09-0.11 | 0 on the critical path | S015 installed this for rs, ck, fp, ls |
| **dwell idle after the index** | bi .199, bu .146, bm .146, rs .145, ck .129, fp .145, ls .140 = **1.05** | 0 | 0 | **1.05** | the largest per-process item left; free to fill with read-only work |
| bi snapshot | **5.08** | 3.29 | 16.7 | ~4.7 | CPU-bound; 0.7 core idle |
| bu / bm snapshot | 1.47 / 1.59 | 2.00 / 2.19 | 2.9 / 3.5 | ~1.43 / ~1.53 | lstat x2 per file (archiver + scanner), llistxattr, parent-tree decode |
| **restore first pass** | **0.44** | 1.13 | 0.50 | ~0.42 | serial: tree load + JSON decode, mkdir, file list |
| restore download (2 workers) | **2.45** | 2.16 | 5.29 | ~2.35 | per-file create/fallocate/pwrite; sha256, zstd |
| **restore second pass** (metadata) | **0.79** | 2.01 | 1.60 | ~0.76 | the walker waits on the metadata pool (window 8) |
| check structure / read-data | 0.10 / **1.83** | 3.6 / 2.33 | 0.36 / 4.27 | ~0.10 / ~1.8 | 2 read workers |
| prune plan / execute | 0.17 / **0.80** | 2.6 / 2.24 | 0.43 / 1.79 | ~0.17 / ~0.83 | repack runs ONE worker (`Connections()-1`) |
| ls walk + output | **0.47** | 1.19 | 0.56 | ~0.47 | serial tree load, decode, encode |

The memory floor is unchanged: `cat config` 55.5 MiB and 0.425 s (KDF plus the read-only dwell), `version` 22.5 MiB.

**What is left of the per-process fixed costs.** KDF 0.205 s and dwell 0.200 s per process: 2.84 s of 17.51 (16%).
KDF is closed. The dwell cannot be shortened (operator ruling) but 75% of it is idle. Index load is no longer on the
critical path in any phase.

**Waiting (Go execution traces, S015; summed over goroutines):**
- restore: 2.11 s of scheduling latency, **1.73 s of it under `restoreMetadataPool.schedule`**.
- check: 1.10 s, **0.55 s of it `chansend1`** (the producer handing packs to 2 busy workers), 0.24 s GC assist yields.
- bi: 7.4 s (2.4 s `selectgo`, 0.83 s GC assist yields, 0.48 s async preemption): the pipeline's handoffs.

**Syscalls (`strace -c`, S015):**
- bi: futex 215k, read 401k, newfstatat 183k, getrandom 155k, fcntl 104k, close/openat 93k, llistxattr 91k,
  nanosleep 90k (runtime), write 84k.
- bu: newfstatat 183k (2 per file), llistxattr 91k, futex 39k, read 34k.
- restore: fchownat/fchmodat/utimensat/llistxattr 91k each, openat 93k, fallocate 89k, pwrite 90k, fcntl 100k,
  futex 70k.

## 4. CPU by category (S015, CPU-s per run, one profile; `data/main7/cpu-categories-s015.json`)

| phase | CPU-s | top categories |
|---|---|---|
| backup-initial | 17.6 | syscalls 3.6, zstd compress 3.0, sha256 2.6, chunker 1.6, AES+poly 1.1, zstd decompress (save verify) 0.64, memmove 0.39, memclr 0.38, GC 0.55. By caller: `saveAndEncrypt` 8.8 (EncodeAll 3.8, `verifyCiphertext` 2.6), file readers 4.2, pack write + hash 1.3 |
| restore | 8.0 | syscalls 2.9, sha256 0.86, zstd 0.52, AES 0.49, GC 0.45, tree JSON 0.17 |
| check-read-data | 5.5 | sha256 1.55, zstd decompress 0.75, AES 0.48, syscalls 0.46, KDF 0.8 |
| bu / bm | 3.8 / 4.3 | syscalls 0.94 / 1.10, chunker 0.51 / 0.54, sha256 0.24 / 0.31, JSON 0.18 / 0.15, KDF 0.8; the scanner is 0.34 of bu |
| forget-prune | 3.1 | zstd 0.45, syscalls 0.32, sha256 0.30, AES 0.17, JSON 0.16, KDF 0.8 |
| ls-json | 1.4 | KDF 0.8, tree JSON decode/encode 0.33 |

## 5. What S015 removed, and what is left of cost-map-6's menu

**S015 (T4 M004) removed 0.242 s on the judge.** `cmd/restic/wait_index_load.go` starts `repo.LoadIndex` (or the
checker's index pass) in a goroutine when the lock wait begins; `lock.go` gained `openWith{Read,Exclusive}LockAndWaitWork`;
restore, check, prune, forget and ls consume the prepared index after the second lock check. Progress output is
buffered until then, a failed check discards the index, and debug mode keeps the synchronous path. The
`parent_tree.go` change skips an unquote when the name has no backslash (bu/bm -0.005 s: null).

| cost-map-6 item | S015 / S016 status | what is left on S015 (judge s) |
|---|---|---|
| 1 third read worker, check | **S016 T3 M001 built the bounded version: -0.451 s on the judge, check +5.6 MiB.** T1 M002 has its own | bounded -0.46 (m); unbounded -0.55 (m), +10-11 MiB |
| 2 third read worker, restore | not built by anyone | **owner found (section 6)**; bounded `p-rs3s` -0.42 (m) |
| 3 second-pass handoff | **S016 T1 M001 (64): -0.240 s; T4 M001/M002 (32): -0.164 / -0.209 s** | -0.31 at 64/256/1024 (m): flat from 64 |
| 4 first-pass prefetch | not built | bounded `p-pf1b` -0.154 (m), memory flat |
| 5 dwell overlap rs/ck/fp/ls | **INSTALLED by S015** (-0.242 s) | 1.05 s of idle dwell remains (section 3) |
| 6 buy memory headroom | not built | the restore peak's composition is now known (section 6) |
| 7 second repack worker | not built | -0.115 (m), +8-10 MiB unbounded |
| 8 decode each restore tree once | S016 review copies only (T2 B07, T3 B02/B08), unjudged | unchanged |
| 9 tree decode | not built | unchanged |
| 10 backup-initial CPU | not built | 0.7 core idle; `--read-concurrency=4` reads -0.278 s (+1.6 points) for +24.9-27.0 MiB: over the guard twice |
| 11 bu/bm snapshot | S016 T2 B15 dropped the separate scanner on a review copy; not integrated | `--no-scan` bound reads -0.053 / -0.014 / -0.018 s on bi / bu / bm (-0.085 s, +0.5 points), memory flat or lower |
| 12 restore per-file syscalls | not built | unchanged |

## 6. Who owns a read worker's memory

`mem7` took heap profiles at the RSS peak (sample rate 16 KiB) with 2 and 3 connections.

**Restore (peak 110-119 MiB; it falls 0.3-0.9 s into the download).** In-use heap at the peak, 2 workers, 52 MiB:

| owner | MiB | what |
|---|---|---|
| `repository.streamPackPartWithBuffer` | **16.0** | the range buffers of the two workers. `streamPack` splits ranges at 1/4 pack (4 MiB) **but never splits a blob**; the workload's large files are ~8 MiB blobs, so each worker holds an ~8 MiB range |
| `zstd.(*Decoder).DecodeAll` | **7.1** | the decoded copy of a large compressed blob (per iterator; garbage after each part) |
| `fileRestorer.addFile` | 7.1 | the 91k-entry file list |
| `filepath.Join` strings | 5.5 | the file locations |
| index | 4.3 | |

- The collector runs at GC percent 50, so the heap goal is 1.5x live: **gctrace peak goal 85 MB with 2 workers, 91 MB
  with 3; live after GC 43-51 MB vs 53-60 MB.**
- A third unrestricted worker adds one more large-blob range and decoded copy: up to 16 MiB live, 24 MiB of peak.
  Measured: +19.6 MiB (median), +20.5 (paired).
- **This is why cost-map-6's `p-rs3b` failed.** It cut the split target to 1/6 pack. On large-blob packs the range is
  one blob whatever the target.
- **The bound that works is by blob size, not by range size.** `p-rs3s` gives the third worker only packs whose needed
  blobs are all <= 1 MiB plaintext (marked while the pack list is built), 1 MiB ranges, and no slot in the 2-buffer
  pool. Result in section 7.

**Check (peak 74-78 MiB, late in read-data).** 2 workers, 25.6 MiB in use: blob buffer `bufReader.ReadFull` 8.7, zstd
stream decoder state 7.6, index 4.5, readers 2.0. With 3 workers: 30.9 MiB (decoder state 11.5, readers 3.0), live
after GC 28-32 MB vs 18-21 MB, peak goal 59 vs 44 MB. The same owner: a worker on a large blob holds the blob and a
decoder window. T3's and `p-ck3b`'s small-pack worker avoids it: +4.3-6.0 MiB instead of +10.3-11.4.

## 7. Prototypes

Diffs vs `s015-plain` are in `data/protos/`. `conn3` is the unmodified S015 binary with `-o local.connections=3`
(a knob used as a bound, not a candidate); `+c3` means the prototype was run with the same knob. Tables:
`data/x9/x9a-table.md` and `data/x9/x9b-table.md` (clock-held, 8 reps). "judge s" is the paired median; memory is the
peak delta (median; paired median in brackets). 1 s = 5.71 points.

| prototype | edit (on s015-plain) | **judge s (wins/8)** | points | memory MiB |
|---|---|---|---|---|
| **`conn3`, restore** | third download worker, unrestricted | **-0.648 (8/8)** | +3.7 | **+19.6 (+20.5)** |
| **`p-rs3s+c3`** | the third worker takes only packs whose needed blobs are all <= 1 MiB; 1 MiB ranges; outside the range-buffer pool (`filerestorer.go`, `repository.go`, `restic/cm7_small.go`) | **-0.42 (8/8)** | +2.4 | **+1.6** |
| `p-md64` | `restoreMetadataPendingMax` 8 -> 64 (= S016 T1 M001) | **-0.305 (8/8)** | +1.7 | -0.4 (+1.0) |
| `p-md256` | 8 -> 256 | **-0.305 (8/8)** | +1.7 | -3.2 (-4.2) |
| `p-md1024` | 8 -> 1024 | -0.313 (8/8) | +1.8 | -0.7 (-1.3) |
| **`p-pf1b`** | first pass reads trees from a BOUNDED prefetcher: depth-first order predicted with a stack, 3 loaders, at most 64 decoded trees waiting, entries dropped when taken (`cm7_pf.go`) | **-0.154 (8/8)** | +0.9 | -2.3 (-1.3) |
| **`p-rs2b`** | `p-md256` + `p-pf1b` | **-0.474 (8/8)** | +2.7 | -0.2 (-2.5) |
| **`p-rsall+c3`** | `p-rs3s` + `p-md256` + `p-pf1b`, 3 connections | **-0.88 (8/8)** | +5.0 | **+2.0 to +3.0** |
| `p-rsall` | the same binary with 2 connections (no third worker) | -0.495 (8/8) | +2.8 | +2.2 (+3.3) |
| **`conn3`, check** | third read worker, unrestricted | **-0.549 (8/8)** | +3.1 | +10.3 (+11.4) |
| **`t3m001`** | S016 T3 M001's team tree (check-only slot, small-pack worker, 256 KiB reader, queues of 2) | **-0.457 (8/8)**; judge -0.451 | +2.6 | +4.3 (+4.8); judge +5.6 |
| `p-ck3b+c3` | cost-map-6's bounded check worker | -0.389 (8/8) | +2.2 | +4.4 (+6.0) |
| `conn3`, forget-prune | second repack worker | -0.115 (8/8) | +0.7 | +8.3 (+10.5) |
| `conn3`, ls | index loaders | +0.001 (3/8) | 0 | -0.8 |
| `noscan`, bi / bu / bm | `backup --no-scan=true` (bound for one traversal instead of two) | bi -0.053 (8/8), bu -0.014 (8/8), bm -0.018 (8/8) | +0.5 | -4.7 / -0.5 / -3.2 |
| `rc4`, bi | `backup --read-concurrency=4` (bound for file-reader concurrency) | -0.278 (8/8) | +1.6 | +24.9 (+27.0) |
| `conn3`, rs + ck + fp + ls | sum of the unrestricted rows | -1.311 | +7.5 | +37.5 |

Notes.
- **The window saturates at 64.** With 64 pending tasks the walker almost never finds the window full while 4
  metadata workers drain it. T4's 32 keeps about two thirds on the judge (-0.164 vs -0.240).
- **`p-pf1b` is bounded and cheap.** It never holds more than 64 trees and frees each on use; the peak did not move.
  The cost-map-6 version (+8-9.5 MiB) kept every tree in its map until the end of the pass. It gains less (-0.154 vs
  -0.229 s) because it looks only one level ahead; a deeper lookahead inside the same cap is open.
- **Pass order and errors.** `p-pf1b` returns a load error when the walk asks for that tree, as before. The stock
  restorer tests pass with it, with the window at 256/1024, and with the small-pack worker.
- **`t3m001` validates the lane**: -0.457 s here, -0.451 s on the judge; memory +4.3-4.8 here, +5.6 there.
- **`rs3s` notes**: the bounded worker keeps 65% of the unrestricted worker's time (-0.421 of -0.648 s) for 8% of its memory (+1.6 of +19.6 MiB). The dispatcher offers each small pack to the small worker and the ordinary workers in one `select`, so a large pack at the head never starves it. Stock `internal/restorer` and `internal/repository` tests pass; restore-manifest parity was not run on the prototype (exit code 0 in all runs). The prototype takes its third slot from `-o local.connections=3`; a candidate needs a restore slot built like T3's check slot.

## 8. Remaining speed levers, ranked (judge seconds; 1 s = 5.71 points; guard 11.5 MiB summed)

"(m)" = measured clock-held in section 7 or by an S016 judged row. Other ceilings are a measured share times a
plausible reduction.

| # | lever | mechanism and files | measured share (judge) | ceiling s (judge) | points | memory |
|---|---|---|---|---|---|---|
| 1 | **Bounded third restore worker** | a backend slot for restore + a small-pack worker: `restorer/filerestorer.go`, `repository.streamPack`, `backend/sema` | download ~2.35 s at 2.2 cores | **-0.42 (m)**; unbounded -0.65 (m) | **+2.4** | **+1.6 (m)**; unbounded +19.6 |
| 2 | **Bounded third check worker** | S016 T3 M001 (check-only slot); `checker.go` | read-data ~1.8 s at 2.3 cores | **-0.45 (m, judge)**; unbounded -0.55 (m) | **+2.6** | +5.6 (m, judge); unbounded +10-11 |
| 3 | **Restore second-pass window** | `restoreMetadataPendingMax` 8 -> 64+ | pass 2 ~0.76 s; 1.73 s sched latency | **-0.31 (m)**; judge -0.24 at 64 | **+1.4 to +1.8** | 0 (m) |
| 4 | **First-pass tree prefetch, bounded** | `restorer.go` first traversal, `tree_prefetch.go` | pass 1 ~0.42 s at 1.1 cores | **-0.15 (m)**; with #3 **-0.47 (m)** | +0.9; +2.7 | 0 (m) |
| 5 | **Use the idle dwell** | `lock.go` wait-work hook; check structure pass, tree read-ahead for rs/ls, prune planning, backup scan and first reads | 1.05 s idle | **-0.1 per phase plausible; -1.05 hard ceiling** | +0.6 per phase | bounded buffers |
| 6 | **Decode each restore tree once** | carry pass-1 trees to pass 2 (S016 review copies) | tree decode twice per tree | -0.1 to -0.2 beyond #3/#4 | +0.6 to +1.1 | 1-2 MiB budget |
| 7 | **Second repack worker** | `repack.go:208`; small-pack bound as in #1 | prune execute ~0.83 s | **-0.12 (m)** unbounded | +0.7 | +8-10 (m) unbounded |
| 8 | **Backup: one traversal** | drop the separate scanner walk (`cmd_backup.go`, `archiver`) | scanner 0.34 CPU-s, 91k lstat per backup | -0.085 over the three backups (m, bound) | +0.5 | 0 |
| 9 | **Backup-initial pipeline** | file readers vs blob savers; nonces; pack hash | 16.7 CPU-s at 3.3 cores | -0.1 to -0.4; rc4 knob -0.278 (8/8) (m) | +0.6 to +2.3 | +24.9 (+27.0) for rc4 |
| 10 | **bu/bm snapshot** | lstat once, `llistxattr`, parent decode | ~1.43-1.53 s each at ~2 cores | -0.1 to -0.2 (both) | +0.6 to +1.1 | 0 |
| 11 | **Tree decode** | S012 T4's `data/tree_decode.go` | bu, ls, restore decode | -0.09 to -0.15 (cost-map-5) | +0.5 to +0.9 | ~+2 |
| 12 | **Buy memory** | restore file list (12.6 MiB live), large-blob range + copy (23 MiB live) | guard 11.5 MiB | 0 by itself | pays for #1, #2, #7 | -5 to -15 plausible |

**Closed / not worth it for speed (new in this map):**
- an unrestricted third restore worker (+19.6 MiB) and an unrestricted second repack worker (+8-10 MiB): bound them.
- smaller stream ranges as a memory bound (a large blob is never split): confirmed with the heap profile.
- a second-pass window above 64-256 (1024 = 256 = 64).
- a buffered check task channel (S015 T1 M003).
- `connections=3` for ls: null again.

**Stacking envelope over S015.** Restore items #3 + #4: **-0.47 s (+2.7), no memory (m)**. With the bounded restore
worker: **-0.88 s (+5.0), +2.0 to +3.0 MiB (m)**. Plus the bounded check worker: another -0.45 s (+2.6),
+5.6 MiB. Plus 0.1 s of dwell work in four phases: -0.4 s (+2.3). **Total -1.33 s (+7.6 points) measured for about +8 MiB summed; about -1.7 s (+10) with the dwell items.** If S016 promotes T3's check worker
or a window change, subtract that row.

## 9. Data, binaries, tools

- **Local** `cost-map-7/data/`: `main7/` (tables.md, results.jsonl, cpu-categories-s015.json, pprof flat/cum per
  phase, Go-trace sched/sync/syscall tops, strace heads), `mem7/` (results + `mem-summary.txt`: memstats, heap tops,
  gctrace), `x9/` (x9a and x9b results + tables), `protos/` (diffs vs s015-plain), `S015-vs-S014.diff`, `s016/` (S016
  team and review-copy diffs vs S015 at 21:35Z), `judge-s015.json`, `closed-ground.md`.
- **Box** `/opt/league/restic-costmap7/`: `bin/`, `src/`, `runs/{main7,mem7,x9a,x9b}/`, `tools/`, `diffs/`,
  `logs/` (builds, `test-restorer-*.log`).
- **Tools** `cost-map-7/tools/`: `setup7.sh`, `chain_main7.sh`, `gen_main7.py`, `gen_mem7.py`, `mem_tab.py`,
  `proto_edit7.py` (`md64 md256 md1024 pf1b rs3s ck3b`), `gen_x9.py`, `run_held.sh` (spinners on, run, spinners off),
  `chain_x9a.sh`, `chain_x9b.sh`, `rows.py` (read-only judged rows of a season), `s016_scan.py`, `s016_diffs.sh`,
  `judge_s015.py`, `box.py` (`sh`, `cm` = confined to CPUs 2-5, `py`, `put`, `up`, `get`). On the box:
  `build7.sh`, `build_proto7.sh`, `tables7.py`, `analyze7.sh`, `spin7.sh`, `launch7.sh`, `census_run.py`, `census.go`,
  `patch_census.py`, `categorize.py`.
