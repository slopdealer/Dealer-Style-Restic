# Restic League V2: cost map 3 (the S009 incumbent vs stock)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-09-30 (box time 2026-09-29 22:30-23:20 CEST) on the league box, CPUs 2-5 only, while S010 ran on its own
lanes (untouched). Same instrument as cost-map-2 (`cost-map-2/COST-MAP.md`, measured on S006), re-run on today's
incumbent `incumbents/S009`. Everything is measured unless marked as an estimate or a bound. Prototypes are research
grade (interleaved reps on 4 CPUs), not the judge. Directive built from this map: `runtime/S011-directive.md`. Raw
data in `data/`; progress log `PROGRESS.md`; closed list `data/closed-ground.md`.

## 0. Headline

1. **S009 is 23.36 s and 637.9 MiB (sum of per-phase peaks); stock is 28.39 s and 990.8 MiB.** This census reads
   S009 at time -17.7%, peak -35.6%, score 26.7 vs stock (judge 42-round: 25.62). Since S006 (same run): time
   -1.84 s (prune -1.37 s, restore -0.12, backups -0.27), peak -105 MiB (backups -63, restore -11, prune -14 noisy,
   check -7). **1 s saved = 2.14 points; 10 MiB off one phase = 0.78; 0.1 s per process x7 = 1.50.**
2. **The map has flattened: no single lever above ~+3 is left, and the biggest pools are the per-process
   constant and the read-side worker count.** Per process: KDF 0.21 s (4 cores) + lock wait 0.20 s (idle) +
   index load 0.07-0.12 s + ~0.03 s spawn/exit = **~0.52 s x 7 = 3.6 s (15.6%)**. Lock-wait removal bound
   (never a candidate): **-1.53 s, +3.02**.
3. **Prune's peak is the index rewrite, not the repack.** The peak lands at t=1.53 of 1.61 s. At the peak: the
   index JSON being encoded into one growing `bytes.Buffer` (19 MB incl. doubling slack; S009's "streamed"
   encode still writes into a single buffer), the old index files loaded (cache read 6.8 MB + `DecodeAll` 13 MB),
   and `saveUnpacked`'s verify, which decompresses the whole plaintext a second time. The same index-save
   transient (14.6 MB) sits at backup-initial's peak. Prune peak is also the noisiest number here: 115-129 MiB in
   this census, **136.0 MiB in the judge's S010 incumbent arm** (the largest phase peak).
4. **A third read worker is now worth +1.05, not +3.2** (connections=3 on restore/check/prune: -1.92 s but
   +39 MiB). The time is still there; the per-worker memory is what the S008/S009 memory gains made expensive.
5. **Restore is 5.97 s: first pass 1.45 s at 1.13 cores (now the longest serial stretch), download 3.22 s at
   2.16 cores, second pass 1.15 s at 1.87 cores** (S009's parallel pass; the cost-map-2 prototype reached 0.61 s).
   Per restored file: a failing verify-open (0.55 CPU-s per run in the first pass), 4 fcntl + a failing epoll_ctl
   (poller), one fallocate (0.67 CPU-s), fchownat/utimensat/fchmodat/llistxattr. Both passes decode all 1,061
   trees (0.58 CPU-s each).
6. **ls-json writes 0.44 s faster to a FIFO than to the judge's regular file** (1.36 vs 1.80 s, unchanged since
   cost-map-2); the walk decodes 1,061 trees serially (0.72 CPU-s).
7. **Prototypes (section 7):** index-save transient (pre-sized JSON + streamed verify) **+1.47** (bi -6, prune
   -10 MiB); lock-wait overlap with the index load started after the lock file is written **+1.17** (-0.68 s; the
   same load started before `LockRepo` gained nothing); restore verify-open bound **+0.96** (-0.41 s); the three
   together **+2.64**. Connections=3 measured +1.05 / +1.56. The ls FIFO gap is NOT the batching path (unbatched
   is 0.60 s slower); its cause is open.

## 1. Method

