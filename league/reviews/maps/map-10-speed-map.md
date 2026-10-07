# Restic League V2: suite speed map (cost map 10, the S019 incumbent, four workloads)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-10-06 03:22Z onward on the league box, between S019 and S020. The mapped tree is `incumbents/S019` (S019's
promoted T2 M004). A hand merge onto this tree was being built at the same time (`merge-S019/`); the prototypes here
are aimed at ground that merge does not take, so the readings hold on either tree.

Instrument: the builders' own harness `research-tools/research_tools_v2.py` (`timeline`, `profile`, `bench`,
`oracle`), run from a job queue (`tools/worker.sh`), one job per research lane under the lane's lock (6-9, 12-15,
19-22, 42-45; performance governor = the judge's clocks). Builds and oracle checks on CPUs 2-5 under the shared lock.
Judge lanes untouched; league workspaces read only. 96 box jobs, 0 failed; 15 oracle checks, all pass; 14 prototype trees.
Log: `PROGRESS.md`. Raw: `data/raw/out/*.json`; tables `data/timelines.txt`, `data/profiles.txt`, `data/benches.txt`,
`data/link.txt`, `data/oracle.txt`, `data/distributions.txt`.

## 0. Headline

1. **Four workloads on the judge** (44-round promotion job, stock -> incumbent): main 22.24 -> **15.52 s** (+30.2%),
   big-index 32.34 -> **18.99** (+41.3%), large-files 16.32 -> **12.57** (+22.7%), remote 38.37 -> **31.27**
   (+18.4%). **1 s saved = +1.61 points on main, +1.32 on big-index, +1.99 on large-files, +0.80 on remote**
   (100 / total / 4). A constant in all 28 processes: 0.1 s = +4.0.
2. **Remote now sends stock's requests in every phase** (439 per pass against 438). Restore is 7.50 s where S018
   took 10.38: its file stage runs at 4.8 of 5 connections, 0.2 s above floor plus latency. **Fewer requests is
   finished ground. What is left on remote is order and overlap.**
3. **The lock wait is the largest cell nobody has taken: 1.28 s on main and 1.41 s on large-files per pass, +4.9
   suite points as a bound** (`p-nodwell`, 0.17 to 0.21 s in every one of 14 cells). Nothing on big-index: its index
   load fills the wait. **A first rough version for one command, check reading packs inside the wait, reads -0.12 s
   on main, -0.11 s on large-files and -0.17 s on remote with the oracle passing (`p-ckwait`).**
4. **The extra verify on save is still 0.45 to 0.60 s of backup-initial on every local workload** (`p-noverify`,
   +3.0 points as a bound). Four teams built four variants in S019; none took a visible part.
5. **A third file reader: +1.7 points as a bound** (`p-rc3`). **A third reader that takes only files up to 256 KiB
   keeps most of it on main and big-index** (`p-rc3s`: backup-initial -0.16 s on both at flat memory, remote -0.14
   s, +1.0 points) but backup-unchanged still rises 10 to 13 MiB: that memory is not chunk buffers.
6. **Check on remote still takes one of two times, and it is the order the packs are handed out in.** Largest
   first in a fixed order: 6.67 s in 12 of 12 runs against 6.68 to 7.02 (`p-cksort`, -0.22 s). As built it loses
   locally (it lists every pack before starting). Without the extra check worker on a five-connection backend:
   6.65 to 6.73 in 12 runs (`p-ckx`).
7. **Restore on remote is single-valued now**: 7.48 to 7.51 s in 12 runs, 89 requests every time.
8. **Remote backup-initial is 1.4 s above floor, latency and startup, and file readers are not the reason**: eight
   readers read -0.25 s at +65 MiB.
