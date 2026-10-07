# REVIEW-s020: soundness of what S020 added on top of `S019-merge`

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Scope: `review\FILES-S020.md` (23 new and 11 changed non-test files). Trees: `trees\S019-merge` (reviewed base),
`trees\S020` (new), `trees\stock` (ba802d42). Paths below are relative to `trees\S020\`.
Method: every changed file diffed against S019-merge, every new file read; nine witness test files (eight new, one
reused from the read-side review), seven run on Windows (go1.27.0, amd64 and as 32-bit binaries), two are
Linux-only and compile-checked for the box.
Workstation only: no box access, no API calls, $0. Date: 2026-10-07. Step log: `review\PROGRESS-s020.md`.

## 1. Summary

16 mechanisms judged.

| Verdict | Count |
|---|---|
| UNSOUND, BLOCKER | 1 (R1 again, reproduced on S020; the chosen repair does not apply to S020) |
| UNSOUND, HIGH | 1 (the code path is certain by reading; its side effects are standard Linux semantics, none reproduced: Linux-only, not run here) |
| SUSPECT, HIGH | 1 (reproduced with modelled probes; the real probes are Linux-only, not run here) |
| UNSOUND, MEDIUM | 1 (reproduced; R7 again in a new file) |
| MEDIUM, integration | 1 (repair R4 and this season's code cannot be merged mechanically; verified) |
| SOUND-UNDER-CONTRACT | 5 |
| SOUND | 4 |
| LOW (should not ship as is) | 2 (one switched-off mechanism, one hygiene group) |

The short version:

- **Prune and index, where the measured gain is (+3.46% big-index): sound.** S020 does not touch a single keep /
  repack / delete decision. `decidePackAction` and `Execute` are stock's code. The delta is three shortcuts in
  how the same numbers are computed and how the blobs of the packs to repack are listed. All three give stock's
  result on randomized states (600 for the pack table, 400 for the count test, 148 index states with 502 pack
  listings) and in 13 end-to-end prunes, also as 32-bit binaries.
- **The three lock-wait mechanisms each bring their own entrance to the R1 defect.** Backup, `check --read-data`
  and restore now start their wait work through three NEW constructors that read the snapshot list and the index
  before the lock is confirmed and keep them. The reviewers' R1 witness run on S020 fails 3 of 3, the backup case
  with data loss. `repairs\R1-validate\0001` does not apply to S020: it fails in exactly the four files where
  these mechanisms hook in. Porting the repair by hand is mandatory; taking S020's side of the conflict leaves
  the BLOCKER open in backup.
- **Two of the three new mechanisms should be dropped.** The backup one walks the backup SOURCE before the lock
  is confirmed and calls `open(2)` on every entry before it knows what it is (FIFOs, device nodes). The check one
  lets `check --read-data` certify one pack from bytes read before the lock, on the strength of a `stat` and an
  inotify watch that do not see changes on network or FUSE mounts. Both are small, benchmark-shaped gains.
- **The restore one is mostly switched off.** The tree preparation is called with `prepareTrees = false`
  (`cmd/restic/cmd_restore.go:154`); about 250 lines ship unreachable. What runs is the snapshot-file preload `ls`
  already had in S019, and a new tree decoder for the metadata pass that repeats the R7 leniency.

What was NOT found: no weakened hash, MAC or verification on the save path; no change of repository format
(a prepared compressed blob is byte for byte the repository encoder's output); no `unsafe`; no validation that is
only a debug check. On a count mismatch prune behaves as stock: `ErrIndexIncomplete` or the stock panic.

## 2. Witness results

Files are in `review\witness-s020\`. The prefix of a file name is the package it belongs in. Copy it into that
package directory of the tree and run the command from the tree root.

| Witness file (copy to) | Test | OS | Result on S020 |
|---|---|---|---|
| `cmd_lock_wait_stale_witness_test.go` (`cmd/restic`), the read-side reviewer's file, unchanged | `TestWitnessLockWaitStale*` | any | **FAIL 3 of 3**: prune beside backup fails where stock succeeds; restore beside prune fails where stock succeeds; **backup beside prune reports success and `check` then finds the snapshot broken (data loss)** |
| `repository_s020_check_wait_pack_witness_test.go` (`internal/repository`) | `TestWitnessS020CheckWaitPackBlindProbes` | any | **FAIL**: with probes that cannot see the change, check verifies a pack that was damaged after the early read and reports no error; the control (pack read now) reports 2 errors |
| `data_s020_tree_metadata_witness_test.go` (`internal/data`) | `TestWitnessS020MetadataTreeNameLeniency` | any | **FAIL**: raw quote and raw newline in a node name accepted where stock rejects. Same result with the R7 patch applied |
| same file | `TestWitnessS020MetadataTreeDifferential` | any | PASS: 17,842 trees (6,479 accepted by stock, 11,363 rejected), same nodes and same errors as stock's decoder |
| `index_s020_listpacks_witness_test.go` (`internal/repository/index`) | `TestWitnessS020ListPacksDifferential` | any | PASS: 472 selections, all three listing paths taken (collect 383, routes 15, fallback 74) |
| same file | `TestWitnessS020ListPacksWhileIndexGrows` | any | PASS: 30 route listings while new packs are stored and indexes merged between receives |
| same file | `TestWitnessS020AssociatedCountsInRangeDifferential` | any | PASS: 400 states x 5 ranges (compact path 83, loop path 317) |
| `repository_s020_prune_witness_test.go` (`internal/repository`) | `TestWitnessS020PackInfoDifferential` | any | PASS: 600 states against stock's `packInfoFromIndex` (copied verbatim): same error, diagnostic, pack table, statistics and keep set; 117 states end in `ErrIndexIncomplete`, 361 have duplicates |
| same file | `TestWitnessS020PruneEndToEnd` | any | PASS: 13 real prunes (see finding 12), then stock's check with all data read and every kept blob reloaded |
| `repository_s020_checker_pack_types_witness_test.go` (`internal/repository`) | `TestWitnessS020CheckerPackTypes` | any | PASS: 40 index sets; 273 incomplete, 219 duplicate, 181 mixed hints, all as the layout demands |
| `repository_s020_compression_preparation_witness_test.go` (`internal/repository`) | `TestWitnessS020CompressionPreparationBytes` | any | PASS: prepared bytes equal the repository encoder's output; an entry holding the compressed form of other data is rejected; everything reads back |
| `repository_s020_check_wait_pack_linux_witness_test.go` (`internal/repository`) | `TestWitnessS020CheckWaitPackRealProbes` | **Linux** | not run. Compiles for linux/amd64 (`go vet`, `go test -c`). Seven kinds of change to the pack after the early read |
| `cmd_s020_backup_compression_source_linux_witness_test.go` (`cmd/restic`) | `TestWitnessS020BackupWait*` | **Linux** | not run. Compiles for linux/amd64. Expected FAIL: a writer blocked on a FIFO in the source is released by the lock-wait walk |

Logs: `review\witness-s020\logs\`. The amd64 runs, the 32-bit runs (`witness_s020_windows_386.log`: same
results) and a cross-build of `./cmd/restic` for 13 targets (`build-matrix-s020.log`: all pass).

Run on Windows from the scratch copy `review\scratch-s020\tree`:

```
go test -run TestWitnessS020 -count=1 -p 2 -v ./internal/repository/index ./internal/repository ./internal/data
MSYS=winsymlinks:lnk go test -run TestWitnessLockWaitStale -count=1 -p 2 -v ./cmd/restic
```

## 3. Findings, most severe first

| # | Mechanism | Files:lines | Verdict, severity | Contract or failure scenario | Witness or deciding experiment |
|---|---|---|---|---|---|
| 1 | **R1 again: three new wait-work constructors read the snapshot list and the index before the lock is confirmed and keep them** | `cmd/restic/backup_compression_index.go:11-27` (used at `cmd_backup.go:546`); `cmd/restic/check_wait_pack.go:18-37` (used at `cmd_check.go:249-251`); `cmd/restic/restore_snapshot_wait.go:24-67` (used at `cmd_restore.go:153-155`); `cmd/restic/wait_index_load.go:19-20,50-52` | **UNSOUND, BLOCKER** (reproduced). Not a new defect: R1, live in S020, at three new call sites | Backup and check each got a copy of the S019 loader with one thing added (a readiness signal; the pack owner: "retains the original snapshots-before-index sequence"). Restore wraps the S019 loader and takes over its settlement. Scenario as in REPAIRS.md R1: backup B's lock upload takes more than 200 ms, a prune finishes inside B's wait, B deduplicates against pruned packs and reports success. The repair chosen for R1 does not reach these copies: `git apply --check repairs\R1-validate\0001-*.patch` on S020 fails in `cmd_backup.go:539`, `cmd_check.go:244`, `cmd_restore.go:148`, `wait_index_load.go:5`. Of the 16 repair patches, 14 apply to S020 in sequence; the two that fail are this one and R4 (finding 5). The S020 files build `backupIndexLoad{...}` and `checkerIndexLoad{...}` literals by hand; after the repair `backupIndexLoad` is an alias of the validated type, so a hand merge that keeps S020's constructor may still compile and skip the validation | `cmd_lock_wait_stale_witness_test.go`: FAIL 3 of 3 on S020, log `logs\cmd_lock_wait_stale_witness.S020.log`. After the port: this file plus the repair's `TestLockWaitSchedules` must pass on the merged tree (NEEDS-RUN 5) |
| 2 | **Backup "compression preparation": the walk over the backup source during the lock wait** | `cmd/restic/backup_compression_source_linux.go:20-42` (open at 26, type test at 30-36); started from `cmd/restic/backup_compression_wait.go:20-44`; option guard at `:21` | **UNSOUND, HIGH** (the open before the type test is certain by reading; the side effects named are standard Linux semantics, none reproduced: Linux-only, not run here) | (a) The walk calls `open(name, O_RDONLY\|O_NOATIME\|O_NOFOLLOW\|O_NONBLOCK)` on every entry it visits and asks only afterwards whether it is a regular file. Stock never opens a FIFO, socket or device node of the source (`internal/archiver/archiver.go:492`: metadata-only handle, `MakeReadable` for files and directories only). Opening has side effects. FIFO: a process blocked in `open(fifo, O_WRONLY)` is released and its write gets EPIPE or is lost. Device nodes, when backup runs as root over a tree that holds them (a root file system, a chroot, `/dev`): the driver's open and release run; for a free `/dev/watchdog` that starts the timer and, with magic-close drivers, can leave it running (host reset); a tty opened without `O_NOCTTY` by a session leader (a systemd service) can become its controlling terminal; `/dev/st0` rewinds on close. Conditions: Linux, repository version 2, `--compression auto` (default), none of `--exclude*`, `--one-file-system`, `--exclude-caches`, `--exclude-if-present`, `--exclude-larger-than`, `--with-atime`, `--dry-run`, stdin; the index loads inside the 200 ms wait (a small repository); the entry is among the first 2,048 visited (128 names per directory, depth 16) before the shelf is full (8 MiB read or 8 entries). (b) Every run reads up to 8 MiB of the source (2 MiB per file of 512 KiB or more) before the lock is confirmed, also when nothing changed; stock opens no unchanged file when a parent snapshot exists. On a slow or recalling source (FUSE cloud mount, HSM) that is new I/O, and confirmation waits for the read in flight (`backup_compression_wait.go:50-51`) | `cmd_s020_backup_compression_source_linux_witness_test.go` (FIFO; expected FAIL) and the `strace` check, NEEDS-RUN 1. Do not test with a real watchdog device |
| 3 | **`check --read-data` reads one pack (at most 2 MiB) during the lock wait and verifies those bytes later instead of reading the pack again** | `internal/repository/check_wait_pack.go:50-61,63-95,97-144,192-220`; probes `check_wait_identity_linux.go:16-26` (dev, inode, size, mtime, ctime, mode from `stat`), `check_wait_watch_linux.go:17-37` (inotify: MODIFY, ATTRIB, CLOSE_WRITE, MOVE_SELF, DELETE_SELF, UNMOUNT); replay `check_wait_range.go:26-62`, `internal/backend/retry/prepared_load.go:34-38`; hooks `checker.go:137,487-488`, `checker_capacity.go:58-59`; `cmd/restic/check_wait_pack.go`, `cmd_check.go:247-256,269,453-455` | **SUSPECT, HIGH** off the contract (network and FUSE mounts); SOUND-UNDER-CONTRACT on local disk file systems. Reproduced with modelled probes, not on a real mount | What it does right: the held bytes must hash to the pack ID; every blob check, the header check and the index membership test still run on them; they are spent before the verifier runs, so a failed first attempt reads the backend (stock's "successful on second attempt" path); on any other OS or backend the mechanism is off. What it rests on: that the pack on the backend still has those bytes when check reports it intact. That is inferred from `stat` and inotify, not read. **Contract:** between the early read and the pack's turn in the checker, every change to the file's content is made through this host's VFS and either raises one of the watched inotify events or changes dev / inode / size / mtime / ctime / mode. Holds on ext4, xfs, btrfs and the like for writes, truncation, rename-over, unlink and writes through a hard link. Does NOT hold: (1) a repository on NFS, SMB, sshfs, rclone mount or any FUSE mount, which `local.Local` cannot tell from a disk and where the mechanism is therefore active: changes made on the server raise no inotify event on the client, and `stat` answers from the attribute cache (NFS default 3 to 60 s); (2) by the reviewer's reading of Linux semantics, not run: a write through a shared mapping whose page was already dirty (no fault, so no timestamp; inotify does not report mmap writes); (3) anything below the file system. Scenario for (1): `check --read-data` on host A, repository on a NAS mount; the pack is read at 50 ms; it is overwritten or deleted on the server at 100 ms; at 250 ms the checker reaches it, `stat` returns the cached attributes, the watch is silent, the memory copy verifies, `check` prints "no errors were found". Stock reads the file at 250 ms and reports it. The window is the attribute cache time and needs an outside change inside it, which is why this is HIGH and not BLOCKER; by the letter of the rubric it is a weakened integrity check. The gain is one read of at most 2 MiB per run | `repository_s020_check_wait_pack_witness_test.go`: FAIL (real capture, settle, offer and verifier code, real local repository; only the two probes replaced by a model of a network mount). Decided for real by `repository_s020_check_wait_pack_linux_witness_test.go`, NEEDS-RUN 2, including the NFS loopback variant |
| 4 | **`LoadMetadataTree`: node names are unquoted only when they contain a backslash** | `internal/data/tree_metadata.go:124-129`; used by `internal/restorer/restorer.go:817` (second pass of restore) | **UNSOUND, MEDIUM** (reproduced). R7 again, in a file the R7 repair does not know | A node name that after JSON decoding holds a raw `"` or a raw newline and no backslash: stock fails with `Unquote: invalid syntax`, this decoder accepts it. restic never writes such a name; it concerns trees written by other tools. By reading: once R7 makes the first pass strict, the second pass still accepts the tree, creates its directories, links and special files, and the error is counted once instead of twice. `repairs\R2-R10\0006` (R7) applies to S020 but does not touch this file: the witness fails the same way with R7 applied | `data_s020_tree_metadata_witness_test.go` `TestWitnessS020MetadataTreeNameLeniency`: FAIL on S020 and on S020 + R7 |
| 5 | **Repair R4 and the compression shelf collide** | `internal/repository/compression_preparation.go:125,172-190` (own copy of the 16-window sampler, built on `autoCompressionMinSampledSize`, `...SampleSize`, `...SampleWindow`, `...SampleWindows`); `internal/repository/repository.go:38-41,433-452` | MEDIUM, integration (verified with `git apply` and by reading) | `repairs\R2-R10\0003` (R4) deletes those four constants and rewrites the lines where S020 inserted `consumeCompressionPreparation` (`repository.go:439-442`). The patch does not apply to S020, and with R4 merged by hand `compression_preparation.go` does not compile. Semantics, for the answer to "does it interact with the sampler R4 removes": the shelf's sampler only decides whether to PREPARE a chunk, never how it is stored; a prepared entry equals the encoder's output; R4's exact rule (`len(compressed)+4 >= len(data)` stores raw) would apply to it unchanged. For the R4 witness blob the shelf prepares nothing. So the shelf does not reintroduce R4, but it cannot be kept without rewriting both | `git apply --check` on S020: fails in `internal/repository/repository.go`. `TestWitnessS020CompressionPreparationBytes`: R4 blob examined, rejected, no entry |
| 6 | **The compression shelf itself: at most 8 compressed chunks (256 KiB) made during the wait, offered to `saveAndEncrypt`** | `internal/repository/compression_preparation.go:50-55,64-155,192-196,252-310`; `repository.go:89,439-442,1428-1434` | SOUND-UNDER-CONTRACT | Nothing about the repository survives through the shelf: an entry is (blob ID, length, compressed bytes) of a source chunk. It is used only for a data blob with that ID and length and only after the compressed bytes were decoded and compared with the plaintext the archiver read (`:266,282-310`), under the shelf's mutex. A source file that changed, a wrong chunk boundary or a damaged entry costs the entry, nothing else. The lookup in the wait-loaded index (`:134`) only skips work. Contract: the producer is cancelled and joined before `Activate` (it is: `backup_compression_wait.go:46-58`); the private encoder has the repository's level, window and CRC setting (`:176` against `repository.go:366-372`). Cost off the benchmark: a mutex taken for every new data blob of the whole backup only to count (`:223-232`) | `TestWitnessS020CompressionPreparationBytes` PASS: 3 entries byte-identical to `getZstdEncoder().EncodeAll`; a foreign entry rejected once; all blobs read back; check passes |
| 7 | **Restore: the snapshot file is fetched during the wait (the S019 `ls` mechanism, now also in restore)** | `cmd/restic/restore_snapshot_wait.go:24-27,69-121`; `cmd_restore.go:151-155,161,170`; S019 code it calls: `cmd/restic/snapshot_metadata_wait.go`, `internal/repository/prepared_snapshot.go` | SOUND-UNDER-CONTRACT | One ciphertext of at most 64 KiB, checked against its name (`prepared_snapshot.go:50`), replayed once into the ordinary `LoadUnpacked`, which checks it again. Contract: the snapshot ID it is asked for comes from a listing taken AFTER confirmation. As S020 stands the listing is the wait's (finding 1); with the R1 repair ported it holds. Then the only difference from stock is the repair's B-2 case | Read. Settlement tests of the tree pass on Windows (`TestRestoreSnapshotWait*`) |
| 8 | **`LoadMetadataTree` returns nodes without content IDs for the second pass of restore** | `internal/data/tree_metadata.go:15-62,64-101,103-123`; `internal/restorer/restorer.go:814-820,843-845` | SOUND-UNDER-CONTRACT | Contract: no second-pass visitor reads `Node.Content`. True today: the only reads are in the first pass (`restorer.go:786`) and in `verifyFile` (`:1181-1183`); hard links, metadata, `--delete` and progress do not use it. Everything else about a node, and every error, equals stock's decoder: content IDs are still parsed and validated one by one, any decode error is replayed through `Node.UnmarshalJSON`. A future visitor that reads `Content` in the second pass would get an empty list without any error | `TestWitnessS020MetadataTreeDifferential` PASS (17,842 trees, byte mutations included) |
| 9 | **One-shot "prepared range" in the retry backend** | `internal/backend/retry/prepared_range.go:9-15`; `prepared_load.go:34-38`; `internal/repository/check_wait_range.go:13-62` | SOUND-UNDER-CONTRACT | A context value can answer exactly one `Load(handle, length, offset)` with bytes held in memory. Matched on owner, handle, length and offset; spent before the consumer runs, so a retry reads the backend. Contract: whoever puts the value into a context vouches for the bytes. The key type is exported (`retry.PreparedRangeKey`): any package can substitute the content of a backend read. Only the check mechanism uses it; it goes with finding 3 | Read. `TestPreparedRange*` of the tree pass |
| 10 | **Repack routes: the blobs of the packs to repack are located once (ordinals in the merged index) instead of once per group of packs** | `internal/repository/index/repack_routes.go:10-11,22-81,83-110,112-135`; `master_index.go:733-735` | SOUND-UNDER-CONTRACT | Used when the selection exceeds the bounded collector (512 packs or 8,192 blobs) and fits 65,536 slots, and only when the master holds exactly one final index of at most 131,072 packs; otherwise the fallback. Contract: after the routes are built no index entry is added for a SELECTED pack, and entries are never removed or moved. True for repack (it adds entries for new packs only; merges append; positions are stable by design of `indexMap`). No lock is held while a pack is handed to the consumer. If the contract broke, a blob would be missing from the listing; stock's "was not repacked" test (`prune.go:646-652`) stops prune before any pack is removed | `TestWitnessS020ListPacksDifferential`, `TestWitnessS020ListPacksWhileIndexGrows` PASS, also as 32-bit |
| 11 | **Checker: pack types taken from the per-index-file groups instead of a second walk over the merged index** | `internal/repository/checker_pack_types.go:11-37`; `checker.go:116,138-140,165-171` | SOUND | Only on a fresh load (empty master); otherwise stock's walk. The map is also the list of packs for which `LoadIndex` reports its hints, and "incomplete pack entry" is an error in `check`: a pack missing from the map would lose that error. Every indexed pack has at least one blob in some loaded file, so the key sets are equal; a type differing inside a group or between groups gives `InvalidBlob`, as stock | `TestWitnessS020CheckerPackTypes` PASS |
| 12 | **Prune: `AssociatedCountsInRange` replaces the two loops over the used-blob set** (the missing-blob test and the sanity panic) | `internal/repository/index/associated_count_validate.go:6-51`; `internal/repository/prune.go:207-214,351-353` | SOUND | The compact path (scan of the two arrays) is taken only with an empty overflow map, exactly one final index and arrays sized to it; otherwise the loop over `All()` runs, as stock. A set position is always the first position of an indexed ID, so both see the same counts. A used blob that is in no index sits in the overflow map, forces the loop and gives `ErrIndexIncomplete` with stock's message. Not a debug check: it is the check. A mismatch is loud (error or panic), the change of path is silent and has the same result | `TestWitnessS020AssociatedCountsInRangeDifferential`, `TestWitnessS020PackInfoDifferential`, `TestWitnessS020PruneEndToEnd` PASS. End to end: 13 prunes: `--max-unused` 0 / 5% / unlimited, `--max-repack-size`, `--repack-cacheable-only`, `--repack-smaller-than`, `--repack-uncompressed`, unsafe recovery, an interrupted prune followed by another, packs missing from the index, an index entry without its pack (prune refuses, as stock); selections sized for each of the three listing paths |
| 13 | **Prune: `prunePackRun` keeps the pack record of consecutive blobs in a variable instead of a map read and write per blob** | `internal/repository/prune_pack_run.go:9-31`; `prune.go:243,249,291,314,343` | SOUND | One sequential owner; written back when the walk moves to another pack and after each walk, before the error test; a revisited pack is reloaded from the map. Same order of updates as stock, duplicate selection included | `TestWitnessS020PackInfoDifferential` PASS (duplicates across packs, more than 255 copies of a blob, mixed packs, split packs, an unsaved index) |
| 14 | **`ListPacks` (check, repair) through a selective scan with a per-index bitmap of selected packs** | `internal/repository/index/repack_fallback.go:14-91`; `repack_ordinals.go:13-29`; `master_index.go:702-704,737-740` | SOUND | Stock's 16 passes with stock's lock lifetime; only entries of selected packs are turned into blobs. The bitmap is per position in `Index.packs`, so a pack ID that appears twice after a merge is handled; it is rebuilt under the read lock for every pass and dropped. Requested packs without entries are still emitted | `TestWitnessS020ListPacksDifferential` PASS |
| 15 | **Restore: trees read and decoded during the wait** | `internal/repository/restore_wait_reader.go` (107 lines); `internal/restorer/restore_wait_trees.go` (101 lines); `cmd/restic/restore_snapshot_wait.go:29-64,141-148`; `restorer.go:28,653,742`; `wait_index_load.go:19-20,50-52` | LOW: unreachable in the shipped binary | `cmd_restore.go:154` passes `prepareTrees = false`; only tests pass `true`. Its design is sound in principle once the snapshot comes from a validated listing (trees are addressed by content and authenticated), but it was not reviewed to the depth of the live code, and it is not what was measured and promoted. Should not ship | Read |
| 16 | **Hygiene group** | section 7 | LOW | Debug switches, statistics structs, benchmark constants, formatting, one test of the tree failing on Windows | `gofmt -l`; targeted runs |

