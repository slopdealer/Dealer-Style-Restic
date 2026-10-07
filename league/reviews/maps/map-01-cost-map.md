# Restic League V2: cost map (stock and S001 incumbent)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-09-26 on the league box, CPUs 2-5 (CCD0, idle when checked; SMT siblings 50-53 idle). No league process was
touched. The frozen workload and reference states came from the judge bundle
`/dev/shm/restic-league-v2-cache/bundle-fea879935b75...` (read only, shared flock held). Everything below is measured
unless it is marked as an estimate. The directive for owners is `runtime/S004-directive.md`. The raw data is in `data/`.

## 0. Headline

1. **Every phase pays about 0.69 s before it touches any data, and that is about 18% of the whole workload.**
   - scrypt KDF on the repository key: 0.47 s per process. The key uses N=32768, r=8, p=8, and x/crypto runs the 8
     lanes one after another on one core.
   - A fixed `time.Sleep(200 ms)` in `newLock`.
   - Index load: 0.07 to 0.14 s.
   - Seven processes: 3.3 s KDF + 1.4 s lock + 0.7 s index = **5.4 s of the incumbent's 27.97 s.**
   - No team has touched any of it.
   - A measurement-only prototype combined three changes: 4-lane scrypt with the identical key and the same memory,
     no lock wait, and buffered ls output. It cut the workload by **16.8% (score +9.1 over 4 reps)**. S001's whole
     promotion was +3.24.
2. **ls-json spends about 1 s sending one line at a time to the terminal goroutine.** The same binary finishes in
   1.58 s when stdout is a FIFO and 2.57 s when it is a file (the judge uses a file).
   - `ls --json` sends every line through an unbuffered channel, and each line becomes one `write(2)`.
   - strace counts about 1 futex per output line (90,705 futex calls for 91,265 lines).
3. **The read side (restore, check and prune) runs 2 workers on 4 cores.** The workers are
   `local.connections=2`, and they reach only 1.4 to 1.8 cores.
   - Setting `connections=4` cuts restore by 1.17 s, check by 0.93 s and prune by 1.34 s (3.4 s = 12% of the
     workload).
   - It also adds 72, 30 and 15 MiB of peak, because every worker materialises a whole pack (up to 32 MiB) as a new
     allocation.
   - **Time is available here. Per-worker memory is what blocks it.**
4. **Restore's peak (216 MiB, the largest phase) is mostly pack-buffer churn, not live data.** At the peak:
   - `streamPackPart` holds 69 MB in use.
   - The heap has 80 MB idle but unreleased: large buffers freed and not reused.
   - The live restore structures (hardlink index, file list, tracking, index) total about 20 MB.
5. **Restore spends 3.5 of its 7 s in two sequential metadata passes.** The first tree pass takes 1.89 s and the
   second (chown/chmod/utimes) pass takes 1.59 s. Both run on one goroutine. Each file costs about 17 syscalls: 2
   openat, 4 fcntl, a failing epoll_ctl, fallocate, and so on.
6. **backup-initial is the only CPU-bound phase.** It uses 3.2 of 4 cores and 19.5 CPU-s, and it allocates 15.2 GB
   for 1.7 GB of input.
   - The allocations come from zstd `EncodeAll(dst=nil)`, 8 MiB pool buffers refilled after each of about 590 GCs,
     the verify decrypt and decompress into fresh buffers, the S001 copy, and a chunker per file.
   - Temp pack files are written once, read twice and written again.
7. **Knobs trade, they don't win.**
   - GOGC 100: score −9.8.
   - GOGC 25: +1.5, but time is −11.5%, which breaks the 5% axis cap.
   - connections=4: −0.2, with RSS −12.7% (breaks the cap).
   - read-concurrency 4: −1.4.
   - `--no-cache`: −0.9 (the local cache costs nothing on tmpfs).

## 1. Method

