# Restic League V2: suite speed map (cost map 9, the S018 incumbent, four workloads)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-10-05 04:18Z onward on the league box, between S018 and S019. The mapped tree is `incumbents/S018` (S018's
promoted T1 M004). **This is the first map of four workloads**: main, big-index, large-files and the new `remote`
(main's corpus behind a simulated link). Maps 1-7 measured main only, map 8 three workloads.

Instrument: the builders' own harness `research-tools/research_tools_v2.py` (`timeline`, `profile`, `bench`,
`oracle`), run from a job queue (`tools/worker.sh`), one job per research lane under the lane's lock (6-9, 12-15,
19-22, 42-45; performance governor = the judge's clocks). Builds and oracle checks on CPUs 2-5 under the shared lock.
Judge lanes untouched; league workspaces read only. 117 box jobs, 0 failed; 28 oracle checks. Log: `PROGRESS.md`. Raw: `data/raw/out/*.json`; tables
`data/timelines.txt`, `data/profiles.txt`, `data/benches.txt`, `data/link.txt`, `data/oracle.txt`.
The remote workload was measured after its states were in place (05:14Z), on the same lanes.

## 0. Headline

1. **Four workloads on the judge's clocks** (stock -> incumbent): main 22.30 -> **15.78 s** (+29.3%), big-index
   32.56 -> **19.36** (+40.6%), large-files 16.37 -> **13.34** (+18.5%), remote 38.74 -> **34.88** (+9.95%).
   **1 s saved = +1.58 points on main, +1.29 on big-index, +1.87 on large-files, +0.72 on remote** (100 / total / 4).
   A constant in all 28 processes: 0.1 s = +3.8.
2. **Remote restore is one constant away from stock's request count.** The tree reads every pack in ranges of at most
   4 MiB, each its own backend request: 430 requests against stock's 89, 12.9 connection-seconds of latency against
   2.7. Its file stage takes 8.62 s; stock's takes 6.74 s.
   - **Prototype `p-onereq` (one request per stock-sized range, the 4 MiB ranges read as windows of that response):
     restore 10.36 -> 7.64 s, -2.73 s, 5/5, requests 430 -> 89, memory flat, null on the three local workloads, oracle
     passes on all four. +2.0 suite points from one new file.**
   - Stock's own range size as a constant (`p-range32`, a bound) gains 1.63 s and costs +133 MiB.
3. **A third ordinary restore worker is worth 0.42-0.50 s on every local workload; its cost is blob-sized buffers.**
   - **`p-rs3b3` (third worker on the additional load slot, 2 MiB windows from one request, at most two workers at a
     time on packs with a blob above 3 MiB): restore -0.208 s main, -0.323 s big-index, -0.314 s large-files, -2.82 s
     remote, at +4.6 / +14.4 / -2.5 / -9.3 MiB. +3.4 suite points in one phase, inside every guard as built.**
4. **Remote, the rest:** every process sends 12 requests one after another before it starts work (0.65 s; 0.40 s
   locally). Overlapping the first three (`p-ctl`) is -0.06 s per process. backup-initial runs at 3.9 of 5
   connections, 1.6 s above its floor plus latency. Half of the incumbent's pass is the byte floor (17.3 of 34.9 s).
5. **A third file reader gives nearly all of a fourth's gain** (`p-rc3`: -0.45 s on main and -0.64 s on large-files
   over the three backups; large-files backup-unchanged alone -0.49 s) for +12 to +15 MiB per phase.
6. **The extra verify on save is 0.52 s of backup-initial on every local workload** (bound; removing it is closed).
7. **large-files:** repeat backups are still slower than stock in the snapshot stage (two readers); never re-reading
   on a ctime change is still -1.27 s (bound); sparse restore's in-rules form is 0.06 s, not 0.46.
8. **S018's unpromoted work, measured against the incumbent:** T2's boundary scan and T3's cold-parent reader
   -0.12 to -0.15 s on large-files backup-unchanged; T4's restore prepare-ahead -0.05 to -0.08 s; every other team's
   check is slower than the incumbent's.

## 1. The four workloads on the judge (seconds stock -> incumbent; MiB = incumbent peak)