## 4. The three lock-wait mechanisms: (a) (b) (c)

### 4.1 Backup compression preparation

What is read or decided during the wait: the snapshot list and the index (the R1 loader copy); then, once the
index is loaded, directory entries of the backup source and up to 8 MiB of file content; chunk boundaries of that
content, which chunks the wait-loaded index does not know, and their compressed form. What survives confirmation:
the snapshot list and index (finding 1), and at most 8 shelf entries.

- **(a) Another instance of R1 as it stands?** Yes for its loader `startCompressionBackupIndexLoad`: this is the
  very path of R1's data-loss scenario, in a constructor the repair has not seen. No for the shelf: it holds
  nothing read from the repository.
- **(b) Does "validate" make it sound?** Yes, for the repository side, if the port routes this constructor
  through the validated load (list snapshots and index again after confirmation, reread on any removal). The
  shelf needs no validation against the repository: every entry is checked against the plaintext at use. A pack
  rewritten in place is not a concern here, no repository file content is held. "Validate" does nothing for
  finding 2, which is about the source, not the repository.
- **(c) What must be discarded?** On a removal: the index and the snapshot listing of the wait, as in R1.
  The shelf may be kept; releasing it too costs nothing.

### 4.2 Check reading a pack during the wait

- **(a) Another instance of R1 as it stands?** Yes for its loader `startCheckerWaitPackLoad` (snapshots, index
  and pack sizes read in the wait and kept): R1 for check, loud, not silent (false errors from a stale index).
  The held pack is R1-shaped (read in the wait, used after) with two differences: the bytes are verified against
  the file's name when read, and the file is probed again when they are used.
