# Results

Sections 1, 3 and 4, the "net price" and "all official jobs" tables of section 5 and the tables of section 6 that
sit between generator marks are written by `league/tools/make_tables.py` from data files: the judge's result files
in `league/evidence/judge/`, the bench tables in `league/evidence/many-core.json` and the run records in
`league/evidence/release-checks-final3-b8cf439/`. Numbers in the other sections are quoted from the league's own
documents, copies of which are under `league/reviews/`; each quotation names its source and, where one exists, the
judge job.

How to read a number:

- **Time saved %** is positive when the first-named tree needs less wall time than the tree it is compared with. It is
  the median, over the rounds of a job, of the paired difference within a round. **Memory saved %** is the same for
  peak resident memory.
- **Every number carries its null.** For a judge job: the rounds won out of the rounds run, and the 90% interval of
  the median (the judge's bootstrap). For a bench: the pairs won out of the pairs run, and whether the difference is
  outside the noise band of its cell ("beyond noise") or inside it ("unresolved"; the bands are in METHODS.md,
  section 3). An unresolved number is reported as measured and is not evidence of a difference.
- **Suite** is the mean of the four workloads' medians (three workloads in S017 and S018, the main workload alone
  before that). That holds for percentages and for memory. Suite **seconds** are the sum of the four workloads' pass
  medians.
- **Precision.** Percentages are shown with two decimals, seconds with two, CPU seconds and MiB with one. The result
  files keep full precision. A figure quoted from one of the league's logs or reports keeps the precision its source
  gave it.
- **Every league number was measured with 4 pinned CPUs and `GOMAXPROCS=4`**, on one machine, with the `performance`
  frequency governor, and with corpus, repository and restore target on tmpfs. Section 6 holds what was measured
  with more CPUs and under another governor; nothing there is a judge score.
- "Upstream" is restic 0.19.1-dev at commit `ba802d42`; the judge's arm for it is called `stock`. "S020" is the
  league's last promoted tree, the `incumbent` arm of the final jobs.

Method: [METHODS.md](METHODS.md). Limits: [KNOWN-LIMITS.md](KNOWN-LIMITS.md).

## 1. The judged commit: final tables

