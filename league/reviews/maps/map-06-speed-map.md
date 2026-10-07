# Restic League V2: speed map (cost map 6, the S014 incumbent)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-10-02 12:00-13:00Z on the league box while S015 ran. **Only CPUs 2-5 were used** (systemd slice
`costmap6.slice`, `AllowedCPUs=2-5` plus `taskset 2-5` on every build, run and analysis). The judge lanes and S015's
research lanes were not touched, no governor was changed, and S015's workspace was read only. The instrument is
cost-map-5's (judge sandbox, argv, starting states, `rusage-launch`, GOMAXPROCS=4) on one lane. Raw data is in `data/`,
the log in `PROGRESS.md`, closed ground in `data/closed-ground.md`. The directive built from this map is
`runtime/S016-directive.md`.

**Two clocks, read this first.** CPUs 2-5 run `schedutil`; the judge and research lanes run `performance`. The same
S014 tree takes **21.97 s here (n=10) and 17.71 s on the judge** (42 rounds, job `c90c07ce...`). Per phase
(here / judge): **bi 1.02, bu 1.54, bm 1.54, rs 1.22, ck 1.15, fp 1.11, ls 1.77**. cost-map-5 converted a lever by
dividing its schedutil delta by the phase ratio. **That overstates any lever that adds busy cores**: the extra worker
also clocks the CPUs up here (a `connections=3` check uses 16% fewer CPU-seconds for the same bytes), and the judge has
no such effect. So this map measures the worker-count levers a second way, **clock-held** (run `x8`): four
idle-priority (`SCHED_IDLE`) spinners pinned to CPUs 2-5 keep schedutil at the top clock while the measured process
preempts them at once. With them, plain S014 reads within a few percent of the judge on every phase (section 2), so the
`x8` deltas are judge seconds. This is an instrument trick on the research side only. It stays banned in candidates.

## 0. Headline

1. **S014 on the judge: 17.71 s, 586.9 MiB summed peaks** (stock 22.42 s; S013 17.96 s). **1 s = 5.65 points; 0.1 s
   per process x7 = 3.95 points.** The memory guard (+2%) is **11.7 MiB summed over the 7 phase peaks**.
2. **What S014 removed (judge, 42 rounds, vs S013): -0.246 s, all in restore.** A decoded-tree prefetch feeds the
   second restore pass while the files download. Pass 1, the download and every other phase are unchanged.
3. **The largest remaining time** (judge seconds): bi snapshot ~4.7 (CPU-bound, 3.5 cores); restore download ~2.35
   at 2.2 cores, second pass ~0.63, first pass ~0.58 at 1.2 cores; check read-data ~1.7 at 2.3 cores; bu/bm snapshot
   ~1.45-1.5 each; KDF 1.4 and dwell 1.4 over the 7 processes (the dwell is idle in rs/ck/fp/ls on S014).
4. **Prototypes, clock-held (judge seconds, 8 interleaved reps, every row 8/8):**
   - **Restore second-pass handoff window 8 -> 256 (`p-md256`, one constant): -0.325 s (+1.8 points), +1 MiB.**
     New in this map. The trace shows 1.47 s of scheduling latency under `restoreMetadataPool.schedule`.
   - **Restore first-pass tree prefetch (`p-pf1`, bound): -0.229 s (+1.3), +8-9.5 MiB unbounded.** With `p-md256`
     (`p-rs2`): **-0.515 s (+2.9), +6 MiB.**
   - **Third read worker, check: -0.545 s (+3.1), +10.4-11.5 MiB.** Bounded variant (`p-ck3b`, the third worker takes
     only packs whose blobs are all <= 1 MiB, 256 KiB reader): **-0.396 s (+2.2), +5.8-6.5 MiB.**
   - **Third read worker, restore: -0.662 s (+3.7), +20-25 MiB.** Shrinking the stream ranges to 1/6 pack with 3 pooled
     buffers (`p-rs3b`) did NOT reduce the memory (+25): the owner is elsewhere. Stack with the first-pass prefetch
     (`p-rstack+c3`): -0.943 s (+5.3), +22 MiB.
   - Second repack worker in prune (same knob): -0.113 s (+0.6), +8-11 MiB. Backups and ls: null.
   - All phases at `connections=3`: **-1.32 s (+7.5 points) for +47 MiB, four times the guard.** Memory is the price
     of everything in this family.