- **(b) Does "validate" make it sound?** No. The repair lists snapshot and index files; neither says anything
  about a pack file. Listing `data/` as well would show a removal or another size, not a rewrite in place under
  the same name with the same size, and "is the content under this name still what the name promises" is the
  question `check --read-data` exists to answer. The mechanism therefore needs more, and has more: the local
  identity (dev, inode, size, mtime, ctime, mode) and the inotify watch. That is enough on a local disk file
  system and not enough on a network or FUSE mount (finding 3). It cannot be made sound on every file system
  without reading the file again, which is the work it saves.
- **(c) What must be discarded?** The held ciphertext and its watch, whenever the validation rereads and whenever
  a probe fails (the second it already does). And the checker, index, pack sizes and snapshot listing built by
  `startCheckerWaitPackLoad`, as the repair does for `startCheckerIndexLoad` (it builds a new checker).

### 4.3 Restore reading the snapshot (and, switched off, the trees) during the wait

- **(a) Another instance of R1 as it stands?** Yes: the snapshot file is chosen from the wait's listing and its
  ciphertext is kept. It is the instance `ls` had in S019, now in restore. The tree preparation would be one too
  (tree blobs read through the wait's index); it is not reachable.
- **(b) Does "validate" make it sound?** Yes. Snapshot files and tree blobs are named by the hash of their
  content, checked at preparation and again by the ordinary load on replay. If the listing taken after
  confirmation still has the snapshot ID, the held bytes are what a load would return. A rewrite in place cannot
  go unnoticed in the dangerous direction: the candidate would use correct bytes where stock reports a damaged
  file (the repair's B-2 case). No identity check is needed.
- **(c) What must be discarded?** The wait's snapshot listing, the index, and the prepared snapshot ciphertext
  (drop it when everything is reread, as the repair does for `ls`). The tree shelf as well, should it ever be
  switched on.

## 5. Recommendation for the final candidate

| # | Mechanism | Recommendation | Reason |
|---|---|---|---|
| 1 | Backup compression preparation (`cmd/restic/backup_compression_*.go`, `internal/repository/compression_preparation.go`, hunks `cmd_backup.go:541-565`, `repository.go:89,439-442,1428-1434`) | **DROP** | Three repairs would be needed for it to ship: stat before open and files and directories only (finding 2), the R1 port to its loader (finding 1), a rewrite against R4 (finding 5). It reads the user's source outside the archiver and before the lock. Its gain exists only when the index loads inside 200 ms and the first files of the source are compressible chunks of 512 KiB to 1 MiB: at most 8 chunk compressions per backup. If the box shows it carries real weight on `main`: KEEP WITH those three repairs |
| 2 | Check reading a pack during the wait (`internal/repository/check_wait_*.go`, `cmd/restic/check_wait_pack.go`, `internal/backend/retry/prepared_range.go` and `prepared_load.go:34-38`, hooks `checker.go:84,137,487-488`, `checker_capacity.go:58-59`, `cmd_check.go:247-256,269,453-455`) | **DROP** | It trades the plain statement "`check --read-data` reads every pack after the lock is confirmed" for one read of at most 2 MiB. Its guard is a property of the file system, and restic cannot know the file system behind a local path. A repair exists (an allow-list of file system types from `statfs`), but it leaves about 420 lines with an OS split in the integrity command for a gain that is a page-cache read |
| 2b | Checker pack types from the load callbacks (`checker_pack_types.go`, `checker.go:116,138-140,165-171`) | **KEEP AS IS** | Sound, witnessed, independent of the lock wait |
| 3 | Restore snapshot preload (`cmd/restic/restore_snapshot_wait.go:24-27,69-121`, `cmd_restore.go` hunks) | **KEEP WITH THIS REPAIR** | Port R1-validate to it exactly as for `ls`: snapshot listing after confirmation, drop the prepared snapshot when everything is reread |
| 3b | Restore tree preparation (`restore_wait_reader.go`, `restore_wait_trees.go`, `restore_snapshot_wait.go:29-64,141-148`, `restorer.go:28,653,742`, `wait_index_load.go:19-20,50-52`, their tests) | **DROP** | Switched off by the team itself; unreachable code and a test-only API |
| 4 | Prune and index (`index/repack_routes.go`, `repack_fallback.go`, `repack_ordinals.go`, `associated_count_validate.go`, `prune_pack_run.go`, hunks `prune.go`, `index/master_index.go`) | **KEEP AS IS** | Stock's decisions, witnessed. Tidy: delete the unreachable stock loop `master_index.go:742-773` and the `collect` parameter; gofmt `repack_routes.go` |
| 5 | `internal/data/tree_metadata.go` (`restorer.go:817`) | **KEEP WITH THIS REPAIR** | Use the R7 rule at `:124`: skip `strconv.Unquote` only for a name without backslash, quote or control byte (`node_name_plain.go` of the R7 patch). State in a comment that the second pass must not read `Node.Content` |

Order of work this implies: drop 1, 2 and 3b first; then R4 applies again (`repository.go` is back to the base)
and the R1 conflict shrinks to restore's wrapper, which is ported like `ls`.

## 6. NEEDS-RUN on the Linux box

Copy the witness files as in section 2. Commands run from the root of the S020 tree unless stated.

1. **Finding 2, the source walk.** Expected FAIL on S020, PASS once the walk stats before it opens.
   ```
   go test -run 'TestWitnessS020BackupWait' -count=1 -p 2 -v ./cmd/restic
   ```
   And with real binaries, a device node included (harmless: major 1 minor 3 is `/dev/null`):
   ```
   mkdir -p /tmp/s020src && mkfifo /tmp/s020src/a.fifo && mknod /tmp/s020src/b.null c 1 3 && head -c 700K /dev/zero > /tmp/s020src/z.bin
   export RESTIC_PASSWORD=x; ./restic-s020 init -r /tmp/s020repo
   strace -f -e trace=openat -o /tmp/s020.strace ./restic-s020 -r /tmp/s020repo backup /tmp/s020src
   grep -E 'a\.fifo|b\.null' /tmp/s020.strace     # stock: no line. S020: one openat for each
   ```
   Do not run this against a real `/dev/watchdog`.
2. **Finding 3, the real probes.** Expected: the first five sub-tests PASS, `dirty-shared-mapping` FAIL.
   ```
   go test -run TestWitnessS020CheckWaitPackRealProbes -count=1 -p 2 -v ./internal/repository
   ```
   The network mount, as root (expected FAIL with default mount options):
   ```
   apt-get install -y nfs-kernel-server
   mkdir -p /srv/witness/r1 /mnt/witness && echo '/srv/witness 127.0.0.1(rw,no_root_squash,sync,no_subtree_check)' >> /etc/exports
   exportfs -ra && mount -t nfs 127.0.0.1:/srv/witness /mnt/witness
   RESTIC_WITNESS_NFS_CLIENT_DIR=/mnt/witness/r1 RESTIC_WITNESS_NFS_SERVER_DIR=/srv/witness/r1 \
     go test -run 'TestWitnessS020CheckWaitPackRealProbes/network-mount' -count=1 -v ./internal/repository
   ```
3. **The tree's own Linux-only tests** (skipped or absent on Windows):
   ```
   go test -run 'TestCheckWaitIdentity|TestCheckWaitPackSameSizeCorruptionRequiresOrdinaryRead|TestCheckWaitPackFailedConfirmationJoinsBeforeReturning|TestBackupCompressionSource' -count=1 -v ./internal/repository ./cmd/restic
   ```
4. **Race detector over the new concurrent code** (no cgo toolchain here):
   ```
   go test -race -count=1 -run 'CheckWait|PreparedRange|CompressionPreparation|BackupCompression|BackupPreparation|RestoreSnapshotWait|RestoreWait|RepackRoutes|TestWitnessS020' ./internal/repository ./internal/repository/index ./internal/restorer ./internal/data ./cmd/restic
   ```
5. **R1 on the merged tree, after the port** (expected PASS; today FAIL):
   ```
   go test -run 'TestWitnessLockWaitStale|TestWitnessLock' -count=1 -v ./cmd/restic
   RESTIC_R1_SCHEDULES=3000 go test -run TestLockWaitSchedules -v ./cmd/restic
   ```
   The schedule test of the repair models backup, restore, prune, forget and check. If mechanisms 1 or 2 are kept,
   add `check --read-data` and a backup with an active shelf to its command set first.
6. **All portable witnesses on linux/amd64 and as 32-bit** (here: Windows amd64 and windows/386):
   ```
   go test -run TestWitnessS020 -count=1 -p 2 ./internal/repository/index ./internal/repository ./internal/data
   GOARCH=386 go test -run TestWitnessS020 -count=1 -p 2 ./internal/repository ./internal/data
   ```
   `GOARCH=386 go test ./internal/repository/index` does not build in S019-merge or S020:
   `pack_membership_test.go:81: constant 4294967296 overflows uint` (an S019 test file; REPAIRS.md experiment 3).
7. **Prune with real binaries on the big-index corpus, stock against candidate.** Two copies of one repository:
   ```
   cp -a /path/big-index-repo /tmp/pa && cp -a /path/big-index-repo /tmp/pb
   for o in "--max-unused 0" "--max-unused 5%" "--max-unused unlimited" "--max-repack-size 50M" "--repack-cacheable-only" "--repack-smaller-than 4M" "--repack-uncompressed"; do
     ./restic-stock -r /tmp/pa prune $o --dry-run --json > /tmp/a.json; ./restic-s020 -r /tmp/pb prune $o --dry-run --json > /tmp/b.json
     diff <(jq -S 'del(.message_type)' /tmp/a.json) <(jq -S 'del(.message_type)' /tmp/b.json) && echo "same plan statistics: $o"
   done
   ./restic-stock -r /tmp/pa prune --max-unused 0 && ./restic-s020 -r /tmp/pb prune --max-unused 0
   ./restic-stock -r /tmp/pa check --read-data && ./restic-stock -r /tmp/pb check --read-data
   diff <(./restic-stock -r /tmp/pa stats --mode raw-data --json) <(./restic-stock -r /tmp/pb stats --mode raw-data --json)
   ```
   (`--repack-small` is a deprecated flag without effect in this version; `--repack-smaller-than` replaced it.)
   Where blobs have duplicates, which copy is kept depends on the order index files were loaded in, in stock as
   well: a difference in the statistics on such a repository is settled by running stock twice.
8. **What dropping costs.** Mechanisms 1 and 2 each have one switch. Build two variants and run the four
   workloads against S020: `startBackupCompressionWaitReady` returning `nil` (`backup_compression_wait.go:20`), and
   `PrepareWaitPackRead` returning `nil` (`check_wait_pack.go:50`).
9. **Kill tests** (REPAIRS.md experiment 4) on the candidate that is kept: kill -9 during a prune that takes the
   route path (more than 8,192 blobs in the packs to repack), then stock `check --read-data`.

## 7. Should not ship (S020 delta)

- **Unreachable code.** The restore tree preparation (finding 15): two files, about 250 lines with their hooks,
  plus the tests that are its only callers. The stock loop left in `MasterIndex.listPacks`
  (`index/master_index.go:742-773`): `ListPacks` no longer calls it and `collect` is always true.
- **Two more debug switches** (R10 class): the mechanism is off while the debug log is on at
  `internal/repository/check_wait_pack.go:54` and `internal/repository/restore_wait_reader.go:31`.
  `cmd/restic/cmd_backup.go:544` is the S019 switch the R1 repair removes; S020 edited the lines around it.
- **Statistics kept for the league's evidence, on production paths.** `CheckWaitPackStats`
  (`check_wait_pack.go:42-48`: 14 fields, four of them wall-clock times, `time.Now()` at admission, completion,
  settlement and offer); `CompressionPreparationStats` (`compression_preparation.go:20-28`: 19 fields) with
  `noteCompressionPreparationNew` taking a mutex for every new data blob of a backup only to count
  (`:223-232`); `RestoreWaitTrees.Bytes / Nodes / Consumed`. The exported `Stats()` methods have test callers only.
- **An exported hook that replaces backend reads:** `retry.PreparedRangeKey` and `retry.PreparedRangeLoader`
  (`internal/backend/retry/prepared_range.go`).
- **Not gofmt-clean:** 9 of the 34 scope files (`cmd/restic/backup_compression_source_linux.go`,
  `backup_compression_source_other.go`, `check_wait_pack.go`, `cmd_check.go`,
  `internal/repository/check_wait_identity_linux.go`, `check_wait_watch_linux.go`, `checker.go`,
  `checker_capacity.go`, `index/repack_routes.go`) and 9 of the 29 added test files.
- **A test of the tree that fails on Windows:** `TestCheckWaitPackCacheForgetFailureKeepsRetry`
  (`internal/repository/check_wait_cache_failure_test.go:30`, "fixture did not fail cache cleanup": the fixture
  relies on POSIX path errors). The other 75 new tests run here pass; 2 are skipped as Linux-only.
- **League names in files and tests:** `_b26_` in four test file names and `B26` in test names.
- **Constants set on the judge's shape.** Check: 2 MiB pack (`check_wait_pack.go:16`). Shelf: 1 MiB chunk, 8 MiB
  read, 2 MiB per file, 256 KiB output, 8 entries (`compression_preparation.go:14-18`); walk: 2,048 entries, 128
  names per directory, depth 16, files from 512 KiB (`backup_compression_source_linux.go:21-37`). Routes: 65,536
  slots, 131,072 packs (`index/repack_routes.go:10-11`); bitmap up to 65,536 packs and at least 4 entries per
  pack (`index/repack_ordinals.go:13-16`). Restore: 128 KiB tree, 512 KiB shelf (unreachable). The index
  constants are bounds with a correct fallback; the others decide whether a mechanism does anything at all.
- **Still in the tree from S019:** `internal/repository/repository.go.orig` (R10).

## 8. Limits of this review

- Linux-only code was read, not run: the two probes of the check mechanism and the source walk of backup. The
  HIGH verdicts of findings 2 and 3 rest on reading plus, for finding 3, a model. Section 6 items 1 and 2 decide.
- No race detector (no cgo toolchain on this machine).
- The prune witnesses compare S020 with stock's algorithm inside one process on the same index. Two real
  binaries on one large repository were not compared (section 6 item 7).
- macOS, BSD and Windows with VSS: the three lock-wait mechanisms are compiled out or unchanged there
  (`*_other.go` return nil); the builds pass, nothing was executed on those systems.