main, big-index and large-files: medians of the S018 promotion job (`821fc91a...`, 42 rounds, 14 per workload).
remote: the builders' `bench` on a research lane at the judge's governor, 3 pairs (the official reference job on this
workload was still running when this was written).

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | 5.49 -> 4.98 (92) | 5.73 -> 4.67 (222) | 4.18 -> 3.68 (107) | 8.40 -> 7.84 (98) |
| backup-unchanged | 2.22 -> 1.78 (69) | 2.71 -> 1.89 (191) | 1.92 -> 1.71 (67) | 3.01 -> 2.42 (68) |
| backup-mutated | 2.35 -> 1.88 (81) | 2.82 -> 1.97 (213) | 2.45 -> 2.33 (92) | 3.24 -> 2.69 (80) |
| restore | 6.00 -> 3.31 (111) | 6.49 -> 3.34 (260) | 2.88 -> 2.51 (69) | 10.77 -> 10.34 (129) |
| check-read-data | 2.52 -> 1.65 (70) | 5.10 -> 3.53 (274) | 2.15 -> 1.51 (59) | 7.27 -> 6.75 (88) |
| forget-prune | 2.25 -> 1.29 (91) | 7.74 -> 2.99 (235) | 2.07 -> 1.16 (80) | 4.00 -> 3.52 (102) |
| ls-json | 1.47 -> 0.90 (57) | 1.95 -> 0.97 (177) | 0.73 -> 0.44 (57) | 2.05 -> 1.33 (57) |
| **total** | 22.30 -> **15.78** (+29.3%) | 32.56 -> **19.36** (+40.6%) | 16.37 -> **13.34** (+18.5%) | 38.74 -> **34.88** (+9.95%) |
| suite points per second (100 / total / 4) | 1.58 | 1.29 | 1.87 | 0.72 |
| memory guard (+2% of the 7 summed peaks) | 11.4 MiB | **31.5 MiB** (was 60) | 10.6 MiB | 12.5 MiB |

**Share of the suite score per cell** (cell seconds x points per second = points if the cell took no time; each
workload sums to 25):

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | 7.9 | 6.0 | 6.9 | 5.6 |
| backup-unchanged | 2.8 | 2.4 | 3.2 | 1.7 |
| backup-mutated | 3.0 | 2.5 | 4.4 | 1.9 |
| restore | 5.2 | 4.3 | 4.7 | 7.4 |
| check-read-data | 2.6 | 4.6 | 2.8 | 4.8 |
| forget-prune | 2.0 | 3.9 | 2.2 | 2.5 |
| ls-json | 1.4 | 1.3 | 0.8 | 1.0 |

Per process on every workload: KDF 0.20 s at 4 cores (stock 0.48 s at 1 core) + lock dwell 0.20 s. KDF is closed;
the dwell may only be overlapped.

## 2. Stage timelines, local workloads (incumbent; wall s at cores used; stock in brackets; medians of 3)

| stage | main | big-index | large-files |
|---|---|---|---|
| index load | 0.027, inside the dwell | 0.26-0.29: 0.20 in the dwell at 3.2 cores + **0.06-0.09 after it** (S017-merge: 0.15; stock 0.52 at 2.8) | 0.004 |
| dwell idle (no CPU) | bi 0.199; others 0.173; check about 0.12 | 0 | 0.196 x 6, bi 0.199 |
| bi snapshot | 4.59 at 3.51 (4.82 at 3.61) | 4.24 at 3.53 (4.60) | 3.25 at 3.50 (3.51 at 3.56) |
| bu snapshot | 1.35 at 2.00 (1.48) | 1.39 at 1.93 (1.52) | **1.32 at 1.94 (1.23 at 2.15)** |
| bm snapshot | 1.45 at 2.20 (1.64) | 1.48 at 2.12 (1.62) | **1.93 at 3.05 (1.81 at 3.27)** |
| restore pass 1 (trees, dirs) | 0.40 at 1.25 (1.33) | 0.39 at 1.17 (1.33) | 0.02 |
| restore files | 2.07 at 2.57 (2.58 at 2.11) | 2.09 at 2.37 (2.66) | **2.14 at 1.76 (2.09 at 1.75)** |
| restore pass 2 (metadata) | 0.46 at 3.04 (1.37) | 0.45 at 3.02 (1.39) | 0.02 |
| check after the lock | 1.31 at 2.76 (stock 1.84) | 3.16 at 2.91 (stock 4.42) | 1.19 at 2.36 (stock 1.48) |
| prune plan | 0.12 (0.18) | **1.06 at 1.75** (3.34) | 0.01 |
| prune execute | 0.80 at 2.15 (1.38) | **1.47 at 1.73** (3.32) | 0.79 at 2.19 (1.45) |
| ls walk | 0.48 at 1.17 (0.75) | 0.48 at 1.08 (0.76) | 0.04 |

