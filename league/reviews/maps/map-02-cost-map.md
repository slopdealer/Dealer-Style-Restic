# Restic League V2: cost map 2 (the S006 incumbent vs stock)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-09-28 on the league box, CPUs 2-5 only (CCD0; idle when checked, SMT siblings 50-53 idle). No league process was
touched; S007 ran on its own lanes throughout. Same method, harness and judge bundle (`fea879935b75...`) as the S004
map (`cost-map/COST-MAP.md`, measured on S001), re-run on today's incumbent `incumbents/S006`. Everything is measured
unless marked as an estimate. Prototypes are research grade (interleaved reps on 4 CPUs), not the judge. The owner
directive built from this map is `runtime/S008-directive.md`; raw data is in `data/`; the progress log is `PROGRESS.md`.

## 0. Headline

1. **The incumbent is 24.84 s and 745.9 MiB (sum of per-phase peaks); stock is 27.71 s and 1009.2 MiB.** This
   census reads S006 at time -10.4%, peak -26.1%, score 18.2 vs stock (the judge's 42-round figure is 18.59). The
   memory side has been harvested hard (restore 215 -> 119 MiB, check 100 -> 84); the time side much less (-2.9 s of
   27.7). **1 s saved anywhere is now worth 2.01 points; 10 MiB off one phase's peak is worth 0.67.**
2. **The read side still runs on 1 to 2 cores, and that is the largest time pool left (about 3 s).**
   - Prune repack runs ONE worker (`repack.go`: `Connections()-1`), and every kept blob is re-saved synchronously
     (compress, encrypt, verify) inside that worker: prune execute is 2.06 s at 1.35 cores.
   - Restore's file download (3.03 s at 2.2 cores) and check's read-data (1.89 s at 2.3 cores) run `Connections()=2`
     workers.
   - `local.connections=3` on those three phases: **-2.85 s, +36 MiB, score +3.21**. Connections=4: -3.69 s,
     +73 MiB, +2.53. The time is there; memory per worker is still what stops it.
   - Research prototype: prune repack with asynchronous saves (a private copy per kept blob handed to the existing
     saver pool): **prune 2.79 -> 1.49 s at flat memory, +2.56 alone** (x4, 4/4 runs clean).
3. **Restore's two tree passes are still sequential on one core: 1.31 s (first) + 1.81 s (second) at 1.1 cores.**
   A research prototype that runs the second pass's per-node metadata work on 4 workers (with a per-directory barrier
   before the directory's own metadata) cut restore by **0.84 s at +3.8 MiB (+1.46)**; the second pass went from
   1.81 s to 0.61 s. The first pass (tree loading plus per-file bookkeeping) is untouched.
4. **The fixed start-up is now 0.51 s per process, 3.6 s of the 24.84 s (14.5%).** KDF 0.20 s (4 cores, CPU-bound,
   parallel scrypt installed), **lock wait 0.20 s (idle)**, index load 0.06-0.14 s. Removing the wait (a ceiling
   bound only; the operator ruling allows overlap, never removal) measured **+2.03**. Every phase's RSS sits on a
   **60 MiB floor after the KDF** (22 MiB binary pages + the freed 32 MiB scrypt V whose pages the heap re-uses);
   ls-json's peak (57 MiB) IS that floor.
5. **The largest peak owner in all three backups is the archiver's 8 MiB read-buffer `sync.Pool`: 32-40 MB in use at
   the peak, and 2.3 GB of re-allocation in backup-initial** because the GC drains the pool (410 GCs). Most files are
   tiny (90,197 files, 1.64 GiB: mean 19 KB) but every chunk read takes an 8 MiB buffer. **A research prototype with
   bounded free lists and a 1 MiB class for small files cut the three backup peaks by 21-28 MiB each: +5.08, the
   largest single lever measured.** All prototypes together measured **+8.7**, and **+10.5** with connections=3
   (section 7).
6. **Where the time went since S001:** KDF -0.27 s x 7, ls -0.77 s (output batching), restore -0.77 s, check -0.18,
   prune -0.24 (with a -0.23 s regression from S005 to S006, reproduced here: s005 2.60 s vs s006 2.83 s), backups
   -0.65 / -0.34 / -0.19. **Where the memory went:** restore -97 MiB, backup-initial -15, backup-mutated -27, check
   -20, ls -12, backup-unchanged -9; **prune +4 (now 133.8, the joint-largest peak with backup-initial).**
7. **Knobs still trade, they don't win:** GOGC 101 -9.0; GOGC 25 +0.2 (time -8.7%, over the axis cap);
   connections=3 on every phase +2.11 (backups gain nothing, backup-mutated +10 MiB).

## 1. Method

- **Same instrument as the S004 map, re-run on today's tree.** Binaries were built with the judge's pinned toolchain
  (`toolchain/go`, go1.27.0) and flags (`-trimpath -tags "selfupdate disable_grpc_modules" -ldflags "-s -w"`,
  GOAMD64=v1, offline) from stock plus the incumbent's non-test `cmd/` and `internal/` `.go` files, as the judge's
  build input does. In `/opt/league/restic-costmap2/bin/`:
  - `stock-plain`, `s006-plain`: judge flags. `s005-plain`: the previous incumbent (prune attribution only).
  - `stock-census`, `s006-census`: tag `profile` (restic's `--cpu-profile`/`--mem-profile`) plus `internal/census`
    marks (KDF, key, lock, index load, work start/end, check and prune stages, the restorer's three passes) and a
    2 ms RSS sampler that dumps the heap profile and MemStats whenever RSS grows 4 MiB.
  - `s006-census3`: the same marks, each also reading process CPU from `/proc/self/stat`. **Correction to the S004
    map:** its per-stage CPU came from `runtime/metrics` `/cpu/classes/total:cpu-seconds`, which is GOMAXPROCS x wall
    time, so it could not show cores used per stage. Whole-process cores (rusage) in the S004 map were correct.
  - `proto-*`: measurement-only prototypes (section 7), never candidates.
- **Harness.** `tools/census_run.py` replicates the judge per phase: the same bwrap sandbox and argv, the same
  starting states (copies of pristine/S1a/Shist from the bundle, bind of its corpusA/B), the judge's `rusage-launch`
  for wall and ru_maxrss, GOMAXPROCS=4, in `systemd-run --slice=boxagents.slice -p AllowedCPUs=2-5`. One JSON line
  per run, a live status file, resumable.
- **Fidelity.** Stock plain medians (n=5) vs the S004 map's stock census: backup-initial 5.56 / 5.59, restore
  6.79 / 6.86, check 2.87 / 2.92, ls 2.56 / 2.59. S006 vs stock here scores 18.22; the judge's 42-round confirmation
  said 18.59.
- **Runs** (all 0 failures except the first repack prototype, section 7):
  - `runs/main2` (260): stock and S006 plain x5, census x3, gctrace, heap-at-peak, CPU profile x2, alloc profile;
    S006 GOGC 101/25 x2, `local.connections` 3 and 4 on restore/check/prune x3, ls to a FIFO x3, trivial commands
    x3, `strace -f -c` per phase.
  - `runs/x2` (109): prototypes and s005, 4 interleaved reps with s006-plain; connections=3 on every phase x3.
  - `runs/x3` (24): census with process-CPU marks x3 (S006 and proto-meta).
  - `runs/x4` (100): bufpool, fixed repack, and all prototypes combined, 4 interleaved reps.
- **Score arithmetic on S006** (24.84 s, 745.9 MiB): 1 s = +2.01; 10 MiB off one phase = +0.67; a per-process
  constant counts 7 times: 0.1 s per process = +1.41, 1 MiB per process = +0.47.

## 2. Phase baseline (plain, n=5 medians; `data/main2/summary/tables.md`)

| phase | stock s | S001 s | **S006 s** | S006 range | stock MiB | S001 MiB | **S006 MiB** | user+sys | cores |
|---|---|---|---|---|---|---|---|---|---|
| backup-initial | 5.561 | 6.000 | **5.352** | 5.33-5.44 | 193.1 | 152.0 | **137.4** | 13.79+4.23 | 3.37 |
| backup-unchanged | 3.410 | 3.379 | **3.041** | 2.79-3.08 | 132.6 | 107.6 | **98.5** | 4.55+1.39 | 1.95 |
| backup-mutated | 3.248 | 3.304 | **3.118** | 2.69-3.23 | 170.1 | 142.6 | **115.6** | 4.89+1.55 | 2.07 |
| restore | 6.787 | 6.801 | **6.030** | 5.95-6.22 | 214.7 | 215.8 | **119.3** | 6.41+3.61 | 1.66 |
| check-read-data | 2.871 | 2.844 | **2.665** | 2.63-2.76 | 100.3 | 104.0 | **84.3** | 5.69+0.51 | 2.33 |
| forget-prune | 3.278 | 3.071 | **2.834** | 2.48-3.08 | 130.9 | 129.7 | **133.8** | 3.56+0.79 | 1.53 |
| ls-json | 2.561 | 2.571 | **1.803** | 1.65-1.82 | 67.5 | 69.0 | **57.0** | 2.31+0.14 | 1.36 |
| **total / sum** | **27.71** | **27.97** | **24.84** | | **1009.2** | **920.7** | **745.9** | | |

Shares of S006: restore 24% of time, backup-initial 22%, then 11-12% each for the other four; peaks: prune and
backup-initial 18% each, restore 16%, backup-mutated 15.5%, backup-unchanged 13%, check 11%, ls 8%.
Null spread within a binary: time ±1-2% (backup-unchanged, backup-mutated and prune ±5-8%), peak ±3-10 MiB.

Workload facts (from the census stdout): backup-initial 90,197 files, 1,061 dirs, 1.64 GiB; backup-unchanged
re-reads **7,833 "changed" files** (82,364 unmodified) and adds 28.6 MiB; backup-mutated 10,856 new + 2,292 changed;
restore 91,264 files/dirs; check reads 76 packs; prune repacks 8,150 blobs / 317.7 MiB from 84 packs. The S1a
repository holds 77,627 blobs in 76 packs.

## 3. Boundary and stages (census marks, n=3 medians; process CPU from x3)

| phase | wall | KDF | lock wait | index load | work | work cores | RSS after KDF |
|---|---|---|---|---|---|---|---|
| backup-initial | 5.445 | 0.207 (4.0 cores) | 0.202 | 0.002 | 5.008 | 3.46 | 59.9 |
| backup-unchanged | 2.677 | 0.207 | 0.201 | 0.086 | 2.124 | 1.98 | 59.9 |
| backup-mutated | 2.845 | 0.210 | 0.201 | 0.080 | 2.269 | 2.09 | 59.7 |
| restore | 6.671 | 0.207 | 0.201 | 0.094 | 6.181 | see below | 59.5 |
| check-read-data | 2.637 | 0.208 | 0.201 | 0.112 | 2.040 | 2.31 (read-data) | 59.8 |
| forget-prune | 2.788 | 0.207 | 0.201 | 0.093 | 2.285 | plan 2.82, execute 1.35 | 60.1 |
| ls-json | 1.370 (FIFO) / 1.817 (file) | 0.209 | 0.201 | 0.098 | 0.829 (FIFO) / 1.317 (file) | 1.20 | 59.8 |

Stock for comparison: KDF 0.47-0.49 s, lock 0.20, index 0.08-0.17.

- **Per process:** KDF 0.20 s + lock 0.20 s + index ~0.09 s + spawn/init/exit ~0.03 s = **~0.51 s x 7 = 3.6 s
  (14.5%)**; the lock wait alone is 1.41 s (5.7%) and CPU-idle.
- **restore:** first tree pass **1.307 s at 1.15 cores**; file restore **3.029 s at 2.18 cores**; second (metadata)
  pass **1.808 s at 1.11 cores** (stock: 1.81 / 3.15 / 1.67; S001: 1.89 / 3.48 / 1.59).
- **check:** index 0.11, structure 0.15 s (3.75 cores), read-data **1.89 s at 2.31 cores** (76 packs, 2 workers).
- **prune:** forget+plan 0.22 s; execute (repack 318 MiB, index rewrite, pack deletion) **2.06 s at 1.35 cores**.
- **ls:** walk plus output 0.83 s to a FIFO, 1.32 s to a regular file (the judge uses a file). The 0.45-0.49 s
  difference is the regular-file output path (see A3).
- **backups:** unchanged and mutated work at ~2 cores (`--read-concurrency` 2 file readers); backup-initial is
  near CPU-bound at 3.46 cores.

**Memory floor.** `version` peaks at 23.2 MiB (binary pages). Every repository command reaches **~60 MiB at the end
of the KDF** (the 32 MiB V of 4 lanes at every-4th-V plus B/XY) and stays there: `runtime.GC()` before index load
frees V, but its pages are retained and re-used by the heap, so they only cost where a phase's own peak is lower.
That is ls-json (57.0 MiB plain) and the trivial commands; for the other six phases the floor is under the work
peak.

## 4. Where the CPU goes (S006, CPU s per run, pprof flat bucketed; `data/main2/summary/cpu-categories-*.json`)

| category | bi | bu | bm | restore | check | prune | ls | total | stock total |
|---|---|---|---|---|---|---|---|---|---|
| **CPU s per run** | 18.27 | 5.72 | 6.39 | 9.59 | 6.20 | 4.03 | 2.31 | 52.5 | 52.4 |
| syscalls | 3.75 | 1.34 | 1.59 | 3.50 | 0.34 | 0.46 | 0.09 | 11.07 | 12.30 |
| sha256 | 2.54 | 0.34 | 0.59 | 0.96 | 1.77 | 0.48 | 0.03 | 6.71 | 7.19 |
| scrypt (parallel; "other" bucket) | ~0.8 | ~0.8 | ~0.8 | ~0.8 | ~0.8 | ~0.8 | ~0.8 | ~5.6 | 3.00 |
| zstd compress | 2.90 | 0.23 | 0.26 | 0 | 0 | 0.37 | 0 | 3.75 | 3.71 |
| AES-CTR + Poly1305 | 1.21 | 0 | 0.10 | 0.55 | 0.60 | 0.23 | 0 | 2.68 | 2.80 |
| chunker | 1.49 | 0.56 | 0.58 | 0 | 0 | 0 | 0 | 2.62 | 2.54 |
| JSON encode/decode | 0.04 | 0.56 | 0.47 | 0.42 | 0.25 | 0.33 | 0.56 | 2.62 | 2.65 |
| zstd decompress | 0.62 | 0.07 | 0.04 | 0.67 | 0.65 | 0.18 | 0.04 | 2.27 | 2.35 |
| memmove + memclr + malloc | 1.24 | 0.35 | 0.33 | 0.32 | 0.38 | 0.28 | 0.14 | 3.02 | 3.61 |
| GC mark/sweep + scavenger | 0.59 | 0.04 | 0.04 | 0.66 | 0.10 | 0.18 | 0.07 | 1.67 | 1.32 |

The parallel KDF buys 0.27 s of wall per process with ~0.35 extra CPU-s (time-memory tradeoff recompute); total CPU
is unchanged from stock. **The incumbent's gains are almost all wall-time from concurrency and memory, not less work.**

**Allocation and GC.** backup-initial allocates 8.1 GB (S001: 15.2 GB) in 410 GCs: `newBufferPool` 2.3 GB (the
8 MiB pool refilled after GCs), `saveFile` 1.6 GB (exact-size copies), `saveAndEncrypt` 1.3 GB + `sliceForAppend`
1.3 GB (ciphertext and verify), zstd `encodeAll` 0.8 GB and verify `DecodeAll` 0.7 GB, `chunker.NewBase` 0.43 GB.
restore allocates 1.73 GB (DecodeAll 0.88 GB into fresh buffers per blob); check 1.02 GB (`bufReader.ReadFull`
0.35 GB per blob, `toPackedBlob` 0.15 GB); prune 1.12 GB.

**strace -f -c** (`data/main2/summary/strace/`):

| phase | notable counts |
|---|---|
| restore | 1.52 M syscalls: fcntl 371k, openat 183k (90,594 fail: open-before-create), epoll_ctl 92.7k (all fail), fstat 92.7k, llistxattr/fchownat/utimensat 91.3k each: **~16.6 per restored file** (unchanged since S001) |
| backup-initial | 2.52 M: fcntl 555k (~6 per file), futex 468k, read 404k, newfstatat 183k (2 per file), getrandom 155k, nanosleep 152k |
| backup-unchanged / mutated | 509k / 616k: newfstatat 183k / 173k, llistxattr 91k / 87k |
| ls-json | 26k: write 1,121 (batched since S004), futex 6.9k |
| check / prune | 67k / 65k |

## 5. Who owns the peak (heap at the RSS high-water mark, S006; `data/main2/summary/pprof/peakheap-*`)

| phase | RSS at peak | HeapInuse | idle | top in-use owners (sampled heap profile) |
|---|---|---|---|---|
| backup-initial | 153 | 118 | 21 | **archiver 8 MiB pool buffers 32 MB**; index `generatePackList` 5.9; JSON encode 5.2; zstd encoder state 4.6 + 4.1 + 6.0 |
| backup-unchanged | 104 | 83 | 24 | **pool buffers 32 MB**; zstd encoder 5.6; index 3.7 |
| backup-mutated | 122 | 74 | 33 | **pool buffers 40 MB**; index 6.7; zstd 4.6 + 4.1 + 1.7 |
| restore | 130 | 86 | 29 | `streamPackPartWithBuffer` 12.5 (23 cum); `DecodeAll` 10.8; **per-file restore maps: HardlinkIndex 9.4, `addFile` 7.5, `trackFile` 5.6**; index 2.6 |
| check-read-data | 89 | 52 | 19 | `bufReader.ReadFull` 10.7 (per blob); bufio 2 x 4 MiB; index 4.2; stream decoder 8.4 |
| forget-prune | 138 | 78 | 41 | `DecodeAll` 11.8; **index rewrite: bytes.growSlice 9.9, strconv 5.2, JSON 4.1 + 4.1 (13.4 cum)**; zstd encoder 4.6 + 4.1; stream buffer 4.0; generatePackList 3.8 |
| ls-json | 61 (at KDF) | 36 | 7 | the KDF V (the peak is reached in the KDF); after it, heap is 7 MB |

GOGC=50 (restic's default) puts the goal at 1.5 x live, so live bytes cost ~1.5x in RSS at the peak.

**The in-memory index (upstream #1988).** 77,627 blobs; the index structures (`indexMap` entries of 56 bytes plus
buckets and hashed-array-tree slack) measure 3.7-6.7 MB sampled at the peaks, about 65-75 bytes per blob; load takes
0.06-0.14 s per process. On this judge the index is ~5-8% of each non-backup-initial peak. **The judge's small
repository hides the #1988 problem:** at 1 M blobs the same structure is ~70 MB live (~105 MiB RSS at GOGC 50) and
at 10 M blobs ~700 MB, where it would dominate every phase. Here, a representation 30% smaller is worth about
-2 to -3 MiB on six phases (+0.8 to +1.2), and halving index load time about +0.5. It is a real product lever the
judge can barely see.

## 6. Experiments: knobs (whole-workload score vs S006)

| experiment | phases | time | RSS | score | note |
|---|---|---|---|---|---|
| GOGC 100 (env 101) | all | +6.1% | -24.1% | **-9.0** | |
| GOGC 25 | all | -8.7% | +9.2% | +0.2 | breaks the 5% time cap |
| `-o local.connections=3` | restore, check, prune | **-2.85 s** (-0.91, -0.74, -1.14) | +36 MiB | **+3.21** | repack gets 2 workers instead of 1 |
| `local.connections=4` | restore, check, prune | -3.69 s | +73 MiB | +2.53 | |
| `local.connections=3` | all phases | +11.3% | -7.1% | +2.11 | backups gain ~0, bm +10 MiB |
| ls stdout to a FIFO | ls | -0.445 s | +1.4 | (+0.8) | the regular-file batching path costs ~0.45 s |
| s005 incumbent vs s006 | bi / restore / prune | +0.02 / +0.06 / **-0.23 s** | -9 / **+26** / -2.5 MiB | | S006's prune time regression is real |

## 7. Prototypes (research grade: 4 interleaved reps with s006-plain on CPUs 2-5; never candidates)

Diffs against s006-plain are in `data/protos/`; tables in `data/runs/x2|x4/prototypes.md`. Baselines in the same
runs: 24.64 s / 747.1 MiB (x2), 24.83 s / 750.0 MiB (x4).

| prototype (edit) | what it changes | phase effect | whole workload | score |
|---|---|---|---|---|
| `proto-bufpool` (x4) | `archiver/buffer.go`: bounded free lists (2 per class) instead of a `sync.Pool`, plus a 1 MiB class for files < 1 MiB (`file_saver.go` asks for a buffer by file size) | bi -0.23 s **-21.2 MiB**; bu **-27.6**; bm **-25.9** | time +0.2%, RSS **+9.95%** | **+5.08** |
| `proto-repack` (x4) | `repack.go`: each kept blob is copied and handed to `SaveBlobAsync` (the saver pool, bounded by the uploader's errgroup limit) instead of a synchronous `SaveBlob` in the single repack worker | prune 2.79 -> **1.49 s**, +0.7 MiB | time +5.2% | **+2.56** |
| `proto-lock0` (x2) | lock wait set to 0 (**ceiling bound only**; removal is not allowed) | -0.13 to -0.26 s in every phase | time +6.1% | +2.03 |
| `proto-meta` (x2, x3) | restore second pass: per-node metadata/special-file jobs on 4 workers, per-directory barrier before `leaveDir` | restore -0.84 s (second pass 1.81 -> 0.61 s at 2.67 cores), +3.8 MiB | time +3.4% | **+1.46** |
| `proto-meta` + connections=4 (x2) | | restore -2.15 s, +34 MiB | | +2.08 |
| `proto-ls` (x2) | 256 KiB bufio under the ls JSON encoder | ls +0.15 s (no gain) | | -0.33 |
| **`proto-all`** (x4) | bufpool + repack + meta + ls | bi -0.21 s -22 MiB; bu -27; bm -25; restore -0.86 s; prune -1.28 s | time +8.1%, RSS +9.3% | **+8.68** |
| **`proto-all` + connections=3** (x4) | | + restore -1.80 s (+20 MiB), check -0.81 s (+15), prune -1.41 s (+6) | time +16.7%, RSS +4.4% | **+10.52** |

Notes.
- The first `proto-repack` (x2) exited 130 ("context canceled") in 3 of 4 runs: the async saves were bound to the
  repack errgroup's context, which is cancelled when `repack` returns. Bound to the uploader's context (x4) it ran
  4/4. A real change must also surface late save errors before `repack` returns success.
- `proto-ls` shows the ls cost is not the encoder: the termstatus line writer still splits and enqueues every line.
  The measured ls cost is the regular-file batching path (0.45 s, section 6).
- These are ceilings on real restic binaries at 4 reps; the oracle, the compat tests and judge noise still decide.
  `proto-meta` and `proto-repack` report errors late (research grade); `proto-bufpool` is behaviour-neutral.

## 8. Structural read: menu, ceilings, soundness

Ceiling = measured share times a plausible reduction, scored with section 1's arithmetic. **SOUND** = keeps format
v2, stock interop, oracle-identical outputs and every frozen API. **RULING** = sound for the oracle but a product
default the owner must argue. Measured prototype rows are marked (m).

| # | area (files) | measured share on S006 | lever | ceiling | soundness |
|---|---|---|---|---|---|
| 1 | **Backup read buffers** (`archiver/buffer.go`, `file_saver.go`) | pool buffers 32-40 MB in use at all three backup peaks; 2.3 GB re-allocated in bi | bounded free lists sized to ReadConcurrency; small-file class; then chunker reuse (0.43 GB `NewBase`) and fewer copies | **+5.1 (m)**, +6 with follow-ons | SOUND |
| 2 | **Prune repack pipeline** (`repack.go`, `prune.go`) | execute 2.06 s at 1.35 cores; one worker; synchronous save; S006 hash-on-write moved into this serial path (-0.23 s vs S005) | async saves with bounded copies, or 2+ workers; surface late errors | **+2.6 (m)** | SOUND |
| 3 | **Read-side workers** (`filerestorer.go`, `checker.go`, `repack.go`, `backend/local` Connections) | restore download 3.03 s at 2.2 cores; check read-data 1.89 s at 2.3 cores | connections=3: -2.85 s, +36 MiB (+3.2 m); bound per-worker memory (stream range 4 MiB + pool, `DecodeAll` 10.8 MB, check `bufReader.ReadFull` per blob 10.7 MB, bufio 2 x 4 MiB) so the extra worker costs ≤ +10 MiB | **+3.2 (m) to +5** | the worker count for the local backend is a RULING (product argument: 2 was chosen for HDD seeks); per-worker memory work is SOUND |
| 4 | **Restore tree passes** (`restorer.go`, `data/tree.go`, `walker`) | first pass 1.31 s at 1.15 cores; second pass 1.81 s at 1.11 cores; ~16.6 syscalls per file | parallel metadata pass (+1.46 m); tree prefetch/decode ahead of both passes and the ls walk; one open per file | **+1.5 (m) to +3** | SOUND if the restored manifest (incl. directory mtimes) matches stock; errors must stay in order and complete |
| 5 | **Lock wait overlap** (`lock.go`, `lock_file.go`, `cmd/restic/lock.go`) | 0.20 s idle x 7 = 1.41 s | run read-only work (index load 0.06-0.14 s, snapshot load, prune planning 0.22 s, backup scan/chunk/hash) during the wait; gate every repository write, every output line and every restore-target write on the confirmed lock | bound +2.0 (m, removal); realistic **+1.0 to +1.7** | OPERATOR RULING: overlap only, never removed or skipped; `TestSetLockTimeout` frozen |
| 6 | **ls output path** (`ui/termstatus/status.go`, `stdio_wrapper.go`, `cmd_ls.go`) | 0.45 s: file 1.80 s vs FIFO 1.36 s | the S004 batching path costs more per line than a direct write (per-line enqueue and batch-timer reset); batch without per-line timer work | **+0.9** | SOUND: same bytes, same order vs stderr |
| 7 | **Prune peak / index rewrite** (`index/index.go` encode, `prune.go`) | prune 133.8 MiB (joint largest); index JSON rewrite ~13-25 MB transient, `DecodeAll` 11.8 | stream the index encode; release repack buffers before the rewrite | +1.0 to +1.7 | SOUND |
| 8 | **Restore memory** (`restorer.go`, `filerestorer.go`, `internal/data` HardlinkIndex) | per-file maps 22 MB at the peak (HardlinkIndex 9.4, addFile 7.5, trackFile 5.6) | compact per-file state (IDs instead of path strings, one map instead of three) | +0.7 to +1.0 | SOUND; the S002 replay loss shows storing MORE per-file state is closed |
| 9 | **backup-initial CPU** (`archiver`, `repository.saveAndEncrypt`, `fs`) | 17.4 CPU-s at 3.46 cores: syscalls 3.75 (6 fcntl per file from poller registration, 2 stats per file, 155k getrandom), zstd 2.9, sha256 2.5, chunker 1.5, AES 1.2, alloc/GC 1.8 | each CPU-s removed ≈ 0.29 s wall | +1 to +1.5 | SOUND (verify stays) |
| 10 | **Index representation** (#1988) (`index/indexmap.go`, `index.go`, `master_index.go`) | 77,627 blobs, ~65-75 B/blob, 0.06-0.14 s load | denser entries/sorted arrays; faster decode | +0.8 to +1.2 (m side) + ~0.5 (time) | SOUND; mostly invisible to this judge (section 5) |
| 11 | **Check memory** (`checker.go`, `repository.go` bufReader) | check 84 MiB: `ReadFull` per blob 10.7 MB, bufio 8 MB | reuse per-worker blob scratch; smaller read-ahead only with more workers | +0.7 to +1.0 | SOUND |

**Not worth it / out of reach.** KDF: 0.20 s at 4 cores is CPU-bound and memory-hard; every lower-memory variant
costs more time than the ls floor it saves (every-8th-V: +0.1 s x 7 = -1.4 vs ls -12 MiB = +0.8). Binary pages
(~22 MiB). GOGC in either direction. `--no-cache`. More file readers in backups without bounded buffers (S001 map:
read-concurrency 4 was -1.4).

**Stacking.** `proto-all` (items 1, 2, 4-meta) measured +8.7 and +10.5 with connections=3; items 5, 6, 7, 8, 9, 10
and 11 are not in it. A realistic S008 envelope is +10 to +15 points over S006 if the teams find the SOUND versions.

## 9. Closed ground

`data/closed-ground.md` (about 60 rows, S001 to S007 M001, from diffs of every official tree against its season's
incumbent). Highlights:
- **Correction to the S006 review:** the promoted T3 M002 tree contains no restorer change. It is read-side buffer
  retention (a 2-buffer cross-stream pool, pack parts at 1/4 pack) plus hash-on-write for packs. Its 42-round row:
  restore t+2.37 m+17.5, **prune t-8.9 m-6.0**. This map reproduces the prune loss (s005 2.60 s vs s006 2.83 s) and
  attributes it to the one-worker repack pipeline: hashing moved from the uploader goroutine into the serial
  write path.
- Closed: hash-on-write alone (5 tries, -0.04 to -0.96); backup save-path buffer leases/EncodeAll pools (2 failures,
  one over the memory cap); prune read-ahead (3 tries); per-worker range windows for repack; restore
  micro-changes (filepath.Join replacement, skipping MkdirAll, no-poller opens, bigger dir caches: flat or
  inverted); known-blob inline completion; atomic reserve-before-copy.
- Open leads: connections 2 -> 3 (S006 T2 one-round probe +2.92, never submitted; measured here +3.21 on the read
  phases); S007 T3's in-flight known-blob check (+2.08, 4/6).
- Never built: lock-wait overlap, parallel metadata pass, tree prefetch, repack concurrency, bounded backup read
  buffers, index representation, chunker reuse, stat-once.

## 10. Data and binaries

- **Local** `cost-map-2/data/`: `main2/summary/` (tables.md, summary.json/txt, pprof CPU/alloc/heap-at-peak and
  MemStats for stock and S006 per phase, cpu-categories, strace), `x2-x4/` (results.jsonl), `closed-ground.md`,
  `s006-vs-stock.diff`.
- **Box** `/opt/league/restic-costmap2/`: `bin/` (all binaries), `runs/{main2,x2,x3,x4}/results.jsonl` and
  `artifacts/<run-id>/` (raw cpu.pprof, mem.pprof, peak-heap.pb.gz, marks.json, gctrace stderr, strace),
  `src/` (census and prototype trees), `tools/` (= `cost-map-2/tools/`).
- **Tools** `cost-map-2/tools/`: `build.sh`, `census.go` + `patch_census.py`, `census_run.py` (plans `main2`,
  `extra:<json>`), `analyze.py`, `categorize.py`, `tables2.py`, `proto_edit2.py` (edits `lock`, `ls`, `meta`,
  `repack`, `bufpool`), `build_proto2.sh`, `make_x2.py`, `run_x3.sh`, `run_x4.sh`.
