# Backup tools vs our tuned restic (BACKUP-TOOLS-BENCH, 2026-10-03)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Operator order 2026-10-03: "Ok let's benchmark the others now". $0 API. the league box, **CPUs 2-5 only** (slice
`backupbench.slice`, `taskset 2-5`; from 08:06Z also under `flock -x .../shared/cpus-2-5.lock`). No governor, judge,
season workspace, chain-state or controller was touched, and nothing of this bench is left running.

**Headline run: `main4`, 10 interleaved reps x 6 arms, every unit complete and clean, clock-held with 4 per-CPU spinners.**
SMT siblings 50-53 were checked idle before the run. The contention sampler logged only 4 kernel-worker samples.
S015 reads **17.07 s** here against **17.51 s** on the judge, and stock reads 21.45 s against 22.60 s.

## Headline (main4, medians of 10, [min-max])

| arm | total time (s) | vs stock | **vs S015** | sum of 7 peak RSS (MiB) | vs S015 | max single peak | restore correct |
|---|---|---|---|---|---|---|---|
| stock restic | 21.45 [21.26-21.56] | 1.00 | 1.26 | 917 | 1.55 | 210 | 10/10 |
| **S015 restic** | **17.07** [16.96-17.19] | 0.80 | **1.00** | **593** | 1.00 | 112 | 10/10 |
| rustic 0.11.4 | 23.51 [23.23-23.86] held; **~19.2-20.2 judge-equivalent** (see a) | 1.10 | 1.38 held; **~1.13-1.18** | 2262 | 3.82 | 1240 | 10/10 (but see b) |
| Kopia 0.23.1 | **14.48** [14.35-15.14] | 0.68 | **0.85** | 2515 | 4.24 | 655 | **0/10** (see c) |
| Borg 1.4.5 (cold cache) | 54.54 [54.16-54.78] | 2.54 | 3.19 | 727 | 1.23 | 135 | 10/10 |
| Borg 1.4.5 (warm cache) | 49.10 [48.60-49.24] | 2.29 | 2.88 | 721 | 1.22 | 137 | 10/10 |

### Per-phase median wall time (s) [min-max], main4

| phase | stock | s015 | rustic | kopia | borg | borg-warm |
|---|---|---|---|---|---|---|
| backup-initial | 5.83 [5.75-5.89] | 5.45 [5.39-5.52] | 11.09 [10.91-11.35] | 3.98 [3.91-4.04] | 15.05 [15.03-15.23] | 15.11 [14.93-15.21] |
| backup-unchanged | 1.88 [1.87-1.91] | 1.51 [1.49-1.51] | 1.67 [1.60-1.71] | 0.52 [0.50-0.53] | 8.24 [8.20-8.32] | 5.19 [5.14-5.22] |
| backup-mutated | 2.40 [2.39-2.42] | 2.01 [1.99-2.03] | 3.17 [3.12-3.25] | 1.11 [1.08-1.51] | 8.38 [8.28-8.48] | 6.18 [6.10-6.22] |
| restore | 5.97 [5.91-6.04] | 3.96 [3.94-4.03] | 4.40 [4.32-4.58] | 4.11 [4.07-4.16] | 11.08 [11.03-11.16] | 11.09 [11.00-11.22] |
| full verify | 2.78 [2.76-2.81] | 2.46 [2.44-2.49] | 1.68 [1.57-1.74] | 2.98 [2.93-3.07] | 7.62 [7.53-7.73] | 7.58 [7.53-7.65] |
| forget + prune | 1.09 [1.09-1.17] | 0.76 [0.75-0.81] | 0.82 [0.74-0.84] | 1.10 [1.07-1.27] | 1.70 [1.68-1.74] | 1.55 [1.32-1.59] |
| list latest | 1.46 [1.45-1.47] | 0.88 [0.87-0.90] | 0.70 [0.69-0.71] | 0.66 [0.65-0.68] | 2.38 [2.35-2.40] | 2.38 [2.36-2.41] |
| **total** | **21.45** | **17.07** | **23.51** | **14.48** | **54.54** | **49.10** |