5. **Surprises.**
   - **cost-map-5's worker-count ceilings were about 1.5x too high.** Schedutil read check -0.87 s and restore
     -0.99 s for the third worker, and the first-pass prefetch -0.52 s; at the judge's clock they are -0.55, -0.66
     and -0.23. Levers that add busy cores also clock CPUs 2-5 up. The S015 directive's "-0.68 s judge" for check was
     that overstatement; the measured number is -0.55.
   - **Nobody in S015 built the third worker**, and the one probe of it (T2) could not have worked: the backend
     semaphore still admitted 2 (section 6).
   - S015's dwell rows confirm cost-map-5's `p-dwell` on the judge: T4 M001 -0.21 s (+1.38, 6/6).
   - `p-falloc` (no fallocate for small files) is null on S014 (-0.057 s here, 5/10).

## 1. Method

- Binaries in `/opt/league/restic-costmap6/bin/`: `s014-plain` and `s014-census` built here with the judge
  toolchain go1.27.0 and judge flags (`tools/build6.sh`). stock and S013 binaries are cost-map-5's. Prototypes are
  `p-*` (`tools/build_proto6.sh`, edits in `tools/proto_edit6.py`).
- `main6` (212 runs, 0 failures): plain S014 and S013 x10 plus stock x4, interleaved and rotated per rep; S014 census
  x2 (stage marks with process CPU); CPU profile, Go execution trace, `strace -c`; trivial commands x3. Schedutil.
- `x6` (210 runs, schedutil): `connections=3` on all 7 phases and the prototypes vs `s014-plain`, 10 interleaved reps,
  on the phases each one touches.
- `x8` (124 runs, **clock-held**): the same variants again with the spinners on, 8 interleaved reps, plus plain S014 on
  the three backup phases (4 reps) as the validation against the judge.
- Noise here (schedutil, S014 per-phase range over 10): bi 5.14-5.26, bu 2.69-2.94, bm 2.89-3.14, rs 4.62-5.19,
  ck 2.49-2.77, fp 1.55-1.73, ls 1.62-1.74 s. On the judge the 42-round S014 ranges were bi 5.06-5.21, bu 1.85-1.87,
  bm 1.92-1.98, rs 4.02-4.07, ck 2.32-2.40, fp 1.44-1.50, ls 0.95-0.97.

## 2. Phase baseline

| phase | stock (judge) | S013 (judge) | **S014 (judge)** | S014 MiB (judge) | S014 here | here/judge | S014 clock-held (x8) | cores here | judge share |
|---|---|---|---|---|---|---|---|---|---|
| backup-initial | 5.486 | 5.104 | **5.102** | 98.8 | 5.196 | 1.02 | 5.433 (n4) | 3.36 | 28.8% |
| backup-unchanged | 2.217 | 1.852 | **1.853** | 64.3 | 2.853 | 1.54 | 1.894 (n4) | 1.93 | 10.5% |
| backup-mutated | 2.341 | 1.941 | **1.941** | 83.3 | 2.987 | 1.54 | 1.999 (n4) | 2.07 | 11.0% |
| restore | 6.075 | 4.285 | **4.039** | 110.0 | 4.922 | 1.22 | 4.258 | 2.01 | 22.8% |
| check-read-data | 2.538 | 2.359 | **2.354** | 76.0 | 2.701 | 1.15 | 2.387 | 2.27 | 13.3% |
| forget-prune | 2.294 | 1.454 | **1.456** | 96.9 | 1.622 | 1.11 | 1.461 | 2.18 | 8.2% |
| ls-json | 1.472 | 0.958 | **0.956** | 57.5 | 1.689 | 1.77 | 0.970 | 1.39 | 5.4% |
| **total** | **22.42** | **17.95** | **17.70** | **586.9** | **21.97** | 1.24 | **18.40** | | |

The judge columns are the S014 42-round confirmation (job `c90c07ce3f12428580abfbf0481409c2`): S014 vs S013 time
+1.35% (42/42), memory +0.20%; vs stock +21.02%. S015's incumbent arms (S014 tree, 6 rounds each) read 17.67-18.03 s.
Here, S014 vs S013 paired is +1.78% (n=10, -0.32..+2.95): restore -0.346 s, every other phase within ±0.02 s.