Totals of these runs: main 15.95 s, big-index 19.63 s, large-files 13.63 s (the judge: 15.78 / 19.36 / 13.34).

What changed since map 8 (S016-racefix):
- **big-index is no longer one big cell.** Prune 6.98 -> 2.99 s (plan 3.37 -> 1.06, execute 2.81 -> 1.47), the index
  tail after the dwell 0.31 -> 0.06-0.09 s per phase, every peak 100-170 MiB lower. Its largest cells are now main's
  (backup-initial, check, restore) plus the index.
- **large-files' repeat backups are still slower than stock in the snapshot stage** (bu 1.32 vs 1.23 s, bm 1.93 vs
  1.81 s) although S018 took 0.24 s out of bu. The two file readers are the critical path: bu runs at 1.94 cores
  with 2.7 idle core-s.
- **large-files restore has not moved in three seasons**: the files stage is 2.14 s at 1.76 cores (stock 2.09 s),
  4.8 idle core-s. Two read workers, large blobs.

## 3. CPU and heap at the peak (one profile per cell; `data/profiles.txt`)

| cell | CPU-s (stock) | where |
|---|---|---|
| main bi | 17.1 (18.1) at 3.36 cores | syscalls 3.14, sha256 2.36, chunker 1.35, zstd compress 2.6, poly1305 0.64, AES 0.53, KDF 0.8. By owner: `saveAndEncrypt` 8.45, of it **`verifyCiphertext` 2.41** (the extra verify on save: decrypt, decompress, hash); file readers 3.88 (stock 2.93) |
| large-files bi | 12.4 (13.3) at 3.28 | sha256 2.37, syscalls 1.72, chunker 1.50, zstd compress 1.8, `verifyCiphertext` 1.92; file readers 3.33 |
| large-files bu | 3.4 (3.2) at 1.96 | chunker 1.21 + digest 0.22, sha256 0.63, KDF 0.8; all of it in `parentCutReader.read` on two readers |
| large-files bm | 6.8 (6.5) at 2.85 | chunker 1.10, sha256 1.14, zstd 0.9, `verifyCiphertext` 0.94 |
| large-files restore | 4.7 (4.4) at 1.81 | sha256 0.87, syscalls 0.71 (6,946 pwrite), zstd 0.52, poly1305 0.29, KDF 0.8 |
| main restore | 8.1 (8.9) at 2.40 | syscalls 2.78 (91k files: open, chown, chmod, utimens, pwrite), sha256 0.83, zstd 0.58 |
| big-index check | 10.7 (11.5) at 2.99 | sha256 1.87, zstd 0.68, index `valuesWithID` 0.41, memeq 0.47 |
| big-index prune | 6.0 (11.4) at 1.96 | `firstIndex` 0.51 (was 2.65), JSON 0.2, KDF 0.8; the rest is spread |

| workload | where the peak falls | owners (MiB in use at the peak) |
|---|---|---|
| main bi | 0.95 s | `saveChunkInline` 11.9, `saveAndEncrypt` 11.7, `readNextChunkOrdinary` 11.1; 47 in use, RSS 94 (stock 191) |
| main restore | 1.29 s | range buffers `streamPackPartWithBuffer` 10.7, zstd `DecodeAll` 6.4, file list 6.0 + 5.6; RSS 111 |
| large-files restore | 1.37 s | range buffers 19.7 (two workers, blobs up to 8 MiB); RSS 74 |
| large-files bi / bu / bm | 1.9 / 0.75 / 1.5 s | `readNextChunkOrdinary` 13.8 / 9.3 / 7.4, `saveChunkInline` 7.9, zstd encoder 5.5; RSS 116 / 67 / 95 |
| big-index, all phases | at the index load (1.0-1.3 s) | the index tables `hashedArrayTree.grow` 76-78 (the floor), zstd `DecodeAll` 0.5-19; 97-134 in use, RSS 180-275. Stock: `DecodeAll` 101-152 + slice growth 62 |

