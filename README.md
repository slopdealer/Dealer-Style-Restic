# Dealer Style Restic

A faster build of [restic](https://github.com/restic/restic), the backup program, that reads and writes the same
repositories with the same commands and the same defaults. It is upstream restic 0.19.1-dev (commit `ba802d42`) plus a
patch series produced by an AI software-improvement league (teams of AI agents, four of them from the second season
on; twenty seasons; every candidate measured against upstream by one judge). The patch series was then reviewed for
soundness by independent reviewer agents and repaired where the reviews found defects. This repository holds the
source, the patch series, the benchmark results with the files they come from, the methods, the reviews and the tools.

**Status: experimental. Independent. Not affiliated with, and not endorsed by, the restic project.** Report problems
here, in this repository's issue tracker, and never to upstream: upstream's developers did not write this code and
cannot be asked to support it. Try it on a separate test repository first, and keep using a second, independent backup
of anything that matters. Do not run `restic self-update` with this build: it would replace it with upstream's
binary.

**Where to report a problem.** Always at <https://github.com/slopdealer/Dealer-Style-Restic/issues>, never to
upstream. Eight error messages that this build inherits from upstream (two in `cmd/restic/cmd_check.go`, three in
`internal/repository/repository.go`, one in `internal/repository/pack/pack.go`, two in
`internal/repository/prune.go`) still tell the user to open an issue in upstream's tracker. They are wrong for this build. They were left as they are because changing them changes the
binary after it was judged, reviewed, checked and rebuilt independently; correcting them is the first change planned
for a next source release.

**The claim, exactly:** identical chunking, blob IDs and blob storage; index and lock files decode to the same
content. Everything this build uses from a repository has been authenticated and hash-checked when it was read.
Where it reads once what upstream reads twice, and where it encodes a file differently, is listed below and in
[KNOWN-LIMITS.md](league/docs/KNOWN-LIMITS.md), section 5.

## Headline numbers (measured with 4 CPUs)

<!-- BEGIN GENERATED:headline -->
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

Judge job `50388f4ecf434b0d8663aa4a2a740d95` (promotion mode), completed 2026-10-07 19:24 UTC: 44 rounds, 11 per workload, three arms in every round (this build, the S020 incumbent, upstream restic at `ba802d42`). **4 CPUs pinned, GOMAXPROCS 4**; tmpfs for corpus, repositories, restore targets and restic temp/cache; one machine (AMD EPYC 9454, 48 cores / 96 threads, 128 GB RAM, Linux x86-64; `league/evidence/machine.txt`), CPUs set to the `performance` frequency governor, one build of each arm with the same pinned Go toolchain. Correctness: 1,019 of 1,019 units matched the upstream oracle; compatibility tests passed: True. **Judged: working commit `b8cf439` (the league's label `rc-8`); the source in this repository is one commit later, which changes one comment and adds one test and no code** (RESULTS.md, section 2).

Time is the sum of the seven phases of one pass; "time saved" is the median over rounds of the paired difference in that round, in percent of the other arm.
<!-- END GENERATED:headline -->

"Suite" means four workloads, each a fixed corpus run through seven phases (first backup, unchanged backup, backup
after changes, full restore, `check --read-data`, `forget --prune`, `ls --json`). The workloads are `main` (91,264
entries, 1.64 GiB, mostly small files), `big-index` (the same data in a repository that already holds 1.5 million
blobs), `large-files` (3,217 entries, 2.91 GB: a sparse disk image, a database file, logs, media) and `remote` (the
main corpus through the REST backend over a simulated link). All 28 cells, in seconds and MiB, with rounds won and
intervals, and against the league's last incumbent: [RESULTS.md](league/docs/RESULTS.md). How the judge works:
[METHODS.md](league/docs/METHODS.md).

### What these numbers do not show

- **Not your machine.** One machine, one build per arm, one Go toolchain. A different CPU, kernel, disk or toolchain
  gives different numbers.
- **Not more than 4 CPUs.** Every league number was measured with 4 pinned CPUs and `GOMAXPROCS=4`. A later bench
  of the published machine code at 8 and 16 cores (5 pairs per cell, the `performance` governor, not a judge score)
  found every phase of the four workloads ahead of upstream at both core counts, 56 cells of 56, and found that this
  build uses more user CPU than upstream in restore, prune and ls ([RESULTS.md](league/docs/RESULTS.md), section
  6.1).
- **Not a machine whose clock follows the load.** Every league timing, the official 44 rounds included, ran on CPUs
  set to the `performance` frequency governor, which holds every CPU at its top clock. On a machine with many idle
  CPUs under a load-following governor, which is the usual setting on Linux, a restore that is almost only
  directories was **4.8% to 10.7% slower than upstream** with 16 CPUs. That is four readings on one machine: 10.1%
  and 10.7% at 606,060 directories (74.0 s against 67.2 s, and 75.3 s against 68.0 s on a quiet machine), 7.8% at
  1,010,100 and 4.8% at 2,020,200. With 4 cores under the same governor it was 26.8% faster (63.3 s against 86.5 s),
  and with the CPUs held at full clock it was 16% to 18% faster at both counts. A commit removed most of an earlier,
  larger loss, not all of it. The other timings taken under the load-following governor are few and incidental;
  they read this build ahead of upstream or level with it ([RESULTS.md](league/docs/RESULTS.md), sections 6.2 and
  6.3; [KNOWN-LIMITS.md](league/docs/KNOWN-LIMITS.md), section 2).
- **Not a disk.** Corpus, repository and restore target were on tmpfs. Performance on a disk was not measured. The
  percentages can change with the cost of storage; measure your own workload.
- **Not a real network.** The `remote` workload uses a REST server on the same machine that sleeps 30 ms before every
  request and paces each of 5 connections at 48 MiB/s. No TLS, no packet loss, no jitter, no object store.
  Performance on a real network was not measured. The percentages can change with latency and with what the backend
  does; measure your own workload.
- **Not concurrent use.** The judge runs one restic process at a time. It could not see, and did not see, the defect
  in the league's own code that lost data when two commands ran against one repository (below). Soundness under
  concurrency rests on the reviews and the release checks, not on the judge.
- **Not every workload.** Four synthetic corpora, cold cache in every phase.
- **Not less memory than the league's last tree.** Against upstream the build uses less memory on all four workloads;
  against the league's last incumbent (S020) it uses more, because the repairs cost memory.
- **Not tested on the held-out workload.** A second corpus that the teams never saw was used to test earlier trees.
  The tree in this repository has not been run on it.
- **Not the last commit.** The judge measured the commit before the last source commit; the last one changes a
  comment and adds a test ([RESULTS.md](league/docs/RESULTS.md), section 2).

## What was changed, and what was not

This is not a rewrite and not a fork with new features. It is upstream's code with files added and upstream files
changed, all under `cmd/` and `internal/` (the counts are in the identity block under "Verify"). `go.mod` and `go.sum`
are upstream's.

Not changed, by rule of the league and checked by the judge in every round and by the release checks:

- the repository format, and every command, flag, default, output line and exit code the judge and the checks
  compare;
- chunking and blob IDs: the same input gives the same chunks and the same IDs as upstream;
- the stored form of data and tree blobs: they are stored exactly as upstream stores them (the same compression
  decision for every blob; release check C06 on the judged commit found repository sizes within 0.0005% of
  upstream's from one initialised repository);
- upstream's 200 ms wait after writing a lock file.

Different from upstream, and kept:

- **Index and lock files decode to the same content but are encoded differently.** They are compressed by a
  streaming or hand-written Zstandard encoder. The result is valid Zstandard and upstream reads it; it is a few bytes
  different from what upstream's encoder writes for the same content.
- **Restore reuses directory trees it has verified.** It keeps complete, verified directory trees from its first pass,
  within a budget of 512 KiB of decoded nodes, and uses them in its second pass. Other trees are loaded again, as
  upstream loads all of them. If storage damages a kept tree between the two passes, this build can finish from its
  valid kept copy. Upstream fails when its second load reaches the damaged storage, as in the reviewer's witness,
  which runs with the cache disabled. A valid local cache can hide the same damage from upstream as well. What is
  restored is the snapshot's content.
- **The index is read during the 200 ms lock wait.** After the lock is confirmed, the index is kept only if a fresh
  listing, taken where upstream lists, still shows the same files with the same sizes. The files are not read again.
  `check` reads nothing during the wait. **What that leaves open:** on a backend whose listing still shows an index
  file after a concurrent prune deleted it, and with a lock-file write slow enough for that prune to finish inside the
  wait, a backup can deduplicate against packs the prune just deleted and report success. Upstream behaves the same
  when that index file is in its local cache, and fails loudly when it is not.
  [KNOWN-LIMITS.md](league/docs/KNOWN-LIMITS.md), section 5.3.
- **The version string.** The build reports `0.19.1-dev-dealer.1`, and snapshots it writes carry that string in
  `program_version`.

What kind of change makes the time: work moved into the lock wait, key derivation on several cores, fewer and larger
requests, streaming decoders for the index, read-ahead of trees in restore, extra workers where a backend has room for
them, fewer copies and allocations in the backup write path. Mechanism by mechanism, with files and tests:
[CHANGES.md](league/docs/CHANGES.md).

## Safety, in short

The league's judge compared every candidate with upstream on every round: snapshots, restored trees, `ls` output,
`check` results. That is a strong test of one process at a time and no test at all of two.

After the last season, independent reviewer agents reviewed the league's tree mechanism by mechanism against written
contracts. All three found the same defect: files read during the lock wait were used after the lock was confirmed, so
a backup running beside a prune could build on data the prune had just removed, write a snapshot that referenced it,
and exit 0. A release check races the two commands with real binaries, 480 trials per tree: 240 with uniform timing
and 240 aimed at the window. Counts were recorded in four runs: on the repaired tree, on the second final tree one
commit before its last and at its last commit, and on the judged commit. In these runs, the unrepaired tree produced
33, 37, 35 and 37 broken snapshots of 480; in the run on the judged commit the 37 were 2 of the 240 uniform trials and
35 of the 240 aimed ones. The tree under test produced 0 of 480 in each of these runs, upstream 0 of 120. The reviews
recorded ten more findings of lower severity (R2 to R11: a deadlock of `repair packs` with one connection, a crash on
a malformed key file, lost error output on a full disk, and others) and a memory finding (M22). All were repaired,
each with a test that fails before the repair. Release checks with real binaries on Linux (16 checks: kill -9,
corrupted packs, cross-reading with upstream in both directions, prune differentials, a 1.5-million-blob index, 32-bit
builds) then found four more failures, which were fixed. All 16 checks passed on the judged commit `b8cf439`. On the
published commit `8f9372b`, which adds a comment and a test to it, two things were run: a file-by-file comparison of
the two exports, and the added test, plain and three times under the race detector.

Last, the tree was given to three reviewers of a different model family who had seen none of this: only upstream's
source and the tree, and a strict contract to test it against. They reported 13 findings, each with a test: 12
distinct (one was found by two reviewers), tabled in 11 rows. **None is a case of data loss, a wrong restore, or an
incomplete backup reported as success.** Five carried the label BLOCKER, because their brief defined a missing
integrity check as one. Of the 11 rows, nine were fixed (one of them in part, with the rest documented) and two were
documented and not changed (the first two items of "Different from upstream" above). The fixes were reviewed in
turn.

The full account: [SOUNDNESS.md](league/docs/SOUNDNESS.md) (every finding, its repair, its test, what remains
unknown) and [RED-TEAM.md](league/docs/RED-TEAM.md) (the last review). The limits, without softening:
[KNOWN-LIMITS.md](league/docs/KNOWN-LIMITS.md).

## Build

Go 1.25.8 or later (`go.mod`), as for upstream:

```
git clone https://github.com/slopdealer/Dealer-Style-Restic
cd Dealer-Style-Restic
go build ./cmd/restic
./restic version               # restic 0.19.1-dev-dealer.1 (compiled manually) ...
```

The judge built every arm with Go 1.27.0, `CGO_ENABLED=0 GOAMD64=v1`, and
`go build -trimpath -buildvcs=false -tags "selfupdate disable_grpc_modules" -ldflags "-s -w" ./cmd/restic`. A release
build adds `-X github.com/restic/restic/internal/global.Version=0.19.1-dev-dealer.1` to the linker flags. That flag
changes address operands in the code, so a release binary is not byte-identical to the binary the judge measured
([METHODS.md](league/docs/METHODS.md), section 12.2). (`go run build.go` is upstream's build script and still passes a
version flag for a symbol that no longer exists; use the commands above.)

## Verify

That the source here is what it says it is:

<!-- BEGIN GENERATED:identity -->
- The source here: every file of the last source commit (`HEAD~1`; the commit after it adds this README, `.gitattributes` and `league/` and renames upstream's README) equals the league's working commit `8f9372b` (branch `final3`, git tree id `8b681f61cc94017a3a2510baa537cbabd0841ff1`). Checked twice when the repository was assembled: every blob id and path equal, and SHA-256 of all 938 files of that commit against an export of the working commit: all equal = true (`league/evidence/source-identity.json`).
- In this repository: commit `4ce4953b79` is the upstream subset, `783c89fbc2` the league tree after S019, then 49 replayed commits up to `8ffea10f9e`, whose git tree id is `96f2bddcd5ffb3dba251a404cefa1d346574078c`. That id differs from the working commit's for one reason: `helpers/verify-release-binaries.sh` carries its executable bit here, as it does upstream.
- Against the upstream subset of commit 1: 296 files added, 56 upstream files changed, 0 deleted; 938 files in all.
- **What the judge measured is one commit earlier**: working commit `b8cf439` (label `rc-8`). From it to the published source 2 files change: `cmd/restic/wait_index_load.go`, `internal/repository/repack_scope_test.go`. In the non-test file every changed line is a comment line. RESULTS.md, section 2, says how the two builds were compared.
<!-- END GENERATED:identity -->

```
git rev-parse 'HEAD~1^{tree}'    # the last commit only adds league/, README.md and .gitattributes
git diff --stat HEAD~1 HEAD -- cmd internal helpers build.go go.mod go.sum VERSION LICENSE   # prints nothing
```

That the first commit is upstream's code: it was compared with upstream's commit
`ba802d42b7294c98b62c16d1157ea3e80820c019` on 2026-10-07 through GitHub's API. All eleven top-level entries (the trees
`cmd`, `internal` and `helpers` and the eight files) have upstream's git object ids. The record is
`league/evidence/upstream-identity/UPSTREAM-IDENTITY.txt`. It was made before every commit of this repository was
given the publishing account's identity as author and committer. That step changed the commit id of commit 1 and none
of the object ids the record compares: a tree id or a file id depends only on content. The commit message gives the
three tree ids and the commands to repeat the comparison.

That the build does what it says:

- `go test ./cmd/... ./internal/...` runs upstream's tests and the tree's own;
  `RESTIC_TEST_LOCK_WAIT=200ms go test ./cmd/restic` runs the integration tests with the production lock wait;
- write a repository with this build and read it with upstream restic, and the reverse
  (`check --read-data`, `restore`, compare the trees);
- run one comparison with the published judge on your own machine: [TOOLS.md](league/docs/TOOLS.md), section 2;
- run the release checks: `league/tools/checks/`, described in [TOOLS.md](league/docs/TOOLS.md), section 5;
- compare a release binary with a build of your own: [METHODS.md](league/docs/METHODS.md), section 12.

## Downloads

Release [`v0.19.1-dev-dealer.1`](https://github.com/slopdealer/Dealer-Style-Restic/releases/tag/v0.19.1-dev-dealer.1),
a tag on this repository's main branch, carries seven files: the five binaries listed below, `SHA256SUMS`, and
`LICENSE`, the unchanged licence text of this repository, which must accompany a binary. A file's address is
`https://github.com/slopdealer/Dealer-Style-Restic/releases/download/v0.19.1-dev-dealer.1/<file name>`. After a
download, `sha256sum -c SHA256SUMS` in the folder that holds the files checks them.

<!-- BEGIN GENERATED:binaries -->
| file | SHA-256 |
|---|---|
| `restic_0.19.1-dev-dealer.1_darwin_amd64` | `516332fc04af27fcf8816f7381efb867804e595faec8b25e32a1feeefa8bc010` |
| `restic_0.19.1-dev-dealer.1_darwin_arm64` | `e26c5e0bdcff313211874299bd0367f305d4ba14919839e0c643652713cc4c9a` |
| `restic_0.19.1-dev-dealer.1_linux_amd64` | `753924dbb9e6bbb2876511c533f722ce5d68d2559928de9a6217b61b80f6f8c1` |
| `restic_0.19.1-dev-dealer.1_linux_arm64` | `7ed7c72be44fdbcb4eaa0935c4f674c1f7572c830a87f16a758d4dea28c087d5` |
| `restic_0.19.1-dev-dealer.1_windows_amd64.exe` | `7b4538e1902730a793542b28e881ce7193796105f8b08dd64a4c3646753e1382` |

The same list is `league/evidence/SHA256SUMS`. The files themselves are not part of the source tree.

**What these files are.** They were built from the published commit (`8f9372b`) with the judge's build flags plus one linker flag that sets the version string. **They are not the binary the judge measured, and they were not themselves judged.** The judged binary is a build of the commit before (`b8cf439`) without that flag. A build of the published commit without the flag has the judged machine code byte for byte. With the flag, the linux/amd64 file carries the same machine instructions at the same addresses as the judged build, with different address operands; no difference was resolved in the one timing of it against the judged binary, which covered one phase with 20 pairs. The other four files were built the same way and were not compared or timed. An independent reviewer rebuilt all five files offline from the published source with the documented command and got these five hashes (`league/evidence/independent-rebuild/`). RESULTS.md, section 2, and METHODS.md, section 12.2, give the detail and the command to rebuild a file and compare its checksum.
<!-- END GENERATED:binaries -->

## Platform support, exactly as tested

| platform | what was done |
|---|---|
| Linux x86-64 | All timing. All 16 release checks with real binaries, on the judged commit `b8cf439`. The tree's own tests and upstream's, as root and as an unprivileged user, and with the race detector. |
| Linux 386 | Builds. The tree's tests pass as 386 where upstream's compile for 386. One release check (C07) ran the 386 binary end to end: same tree ID as the amd64 binary, restore equal. |
| Windows x86-64 | Builds. Tested only as far as upstream's own tests and the reviewers' tests run on an unprivileged account. Not tested: VSS (`--use-fs-snapshot`), elevated runs, timing, the release checks. |
| macOS, FreeBSD, OpenBSD, NetBSD, Solaris, AIX; other Linux architectures | Build only. 25 release targets built on the second final tree, eleven commits before the judged one and twelve before the published source; from the published commit release binaries were built for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64. Nothing was run on these platforms. |

The Linux-specific fast paths (`internal/fs/file_open_linux.go`, `path_stat_linux.go`) have fall-backs on other
platforms that call upstream's code.

## What is in this repository

| path | content |
|---|---|
| `cmd/`, `internal/`, `helpers/`, `build.go`, `go.mod`, ... | the source |
| `LICENSE`, `README.upstream.md` | upstream's licence and upstream's README, unchanged |
| [`league/docs/RESULTS.md`](league/docs/RESULTS.md) | every result table, what soundness cost, more than 4 CPUs, the season history, negative results |
| [`league/docs/METHODS.md`](league/docs/METHODS.md) | the judge, the workloads, noise, the league, the reviews and checks as methods, how to reproduce |
| [`league/docs/SOUNDNESS.md`](league/docs/SOUNDNESS.md) | every finding of the reviews and checks, the repairs, what is unknown |
| [`league/docs/RED-TEAM.md`](league/docs/RED-TEAM.md) | the last independent review: what the reviewers were given, what they found, what was done |
| [`league/docs/KNOWN-LIMITS.md`](league/docs/KNOWN-LIMITS.md) | every limit we know of |
| [`league/docs/CHANGES.md`](league/docs/CHANGES.md) | the patch series explained for a restic developer |
| [`league/docs/TOOLS.md`](league/docs/TOOLS.md) | every published tool: what it does and how to run it |
| `league/series/` | the patches: one for the league's tree after season S019, then one per later commit |
| `league/evidence/` | the judge's result files, the identity record, the many-core data and its per-pair files, the release checks' records, the comparison with upstream, the judged-against-published binary proof, the machine, the held-out results; `league/evidence/README.md` says what is and is not included |
| `league/reviews/` | scrubbed copies of the league's own documents: season reviews, soundness reviews, the independent review and its triage, merge reports, check logs, the contract, the speed maps |
| `league/tools/` | the judge, workload generator, REST server, research tools, check harness, merge tools, `make_tables.py` |

History: commit 1 is the upstream subset, commit 2 the league's tree after nineteen seasons (do not use that tree:
it has the data-loss defect), then one commit per repair and per later change, then one commit that adds `league/`
and this README.

## Licence

restic is copyright of its authors and licensed under the BSD 2-Clause License (`LICENSE`, unchanged). The changes to
restic's source in this repository are published under the same licence. The documents, tools and result files under
`league/` are published under the same licence unless a file says otherwise.