**Score arithmetic on S014 (judge): 1 s = 5.65 points; 0.1 s per process x7 = 3.95 points; the +2% memory guard is
11.7 MiB summed over the 7 phase peaks** (1.68 MiB on the mean).

## 3. Where the time goes (S014 census, n=2, schedutil; judge estimates from the per-phase judge totals)

| element | here s | cores | CPU-s | judge s (est.) | what it is |
|---|---|---|---|---|---|
| spawn, runtime init, exit | 0.02 x 7 | - | - | 0.14 | fixed |
| **KDF** (scrypt, 4 cores) | 0.20 x 7 = **1.42** | 3.97 | 0.81 each | ~1.4 | repository-defined. Closed |
| **lock dwell** (200 ms) | 0.20 x 7 = **1.40** | 0.03 | 0 | **1.40** | bu/bm: index load inside it (0.056-0.060 s). bi: nothing to load. **rs, ck, fp, ls: still idle in S014** (S015 is filling it) |
| index load after the dwell | rs .087, ck .128, fp .061, ls .112 = **0.39** | 1.5-1.8 | 0.11-0.21 | **0.21-0.24 (S015 judged rows)** | on the critical path in 4 phases |
| bi snapshot | **4.83** | 3.47 | 16.7 | ~4.7 | CPU-bound |
| bu / bm snapshot | 2.39 / 2.37 | 1.90 / 2.09 | 4.5 / 5.0 | ~1.44 / ~1.53 | lstat x2 per file, llistxattr, parent-tree decode, serial dir walk |
| **restore first pass** | **0.75** | 1.19 | 0.90 | ~0.58 | serial: tree load + JSON decode, mkdir, file list. S014 did not touch it |
| restore download (2 workers) | **3.04** | 2.16 | 6.56 | ~2.35 | per-file create/fallocate/pwrite; sha256, zstd |
| **restore second pass** (metadata) | **0.82** (S013: 1.08) | 2.27 | 1.85 | ~0.63 | S014's prefetch decodes its trees during the download; the walker now waits on the metadata pool |
| check structure / read-data | 0.15 / **1.90** | 3.3 / 2.30 | 0.51 / 4.38 | ~0.13 / ~1.7 | 2 read workers |
| prune plan / execute | 0.21 / **0.89** | 2.6 / 2.24 | 0.54 / 1.99 | ~0.19 / ~0.80 | repack runs ONE worker (`Connections()-1`) |
| ls walk + output | **1.14** | 1.22 | 1.39 | ~0.48 | serial tree load, decode, encode |

The memory floor is unchanged: `cat config` 56 MiB and 0.43 s (KDF plus the read-only dwell), `version` 22 MiB.

**Waiting (Go execution traces, S014; summed over goroutines):**
- restore: 1.94 s of scheduling latency, **1.47 s of it under `restoreMetadataPool.schedule`** (the second-pass
  walker hands each file to a 4-worker pool through a window of 8 and sleeps on the oldest task). Blocking syscalls:
  fallocate 0.83, openat 0.77, fchownat 0.34, pwrite 0.33, fchmodat 0.27, utimensat 0.27, llistxattr 0.24, read 0.20 s.
- bu: fstatat 0.64 s, llistxattr 0.35 s blocking; 0.41 s `selectgo` + 0.20 s GC-assist yields.
- check: 0.60 s of `chansend1` latency (the producer handing packs to 2 busy workers), 0.25 s in read.
- ls: 0.20 s `goschedIfBusy` (GC assist), 0.03 s in write.

**Syscalls (`strace -c`, S014):**
- restore: openat 93k, fallocate 89k, fchownat/fchmodat/utimensat/llistxattr 91k each, pwrite 90k, close 93k,
  fstat 93k, fcntl 100k, futex 73k.
- bi: futex 217k, read 401k, newfstatat 183k (2 per file), getrandom 155k, fcntl 104k, write 84k.
- check: read 2.2k, futex 31k.

## 4. CPU by category (S014, CPU-s per run, one profile; `data/main6/summary/cpu-categories-s014.json`)

