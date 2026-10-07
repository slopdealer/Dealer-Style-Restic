# Release checks: real FAILs (written as they are found; newest last)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Each entry: what failed, on which tree, the exact repro, the evidence path on the box, whether stock passes.
Toolchain environment for every `go` command below (the league's pinned offline toolchain):

```
R=/opt/league/restic-league-v2
export PATH=$R/toolchain/go/bin:/usr/bin:/bin GOROOT=$R/toolchain/go GOTOOLCHAIN=local GOMODCACHE=$R/toolchain/gomodcache \
  GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off GOTELEMETRY=off GOCACHE=/home/restic-final-checks/_gocache
```

## F1 (C1, LOW, test file only). `internal/repository/index` tests do not compile for 32 bit

- Tree: `repaired` (`/opt/league/restic-final/src/repaired`, 01d1452). Inherited from S019-merge (same line there).
- What: `GOARCH=386 go test ./internal/repository/index` stops at the build:
  `internal/repository/index/pack_membership_test.go:81:17: constant 4294967296 overflows uint`.
  The league's own test file writes `wide := uint(uint64(1) << 32)` inside `if strconv.IntSize > 32 { ... }`; the
  constant conversion is checked by the compiler on every platform, so on 32 bit none of the package's tests can run
  (stock's index tests included). Production code is not affected: `GOARCH=386 go build ./cmd/restic` is fine.
- Stock: passes (`GOARCH=386 go test ./internal/repository/index` builds and passes on stock).
- Repro: `cd <tree> && CGO_ENABLED=0 GOARCH=386 go vet ./internal/repository/index`
- Evidence: `/opt/league/restic-final/checks/results/repaired/c01/fail-386-github.com_restic_restic_internal_repository_index-PACKAGE.txt`
- Suggested fix (one line): make it a run-time value, e.g. `one := uint64(1); wide := uint(one << 32)`, or move the
  three wide cases into a file with `//go:build amd64 || arm64 || ...` (64-bit only).
- Not ours, for the record: stock's own `cmd/restic` tests do not compile for 386 either
  (`cmd_repair_snapshots_integration_test.go:160: 1234567890123 overflows int`), so no tree can run the `cmd/restic`
  tests on 32 bit.

## F2 (C11, MEDIUM, behaviour differs from stock; no data at risk). `prune --repack-uncompressed` never comes to rest; a prune by the tree stores incompressible blobs in another form than stock

- Tree: `repaired` (01d1452). Cause is the rule repair R4 put into `internal/repository/repository.go:414-424`
  (`autoRawData`): under `--compression auto` a data blob of 512 KiB or more is stored RAW when its zstd output is
  not smaller. Stock stores the zstd output always (`repository.go:396-397`). The same rule runs when prune
  repacks. The unrepaired S019-merge had the older sampler with the same effect (review finding R4); not measured
  separately.
- What, measured on a copy of the judge's big-index repository with a small corpus added (harness C11, starting
  repository `lib/bigindex.py` variant `routes`), both binaries from the same starting copy:
  1. `prune --max-unused 0`: same packs kept and removed, same blobs in the new packs, snapshots unchanged, stock
     `check --read-data` passes for both. But stock `stats --mode raw-data` afterwards: stock's result
     `compression_progress 100`, the tree's `79.84` (total size 26,403,428 against 26,403,205 bytes: the tree saves
     223 bytes). The repacked incompressible blobs are now "uncompressed" blobs in the index.
  2. `prune --repack-uncompressed --max-unused 0 --dry-run` right after that: stock-pruned copy `to repack: 0 blobs`;
     tree-pruned copy `to repack: 16179 blobs`.
  3. After a REAL `prune --repack-uncompressed --max-unused 0` by the tree, the same dry run still says
     `to repack: 16179 blobs` (the stock binary asked on that repository says 16179 as well). Stock after its own
     run: 0. With the tree every `prune --repack-uncompressed` rewrites every pack that holds an incompressible blob
     of 512 KiB or more, each time: on a repository of media or already compressed files that is most of the data,
     downloaded and uploaded again on every run. Stock's flag is a one-time conversion that later costs nothing.
- Stock: passes (0 blobs on the second run).
- Repro: `restic -r REPO prune --max-unused 0; restic -r REPO prune --repack-uncompressed --max-unused 0; restic -r REPO prune --repack-uncompressed --max-unused 0 --dry-run`
  on any v2 repository with a few MiB of random data; or `python3 c11_prune_differential.py <tree> <label>`.
- Evidence (full run on `repaired`, 2026-10-07T06:24Z): `/opt/league/restic-final/checks/results/repaired/c11/repack-uncompressed-twice-tree.txt`
  (the five commands with their output), `.../repack-uncompressed-twice-stock.txt`, `.../prunes.json`.
  In that run all 12 prune pairs (7 option sets, three sizes of repack listing) agree with stock on plan output,
  packs kept and removed, blobs in new packs, snapshot contents and stock `check --read-data`; the stored form
  differs in the 3 pairs with `--max-unused 0` (the ones that repack the incompressible blobs).
