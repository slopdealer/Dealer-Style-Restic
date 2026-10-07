# Known limits

Every limit we know of, in one place, stated with its conditions. Where a limit comes from a document, the document
is named. "This build" is a binary made from the source in this repository; "upstream" is restic 0.19.1-dev at
`ba802d42`.

The claim this document qualifies: **identical chunking, blob IDs and blob storage; index and lock files decode to
the same content.** It is not "the same stored bytes", and it is not "every check upstream performs, at the same
moment". Section 5 says exactly where the difference lies.

## 1. What the measurements are, and are not

- **One machine.** An AMD EPYC 9454 (one socket, 48 cores, 96 threads), 128 GB RAM installed (125.5 GiB as the kernel
  reports it), Linux 6.8 on x86-64. The record is `league/evidence/machine.txt`. No second machine, no second CPU
  family, no ARM.
- **One build of each arm.** One pinned Go toolchain (1.27.0), `CGO_ENABLED=0`, `GOAMD64=v1`, the judge's build
  flags. Section 7 shows that a property of one build moved a phase by 3%.
- **4 CPUs.** Every league number was measured with the process pinned to four physical cores and `GOMAXPROCS=4`.
  Section 2.
- **The `performance` frequency governor.** Every league timing, the official 44 rounds included, ran on CPUs set to
  the `performance` governor, which holds a CPU at its top clock whether it is busy or not. Most machines let the
  clock follow the load. The only timings taken under a load-following governor are those of the release-check
  harness. In one kind of restore they are a loss against upstream. Section 2.
- **The judged commit is not the last commit.** The official jobs measured the commit before the last source commit.
  The last one changes a comment and adds a test, and a build of it with the judge's flags has the judged machine code
  exactly (RESULTS.md, section 2).
- **A release binary is not the judged binary.** It is built with one more linker flag, for the version string. Its
  code has the same symbols at the same addresses but differs from the judged binary's in address operands. No
  difference was resolved in the one timing that was made, one phase with 20 pairs; that does not establish that
  there is none (METHODS.md, section 12.2). Only the linux/amd64 binary was compared and timed; the binaries for the
  other four targets were built and nothing was run with them.
- **tmpfs.** Corpus, repository, restore target, temporary files and cache were in memory. Nothing touched a disk
  while timed. Performance on a disk and over a real network was not measured. The percentages can change with the
  cost of storage, with latency and with what the backend does; measure your own workload.
- **A cold cache in every phase.** The local cache starts empty for every measured process. This follows the judge's
  design and is not how a machine that backs up every night runs.
- **One restic process at a time.** No phase runs two commands against one repository. The judge is blind to
  concurrency defects, and was (SOUNDNESS.md, section 2).
- **Four synthetic corpora**, generated from seeds, seven phases each: 28 cells. Small repositories (about 1.2 to
  1.3 GB stored; the big-index workload adds 1.5 million tiny blobs). No multi-terabyte repository, no repository
  with years of history, no real user data.
- **Stored defaults.** Repository format 2, compression `auto`, default pack size, default connections. Other
  settings were covered by the release checks for correctness (C06, C08), not for speed.
- **The teams could see the visible workloads and the judge.** Their roles ran with enough access on the league box to
  read the judge's source. A transcript audit after season S017 found builders doing so (season review S017; the
  contract's note "KNOWN GAP"). No rule forbade tuning to the visible corpora, and some gains were fitted to them
  (RESULTS.md, section 7). The held-out workload exists for that reason; see section 9 for its own limit.
- **Noise.** Between identical arms the per-round time difference had a standard deviation of 0.27% on the judge's
  CPUs on the main workload; per phase 0.27% to 1.19% (METHODS.md, section 3). A per-phase cell from 11
  rounds that is smaller than about 1% is not established by this data. Per-phase memory is noisier: 1.3% to 6.9% per
  round.
- **The league's own verdict on this tree is "not eligible".** It is slower and larger than the league's last
  incumbent (RESULTS.md, section 1.2). That is the price of the repairs; it is stated here so that nobody reads the
  headline against upstream as "the league's best tree".

## 2. More than 4 CPUs, and a clock that follows the load

**Every league timing was taken on 4 pinned cores with the `performance` frequency governor.** What exists beyond
that was measured on the same machine afterwards (RESULTS.md, section 6):