## 4. Prototypes on the local workloads (builders' `bench`, 5 fixed pairs vs S018, research lanes; diffs in `data/protos/`)

Seconds are paired medians, negative = faster; every row is 5/5 wins unless marked. MiB = change of the phase peak.
BOUND = not allowed as built (a product-behaviour change, a weakened check, or an unrestricted knob); it sizes what
is there. ROUGH = inside the rules as far as built, not a candidate. Oracle column: the builders' `oracle` op on the
phases touched.

| prototype | edit | main | big-index | large-files | oracle |
|---|---|---|---|---|---|
| `p-rs3` ROUGH | a third ORDINARY restore worker (may take large blobs), on the local backend's additional load slot (`restorer/filerestorer.go`, 4 lines) | rs **-0.419**, +15.0 MiB | (as `p-conn3`) | rs **-0.462**, +17.0 MiB | pass |
| `p-rs3w2` ROUGH | `p-rs3` + ranges of 2 MiB, read as windows of ONE backend request per pack (`p-onereq`) | rs -0.187, **+19.6 MiB** | rs **-0.300**, +1.7 MiB | rs **-0.402**, **+3.0 MiB** | pass |
| **`p-rs3b3` ROUGH** | `p-rs3w2` + at most two workers at a time hold a pack with a needed blob above 3 MiB | rs **-0.208**, **+4.6 MiB** | rs **-0.323**, +14.4 MiB (guard 31.5) | rs **-0.314**, **-2.5 MiB** | pass |
| `p-rs3b2` / `p-rs3b4` | the same with the limit at 2 MiB / 4 MiB | 2: +0.003 (null) / 4: -0.203, +13.3 MiB | | 2: +0.010 (null) / 4: -0.407, +9.8 MiB | pass (2) |
| `p-rs3m` ROUGH | `p-rs3w2` + decode buffers carried across windows and packs, pools sized for three | rs -0.323, **+26.2 MiB** | rs -0.378, +13.9 MiB | rs **-0.568**, **+33.5 MiB**; fp -0.032 | pass |
| `p-conn3` BOUND | local `Connections` 3: unrestricted third restore, check, repack and upload worker | rs -0.427, ck -0.081, fp -0.120; +21.6 / +8.0 / +15.2 MiB | rs -0.499, ck -0.204, fp -0.059; +16.8 / +15.2 / +8.8 | rs -0.421, ck -0.125, fp -0.219; +14.4 / +5.9 / +11.2 | pass |
| `p-rc3` BOUND | `ReadConcurrency` 3 (a third file reader, unrestricted) | bi -0.189, bu -0.137, bm -0.120; +12.3 / +13.0 / +12.9 MiB | | bi -0.052 (4/5), **bu -0.489**, bm -0.102; +7.2 / +15.1 / +3.6 | pass |
| `p-rc4` BOUND | `ReadConcurrency` 4 | bi -0.227, bu -0.153, bm -0.126; +25.8 / +8.6 / +14.9 | bi -0.256, bu -0.171, bm -0.128; +22.1 / +14.0 / +13.5 | bi +0.036 (0/5), bu -0.475, bm -0.048; +18.4 / +13.6 / +10.8 | pass |
| `p-noverify` BOUND | the extra verify on save switched off (an integrity check: CLOSED) | bi **-0.525**, bm -0.030 | bi **-0.512** | bi **-0.517**, bm -0.181 | pass |
| `p-ictime` BOUND | `--ignore-ctime` forced on (product behaviour: CLOSED) | bu **-0.335** | | bu **-1.266** (1.74 -> 0.47 s) | FAILS (snapshot summary differs: it is a behaviour change) |
| `p-sparse` BOUND | `restore --sparse` forced on (block allocation: CLOSED) | | | rs **-0.456** | pass (the manifest does not see blocks) |
| `p-onereq` ROUGH | one backend request per stock-sized pack range, read in the tree's 4 MiB windows (section 6) | rs -0.008, fp -0.004 (unresolved) | rs -0.026 (unresolved) | rs +0.056 (1/5, unresolved), fp +0.009 | pass, three workloads |
| `p-ctl` ROUGH | the first control requests of a process overlapped (section 6) | bu -0.009, ls -0.001 (null) | | | pass |