Time ratio vs S015 per phase (median):

| phase | stock | rustic | kopia | borg | borg-warm |
|---|---|---|---|---|---|
| backup-initial | 1.07 | 2.03 held / **1.41 unheld** | **0.73** | 2.76 | 2.77 |
| backup-unchanged | 1.25 | 1.11 | **0.34** | 5.46 | 3.44 |
| backup-mutated | 1.20 | 1.58 held / 1.25 unheld | **0.55** | 4.18 | 3.08 |
| restore | 1.51 | 1.11 | 1.04 | 2.80 | 2.80 |
| full verify | 1.13 | **0.68** | 1.21 | 3.09 | 3.07 |
| forget + prune | 1.44 | 1.08 | 1.44 | 2.24 | 2.04 |
| list latest | 1.66 | **0.79** | **0.75** | 2.69 | 2.69 |

### Per-phase median peak RSS (MiB) [min-max], main4

| phase | stock | s015 | rustic | kopia | borg | borg-warm |
|---|---|---|---|---|---|---|
| backup-initial | 205.7 [194-215] | 112.1 [100-119] | 225.4 [212-246] | 484.1 [463-506] | 135.3 [135-136] | 133.6 [132-134] |
| backup-unchanged | 69.0 [68-70] | 68.0 [62-70] | 138.8 [138-139] | 149.2 [145-154] | 109.0 [108-109] | 102.6 [102-103] |
| backup-mutated | 174.8 [164-190] | 92.3 [81-95] | 146.8 [140-155] | 301.1 [273-377] | 132.7 [121-137] | 137.5 [128-142] |
| restore | 207.9 [197-219] | 109.0 [105-119] | **1239.8 [1020-1444]** | 654.9 [626-744] | 97.1 [91-100] | 94.4 [91-100] |
| full verify | 106.4 [100-111] | 81.7 [76-90] | 238.4 [229-253] | 539.5 [503-638] | 94.3 [91-98] | 94.5 [88-100] |
| forget + prune | 87.0 [85-92] | 67.7 [64-80] | 140.4 [140-140] | 207.6 [187-219] | 80.0 [80-80] | 80.0 [80-80] |
| list latest | 68.6 [68-69] | 68.8 [61-75] | 138.8 [139-139] | 142.1 [140-151] | 80.0 [80-80] | 80.0 [79-80] |
| **sum** | **917** | **593** | **2262** | **2515** | **727** | **721** |

All arms ended with exactly 1 snapshot after forget. Prune frees little in this sequence for every tool (v1-only
data is ~7% and mixed into live packs). Median repo MiB after backup-mutated -> after prune: stock 1349 -> 1343,
S015 1342 -> 1336, rustic 1343 -> 1343, kopia 1412 -> 1413, borg 1385 -> 1353.

## Follow-up investigations

### (a) rustic's held-vs-unheld gap is an artifact of the clock-hold method

Backup-initial only, spinners on/off interleaved, 6 reps each (`runs/followup/held.jsonl`):

| tool | bi held | bi unheld | held/unheld | user CPU s held / unheld |
|---|---|---|---|---|
| rustic | 11.07 [10.92-11.25] | 7.66 [7.60-7.70] | **1.45** | 20.3 / 20.0 |
| s015 | 5.41 [5.36-5.44] | 5.20 [5.10-5.27] | 1.04 | 13.5 / 13.6 |
| stock | 5.79 [5.75-5.86] | 5.57 [5.54-5.61] | 1.04 | 13.6 / 13.6 |

`strace -f -c` of rustic's bi shows **0.9-1.3 million `sched_yield` calls** and 1.2 million futex calls: a
spin-then-yield worker pipeline. S015 makes no `sched_yield` calls; it parks on futex, nanosleep and epoll. With a
SCHED_IDLE spinner on each CPU, every rustic yield hands the core to the spinner, so wall time rises 45% while CPU
work is unchanged. On the judge (performance governor, no spinners) rustic would not pay this. Best estimate of
rustic's judge-equivalent total: the per-phase minimum of held (main4) and unheld (`rusticrepro`, 9 complete units,
total 20.15 [20.00-20.42] s) = **~19.2 s, so 19.2-20.2 s = 1.13-1.18x S015**. The held figure (1.38x) overstates the gap.
The earlier `main3` run had a harness bug: a single floating spinner instead of four per-CPU ones, so it was mostly
unheld. It shows the same pattern: rustic bi 9.30 s; every multi-threaded arm about 20% slower; borg unchanged.