- **The four workloads at 8 and 16 cores: a bench estimate, not a judge score.** The builders' bench tool, 5 fixed
  pairs per cell, whole cores, the `performance` governor, on the published machine code (the published commit built
  with the judge's flags): all 56 cells faster than upstream, every cell 5 of 5; the smallest, `check --read-data` on
  the remote workload at 16 cores, +5.51%. The "pass" figures at 8 and 16 cores are the bench tool's estimate. No
  oracle per round, no three-arm rotation, five pairs. The table is in RESULTS.md, section 6.1. An earlier table of
  the same bench stands beside it there, labelled as earlier: it was measured on the second final tree, eleven commits
  before the judged tree and twelve before the published source. The last of the twelve changes a comment and adds a
  test.
- **This build uses more user CPU than upstream in restore, forget-prune and ls**, and the gap widens with the number
  of cores. Its total CPU, user plus system, is higher than upstream's in some of those cells and equal or lower in
  others (RESULTS.md, section 6.1). On a machine where CPU time is what you pay for, that matters even where wall
  time is shorter.
- **On a machine with many idle CPUs under a load-following frequency governor, which is the usual setting on
  Linux, a restore that is almost only directories was 4.8% to 10.7% slower than upstream with 16 CPUs.** This is
  the one measured case in which this build loses to upstream outright. Four readings, all on the release-check
  harness's CPUs under `schedutil`, judged commit against upstream:

  | directories | rounds | upstream | judged commit | |
  |---|---|---|---|---|
  | 606,060 and 3 files | three rotated, the official judge job running on other cores | 67.2 s | 74.0 s | **10.1% slower** |
  | 606,060 and 3 files | three rotated, quiet machine | 68.0 s | 75.3 s | **10.7% slower** |
  | 1,010,100 | three rotated, from a run that was stopped before it was complete | 110.7 s | 119.3 s | **7.8% slower** |
  | 2,020,200 and 10 files | one run per binary (release check C10, part B) | 221.6 s | 232.3 s | **4.8% slower** |

  The same restore of 606,060 directories in the other conditions that were measured:

  | check CPUs | governor | upstream | judged commit | |
  |---|---|---|---|---|
  | 4 cores | `schedutil` | 86.5 s | 63.3 s | 26.8% faster |
  | all 16 | `performance` (CPUs held at full clock) | 53.3 s | 44.7 s | 16.1% faster |
  | 4 cores | `performance` | 53.5 s | 44.1 s | 17.6% faster |

  What the tests settle: the `performance` governor removes the loss; a quiet machine does not; the cache layout of
  the CPUs was ruled out before. The commit that batches restore's hand-overs had already removed most of an earlier,
  larger loss (27.2% at 606,060 directories before it), not all of it. Peak resident memory in the four rows, this
  build against upstream: 58.1 against 57.0 MiB, 66.0 against 58.5 MiB, 60.8 against 57.8 MiB, 58.0 against 56.2 MiB
  (the largest of the rounds of each row).

  The restore keeps about one CPU busy (1.04 of 16 for upstream, 1.31 for this build). Under `schedutil` on the quiet
  machine the running CPUs' mean clock was 2.55 GHz for upstream and 2.02 GHz for this build, and this build used 98.7
  CPU seconds against 71.4. At the fixed clock both use about 55. **An inference, not a measurement:** this build
  spreads the work over more threads, so the governor sees less load per CPU and holds the clock down.

  Not established: the size of the loss on another machine, under another load-following governor or on another
  kernel; any other shape that is mostly directories; any CPU count but 4 and 16.
- **The other timings under the load-following governor** are seven incidental readings of the release-check
  harness, with one to three runs each. They are not benchmarks. All seven read this build ahead of upstream or level
  with it (RESULTS.md, section 6.3).

Until someone measures your shape on your machine: **read every gain in this repository as a gain at 4 CPUs with
the `performance` governor.** Several of the league's mechanisms add workers or read-ahead inside fixed budgets that
were tuned with 4 CPUs. If you restore trees that are mostly directories on a machine with many CPUs, time this
build against upstream on that machine before you rely on it. Restricted to 4 cores, this build was faster than
upstream under both governors.

## 3. Platforms

| platform | tested | not tested |
|---|---|---|
| Linux x86-64 | all timing; all 16 release checks, on the judged commit `b8cf439`; own and upstream tests as root and unprivileged; race detector | file systems other than tmpfs and ext4; kernels other than the judge machine's |
| Linux 386 | builds; tests of the touched packages; one end-to-end check with the 386 binary (C07) | everything else; upstream's own `cmd/restic` tests do not compile for 386 |
| Windows x86-64 | builds; upstream's own tests as far as they run on an unprivileged account; the reviewers' tests and witnesses (the reviews were made on Windows); `go vet` (two lines in a file identical to upstream's) | VSS (`--use-fs-snapshot`), elevated runs, timing, the release checks |
| macOS (both architectures), FreeBSD, OpenBSD, NetBSD, Solaris, AIX, other Linux architectures | build only: 25 release targets built on the second final tree (eleven commits before the judged tree and twelve before the published source); from the published commit, release binaries for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and windows/amd64 | everything else. Nothing was run |

The Linux-only fast paths (opening files without the runtime poller, a direct path stat) fall back to upstream's code
elsewhere, so those platforms get less of the gain as well as less testing.

## 4. The remote workload is a simulation

The `remote` workload talks to a REST server on the same machine through a Unix socket. The server sleeps 30 ms
before it looks at each request, paces each of at most 5 connections at 48 MiB/s in both directions, and queues
further requests. Not simulated, in the judge's own words: "TLS, HTTP/2, loss, jitter, per-request server work
(listing cost), a slow start."

It can tell you that this build issues fewer requests and wastes less of a per-connection rate limit than upstream.
It cannot tell you what the build does on S3, B2, Azure, GCS, rclone, SFTP over a real line, a link limited as a whole
instead of per connection, a link with loss, or a server that throttles. One idea was left out of the final merge for
exactly this reason: its gain exists only where each connection has its own rate limit (RESULTS.md, section 11.2).
SFTP and REST with 1 and 2 connections were checked for correctness (C05, C08), not for speed.

## 5. Differences from upstream that are kept

Everything this build uses from a repository has been authenticated and hash-checked when it was read. The
differences are about reading something once where upstream reads it twice, about when a file is read, and about
how two kinds of file are encoded.

### 5.1 Restore uses the directory trees it verified in its first pass

A restore walks the snapshot twice: a first pass that plans and writes file contents, and a second pass that sets
metadata and restores special files and hard links. Upstream loads every directory tree from the repository in both
passes (its tree loader is called in both traversals). This build keeps the decoded nodes of trees it loaded,
authenticated, hash-checked and decoded in the first pass, and its second pass uses the kept copy without loading the
stored tree again. Only complete, successfully decoded trees are kept, and only up to a budget (512 KiB of decoded
nodes per restore, `internal/restorer/tree_carry.go`). A tree that was not kept is loaded and verified again in the
second pass, as upstream does.

The consequence, exactly: **if a kept tree's blob is damaged on storage during the restore** (after the first pass
read it, before upstream's second pass would have read it), **this build can finish from its valid kept copy and
report success, where upstream fails with an authentication error when its second load reaches the damaged
storage.** That is the reviewer's witness: a repository with the local cache disabled and a small root tree, which
is kept. With a local cache, which is the default, upstream caches the packs that hold trees, and a valid cached
copy hides the same storage damage from upstream as well. The difference concerns only trees inside the 512 KiB
budget; every other tree is loaded again by both programs. What this build restores is the content of the snapshot:
the kept copy was valid when it was read, and file contents are verified as they are written. What it does not do is report
storage damage that occurs, to those objects, in the minutes of one restore. Run `check --read-data` if you need to
know the state of the storage; a restore is not a check in either program, but upstream's is slightly more of one.

Source: finding 4 of the independent review ([RED-TEAM.md](RED-TEAM.md), section 4); the reviewer's test of it still
fails on this tree by design.

### 5.2 Index files and lock files are encoded differently

Index files are compressed by a streaming encoder and lock files by a hand-written raw-block frame. Both are **valid
Zstandard, decode to the same content, and are read by upstream**. They are **not byte for byte what upstream's
encoder writes** for that content: a few bytes differ, and the lock frame ignores the chosen compression level.

**Data blobs and tree blobs are stored exactly as upstream stores them**: the same chunks, the same IDs, the same
compression decision, the same encoder and options. That was checked in three ways: release check C06 on the judged
commit (11 size comparisons from one initialised repository, all within 0.0005% of upstream's); the size anatomy made
on the first final tree (every data blob ID present in both repositories, 0 blobs with a different stored length);
and the reviewers' own tests that read stored entries back.

What follows from it: a tool that compares two repositories byte by byte will see index and lock files differ.
Their names differ as well, and would between two runs of upstream: an index file is named by the hash of its
stored ciphertext, nonce included, in both programs. Equal decoded content does not imply equal file names, because
the compressed form and the random encryption nonce both change the stored bytes.
Source: finding 11 of the independent review.

### 5.3 The lock-wait contract: a kept index is validated by a listing, not read again

restic waits 200 ms after writing its lock file. This build reads the index (and, for `ls`, one snapshot file)
during that wait. After the lock is confirmed, **for every command except `check`**, what was read is kept only if
a listing taken after confirmation, at the point where upstream lists, still shows every file it came from **with
the same name and the same size**. The files are not read again. `check` reads nothing during the wait.

That validation rests on one assumption: a listing shows a file under its old name and size only if the file can
still be read with the content it had. The schedule tests model listings and deletions; no stale result was observed
in 12,000 schedules in their tested cases. No real cloud backend was tested. Listings that are consistent with
deletions avoid the stale-deletion example below; they do not detect same-length damage on storage (section 5.4).

**Where it does not hold.** On a backend whose listing can still show a file after it was deleted (a directory cache
of a network mount, an eventually consistent object store), this can happen. A backup's lock-file write is slow. A
prune whose lock was confirmed meanwhile deletes packs and replaces the index, all inside the backup's wait. The
listing after confirmation still shows the old index file. The backup keeps the index it read before the prune,
deduplicates against packs the prune just deleted, and **reports success for a snapshot that references deleted
data**. That is the outcome the lock-wait repair R1 was made to prevent, returning in this corner.

In this build it needs two things at once:

1. a lock-file write slow enough for a whole prune to finish inside the window;
2. a listing that lags behind the deletion.

Whether upstream differs there depends on a third thing, its local cache. Upstream lists the same lagging listing
and then reads the files. **Without a cached copy of the old index file upstream fails loudly there**, because the
read finds nothing. **With the file in its local cache, which is the default after a first run, upstream goes on
with the stale index and behaves as this build does.** restic's lock protocol itself decides by listings, so a
backend whose listings lag is outside what locking can make safe in upstream too.

One `Stat` per kept index file after confirmation would close it, at the price of one request per index file per
command on every backend. **We chose not to pay it.** If your backend's listings can lag its deletions, do not run
prune concurrently with other commands; that advice holds for upstream as well.

Source: the review of the fixes, "Addendum 2", section B3 of
[`REVIEW-final.md`](../reviews/soundness/REVIEW-final.md); the triage, "After the fixes"; the witness
`TestWitnessFinal3LockWaitTrustsAListingThatLagsADeletion` there.

### 5.4 Damage on storage inside the 200 ms window (commands other than `check`)

Finding 2 of the independent review was fixed for `check` and narrowed for the rest. What remains, for backup,
restore, ls, forget and prune: an index file that is **changed in place without changing its length**, on storage,
**after this build read it during the lock wait and before the lock is confirmed**, is not noticed by that run. The
kept copy is the content the file had when it was read. Upstream reads the file after confirmation; it would see the
damage if it reads from the backend, and would not if it reads the file from its local cache.

No restic process can cause this: an index file is named by the hash of its stored bytes and is never rewritten in
place. It needs storage damage inside a window of 200 ms plus the time the lock file takes to write. The effect is
that the moment of reading moves earlier by at most that window; damage after upstream's own read goes unnoticed by
upstream in the same way. A change that alters the length is caught (the size comparison), and `check` is not
affected at all.

### 5.5 Smaller differences recorded by the review of the fixes

- **One diagnostic of the lock wait is not held back.** Reports of the reads made during the wait are held and shown
  only if those reads are used. The cache layer's message "error clearing index files in cache" is printed directly,
  and the clean-up of the local cache it reports on runs during the wait. It concerns local files only. So in that
  one case this build can print a line where upstream, whose lock was refused, prints only the lock error.
- **`check --read-data`'s decode ceiling is a constant.** It is 64 GiB, upstream's default. A decoder configured
  with another limit would not be followed; none of check's decoders is so configured.
- **A message that spans a flush of the output collector** (at its size cap, or around one over-long line) counts as
  two or three messages. It shows only when a write fails inside it: the rest is written where upstream gives the
  whole message up.
- **The batched tree producer can make a traversal wait.** In restore's second pass the producer offers its batch
  after each tree it has prepared. A traversal that runs out of trees while the producer is in the middle of
  preparing the next one waits for that tree, although an earlier prepared tree is ready in the producer's hands.
  This is a performance limitation, not an integrity finding (note N6 of the final review,
  [`REVIEW-final.md`](../reviews/soundness/REVIEW-final.md)).

### 5.6 The version string, and `self-update`

- A build from this source reports `restic 0.19.1-dev-dealer.1 (compiled manually)`; the release binaries report
  `0.19.1-dev-dealer.1`. **Snapshots written by this build carry that string in their `program_version` field.**
  That is by design, and it is the one place where a stored file differs in content from what upstream writes for
  the same backup.
- **Do not run `restic self-update` with this build.** It is upstream's code, unchanged: it compares the version
  with upstream's latest release and would replace this binary with upstream's.

### 5.7 Error messages that still name upstream's issue tracker

Eight error messages that this build inherits from upstream end with a request to open an issue at upstream's
tracker (`https://github.com/restic/restic/issues/new/choose`): two in `cmd/restic/cmd_check.go` (damaged pack files,
damaged snapshot files), three in `internal/repository/repository.go` and one in `internal/repository/pack/pack.go`
(data corruption detected while saving), two in `internal/repository/prune.go` (blobs missing from the index).
**That advice is wrong for this build.** A problem with this build is always reported at
<https://github.com/slopdealer/Dealer-Style-Restic/issues> and never to upstream.

The messages were left as they are for this release: changing them changes the binary after it was judged,
reviewed, checked and rebuilt independently. Correcting them is the first change planned for a next source release.

### 5.8 Requests, debug output, budgets

- **Requests.** The snapshot files and the index files are listed during the lock wait and again after the lock is
  confirmed, where upstream lists them: two listings more per process than upstream. On the simulated link the
  repair that introduced them measured 29 to 63 ms per process. For a command given a full snapshot ID, upstream
  lists no snapshot files at all and this build still lists them after confirmation: one more request, no difference
  in what the command decides.
- **Debug output.** With `DEBUG_LOG` set, lines of the work done during the lock wait may interleave with those of
  the lock code.
- **Budgets that are constants.** Restore's read-ahead holds at most 524,288 tree IDs (16 MiB); restore, check and
  repack each have byte budgets fitted to the judge's workloads. They bound memory; they are not tuned for your
  data.

## 6. Where this build is slower, or no faster, than upstream or than the league's last tree

- **Directory-only restores, as measured before the batching commit.** With no read-ahead running, a restore of
  505,050 empty directories took 60.3 s against upstream's 56.5 s on 4 CPUs: 6.7% slower, with more user CPU (38 to 41
  s against 29 to 30 s). (The merge step log's line for it says "6% slower"; the two medians give 6.7%.) The batching
  commit removed the hand-over per directory that this came from. The measurement after it is the table in RESULTS.md,
  section 6.2, which is a different shape (606,060 directories and three files). The empty-directory shape was not
  measured again.
- **Restores of more than 524,288 directories** lose the read-ahead for the trees past that bound.
- **Many directories with many CPUs under a load-following governor**: section 2.
- **First backups of data that does not compress** pay the full compression pass, as upstream does. The league's
  rule that skipped it is gone (F2). The first backup is the phase with the smallest gain against upstream.
- **`check` on a repository with a large index** lost the head start it had from reading during the lock wait
  (5.31% of the check phase on the big-index workload; RESULTS.md, section 5.7).
- **Memory against S020.** This tree uses more memory than the league's last incumbent on all four workloads; the
  figures are in RESULTS.md, sections 1.2, 4 and 5.5. Against upstream it uses less.
- **`remote` against S020.** The repairs R1 and R2 cost requests: `ls` and `forget --prune` on the remote workload
  are slower than S020's.
- **`prune` with one connection** is refused by upstream and by this build alike.

## 7. The key-derivation alignment

restic derives its key with scrypt in every process. The league's parallel derivation has an inner loop whose speed
depends on where the linker places it: at one alignment modulo 64 bytes it runs about 3% slower, 6 to 7 ms in every
restic process. Adding code in front of the loop in the binary can move it. That happened twice (once in a hand merge,
once in a repair) and showed as a slower `ls --json`, a phase that is almost all start-up, with no `ls` code changed.
The published tree is arranged so that the judge's build lands on the fast alignment (in the build of the judged
commit the loop is at `0x8241a0`, 32 modulo 64). **This is a property of one build with one toolchain.** A binary
built for another platform, with another Go version or other flags, lands where it lands. METHODS.md, section 4, says
how to check a binary.

## 8. What was not run

- **The full harness on the published commit itself.** All 16 release checks, C01 to C16, passed on the judged commit
  `b8cf439`; the last of them, the scale check C10, on 2026-10-07 at 19:40 UTC (SOUNDNESS.md, section 8.1). On the
  published commit `8f9372b`, which differs from the judged commit in one comment and one test, only that test was run
  (plain and three times under the race detector), and the two exports were compared file by file.
- **The scale check uses synthetic sizes.** Its 50 GiB file is a sparse file with a few written regions, restored
  with and without `--sparse`. Its large index (3, 10 and 30 million entries) holds invented blob entries with no
  packs behind them and is loaded during a small backup. That does not test a 50 GiB file of incompressible data, or
  a fully populated repository of that size under restore, prune or a full check. Its criterion is memory; its
  timings are single readings (RESULTS.md, section 6.3).
- macOS, BSD, Windows volume snapshots (VSS): not possible on the league's machine.
- The FUSE `mount` tests: the harness runs the test suites with them switched off.
- A repository index written by rustic.
- The full race-detector suite with a count above 1. The one added test, `TestAsyncSaveResultSuppressionIsNarrow`,
  was run with `-race -count=3` on the published source.
- Real object stores (section 4). A real backend with lagging listings (section 5.3 is from modelled backends).
- For the repack read-ahead (A16): blocked upload queues with a source read failure were tested through the
  injected REST backend in checks C14 and C16. Several simultaneous upload failures on a real provider, and long
  stalls in production, remain untested.
- Long-running use. The longest any process ran was a restore of 2 million directories (minutes). No soak test, no
  `mount`, no `rebuild-index` on a damaged repository beyond what upstream's tests do. One fix concerns a leak that
  only a long-lived process shows (`mount --no-lock`); it is tested by a unit test, not by a long run.
- Upstream's tests that fail in the judge's sandbox for environmental reasons (no FUSE, no `python` binary,
  user-namespace ownership: 20 tests) fail for every arm and prove nothing about this build.

## 9. The held-out workload

For every incumbent from S015 to S019, gains transferred to a corpus the teams never saw (RESULTS.md, section 9).
**This tree has not been run on it.** Neither has S020. The claim "the gains transfer" therefore stops at S019. It is
one corpus, it is local, and it says nothing about the remote workload.

## 10. The process

- The code was written by AI agents under a score. The league's rules forbade changing formats, defaults and
  outputs, and the judge enforced what it could see. What it could not see, the agents got wrong at least twice
  (SOUNDNESS.md, sections 2 and 5).
- The reviews were made by AI reviewer agents, in four rounds, and each round found things the one before had not:
  the last, by reviewers of a different model family, found thirteen. That is how review works, and it also means
  nobody should assume the last round found the last defect.
- The independent review ran about 40 minutes per reviewer on one Windows workstation, without the race detector,
  without real concurrent processes and without a real lagging backend.
- No human who maintains restic has reviewed this code.
- The league's documents under `league/reviews/` are working documents. They contain earlier numbers, earlier verdicts
  and statements that later work corrected. Where the documents disagree, those under `league/docs/` are the later
  statement.

## 11. The tools

- The judge needs Linux, root, bubblewrap, a tmpfs with about 8 GB free for the main workload, and pinned CPUs. The
  judge's paths are the league box's (TOOLS.md).
- A run of the judge on your machine is not comparable with the published numbers, only with itself.
- The workload generator for the held-out workload, the league's engine and the operator's audit tools are not
  published.