Notes.
- **The third restore worker is worth 0.42-0.50 s on every local workload; what it costs is blob-sized buffers.**
  The range buffer and the decoded copy of a blob both grow to the blob (up to 8 MiB). `profile` of `p-rs3w2` on
  main at the peak: zstd `DecodeAll` 20.1 MiB (incumbent 5.3), range windows 17.0 (7.1).
  - 2 MiB windows alone bound it on large-files (+3.0 MiB) and big-index (+1.7) but not on main (+19.6).
  - Carrying buffers in pools made main worse (`p-rs3m` +26 MiB): retained blob-sized buffers are the cost.
  - **Limiting packs with a blob above 3 MiB to two workers at a time holds main at +4.6 MiB and keeps half of the
    gain (-0.208 s); large-files keeps -0.314 s at -2.5 MiB.** At 2 MiB the third worker never runs (null on both).
    The limit is a constant to sweep; a byte budget shared by the workers is the complete form.
- **A third file reader gives almost all of the fourth's gain** (`p-rc3` vs `p-rc4`), and on large-files
  backup-unchanged all of it (-0.489 s). It costs 12-15 MiB per phase: over the guard on main and large-files as an
  unrestricted reader. A reader holds its read buffer and the chunk being assembled (up to 8 MiB).
- **The extra verify on save is 0.52 s of backup-initial on every local workload** (2.4 CPU-s on main, 1.9 on
  large-files; backup-initial already runs at 3.3-3.5 cores). It may not be removed or weakened. What is open is
  doing the same check with less CPU, or on cores that are idle at that moment.
- **Never re-reading on a ctime change is still -1.27 s on large-files and -0.34 s on main**: the three
  parent-guided readers built in S018 have taken about a quarter of the bound.
- **Sparse restore's in-rules form is small.** `p-sparse` reads -0.456 s; T3's zero-write skip after `fallocate`
  (same bytes, same blocks) reads -0.064 s against S018 in a tree that also carries decode buffers (section 7).
  The time is block allocation, not the writes.

## 5. Remaining levers, ranked by suite points (four workloads; 1 s = 1.58 main / 1.29 big-index / 1.87 large-files / 0.72 remote)

| # | lever | measured or bounded | suite points | memory |
|---|---|---|---|---|
| R1 | **One backend request per pack range, read in windows** (section 6) | remote restore **-2.73 s**, prune -0.10 s (`p-onereq`); local null | **+2.0 measured** | flat |
| R2 | Control requests of every process overlapped (section 6) | -0.06 s per process as built (`p-ctl`); about 0.12 s reachable of the 0.25 s the link adds | +0.3 measured; +0.6 | flat |
| R3 | backup-initial on remote: the fifth connection | 1.6 s above floor plus latency at 3.9 connections busy; a third reader -0.12 s | ceiling +1.1; unprototyped | |
| 1 | **Third ordinary restore worker inside a buffer bound** | bound -0.42 / -0.50 / -0.46 s (`p-rs3`, `p-conn3`); inside the guard -0.208 / -0.323 / -0.314 (`p-rs3b3`); with R1 in the same tree remote -2.82 | **+2.2 bound; +1.3 measured inside the guard (+3.4 with R1)** | the work: +4.6 / +14.4 / -2.5 / -9.3 MiB as built |
| 2 | **Third file reader inside a buffer bound** | `p-rc3`: main -0.446 s, large-files -0.643 s over the three backups; big-index -0.555 s (`p-rc4`) | **+2.6 bound** | +12 to +15 MiB per phase unrestricted |
| 3 | The extra verify on save, cheaper or overlapped | bound -0.52 s of backup-initial on each workload, -0.18 s of large-files bm | +2.9 bound (closed as built) | lower |
| 4 | Repeat-backup re-read, faster | bound -0.34 / about -0.34 / -1.27 s; T2's and T3's readers -0.12 to -0.15 s on large-files bu against S018 | +3.3 bound; +0.2 to +0.3 seen | flat |
| 5 | Third check and repack worker for large packs | `p-conn3`: ck -0.08 / -0.20 / -0.13, fp -0.12 / -0.06 / -0.22 | +1.3 bound | +6 to +15 MiB per phase |
| 6 | Use the idle dwell | idle 1.2 s on main, 1.4 s on large-files, 0 on big-index, summed over 7 processes | ceiling +4.5; no prototype | bounded buffers |
| 7 | big-index: index tail, prune, check | tail 0.06-0.09 s x 7; plan 1.06 s at 1.75 cores; execute 1.47 s at 1.73; check 3.16 s | +0.7 (tail); unmeasured beyond | lower |
| 8 | Restore prepare-ahead and range prefetch (T4) | -0.05 / (+0.07) / -0.08 s against S018 | +0.2 | flat |
| 9 | Zero blobs in restore | bound -0.456 s (closed); in-rules form -0.06 s | +0.1 | flat |