9. **big-index moved least in S019 and its check went backwards** (3.50 s against S018's 3.43). Its prune runs at
   1.6 to 1.7 of 4 cores for 2.6 s.
10. A third check and repack worker is still +1.45 points as a bound (`p-conn3`); a tree pack read in five ranges
    is -0.07 to -0.08 s per process on remote (`p-treepar`).

## 1. The four workloads on the judge (seconds stock -> incumbent; MiB = incumbent peak)

Medians of the S019 promotion job (`28102fc2...`, 44 rounds, 11 per workload), remote included.

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | 5.47 -> 4.86 (95) | 5.72 -> 4.57 (222) | 4.17 -> 3.49 (109) | 8.40 -> 7.80 (97) |
| backup-unchanged | 2.22 -> 1.70 (69) | 2.72 -> 1.80 (191) | 1.91 -> 1.36 (66) | 2.99 -> 2.31 (67) |
| backup-mutated | 2.33 -> 1.82 (82) | 2.81 -> 1.92 (208) | 2.46 -> 2.08 (91) | 3.24 -> 2.53 (81) |
| restore | 5.92 -> 3.28 (106) | 6.44 -> 3.37 (250) | 2.87 -> 2.53 (73) | 10.71 -> 7.51 (117) |
| check-read-data | 2.52 -> 1.65 (70) | 5.13 -> 3.50 (282) | 2.16 -> 1.53 (62) | 7.25 -> 6.78 (91) |
| forget-prune | 2.31 -> 1.32 (88) | 7.58 -> 2.86 (235) | 2.02 -> 1.15 (80) | 3.75 -> 3.09 (98) |
| ls-json | 1.47 -> 0.89 (57) | 1.94 -> 0.96 (181) | 0.73 -> 0.44 (57) | 2.04 -> 1.26 (58) |
| **total** | 22.24 -> **15.52** (+30.2%) | 32.34 -> **18.99** (+41.3%) | 16.32 -> **12.57** (+22.7%) | 38.37 -> **31.27** (+18.4%) |
| suite points per second (100 / total / 4) | 1.61 | 1.32 | 1.99 | 0.80 |
| memory guard (+2% of the 7 summed peaks) | 11.3 MiB | 31.4 MiB | 10.8 MiB | 12.2 MiB |

Against S018 in the same job (seconds, negative = faster): main -0.25 (bi -0.09, bu -0.07, bm -0.05); big-index
-0.16 (bi -0.10, bu -0.07, bm -0.05, **check +0.065**); large-files -0.70 (bi -0.16, bu -0.33, bm -0.25); remote
-3.35 (restore -3.02, forget-prune -0.11, backup-unchanged -0.10, backup-mutated -0.09, ls-json -0.07).

**Share of the suite score per cell** (cell seconds x points per second = points if the cell took no time; each
workload sums to 25):

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | 7.8 | 6.0 | 6.9 | 6.2 |
| backup-unchanged | 2.7 | 2.4 | 2.7 | 1.8 |
| backup-mutated | 2.9 | 2.5 | 4.1 | 2.0 |
| restore | 5.3 | 4.4 | 5.0 | 6.0 |
| check-read-data | 2.7 | 4.6 | 3.0 | 5.4 |
| forget-prune | 2.1 | 3.8 | 2.3 | 2.5 |
| ls-json | 1.4 | 1.3 | 0.9 | 1.0 |

Per process on every workload: KDF 0.20 s at 4 cores (stock 0.48 s at 1 core) + lock wait 0.20 s. KDF is closed; the
wait may only be overlapped.

## 2. Stage timelines, local workloads (incumbent; wall s at cores used; stock in brackets; medians of 3)

| stage | main | big-index | large-files |
|---|---|---|---|
| index load | 0.027, inside the wait | 0.26-0.28: 0.20 in the wait at 3.2 cores + **0.06-0.08 after it** (stock 0.50-0.53 at 2.9) | 0.004 |
| lock wait idle (no CPU) | bi 0.199; others 0.173; check 0.18: **1.24 s per pass** | 0 | 0.196-0.199 x 7: **1.38 s per pass** |
| bi snapshot | 4.55 at 3.47 (4.83 at 3.61) | 4.23 at 3.49 (4.68) | 3.10 at 3.52 (3.57 at 3.54) |
| bu snapshot | 1.30 at 1.98 (1.50) | 1.33 at 1.92 (1.51) | **0.99 at 2.24 (1.24)** |
| bm snapshot | 1.40 at 2.19 (1.62) | 1.44 at 2.09 (1.61) | **1.68 at 3.32 (1.79)** |
| restore pass 1 (trees, dirs) | 0.40 at 1.25 (1.31) | 0.39 at 1.13 (1.31) | 0.02 |
| restore files | 2.11 at 2.57 (2.65 at 2.11) | 2.05 at 2.42 (2.70) | **2.28 at 1.75 (2.26 at 1.70)** |
| restore pass 2 (metadata) | **0.46 at 3.04** (1.38) | **0.46 at 3.01** (1.38) | 0.02 |
| check after the lock | 1.27 at 2.85 (stock 1.87) | 3.19 at 2.84 (stock 4.47) | 1.09 at 2.59 (stock 1.47) |
| prune plan | 0.12 at 3.2 (0.18) | **1.07 at 1.70** (3.32) | 0.01 |
| prune execute | 0.78 at 2.26 (1.42) | **1.53 at 1.61** (3.25) | 0.77 at 2.18 (1.45) |
| ls walk | 0.49 at 1.17 (0.75) | 0.49 at 1.06 (0.76) | 0.04 |

What changed since map 9 (S018):
- **large-files' repeat backups are now faster than stock in the snapshot stage** (bu 0.99 against 1.24 s, bm 1.68
  against 1.79): the scanner and the overlapped hashing took 0.33 and 0.25 s.