- Binaries (`/opt/league/restic-costmap3/bin/`): judge toolchain go1.27.0, judge flags (`-trimpath -tags
  "selfupdate disable_grpc_modules" -ldflags "-s -w"`, GOAMD64=v1, offline), stock plus each incumbent's non-test
  `cmd/` and `internal/` `.go`. `stock-plain`, `s006-plain` reused from cost-map-2; `s008-plain`, `s009-plain`,
  `stock-census`, `s009-census` (census marks with process CPU from `/proc/self/stat`, restorer pass marks,
  2 ms RSS sampler dumping heap profile + MemStats at RSS growth) built here; `proto-*` = section 7.
- Harness `tools/census_run.py` (from cost-map-2, work dir moved to `/dev/shm/restic-costmap3`): the judge's bwrap
  sandbox and argv, starting states copied from bundle `fea879935b75...` (pristine/S1a/Shist, corpusA/B),
  `rusage-launch` for wall and ru_maxrss, GOMAXPROCS=4, in `systemd-run --slice=boxagents.slice -p
  AllowedCPUs=2-5`. One JSON line per run, live status file, resumable.
- Runs: `runs/main3` (188, 0 failures): plain stock/S006/S009 x3 interleaved, then S009/S008 x2 more; census S009
  x3 + stock x1; gctrace, heap-at-peak, alloc profile x1; CPU profile x2; connections 3/4 on restore/check/prune
  x3; ls to a FIFO x3; trivial commands; `strace -f -c` per phase. Prototypes `runs/x5`-`x9` (section 7).
- Fidelity: S009 vs stock here 26.7 (judge 42-round 25.62, the S010 incumbent arm 6-round medians 23.69 s /
  664.8 MiB). Null spread within a binary: time ±1-3% (bu, bm, prune ±5%), peak ±2-8 MiB (prune ±7).
- Score arithmetic on S009 (23.36 s, 637.9 MiB): **1 s = +2.14; 10 MiB off one phase = +0.78**; a per-process
  constant counts 7 times: 0.1 s = +1.50, 1 MiB = +0.55. Against the judge's arm (23.69 s, 664.8 MiB): 2.11 / 0.75.

## 2. Phase baseline (plain medians; `data/main3/summary/tables.md`)

| phase | stock s | S006 s | S008 s | **S009 s** | stock MiB | S006 MiB | S008 MiB | **S009 MiB** | S009 cores |
|---|---|---|---|---|---|---|---|---|---|
| backup-initial | 5.626 | 5.392 | 5.264 | **5.222** | 186.8 | 139.0 | 114.6 | **112.5** | 3.37 |
| backup-unchanged | 3.498 | 3.019 | 2.868 | **3.000** | 134.1 | 97.5 | 76.1 | **75.7** | 1.94 |
| backup-mutated | 3.472 | 3.171 | 3.021 | **3.086** | 164.3 | 110.7 | 87.6 | **85.2** | 2.05 |
| restore | 6.979 | 6.089 | 6.109 | **5.968** | 211.1 | 120.0 | 125.0 | **109.4** | 1.92 |
| check-read-data | 2.890 | 2.788 | 2.787 | **2.688** | 100.5 | 83.3 | 75.1 | **76.1** | 2.26 |
| forget-prune | 3.340 | 2.960 | 1.644 | **1.592** | 126.5 | 134.4 | 134.3 | **120.7** (judge 136.0) | 2.23 |
| ls-json | 2.589 | 1.777 | 1.771 | **1.801** | 67.5 | 57.7 | 57.7 | **58.4** | 1.37 |
| **total / sum** | **28.39** | **25.20** | **23.46** | **23.36** | **990.8** | **742.6** | **670.4** | **637.9** | |

n: stock/S006 3, S008 2, S009 5. Shares of S009 time: restore 26%, backup-initial 22%, bu/bm 13% each, check 12%,
ls 8%, prune 7%. Peaks: prune 19-20% (the largest), backup-initial 18%, restore 17%, bm 13%, check 12%, bu 12%,
ls 9%. Prune and restore are the only phases whose peak did not fall with S008's buffer work.

## 3. Boundary and stages (census marks, n=3 medians; process CPU per stage)