<!-- BEGIN GENERATED:final -->
Judge job `50388f4ecf434b0d8663aa4a2a740d95` (promotion mode), completed 2026-10-07 19:24 UTC: 44 rounds, 11 per workload, three arms in every round (this build, the S020 incumbent, upstream restic at `ba802d42`). **4 CPUs pinned, GOMAXPROCS 4**; tmpfs for corpus, repositories, restore targets and restic temp/cache; one machine (AMD EPYC 9454, 48 cores / 96 threads, 128 GB RAM, Linux x86-64; `league/evidence/machine.txt`), CPUs set to the `performance` frequency governor, one build of each arm with the same pinned Go toolchain. Correctness: 1,019 of 1,019 units matched the upstream oracle; compatibility tests passed: True. **Judged: working commit `b8cf439` (the league's label `rc-8`); the source in this repository is one commit later, which changes one comment and adds one test and no code** (RESULTS.md, section 2).

### 1.1 Against upstream restic (4 CPUs)

|  | suite | main | big-index | large-files | remote |
|---|---|---|---|---|---|
| time saved vs upstream | **+29.00%** | +30.63% | +45.13% | +22.96% | +17.26% |
| rounds won (time) | 44 of 44 | 11 of 11 | 11 of 11 | 11 of 11 | 11 of 11 |
| 90% interval of the time figure | +28.84..+29.05 | +30.37..+30.78 | +44.96..+45.21 | +22.60..+23.10 | +17.10..+17.44 |
| peak memory saved vs upstream | **+38.99%** | +34.90% | +44.19% | +34.08% | +42.79% |
| rounds won (memory) | 44 of 44 | 11 of 11 | 11 of 11 | 11 of 11 | 11 of 11 |
| pass medians in seconds (suite = sum), this build / upstream | 78.27 / 110.26 | 15.55 / 22.40 | 17.84 / 32.53 | 12.67 / 16.45 | 32.21 / 38.88 |
| median peak memory, MiB, this build / upstream | 127.4 / 214.6 | 93.6 / 143.0 | 236.8 / 423.0 | 84.5 / 128.5 | 94.5 / 164.1 |

Suite percentages and memory are means of the four workload medians; suite seconds are the **sum** of the four workload pass medians. Memory of a workload is the median over rounds of the mean of the seven phases' peak resident sizes; it is not a single maximum.

### 1.2 Against the S020 incumbent, the league's last promoted tree (4 CPUs)

|  | suite | main | big-index | large-files | remote |
|---|---|---|---|---|---|
| time saved vs S020 | **-1.06%** | -0.83% | -0.18% | -1.07% | -2.18% |
| rounds won (time) | 6 of 44 | 1 of 11 | 5 of 11 | 0 of 11 | 0 of 11 |
| 90% interval of the time figure | -1.13..-0.91 | -1.03..-0.74 | -0.31..+0.09 | -1.34..-0.67 | -2.26..-2.02 |
| peak memory saved vs S020 | **-8.65%** | -12.62% | -5.17% | -7.23% | -9.58% |
| rounds won (memory) | 0 of 44 | 0 of 11 | 0 of 11 | 0 of 11 | 0 of 11 |
| pass medians in seconds (suite = sum), this build / S020 | 78.27 / 77.30 | 15.55 / 15.41 | 17.84 / 17.83 | 12.67 / 12.55 | 32.21 / 31.49 |
| median peak memory, MiB, this build / S020 | 127.4 / 118.2 | 93.6 / 82.4 | 236.8 / 226.0 | 84.5 / 78.4 | 94.5 / 86.2 |

Suite percentages and memory are means of the four workload medians; suite seconds are the **sum** of the four workload pass medians. Memory of a workload is the median over rounds of the mean of the seven phases' peak resident sizes; it is not a single maximum.

Order of the arms: it rotates through six patterns from round to round, which balances the positions over complete cycles of six. In this job of 44 rounds the arms ran first / second / third: this build 12 / 16 / 16; S020 16 / 16 / 12; upstream 16 / 12 / 16.

The judge also gives the league's own promotion verdict for this pair. It reads: eligible = false. Its reasons, verbatim:

- candidate won 6/44 score pairs; 29 required
- suite score -1.0629% is below 0.25%
- rss_saved_pct versus incumbent is -8.649%, beyond the -5.0% axis cap
- peak memory versus incumbent is -8.649%, beyond the -2.0% memory guard
- workload main: peak memory versus incumbent is -12.616%, beyond the -2.0% memory guard
- workload big-index: peak memory versus incumbent is -5.168%, beyond the -2.0% memory guard
- workload large-files: peak memory versus incumbent is -7.231%, beyond the -2.0% memory guard
- workload large-files: time versus incumbent is -1.069%, worse than the -1.0% per-workload regression tolerance
- workload remote: peak memory versus incumbent is -9.581%, beyond the -2.0% memory guard
- workload remote: time versus incumbent is -2.176%, worse than the -1.0% per-workload regression tolerance

**By the league's own rules this is a loss against S020: the tree would not have been promoted.** It won 6 of 44 rounds where 29 are required; its suite time is -1.06% where at least +0.25% is required; its peak memory is -8.65% where the guard allows -2.00%.
<!-- END GENERATED:final -->

**Read section 1.2 for what it is.** S020 is the league's last champion, and it is unrepaired. It carries the
lock-wait defect R1, at three new call sites as well, and stores some blobs in a form upstream does not
([SOUNDNESS.md](SOUNDNESS.md), sections 2 and 5). Against that tree the published one is slower in most rounds and
uses more memory in every round (the counts are in the table). The league's own promotion rules call that a loss: the
tree would not have been promoted. That is the price of the repairs and of the fixes that followed, and section 5
takes it apart. A reader who wants the fastest tree the league produced is looking at S020, and should not use it.

**What ran beside the job.** No other judge job and none of the merge agent's benches ran while the 44 rounds were
measured (merge step log, entry of 17:50Z). The release-check harness was running during that time on other CPUs of
the same machine (check step log, entries of 2026-10-07 17:09Z to 19:04Z). The same holds for the 44-round job on the
second final tree, `940c4e64` (check step log, entries of 12:06Z to 14:26Z).

What section 1 does not show is listed in the README under the headline table and, in full, in
[KNOWN-LIMITS.md](KNOWN-LIMITS.md). In one line each: 4 CPUs only; the `performance` frequency governor only; one
machine and one build; tmpfs, so no disk; a simulated link for `remote`; one restic process at a time; four
synthetic corpora with a cold cache; not the held-out workload.

## 2. What was judged and what is published

The official jobs measured the league's working commit `b8cf439` (its label: `rc-8`). The source in this repository
is the working commit `8f9372b`, one commit later. That last commit rewrites a comment in
`cmd/restic/wait_index_load.go` and adds a test function, `TestAsyncSaveResultSuppressionIsNarrow`, in
`internal/repository/repack_scope_test.go`. It changes no code. The identity record
(`league/evidence/source-identity.json`, written when this repository was assembled) lists the two files and records
that in the non-test file every changed line is a comment line.

A comment still changes the binary, in one place: Go stamps a hash of the source text, comments included, into the
build ID. So the two builds were compared section by section.

The record of that comparison is in this repository: `league/evidence/judged-vs-published/PROOF.txt`, written
2026-10-07 19:25 UTC by the script beside it (`proof.sh`; the same script is `league/tools/merge/box/proof.sh`), with
the list of differing byte offsets in `diff.offsets`. In it the judged commit is called `rc-8` and the published one
`rc-9`. Both were built in their trees with the judge's command and environment, linux/amd64, Go 1.27.0. What it
records:

| | judged commit `b8cf439` | published commit `8f9372b` |
|---|---|---|
| size of the binary | 32,542,880 bytes | 32,542,880 bytes |
| SHA-256 of the binary | `4095e7ceb4cb0d8b2919cc71613085d930d09a13c247f325238d4b7e3241fe7a` | `958d9ff2c6bc99a4c7b49d31ac65f0708c134b5f20a5b4fc56604cb3b85d3ed3` |

- The two files are **not** equal. **60 bytes differ, at file offsets 3977 to 4096 (counting the first byte as 1):
  40 inside `.note.go.buildid` and 20 inside `.note.gnu.build-id`.**
- Every other section has the same SHA-256 in both binaries: `.text` (14,722,353 bytes), `.rodata`, `.gopclntab` (the
  line tables), `.go.type`, `.go.func`, `.go.buildinfo`, `.go.fipsinfo`, `.go.module`, `.noptrdata`, `.data` and
  `.shstrtab`. `.bss` and `.noptrbss` have no bytes in the file.
- The Go build ID has four parts, two that hash what went into the build and two that hash what came out. The two
  input parts differ between the binaries; the two output parts are identical.

So the machine code that was judged is the machine code a build of the published source contains. The first SHA-256
in the table is the one the judge recorded for its candidate in both jobs on the judged commit
(`arms.candidate.binary_sha256` in `league/evidence/judge/final3-rc8-44r.result.json` and `final3-rc8-12r.result.json`).

**Three builds are involved, and they are not the same file:**

1. **The judged binary**: commit `b8cf439`, the judge's flags. Every number in section 1 is a measurement of it.
2. **The published commit `8f9372b` with the judge's flags**: the same size, 60 differing bytes inside the two
   build-ID notes, `.text` and every other section byte-identical to (1). That is what the proof above shows.
3. **The release binaries**: the published commit with the judge's flags **plus** the linker flag that sets the
   version string. They were **not themselves judged**. The linux/amd64 one is **not** byte-identical in `.text` to
   (1) or (2). The version string the flag sets is 20 bytes shorter than the value in the source, which moves
   read-only data and so changes address operands in the code. The size of `.text` is the same and all 37,880 text
   symbols are at the same addresses. A timing of the release binary against the judged binary (`ls --json` of a
   90,000-file snapshot, 20 fixed pairs) gave a median paired difference of -1.78 ms (-0.19%), 12 of 20 pairs. No
   difference was resolved in this one-phase, 20-pair measurement; it does not establish that there is none (merge
   step log, entry of 2026-10-07 19:26Z).

So a published binary carries the same machine instructions at the same addresses as the judged build, with
different address operands, and the published numbers are those of build (1). How the release binaries are made and
how to rebuild one: METHODS.md, section 12.2.

**An independent rebuild.** One of the two reviewers who checked this write-up before publication rebuilt builds (1)
and (2) offline from the two commits and got the two SHA-256 values of the table above and the same 60 differing
offsets (`league/evidence/independent-rebuild/offline-proof.json`). The same reviewer rebuilt the five release
binaries offline from the published source with the command of METHODS.md, section 12.2, and reports all five
byte-identical to the published files. `offline-targets.json` in the same folder records four of the five with both
hashes, and the reviewer's report, which is not published, lists the fifth (linux/amd64) with the hash that
`league/evidence/SHA256SUMS` gives.

How a reader can repeat it:

1. Build the judged commit and the published commit with the judge's flags (METHODS.md, section 12). In this
   repository the judged commit is the parent of the last source commit: `git rev-parse HEAD~2`; the published source
   is `HEAD~1`.
2. Compare the two binaries byte by byte (`cmp -l a b | wc -l`) and section by section (`readelf -S`, then
   `objcopy -O binary --only-section=.text` on each, and `cmp`).
3. Expected: the same size; the only differing bytes lie in the two build-ID notes.

Both official jobs on the judged commit, 12 rounds and 44 rounds, are in the arms table of section 5, beside the two
of the second final tree, the one the last independent review was given (`rc-7`).

## 3. All 28 cells: seven phases on four workloads

<!-- BEGIN GENERATED:phases -->
From job `50388f4e`, 44 rounds, 11 per workload, 4 CPUs. A cell reads: time saved (rounds won of rounds; 90% interval of the median, bootstrap as the judge computes it). The noise between identical arms, per phase, is in METHODS.md, section 3: a cell smaller than its band is not established.

### 3.1 All 28 cells against upstream: time saved

| phase | main | big-index | large-files | remote | mean of 4 |
|---|---|---|---|---|---|
| backup-initial | +7.17% (11/11; +6.92..+7.71) | +17.33% (11/11; +17.05..+17.86) | +11.15% (11/11; +10.82..+11.76) | +6.83% (11/11; +5.58..+8.10) | +10.62% (44/44) |
| backup-unchanged | +23.96% (11/11; +23.92..+24.27) | +34.23% (11/11; +33.85..+34.67) | +26.96% (11/11; +26.60..+27.27) | +21.15% (11/11; +20.99..+21.44) | +26.57% (44/44) |
| backup-mutated | +22.36% (11/11; +21.55..+22.70) | +31.59% (11/11; +31.42..+31.85) | +16.20% (11/11; +14.51..+16.61) | +18.44% (11/11; +17.53..+20.83) | +22.15% (44/44) |
| restore | +48.76% (11/11; +48.40..+48.95) | +52.06% (11/11; +51.93..+52.48) | +20.73% (11/11; +20.27..+20.76) | +30.18% (11/11; +30.01..+30.25) | +37.94% (44/44) |
| check-read-data | +32.45% (11/11; +32.38..+33.95) | +39.78% (11/11; +38.73..+40.54) | +27.61% (11/11; +27.18..+28.27) | +5.55% (11/11; +5.22..+5.70) | +26.35% (44/44) |
| forget-prune | +44.60% (11/11; +43.96..+45.47) | +70.75% (11/11; +70.63..+70.82) | +45.80% (11/11; +45.06..+46.61) | +11.98% (11/11; +9.28..+13.25) | +43.28% (44/44) |
| ls-json | +42.33% (11/11; +42.18..+42.40) | +53.00% (11/11; +52.44..+53.28) | +38.78% (11/11; +38.70..+38.88) | +36.92% (11/11; +36.63..+36.95) | +42.76% (44/44) |

### 3.2 Median seconds per phase, this build / upstream

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | 5.14 / 5.53 | 4.79 / 5.81 | 3.82 / 4.28 | 7.87 / 8.43 |
| backup-unchanged | 1.69 / 2.22 | 1.80 / 2.74 | 1.41 / 1.94 | 2.36 / 3.00 |
| backup-mutated | 1.82 / 2.35 | 1.93 / 2.83 | 2.08 / 2.48 | 2.63 / 3.25 |
| restore | 3.09 / 6.03 | 3.11 / 6.49 | 2.31 / 2.91 | 7.51 / 10.76 |
| check-read-data | 1.71 / 2.56 | 3.06 / 5.07 | 1.57 / 2.18 | 6.88 / 7.28 |
| forget-prune | 1.23 / 2.23 | 2.23 / 7.61 | 1.05 / 1.94 | 3.68 / 4.14 |
| ls-json | 0.85 / 1.47 | 0.93 / 1.97 | 0.44 / 0.73 | 1.29 / 2.04 |

### 3.3 All 28 cells against the S020 incumbent: time saved

| phase | main | big-index | large-files | remote | mean of 4 |
|---|---|---|---|---|---|
| backup-initial | -5.03% (0/11; -5.67..-4.55) | -3.39% (0/11; -3.98..-2.61) | -6.72% (0/11; -7.15..-6.25) | -0.64% (2/11; -1.77..-0.07) | -3.95% (2/44) |
| backup-unchanged | +1.31% (11/11; +0.71..+1.98) | +2.08% (10/11; +0.91..+2.31) | +0.05% (7/11; -0.92..+1.08) | -1.81% (0/11; -2.35..-1.27) | +0.41% (28/44) |
| backup-mutated | +0.60% (7/11; -0.49..+0.84) | +0.05% (7/11; -0.49..+0.47) | +0.35% (8/11; +0.19..+1.55) | -4.49% (1/11; -5.69..-1.50) | -0.87% (23/44) |
| restore | +2.77% (11/11; +2.05..+3.08) | +2.59% (11/11; +1.90..+2.86) | +0.87% (8/11; -0.05..+1.73) | -0.25% (2/11; -0.37..-0.23) | +1.49% (32/44) |
| check-read-data | -2.21% (2/11; -2.70..-1.26) | +1.47% (8/11; -0.33..+1.87) | +0.92% (8/11; -0.35..+2.07) | -2.95% (0/11; -3.02..-2.63) | -0.69% (18/44) |
| forget-prune | +2.85% (11/11; +2.30..+3.09) | +0.26% (6/11; -0.84..+1.10) | +5.96% (11/11; +5.17..+6.89) | -6.92% (0/11; -7.28..-4.34) | +0.54% (28/44) |
| ls-json | +0.60% (9/11; +0.08..+1.15) | +0.62% (7/11; -0.50..+0.86) | +0.09% (8/11; +0.01..+0.28) | -4.48% (0/11; -4.69..-4.43) | -0.79% (24/44) |
<!-- END GENERATED:phases -->

## 4. Memory

<!-- BEGIN GENERATED:memory -->
From job `50388f4e`, 4 CPUs. Memory of a pass is the mean over the seven phases of each phase's peak resident size; the figure per workload is the median over rounds. The noise between identical arms is larger for memory than for time (METHODS.md, section 3).

| workload | median MiB: this build / upstream / S020 | saved vs upstream (rounds won; 90% interval) | saved vs S020 (rounds won; 90% interval) |
|---|---|---|---|
| main | 93.6 / 143.0 / 82.4 | +34.90% (11/11; +33.68..+35.33) | -12.62% (0/11; -14.95..-11.63) |
| big-index | 236.8 / 423.0 / 226.0 | +44.19% (11/11; +43.58..+44.94) | -5.17% (0/11; -5.73..-3.92) |
| large-files | 84.5 / 128.5 / 78.4 | +34.08% (11/11; +32.31..+36.15) | -7.23% (0/11; -9.30..-4.84) |
| remote | 94.5 / 164.1 / 86.2 | +42.79% (11/11; +41.67..+43.24) | -9.58% (0/11; -10.62..-8.43) |

Suite: +38.99% against upstream (44 of 44 rounds), -8.65% against S020 (0 of 44).

### 4.1 Peak memory per phase in MiB, this build / upstream

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | 123.2 / 192.2 | 252.3 / 415.5 | 132.3 / 198.9 | 122.4 / 196.3 |
| backup-unchanged | 65.9 / 129.4 | 191.1 / 421.6 | 68.0 / 103.2 | 65.7 / 133.1 |
| backup-mutated | 105.4 / 167.2 | 239.7 / 415.5 | 100.6 / 177.6 | 102.3 / 169.8 |
| restore | 125.3 / 211.0 | 263.5 / 409.1 | 75.8 / 167.6 | 126.3 / 301.3 |
| check-read-data | 68.8 / 99.4 | 287.3 / 422.2 | 61.5 / 94.8 | 82.1 / 135.3 |
| forget-prune | 107.0 / 128.7 | 244.9 / 429.3 | 95.1 / 95.7 | 105.0 / 148.1 |
| ls-json | 56.2 / 68.2 | 176.7 / 429.7 | 57.0 / 60.0 | 57.8 / 67.5 |

### 4.2 Peak memory per phase against upstream: saved, with rounds won

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | +35.52% (11/11) | +39.27% (11/11) | +32.44% (11/11) | +36.99% (11/11) |
| backup-unchanged | +49.16% (11/11) | +54.24% (11/11) | +34.13% (11/11) | +51.23% (11/11) |
| backup-mutated | +35.97% (11/11) | +43.04% (11/11) | +42.66% (11/11) | +38.75% (11/11) |
| restore | +40.05% (11/11) | +36.88% (11/11) | +54.15% (11/11) | +58.15% (11/11) |
| check-read-data | +30.94% (11/11) | +33.33% (11/11) | +34.63% (11/11) | +39.73% (11/11) |
| forget-prune | +14.67% (11/11) | +43.29% (11/11) | -1.66% (5/11) | +29.85% (11/11) |
| ls-json | +17.58% (11/11) | +58.61% (11/11) | +5.00% (11/11) | +14.44% (11/11) |
<!-- END GENERATED:memory -->

Against upstream the build uses less memory on every workload (per phase: table 4.2). Against S020 it uses more:
that is the memory price of the repairs and of upstream's stored form, taken apart in sections 5.3 to 5.5. The
league's own promotion rule allows 2% more memory than the incumbent; this tree is past it on every workload, and the
judge's verdict in section 1.2 says so.

## 5. The cost of soundness

The judged commit is slower and uses more memory than the league's last incumbent (S020), by the amounts in section
1.2. That is the price of the repairs, of storing blobs as upstream stores them, and of the fixes that followed the
last independent review. This section takes the price apart. First the summary, then the official jobs, then the
detail.

| what | where it costs | measured | beyond noise? |
|---|---|---|---|
| the repairs R1 to R11 and M22, in their final form | first backup and forget-prune on every workload; every process on `remote` | pass time -2.00 / -1.72 / -2.55 / -1.66% on main / big-index / large-files / remote: **mean -1.98 suite points**; summed phase peaks +71.9 / +84.8 / +39.2 / +68.0 MiB (bench, 5 fixed pairs per cell; section 5.3) | first backup and forget-prune cells: yes. Pass totals: not stated per workload |
| upstream's stored form (F2) | first backup, prune | **0.79 suite points** between two official 12-round jobs; bench mean -0.70 (10 fixed pairs per cell; section 5.4) | three first-backup cells: yes |
| red-team fix 6: `check` reads nothing during the lock wait | check | big-index check **-5.31%** (0 of 8 pairs), which is -0.83 points of the big-index pass; main -0.98% (0/8), large-files -1.26% (3/8), remote -0.98% (1/8) (section 5.7) | big-index: yes. The other three: unresolved |
| red-team fix 7: prune verifies a kept index file | forget-prune on big-index | -1.08% (1 of 8 pairs; band 1.15) | unresolved |
| red-team fix 4: listings where upstream lists | restore on `remote` | -0.30% (1 of 8 pairs; band 0.3); 91 requests before and after | unresolved |
| the other six red-team fixes | error paths, bounds, output | not priced singly | |
| restore hands work over per batch | restore | main +1.01% (7/8), big-index +1.66% (6/8), large-files -0.35% (4/8), remote +0.83% (8/8) (section 6.2 for the many-directory case it was made for) | remote: yes. The other three: unresolved |
| the version string | `ls --json` (start-up) | big-index +0.41% (7 of 12 pairs); main -0.18% (3/8), large-files -0.01% (4/8), remote +0.02% (4/8) | unresolved; no difference established |
| the nine red-team fixes, the hand-over commit and the version string together | the whole suite | the table "net price" below: a difference between two official 44-round jobs | not a paired measurement; each job has its own interval |

The red-team rows are bench figures from the merge agent's step log
([`../reviews/merge/FINAL-MERGE-STEP-LOG.md`](../reviews/merge/FINAL-MERGE-STEP-LOG.md), entries of 2026-10-07 16:22Z
to 16:46Z): the tree with the nine fixes against the tree before them, 8 fixed pairs per cell, 4 cores. The first set
was not taken on a silent machine; the fix 7 and version-string cells were.

**The net price of the eleven commits between the second final tree and the judged commit, from two official jobs:**

<!-- BEGIN GENERATED:net -->
Before: the second final tree (`rc-7`), the tree the last independent review was given. After: the judged commit. Between them: the nine fixes that followed that review, the change to restore's hand-overs, and the version string.

|  | before: job `940c4e64`, 44 rounds | after: job `50388f4e`, 44 rounds | difference, points |
|---|---|---|---|
| suite time vs upstream | +29.08% (44/44; +29.01..+29.19) | +29.00% (44/44; +28.84..+29.05) | -0.08 |
| main, time vs upstream | +30.73% (11/11; +30.62..+30.94) | +30.63% (11/11; +30.37..+30.78) | -0.10 |
| big-index, time vs upstream | +45.53% (11/11; +45.36..+45.56) | +45.13% (11/11; +44.96..+45.21) | -0.39 |
| large-files, time vs upstream | +22.55% (11/11; +22.46..+22.84) | +22.96% (11/11; +22.60..+23.10) | +0.41 |
| remote, time vs upstream | +17.50% (11/11; +17.37..+17.95) | +17.26% (11/11; +17.10..+17.44) | -0.24 |
| suite memory vs upstream | +39.10% (44/44) | +38.99% (44/44) | -0.11 |
| suite time vs S020 | -0.79% (6/44; -1.07..-0.65) | -1.06% (6/44; -1.13..-0.91) | -0.27 |
| main, time vs S020 | -0.69% (0/11; -0.98..-0.38) | -0.83% (1/11; -1.03..-0.74) | -0.14 |
| big-index, time vs S020 | +0.05% (6/11; -0.26..+0.43) | -0.18% (5/11; -0.31..+0.09) | -0.23 |
| large-files, time vs S020 | -1.40% (0/11; -2.00..-1.22) | -1.07% (0/11; -1.34..-0.67) | +0.33 |
| remote, time vs S020 | -1.11% (0/11; -1.80..-0.97) | -2.18% (0/11; -2.26..-2.02) | -1.06 |
| suite memory vs S020 | -9.04% (0/44) | -8.65% (0/44) | +0.39 |

The net price of those commits, read off two jobs: **-0.08 suite points against upstream and -0.27 against S020.** This is a difference between two separate jobs, each with its own interval, not a paired measurement: the two trees never ran in the same round. Cells show rounds won and the 90% interval of each job.
<!-- END GENERATED:net -->

The per-workload differences in that table are small against the noise of a single workload in a single job and point
in different directions (large-files reads better after the fixes, the other three worse). Only `remote` against S020
moves by about a point. The bench rows above say where a real cost is expected: `check` on a large index.

**All official jobs of the final work:**

<!-- BEGIN GENERATED:arms -->
| job | id | rounds | candidate vs reference | suite time vs reference (90% interval; wins) | main / big-index / large-files / remote | memory vs reference | suite time vs upstream (90% interval; wins) | memory vs upstream |
|---|---|---|---|---|---|---|---|---|
| price of the repairs, as first staged | `3c21cf80` | 12 | repaired tree vs league tree after S019 | -3.15% (-3.50..-2.77; 0/12) | -3.52 / -2.09 / -4.59 / -2.42 | -8.05% | +26.90% (+26.72..+27.30; 12/12) | +38.84% |
| raw-rule arm (final merge before F2) | `aca189ed` | 12 | merge candidate `rc-3` vs S020 incumbent | -0.12% (-0.69..+0.28; 7/12) | +0.04 / +0.76 / +0.14 / -1.41 | -3.43% | +29.85% (+29.47..+30.23; 12/12) | +41.72% |
| first final tree, reference length | `757b745c` | 12 | `rc-5` (with F2) vs S020 incumbent | -0.91% (-1.13..-0.59; 1/12) | -1.50 / -0.33 / -1.16 / -0.65 | -9.75% | +29.38% (+29.26..+29.50; 12/12) | +38.07% |
| first final tree, promotion length | `c87fa304` | 44 | `rc-5` vs S020 incumbent | -0.99% (-1.12..-0.76; 7/44) | -0.98 / +0.02 / -1.23 / -1.74 | -9.90% | +29.46% (+29.33..+29.56; 44/44) | +38.26% |
| second final tree, restore tree-ID bound 65,536 | `f6765696` | 12 | `rc-6` vs S020 incumbent | -1.02% (-1.29..-0.80; 2/12) | -1.32 / +0.05 / -1.12 / -1.67 | -9.48% | +29.41% (+29.30..+29.49; 12/12) | +39.08% |
| second final tree (the tree the last review was given), reference length | `d5c8c004` | 12 | `rc-7` vs S020 incumbent | -1.00% (-1.15..-0.62; 1/12) | -1.34 / -0.04 / -1.07 / -1.54 | -9.54% | +29.28% (+29.21..+29.35; 12/12) | +38.34% |
| second final tree, promotion length | `940c4e64` | 44 | `rc-7` vs S020 incumbent | -0.79% (-1.07..-0.65; 6/44) | -0.69 / +0.05 / -1.40 / -1.11 | -9.04% | +29.08% (+29.01..+29.19; 44/44) | +39.10% |
| the judged commit of this release, reference length | `9fc9eafd` | 12 | `rc-8` vs S020 incumbent | -1.07% (-1.27..-0.91; 2/12) | -0.70 / +0.03 / -1.43 / -2.17 | -8.85% | +29.33% (+29.04..+29.45; 12/12) | +38.56% |
| **the judged commit of this release, promotion length** | `50388f4e` | 44 | `rc-8` vs S020 incumbent | -1.06% (-1.13..-0.91; 6/44) | -0.83 / -0.18 / -1.07 / -2.18 | -8.65% | +29.00% (+28.84..+29.05; 44/44) | +38.99% |

All jobs: official judge lanes, 4 CPUs pinned, the same four workloads. Positive = the candidate needs less than the tree it is compared with. The result files are in `league/evidence/judge/` under the names in `index.json`.
<!-- END GENERATED:arms -->

Two more job ids exist and measured nothing: `5d43382d` failed at start (a mangled path) and `ef51c559` was stopped
in its warm-up (its candidate was on the slow key-derivation alignment, see METHODS.md section 4). Their result files
are not published.

Sections 5.1 to 5.6 quote the merge agent's report,
[`../reviews/merge/FINAL-MERGE-REPORT.md`](../reviews/merge/FINAL-MERGE-REPORT.md), written for the first final tree
`rc-5`. Its bench figures are not official judge results: they were taken on the research CPUs with fixed pairs of
runs (5 or 10 pairs per cell), the same four workloads and the same oracle.

### 5.1 The repairs, as first staged

Job `3c21cf80` (12 rounds, 347 of 347 units verified): the repaired tree against the league tree after S019 read
-3.15% on the suite (0 of 12 wins; interval -3.50..-2.77) and +26.90% against upstream (12 of 12). Per workload:
main -3.52, big-index -2.09, large-files -4.59, remote -2.42. The largest cells were the first backup (main -7.10%,
big-index -5.00%, large-files -10.28%, remote -2.47%) and forget-prune (main -6.16%, big-index -2.45%, large-files
-11.03%, remote -7.94%).

Two costs inside that figure were not the price of soundness, and both were removed afterwards:

- Repair R4 as first written let the Zstandard encoder grow its output buffer block by block. Profile of the main
  workload's first backup: memmove +0.54 s, madvise +0.24 s and 170 more garbage collections, against +0.74 s in the
  encoder itself. Commit "R4 (cost)" allocates the buffer once.
- Repair R3's parameter checks were inlined in front of the key derivation's inner loop and moved it onto a slower
  code alignment: about 7 ms in every restic process. Commit "R3 (layout)" moved the checks out. This is a property
  of one build; see METHODS.md section 4.

### 5.2 Which repair costs where (bench, research CPUs, 10 fixed pairs unless stated)

| repair | where it costs | measured |
|---|---|---|
| R1, validation after the lock is confirmed | every process on `remote`: two listings after confirmation | remote pass 32.05 to 32.41 s (-1.14%); `ls` -4.36% (beyond the band); one or two more requests per process. Local workloads: main -0.15%, big-index +0.02%, large-files -0.21% (5 pairs, unresolved) |
| R2, pack handlers run after the backend call returned | forget-prune on `remote` | quiet machine: 3.410 to 3.524 s (-3.27%, 0 of 10), requests 119 to 134, peak +4.9 MiB; local workloads +0.27 / +0.30 / +1.39% (unresolved) |
| R4, every blob is encoded again (first form) | first backup and prune | main first backup -8.30% (+29.4 MiB), large-files -10.99% (+22.8 MiB) |
| R4 in its final form | the same | against the tree with the sampler: main first backup -2.39% (+8.8 MiB), large-files -2.57% (+8.4 MiB); forget-prune, quiet: main -1.96%, big-index -1.40%, large-files -3.83%, remote +0.36% |
| F2, upstream's stored form | first backup and prune | -0.70 suite points (section 5.4) |
| R5 | index load on `remote` | +0.41% (unresolved), +1.7 MiB |
| R7 | tree decode | main: unchanged backup +0.21, restore -0.49, check -0.33, ls -0.12 (all unresolved) |
| R3, R6, R8, R9, R10, R11, M22 | | not priced singly; inside the totals |

### 5.3 The repairs in their final form, without anything else

A repairs-only branch (the repaired tree, R4 in its final form, the R3 layout commit and F2; no S020 piece and no
port) against the league tree after S019, 5 fixed pairs per cell, forget-prune repeated on a quiet machine with 10:

| | main | big-index | large-files | remote | mean |
|---|---|---|---|---|---|
| pass time | -2.00% (15.69 to 16.00 s) | -1.72% | -2.55% | -1.66% | **-1.98** |
| first backup | -6.07% | -3.51% | -7.43% | -1.71% | |
| forget-prune, quiet | -2.99% (0/10) | -2.50% (1/10) | -6.54% (0/10) | -6.23% (0/10) | |
| summed phase peaks | +71.9 MiB | +84.8 MiB | +39.2 MiB | +68.0 MiB | |

One small reading in that tree is unattributed: `ls --json` reads -0.6 to -0.7% on main and large-files after the
layout commit (2 to 6 ms per process, 5 pairs, one cell beyond its band).

### 5.4 What upstream's stored form costs

The league allowed a blob to be stored uncompressed when compression did not shrink it (a ruling of season S004).
Release check C11 showed the consequence: `prune --repack-uncompressed` never came to rest on such a repository,
because the blobs stored raw count as uncompressed (failure F2 in [SOUNDNESS.md](SOUNDNESS.md)). The tree now stores
every blob as upstream does. The two official 12-round jobs in the table above differ in that rule only (`rc-3` with
the raw rule, `rc-5` without): the official difference is 0.79 suite points. A direct bench of the same two trees
(10 fixed pairs per cell, forget-prune repeated on a quiet machine) read main -0.94%, big-index -0.14%, large-files
-1.41%, remote -0.32%, mean -0.70, and summed phase peaks +43.9 / +51.6 / +27.5 / +31.7 MiB. Cells beyond their band:
first backup main -2.34%, big-index -1.62%, large-files -4.79%.

The cause, as the report gives it: a blob stored in compressed form is verified on save through the compressed path (a
decrypted copy and, below 2 MiB, a decoded copy). A raw blob was decrypted and hashed as a stream with no buffer. A
streaming form of the compressed verification would keep upstream's stored form and the same check. **It was not
built**: it would be new code in the verification-on-save path, and it was left for a later release.

### 5.5 Memory against S020

At 44 rounds the earlier final tree `rc-5` (job `c87fa304`) read -9.90% memory against S020 on the suite: main
-13.59%, big-index -5.03%, large-files -9.98%, remote -11.02%. Summed over the seven phases: +76.1 / +78.9 / +53.9 /
+57.9 MiB. The league's own promotion rule allows 2% more memory than the incumbent, so the judge's league verdict
on that job was "not eligible" (below S020 on time, 7 wins where 29 are required, memory past the guard). The
verdict on the tree in this repository is printed in section 1.2.

The merge report nets the repairs' measured memory price (section 5.3) out of the difference and finds +4.2 MiB on
main, -5.9 on big-index, +14.7 on large-files and -10.1 on remote, against a 2% allowance of 11.4 / 31.5 / 10.9 /
12.2 MiB: **large-files is over the allowance by about 4 MiB on that arithmetic.** The report calls this "a possible
miss; nothing in the tree is known to cause it", and notes that the exemption is a 5-pair bench figure. Without the
exemption the 2% guard is exceeded on every workload, by the repairs.

### 5.6 What each added piece measured, alone

All bench figures, fixed pairs, research CPUs, from the same report.

| piece | measured |
|---|---|
| S020's kept pieces (index-side work in check and prune, the metadata tree decoder) | on the repaired tree, 5 pairs, beside other jobs: big-index check 3.630 to 3.160 s (+13.87%, 5/5); big-index forget-prune +25.39% (5/5) beside other jobs, where S020's own quiet figure is +6.70%; large-files pass -0.12% |
| R4 in its final form, against the repaired tree | main first backup 5.442 to 5.125 s (+5.64%, 10/10), peak -15.7 MiB; large-files first backup +7.34% (10/10), peak -22.3 MiB |
| A3, check's extra worker only where the backend grants a slot | remote check 6.845 to 6.761 s (+0.56%, 8/12, unresolved), peak -6.6 MiB; taken as a correctness fix |
| A16, repack reads one pack ahead | forget-prune, quiet, 10 pairs: big-index 2.300 to 2.223 s (+3.62%, 10/10), large-files 1.228 to 1.100 s (+10.36%, 10/10), remote +0.57% (6/10, unresolved); main +4.33% and +4.01% (10/10 each) in two runs that each overlapped other work for seconds |
| A14, a backup item's line is formatted only when printed | three backups, 10 pairs: main 8.87 to 8.80 s (+0.84%), big-index +0.99%, remote +0.66%, large-files -0.06% |
| A5, a prepared key keeps its expanded AES keys | big-index check 3.182 to 3.046 s (+4.83%, 10/10); passes: main +0.26%, big-index +0.66%, large-files -0.02%, remote +0.72%; micro: seal and open of a 300-byte blob 988 to 571 ns |
| R3 layout | `ls --json`, same code but for where the checks sit: large-files 0.452 to 0.445 s (+1.36%, 10/10), main +1.21% (9/10) |

### 5.7 What the red-team fixes cost

The nine fixes that followed the last independent review ([RED-TEAM.md](RED-TEAM.md)) change error paths, bounds and
the order of a few reads. Three of them are on a path the judge times. All figures: the tree with the nine fixes
against the tree before them (`rc-7`), 8 fixed pairs per cell, 4 cores, the builders' bench tool; the first two rows
were measured while other work ran on the machine.

| fix | cell | time saved | pairs won | band | verdict |
|---|---|---|---|---|---|
| fix 6: `check` takes upstream's lock call and reads after confirmation | check, big-index | -5.31% | 0 of 8 | | beyond noise |
| | check, main | -0.98% | 0 of 8 | | unresolved |
| | check, large-files | -1.26% | 3 of 8 | | unresolved |
| | check, remote | -0.98% | 1 of 8 | | unresolved; 90 requests against 92 |
| fix 4: the validating listings are taken where upstream lists | restore, remote | -0.30% | 1 of 8 | 0.3 | unresolved; 91 requests against 91 |
| fix 7: prune reads a kept index file raw and compares its hash with its name | forget-prune, big-index (quiet machine) | -1.08% | 1 of 8 | 1.15 | unresolved |

Fix 6 is the one that costs measurably, and where: `check` on a repository with a large index used to load the index
during the 200 ms lock wait and now loads it afterwards, as upstream does. On the big-index workload that is 5.31%
of the check phase, or 0.83 points of that workload's pass. It was accepted because a check that can miss damage is
worth less than a faster one.

The two other commits made at the same time:

- **Restore hands work between its goroutines per batch.** Made for restores of very many directories (section
  6.2). On the four workloads' restore cells, against the tree with the nine fixes, 8 fixed pairs, measured beside
  other runs: main +1.01% (7 of 8; inside the band), big-index +1.66% (6 of 8; unresolved), large-files -0.35% (4 of
  8; unresolved), remote +0.83% (8 of 8; beyond noise).
- **The version string became a variable.** `ls --json`, which is nearly all start-up, against the tree without the
  commit: main -0.18% (3 of 8), large-files -0.01% (4 of 8), remote +0.02% (4 of 8); big-index read -1.23% (1 of 8;
  band 1.71) beside two other jobs and +0.41% (7 of 12) when repeated on a quiet machine. Unresolved; no difference established. The
  key-derivation loop stayed on the fast alignment (METHODS.md, section 4).

Against all of these stands one official comparison: the 44-round job on `rc-8` and the 44-round job on `rc-7`,
both against S020, in the "net price" table at the head of this section. They are two separate jobs, so their
difference carries the noise of both.

## 6. More than 4 CPUs, and another frequency governor

Every league timing was taken on 4 pinned cores, and every one of them, the official 44 rounds included, on CPUs set
to the `performance` frequency governor, which holds a CPU at its top clock whether it is busy or not. The league
never scored anything on more cores or under another governor. Most machines do not run that way: they let the clock
follow the load. This section is what was measured afterwards, on the same machine, to find out whether the gains
hold when restic is given 8 or 16 cores, and what happened in the one case where they did not. **Nothing in it is a
judge score.**

Sources: the merge agent's record [`../reviews/merge/MANY-CORE.md`](../reviews/merge/MANY-CORE.md) and the data file
parsed from it, `league/evidence/many-core.json`; the per-pair files of the 8- and 16-core bench,
`league/evidence/many-core-rc9/`; the release-check harness's records,
`league/evidence/release-checks-final3-b8cf439/`; the machine, `league/evidence/machine.txt`.

### 6.1 The four workloads at 8 and 16 cores

<!-- BEGIN GENERATED:many-core -->
Measured on `rc-9` (working commit `8f9372b`: the published commit built with the judge's flags: the same machine code as the judged binary). 4 cores: official judge job `50388f4e` on the judged commit `b8cf439`, 44 rounds, 11 per workload, every cell 11 of 11. 8 and 16 cores: the builders' bench tool, 5 fixed pairs per cell, whole physical cores without their SMT siblings, GOMAXPROCS equal to the number of cores, performance governor, no other league job running (the release-check harness was at work on its own cores); every cell 5 of 5 at both core counts. **At 8 and 16 cores the pass rows are the bench tool's estimate (each phase's saving weighted by upstream's share of the time); they are not a judge score.**

| workload | phase | 4 cores (judge) | 8 cores (bench, 5 pairs) | 16 cores (bench, 5 pairs) |
|---|---|---|---|---|
| main | backup-initial | +7.17 | +6.03 | +11.38 |
| main | backup-unchanged | +23.96 | +24.77 | +25.27 |
| main | backup-mutated | +22.36 | +24.19 | +24.41 |
| main | restore | +48.76 | +46.72 | +45.95 |
| main | check-read-data | +32.45 | +33.85 | +34.19 |
| main | forget-prune | +44.60 | +49.04 | +50.16 |
| main | ls-json | +42.33 | +41.88 | +42.38 |
| main | **pass** | +30.63 | +31.47 | +33.03 |
| big-index | backup-initial | +17.33 | +15.43 | +15.86 |
| big-index | backup-unchanged | +34.23 | +31.32 | +31.82 |
| big-index | backup-mutated | +31.59 | +29.59 | +29.81 |
| big-index | restore | +52.06 | +49.85 | +49.61 |
| big-index | check-read-data | +39.78 | +40.39 | +43.74 |
| big-index | forget-prune | +70.75 | +71.62 | +72.33 |
| big-index | ls-json | +53.00 | +50.35 | +51.75 |
| big-index | **pass** | +45.13 | +44.99 | +46.27 |
| large-files | backup-initial | +11.15 | +17.83 | +28.28 |
| large-files | backup-unchanged | +26.96 | +36.33 | +38.36 |
| large-files | backup-mutated | +16.20 | +26.73 | +31.81 |
| large-files | restore | +20.73 | +18.73 | +18.96 |
| large-files | check-read-data | +27.61 | +28.78 | +30.08 |
| large-files | forget-prune | +45.80 | +50.13 | +48.67 |
| large-files | ls-json | +38.78 | +38.66 | +38.56 |
| large-files | **pass** | +22.96 | +28.86 | +32.11 |
| remote | backup-initial | +6.83 | +6.58 | +8.57 |
| remote | backup-unchanged | +21.15 | +21.67 | +22.48 |
| remote | backup-mutated | +18.44 | +19.75 | +22.37 |
| remote | restore | +30.18 | +30.07 | +30.76 |
| remote | check-read-data | +5.55 | +6.16 | +5.51 |
| remote | forget-prune | +11.98 | +13.24 | +13.38 |
| remote | ls-json | +36.92 | +37.08 | +37.41 |
| remote | **pass** | +17.26 | +17.64 | +18.51 |

Smallest cell at 8 or 16 cores: remote check-read-data, +5.51%.

User and system CPU in the five cells the bench record lists because this build's **user** CPU is higher there than upstream's (CPU seconds, mean of the five pairs, this build / upstream; the user + system columns add the two displayed figures). Higher user CPU does not always mean higher total CPU: in several of these cells the build's system CPU is lower by as much.

| cell | user, 8 cores | system, 8 cores | user + system, 8 cores | user, 16 cores | system, 16 cores | user + system, 16 cores |
|---|---|---|---|---|---|---|
| main restore | 6.3 / 5.6 | 2.8 / 3.6 | 9.1 / 9.2 | 6.9 / 5.7 | 3.0 / 3.7 | 9.9 / 9.4 |
| large-files restore | 3.4 / 2.6 | 1.8 / 1.8 | 5.2 / 4.4 | 3.5 / 2.7 | 1.8 / 1.7 | 5.3 / 4.4 |
| remote restore | 6.1 / 5.5 | 3.0 / 3.8 | 9.1 / 9.3 | 6.4 / 5.7 | 3.1 / 3.9 | 9.5 / 9.6 |
| main forget-prune | 3.0 / 2.6 | 0.6 / 0.6 | 3.6 / 3.2 | 3.3 / 2.8 | 0.6 / 0.7 | 3.9 / 3.5 |
| main ls-json | 1.4 / 1.3 | 0.1 / 0.2 | 1.5 / 1.5 | 1.5 / 1.3 | 0.1 / 0.2 | 1.6 / 1.5 |
<!-- END GENERATED:many-core -->

What the table shows: on these four workloads no phase was slower than upstream at 8 or at 16 cores (56 cells, each
ahead in 5 of 5 pairs), and the large-files workload gained more with more cores. What it does not show: a judge
score (no oracle per round, no rotation of three arms, five pairs); other machines; more than 16 cores; SMT siblings
in use; any governor but `performance`.

The CPU table is the warning in it. In restore, forget-prune and ls the build finishes sooner than upstream and
uses more **user** CPU doing so, and the gap widens with the number of cores. Its system CPU is lower in several of
those cells, so the total of user and system is higher than upstream's in some of them and equal or lower in others;
the table gives both. In the four workloads that costs nothing visible, because their restores are dominated by file
data. Section 6.2 is the case where it did cost.

The same bench was first run on the second final tree, before the nine red-team fixes and the change to restore's
hand-overs. It is kept because documents written before the re-measurement quote it:

<!-- BEGIN GENERATED:many-core-earlier -->
**An earlier reading, not a measurement of the published build.** Measured on `rc-7` (working commit `d4f7e81`: the second final tree, before the nine red-team fixes and the change to restore's hand-overs). 4 cores: official judge job `940c4e64` on that tree, 44 rounds. 8 and 16 cores: the same CPU sets and governor, 5 fixed pairs per cell, every cell 5 of 5. **At 8 and 16 cores the pass rows are the bench tool's estimate (each phase's saving weighted by upstream's share of the time); they are not a judge score.**

| workload | phase | 4 cores (judge) | 8 cores (bench, 5 pairs) | 16 cores (bench, 5 pairs) |
|---|---|---|---|---|
| main | backup-initial | +7.22 | +3.38 | +12.92 |
| main | backup-unchanged | +23.98 | +24.95 | +25.03 |
| main | backup-mutated | +22.12 | +23.96 | +24.31 |
| main | restore | +47.51 | +45.14 | +44.41 |
| main | check-read-data | +35.88 | +36.03 | +35.87 |
| main | forget-prune | +45.39 | +48.13 | +50.30 |
| main | ls-json | +42.59 | +42.42 | +42.75 |
| main | **pass** | +30.73 | +30.87 | +33.10 |
| big-index | backup-initial | +17.55 | +11.06 | +16.31 |
| big-index | backup-unchanged | +34.16 | +31.26 | +31.41 |
| big-index | backup-mutated | +32.21 | +30.44 | +29.65 |
| big-index | restore | +51.49 | +48.48 | +47.45 |
| big-index | check-read-data | +42.24 | +43.57 | +44.29 |
| big-index | forget-prune | +71.61 | +71.95 | +72.31 |
| big-index | ls-json | +52.84 | +51.05 | +52.14 |
| big-index | **pass** | +45.53 | +44.75 | +45.92 |
| large-files | backup-initial | +10.91 | +16.66 | +28.01 |
| large-files | backup-unchanged | +27.28 | +36.81 | +38.62 |
| large-files | backup-mutated | +16.07 | +27.11 | +31.91 |
| large-files | restore | +21.10 | +18.79 | +18.89 |
| large-files | check-read-data | +22.13 | +31.35 | +30.93 |
| large-files | forget-prune | +46.79 | +50.30 | +48.89 |
| large-files | ls-json | +38.79 | +38.65 | +38.51 |
| large-files | **pass** | +22.55 | +29.10 | +32.19 |
| remote | backup-initial | +6.05 | +6.26 | +7.07 |
| remote | backup-unchanged | +22.40 | +22.85 | +23.53 |
| remote | backup-mutated | +20.44 | +20.41 | +22.02 |
| remote | restore | +29.50 | +29.96 | +30.11 |
| remote | check-read-data | +6.56 | +7.47 | +6.37 |
| remote | forget-prune | +13.75 | +14.80 | +15.03 |
| remote | ls-json | +37.11 | +37.38 | +37.34 |
| remote | **pass** | +17.50 | +18.10 | +18.40 |

Smallest cell at 8 or 16 cores: main backup-initial, +3.38%.

User and system CPU on that earlier tree, the same five cells:

| cell | user, 8 cores | system, 8 cores | user + system, 8 cores | user, 16 cores | system, 16 cores | user + system, 16 cores |
|---|---|---|---|---|---|---|
| main restore | 6.3 / 5.6 | 2.9 / 3.6 | 9.2 / 9.2 | 6.9 / 5.8 | 3.0 / 3.7 | 9.9 / 9.5 |
| large-files restore | 3.4 / 2.6 | 1.9 / 1.8 | 5.3 / 4.4 | 3.6 / 2.7 | 1.9 / 1.8 | 5.5 / 4.5 |
| remote restore | 6.2 / 5.6 | 3.0 / 3.8 | 9.2 / 9.4 | 6.5 / 5.7 | 3.1 / 3.9 | 9.6 / 9.6 |
| main forget-prune | 3.0 / 2.6 | 0.6 / 0.6 | 3.6 / 3.2 | 3.4 / 2.8 | 0.6 / 0.6 | 4.0 / 3.4 |
| main ls-json | 1.4 / 1.3 | 0.1 / 0.2 | 1.5 / 1.5 | 1.5 / 1.3 | 0.1 / 0.2 | 1.6 / 1.5 |
<!-- END GENERATED:many-core-earlier -->

### 6.2 A restore of very many directories

**What the release check saw.** Release check C10 and its diagnostic x11 restored a snapshot of 606,060 directories
and three files with the second final tree (`rc-7`), three rounds in rotated order, on the check harness's CPUs
([`../reviews/checks/STEP-LOG.md`](../reviews/checks/STEP-LOG.md), entries of 2026-10-07 14:26Z and 14:56Z):

| CPUs | upstream | `rc-7` | |
|---|---|---|---|
| 4 cores | 86.7 to 89.1 s | 77.8 to 79.7 s | about 10% faster |
| 16 CPUs | 67.1 to 68.0 s (user 28 s, system 42 s) | 85.9 s (user 52.5 s, system 66.2 s) | about 27% slower |

Larger trees with 16 CPUs: 1,010,100 directories 113.5 s against 137.0 s (21% slower); 2,020,200 directories 226.8
s against 263.4 s (16% slower).

**What the merge agent reproduced, and did not.** The same shape, `rc-7` against upstream, two runs each in
alternating order, on quiet whole cores of the bench lanes: the 27% loss was **not reproduced**. `rc-7` stayed ahead
of upstream at every core count (restore target on tmpfs: 28.9 s against 34.4 s on 4 cores, 31.8 s against 35.1 s on
16; target on ext4: 48.6 s against 52.0 s on 4 CPUs, 50.4 s against 52.6 s on 16 logical CPUs). What did show is the
same effect in smaller form: `rc-7` lost 10% going from 4 to 16 cores while upstream stayed flat, and its user CPU
grew with the core count while upstream's did not. So the bench lanes showed the direction of the effect and not
its size. The reason was found later and is at the end of this section: the two harnesses ran under different
frequency governors.

**The mechanism.** Both passes of restore passed every directory through another goroutine and back: in the first
pass one request and one reply per directory node, in the second pass one prepared tree at a time through a queue of
one. Each hand-over puts one goroutine to sleep and wakes another, and every wake-up sets idle CPUs searching for
work: 1.3 million voluntary context switches against upstream's 0.09 million, about two per directory.

**The change** (commit "Many directories: restore hands work between its goroutines per batch, not per directory"):
in the first pass the traversal decodes directory nodes itself; in the second pass prepared trees are handed over in
batches of up to 64 trees and 256 KiB. Order, errors, the fallback and the byte budget are unchanged; an independent
reviewer agent compared 300 random restore scenarios before and after and found every trace identical
([RED-TEAM.md](RED-TEAM.md), section 5).

<!-- BEGIN GENERATED:dirs -->
Restore of 606,060 directories and three small files; snapshot written by upstream; two runs each on the bench lanes, governor: performance. Cells: wall seconds / user CPU seconds / voluntary context switches (M = million). "Before" is the tree with the nine fixes and without the hand-over commit, except where a cell says `rc-7`, which has the same restore code; "after" adds that commit.

| set-up | before | after | upstream | after against upstream, wall time |
|---|---|---|---|---|
| tmpfs, 4 cores | 28.7 / 19.9 / 1.30M | 25.8 / 16.5 / 0.09M | 34.4 / 17.8 / 0.09M | +25.0% |
| tmpfs, 8 cores | 30.5 / 22.5 / 1.32M | 26.7 / 18.4 / 0.11M | 35.0 / 18.4 / 0.09M | +23.7% |
| tmpfs, 16 cores | 31.6 / 23.6 / 1.28M | 27.4 / 19.1 / 0.11M | 35.6 / 18.2 / 0.08M | +23.0% |
| ext4, 4 CPUs | 47.6 / 22.5 / 1.86M | 41.8 / 17.5 / 0.11M | 51.7 / 18.9 / 0.14M | +19.1% |
| ext4, 16 logical CPUs (8 cores with SMT) | 50.4 / 27.0 / 1.75M (rc-7) | 43.0 / 20.0 / 0.14M | 51.7 / 18.8 / 0.12M | +16.8% |
<!-- END GENERATED:dirs -->

After the change the merge agent's harness reads the restore 17% to 25% faster than upstream at 4, 8 and 16 cores,
with about upstream's user CPU and about upstream's number of context switches. A rise with the core count remains
(25.8 s to 27.4 s on tmpfs).

**The re-check by the harness that saw the loss.** The release-check harness measured the same restore again on the
judged commit (`rc-8`): 606,060 directories and 3 files, snapshot written by upstream, three rounds in rotated order,
target removed and `sync -f` before every restore, restore target on ext4, started 18:27 UTC
([`../reviews/checks/STEP-LOG.md`](../reviews/checks/STEP-LOG.md), entry of 2026-10-07 18:57Z; recorded as F6 in
[`../reviews/checks/FAILURES.md`](../reviews/checks/FAILURES.md), "performance observation, not a correctness
failure"). "All 16 check CPUs" are 8 cores and their SMT siblings; "4 cores" are four of those cores. The governor of
those CPUs was `schedutil`, which follows the load.

<!-- BEGIN GENERATED:dirs-recheck -->
| CPUs | binary | wall s, rounds 1 to 3 | median wall s | against upstream | user + system CPU s (medians) | CPU s of a run (median) | peak RSS MiB (largest) |
|---|---|---|---|---|---|---|---|
| all 16 check CPUs | upstream | 66.5 / 68.0 / 67.2 | 67.2 |  | 28.6 + 41.9 | 70.5 | 57.0 |
| all 16 check CPUs | judged commit (`rc-8`) | 73.0 / 74.7 / 74.0 | 74.0 | 10.1% slower | 39.4 + 57.8 | 97.1 | 58.1 |
| all 16 check CPUs | before the hand-over commit (`rc-7`) | 85.5 / 85.6 / 84.1 | 85.5 | 27.2% slower | 52.5 + 65.8 | 118.4 | 57.5 |
| 4 cores | upstream | 86.5 / 86.7 / 86.5 | 86.5 |  | 39.0 + 53.9 | 92.8 | 62.2 |
| 4 cores | judged commit (`rc-8`) | 63.4 / 63.3 / 63.0 | 63.3 | 26.8% faster | 31.7 + 47.6 | 79.3 | 56.2 |
| 4 cores | before the hand-over commit (`rc-7`) | 76.1 / 78.8 / 77.6 | 77.6 | 10.3% faster | 43.6 + 58.6 | 102.2 | 57.0 |

From `league/evidence/release-checks-final3-b8cf439/x11-606060/runs.json`, every run. The harness recorded seconds to one decimal. "CPU s of a run" is the median over the three rounds of user plus system of one run, so it can differ by 0.1 from the sum of the two medians beside it.
<!-- END GENERATED:dirs-recheck -->

What it shows:

- **The commit removed most of the loss, not all of it.** In this set-up with 16 CPUs the restore went from 27.2%
  slower than upstream to 10.1% slower.
- **The sign depends on the CPU count.** Upstream gets faster with more CPUs (86.5 s to 67.2 s); this build gets
  slower (63.3 s to 74.0 s).
- **With 16 CPUs this build uses more CPU time than upstream, user and system both**; with 4 cores it uses less of
  both. The table has the figures.
- It is not an order effect: each binary read the same in all three positions.
- Peak memory is within 1.1 MiB of upstream's with 16 CPUs and 6 MiB below it with 4 cores.

**Larger trees on the same harness under the same governor.**

<!-- BEGIN GENERATED:dirs-large -->
1,010,100 directories, three rotated rounds with all 16 check CPUs, from the run of the diagnostic that was stopped before its 4-core part was complete (`x11-large-partial/runs.json`):

| CPUs | binary | wall s, rounds 1 to 3 | median wall s | against upstream | user + system CPU s (medians) | CPU s of a run (median) | peak RSS MiB (largest) |
|---|---|---|---|---|---|---|---|
| all 16 check CPUs | upstream | 111.9 / 109.9 / 110.7 | 110.7 |  | 46.7 + 69.9 | 116.6 | 57.8 |
| all 16 check CPUs | judged commit (`rc-8`) | 119.3 / 119.3 / 118.2 | 119.3 | 7.8% slower | 58.7 + 86.6 | 145.4 | 60.8 |
| all 16 check CPUs | before the hand-over commit (`rc-7`) | 134.4 / 135.9 / 134.5 | 134.5 | 21.5% slower | 75.9 + 98.4 | 174.3 | 57.3 |

Its 4-core part finished one round for two binaries: upstream 143.7 s, judged commit 100.4 s (30.1% faster) (`x11-large-partial/rows-completed-before-stop.json`).

2,020,200 directories and 10 files, all 16 check CPUs, **one run per binary**, not rotated (release check C10, part B; `c10/summary.json`): upstream 221.6 s, judged commit 232.3 s (4.8% slower), `rc-7` 261.3 s (17.9% slower); peak RSS 56.2 / 58.0 / 58.6 MiB.
<!-- END GENERATED:dirs-large -->

**Why this harness read a loss and the bench lanes did not.** The merge agent's own run of the same restore with
16 logical CPUs (8 cores and their SMT siblings, target on ext4) came out faster than upstream: 43.0 s against 51.7 s
(last row of the before-and-after table). The release-check harness, also with 8 cores and their SMT siblings and a
target on ext4, read 10.1% slower. At 4 cores both read this build well ahead of upstream. The two set-ups differ in
three things: which cores they use, the frequency governor of those cores, and what ran beside them. Each was tested.
The outcome: with the same governor the two set-ups agree.

**Tested and ruled out: the cache layout.** The check harness's 8 cores are two from each of four L3 cache domains;
the bench lanes use neighbouring cores. The merge agent ran the published machine code (the published commit built
with the judge's flags; `rc-9` in the table) against upstream on 8 cores with their SMT siblings, 606,060 directories,
target on ext4, three rotated rounds per row. The runs used the bench lanes with the `performance` governor: once on
neighbouring cores, once spread over four L3 domains as the check's CPUs are, and once spread and reading the
repository the way the check does. Evidence: `league/evidence/many-core-rc9/l3dirs.log`.

<!-- BEGIN GENERATED:dirs-l3 -->
| 8 cores with their SMT siblings (governor: performance) | upstream | rc-9 | rc-9 against upstream |
|---|---|---|---|
| compact: 4 + 4 cores of two L3 domains (26-29, 30-33) | 52.7, 52.8, 54.9 (19.7 / 33.6 / 0.12M) | 42.8, 43.7, 44.0 (20.1 / 33.3 / 0.14M) | 17% faster |
| spread: 2 cores from each of four L3 domains (12, 13, 19, 20, 26, 27, 36, 37) | 51.0, 52.6, 52.6 (19.6 / 33.0 / 0.12M) | 42.9, 43.5, 43.6 (20.7 / 33.0 / 0.14M) | 17% faster |
| spread, with the repository on ext4 and `--no-cache`, as the check runs it | 56.1, 56.2, 56.8 (22.7 / 34.7 / 0.13M) | 47.0, 47.5, 47.7 (24.3 / 34.3 / 0.15M) | 16% faster |

Cells: wall seconds of the three rounds (user CPU s / system CPU s / voluntary context switches of the median round, as the record gives them).
<!-- END GENERATED:dirs-l3 -->

Spread over four L3 domains the restore is as fast as on neighbouring cores, and this build is 16% to 17% faster
than upstream in all three rows. The placement of the CPUs does not explain the loss.

**Tested and found to be the condition of the loss: the frequency governor.** The check harness's 16 CPUs ran
`schedutil`, a governor that follows the load (driver acpi-cpufreq, 1,500 to 2,750 MHz, boost on). The judge's CPUs
and the bench lanes run `performance`. By the operator's order the check agent recorded the state of its 16 CPUs, set
the governor of those 16 CPUs and of nothing else to `performance`, and repeated the restore of 606,060 directories
and 3 files, judged commit against upstream, three rotated rounds; then it wrote the saved state back and read it back
equal. Then it repeated the 16-CPU run once more under `schedutil` on a quiet machine. The rows "the official judge
job" are the run of the table above. Source: the check agent's report,
[`../reviews/checks/REPORT.md`](../reviews/checks/REPORT.md), section "F6 settled"; the records of the governor change
are in `league/evidence/release-checks-final3-b8cf439/cpufreq/`.

<!-- BEGIN GENERATED:dirs-governor -->
| check CPUs | governor | beside it | upstream: wall s of rounds 1 to 3; median | judged commit: wall s of rounds 1 to 3; median | judged commit against upstream | user + system CPU s (medians), upstream / judged commit | CPU s of a run (median), upstream / judged commit | peak RSS MiB (largest), upstream / judged commit |
|---|---|---|---|---|---|---|---|---|
| all 16 check CPUs | `schedutil` | the official judge job | 66.5 / 68.0 / 67.2; 67.2 | 73.0 / 74.7 / 74.0; 74.0 | **10.1% slower** | 28.6 + 41.9 / 39.4 + 57.8 | 70.5 / 97.1 | 57.0 / 58.1 |
| all 16 check CPUs | `schedutil` | nothing | 68.2 / 68.0 / 67.8; 68.0 | 75.3 / 74.7 / 75.6; 75.3 | **10.7% slower** | 29.1 + 42.2 / 40.5 + 58.2 | 71.4 / 98.7 | 58.5 / 66.0 |
| all 16 check CPUs | `performance` | a bench of the merge agent, light | 53.2 / 53.7 / 53.3; 53.3 | 44.7 / 44.3 / 44.7; 44.7 | **16.1% faster** | 22.3 + 32.6 / 23.6 + 32.9 | 55.0 / 56.5 | 58.5 / 60.9 |
| 4 cores | `schedutil` | the official judge job | 86.5 / 86.7 / 86.5; 86.5 | 63.4 / 63.3 / 63.0; 63.3 | **26.8% faster** | 39.0 + 53.9 / 31.7 + 47.6 | 92.8 / 79.3 | 62.2 / 56.2 |
| 4 cores | `performance` | nothing | 53.1 / 53.7 / 53.5; 53.5 | 44.1 / 43.6 / 44.1; 44.1 | **17.6% faster** | 23.4 + 33.1 / 22.1 + 31.9 | 56.5 / 54.3 | 62.2 / 57.0 |

From `runs.json` in `x11-606060/`, `x11-606060-schedutil-quiet/` and `x11-606060-performance/` under `league/evidence/release-checks-final3-b8cf439/`, every run. The harness recorded seconds to one decimal. "CPU s of a run" is the median over the three rounds of user plus system of one run, so it can differ by 0.1 from the sum of the two medians beside it.
<!-- END GENERATED:dirs-governor -->

"Light" is 1.7 to 1.8 busy CPUs outside the check set, "nothing" 0.2 to 0.4. The clock of the CPUs was sampled every
5 s in the three later runs; the figures are the report's, means of the readings taken on a CPU that was running:

| governor | check CPUs | mean clock of a running CPU, upstream | the same, judged commit |
|---|---|---|---|
| `performance` | all 16 | 3,798 MHz | 3,798 MHz |
| `performance` | 4 cores | 3,799 MHz | 3,798 MHz |
| `schedutil`, quiet machine | all 16 | 2,551 MHz; 61% of readings at the 1,500 MHz minimum | 2,020 MHz; 58% of readings at the 1,500 MHz minimum |

What this settles:

- **The `performance` governor removes the loss.** With the CPUs held at full clock this build is 16.1% faster than
  upstream on 16 CPUs and 17.6% faster on 4 cores, and the two CPU sets give the same times. That agrees with the
  merge agent's readings on the bench lanes.
- **A quiet machine does not remove it.** Under `schedutil` with nothing beside it this build is 10.7% slower than
  upstream, as it was with the judge job beside it (10.1%).
- **The cache layout was ruled out before** (the table above this one).

The restore keeps about one CPU busy: 1.04 of the 16 for upstream, 1.31 for this build. Under `schedutil` on the quiet
machine this build's running CPUs were clocked lower than upstream's (2.02 against 2.55 GHz) and this build used 98.7
CPU seconds against upstream's 71.4; at the fixed clock both use about 55. **An inference, not a measurement:** this
build spreads the work over more threads, so the governor sees less load per CPU and holds the clock down. That
reading comes from the clock samples and the CPU seconds; no experiment isolated it.

**What this means for a user.** On a machine with many idle CPUs under a load-following frequency governor, which
is the usual setting on Linux, a restore that is almost only directories was 4.8% to 10.7% slower than upstream with
16 CPUs. That is four readings on one machine: 10.1% and 10.7% at 606,060 directories, 7.8% at 1,010,100, 4.8% at
2,020,200 (one run per binary). With 4 cores under the same governor it was 26.8% faster, and with the CPUs held at
full clock it was 16% to 18% faster at both counts. The other timings that exist under the load-following governor
are the incidental ones of section 6.3. No code change follows from this for the release; the limit is stated in
[KNOWN-LIMITS.md](KNOWN-LIMITS.md), section 2.

### 6.3 The other timings under the load-following governor

The release-check harness is the only place where anything was timed under `schedutil`. These are **incidental
timings of correctness checks**, one to three runs each and not paired: they are not benchmarks. They are listed
because no other numbers under a load-following governor exist. Judged commit `b8cf439` against upstream; check CPUs
as in section 6.2; memory limit 16 GiB; taken beside the official 44-round job. The 50 GiB file of part C is a
sparse file and the index of part D holds invented entries with no packs behind them
([METHODS.md](METHODS.md), section 10).

<!-- BEGIN GENERATED:harness-timings -->
| check | what ran | upstream | judged commit | runs |
|---|---|---|---|---|
| C10 part A | load 500 index files through 50 ms of latency, 1 CPU | 11.82 s | 11.56 s | three each, all within 0.01 s |
| C10 part A | the same, 2 CPUs | 11.80 s | 11.47 s | three each |
| C10 part B | restore 2,020,200 directories and 10 files, 16 CPUs | 221.6 s | 232.3 s (4.8% slower) | one each |
| X11 | restore 606,060 directories and 3 files, 16 CPUs | 67.2 s | 74.0 s (10.1% slower) | three rotated, median |
| X11 | the same, 4 cores | 86.5 s | 63.3 s (26.8% faster) | three rotated, median |
| C10 part C | restore one 50 GiB file | 41.7 s | 41.3 s | one each |
| C10 part C | the same with `--sparse` | 1.8 s | 1.6 s | one each |
| C10 part D | backup against an index of 30 million blobs (600 index files) | 9.6 s | 6.9 s | one each |
| C10 part D | the same, 10 million blobs | 3.4 s | 2.2 s | one each |
| C10 part D | the same, 3 million blobs | 1.4 s | 1.0 s | one each |
<!-- END GENERATED:harness-timings -->

Source: `status.txt`, `c10/summary.json` and `x11-606060/table.md` in
`league/evidence/release-checks-final3-b8cf439/`, and the check agent's report,
[`../reviews/checks/REPORT.md`](../reviews/checks/REPORT.md).

## 7. Season by season

The league ran twenty seasons between 2026-09-24 and 2026-10-07. A season is four matches; the best tree is confirmed
in one long job and promoted if it passes the gates (METHODS.md section 5). The table gives the confirmation of each
season's nominee. Until S012 the score was the mean of time saved and memory saved on the main workload. From S013 the
score is time saved, with a memory guard. From S017 it is the mean over three workloads, from S019 over four. **The
"against upstream" column therefore changes basis three times; the basis is marked.** All jobs: 4 CPUs.

| season | teams ran on | nominee | confirmation against the incumbent | job | against upstream at that point |
|---|---|---|---|---|---|
| [S001](../reviews/seasons/S001-review.md) | one team; a provider's unnamed preview model, effort max | promoted | score +3.24% (41/42); time -2.16% (slower), memory +8.26% | `d5a29fd7` | the same job (the incumbent was upstream) |
| [S002](../reviews/seasons/S002-review.md) | four teams from here on; GPT-6 Luna, effort max | T1: **not promoted** | score -0.53% (15/42); time +1.83%, memory -2.95% | `3db4fd58` | score +3.03% |
| [S003](../reviews/seasons/S003-review.md) | GPT-6 Luna max | T1: **not promoted** | score +0.49% (27/42, 29 required); time +0.58%, memory +0.71% | `f3d98087` | score +3.64% |
| [S004](../reviews/seasons/S004-review.md) | GPT-6 Luna max | T1: promoted | score +5.25% (42/42); time +8.15%, memory +2.14% | `704f4cc7` | score +8.57% (time +6.52%, memory +10.83%) |
| synthesis after S004 | by hand | T2's and T4's memory work joined to S004 | not quoted in the reviews | | score +15.07% |
| [S005](../reviews/seasons/S005-review.md) | GPT-6 Luna max | T4: promoted | score +2.99% (41/42); time +3.49%, memory +3.24% | `cce6a759` | score +17.82% (time +11.47%, memory +24.11%) |
| [S006](../reviews/seasons/S006-review.md) | GPT-6 Luna max | T3: promoted | score +1.11% (32/42); time -0.26%, memory +2.30% | `faa13f6b` | score +18.59% (time +11.26%, memory +25.68%) |
| [S007](../reviews/seasons/S007-review.md) | GPT-6 Luna max | T3: **not promoted** | score +0.014% (21/42; interval -0.22..+0.71) | `da1cf1e5` | unchanged |
| [S008](../reviews/seasons/S008-review.md) | GPT-6 Luna max | T3: promoted | score +8.42% (42/42; +8.16..+8.80); time +5.92%, memory +11.27% | `73d8c5d1` | score +24.98% (time +16.03%, memory +33.49%) |
| [S009](../reviews/seasons/S009-review.md) | GPT-6 Luna max | T1: promoted | score +1.30% (38/42); time +0.52%, memory +1.94% | `7c206f5a` | score +25.62% (time +15.94%, memory +35.50%) |
| [S010](../reviews/seasons/S010-review.md) | GPT-6 Luna max | T1: promoted | score +0.84% (32/42); time +0.58%, memory +1.06% | `87861b3e` | score +26.88% (time +16.67%, memory +37.15%) |
| [S011](../reviews/seasons/S011-review.md) | GPT-6 Luna max | T1: promoted | score +2.10% (42/42); time +1.64%, memory +2.43% | `f6e33b96` | score +27.50% (time +16.91%, memory +38.30%) |
| [S012](../reviews/seasons/S012-review.md) | GPT-6.1 Sol, effort high | T1: promoted | score +2.73% (42/42); time -0.64%, memory +5.52% | `f9ece693` | score +29.68% (time +17.38%, memory +41.79%) |
| | | | **from here: score = time saved; memory is a guard** | | **time on main** |
| [S013](../reviews/seasons/S013-review.md) | GPT-6 Luna max | T1: promoted | time +3.11% (42/42); memory +0.05% | `651a07c9` | time +19.69%, memory +41.64% |
| [S014](../reviews/seasons/S014-review.md) | GPT-6 Luna max | T4: promoted | time +1.35% (42/42); memory +0.20% | `c90c07ce` | time +21.02%, memory +41.19% |
| [S015](../reviews/seasons/S015-review.md) | GPT-6 Luna max | T4: promoted | time +1.47% (42/42); memory -0.29% | `f3628902` | time +22.58%, memory +42.26% |
| [S016](../reviews/seasons/S016-review.md) | GPT-6 Luna max, then GPT-6.1 Sol medium from mid-season | T1: promoted | time +4.71% (42/42; +4.65..+4.80); memory +0.84% | `9b3da9b7` | time +25.75%, memory +42.18% |
| [race fix](../reviews/merge/RACE-FIX-REPORT.md) | by hand | snapshots are listed before the index again | no timing job quoted | | |
| | | | **from here: mean of main, big-index, large-files** | | **suite of three** |
| [S017](../reviews/seasons/S017-review.md) | GPT-6.1 Sol medium | T1: promoted | +1.93% (34/42; +1.79..+2.05) = +2.18 / +3.72 / -0.10; memory -0.33% | `582b1263` | +21.89% = +27.61 / +25.19 / +12.87; memory +26.71% |
| [merge after S017](../reviews/merge/S017-MERGE-REPORT.md) | by hand | three unpromoted ideas and two prototypes | +6.06% (33/42) = +0.16 / +17.90 / +0.11; memory +5.20% | `b740c9e2` | +27.08% = +27.92 / +39.84 / +13.48; memory +32.72% |
| [S018](../reviews/seasons/S018-review.md) | GPT-6.1 Sol medium | T1: promoted | +4.00% (42/42; +3.86..+4.13) = +1.83 / +4.04 / +6.15; memory +13.03% | `821fc91a` | +29.46% = +29.30 / +40.59 / +18.48; memory +43.09% |
| | | | **from here: mean of four, with remote; 44 rounds** | | **suite of four** |
| [S019](../reviews/seasons/S019-review.md) | GPT-6.1 Sol medium | T2: promoted | +4.33% (44/44; +4.18..+4.59) = +1.68 / +0.89 / +5.37 / +9.37; memory +0.70% | `28102fc2` | +28.15% = +30.23 / +41.29 / +22.72 / +18.37; memory +43.97% |
| [merge after S019](../reviews/merge/S019-MERGE-REPORT.md) | by hand | | +1.14% (36/44; +1.05..+1.27) = +1.29 / +1.31 / +2.23 / -0.25; memory -0.81% | `014cbfda` | +28.99% (44/44) = +31.11 / +41.96 / +24.22 / +18.68; memory +43.16% |
| [S020](../reviews/seasons/S020-review.md) | GPT-6.1 Sol medium | T2: promoted | +1.03% (35/44; +0.89..+1.14) = +0.33 / +3.46 / +0.24 / +0.11; memory +0.51% | `7398d456` | +29.94% (44/44; +29.79..+30.01) = +31.28 / +45.55 / +23.97 / +18.95; memory +43.91% |

The league tree after S019 (the row "merge after S019") is commit 2 of this repository. The S020 incumbent is the
`incumbent` arm of the jobs in sections 1 and 5.

**From S020 to the published source.** The league ended with S020. The published tree is not S020: it starts again
from the league tree after S019 and takes five more steps, each measured by an official job in the arms table of
section 5:

| step | commits in this repository | tree | official jobs (section 5) |
|---|---|---|---|
| soundness reviews; repairs R1 to R11 and M22 | 3 to 19 | the repaired tree | `3c21cf80`, against the league tree after S019 |
| the final merge: the sound part of S020, four ports, the cost forms of R3 and R4, fixes F1 to F3 | 20 to 35 | the first final tree (`rc-5`) | `757b745c` (12 rounds), `c87fa304` (44 rounds); the raw-rule arm `aca189ed` is this tree before F2 |
| fix F5 and its bound, two tests from the review of the ports | 36 to 39 | the second final tree (`rc-7`); the tree the last independent review was given | `f6765696` (one commit before), `d5c8c004` (12 rounds), `940c4e64` (44 rounds) |
| nine fixes from that review, restore's hand-over per batch, the version string | 40 to 50 | the judged commit (`rc-8`) | section 1 |
| one comment and one test | 51 | the published source | not judged separately; section 2 | Every season's API cost is recorded as $0 in its review: the teams
ran on a subscription or a free tier, not on metered calls.

The mechanisms by season are in [CHANGES.md](CHANGES.md). What the seasons taught, where it matters to a reader of the
numbers:

- **A six-round screen is not a result.** S002: a row screened at +1.29 confirmed at -0.53. S003: +1.47 became +0.49
  and missed promotion by two wins. S007: +2.08 became +0.014. After S007 the three best rows were measured again at
  12 rounds before the confirmation slot was spent (in use from S013). In S017 that rescreen changed the nominee: the
  row that led at six rounds (+2.44) read +2.13 at twelve, the second (+2.35) read +2.68 and confirmed at +1.93.
- **A measured map of where the time goes beat unaimed search.** S001 to S003 found small memory gains in one
  corner. S004, the first season aimed by a cost map, confirmed +5.25%. S005 to S007 added +2.99, +1.11 and nothing;
  S008, on a fresh map, +8.42% (METHODS.md section 6).
- **The score decides what is built.** In S012, under the equal-weight score, 14 of 16 rows were slower than their
  incumbent and bought memory. In S013, the first season scored on time, every row was a time gain.
- **Gains were fitted to the workload that was scored.** Before S017 only `main` was scored; on the other workloads
  the S015 incumbent held about half of its main gain. In S019, the first season with `remote`, every team found in
  its first match that the incumbent had been issuing five times upstream's requests in restore.
- **The incumbent twice carried a concurrency defect the judge could not see** (section 11.1).
- **Two judge jobs side by side inflated prune cells** in S020: a big-index prune cell read +22.54% in a match and
  +23.83% at its rescreen, both beside another job, and +6.70% in the 44-round job, which ran alone.

## 8. How the scoring changed

From the contract, [`../reviews/contract/INTERFACE.md`](../reviews/contract/INTERFACE.md), and the season reviews.

| from | score | workloads | match / rescreen / confirmation | promotion gates |
|---|---|---|---|---|
| S001 | mean of time saved and memory saved | main | 6 rounds / none / 42 rounds | 29 of 42 wins; median score at least +0.25%; ahead of upstream; neither axis more than 5% worse |
| S003 | the same; a tree identical to the incumbent scores zero | main | the same | the same |
| S005 | the same; match points also count per-phase cells | main | the same | the same |
| S013 (2026-10-01) | **time saved**; memory may not be more than 2% worse than the incumbent | main | 6 / 12-round rescreen of the top three / 42 | 29 of 42 wins on time; +0.25%; memory guard |
| S017 (2026-10-03) | mean time saved over three workloads | main, big-index, large-files | 6 (each workload twice) / 12 / 42 | as before, plus per workload: memory guard 2%, time not more than 1% worse |
| S019 (2026-10-05) | mean time saved over four workloads | the three and remote | 8 / 12 / **44** (11 per workload) | 29 of 44 wins; the same per-workload gates |

Reasons, as the documents give them: the equal-weight score was replaced because the gains had become almost all
memory bought with a little time (S012). The suite replaced the single workload because gains were fitted to it (S016
review: "On the other visible workloads S015 held about half of its main-workload gain"). `remote` was added because
nothing measured the number of requests. Scores on different bases are not comparable, and the season reviews say so
at each change. The performance CPU governor on the judge's CPUs dates from S013; with it the per-round time noise
between identical arms fell from 1.11% to 0.27% (METHODS.md section 3).

## 9. The held-out workload: did the gains transfer?

The held-out workload is a second corpus, different from `main` on every axis `main` fixes, that the teams never saw
and that was never scored. It was run only at season boundaries, by the operator, to test whether an incumbent's gain
against upstream exists outside the corpora the teams optimised against. How it was kept from the teams and how that
was audited is in METHODS.md section 8. Its seed, location and tooling are not published.

Each row is one 12-round job against upstream on the held-out workload (4 CPUs, one official lane). Source: the
operator's transfer notes, restated in
[`../evidence/holdout/README.md`](../evidence/holdout/README.md).

| incumbent | held-out: time saved vs upstream | held-out: memory saved | the same tree on visible `main`: time |
|---|---|---|---|
| S015 | +23.27% (interval 22.97..23.36; 12/12) | +29.65% | +22.58% |
| S016 with the race fix | +26.59% (26.45..26.76) | +28.58% | +26.2% (a suite canary) |
| S017 | +28.75% (28.65..28.90; 12/12) | +31.26% | +27.61% |
| S018 | +29.91% (29.68..30.08; 12/12) | +36.89% | +29.30% (14 main rounds of job `821fc91a`) |
| S019 | +30.76% (30.48..30.91; 12/12) | +39.34% (38.85..39.86) | +30.23% (11 main rounds of job `28102fc2`) |

Upstream's pass on the held-out workload took 46.67 s and S019's 32.35 s. By phase, S019 against upstream on the
held-out workload: first backup +12.83%, unchanged backup +18.11%, changed backup +14.38%, restore +47.84%, check
+31.80%, forget-prune +45.40%, ls +36.77%.

What this shows: through S019 the gain on a corpus the teams never saw was as large as on `main`, season by season.
What it does not show: **the tree in this repository has not been run on the held-out workload**, and neither has
the S020 incumbent or the league tree after S019 with its merge. The last tree run on it is S019. The held-out
workload is local, so it says nothing about `remote`; and it is one corpus.

## 10. Other backup tools, as measured

On 2026-10-03 the then-incumbent **S015** (not the tree in this repository) and upstream restic were run beside
rustic 0.11.4, Kopia 0.23.1 and Borg 1.4.5 on the main workload's seven steps: 10 interleaved repetitions, CPUs 2 to
5 of the same machine (not the judge's CPUs, no performance governor), each tool with its defaults. Source and every
caveat: [`../reviews/comparison/BACKUP-TOOLS-RESULTS.md`](../reviews/comparison/BACKUP-TOOLS-RESULTS.md).

| tool | total seconds, median [min-max] | sum of the seven peak RSS, MiB | restores correct |
|---|---|---|---|
| upstream restic | 21.45 [21.26-21.56] | 917 | 10/10 |
| S015 | 17.07 [16.96-17.19] | 593 | 10/10 |
| rustic 0.11.4 | 23.51 [23.23-23.86] under the clock-holding method; the report estimates 19.2 to 20.2 without it | 2262 | 10/10 |
| Kopia 0.23.1 | 14.48 [14.35-15.14] | 2515 | 0/10 |
| Borg 1.4.5, cold cache | 54.54 [54.16-54.78] | 727 | 10/10 |
| Borg 1.4.5, warm cache | 49.10 [48.60-49.24] | 721 | 10/10 |

Read this table with its caveats, which the source states at length:

- The tools differ in defaults that were **not equalised**: chunking, compression, encryption, what a "full verify"
  reads, what a cache holds. The comparison is of tools as shipped, not of algorithms.
- The run used a method that holds the CPU clock up with idle-priority spinners. rustic's worker pipeline yields the
  CPU in a loop, and under that method its first backup took 45% longer (11.07 s held against 7.66 s unheld). The
  other tools were barely affected (4%). The report gives both figures.
- Kopia's 0 of 10 is the report's finding (c). In every repetition its restore differed from the source in exactly
  three items of the corpus's special entries (a file name that is not valid UTF-8 was stored with replacement
  characters, a named pipe was skipped without a message, and two groups of hard links were restored as independent
  files). All regular file contents, symbolic links, modes and modification times matched. The report traces this to
  Kopia's own behaviour, not to the harness.
- Cold cache in every step follows the judge and is unrealistic for Borg (its unchanged backup: 8.24 s cold, 5.19 s
  warm). The workload favours change detection: Kopia's unchanged and changed backups (0.52 s and 1.11 s) are most
  of its lead.
- Peak memory is `ru_maxrss`; rustic's 1.0 to 1.4 GiB restore peak may include memory-mapped restore targets on
  tmpfs, which was not split out.
- 5 of 30 rustic units in all runs had a failing phase (a MAC failure from a corrupted local cache; the repository
  was intact).
- Per phase the picture is mixed: Kopia was faster than S015 in the three backups and in listing, rustic in full
  verify and listing, S015 in restore and prune. Peak memory: S015 was lowest in five of seven steps, Borg in
  restore, upstream restic in listing.

## 11. Negative results

### 11.1 The league's own unsound work

- **The lock-wait overlap as the league built it lost data** (finding R1). It was promoted in S013 for backup and in
  S015 for every command, and it was the largest single lever of the speed-scored seasons. The judge passed it in
  every round of every season, because the judge never runs two commands at once. [SOUNDNESS.md](SOUNDNESS.md),
  section 2.
- **Before that, the same code listed the index before the snapshots.** With a backup running beside it,
  `restore latest` failed in 31 of 100 runs and `ls latest` in 29 of 100 on the S015 incumbent (upstream 0 of 100;
  with the fix 0 of 100). Upstream's own `TestRestore` caught it, but only under CPU load: 21 of 1,000 parallel runs.
  Found and fixed after S016 ([race-fix report](../reviews/merge/RACE-FIX-REPORT.md)). The fix did not cover R1.
- **S020's three new lock-wait mechanisms cost time.** S020 with them against S020 without them, 10 fixed pairs per
  cell: main -0.29%, big-index +0.04%, large-files +0.14%, remote -0.45%, mean -0.14. All three were dropped by the
  review and are not in this tree ([SOUNDNESS.md](SOUNDNESS.md), section 5).
- **Storing incompressible blobs raw** saved time and was allowed by a ruling. It made `prune --repack-uncompressed`
  rewrite the same blobs on every run. Removed (section 5.4).
- **A compression sampler** (encode 16 windows of 2 KiB and store the blob raw if they do not shrink) stored one
  incompressible 64 KiB block repeated sixteen times at 1,048,608 bytes where upstream stores 65,661. Removed (R4).

### 11.2 Ideas tried and closed

From the inventory of every idea the league produced,
[`../reviews/INVENTORY.md`](../reviews/INVENTORY.md) (section 4 and section 8.5), and the final merge report. One
line each; the inventory gives the season, the team and the record for every line.

**Closed by a rule, whatever they scored.** Shortening or removing the 200 ms lock wait. Changing `GOGC` or
`GOMEMLIMIT` (`GOGC=off` was slower in 6 of 7 phases). Changing the key-derivation parameters. Removing or weakening
the verification on save (removing hash-on-write alone read -3.2 in S007). Detecting the workload. Changing output
bytes, order, exit codes, chunk boundaries or IDs. Busy-waiting to hold the CPU clock up. More than 5 REST
connections.

**Restore.** A second-pass window above 64: flat (-0.305 / -0.305 / -0.313 s at 64 / 256 / 1024). No `fallocate` for
small files: no effect. Prefetching raw tree payloads: the decode is the cost, not the read. Bounds for a third worker
that failed: a per-pack blob-size gate (nothing at 2 MiB, over the memory guard at 3 MiB); one budget on which every
worker waits (11 to 22% slower). Pooled blob-sized buffers: +26 to +34 MiB. Upstream's 32 MiB ranges instead of
windows: +133 MiB and 1.1 s less gain. An additional worker on `remote`: +0.3% for 5 to 8 MiB. The A14 idea applied
to restore's printer: +0.15% / +1.10% / +1.08%, all unresolved, dropped.

**Check.** A buffered task channel: nothing. Per-worker buffer reuse: nothing on either axis. Three hash-pipeline
designs from S018: 0.10 to 0.21 s slower than the incumbent's check on large-files. Handing out packs largest first:
+0.18 / +0.85 / +0.14 s locally for +130 MiB.

**Backup.** An in-flight known-blob check (four builds): null at 42 rounds (the S007 nominee). Four forms of
save-verify from S019: the bound did not move. A parent-blob cache: backups slower. Three connections for backups:
null. Four or eight readers: no better than three on large-files; on `remote` eight read -0.25 s for +65 MiB.

**Prune and forget.** Used size from header arithmetic (three builds): null. An index save verified by a stream
hash: null. Repack through larger requests (S019): inside the noise.

**ls.** Pre-batched output tagging, a pull-iterator walk, tree read-ahead: null. Three connections: null.

**Index.** A streaming decoder for the whole load as first built in S009: slower (later forms are installed).
Slab, bitset and batched listings before S004: +1.09 at best.

**Start-up and lock wait.** Three builds of index preparation inside the wait in S012: overtaken. Four builds that
prepared or deferred start-up requests in S019: equal or behind.

**Left out of the final merge with a number** (merge report, "What was left out, and why"): a restore snapshot
preload inside the lock wait (+0.36% on remote restore, about +0.02 suite points; left out); a tree pack fetched in
five parallel ranges (its gain exists only where each connection has its own rate limit, which is the simulated link;
on a link limited as a whole it costs a size request and four more GETs per uncached tree pack; not built); a larger
restore budget (restore +1.12% main, +1.68% big-index for 3 to 6 MiB more peak; left for a decision, because with the
repairs memory is the tight side); the index decoder recycler of the A5 source tree (+0.72%, unresolved, not taken).

**The automatic season merge** (join the best trees per phase by file) ran in four seasons and found nothing to merge
in any of them: the teams' best trees edited the same files. Every merge in the history was made by hand.