| phase | CPU-s | top categories |
|---|---|---|
| backup-initial | 17.7 | zstd compress 3.2, syscalls 3.1, sha256 2.5, chunker 1.6, AES 1.2, zstd decompress (save verify) 0.65, memmove 0.61, GC 0.52, memclr 0.43 |
| restore | 9.9 | syscalls 3.5, sha256 1.07, **tree JSON decode 0.84 (every tree is decoded twice: pass 1 and the prefetch)**, zstd 0.74, GC 0.56, AES 0.53 |
| check-read-data | 6.2 | sha256 1.98, zstd decompress 0.72, AES 0.65, syscalls 0.34, memmove 0.24, KDF 0.8 |
| bu / bm | 5.5 / 6.1 | syscalls 1.46 / 1.56, chunker 0.58, JSON 0.48 / 0.29, sha256 0.34 / 0.64, KDF 0.8 |
| forget-prune | 3.5 | syscalls 0.35, zstd 0.30+0.16, JSON 0.29, sha256 0.27, AES 0.24, KDF 0.8 |
| ls-json | 2.2 | KDF 0.8, tree JSON decode/encode 0.38 |

## 5. What S014 removed, and what is left of cost-map-5's menu

**S014 (T4 M003) removed 0.246 s on the judge, all of it in restore (-5.8%).** `tree_prefetch.go` records the tree IDs
during pass 1, then one goroutine loads and decodes them in order (queue of 1, 256 KiB per tree) while the files
download; pass 2 takes prepared node lists. Here the second pass fell 1.08 -> 0.82 s. Pass 1 and the download are
unchanged. Every other phase is within ±0.005 s of S013.

| cost-map-5 item | S014 / S015 status | what is left on S014 (judge) |
|---|---|---|
| 1 third read worker | **not built by anyone.** S015 T2 probed a third check worker without raising the backend limit: it never ran 3 readers (section 6) | section 7: the largest lever on the map |
| 2 dwell overlap rs/ls/fp/ck | not in S014. **S015: T4 M001 -0.21 s (+1.38, 6/6), T1 M002 -0.15 s, T3 M001 rs -0.07** | 0.21-0.24 s when it lands; then ~0.1 s idle per phase for prune planning and first tree reads |
| 3 restore first pass | **S014 did the second pass instead.** Pass 1 untouched | section 7 `p-pf1` |
| 4 restore per-file syscalls | not built | `p-falloc` re-measured: null |
| 5 backup-initial CPU | not built | unchanged from cost-map-5 |
| 6 bu/bm snapshot | not built (S014 T2/T3 parent lookup in the dwell: null) | unchanged |
| 7 tree decode (`p-t4dec`) | not built | unchanged: ~-0.09 s (bu, ls); restore now decodes every tree twice |
| 8 check pipeline | S015 T1 M003 in flight (task channel buffered by 1) | the 0.60 s `chansend1` latency is the producer waiting for a free worker: a third worker removes it, a buffer does not |
| 9 ls walk | S014 T1 pull walk null, S014 T3 read-ahead null | closed as built; only decode cost and the dwell remain |
| 10 backup pool time | not built | unchanged |
| 11 prune | S014 T2 prune dwell -0.06; S015 T4 same | repack worker count (section 7) |
| 12 restore download workers | not built | section 7 |

## 6. Why S015's third-worker probe read null

S015 T2 (seat B14) added a third worker goroutine in `checker.go` `ReadPacks` and probed it once on a research lane:
check -0.063 s, +14 MiB. **That probe did not measure a third reader.** Every backend `Load` goes through
`internal/backend/sema`, whose semaphore is sized by the backend's `Connections` (local: 2,
`internal/backend/local/config.go`). `checkPackInner` holds one token for the whole pack. With the limit at 2, a third
check worker only queues for a token. The same holds for restore (`newFileRestorer` workers) and repack. A real third
reader needs the local limit raised to 3 (operator ruling 2026-10-02: allowed), and then each consumer's worker count
chosen on purpose: `checker.go:291`, `restorer.go:684`, `repack.go:208` (`Connections()-1`), the packer uploader
(`repository.go:966`), the index loaders (`index_parallel.go:19`, `master_index.go:596`), `tree_stream.go:204`.
`-o local.connections=3` in this map is that change as a knob: it raises all of them at once.