## 6. The remote workload (`incumbents/S018`; builders' harness on research lanes; `data/link.txt`)

**What it is.** main's corpus, mutation set, phases and oracle. restic reaches the repository through its REST
backend over a simulated link: every request waits 30 ms before its first byte, each connection carries 48 MiB/s,
at most 5 requests are in flight (a sixth waits at the server). A phase that moves B MiB cannot finish under
B / 240 s: the floor.

**Per phase, stock -> incumbent** (`bench`, 3 pairs; requests, MiB and latency are the same in every run):

| phase | seconds | peak MiB | requests | MiB up / down | floor s | latency, connection-s (requests x 30 ms) | above the floor (incumbent) |
|---|---|---|---|---|---|---|---|
| backup-initial | 8.40 -> 7.84 (+6.2%) | 190 -> 98 | 88 -> 89 | 1,248 / 0 | 5.20 | 2.6 -> 2.7 | 2.64 s |
| backup-unchanged | 3.01 -> 2.42 (+19.6%) | 134 -> 68 | 17 -> 17 | 6.1 / 9.9 | 0.07 | 0.5 | 2.35 s |
| backup-mutated | 3.24 -> 2.69 (+17.3%) | 165 -> 80 | 23 -> 23 | 93.5 / 9.9 | 0.43 | 0.7 | 2.26 s |
| restore | 10.77 -> 10.34 (+4.2%) | 313 -> 129 | 89 -> **430** | 0 / 1,245 | 5.19 | 2.7 -> **12.9** | **5.15 s** |
| check-read-data | 7.27 -> 6.75 (+7.1%) | 145 -> 88 | 90 -> 90 | 0 / 1,248 | 5.20 | 2.7 | 1.55 s |
| forget-prune | 4.00 -> 3.52 (+14.0%) | 144 -> 102 | 117 -> 132 | 135 / 150 | 1.19 | 3.5 -> 4.0 | 2.33 s |
| ls-json | 2.05 -> 1.33 (+35.0%) | 68 -> 57 | 14 -> 14 | 0 / 9.9 | 0.04 | 0.4 | 1.29 s |
| **one pass** | **38.74 -> 34.88 (+9.95%)** | 1,158 -> 623 | **438 -> 795** | 1,483 / 2,674 | **17.32** | 13.1 -> 23.9 | 17.56 s |

- **The floor is half of the incumbent's pass** (17.3 of 34.9 s). 1 s = **+0.72 suite points** (100 / 34.88 / 4).
  Memory guard: about 12.5 MiB summed over the 7 peaks.
- **The incumbent keeps +9.95% of its local +29% here.** What it won in key derivation, tree passes and CPU shows in
  every phase; what it did to the request count shows in restore.
- No arm moves fewer bytes than stock in any phase. The only request differences are `GET data`: **restore 417
  against stock's 76, forget-prune 63 against 48.**

**Stage timeline** (`timeline`, 2 runs per arm; wall s; requests that arrived in the stage; "busy" = connections
occupied on average, of 5, latency included):