| phase | wall | KDF (cores) | lock wait | index load (cores) | work | work cores |
|---|---|---|---|---|---|---|
| backup-initial | 5.272 | 0.208 (3.99) | 0.201 | 0.002 | 4.837 | 3.48 |
| backup-unchanged | 2.967 | 0.209 | 0.201 | 0.074 (1.49) | 2.454 | 1.92 |
| backup-mutated | 3.040 | 0.208 | 0.201 | 0.084 (1.44) | 2.486 | 2.08 |
| restore | 6.305 | 0.208 | 0.201 | 0.095 (1.58) | 5.794 | pass 1 1.13, download 2.16, pass 2 1.87 |
| check-read-data | 2.643 | 0.208 | 0.201 | 0.116 (1.56) | 2.077 | structure 3.59, read-data 2.30 |
| forget-prune | 1.610 | 0.210 | 0.201 | 0.070 (1.57) | 1.097 | plan 2.72, execute 2.24 |
| ls-json | 1.906 | 0.207 | 0.201 | 0.106 (1.70) | 1.367 | 1.20 |

Stock for comparison: KDF 0.48-0.71 s, lock 0.20, index 0.09-0.18; restore passes 1.91 / 3.27 / 1.91 s.

- **restore:** first pass 1.446 s at 1.13 cores; download 3.217 s at 2.16; second pass 1.146 s at 1.87 (S006
  1.31 / 3.03 / 1.81). Restore CPU rose 9.6 -> 11.3 CPU-s per run with the parallel pass (futex 56k).