**Gate trap (verified against the stock tree).** `internal/backend/local/config_test.go` expects `ParseConfig` to return
`Connections: 2`, and `backend/local` is in the judge's compatibility subset. A candidate that changes `NewConfig`'s
default fails the gate. The effective limit has to be raised downstream (what `Properties()` reports, or how the
semaphore is sized), with an explicit `-o local.connections=N` still honoured.

## 7. Prototypes

Diffs vs `s014-plain` are in `data/protos/`. `conn3` is the unmodified S014 binary with `-o local.connections=3`
(a knob used as a bound, not a candidate); `+c3` means the prototype was run with the same knob. Tables:
`data/x6/table.md` (schedutil, 10 reps) and `data/x8/table.md` (clock-held, 8 reps). "judge s" is the clock-held paired
median; memory is the clock-held peak delta (median; paired median in brackets). 1 s = 5.65 points.

| prototype | edit (on s014-plain) | schedutil s (wins/n) | **judge s (clock-held, wins/8)** | points | memory MiB |
|---|---|---|---|---|---|
| **`conn3`, check** | third read worker in `ReadPacks` | ck -0.873 (9/9) | **-0.545 (8/8)** | **+3.1** | +11.5 (+10.4) |
| **`p-ck3b+c3`** | with 3 connections the extra check worker is bounded: 256 KiB reader, only packs whose blobs are all <= 1 MiB (`checker.go`, `cm6_small.go`) | ck -0.681 (9/9) | **-0.396 (8/8)** | +2.2 | +6.5 (+5.8) |
| **`conn3`, restore** | third download worker | rs -0.986 (9/9) | **-0.662 (8/8)** | **+3.7** | +25.3 (+20.5) |
| `p-rs3b+c3` | `streamPack` ranges 1/6 pack, 3 pooled range buffers, pooled `DecodeAll` destinations; 3 workers | rs -0.985 (9/10) | -0.723 (8/8) | +4.1 | **+25.8 (+25.1): the bound failed** |
| `p-rs3b` | the same with 2 workers | rs -0.104 (8/10) | not run | | +5.0 (schedutil) |
| **`p-md256`** | restore second pass: `restoreMetadataPendingMax` 8 -> 256 (`restorer.go`, one constant) | not run | **-0.325 (8/8)** | **+1.8** | +1.7 (+1.1) |
| **`p-pf1`** (bound) | restore first pass reads trees from a parallel prefetcher (3 workers, breadth-first from the root, unbounded; `cm6_pf.go`) | rs -0.519 (9/10) | **-0.229 (8/8)** | +1.3 | +9.5 (+7.7) |
| **`p-rs2`** | `p-md256` + `p-pf1` | not run | **-0.515 (8/8)** | **+2.9** | +6.3 (+6.2) |
| `p-rstack+c3` | `p-rs3b` + `p-pf1` + no fallocate <= 4 MiB, 3 workers (no `md256`) | rs -1.541 (9/9) | **-0.943 (8/8)** | +5.3 | +22.7 (+21.6) |
| `conn3`, forget-prune | second repack worker (`Connections()-1`) | fp -0.161 (9/9) | **-0.113 (8/8)** | +0.6 | +10.7 (+7.8) |
| `conn3`, bi / bu / bm / ls | third pack uploader, index loaders | -0.006 / -0.031 / +0.006 / -0.029 | ls -0.004 | 0 | ~0 |
| `p-falloc` | restore: no `fallocate` for files <= 4 MiB | rs -0.057 (5/10) | not run | 0 | ~0 |
| **`conn3`, all phases** | sum of the rows above | -2.08 | **-1.32** | **+7.5** | **+47** |

**Clock-held validation.** With the spinners on, plain S014 read bu 1.894, bm 1.999, rs 4.258, ck 2.387, fp 1.461,
ls 0.970 and bi 5.433 s, against the judge's 1.853, 1.941, 4.039, 2.354, 1.456, 0.956 and 5.102: +0.3% to +5.4%, and
+6.5% on bi (which wants all four cores, so the idle-priority spinners cost it a little). Deltas measured this way are
judge seconds to within that margin. Research lanes run `performance`, so teams measure this directly.