| stage | incumbent | stock |
|---|---|---|
| before work, every process | **0.65 s, 12 requests one after another**: `HEAD config` 0.03; key 0.30 (`GET keys/`, `GET keys`, `GET config`, derivation 0.20); 0.09 (`GET locks/`, `POST locks`, `GET snapshots/`); index 0.14 (`GET index/`, two `GET index`, 3.5 MiB) inside the dwell; `GET locks/` 0.06; `GET snapshots` 0.03. Local: 0.40 s | 1.11 s |
| backup-initial snapshot | **7.26 s at 2.2 cores, 78 requests, 1,248 MiB, busy 3.91** (floor 5.20 + 0.47 of latency) | 7.56 s, busy 3.75 |
| backup-unchanged snapshot | 1.73 s (local 1.35): `GET data` 6.3 MiB (the tree pack, one request, 0.16 s), then `POST data` 6.0 MiB, `POST index`, `POST snapshots` one after another | 1.85 s |
| backup-mutated snapshot | 1.97 s (local 1.45), 10 requests, 99.8 MiB, busy 1.2 | 2.13 s |
| restore pass 1 (trees) | 0.56 s, 1 request, 6.3 MiB (local 0.40) | 1.49 s |
| **restore files** | **8.62 s at 0.64 cores, 416 requests, 1,236 MiB, busy 4.43** | **6.74 s, 75 requests, busy 4.15** |
| restore pass 2 (metadata) | 0.46 s, no requests | 1.38 s |
| check after the lock | 6.09 s at 0.65 cores, 79 requests, 1,245 MiB, busy 4.65 | 6.32 s, busy 4.86 in read-data |
| prune plan / execute | 0.37 s, 5 requests / **2.45 s, 109 requests, 270 MiB, busy 3.63** (38 of them deletes) | 0.42 / 2.50 s, 94 requests |
| ls walk | 0.67 s, 2 requests, 6.3 MiB (local 0.48) | 0.94 s |
| exit | 0.05 s (`DELETE locks`) | 0.05 s |

**Why restore's file stage is 1.9 s slower than stock's.** The tree splits every pack into ranges of at most 4 MiB
(1 MiB on the small-blob worker) so that a worker holds one small buffer; stock reads up to 32 MiB per request and
holds it all (313 MiB peak against 129). Each range is its own backend `Load`: free on a local disk, 30 ms on a
connection here. 341 extra requests x 30 ms = 10.2 connection-seconds = 2.0 s over five connections.

**Prototypes on remote** (5 pairs unless marked; all pass the oracle on remote, where the corrupted-pack check goes
through the link):

| prototype | edit | remote | requests | peak | local workloads |
|---|---|---|---|---|---|
| **`p-onereq`** ROUGH | ONE backend request per stock-sized pack range (up to 32 MiB, a new request after a 1 MiB gap, as stock). The tree's ranges stay as WINDOWS read one after another from that response into the same buffer; a retried or failed request resumes at the first window not yet handled (`repository/cm9_one_request.go`, 20 lines in `streamPackWithPolicy`) | **restore 10.36 -> 7.64 s, -2.73 s (5/5)**; forget-prune -0.10 s (5/5) | restore 430 -> **89**; prune 132 -> 117 | 126.7 -> 126.1 MiB | null (section 4) |
| `p-range32` BOUND | ranges of 32 MiB as in stock (one constant) | restore -1.63 s | 430 -> 121 | **+133 MiB** | not run: over the guard everywhere |
| **`p-rs3b3`** ROUGH | `p-onereq` + 2 MiB windows + a third ordinary restore worker (section 4) | **restore -2.82 s (3/3)** | 430 -> 89 | **-9.3 MiB** | -0.208 / -0.323 / -0.314 s |
| `p-ctl` ROUGH | `GET config` and `GET keys/` start beside `HEAD config` when the backend opens; a repository with one key file has it fetched as soon as the list returns. Same requests, each sent once (`global/cm9_prefetch.go`) | backup-unchanged **-0.054 s**, ls-json **-0.067 s** (5/5 each) | unchanged | flat | null |
| `p-onereq-ctl` ROUGH | both, all seven phases (3 pairs) | **one pass 34.85 -> 31.71 s: +9.0% on the workload = +2.3 suite points**; bi -0.01, bu -0.06, bm -0.06, restore -2.77, check -0.01, prune -0.16, ls -0.07 | 795 -> 438 | 623 -> 631 MiB summed (guard 12.5) | null |
| `p-rc3` BOUND | a third file reader | backup-initial -0.118 s (3/3, inside the band), backup-mutated -0.153 s (3/3) | unchanged | +11.8 / +11.1 MiB | section 4 |