- Suggested fix, either: (a) return to stock's rule (always store the encoder's output under auto), which also
  removes the difference in stored form; or (b) keep the raw rule for backup but have a repack that was asked to
  compress (`RepackUncompressed`) store the encoder's output, and let prune not count a raw blob that it would
  store raw again as work. (a) is one branch less and costs about 4 bytes per incompressible blob.

## F3 (C1, LOW, test only). The league's own test `TestRestoreKeepsNoPathPerDirectory` is flaky

- Tree: `repaired` (the regression test of repair M22, `internal/restorer/restorer_directory_state_test.go:75-88`).
- What: it samples the live heap while restoring 3,010 and 12,040 directories and fails above 150 bytes per
  additional directory. The measured value moves between 1 and 336 bytes from run to run:
  `GOARCH=386 go test -count=15 -run '^TestRestoreKeepsNoPathPerDirectory$' ./internal/restorer` fails 4 of 15,
  the amd64 run 7 of 15 (4 CPUs, other load on the box). In the four single runs of C1 it failed once (386).
  Whether the restore itself keeps memory per directory is decided by C10 part B (2 million directories, peak RSS
  against stock), not by this test.
- Stock: does not have the test.
- Repro: the command above, in the tree, with the toolchain environment at the top of this file.
- Evidence: `/opt/league/restic-final/checks/results/repaired/c01/fail-386-github.com_restic_restic_internal_restorer-TestRestoreKeepsNoPathPerDirectory.txt`
- Suggested fix: measure retained memory after the restore with a forced GC (`runtime.GC()` then `HeapAlloc`), or
  count the entries of the structure the repair bounded, instead of sampling the peak of a running heap.

## Status on the frozen copy of the final tree (label `final-d708925`, commit d708925), 2026-10-07T09:50Z

- F1: not present. `pack_membership_test.go` now builds the 2^32 value at run time; `GOARCH=386 go test ./internal/repository/index` builds, 226 tests pass (C9).
- F2: not present. C11 on the final tree: all 12 prune pairs equal to stock including the stored form; after its own prune `prune --repack-uncompressed --dry-run` wants 0 blobs, as stock. The final tree stores no data blob raw under `--compression auto` (x06: 0 raw entries in 76,440; `repaired` 561).
- F3: the test is still in the tree; it did not fail in the four passes of C1 on the final tree. It is a flaky test, so that proves nothing either way.

## Withdrawn: "C06 FAIL, the tree's repository is up to 11.969% larger than stock's" (runs `repaired` 07:32Z and `final-d708925` 09:00Z)

NOT a property of the tree: a defect of the check (mine). C06 let every binary initialise its own repository. `restic init`
draws a random chunker polynomial, and two repositories with different polynomials cut large files at different places, so
they hold different blobs for the same input. Stock against stock on a second init: -1.11% on the judge's main corpus
(1,304,549,880 against 1,319,177,009 bytes), -3.05% and +11.7% on the generated corpus (v2 off and v2 auto, pack size 128).

From copies of ONE initialised repository (`x06_size_anatomy.py`, results `results/final-d708925/x06/anatomy.md`, `runs.json`),
every binary twice:

| input | stock | final tree | S019-merge | repaired | S001 |
|---|---|---|---|---|---|
| judge main corpus, default options (bytes in data/) | 1,319,177,009 / ...045 | 1,319,177,045 / ...045 | 1,319,147,283 (562 blobs raw, -27,514 B) | 1,319,147,249 (561 raw, -27,552 B) | as stock |
| generated, v2 off, pack 128, two backups | 67,924,191 / ...358 | 67,924,406 / ...141 | within 300 B | within 300 B | within 300 B |
| generated, v2 auto, pack 128, two backups | 46,210,288 / 46,209,943 | 46,210,112 / 46,209,994 | 21 raw, -1,053 B | 21 raw, -1,053 B | as stock |

In every run: index entries = distinct blob IDs (0 duplicate data blobs, 0 duplicate tree blobs), every data blob ID present in
both stock and the tree, 0 blobs with a different stored length in the final tree and in S001, pack-header overhead equal (3,180,118
to 3,180,154 bytes at 75 or 76 packs on main; the 36 bytes are one pack more or less). The suspected duplicate data blobs from
concurrent workers do not occur in any of the four league trees: `saveBlob` marks a blob pending before it is stored exactly as
stock does (`internal/repository/repository.go:1361` calls `MasterIndex.AddPending`, `index/master_index.go:93-111`; stock: `:1042`, `:78-96`).
C06 now writes both binaries into copies of one initialised repository (tolerance 0.1% + 64 KiB).

(There is no F4: the number was reserved for the repository-size alarm, which is withdrawn above.)