Notes.
- **`p-md256` is the cheapest win on the map.** The second pass walker schedules one task per file into a 4-worker
  pool and, with 8 pending, sleeps until the oldest finishes: about 91k sleep/wake pairs. A window of 256 lets it keep
  feeding while workers finish in any order; warnings and errors are still reported in snapshot order by
  `waitOldest`. Retained state is 256 small task records. A real candidate should check the error-settlement tests
  (S009's ordered settlement is a locked change) and may batch per directory instead.
- **`p-pf1` is a bound, not a design.** It holds every decoded tree until the walk consumes it (+8-9.5 MiB). A
  candidate needs a credit budget like S014's 256 KiB cap, and must keep pass 1's error order. Decoding each tree
  once for both passes is the next step (restore spends 0.84 CPU-s on tree JSON, twice per tree), but holding all
  trees across the download costs memory.
- **The third restore worker's memory is not the stream range buffer.** `p-rs3b+c3` cut the range to 1/6 pack and
  pooled three, and the peak rose by the same +25 MiB. Find the owner first (heap profile at the RSS peak with 3
  workers: `census.go` marks, `--mem-profile`): candidates are the files-writer buckets, per-part `DecodeAll` output,
  and GC pacing (three workers allocate faster than the collector finishes a cycle).
- **Check's third worker fits the guard alone** (+10.4-11.5 of 11.7), with nothing left over. The bounded variant keeps
  73% of the time for 55% of the memory. A candidate can do better: the producer blocks on the unbuffered `ch` when a
  large pack is at the head, which starves the small worker (`p-ck3b` uses a plain `select`).
- **Memory can be bought.** The guard is on the mean of the 7 phase peaks, so memory saved in any phase pays for a
  worker in another. The memory-only shelf in `data/closed-ground.md` (drains after repack, per-parent restore status
  vectors, sorted-slice file lists, prune loader caps: each read 4-8% of its phase peak, roughly 4-9 MiB) scored zero
  under the time score and is now currency: ~15-25 MiB of headroom if they still apply, enough for a third restore
  worker. None was re-measured on S014.

## 8. Remaining speed levers, ranked (judge seconds; 1 s = 5.65 points; guard 11.7 MiB summed)

"(m)" = measured clock-held in section 7 or by an S015 judged row. Other ceilings are a measured share times a
plausible reduction.

| # | lever | mechanism and files | measured share (judge) | ceiling s (judge) | points | memory |
|---|---|---|---|---|---|---|
| 1 | **Third read worker, check** | local `Connections` 3 (`backend/local/config.go`) so the `sema` backend admits 3; `checker.go:291` | read-data ~1.7 s at 2.3 cores; 0.60 s producer wait | **-0.55 (m)**; bounded -0.40 (m) | **+3.1**; +2.2 | +10.4-11.5 (m); bounded +5.8-6.5 (m) |
| 2 | **Third read worker, restore** | `restorer.go:684` -> `newFileRestorer` workers; per-worker memory owner unknown | download ~2.35 s at 2.2 cores | **-0.66 (m)** | **+3.7** | **+20-25 (m)**: needs headroom or a bound |
| 3 | **Restore second-pass handoff** | `restorer.go` `restoreMetadataPool`: window 8 -> 256, or batch per directory | pass 2 ~0.63 s; 1.47 s sched latency | **-0.33 (m)** | **+1.8** | +1 (m) |
| 4 | **Restore first-pass prefetch** | `restorer.go` first `traverseTreeWithTreeAccess`; extend `tree_prefetch.go` to pass 1 (parallel, bounded) | pass 1 ~0.58 s at 1.2 cores | **-0.23 (m, bound)**; with #3 **-0.52 (m)** | +1.3; +2.9 | +8-9.5 unbounded (m); bound it |
| 5 | **Dwell overlap rs/ck/fp/ls** | `cmd/restic/lock.go` wait-work hook; S015 T4 `wait_index_load.go`, T1 `check_index_load.go` | index after the dwell 0.39 s here | **-0.21 (m, S015 T4 M001)** to -0.35 with prune planning and first tree reads | +1.2 to +2.0 | ~0 (m) |
| 6 | **Buy memory headroom** | the memory-only shelf (closed ground B): repack drains, restore status vectors, file lists, prune loader caps | guard 11.7 MiB | 0 by itself; unlocks #2 and the rest of #1 | (+3.7) | **-15 to -25 (not re-measured)** |
| 7 | **Second repack worker** | `repack.go:208` (`Connections()-1`) | prune execute ~0.80 s | **-0.11 (m)** | +0.6 | +8-11 (m) |
| 8 | **Decode each restore tree once** | pass 1 keeps what the prefetch decodes; `tree_prefetch.go` | 0.84 CPU-s tree JSON, twice per tree | -0.1 to -0.2 beyond #3/#4 | +0.6 to +1.1 | held trees: bound it |
| 9 | **Tree decode** | S012 T4's `data/tree_decode.go` direct node decoder | bu, ls, restore decode | -0.09 (cost-map-5, schedutil-converted) to -0.15 | +0.5 to +0.8 | ~+2 |
| 10 | **Backup-initial CPU** | archiver: lstat once per file (183k), 155k `getrandom`, futex handoffs, 610 GCs | 17.7 CPU-s at 3.47 cores | -0.1 to -0.3 | +0.6 to +1.7 | must not regrow the pool |
| 11 | **bu/bm snapshot** | two `lstat` per file, `llistxattr`, parent decode | ~1.45-1.5 s each at ~2 cores | -0.1 to -0.2 (both) | +0.6 to +1.1 | 0 |
| 12 | **Restore per-file syscalls** | `fileswriter.go`, `restoreNodeMetadata` (fchownat when the owner already matches; one open per file) | fallocate 0.83, openat 0.77, fchownat 0.34 s blocking | 0 (m, fallocate) to -0.2 | 0 to +1.1 | 0 |

**Closed / not worth it for speed (new in this map):**
- restore: no fallocate for small files (`p-falloc` null on S014).
- restore: smaller stream ranges as a memory bound for a third worker (`p-rs3b`: memory unchanged).
- a third check worker without raising the backend limit (S015 T2: never runs).
- `connections=3` for backups and ls: null.
- restore raw tree-payload prefetch (S014 T3), ls tree read-ahead (S014 T3), ls pull walk (S014 T1), backup parent
  lookup in the dwell (S014 T2/T3): null on the judge.

**Stacking envelope over S014.** No-memory items: #3 + #4 bounded (-0.5) and #5 (-0.2, likely installed by S015) =
**-0.7 s (+4)**. With check's third worker inside the guard: **-1.1 to -1.25 s (+6 to +7)**. With memory bought for
restore's third worker as well: **-1.7 to -2.0 s (+10 to +11)**. The stacks were not measured together beyond `p-rs2`
and `p-rstack+c3`.

## 9. Data, binaries, tools

- **Local** `cost-map-6/data/`: `main6/summary/` (tables.md, pprof flat/cum per phase, cpu-categories-s014.json,
  Go-trace sched/syscall/sync tops, strace -c heads), `main6/results.jsonl`, `x6/`, `x8/` (results + tables),
  `protos/` (diffs vs s014-plain), `S014-vs-S013.diff`, `s015/` (S015 team diffs vs S014 at 12:10Z),
  `closed-ground.md`.
- **Box** `/opt/league/restic-costmap6/`: `bin/`, `src/`, `runs/{main6,x6,x8}/` (results.jsonl, status.json,
  artifacts), `tools/`, `diffs/`.
- **Tools** `cost-map-6/tools/`: `build6.sh`, `build_proto6.sh`, `proto_edit6.py` (`ck3b rs3b pf1 md256 nofalloc
  dwellall t4dec nogc noxlist`), `gen_main6.py`, `gen_x6.py`, `gen_x8.py`, `spin6.sh` (the clock-hold
  spinners), `launch6.sh` (one unit on CPUs 2-5), `tables6.py main6|x6|x8`, `analyze6.sh`, `box.py`
  (`sh`, `cm` = confined to CPUs 2-5, `py`, `put`, `get`), `s015_scan.py` / `s014_scan.py` (read-only season scans),
  `judge_s014.py`. `census_run.py`, `census.go`, `patch_census.py`, `categorize.py` are cost-map-4's.