- **check:** read-data 1.91 s at 2.30 cores (2 workers, 76 packs), structure 0.15 s.
- **prune:** plan 0.22 s; execute 0.88 s at 2.24 cores (was 2.06 s at 1.35 on S006: S008's async repack).
- **ls:** walk + output 1.37 s at 1.20 cores (regular file); 0.93 s to a FIFO.
- **Memory floor:** every repository command is at ~59.5 MiB after the KDF; ls-json's peak (58.4) IS the floor
  (peak at t=0.53, the KDF/index moment). `cat config` 55.5 MiB, `version` 21.8.
- **GC:** backup-initial 443 cycles (2% GC CPU), restore 123, prune 141, check 133.

## 4. Where the CPU goes (S009, CPU s per run; `data/main3/summary/cpu-categories-s009.txt`)

| phase | CPU s | top categories |
|---|---|---|
| backup-initial | 17.7 | syscalls 3.8 (fcntl 555k = 6 per file, read 401k, newfstatat 183k, getrandom 155k), zstd compress 3.1, sha256 2.5, chunker 1.6, AES 1.1 |
| restore | 11.3 | syscalls 4.5 (openat 183k incl. 90.6k failing verify-opens, fcntl 371k, fallocate 89k, fchownat/utimensat/fchmodat/llistxattr 91k each), sha256 1.1, zstd decode 0.7, tree JSON 0.7 |
| check-read-data | 6.5 | sha256 1.9, zstd decode 0.8, AES 0.6 |
| backup-mutated / unchanged | 6.4 / 5.9 | syscalls 1.7 / 1.5 (newfstatat 183k, llistxattr 91k), chunker 0.6, JSON 0.5 |
| forget-prune | 3.6 | spread: zstd 0.3+0.2, sha256 0.3, JSON 0.3 |
| ls-json | 2.5 | scrypt ~0.8, tree JSON decode 0.72 (`Node.UnmarshalJSON` 0.47), JSON encode 0.34, output writer 0.08 |

**Allocation.** backup-initial ~7.4 GB (`saveFile` exact-size copies 1.65 GB, `saveAndEncrypt` 1.3 GB +
`sliceForAppend` 1.3 GB, zstd encode 0.77, verify `DecodeAll` 0.71); restore `DecodeAll` 0.88 GB; check
`bufReader.ReadFull` 0.35 GB (per blob) + `toPackedBlob` 0.15; prune `DecodeAll` 0.22, repack copies 0.17,
`toPackedBlob` 0.15.

## 5. Who owns the peak (heap at the RSS high-water mark, S009; `data/main3/summary/pprof/peakheap-*`)

| phase | peak at | HeapInuse | top in-use owners |
|---|---|---|---|
| backup-initial | t=4.48 of 5.27 (final index flush) | 66 | read buffers 16 MB (2 x 8 MiB); **index save `bytes.Buffer` 12 MB (saveFullIndex cum 14.6)**; zstd encoders 5.6+3.6; index 4.7 |
| backup-unchanged | work | 46 | read buffers 16 MB; zstd 5.6+2.4; index 2.6 |
| backup-mutated | work | 48 | read buffers 16 MB; `saveAndEncrypt` 5.4 + ciphertext 4.2; index 6.3; zstd 5.6 |
| restore | download (t=2.6) | 57 | `DecodeAll` 8.3; stream part 8.0 (16.2 cum); **`addFile` 8.2 + `trackFile` 3.7**; index 5.2 |
| check-read-data | read-data | 36 | `bufReader.ReadFull` 5.5 (per blob); bufio 2.4; stream decoder 2.4+1.5+1.2; index 2.6 |
| forget-prune | t=1.53 of 1.61 (index rewrite) | 82 | **index rewrite: encode `bytes.Buffer` 19 MB, old index load 20 MB (cache read 6.8 + `DecodeAll` 13)**; repack save path 12.8 (EncodeAll); zstd state 4.6+4.1; stream part 4.0 |
| ls-json | KDF/index | 36 | the KDF moment; heap afterwards ~7 MB |

## 6. Knobs on S009 (whole-workload score vs S009 plain)

| knob | phases | effect | score |
|---|---|---|---|
| `local.connections=3` | restore, check, prune | restore -0.94 s +17.7 MiB; check -0.81 s +8.4; prune -0.18 s +13.0 | **+1.05** (was +3.21 on S006) |
| `local.connections=4` | restore, check, prune | -2.30 s, +59 MiB | +0.28 |
| ls stdout to a FIFO | ls | -0.44 s, -2.1 MiB | (+1.11) |

## 7. Prototypes (research grade: 4 interleaved reps with s009-plain on CPUs 2-5, x8 5 reps; never candidates)

Diffs against s009-plain in `data/protos/`; tables in `data/runs/x5..x9/prototypes.md` (`tools/xtab.py`). Scores
use the main3 denominators (23.36 s, 637.9 MiB) and only the phases each run measured.

| prototype (run) | edit | phase effect | score |
|---|---|---|---|
| `proto-lock0` (x5) | lock wait 0 (**bound only**; removal is not allowed) | -0.11 to -0.30 s per phase, total -1.53 s | +3.02 |
| `proto-lockov` v1 (x5) | index load started in a goroutine before `LockRepo` | -0.22 s total, noise in memory | -0.29 |
| **`proto-lockov2`** (x9) | index load started by a hook after `createLock`, inside the unchanged 200 ms dwell (check excluded: it loads its own index; bi has an empty index) | bu -0.22, bm -0.07, restore -0.20, prune -0.11, ls -0.07 s = **-0.68 s** | **+1.17** (time part +1.44) |
| **`proto-noverify`** (x6) | restore: skip the verify-open of each target file (**bound**; a sound version skips it only where the file cannot exist) | restore **-0.41 s** | **+0.96** |
| **`proto-idxsave`** (x7) | `SaveIndex` JSON buffer pre-sized from the blob count; `verifyUnpacked` decompresses as a stream and compares in 64 KiB chunks (verify kept) | backup-initial **-6.0 MiB**, prune **-9.6 MiB**, time flat | **+1.47** (memory part +1.2) |
| `connections=3` (x6) | knob | restore -0.93 s +18.8 MiB, check -0.82 s +9.3, prune -0.14 s +3.4 | +1.56 (main3: +1.05) |
| `proto-best` (x6) | lockov v1 + noverify | restore -0.47 s | +1.26 |
| `proto-best` + connections=3 (x6) | | restore -1.48 s +14.5, check -0.80 s +11.2, prune -0.24 s +8.9 | +2.67 |
| **`proto-all3`** (x7) | lockov2 + noverify + idxsave | -0.74 s, -13.4 MiB (bi -7.1, prune -6.9) | **+2.64** |
| `proto-nobatch` (x8) | ls: regular-file stdout takes the unbatched path | ls **+0.60 s** | -1.34 |
| `proto-idlewin` (x8) | ls: idle flush window 100 us -> 5 ms | flat | -0.14 |
| ls to a FIFO (x8) | harness only | ls -0.48 s | (+0.96) |

Notes.
- **Lock overlap must start after the lock file exists.** v1 started the index load before `LockRepo`; the local
  backend's two connections were then busy loading index files while the lock code wanted to list and create lock
  files, so the lock finished later by about as much as the load saved. v2 hooks the start into `newLock` after
  `createLock`: -0.68 s over five phases. The dwell has ~0.1 s of headroom left per phase for more read-only work
  (snapshot load, prune plan, restore/ls tree reads, S010 T4's backup parent lookup).
- **The ls gap is not the batching path.** Unbatched output to a regular file is 0.60 s slower; the batching
  installed in S004 is what makes the file case tolerable. Output to a FIFO is still 0.44-0.48 s faster than the
  batched file path; the syscall profile of ls (1,203 writes, 7.3k futex, 1.6k nanosleep, ~1.1k pack opens for
  1,061 tree reads) does not explain it, so the cause is open. Nobody has measured where the ls process waits.
- idxsave touches only the transient; the old index load in `Rewrite` (cache read + `DecodeAll`, 20 MB at the
  prune peak) is untouched, so prune has more to give.

## 8. Structural read: menu, ceilings, soundness

Ceiling = measured share times a plausible reduction, scored on S009. (m) = measured prototype above.

| # | area (files) | measured share on S009 | lever | ceiling | soundness |
|---|---|---|---|---|---|
| 1 | **Index save / rewrite transient** (`index/index.go`, `repository.go` saveUnpacked/verifyUnpacked, `master_index.go` Rewrite) | prune peak at the rewrite: JSON buffer 19 MB, old index load 20 MB, verify re-decompress; bi final flush 14.6 MB | pre-size + streamed verify (+1.47 m); then never materialise the JSON (stream compress, verify by hash of the stream) and stream the old-index load | **+1.5 (m) to +2.5** | SOUND (verify stays, format unchanged) |
| 2 | **Read-side workers, bounded per-worker memory** (`backend/local`, `filerestorer.go`, `checker.go`, `repack.go`, `streamPack`) | connections=3: -1.9 s (+4.0) but +29-39 MiB | bound stream part + pool, `DecodeAll` destination, check `ReadFull`, bufio per worker so the third worker costs ≤ +10 MiB | +1.05-1.56 (m knob); **~+3** bounded | worker count = product RULING; memory work SOUND |
| 3 | **Lock-wait overlap** (`lock_file.go`, `lock.go`, `cmd/restic/lock.go`) | 0.20 s x 7 idle | read-only work started after `createLock`: index load (+1.17 m), then snapshots, prune plan, tree reads, backup parent lookup | bound +3.02 (m); realistic **+1.2 (m) to +2** | OPERATOR RULING: overlap only; `TestSetLockTimeout` frozen |
| 4 | **Restore first pass + per-file syscalls** (`restorer.go`, `fileswriter.go`, `data/tree.go`) | pass 1 1.45 s at 1.13 cores; 90.6k failing verify-opens (0.55 CPU-s); trees decoded twice (0.58 CPU-s per pass); fallocate 89k (0.67 CPU-s) | skip verify-open where the file cannot exist (bound -0.41 s m); decode trees once or prefetch ahead; no fallocate where it cannot matter | **+1.0 (m) to +2** | SOUND if manifest, mtimes and error order match stock |
| 5 | **ls output path** (`termstatus`, `cmd_ls.go`) | FIFO 0.44-0.48 s faster than the judge's regular file | find where ls waits (not the batching: m) | +0.9 | SOUND (same bytes and order) |
| 6 | **Restore second pass, rest** (`restorer.go`) | 1.15 s at 1.87 cores; prototype reached 0.61 | directory metadata and hardlinks off the traversal goroutine; pending bound 8 | ~+1 | SOUND; S009's ordered settlement is locked |
| 7 | **Restore per-file memory** (`restorer.go`, `filerestorer.go`) | `addFile` 8 MB + `trackFile` 4 MB at the peak | sorted-slice file list (S009 T3 lead: rs m+4%) | +0.5-0.8 | SOUND |
| 8 | **Check per-blob buffers** (`checker.go`, `bufReader`) | `ReadFull` 350 MB allocated, 5.5 MB at the peak | per-worker reuse (S008 T2 lead) | +0.4-0.6 | SOUND |
| 9 | **backup-initial CPU** (`archiver`, `fs`, `repository.saveAndEncrypt`) | 17.7 CPU-s at 3.48 cores; syscalls 3.8 (6 fcntl/file, 155k getrandom, 2 stats/file), zstd 3.1, sha256 2.5, chunker 1.6 | each CPU-s ≈ 0.29 s wall | +1-1.5 | SOUND (verify stays) |
| 10 | **Backup read buffers, round 2** (`archiver/buffer.go`) | 2 x 8 MiB in use at every backup peak | size buffers to what is read, not the max chunk | +0.5-1.5 | SOUND; S008 file is locked for its owner |
| 11 | **Per-process start-up** (index decode) | index load 0.07-0.12 s at 1.5 cores x 6 | faster decode (overlaps with 3) | ~+0.5 | SOUND |
| 12 | **GC / allocation churn** | bi 443 GCs at 2% GC CPU, 7.4 GB allocated | fewer copies in `saveFile` / `saveAndEncrypt` | ≤ +0.5 time; memory only via items 1/10 | SOUND; GOGC closed |

**Not worth it / closed.** KDF (0.21 s at 4 cores, memory-hard; every lower-memory variant costs more time than
ls's floor gives). GOGC either way. connections as a bare knob. See `data/closed-ground.md`.

**Stacking.** lockov2 + noverify + idxsave measured **+2.64**; adding a third read worker to the restore/check/prune
side measured +2.67 over the noverify/lockov-v1 pair. A realistic S011 envelope is +3 to +6 over S009.

## 9. Closed ground

`data/closed-ground.md` (S001-S010 M001, compiled read-only from reviews, status.json and team-tree diffs). New
since cost-map-2: the in-flight known-blob check is dead (42-round +0.01); inline known-blob completion has been
built 5 times; decoded-blob scratch reuse (4 rows, S007 T4); a second metadata pool duplicating S009's; prune
index loader/saver caps alone (-0.24). Open leads: S010 T4 backup lock-dwell overlap (+0.96, interval above zero),
S009 T3 sorted-slice restore file list, S008 T2 check blob-buffer reuse, S009 T4 stream-buffer drain after repack,
S009 T2 streaming index decode (mixed). Never built: tree prefetch/decode-once, restore first-pass parallelism,
one open per restored file, chunker reuse, stat-once, poller removal in backup, index representation (#1988),
bounded-memory third read worker.

## 10. Data and binaries

- **Local** `cost-map-3/data/`: `main3/summary/` (tables.md, summary.json/txt, pprof CPU/alloc/heap-at-peak and
  MemStats for S009, CPU categories for stock and S009, strace per phase), `main3/results.jsonl`,
  `runs/x5..x9/` (results.jsonl, prototypes.md), `protos/` (prototype diffs vs s009-plain), `closed-ground.md`,
  `s008-vs-s006.diff`, `s009-vs-s008.diff`.
- **Box** `/opt/league/restic-costmap3/`: `bin/` (stock/s006/s008/s009 plain, stock/s009 census, protos),
  `runs/{main3,x5..x9}/results.jsonl` and `artifacts/<run-id>/` (raw cpu.pprof, mem.pprof, peak-heap.pb.gz,
  marks.json, gctrace stderr, strace), `src/` (trees), `tools/` (= `cost-map-3/tools/`).
- **Tools** `cost-map-3/tools/`: `build3.sh`, `build_proto3.sh`, `census.go` + `patch_census.py`, `census_run.py`
  (plan `main3`, `extra:<json>`), `analyze.py`, `categorize.py`, `tables3.py`, `xtab.py`, `proto_edit3.py`
  (`lock0`, `lockov`, `lockov1`, `noverify`, `idxsave`, `nobatch`, `idlewin`), `run_main3.sh`, `run_x5..x9.sh`.