### (b) rustic MAC failure: reproduces; rustic's local cache is corrupted, the repository is not

- Rate: **5 of 30 rustic units** had a failing phase. That was 0/10 in held main4, 3/12 in the unheld repro
  (`runs/rusticrepro`), 1/3 in main3, 1/2 in the first main run and 0/2 in trials. Four failures were in
  `forget --keep-last 1 --prune` and one in `check --read-data`.
- Exact error (identical shape every time; first lines of `rusticrepro` rep 5):
  ```
  [WARN] Error in cache backend writing Pack,9952b7ea: Error: Failed to move `.../.cache/rustic/<repo>/data/99/9952b7ea...-tmp-`
         to `.../.cache/rustic/<repo>/data/99/9952b7ea...` (kind: related to input/output operations): caused by: No such file or directory (os error 2)
  error: `rustic_core` experienced an error related to `cryptographic operations`.
  Message: error processing Pack:9952b7ea at BlobLocation { offset: 1059442, length: 11195, uncompressed_length: Some(56989) }
  Caused by: Data decryption failed, MAC check failed.   Context: - nonce: [0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]
  ```
  Two threads cache the same pack under the same `<id>-tmp-` name. One rename wins, the other fails with ENOENT, and
  a reader then gets a zero-filled region (all-zero nonce), so the MAC check fails.
- Re-running `check --read-data` with that same cache **crashed rustic with SIGSEGV (exit 139)** twice in the repro
  and wrote a crash report; it passed once (main3). The rep-4 `check` failure was the same zero-nonce read and also
  ended in a crash.
- **The repositories are intact.** Copies of all three saved failed repos pass rustic `--no-cache check --read-data`,
  stock restic `check --read-data` ("no errors were found") and a rustic `--no-cache prune` retry, all exit 0
  (`runs/followup/repocheck.txt`). So this is a rustic cache-concurrency bug that a fresh or cold cache triggers. It is
  not data loss, but in a judge it would show as random phase failures in about 1 run in 6.

### (c) Kopia's restore failures are real Kopia behaviour, not harness artifacts

The probe is `runs/followup/kopia.txt`. It backed up corpusA through the harness, listed what Kopia stored, and then
restored **outside bwrap as plain root**:
- **Non-UTF-8 name**: the source is `raw-invalid-\xff-\xfe.bin`. Kopia **stores** it as `raw-invalid-�-�.bin` (seen
  in `kopia ls` of the snapshot), so the original name is lost at backup time. The plain restore writes
  `raw-invalid-\xef\xbf\xbd-\xef\xbf\xbd.bin`.
- **Named pipe**: there is no entry in the snapshot. The root summary reports `files: 90197, numFailed: 0`, so the
  fifo is skipped silently, and the plain restore has no pipe.
- **Hardlinks**: the source groups `{special/hardlink-a,b,c}` and `{special/hardlink-pair-1, medium/hardlink-pair-2}`
  share inodes. Kopia stores them as independent file entries with the same content object. The plain restore gives
  each its own inode.
- All regular-file contents, symlinks, modes and mtimes matched in every run. The failures are exactly these 3 items
  (2 missing + 1 extra + 2 split groups in the verifier).

## Tools and checksums (official GitHub releases only)