Notes.
- **`p-onereq` is the largest single lever of this map: +2.0 suite points, memory flat, nothing lost locally.**
  Reading inside the response also lets decrypting and writing overlap the transfer: `p-range32` sends 121 requests
  and gains 1.1 s less. What is left in restore after it: 7.64 s = 0.65 before work + 0.56 trees + about 5.9 files
  (floor 5.15 + 0.45 of latency) + 0.46 metadata.
- **A candidate must settle what the rough version leaves open**: a handler error stops the request without
  reading the rest of the body; the fallback path for a failed request is the tree's per-range path and was not
  exercised; the connection is held while blobs are written to disk.
- **The control requests are worth about 0.06 s per process as built (0.4 s per pass, +0.3 points)**. Still serial
  after it: `GET locks/` then `POST locks` (0.06 s), `GET locks/` after the dwell (0.03), `GET snapshots` (0.03,
  after the lock), two `GET index` one after another. The dwell (0.20 s) holds the index requests today (0.14 s).
  Order rule: snapshots are listed before the index on every path.
- **backup-initial leaves a fifth of the link idle** (busy 3.91 of 5, 1.6 s above floor plus latency). The small-file
  part of the corpus is fed by two readers (2.2 cores); the large-file part is link-bound. A third reader reads
  -0.12 s. No prototype reaches the rest.

## 7. What S018's teams built that the incumbent lacks (measured against S018, 5 pairs; `data/s018-unpromoted.md`)

| tree | phase | main | big-index | large-files | what carries it |
|---|---|---|---|---|---|
| T2 M004 | backup-unchanged | | | **-0.124** (+7.2%) | two-tile parallel boundary scan (`repository/chunker_discovery*.go`) |
| T2 M004 | backup-mutated | | | **-0.083** (+3.5%) | the same |
| T2 M004 | check-read-data | | | +0.207 (loss) | its check pipeline is slower than the incumbent's check |
| T3 M004 | backup-unchanged | | | **-0.150** (+8.7%) | cold-parent reader with a parallel first-cut search (`archiver/cold_parent_reader.go`, `repository/first_cut.go`) |
| T3 M004 | restore | | | -0.064 (5/5, inside the band) | zero-write skip after `fallocate` + decode buffers carried |
| T3 M004 | check-read-data | | | +0.095 (loss) | |
| T4 M003 | restore | -0.048 (5/5, inside the band) | +0.074, +69 MiB | -0.078 (5/5, inside the band) | prepare-ahead + range prefetch (`repository/restore_prepare_ahead.go`, `restore_range_prefetch.go`) |
| T4 M004 | restore | -0.075 (5/5) | | | the same code: its judged +0.233 s was noise |
| T4 M004 | backup-mutated / check | | | -0.020 (4/5) / +0.126 (loss) | zero-chunk routes / check hash ring and pack hash ahead |

Still open after S018: T2's boundary scan and T3's cold-parent reader (large-files repeat backups, 0.12-0.15 s each;
they overlap each other and the incumbent's reader); T4's restore prepare-ahead and prefetch (0.05-0.08 s); T3's
decode-buffer carry (memory for lever 1). Check is settled: the incumbent's is fastest.

## 8. Data, binaries, tools

- **Local** `cost-map-9/`: `SPEED-MAP.md`, `PROGRESS.md`, `data/` (`timelines.txt`, `profiles.txt`, `benches.txt`,
  `oracle.txt`, `raw/out/*.json`, `raw/PROGRESS.log`, `protos/*.diff`, `s018-rows.txt`, `s018-promotion.txt`,
  `s018-unpromoted.md`, `s018/*.diff`, `inc-src/` = the incumbent's non-test Go, `closed-ground.md`), `tools/`.
- **Box** `/opt/league/restic-costmap9/`: `src/p-*` and `src/T?-M00?` (prototype trees and copies of team
  trees), `out/`, `logs/`, `diffs/`, `tools/`.
- **Tools** `cost-map-9/tools/`: `box.py` (set `MSYS_NO_PATHCONV=1` under Git Bash), `worker.sh` (one queue, one
  worker per research lane, the lane's lock per job), `genjobs.py` (`map`, `bench`, `one`), `proto_edit9.py`
  (every prototype named above), `build_check9.sh`, `oracle9.sh`, `summ.py` (`tl`, `pf`, `bn` tables; link fields
  on remote), `show.py`, `s018_scan.py`, `promo.py`.
