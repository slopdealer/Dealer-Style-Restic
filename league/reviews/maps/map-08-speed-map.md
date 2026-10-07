# Restic League V2: suite speed map (cost map 8, the S016-racefix incumbent, three workloads)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-10-04 05:50-07:10Z on the league box while S017 finished (M004 judging, then rescreens). **This is the first map
of all three visible workloads.** Maps 1-7 measured `main` only. The mapped tree is `incumbents/S016-racefix`, the
incumbent at S017 start; S017 had promoted nothing when this was written (stage: rescreening). If S017 promotes, read
its diff and subtract what it installed (section 8 names its leading rows).

Instrument: the builders' own harness `research-tools/research_tools_v2.py` (`timeline`, `profile`, `bench`, `oracle`),
run from a job queue (`tools/worker.sh`), one job per lane under the lane's lock. CPUs 2-5 (schedutil, clock held by
idle-priority spinners, exclusive lock) until S017's stage passed M004 at 06:16Z, then also the four research lanes
(performance governor, the judge's clocks). 85 box jobs, 0 failed. Judge lanes untouched, S017 workspaces read only.
Log: `PROGRESS.md`. Raw: `data/raw/out/*.json`, tables `data/timelines.txt`, `data/profiles.txt`, `data/benches.txt`.

## 0. Headline

1. **Judge seconds per workload** (suite canary `7da6fa2c...`, 6 rounds; stock -> incumbent):
   main 22.63 -> **16.71** (+26.2%), big-index 33.18 -> **25.84** (+22.1%), large-files 16.68 -> **14.53** (+12.9%).
   **1 s saved = +1.99 points on main, +1.29 on big-index, +2.29 on large-files** (100 / total / 3). A constant in all
   21 processes: 0.1 s = +3.9.
2. **big-index forget-prune (6.98 s, the largest cell of the suite, 9.0 points) is two index walks.**
   - Planning is 3.37 s on 1.23 cores, same as stock. 2.6 CPU-s of it is `indexMap.firstIndex`: prune walks the whole
     index several times and makes one hash lookup per blob per walk to find a position it is already standing on.
   - **Prototype `p-fpseq` (a position cursor): forget-prune 6.89 -> 4.29 s, -2.60 s, 5/5, oracle passes on all three
     workloads. +3.5 suite points from one file.** S017 took 0.18 s of it (T1 M004).
   - Execute is 2.81 s (main: 0.80 s). The index rewrite reloads and decodes every index file (1.85 CPU-s) to learn
     that the eight ballast files need no rewrite. **Prototype `p-fprw` (remember each file's packs at load, skip
     the reload of kept files): -0.52 s, 5/5.**
   - **Both (`p-fp2`): 6.86 -> 3.67 s, -3.19 s, 5/5, oracle passes on all three workloads: +4.2 suite points.**
   - **The same two edits on top of S017's leading tree (T1 M004, `t1m4-fp2` vs that tree): 6.71 -> 3.52 s, -3.19 s,
     5/5, peak 576 -> 397 MiB, oracle passes.** The lever is whole whichever S017 tree promotes.
3. **Why the big-index prune peak is above stock (541 vs 411 MiB).** The incumbent's peak is at 6.7 s, at the END of
   prune, not at the index load. The rewrite's loaders (four at once) read index files through a streaming JSON
   decoder whose buffer grows to the whole document: **216 MiB of `jsontext.decoderState.fetch`** while the old index
   memory is not yet returned. Stock decodes from one exact-size buffer and peaks at its load (1.0 s). **With
   `p-fprw` the peak is 392 MiB (-152), with `p-fp2` 403 MiB (-189): below stock.**
4. **`backup-unchanged` is not unchanged.** The judge builds corpus B as a hardlink farm of A; the mutation set
   detaches every file it touches, which changes the ctime of A's copy after the parent snapshot was taken. So
   backup-unchanged RE-READS every file that v2 mutates: all nine large files on large-files (1.45 GiB of data and
   1.3 GiB of holes), about 0.5 CPU-s of chunking on main and big-index. Bound (`p-ictime`, never re-read on a
   ctime-only change; a product-behaviour bound, not a candidate): **-0.38 s main, -0.39 s big-index, -1.53 s
   large-files = +4.8 suite points.** This is most of the gap to the other backup tool on repeat backups.
5. **Why large-files holds half of main's gain.** Its time is bytes (chunk, hash, compress, write), not files.
   - What made main fast does nothing here: restore's tree passes are 0.04 s (main 0.88 s), the bounded third
     check worker never takes a large pack (read-data 1.43 s at 2.04 cores, stock 1.47), the index is 7 ms.
   - **The incumbent's backup snapshot stage is SLOWER than stock on large-files: backup-unchanged 1.57 s at 1.62
     cores vs stock 1.24 s at 2.14; backup-mutated 2.09 vs 1.83 s.** The inline known-blob check hashes every chunk
     on the file-reader goroutine; stock hashes in the saver pool. With two readers and a 1.5 GiB file, the reader
     is the critical path. The KDF gain (0.27 s per process) hides it.
6. **Readers and workers (all measured across the suite, section 6):** read concurrency 4 = -0.54 / -0.60 / -0.74 s
   over the three backups (+3.5 points) but +54 and +32 MiB on main and large-files; an unrestricted third connection
   = -0.86 / -0.90 / -1.12 s over restore, check and prune (+5.4) for +33 to +51 MiB. On large-files the third worker
   is worth more than on main (check -0.44 s, restore -0.45 s) and the small-blob bound gives it nothing.
7. **Idle lock dwell:** main 1.03 s, large-files 1.36 s, big-index 0 (the index load fills it and runs 0.30-0.33 s
   past it in every phase).

## 1. The three workloads on the judge (seconds, stock -> incumbent; MiB = incumbent peak)

| phase | main | big-index | large-files |
|---|---|---|---|
| backup-initial | 5.55 -> 5.11 (98) | 5.86 -> 5.01 (405) | 4.28 -> 3.91 (118) |
| backup-unchanged | 2.25 -> 1.85 (64) | 2.78 -> 2.21 (407) | 1.93 -> **1.98** (65) |
| backup-mutated | 2.38 -> 1.94 (82) | 2.88 -> 2.29 (413) | 2.49 -> 2.47 (94) |
| restore | 6.12 -> 3.77 (112) | 6.72 -> 4.07 (407) | 3.01 -> 2.62 (71) |
| check-read-data | 2.58 -> 1.75 (70) | 5.15 -> 4.05 (430) | 2.18 -> 1.87 (58) |
| forget-prune | 2.28 -> 1.40 (84) | 7.83 -> **6.98 (541; stock 411)** | 2.06 -> 1.23 (84) |
| ls-json | 1.47 -> 0.89 (57) | 1.97 -> 1.22 (395) | 0.73 -> 0.45 (56) |
| **total** | 22.63 -> **16.71** | 33.18 -> **25.84** | 16.68 -> **14.53** |
| points per second | 1.99 | 1.29 | 2.29 |
| memory guard (+2%, summed over 7 peaks) | 11.3 MiB | 60 MiB | 10.9 MiB |

**Share of the suite score per cell** (cell seconds x points per second = points if the cell took no time; each
workload sums to 33.3):

| phase | main | big-index | large-files | all three |
|---|---|---|---|---|
| backup-initial | 10.2 | 6.5 | 9.0 | 25.6 |
| backup-unchanged | 3.7 | 2.9 | 4.5 | 11.1 |
| backup-mutated | 3.9 | 3.0 | 5.7 | 12.5 |
| restore | 7.5 | 5.3 | 6.0 | 18.8 |
| check-read-data | 3.5 | 5.2 | 4.3 | 13.0 |
| forget-prune | 2.8 | **9.0** | 2.8 | 14.6 |
| ls-json | 1.8 | 1.6 | 1.0 | 4.4 |

Per process on every workload: KDF 0.21 s at 4 cores (stock 0.48 s at 1 core) + lock dwell 0.20 s = 0.41 s x 7 =
2.87 s: 17% of main, 11% of big-index, 20% of large-files. KDF is closed; the dwell may only be overlapped.

## 2. Stage timelines (incumbent; wall s at cores used; stock in brackets; medians of 3, clock-held lane)

| stage | main | big-index | large-files |
|---|---|---|---|
| index load | 0.053 inside the dwell | **0.52: 0.21 in the dwell at 3.6 cores + 0.30-0.33 after it at 2.3 cores** (stock 0.53 at 2.8, after its dwell) | 0.007 |
| dwell idle | bi 0.199; others 0.145-0.148; ck ~0.08 | 0 | 0.194 x 6, bi 0.199 |
| bi snapshot | 4.94 at 3.34 (5.08) | 4.41 at 3.43 (4.88) | 3.70 at 3.21 (3.79 at 3.34) |
| bu snapshot | 1.47 at 1.94 (1.59) | 1.48 at 1.90 (1.59) | **1.57 at 1.62 (1.24 at 2.14)** |
| bm snapshot | 1.57 at 2.15 (1.68) | 1.57 at 2.10 (1.68) | **2.09 at 2.88 (1.83 at 3.22)** |
| restore pass 1 (trees, dirs) | 0.40 at 1.27 (1.33) | 0.39 at 1.19 (1.32) | 0.02 (0.05) |
| restore files | 2.51 at 2.10 (2.66) | 2.47 at 2.02 (2.60) | 2.14 at 1.73 (2.20 at 1.72) |
| restore pass 2 (metadata) | 0.48 at 2.96 (1.38) | 0.47 at 2.94 (1.38) | 0.02 (0.05) |
| check before read-data | 0.11 (0.19) | **0.98**: index 0.51 + pack list 0.09 + structure 0.38 at 3.8 (1.27) | 0.01 |
| check read-data | 1.29 at 2.71 (1.70 at 2.08) | 2.48 at 2.93 (3.17 at 2.26) | **1.43 at 2.04 (1.47 at 2.03)** |
| prune plan | 0.17 (0.18) | **3.37 at 1.23 (3.40)** | 0.02 |
| prune execute | 0.80 at 2.09 (1.32) | **2.81 at 1.88 (3.14 at 1.51)** | 0.92 at 2.15 (1.61 at 1.47) |
| ls walk | 0.48 at 1.18 (0.77) | 0.49 at 1.10 (0.78) | 0.04 |

Critical path = KDF 0.21 + dwell 0.20 + (big-index: 0.31 index tail) + the work stage. Waiting: everything below 4
cores in a stage is idle cores, e.g. large-files bu 3.7 idle core-s, big-index prune plan 9.3 idle core-s.

## 3. CPU by category (one profile per cell; CPU-s; incumbent, stock in brackets where it differs)

| cell | CPU-s | where |
|---|---|---|
| large-files bi | 13.1 | sha256 2.57, syscalls 1.89, chunker 1.70, zstd compress 2.1, poly1305 0.62, memmove 0.52, AES 0.50, zstd decompress (save verify) 0.48, KDF 0.8. By owner: `saveAndEncrypt` 7.2 (`verifyCiphertext` 2.0), file readers 3.35 (stock 2.76) |
| large-files bu | 3.4 | chunker 1.23, sha256 0.70, KDF 0.8, syscalls 0.32. **All 2.37 s of it on the two file readers** (stock: readers 1.80, hash 0.78 in the saver pool) |
| large-files bm | 6.9 | chunker 1.46, sha256 1.15, zstd compress 1.1, syscalls 0.56, KDF 0.8. File readers 2.67 (stock 1.86) |
| large-files restore | 4.7 | sha256 0.86, syscalls 0.59 (6,946 pwrite; 1.3 GiB of them zeros), zstd 0.48, poly1305 0.37, AES 0.24, KDF 0.8 |
| large-files check | 3.8 | sha256 1.62, zstd 0.41, poly1305 0.25, KDF 0.8 |
| large-files prune | 2.9 | sha256 0.54, syscalls 0.45, poly1305 0.23, AES 0.20, KDF 0.8 |
| big-index prune | 11.9 | **`firstIndex` 2.64 flat (3.07 cumulative)**, JSON 1.2 (`DecodeIndex` 0.95 at load + `LoadIndexFile` 1.85 in the rewrite), `StreamTrees` 1.4 (used blobs of the ballast tree), sha256 0.56, KDF 0.8 |
| big-index check | 11.7 | sha256 1.75, zstd 0.83, memmove 0.58, index `valuesWithID` 0.54, memeq 0.50, JSON 0.37, KDF 0.8 |
| big-index bu / bm / ls / restore | 5.2 / 5.6 / 2.9 / 9.2 | main's profile plus the index load: `ForAllIndexes` 1.33 (of it `DecodeIndex` 0.95) |
| main | | as cost-map-7 section 4; S016 changed check and restore only |

Syscalls (incumbent vs stock): large-files bi read 54k vs 199k, write 39.5k, futex 32k; bu read 6.5k, newfstatat 6.5k,
llistxattr 3.2k; restore pwrite 6.9k, fcntl 6.0k vs 13.9k. big-index adds nothing to main's counts.

## 4. Heap owner at the RSS peak

| workload | where the peak falls | owners (MiB in use at the peak) |
|---|---|---|
| big-index, six phases | at 0.46-0.53 s: the index LOAD | zstd `DecodeAll` 127-153, `reflect.growslice` 8-47, `bytes.growSlice` 16-40; 210-240 of 390-430 RSS. Stock: the same owners at 1.0 s |
| **big-index forget-prune** | **at 6.7 s: the index REWRITE** (stock: at 1.0 s, the load) | **`jsontext.decoderState.fetch` 216**, `bytes.growSlice` 35; 290 in use + 94 freed but not returned; RSS 571 vs stock 419 |
| large-files bi | 2.2 s | `readNextChunk` 15.8, `saveFile` 12.3, zstd encoder 11; 53 in use, RSS 118 (stock 203: buffer pool 48, `encodeAll` 22) |
| large-files restore | 0.6 s | `streamPackPartWithBuffer` 15.5 (two large-blob ranges), `DecodeAll` 5.0; RSS 72 (stock 164) |
| large-files bu / bm | 0.8 / 1.7 s | `readNextChunk` 4.9-10.1, zstd encoder 4-5.5; RSS 68 / 100 |
| main | | cost-map-7 section 6 (large blobs own a read worker's memory) |

## 5. What the unmapped workloads are made of

**big-index = main + 1.5 M index entries.** Every phase pays the index load (1.5 CPU-s, 0.52 s wall, 0.31 s of it on
the critical path after the dwell: -2.2 s over 7 phases if it fitted in the dwell, +2.8 points). Check pays it in
0.51 s plus a 0.38 s structure pass. Prune pays for it three times: load, plan (3.4 s), rewrite (about 2 s of the
2.8 s execute). Everything else is main.

**large-files = 2.9 GiB apparent in 9 large files + 3,000 small ones.** `vm/disk.img` is 1,536 MiB apparent, 239 MiB
allocated: **45% of the workload's bytes are holes.** The incumbent already skips the hash of an all-zero 512 KiB
chunk (`ZeroChunk`); it still reads the holes and runs them through the chunker. Three 256 MiB incompressible media
files, a 256 MiB database file, four 48 MiB logs. v2 mutates every large file, so backup-unchanged re-reads all of
them (headline 4) and backup-mutated re-reads them changed.

## 6. Prototypes across the suite (builders' `bench`, 5 fixed pairs vs S016-racefix; diffs in `data/protos/`)

Seconds are paired medians, negative = faster. "(h)" = measured on the clock-held lane 2-5, the rest on research
lanes at the judge's governor. Points = seconds x the workload's points per second. BOUND = a product-behaviour change
or an unrestricted knob, measured only to size what is there.

| prototype | edit | main | big-index | large-files | suite points | memory (MiB, summed over the phases touched) |
|---|---|---|---|---|---|---|
| **`p-fpseq`** | prune planning asks the used-blob set by position while it walks the index in order (`index/cm8_seq.go`, `associated_data.go`, one line in `prune.go`); entries whose ID occurs twice keep the hash lookup | fp -0.056 (5/5) | **fp -2.600 (5/5)**; plan 3.36 -> 1.55 s, execute 2.80 -> 2.01 s | fp -0.019 (h, unresolved) | **+3.5** | big-index +18 and -0.2 in two runs (guard 60); main +3.3 |
| `p-fp2` | `p-fpseq` + index rewrite that does not reload untouched index files (`index/cm8_rw.go`) | fp -0.059 (5/5) | **fp -3.188 (5/5): 6.86 -> 3.67 s**; `p-fprw` alone -0.515 (5/5) | fp -0.011 (unresolved) | **+4.2** | **big-index fp peak 591 -> 403 MiB (-189)**; `p-fprw` alone 544 -> 392: below stock's 411 |
| `t1m4-fp2` | `p-fp2`'s edits applied to S017 T1 M004's tree, benched against that tree | fp -0.046 (5/5) | **fp -3.191 (5/5): 6.71 -> 3.52 s** | not run | +4.2 | big-index fp peak 576 -> 397 MiB (-178) |
| `p-rc4` | `ReadConcurrency` default 2 -> 4 (BOUND: unrestricted readers) | bi -0.202, bu -0.191, bm -0.142 | bi -0.221, bu -0.220, bm -0.163 | (h) bi -0.026, bu **-0.655**, bm -0.061 | +1.06 +0.78 +1.70 = **+3.5** | **main +54 (guard 11.3), large-files +32 (guard 10.9)**, big-index -19 |
| `p-hpar` | chunks above 256 KiB are hashed in the saver pool, as in stock; small chunks stay inline (`archiver/file_saver.go`, 4 lines) | (h) +0.07, 0, 0: unresolved | bu -0.057, bm -0.037 (both 5/5) | bi -0.059, **bu -0.234, bm -0.120** (5/5) | 0 +0.12 +0.95 = **+1.1** | large-files +13; main +14 (h): large chunks are copied for the saver |
| `p-hpar-rc4` | both | | | bi -0.097, bu -0.646, bm -0.108 | large-files +1.95 | large-files +62 |
| `p-ictime` | BOUND: `--ignore-ctime` forced on | bu **-0.383**, bm 0 | bu **-0.388**, bm -0.015 | (h) bu **-1.529** (2.01 -> 0.48 s), bm -0.008 | +0.76 +0.50 +3.50 = **+4.8** | lower (-7, +2, -8) |
| `p-sparse` | BOUND: `restore --sparse` forced on | (h) -0.026 | 0.000 | **-0.551** (2.83 -> 2.27 s) | **+1.26** | flat |
| `p-conn3` | BOUND: local `Connections` 3 (third restore, check and upload worker, second repack worker) | (h) rs -0.640, ck -0.101, fp -0.114 | rs -0.647, ck -0.181, fp -0.071 | rs -0.452, **ck -0.439**, fp -0.232 | +1.70 +1.16 +2.57 = **+5.4** | main +51, big-index +33, large-files +40 |

Notes.
- **`p-fpseq`.** `AssociatedSet.Get/Set` call `MasterIndex.blobIndex`, a bucket walk with cache misses, about 450 ns
  each, 6 M times in planning. In an index-order walk the entry is the one after the last. The prototype keeps a
  cursor per blob type, verifies the ID at the cursor, and falls back for duplicates (one pass over the buckets marks
  them) and for out-of-order asks. What is left in the plan (1.55 s) is the used-blob walk of the ballast tree
  (`StreamTrees` 1.4 CPU-s, random-order `Insert`s) and the iteration itself. The cursor is not safe for concurrent
  callers as written; a candidate needs a private cursor per walk or position-carrying iterators.
- **`p-fprw`.** `MasterIndex.Load` records each index file's pack IDs and whether it is full and not oversized.
  `Rewrite` then skips the reload of a file that holds no removed pack. The stock loop also compares pack contents
  between index files (a pre-0.10 duplicate case); the prototype only checks that no pack is listed by two loaded
  files and reads files written by this run as before. A candidate must settle that case exactly.
- **`p-ictime` is a bound, not an option.** A ctime change means the content may have changed; restic must read
  it. What a candidate may do is read faster (section 7, levers 3 and 4).
- **`p-sparse` is a bound.** Default sparse restore changes how many blocks a restored file occupies: a product
  change that needs an operator ruling. Inside the rules: a zero blob written into a range this process has just
  created and preallocated changes no byte and no block; unmeasured.
- **`p-conn3`** confirms cost-map-7 on main (restore -0.65 s unrestricted) and adds: big-index restore the same
  -0.65 s; large-files restore -0.45 s and check -0.44 s, where the small-blob bound does not apply.

## 7. Remaining levers, ranked by suite points (on S016-racefix; subtract what S017 installs)

"(m)" = measured here or by an S017 judged row.

| # | lever | where it pays | measured or bounded ceiling | suite points | memory |
|---|---|---|---|---|---|
| 1 | **Prune planning by position** (`repository/prune.go`, `index/associated_data.go`, `index/master_index.go`) | big-index fp | **-2.60 s (m, `p-fpseq`)**, main -0.06 | **+3.5** | 0 to +18 MiB on big-index (guard 60) |
| 2 | **Re-read of ctime-changed files, done faster** (bu on all three; bm on large-files) | bu | bound -0.38 / -0.39 / -1.53 s (m). Reachable forms: bounded extra readers (rc4: -0.19 / -0.22 / -0.66 on bu (m)); hash at the parent's chunk boundaries in parallel inside one file, no chunker (est. half to two thirds of the bound; unbuilt) | **+2 to +3.2 of a +4.8 bound** | readers hold chunks up to 8 MiB each: bound them |
| 3 | **A third read worker that may take large blobs inside a memory budget** (restore, check, repack) | large-files rs, ck, fp; main and big-index rs | unrestricted -0.86 / -0.90 / -1.12 s (m, `p-conn3`). S017 T1's small-blob restore worker: main -0.46, big-index -0.49, large-files -0.01 (m, judge) | **+5.4 bound; +1.6 taken by the small-blob restore worker; large-files' +2.6 open** | unrestricted +33 to +51; a shared large-buffer budget is the work |
| 4 | **File-reader throughput in backups** (hash off the reader for large chunks; bounded extra readers) | large-files bu, bm, bi; main and big-index bi | `p-hpar` -0.41 s on large-files (m); rc4 bi -0.20 main, -0.22 big-index (m) | **+1.1 (m); up to +3.5 with readers** | +13 (hpar), +32 to +54 (rc4): bound both |
| 5 | **Index load inside the dwell** (decode cost: 1.5 CPU-s for 0.8 core-s of dwell) | big-index, all 7 phases | -0.31 s per phase ceiling = -2.2 s; S017 T2 M003: -0.94 s and -330 MiB summed (m, judge) | **+2.8 ceiling; +1.2 taken by T2** | lower |
| 6 | **Index rewrite in prune without reloading kept files** | big-index fp | **-0.52 s (m, `p-fprw`)**; with lever 1 **-3.19 s (m, `p-fp2`)** | **+0.7; +4.2 with lever 1** | **-150 to -190 MiB at the fp peak (m)**: the decode buffers are gone and the peak moves back to the index load |
| 7 | **Use the idle dwell** | main (1.03 s idle), large-files (1.36 s) | 0.1 s per phase plausible: -0.7 s on each | +1.4 main, +1.6 large-files; hard ceiling +5.2 | bounded buffers |
| 8 | Restore tree passes (decode once, deeper lookahead) | main, big-index | pass 1 0.40 + pass 2 0.48 s each; S017 T3 M003 restore -0.25 / -0.28 s (m, judge) | +0.5 to +1.0 | 1-2 MiB |
| 9 | Zero blobs in restore | large-files rs | bound -0.55 s (m, `p-sparse`); the in-rules form is unmeasured | +1.26 bound | 0 |
| 10 | Check on a large index (pack list and structure pass before read-data: 0.98 s) | big-index ck | S017 T1 M003 ck -0.27 s (m, judge) | +0.35 taken; +0.5 left | 0 |
| 11 | backup-initial CPU (13-17 CPU-s at 3.2-3.4 cores) | all | rc4 bound -0.20 / -0.22 / -0.03 (m) | +0.7 | as lever 4 |
| 12 | Hole-aware reading (`SEEK_DATA`; emit zero chunks without read and chunker) | large-files bi, bu, bm | 1.3 GiB of holes per backup: about 0.15 s of read and 0.1-0.2 s of chunker and scan each; unmeasured | +0.5 to +1.5 (est.) | 0 |

**Stacking.** Levers 1, 5 and 6 are big-index only and independent: +4.2 measured for 1 + 6, up to +7 with 5. Levers 2, 3, 4 are where
large-files moves, and each needs a memory bound first. A change must not cost any workload more than 1% of its time.

## 8. What S017 built (rows vs S016-racefix, 6 rounds = 2 per workload; `data/s017-rows.txt`, diffs `data/s017/`)

| row | suite | main | big-index | large-files | what |
|---|---|---|---|---|---|
| **T1 M004** | **+2.44 (6/6)** | +2.75 | +4.22 | +0.33 | T1 M003 plus check structure overlap and one-lookup counting in prune planning (big-index rs -0.47, ck -0.20, fp -0.18 s) |
| **T1 M003** | **+2.35 (5/6)** | +3.15 | +3.79 | +0.10 | bounded small-blob third restore worker (rs -0.46 / -0.49 / -0.01 s), check pack sizes and structure prepared early (big-index ck -0.27), index merge insert, parent tree reuse in backup |
| T2 M003 | +1.91 (6/6) | +1.54 | +3.64 | +0.55 | direct index decode + adopt (big-index: every phase -0.03 to -0.35 s, **-330 MiB summed**), small repack worker, restore small worker, tree carry |
| T2 M004 | +1.73 (5/6) | +1.70 | +3.65 | -0.16 | the same plus index segments, bounded tree loads |
| T3 M003 | +1.64 (5/6) | +1.82 | +2.39 | +0.71 | restore small worker, tree carry, index reserve, tree JSON buffer |
| T1 M002 | +1.88 | +2.08 | +3.68 | -0.12 | |
| T4 M003 | +0.64 | -0.06 | +1.22 | +0.77 | parent compare in backup, index merge entry, tree carry |
| T4 M004 | +0.03 | +0.45 | +0.19 | **-0.56** | bounded restore loader (large-files restore +0.08 s) |

No S017 row moved large-files by more than +0.8%. Prune planning was touched once (T1 M004's counter, -0.18 s of
the 2.6 s). All 16 rows are judged; the season merge was skipped ("fewer than two archive trees merge cleanly");
rescreens were running at compile time and nothing was promoted.

## 9. Data, binaries, tools

- **Local** `cost-map-8/`: `SPEED-MAP.md`, `PROGRESS.md`, `data/` (`timelines.txt`, `profiles.txt`, `benches.txt`,
  `fp-proto.txt`, `raw/out/*.json`, `raw/PROGRESS.log`, `protos/*.diff`, `s017-rows.txt`, `s017/*.diff`, `baseline/`,
  `inc-src/` = the incumbent's non-test Go, `closed-ground.md`), `tools/`.
- **Box** `/opt/league/restic-costmap8/`: `src/p-*` (prototype trees), `out/`, `logs/`, `diffs/`, `tools/`.
- **Tools** `cost-map-8/tools/`: `box.py` (as cost-map-7; set `MSYS_NO_PATHCONV=1` under Git Bash), `worker.sh` (one
  queue, one worker per lane, the lane's lock per job), `genjobs.py` (`map`, `bench`, `one`), `proto_edit8.py`
  (`p-fpseq p-fprw p-fp2 t1m4-fp2 p-rc4 p-hpar p-hpar-rc4 p-ictime p-sparse p-conn3`), `build_check8.sh`, `summ.py` (`tl`, `pf`,
  `bn` tables), `show.py`, `s017_scan.py` (read-only rows per workload).