| arm | version | artifact | sha256 | verification |
|---|---|---|---|---|
| stock | restic 0.19.1-dev, league stock tree, go1.27.0, judge flags | `restic-costmap7/bin/stock-plain` | `7d39ed17b7322fc241a0a6caca2b52a06d52fb359ef6e4a24b8c929b6e92f054` | league build |
| s015 | restic 0.19.1-dev + incumbent S015, same toolchain | `restic-costmap7/bin/s015-plain` | `b9124f3d6f057a66f859e260080303d8354544a34b87e1f5cb1b87aacba3092f` | league build |
| rustic | v0.11.4 | `rustic-v0.11.4-x86_64-unknown-linux-gnu.tar.gz` | `c20bc3682c6275de3cfbc9317101dca5c0800a2fb5c610d0f9ef2addfd27cf81` (binary `3cf6e3d3e1e9de1528b07dcaee220d9882c5921f2a01e510360a1c6be8e9fc13`) | matches published `.sha256`; `.asc` not GPG-checked |
| kopia | 0.23.1 (build 72ec08fd) | `kopia-0.23.1-linux-x64.tar.gz` | `416d0f84a3dbb321a8b2d8f0997b1a0a6e915babe79ee76fa6e4d2bd1e1c5178` (binary `e306c3d48b47756912928738241baffe68d1a8a16ab804ab3fbabd725a61df7f`) | matches published `checksums.txt`; `.sig` not GPG-checked |
| borg | 1.4.5 (latest stable; 2.0 is beta) | `borg-linux-glibc231-x86_64.tgz` (onedir) | `b6cba6b5eee19d51fbf4d89c6090dd40647b856755fffe2e26417365536f2c58` (borg.exe `e0a23534bf28aa90940f749bb25dbbeecd401e9bf1de1dd8872cedc45f98718d`) | sha256 recorded; `.asc` not GPG-checked |

## Method

- Workload: the judge's MAIN bundle (`bundle-fea87993...`), read in place under a shared flock like census_run.
  corpusA = v1 (90,198 files, 1.69 GiB); corpusB = v2. Nothing else is used or referenced.
- Each unit gets a fresh repo on tmpfs and runs: init (untimed), then 7 timed phases in sequence.
  Each command is a separate process launched through the judge's `rusage-launch` (wall plus wait4 `ru_maxrss`).
  It runs inside the judge's bwrap sandbox (same namespaces, caps, `/data` and `/work` binds, `GOMAXPROCS=4`,
  `taskset 2-5`), plus a read-only `/usr` for all arms because rustic and borg are dynamically linked.
- **Cold cache per phase, as on the judge**: HOME/TMPDIR is emptied before every phase. `borg-warm` keeps its cache,
  because borg's change detection lives only in that cache.
- Arm order rotates per rep. A restore is verified against the bundle's `expected-restore-v2.json` (kind, size +
  sha256, symlink target, mode, mtime ns, owner, hardlink groups, missing/extra). The snapshot count is checked
  after forget. A foreign running task sampled on CPUs 2-5 during a phase marks the unit contaminated.

### Command mapping

| phase | restic (stock, s015) | rustic | kopia | borg 1.4 |
|---|---|---|---|---|
| init (untimed) | `init` | `init` | `repository create filesystem`; `policy set --global --compression=zstd`; `maintenance set --enable-quick=false --enable-full=false` | `init -e repokey` |
| bi / bu / bm | `backup /data/src` | `backup /data/src` | `snapshot create /data/src` | `create -C zstd,3 ::bi\|::bu\|::bm /data/src` |
| restore | `restore latest --target /work/restore` | `restore latest /work/restore` | `snapshot restore <latest-id> /work/restore` (id looked up untimed) | `extract ::bm` (cwd = target) |
| full verify | `check --read-data` | `check --read-data` | `snapshot verify --verify-files-percent=100` | `check --verify-data` |
| forget + prune | `forget --keep-last 1 --prune` | `forget --keep-last 1 --prune` | `policy set --keep-latest=1` (others 0) + `snapshot expire --all --delete` + `maintenance run --full --safety=none` (3 processes; times summed, RSS max) | `prune --keep-last 1` + `compact` |
| list | `ls --json latest` | `ls -l latest` (**`ls --json` exits 1**: "path contains invalid UTF-8 characters") | `ls -l -r <root-obj>` (no JSON mode) | `list --json-lines ::bm` |