## F5 (C10 part B, LOW to MEDIUM; finding M22, repaired in part). Restore memory grows with the number of directories; stock's does not

- Trees: `final-d708925` and `repaired` (same numbers); the unrepaired S019-merge is worse.
- What (x10_dirs_memory.py on the final tree copy, `restore latest` into an empty target, peak RSS read through a
  10 MiB helper process, snapshot of empty directories three levels deep with names of 5 and 3 bytes):

  | directories | stock | final tree | unrepaired S019-merge |
  |---|---|---|---|
  | 1,010,100 | 63.0 MiB | 131.1 MiB | 332.5 MiB |
  | 2,020,200 | 62.2 MiB | 231.2 MiB | 583.2 MiB |
  | per additional directory | 0 bytes | **104 bytes** | 260 bytes |

  C10 part B read the same cell as stock 57 MiB / tree 230 MiB on both trees (its criterion: more than 1.3 x stock + 64 MiB fails).
- Where the memory is (heap profile at the end of the restore of 2,020,200 directories, final tree built with the
  `profile` tag, `--mem-profile`): 77.62 MiB in use, 75.17 MiB of it (96.85%) allocated at
  `internal/restorer/restorer.go:736`, `restoreTreeIDs = append(restoreTreeIDs, id)`: one 32-byte tree ID per
  directory visited in the first pass, kept in a slice for the second-pass prefetch (`:834-843`,
  `newRestoreTreePrefetchWithCarry`). That is 39 bytes in use per directory (32 for the ID, the rest is the spare
  capacity append leaves). Nothing else in the profile grows with the directory count (next entry: 1.06 MiB, the
  index). Peak RSS is 104 bytes per directory and not 39 because the slice is regrown by append (old and new
  backing array alive together, the old one garbage until the next collection, default GOGC).
  The list is filled even when the prefetch that reads it is not started (`:834`: only when there are files to write).
- Stock: keeps nothing per directory (flat at 62 to 63 MiB).
- Repro: `python3 x10_dirs_memory.py <tree> <label>` (30 minutes), or a restore of any snapshot with a million
  directories under `/usr/bin/time -v`.
- Evidence: `/opt/league/restic-final/checks/results/final-d708925/x10/` (progress.log, `profile-tree/mem.pprof`,
  `profile-tree/top-lines-inuse_space.txt`).
- Suggested fix: bound the list (keep the first N tree IDs, for example 64,000 = 2 MiB, and let the second pass
  load the rest itself as stock does), or drop the list when the first pass ends without files to write. A cap
  costs the prefetch only on snapshots with more directories than the cap.

## Status on the judged commit b8cf439 (label `final3-b8cf439`, export rc-8), 2026-10-07T19:00Z

All of C01 to C09 and C11 to C16 PASS (C10 running). F1, F2: not present. F3: the test passed in all four passes of C1. F5: fixed at the size
measured so far (606,060 directories: peak RSS 58.1 MiB against stock 57.0 MiB); the 2,020,200-directory cell comes with C10.

## F6 (x11, performance observation, not a correctness failure). With 16 CPUs a restore of many directories is 10% slower than stock; with 4 cores it is 27% faster

- Tree: rc-8 (b8cf439). rc-7 (d4f7e81) was +27% / -10% in the same two set-ups.
- What: restore of a snapshot with 606,060 directories and 3 files into an empty ext4 target, three rounds in rotated order
  (stock / rc-8 / rc-7), `sync -f` before every restore. Median wall seconds, user + system CPU seconds:
  16 logical CPUs (8 cores + SMT): stock 67.2 s (28.6 + 41.9), rc-8 74.0 s (39.4 + 57.8), rc-7 85.5 s (52.5 + 65.8).
  4 cores: stock 86.5 s (39.0 + 53.9), rc-8 63.3 s (31.7 + 47.6), rc-7 77.6 s (43.6 + 58.6).
  The spread between rounds is under 2.5%, so the order of the runs is not the cause. Stock gains from more CPUs, the tree loses:
  with 16 CPUs it spends 97 CPU seconds where it spends 79 on 4 cores (stock: 70.5 and 92.8).
- Stock: is the reference; faster than the tree on 16 CPUs, slower on 4 cores.
- Repro: `CHK_X11_TOPS=60 CHK_X11_ROUNDS=3 CHK_EXTRA_TREES=rc7=<rc-7> bash run_all.sh <tree> <label> x11` (25 minutes, alone on the check CPUs),
  or `restic restore latest --target T` on such a snapshot with and without `taskset -c` on four cores.
- Evidence: `/opt/league/restic-final/checks/results/final3-b8cf439/x11-606060/table.md`, `runs.json`.
- Not examined: where the extra CPU time goes on 16 CPUs (a CPU profile of the restore with GOMAXPROCS 16 against 4 would show it; the
  per-directory work is spread over more workers than the file system serves in parallel is the obvious candidate).