- **Binaries.** Built with the judge's pinned toolchain (`toolchain/go`, go1.27.0), `-trimpath -tags "selfupdate
  disable_grpc_modules" -ldflags "-s -w"`, GOAMD64=v1, offline. These are in `/opt/league/restic-costmap/bin/`.
  - `stock-plain`, `s001-plain`: judge flags.
  - `*-census`: adds tag `profile`, which enables restic's `--cpu-profile` and `--mem-profile`, plus
    `internal/census` marks. The marks are timestamp, RSS, VmHWM, heap-object bytes, GC cycles, GC CPU,
    RssAnon/RssFile, and a 2 ms RSS sampler that dumps a heap profile and MemStats whenever RSS grows 4 MiB.
    They are placed at KDF, key, lock, index load, work start/end, check and prune stages, and restore passes.
  - `proto-*`: measurement-only prototypes (section 6).
  - Tools: `cost-map/tools/`.
- **Harness.** `tools/census_run.py` replicates the judge per phase: the same bwrap sandbox and argv, the same
  starting states (copy of pristine/S1a/Shist, bind of the bundle's corpusA/B so inode and ctime match), and the
  judge's own `rusage-launch` for wall time and ru_maxrss, with GOMAXPROCS=4.
  - It runs in `systemd-run --slice=boxagents.slice -p AllowedCPUs=2-5`.
  - It writes one JSON line per run and a live status file, and it can resume.
- **Fidelity.** The plain stock medians (n=5) match the judge's stock phase figures to within 2%, apart from one
  phase:

| phase | this census | judge |
|---|---|---|
| backup-initial | 5.59 s | 5.55 s |
| backup-unchanged | 3.39 s | 3.40 s |
| backup-mutated | 3.38 s | 3.35 s |
| restore | 6.86 s | 6.91 s |
| check-read-data | 2.92 s | 2.92 s |
| forget-prune | 3.27 s | 3.42 s |
| ls-json | 2.59 s | 2.61 s |

  - Peaks: 188.5 / 132 / 162 / 211 / 101 / 127 / 68 MiB.
  - forget-prune is 4% under the judge. It is the noisiest phase: plain spread 2.73 to 3.39 s.
- **Runs.**
  - `runs/main`: 276 runs, 0 failures. Plain ×5, census marks ×3, gctrace, heap-at-peak, CPU profile ×2, alloc
    profile, `--no-cache` ×3, GOGC 101 and 25 ×2, and trivial commands ×5.
  - `runs/extra`: 117 runs. ls to FIFO and to /dev/null, `-o local.connections=4|8`, `--read-concurrency 1|4`,
    plain ×3.
  - `runs/extra2`: 35 runs. Restore pass marks, RSS anon/file split, and `strace -f -c` per phase.
  - `runs/proto`: 116 runs (section 6b).
- **Score arithmetic** (incumbent = S001, 27.97 s, sum of peaks 920.7 MiB):
  - 1 s saved anywhere = 3.58% time = **+1.79 score**.
  - 10 MiB off one phase's peak = 1.09% RSS = **+0.54 score**.
  - A per-process constant counts 7 times: 0.1 s per process = +1.25 score, and 1 MiB per process = +0.38 score.

## 2. Phase baseline (S001 incumbent, plain, n=5 medians; `data/main/summary.txt`)

| phase | wall s | share of time | peak MiB | share of peak | user s | sys s | cores used |
|---|---|---|---|---|---|---|---|
| backup-initial | 6.000 | 21.5% | 152.0 | 16.5% | 14.32 | 5.06 | 3.23 |
| backup-unchanged | 3.379 | 12.1% | 107.6 | 11.7% | 4.29 | 1.40 | 1.68 |
| backup-mutated | 3.304 | 11.8% | 142.6 | 15.5% | 4.43 | 1.52 | 1.80 |
| restore | 6.801 | 24.3% | 215.8 | 23.4% | 6.02 | 4.16 | 1.50 |
| check-read-data | 2.844 | 10.2% | 104.0 | 11.3% | 4.70 | 0.53 | 1.84 |
| forget-prune | 3.071 | 11.0% | 129.7 | 14.1% | 3.21 | 0.95 | 1.36 |
| ls-json | 2.571 | 9.2% | 69.0 | 7.5% | 2.41 | 0.54 | 1.15 |
| **total / mean** | **27.970** | | **131.5 mean** | | | | |

S001 against stock:
- backup-initial: +7.3% time, −19% peak.
- backup-unchanged: −0.3% time, −19% peak.
- backup-mutated: −2% time, −12% peak.
- The other phases are equal within noise.

Null spread, plain reps within a binary:
- Time: ±1% on most phases, ±5% on forget-prune and backup-mutated.
- Peak: ±3 to 8 MiB.

## 3. Boundary accounting (S001 census marks, n=3 medians; `data/extra2/summary/summary.txt`)

Every phase runs the same prologue: Go init and package inits, open backend, stat config, SearchKey (scrypt KDF),
load config, cache setup, lock (create lock file, **sleep 200 ms**, re-list locks), and then index load.

| phase | pre-KDF | KDF | lock | index load | work | exit→wall | wall | RSS after KDF |
|---|---|---|---|---|---|---|---|---|
| backup-initial | 0.004 | 0.473 | 0.201 | 0.001 | 5.357 | 0.029 | 6.07 | 59.8 |
| backup-unchanged | 0.004 | 0.469 | 0.202 | 0.130 | 2.624 | 0.020 | 3.45 | 59.8 |
| backup-mutated | 0.004 | 0.478 | 0.202 | 0.111 | 2.308 | 0.029 | 3.14 | 60.3 |
| restore | 0.004 | 0.496 | 0.201 | 0.117 | 6.960 | 0.021 | 7.80 | 59.9 |
| check-read-data | 0.004 | 0.469 | 0.201 | 0.140 | 2.218 | 0.018 | 3.05 | 60.1 |
| forget-prune | 0.004 | 0.478 | 0.202 | 0.071 | 2.633 | 0.027 | 3.42 | 60.0 |
| ls-json | 0.004 | 0.468 | 0.202 | 0.131 | 1.842 | 0.019 | 2.67 | 59.9 |
| `cat config` | 0.004 | 0.470 | 0.201 | – | – | 0.020 | 0.70 | 60.2 |
| `version` | – | – | – | – | – | 0.014 | 0.017 | – |

**Sum across the 7 phases:**
- KDF 3.33 s (11.9% of 27.97 s).
- Lock wait 1.41 s (5.0%).
- Index load 0.70 s (2.5%).
- Process spawn, runtime init and exit about 0.17 s (0.6%).

**Memory floor per process.**
- `version` peaks at 24.0 MiB: about 20.5 MiB is the file-backed binary pages and 3.6 MiB is anonymous.
- File-backed RSS grows to 22 to 23 MiB in every phase. It comes from dependency package inits (protobuf, GCS,
  Azure) and cannot be changed without dropping backends, which is forbidden.
- Every phase then jumps to **60 MiB** at the end of the KDF: the scrypt V array is 32 MiB, plus 1 MiB of B/XY.
- Heap objects are 34 MiB at kdf:end, and the dead V is still held until the next GC. Lock creation then adds the
  zstd encoder (+5.6 MB) to compress the ~200-byte lock file.

**Work sub-stages.**
- **restore:** first tree pass 1.885 s, file restore 3.483 s, second metadata pass 1.590 s.
- **check:** snapshots and structure 0.165 s, read-data 2.05 s (76 packs).
- **forget-prune:** forget plus plan 0.23 s, repack 2.14 s (20 packs, 318 MiB), index rebuild 0.25 s, pack removal
  ~0.1 s.
- **ls:** tree walk plus output 1.84 s to a file; the same walk takes 0.8 s to a FIFO.

## 4. Where the CPU goes (S001, CPU s per run, pprof flat bucketed; `data/cpu-categories-s001.json`)

| category | bi | bu | bm | restore | check | prune | ls | total |
|---|---|---|---|---|---|---|---|---|
| **CPU s per run** | 19.50 | 5.99 | 6.14 | 10.16 | 5.17 | 4.75 | 2.96 | 54.66 |
| syscalls (read/write/open/stat/xattr) | 4.12 | 1.56 | 1.47 | 4.38 | 0.17 | 0.55 | 0.48 | 12.71 |
| sha256 (blob IDs, verify, pack IDs) | 2.52 | 0.33 | 0.47 | 0.97 | 1.92 | 0.54 | 0.04 | 6.79 |
| zstd compress | 3.08 | 0.26 | 0.27 | 0 | 0 | 0.38 | 0 | 3.99 |
| **scrypt KDF** | 0.46 | 0.43 | 0.46 | 0.40 | 0.43 | 0.47 | 0.43 | 3.08 |
| JSON encode/decode (trees, index, ls) | 0.02 | 0.66 | 0.39 | 0.42 | 0.23 | 0.36 | 0.64 | 2.71 |
| chunker (Rabin) | 1.56 | 0.54 | 0.57 | 0 | 0 | 0 | 0 | 2.67 |
| AES-CTR + Poly1305 | 1.06 | 0 | 0.14 | 0.53 | 0.55 | 0.33 | 0 | 2.60 |
| zstd decompress | 0.56 | 0.11 | 0.10 | 0.69 | 0.65 | 0.21 | 0.10 | 2.42 |
| memmove (copies) | 1.06 | 0.14 | 0.20 | 0.11 | 0.28 | 0.35 | 0.04 | 2.18 |
| memclr (zeroing new allocations) | 0.92 | 0.10 | 0.15 | 0.18 | 0.08 | 0.23 | 0.03 | 1.70 |
| GC mark/sweep + scavenger (madvise) | 0.83 | 0.07 | 0.15 | 0.49 | 0.17 | 0.31 | 0.09 | 2.11 |

**Notes.**
- The CPU profile does not show off-CPU waits. Restore uses only 1.5 cores and ls only 1.15.
- backup-initial allocates 15.2 GB:
  - zstd `encodeAll`: 4.5 GB.
  - Pool buffers: 3.2 GB, meaning 8 MiB buffers refilled after GCs.
  - S001 copy: 1.7 GB.
  - Verify `DecodeAll`: 1.55 GB.
  - Ciphertext: 1.27 GB.
  - Verify decrypt: 1.26 GB.
  - `chunker.NewBase` per file: 418 MB.
- S001 runs 589 GCs in backup-initial (stock runs 313).

**strace -c** (`data/extra2/artifacts/strace-*`), per phase:

| phase | notable syscall counts |
|---|---|
| restore | openat 182,744 (90,277 fail); fcntl 369,700; epoll_ctl 92,424 (all fail); fallocate 89,270; newfstatat 183,653; fchownat, fchmodat, utimensat and llistxattr about 91k each; pwrite 90,119 |
| backup-initial | read 560,965; fcntl 554,946; newfstatat 182,662 (the scanner and the archiver each stat every file); getrandom 155,335; llistxattr 91,266; epoll_ctl 92,577 (failing) |
| backup-unchanged | newfstatat 182,597; llistxattr 91,266 |
| ls-json | write 91,289; futex 90,705 |

The fcntl and epoll_ctl counts in restore and backup-initial come from Go's `os.OpenFile` registering regular files
with the poller: about 4 to 6 wasted syscalls per file, times 90k files, in each of 4 phases.

## 5. Who owns the peak (heap at peak, MemStats at the RSS high-water mark, S001; `data/main/pprof/peakheap-*`)

| phase | RSS at peak | HeapInuse | idle, not released | GC goal | top in-use owners at the peak |
|---|---|---|---|---|---|
| backup-initial | 195 | 146 | 32 | 157 | pool 8 MiB read buffers 32 MB; S001 chunk copies 16 MB; ciphertext 14 MB; saveAndEncrypt 7 MB; verify DecodeAll 7 MB; zstd encoder state 5.5 + 4.6 + 5.5 MB |
| backup-unchanged | 120 | 72 | 20 | 74 | pool buffers 32 MB; zstd encoder 5.5 MB; index 3 MB |
| backup-mutated | 140 | 88 | 23 | 94 | pool 16 MB; copies 12 MB; ciphertext 6 MB; index 6 MB; zstd 5.5 + 6 + 4.6 MB |
| restore | 237 | 133 | **80** | 181 | **streamPackPart 69 MB** (whole pack parts, new allocation per pack); hardlink index 6 MB; index 6 MB; zstd encoder 5.5 MB (lock file only); addFile 4 MB; trackFile 3 MB |
| check-read-data | 112 | 49 | **43** | 71 | bufReader.ReadFull 17 MB; zstd DecodeAll 9.5 MB; bufio 2×4 MiB; index 5.7 MB; zstd encoder 5.5 MB |
| forget-prune | 129 | 85 | 17 | 86 | zstd DecodeAll 11.8 MB; index rewrite (generatePackList 7 MB, JSON buffer 6 MB); zstd encoder 5.5 + 4.6 MB; index 4.6 MB |
| ls-json | 68 | 51 | 0 | 51 | **dead scrypt V (32 MiB) not yet collected** + zstd encoder (lock file) + index load |

**Reading the table.**
- The peaks of restore and check are 35 to 40% idle heap. Large transient buffers of varying size (whole pack
  parts, whole blobs) are freed and not reused. Fixed-size reusable buffers remove both the churn and the idle heap.
- GOGC=50 (restic's default, `tweakGoGC`) puts the goal at 1.5× live, so live buffer bytes are multiplied by
  about 1.5 at the peak.

## 6. Experiments: knobs (whole-workload score vs S001) and boundary prototypes

| experiment (S001 tree, phases affected) | time | RSS | score | note |
|---|---|---|---|---|
| `--no-cache` (all) | −2.2% | +0.4% | −0.9 | local cache is free on tmpfs: CLOSED |
| GOGC 100 (env 101) | +2.8% | −22.3% | −9.8 | |
| GOGC 25 | −11.5% | +14.5% | +1.5 | breaks the 5% time cap; exchange rate ≈ 0.8% time per 1% RSS near 50 |
| `-o local.connections=4` (restore/check/prune only) | +12.3% | −12.7% | −0.2 | time is there; memory per worker kills it |
| `local.connections=8` | +11.3% | −41% | −15 | restore 435 MiB |
| `--read-concurrency 4` (bi, bm) | +1.7% | −4.5% | −1.4 | |
| `--read-concurrency 1` (bi, bm) | −8.5% | +4.7% | −1.9 | |
| ls stdout FIFO / file / /dev/null | 1.58 / 2.57 / 2.46 s | | | cost is per-line handoff plus a blocking write, not tmpfs |
| KDF bench (`data/kdfbench.txt`; one process per variant, identical key) | | | | see below |

**KDF bench results:**
- Serial (x/crypto, what restic does): 0.47 s at 35.7 MiB HWM.
- 4 lanes storing every V: 0.14 s at 134 MiB.
- **4 lanes storing every 4th V: 0.20 s at 35.8 MiB.**
- 4 lanes storing every 8th V: 0.30 s at 19.4 MiB.
- 2 lanes storing every V: 0.26 s at 68 MiB.

**Boundary prototypes** (measurement only, never candidates; `runs/proto`, 4 reps interleaved with s001-plain):
- `proto-kdf`: 4-lane every-4th-V scrypt.
- `proto-lock`: no 200 ms wait.
- `proto-ls`: 256 KiB bufio under the ls JSON encoder.
- `proto-all`: all three.

Results are in section 6b. The build scripts are `tools/build_proto.sh` and `tools/proto_edit.py`, and the scrypt
code is `tools/proto/zz_parscrypt.go`.

## 6b. Prototype results (research-grade: 4 interleaved reps, not the judge; `data/proto-summary.txt`)

Baseline in the same run: s001-plain, 28.79 s total, sum of peaks 923.1 MiB.

| prototype | per-phase time change | whole workload | score |
|---|---|---|---|
| proto-kdf | −0.21 to −0.35 s in every phase | −2.02 s (−7.0%); peaks −2.0% (partly noise) | **+4.5** |
| proto-lock (wait removed; RULING) | −0.19 to −0.38 s in every phase | −1.57 s (−5.4%); peaks flat | **+2.5** |
| proto-ls | ls 2.62 → 1.86 s | −0.76 s (−2.6%) | **+1.3** |
| **proto-all** | ls 2.62 → **1.25 s** | **−4.84 s (−16.8%)**; peaks −1.3% | **+9.1** |

These are ceilings measured on real restic binaries. The oracle, compat tests and judge noise still decide. The
lock variant here removes the wait outright. The overlap variant in section 7A is the one that needs no ruling.

## 7. Structural read: areas, ceilings, soundness

The ceiling is the measured share times a plausible reduction. The score uses section 1's arithmetic. **SOUND** means
it keeps format v2, keeps stock interop and oracle-identical outputs, and does not re-sign a frozen test API.
**RULING** means it is sound for the oracle, but the owner must accept the product argument.

### A. Process boundary (all 7 phases; about 20% of time; ls peak)

| lever | measured cost | plausible saving | score ceiling | soundness |
|---|---|---|---|---|
| scrypt lanes in parallel with a time-memory tradeoff (`internal/repository/crypto/kdf.go`; `KDF` signature unchanged) | 0.47 s × 7 | −0.27 s × 7 = 1.9 s | **+3.4** | SOUND: identical key, pure computation, same memory at every-4th-V |
| lock: drop or overlap the fixed 200 ms `waitBeforeLockCheck` (`lock_file.go`) | 0.20 s × 7 | overlap with read-only index and snapshot loading: ~0.1 s × 7; removed for strongly consistent local backends: 0.2 s × 7 | +1.25 / +2.5 | Overlap: SOUND. Backend-aware zero wait: RULING. `TestSetLockTimeout` is a frozen API |
| buffered non-terminal output (`ui/termstatus` lineWriter/print path; ls JSON printer) | ~1.0 s in ls | ~1.0 s | **+1.8** | SOUND: same bytes and order; flush before exit and before stderr |
| free the dead KDF V before index load (explicit GC or a non-heap buffer) plus a smaller V (every-8th-V) | ls peak 69 = 32 dead V + encoder + index | −9 to −20 MiB on ls | +0.5 to +1.1 | SOUND |
| no 5.5 to 10 MB zstd encoder just to compress a lock file in read-only phases (restore, check, ls; small unpacked files) | 5.5 MB in use at restore, check and ls peaks | −5 to −10 MiB × 3 phases | +0.8 to +1.6 | SOUND if the output is valid zstd and stock can read it |
| index load/decode (0.07 to 0.14 s, 4 to 6 MB) | 0.70 s total | −0.3 s | +0.5 | SOUND |

### B. Read-side data plane (restore file restore, check read-data, prune repack)

Code: `repository.go` streamPack/streamPackPart/LoadBlobsFromPack/bufReader, `checker.go` ReadPacks/checkPack,
`repack.go`, `restorer/filerestorer.go` scheduling, and the local backend `Connections` default.

- **Measured.**
  - Wall time: 3.48 + 2.05 + 2.14 s = 7.7 s, at 1.4 to 1.8 cores.
  - `connections=4` saves 3.4 s but costs +117 MiB.
  - Peaks: restore 216 (69 MB pack parts + 80 MB idle), check 104 (43 idle), prune 130.
- **Structural leads.**
  - A fixed, reusable per-worker window (blob-at-a-time streaming from a bounded read buffer) instead of
    `make([]byte, dataEnd-dataStart)` per pack part.
  - A shared decode scratch per worker: `DecodeAll(dst)` and `Open(dst)` into reused buffers.
  - Then raise worker count to GOMAXPROCS (as a product default for the local backend) once memory per worker is
    bounded.
  - Pipelining the CPU (decrypt, hash, decompress) apart from the I/O inside a worker.
- **Ceiling.**
  - Time: −3.4 s (+6.1 score).
  - Memory: −60 to −130 MiB (+3 to +7 score).
  - Combined upper ceiling about **+9 to +13**.
- **Soundness.** SOUND: same data, integrity checks kept (hash of every blob, pack hash in check). Changing the
  local `Connections` default needs a product argument: tmpfs and SSD versus HDD seek.

### C. Restore filesystem plane and tree walking

Code: `restorer/restorer.go` passes, `fileswriter.go` createFile/openFile, `internal/fs` node metadata, `data/tree`
decode, `walker`.

- **Measured.**
  - Sequential first pass 1.89 s and second pass 1.59 s.
  - About 17 syscalls per file.
  - Each tree is decoded twice (JSON 0.42 s CPU).
  - The ls walk decodes 1,061 trees sequentially (0.8 s).
- **Leads.**
  - Parallel tree loading/prefetch for both passes and for ls.
  - A per-directory cache instead of an `ensureDir` Lstat per file.
  - Parallel metadata application, with a directory's metadata applied after its children.
  - `syscall.Open` plus `os.NewFile` (or raw fds and pwrite) to skip the poller's fcntl/epoll_ctl round trips.
  - One open per file instead of an open-fail-then-create.
  - Skip `llistxattr` when there is nothing to restore (stock semantics must hold).
- **Ceiling.**
  - Restore passes 3.5 s → about 1.2 s (−2.3 s, +4.1 score).
  - ls walk −0.4 s (+0.7).
  - Memory is small. The restore live maps are about 15 MB, and S002 T1's replay cost +8% restore RSS: CLOSED.
- **Soundness.** SOUND. Restored metadata must equal stock's manifest, and the order of directory mtimes matters.

### D. Backup write path

Code: `archiver/*` (file_saver, tree_saver, scanner), `repository.saveAndEncrypt`/verify, `packer_manager.go` and
`savePacker`, chunker use, zstd use.

- **Measured.**
  - backup-initial is CPU-bound: 19.5 CPU-s, 3.2 cores.
  - It allocates 15.2 GB.
  - Temp pack round trip: write, read to hash, read again to save, write to the repo (4 × 1.2 GB).
  - Per-file open overhead is 6 fcntl/epoll syscalls.
  - Scanner plus archiver make 2 stat calls per file, plus llistxattr.
  - zstd runs on about 0.6 GB of incompressible random data. Compression is 3.1 CPU-s.
- **Leads.**
  - Buffer reuse on the save path: `EncodeAll(src, pooled dst)`, verify into pooled buffers, a bounded free list
    instead of a `sync.Pool` that GC drains, and per-worker chunker reuse (`chunker.Reset`).
  - Hash the pack while writing it (removes one full read pass).
  - Hand the finished temp file to the local backend without a copy.
  - Store incompressible blobs uncompressed. Format v2 allows it, stock reads it, and `data_added_packed` is not in
    the oracle.
  - Stat once per file.
- **Ceiling.**
  - backup-initial −1.0 to −1.3 s, backup-unchanged and backup-mutated −0.3 to −0.5 s each.
  - −20 to −40 MiB per backup phase.
  - About **+6 to +9**.
- **Soundness.**
  - SOUND, including the verify step, which must stay (skipping it would weaken an integrity check).
  - Storing incompressible blobs uncompressed: RULING, because it changes packed bytes.

### E. Out of reach or closed by measurement

- **Binary file-backed pages** (about 22 MiB per process): these come from dependency inits and can't be reached
  without removing backends.
- **Process spawn:** 0.02 s per phase.
- **Local cache:** CLOSED (`--no-cache` measured −0.9).
- **GC percent and read-concurrency knobs:** trades only. Section 6 gives the exchange rate for owners who want to
  convert banked time into memory at the end of a season.
- **Removing verification** (`NoExtraVerify`), weakening blob or pack hashing, or changing chunk boundaries: forbidden.
  Tree IDs in the snapshot are compared.

## 8. Closed ground

`data/closed-ground.md` has 24 rows with evidence paths. In short:
- Archiver buffer-ownership variants: closed 6 ways (1 promoted).
- Presence caches and scanner merges: closed.
- Checker read-ahead shrink: phase-real, but not a whole-run win.
- Restore second-pass replay: reversed at 42 rounds (fast, but RSS-heavy).
- Index slab/bitset/HasPackBlob: flat.
- Prune header arithmetic: flat.

Never touched: KDF, lock, output path, ls, index loading, chunker, pack uploader, zstd use, restore passes'
parallelism, and connection/concurrency shape.

## 9. Data and binaries

- **Local.**
  - `data/main/summary.txt` and `summary.json`: all tables above.
  - `data/main/pprof/`: CPU flat/cum, alloc, heap-at-peak and MemStats for stock and S001, per phase.
  - `data/extra/summary/`, `data/extra2/summary/`, `data/extra2/artifacts/strace-*`.
  - `data/cpu-categories-{s001,stock}.json`, `data/kdfbench.txt`, `data/closed-ground.md`,
    `data/proto-summary.txt`.
- **Box.** `/opt/league/restic-costmap/`:
  - `bin/`: all binaries.
  - `runs/{main,extra,extra2,proto}/results.jsonl` and `artifacts/<run-id>/`: raw cpu.pprof, mem.pprof,
    peak-heap.pb.gz, marks.json, stderr gctraces and strace outputs.
  - `src/`: census and prototype source trees.
  - `tools/`: identical to `cost-map/tools/`.