Settings matched: encryption on everywhere and zstd-class compression (restic auto, rustic default zstd, kopia `zstd`,
borg `zstd,3`). Default chunking everywhere (restic/rustic Rabin ~1 MiB avg, kopia buzhash ~4 MiB, borg buzhash
~2 MiB). Passwords come from a file or env.
Settings not matched:
- kopia: file and content logging disabled (its default is debug-level log files), auto-maintenance off, and
  `--safety=none` so prune deletes immediately as restic does.
- The restic arms pay the 200 ms lock dwell. rustic and kopia are lockless, and borg locks without a dwell.

## Caveats

- Clock-hold method: the SCHED_IDLE spinners match the judge for restic (S015 17.07 vs 17.51 s) but penalise
  rustic's `sched_yield`-heavy pipeline (+45% on bi), so rustic is quoted as a range (section a). Kopia and borg do
  not move with the spinners.
- Cold cache per phase follows the judge but is unrealistic for borg. Its bu re-reads all data without the files
  cache (8.24 s cold against 5.19 s warm).
- The workload favours change detection on unchanged and mutated backups. Kopia's bu (0.52 s) and bm (1.11 s) are
  most of its lead: it walks the tree against the previous snapshot manifest with no lock dwell, using 4 MiB average
  chunks. Its restore, verify and prune are level with or slower than S015's.
- RSS is wait4 `ru_maxrss` (anon + file-mapped). rustic's 1.0-1.4 GiB restore peak may include mmapped restore
  targets on tmpfs. Its composition was not split (`tools/probe_rss.py` exists but was not run).
- Earlier data on CPUs 2-5 was discarded: `main` was disturbed by the workload-suite smoke from 06:40:48Z, and
  `main3` had the single-spinner bug. Both are kept on the box for reference only; the headline is main4.

## Verdict

- **Kopia is the only tool faster than S015: 0.85x total (14.48 vs 17.07 s, all 10 reps, no overlap).** The whole
  gain is in change-detecting backups: bu 0.34x, bm 0.55x, bi 0.73x, list 0.75x. Restore is level (1.04x), and
  verify (1.21x) and prune (1.44x) are slower. **It uses 4.2x S015's memory** (2.5 GiB summed, 0.5-0.65 GiB per
  phase), and **it fails restore correctness in 10/10 runs** (non-UTF-8 name mangled at backup, fifo silently
  dropped, hardlinks split), which is real Kopia behaviour. Under our judge it would be disqualified on correctness
  and on the 2% memory guard.
- **rustic is ~13-18% slower than S015** once the spinner artifact is removed (1.38x held). It wins verify (0.68x)
  and list (0.79x) and loses backups. It uses **3.8x the memory** (1.0-1.4 GiB restore peaks), has no working JSON
  listing on this corpus, and has a **reproducible cache race** that fails about 1 run in 6 with a MAC error or
  SIGSEGV. The repo stays intact, so this is not data loss, but it would cause random judge failures.
- **Borg is 2.9-3.2x slower than S015** and 1.2x its memory; it is single-threaded. Out.
- **A language/architecture switch is not worth a new league.** No tool beats S015 on time and memory together, and
  the one faster tool fails correctness. The finding to take back to the restic league is Kopia's change-detection
  path: unchanged backup in 0.52 s against S015's 1.51 s, and mutated in 1.11 s against 2.01 s. That is about
  1.9 s of the 7-phase total, the same order as S015's whole lead over stock.

## Files

- Runner `tools/bench.py`, chains `tools/chain.sh`, `tools/chainf.sh`, tables `tools/tables.py`, purge
  `tools/purge.py`, follow-ups `tools/followup.py`, `tools/repocheck.sh`, `tools/reprotab.py`, `tools/reprodetail.py`,
  `tools/heldtab.py`, unused RSS probe `tools/probe_rss.py`. Box copies are in `/opt/league/backup-tools-bench/tools`.
- Raw data on the box: `runs/main4` (headline), `runs/rusticrepro`, `runs/followup/{held.jsonl, strace-*.txt,
  kopia.txt, repocheck.txt}`, failed rustic repos in `runs/{main3,rusticrepro}/failed-repos`, and the superseded
  `runs/{main, main3, trial1-3}`.