- **large-files restore has still not moved**: 2.28 s at 1.75 cores (stock 2.26), 5.1 idle core-s. The hand merge is
  building the third ordinary worker there.
- **Restore's metadata pass is 0.46 s on main and big-index (and 0.47 s on remote) and starts only when the last
  file is written.** The files stage before it leaves 3.0 core-s idle on main, 3.3 on big-index, and runs at 0.95
  cores on remote. The pass needs 1.4 CPU-s.
- **big-index prune is 2.6 s at 1.6 to 1.7 cores** (plan 2.5 idle core-s, execute 3.7).
- backup-initial runs at 3.5 of 4 cores on every local workload: 2.4 idle core-s on main, 1.5 on large-files.

## 3. CPU and heap at the peak (one profile per cell; `data/profiles.txt`)

| cell | CPU-s (stock) at cores | where |
|---|---|---|
| main bi | 16.6 (18.4) at 3.34 | syscalls 3.49, sha256 2.42, zstd compress 2.4, chunk scanner 1.09 (stock's 1.35), poly1305 0.76, memmove 0.76, KDF 0.8. By owner: `saveAndEncrypt` 8.42, of it **`verifyCiphertext` 2.36** (the extra verify on save: decrypt, decompress, hash); file readers 3.15; pack writes 1.10 |
| big-index bi | 16.4 (18.8) at 3.49 | the same shape: syscalls 3.48, sha256 2.44, `verifyCiphertext` 2.53, readers 3.10 |
| large-files bi | 12.0 (13.4) at 3.28 | sha256 2.36, syscalls 1.69, scanner 1.25, zstd compress 1.6, `verifyCiphertext` 1.99; file readers 2.53 |
| large-files bu | 3.0 (3.2) at 2.15 | scanner 1.06, sha256 0.65, KDF 0.8; all of it in `parentCutReader.read` on two readers |
| large-files bm | 6.4 (6.5) at 3.05 | sha256 1.28, scanner 0.92, zstd compress 0.94, `verifyCiphertext` 0.88 |
| large-files restore | 4.9 (4.5) at 1.81 | sha256 0.97, syscalls 0.86, zstd decode 0.45, poly1305 0.27, KDF 0.8 |
| main restore | 8.2 (8.9) at 2.42 | syscalls 2.79 (90,000 files: open, chown, chmod, utimens, pwrite), sha256 0.94, zstd 0.51; pack streams 4.4 |
| main check | 4.5 (4.6) at 2.69 | sha256 1.48, zstd 0.52, poly1305 0.32, KDF 0.8 |
| big-index check | 10.7 (11.6) at 2.96 | sha256 1.97, zstd 0.63, memmove 0.53, memeq 0.47, poly1305 0.44, index `valuesWithID` 0.42 |
| big-index prune | 5.8 (11.4) at 1.91 | tree loads 1.37 (parallel), index `Values` 0.73, `blobIndex` 0.50, `firstIndex` 0.47, `PlanPrune` 0.35, KDF 0.8: the index walks are single loops |
| remote bi | 16.7 (18.1) at 2.15 | main's profile at two thirds of the cores: the link sets the pace |
| remote restore, check, prune | 8.3 at 1.1 / 4.9 at 0.73 / 2.9 at 0.86 | link-bound; restore's metadata pass alone runs at 3.0 cores |

| workload | where the peak falls | owners (MiB in use at the peak) |
|---|---|---|
| main bi | 1.74 s | chunk assembly `readNextChunkHashed` 8.1, `saveAndEncrypt` 6.8, zstd `DecodeAll` 6.2 (the verify), zstd encoder 5.5; 49 in use, RSS 98 (stock 206) |
| main restore | 1.32 s | zstd `DecodeAll` 12.7, range windows `streamPackPartWithBuffer` 11.9, file list 9.5 + 4.5; 58 in use, RSS 111 |
| main check | 0.91 s | pack reader 7.7, index 4.6, zstd history 4.3, read buffers 3.0; RSS 78 |
| large-files restore | 1.43 s | range windows 15.9, zstd `DecodeAll` 9.8; RSS 70 |
| large-files bi / bu / bm | 1.77 / 0.66 / 1.73 s | chunk assembly 17.3 / 8.1 / 4.9, zstd encoder 5.5 / 2.7 / 5.5; RSS 111 / 65 / 95 |
| big-index, all phases | at or after the index load | the index tables `hashedArrayTree.grow` 63-88 (+ 7-25 preallocated); the pack listing 18.4 in check; RSS 177-287 |

## 4. Prototypes (builders' `bench` vs `incumbents/S019`, research lanes; 5 fixed pairs locally, 10 or 12 on remote; diffs in `data/protos/`)

Seconds are paired medians, negative = faster, with wins of pairs. MiB = change of the phase peak. BOUND = not
allowed as built (an operator ruling, a weakened check, or an unrestricted knob); it sizes what is there. ROUGH =
inside the rules as far as built, not a candidate. TEST = built to find a cause. Oracle: the builders' `oracle` op on
the phases touched (`data/oracle.txt`).

| prototype | edit | main | big-index | large-files | remote | oracle |
|---|---|---|---|---|---|---|
| `p-nodwell` BOUND | the lock wait cut to nothing (operator ruling 1: CLOSED). Sizes what perfect overlap could give | bi -0.213, bu -0.187, bm -0.178, rs -0.171, ck -0.187, fp -0.172, ls -0.172 = **-1.28 s** (35/35) | bu +0.010, rs +0.006, ls -0.005: **null** | bi -0.208, bu -0.190, bm -0.214, rs -0.212, ck -0.202, fp -0.188, ls -0.197 = **-1.41 s** (35/35) | bu -0.098, rs -0.088, fp -0.078, ls -0.066 (5/5 each); bm -0.026, ck -0.019 (unresolved); bi not run | pass (main) |
| **`p-ckwait` ROUGH** | check `--read-data` starts reading packs inside the lock wait; a pack that verifies clean is skipped by the ordinary pass, a pack with any error is read again and reported as before; nothing is printed early (`repository/cm10_check_preread.go`, 80 lines, and 4 hooks) | ck **-0.119 (5/5), -2.0 MiB** | ck **+0.120 (0/5), +11.8 MiB** | ck **-0.113 (5/5), +1.5 MiB** | ck **-0.173 (11/12), -1.9 MiB** | pass on all four (the corrupted pack is detected) |
| `p-noverify` BOUND | the extra verify on save switched off (an integrity check: CLOSED) | bi **-0.600**, bm -0.021 | bi **-0.561** | bi **-0.454**, bm -0.169 | | pass |
| `p-rc3` BOUND | `ReadConcurrency` 3 (a third file reader, unrestricted) | bi -0.192, bu -0.100, bm -0.068; +14.3 / +14.3 / +10.2 MiB | bi -0.150, bu -0.105, bm -0.081; +13.4 / +11.1 / +8.1 | bi -0.034 (3/5), **bu -0.299**, bm +0.001; +6.1 / +12.1 / +2.1 | bi -0.082 (9/10), +9.1 MiB | pass |
| `p-rc4` / `p-rc8` BOUND | `ReadConcurrency` 4 / 8, remote backup-initial only | | | | -0.211 (10/10), +19.5 MiB / **-0.250 (10/10), +65.2 MiB** | |
| **`p-rc3s` ROUGH** | a third reader that takes only files up to 256 KiB (`archiver/file_saver.go`, 30 lines) | bi **-0.159, -0.2 MiB**; bu -0.106, **+13.3 MiB**; bm -0.039, +4.9 | bi **-0.155, +0.7 MiB**; bu -0.107, **+9.6**; bm -0.023 (unresolved), -3.8 | bi -0.003, bu -0.030, bm -0.013 (null); +7.3 / +11.1 / -1.1 | bi **-0.144 (9/10), +1.9 MiB**; bm -0.065 (9/10), +4.8 | pass (main, large-files) |
| `p-rc3s2` ROUGH | the same with the limit at 2 MiB | bi -0.183, +7.2 MiB; bu -0.096, +15.4; bm -0.039, +4.8 | | null; bu +9.8 MiB | | |
| `p-conn3` BOUND | local `Connections` 3: an unrestricted third check and repack worker (restore: see the hand merge) | ck -0.067, fp -0.130; +6.9 / +9.3 MiB | ck -0.197, fp -0.140; +9.6 / +7.1 | ck -0.123, fp -0.224; +7.5 / +13.0 | | pass (big-index) |
| `p-cksort` TEST | check lists every pack first, then hands them out largest first in a fixed order | ck **+0.182 (0/5)**, +5.4 MiB | ck **+0.845 (0/5), +130 MiB** | ck +0.140 (0/5) | ck **-0.223 (12/12)**, +4.0 MiB; **6.66-6.68 s in 12 runs** (incumbent 6.68-7.02) | pass (main, remote) |
| `p-ckx` TEST | the extra check worker takes nothing when the backend has more than two connections | unchanged by construction | unchanged | unchanged | ck -0.048 (9/12), -6.3 MiB; **6.65-6.73 s in 12 runs** (incumbent in the same job 6.63-6.92) | |
| `p-cklast` TEST | packs under 8 MiB held back and handed out last; full packs as listed | ck +0.036 (1/5) | ck -0.103 (4/5, unresolved) | ck +0.002 | ck -0.027 (8/12): **still two values**, 6.66-6.99 | |
| `p-treepar` ROUGH | a pack the cache fetches whole (the tree pack) read in five parallel ranges after one size request (`backend/cache/backend.go`, 45 lines) | not active (two connections) | not active | not active | bu **-0.071 (10/10)**, ls **-0.074 (10/10)**, rs **-0.081 (10/10)**, ck -0.076 (8/10), bm -0.028 (7/10), fp +0.026 (2/10); 5 more requests per process; memory flat | pass |

Notes.
- **The lock wait.** Every cell on main and large-files gives 0.17 to 0.21 s when the wait is cut: the processes do
  nothing with it beyond a 0.03 s index load (0.004 s on large-files). On big-index the bound is null: the index
  load (0.26-0.28 s at 3.2 cores) fills the wait and runs 0.06-0.08 s past it. Read-only work that could start
  inside it, by command: a backup's source scan, reads, chunking, hashing and compression into packs whose uploads
  wait; restore's tree load, plan and first pack reads; check's pack reads with results held; prune's plan.
- **Check is the first command with a rough version (`p-ckwait`): -0.12 s on main, -0.11 s on large-files, -0.17 s
  on remote, memory flat, oracle passing.** That is 0.12 of the 0.19 to 0.20 s the bound shows in the check cell.
  By construction the pre-read also runs through what the process does between the wait and its ordinary pack pass.
  **On big-index it loses 0.12 s and 11.8 MiB as built**: the wait is already full there, and the rough version
  lists the packs of the large index a second time and keeps reading beside the structure pass. A candidate stops
  at the confirmed check or shares one listing. The other four commands have no rough version: each needs its flow
  after the lock to start before it.
- **The extra verify on save has not moved.** Verify off reads -0.60 / -0.56 / -0.45 s of backup-initial (map 9 on
  S018: -0.53 / -0.51 / -0.52). backup-initial runs at 3.5 of 4 cores, so there is half a core to overlap it with,
  not two and a half: this bound is CPU that has to be removed, not moved.
- **The third file reader.** On main and big-index the gain is in the 90,000 small files: a reader that takes only
  files up to 256 KiB keeps 0.16 of the 0.19 s in backup-initial at flat memory, and reads -0.14 s on remote
  backup-initial. On large-files the gain is in backup-unchanged alone (-0.30 s) and belongs to the large files.
  - **backup-unchanged rises 10 to 13 MiB with any third reader, small files only or not.** `profile` of `p-rc3s` on
    main at the peak: heap in use 52 MiB against 36, a zstd encoder 5.5 MiB that the incumbent does not hold at that
    moment, one more pool buffer 2.0, one more read buffer 0.8, 54 collections against 60. Chunk buffers are equal
    (9.8 against 10.6 MiB). The cost is what is alive at the peak moment and collection pacing, not blob size.
  - A 2 MiB limit gives nothing more than 256 KiB and costs +7 MiB in backup-initial.
- **A third check and repack worker** is worth what it was (check -0.07 / -0.20 / -0.12, prune -0.13 / -0.14 /
  -0.22 s) and costs 7 to 13 MiB per phase unrestricted.
- **Check on remote: the two values are the order.** The incumbent's check reads 6.62 to 6.79 s in some runs and
  6.84 to 7.02 in others on the same state, same requests, same bytes (48 runs of the incumbent across four jobs;
  the slow share was 3 of 12 in one job and 11 of 12 in another). The packs are listed from a map, so their order
  differs from run to run.
  - Largest first in a fixed order: one value, the fast one, 12 of 12. But the rough version lists every pack before
    the first read starts and holds every blob list: +0.14 to +0.85 s and up to +130 MiB locally. **Closed as built.**
  - Holding only the small packs back does not remove it: the order of the full packs matters.
  - With the extra check worker switched off on a backend that already has five connections, 12 runs read 6.65 to
    6.73 s and the peak falls 6 MiB. The extra worker has a slot of its own only on the local backend
    (`sema.WithAdditionalLoadSlot`); on any other backend it joins the ordinary semaphore, so on remote six workers
    share five connections.
- **The tree pack on remote.** backup-unchanged, backup-mutated, ls-json, restore, check and forget-prune fetch one
  6.3 MiB pack in one request (0.16 s) while four connections idle. Five ranges after a size request: -0.07 to
  -0.08 s in the four phases where it is on the critical path. The size request costs 0.03 s of that; the index
  already knows the pack's size. forget-prune fetches two such packs and reads +0.03 s (inside the band).

## 5. Remaining levers, ranked by suite points (1 s = 1.61 main / 1.32 big-index / 1.99 large-files / 0.80 remote)

| # | lever | measured or bounded | suite points | memory |
|---|---|---|---|---|
| 1 | **Use the lock wait** (read-only work started inside it; writes and output after the confirmed check) | bound -1.28 s main, -1.41 s large-files, null on big-index, -0.33 s on remote in four phases (`p-nodwell`); check alone, rough: -0.119 main, -0.113 large-files, -0.173 remote (`p-ckwait`) | **+4.9 bound on two workloads** (+0.3 more on remote); **+0.55 measured for check**; the other four commands have no rough version | bounded buffers |
| 2 | **The extra verify on save**: the same check with less CPU | bound bi -0.60 / -0.56 / -0.45, large-files bm -0.17 (`p-noverify`) | +3.0 bound (closed as built); four S019 variants took none of it | lower |
| 3 | **A third file reader inside a bound** | bound -0.36 s main, -0.34 big-index, -0.30 large-files bu, remote bi -0.08 (`p-rc3`); small files only: main -0.30, big-index -0.26, remote -0.21 (`p-rc3s`) | **+1.7 bound; +1.0 measured** | bound: +10 to +14 MiB per phase; small files only: flat in bi, **+10 to +13 in bu** |
| 4 | **Restore's metadata pass during the files stage** | stage 0.46 s main, 0.46 big-index, 0.47 remote; 3.0 / 3.3 idle core-s before it, 0.95 cores used on remote | ceiling +1.7; no prototype | the nodes of files not yet settled |
| 5 | **A third check and repack worker inside a bound** | bound check -0.07 / -0.20 / -0.12, prune -0.13 / -0.14 / -0.22 (`p-conn3`) | +1.45 bound | +7 to +13 MiB per phase unrestricted |
| 6 | **backup-initial on remote** | 7.80 s against floor 5.20 + latency 0.47 + startup and exit 0.67 = 6.34; readers are not it (`p-rc8` -0.25 s) | ceiling +1.1; unprototyped | |
| 7 | big-index: prune at 1.6-1.7 cores, check behind S018, index tail | plan 1.07 s, execute 1.53 s; check +0.065 s vs S018; tail 0.06-0.08 s x 7 | tail +0.6; prune and check unmeasured | lower |
| 8 | forget-prune on remote | execute 2.33 s, 94 requests, 272 MiB: 1.76 s at five busy connections | ceiling +0.45; unprototyped | |
| 9 | The tree pack in parallel ranges on remote | -0.30 s per pass over four phases (`p-treepar`) | **+0.24 measured**, about +0.35 without the size request | flat |
| 10 | Check on remote: the order of the packs | -0.22 s in the slow runs (`p-cksort`, `p-ckx`) | +0.1 to +0.2, and it removes a 0.3 s coin from every remote row | `p-ckx` -6 MiB |
| - | A change anywhere moves the key derivation | found by the hand merge (`merge-S019/PROGRESS.md`, 03:50Z): the derivation read 0.208 s against 0.202 s in a tree that changed only the restorer; binary layout | about 6 ms per process either way = 0.17 s per workload: check `open:backend-opened -> open:key-done` before believing a small gain in every phase | |
| 11 | What a process sends before work on remote | 0.62 s; reachable about 0.56 | +0.3 | flat |

## 6. The remote workload (`incumbents/S019`; `data/link.txt`)

**Per phase, stock -> incumbent** (`bench`, 3 pairs on a research lane; requests and MiB are the same in every run):

| phase | seconds | peak MiB | requests | MiB up / down | floor s | above the floor (incumbent, judge seconds) |
|---|---|---|---|---|---|---|
| backup-initial | 8.48 -> 7.81 | 197 -> 95 | 88 -> 89 | 1,248 / 0 | 5.20 | 2.60 s |
| backup-unchanged | 3.02 -> 2.33 | 143 -> 67 | 17 -> 17 | 6.1 / 9.9 | 0.07 | 2.24 s |
| backup-mutated | 3.32 -> 2.62 | 184 -> 81 | 23 -> 23 | 93.5 / 9.9 | 0.43 | 2.10 s |
| restore | 10.76 -> 7.50 | 292 -> 118 | 89 -> **89** | 0 / 1,245 | 5.19 | 2.32 s |
| check-read-data | 7.25 -> 6.73 | 145 -> 90 | 90 -> 90 | 0 / 1,248 | 5.20 | 1.58 s |
| forget-prune | 4.09 -> 3.34 | 142 -> 103 | 117 -> **117** | 135 / 153 | 1.20 | 1.89 s |
| ls-json | 2.05 -> 1.26 | 68 -> 58 | 14 -> 14 | 0 / 9.9 | 0.04 | 1.22 s |
| **one pass** | **38.96 -> 31.60** | 1,171 -> 612 | **438 -> 439** | 1,483 / 2,674 | **17.33** | 13.95 s |

- **The floor is 55% of the incumbent's pass** (17.3 of 31.3 s on the judge).
- **The incumbent sends stock's requests in every phase** (one more `POST data` in backup-initial). S018 sent 795.

**Stage timeline** (`timeline`, 2 runs per arm; wall s; requests that arrived in the stage; "busy" = connections
occupied on average, of 5, latency included):

| stage | incumbent | stock |
|---|---|---|
| before work, every process | **0.62 s, 12 requests**: key 0.29 (`HEAD config`, `GET keys/`, `GET keys`, `GET config`; derivation 0.20 at 2.7 cores); 0.09 (`GET locks/`, `POST locks`, `GET snapshots/` one after another); index 0.14 (`GET index/`, two `GET index`, 3.5 MiB) inside the wait; 0.06 rest of the wait with `GET locks/`; `GET snapshots` 0.03. Local: 0.40 s | 1.11 s |
| backup-initial snapshot | **7.09 s at 2.18 cores, 78 requests, 1,248 MiB, busy 4.00** (floor 5.20 + 0.47 of latency) | 7.37 s, busy 3.85 |
| backup-unchanged snapshot | 1.64 s (local 1.30): `GET data` 6.3 MiB (the tree pack, 0.16 s), then `POST data` 6.0 MiB (0.16 s), `POST index`, `POST snapshots` one after another | 1.85 s |
| backup-mutated snapshot | 1.90 s (local 1.40), 10 requests, 99.8 MiB, busy 1.26 | 2.11 s |
| restore pass 1 (trees) | 0.56 s, 1 request, 6.3 MiB (local 0.40) | 1.47 s |
| **restore files** | **5.79 s at 0.95 cores, 75 requests, 1,236 MiB, busy 4.83** (floor 5.15 + 0.45) | 6.75 s, busy 4.15 |
| restore pass 2 (metadata) | **0.47 s at 3.0 cores, no requests** | 1.40 s |
| check after the lock | 6.06 s at 0.65 cores, 79 requests, 1,245 MiB, busy 4.67 | 6.38 s |
| prune plan / execute | 0.37 s, 5 requests / **2.33 s at 0.69 cores, 94 requests (38 deletes), 272 MiB, busy 3.64** | 0.42 / 2.47 s |
| ls walk | 0.67 s, 2 requests, 6.3 MiB (local 0.49) | 0.93 s |
| exit | 0.05 s (`DELETE locks`) | 0.05 s |

**Run-to-run on the same state** (`data/distributions.txt`; seconds of the incumbent, sorted):

| phase | runs | values |
|---|---|---|
| restore | 12 | 7.48 to 7.51: **one value**; 89 requests every time (S018 in the same job: 10.33-10.40, 430) |
| check-read-data | 48 | **two values**: 6.62-6.79 and 6.84-7.02 |
| backup-initial | 20 | 7.70 to 7.92, spread evenly |
| backup-mutated | 10 | 2.45 to 2.61, spread evenly |
| forget-prune | 10 | 3.29 to 3.43, spread evenly |
| backup-unchanged, ls-json | 10 each | within 0.03 s |

Notes.
- **Restore on remote: 7.50 s = 0.62 before work + 0.56 trees + 5.79 files + 0.47 metadata + 0.05 exit.** The files
  stage is 0.2 s above its floor plus latency. What is left: the tree stage (0.16 s of it is the tree pack on one
  connection), the metadata pass (no requests, three idle cores before it), and the lock release at the very end
  although no repository read follows the files stage.
- **backup-initial on remote: 7.09 s of snapshot stage against 5.67 at five busy connections.** The corpus is read
  in directory order: 6 large files (770 MiB), 161 medium files (347 MiB), then 90,000 small files (726 MiB). CPU
  is 2.2 of 4 cores. More readers move it little: three -0.08 s, eight -0.25 s at +65 MiB. Both runs spread evenly
  over 0.2 s; there is no second value.
- **Repeat backups on remote are their local time plus four requests in turn**: the tree pack down (0.16 s) before
  the walk can start, the new tree pack up (0.16 s), then the index, then the snapshot. backup-mutated also ends on
  its last data pack: seven uploads of about 13 MiB, each 0.31 s on one connection.
- **forget-prune on remote: execute 2.33 s for work that fills five connections in 1.76 s** (272 MiB / 240 + 94
  requests x 30 ms / 5). 31 pack deletes, 5 index deletes and 2 snapshot deletes; 48 `GET data`; 9 `POST data`.
- **What every process sends before work is 0.62 s against 0.40 s locally.** In order it cannot go under about 0.56:
  two requests before the key derivation (0.06), the derivation (0.20, closed), the lock list and the lock write
  (0.06), the wait (0.20, which holds the snapshot list and the index), the second lock list (0.03).

## 7. What S019's teams built that the incumbent lacks (`data/closed-ground.md` section R; rows `data/s019-rows.txt`)

| team | mechanism | judged (M004 unless marked; seconds vs S018) | status |
|---|---|---|---|
| T4 | restore dispatch look-ahead, one-blob files without the second index lookup (`restorer/pack_dispatch.go`, `planned_singleton.go`, `restore_offsets.go`) | restore main -0.075 to -0.135, big-index -0.135 to -0.182 in three rows | in the hand merge |
| T4 | ls node decode in batches on a helper goroutine (`walker/listing_*.go`) | ls main -0.042, big-index -0.038, remote -0.081 | in the hand merge |
| T3 | restore decode output carried across the windows of one response (`restore_span_scratch.go`, `restore_decode_reserve.go`) | large-files restore -0.100, bi -0.095 | in the hand merge (memory for the third restore worker) |
| T1 | bounded restore worker extended to compressed packs (`compressed_restore*.go`) | large-files restore -0.094 | subsumed by the third ordinary restore worker in the hand merge |
| T4 | index decoder recycler, prepared cipher schedules (`index_decoder_recycler.go`, `crypto/cipher_preparation.go`) | big-index check -0.096 and -0.101 in two rows (the incumbent: +0.065 at 11 rounds) | **open** |
| T4 | repack with a successor read and a retained tail (`repack.go`, `repack_tail.go`) | large-files prune +3.7% in the team's ablation; big-index prune +0.126 s; main memory +2.9% in a probe | **open with a memory bound** |
| T3 | repack through larger requests (`repack_pack_stream.go`) | remote prune -0.172 (the incumbent -0.212) | inside the noise |
| T1, T3, T4 | save verify variants; startup request variants; chunk scanners; second hash slot | the incumbent's forms are equal or ahead in every phase | closed |

## 8. Data, binaries, tools

- **Local** `cost-map-10/`: `SPEED-MAP.md`, `PROGRESS.md`, `data/` (`timelines.txt`, `profiles.txt`, `benches.txt`,
  `distributions.txt`, `link.txt`, `oracle.txt`, `raw/out/*.json`, `raw/PROGRESS.log`, `protos/*.diff`,
  `s019-rows.txt`, `s019-table.txt`, `s019-promotion.txt`, `s019/diffs/*.diff`, `inc-src/` = the incumbent's
  non-test Go, `closed-ground.md`), `tools/`.
- **Box** `/opt/league/restic-costmap10/`: `src/p-*` (prototype trees), `out/`, `logs/`, `diffs/`, `tools/`.
- **Tools** `cost-map-10/tools/`: `box.py` (set `MSYS_NO_PATHCONV=1` under Git Bash; commands with quotes go
  through `shf`), `worker.sh`, `genjobs.py` (`map`, `bench`, `one`), `proto_edit10.py` and `proto_extra10.py`
  (every prototype named above), `build_check10.sh`, `oracle10.sh`, `summ.py`, `bn.py` (compact bench table),
  `dist.py` (per-run values), `link.py`, `s019_scan.py`, `promo.py`, `rows_table.py`.
