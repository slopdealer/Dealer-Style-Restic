# REVIEW-storage: soundness of the storage core of `S019-merge` against stock restic (ba802d42)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Reviewer scope: `internal/repository`, `internal/repository/index`, `internal/repository/pack`,
`internal/repository/crypto`, `internal/checker`, `internal/restic` (57 files in FILES.md, area `storage`).
Method: every changed file diffed against stock, every new file read; six witness test files written and run
on Windows (go1.27.0) in a scratch copy of the candidate tree; one of them also run on a scratch copy of stock
as the control. All paths below are relative to `trees/S019-merge/` unless they start with `cmd/` (same tree).
Date: 2026-10-06.

## Summary

34 mechanisms judged.

| Verdict | Count |
|---|---|
| UNSOUND, BLOCKER | 1 (reproduced at API level; end-to-end run still needed) |
| UNSOUND, HIGH | 1 (reproduced; stock control passes) |
| UNSOUND, MEDIUM | 2 (both reproduced) |
| SUSPECT, MEDIUM | 1 (follows from the formula; size of the effect not measured) |
| NEEDS-RUN | 1 |
| SOUND-UNDER-CONTRACT | 8 |
| SOUND | 20 |
| LOW (hygiene, must not ship as is) | 1 group outside the 34, listed at the end |

What was NOT found: no weakened hash, MAC or verification on the save path, in `check` or in
`check --read-data`; no change of repository format (packs, index JSON bytes, lock files, pack IDs, chunk
boundaries); no use of `unsafe`, `reflect` or new atomics in the area; the five changed stock test files only
gained tests. The four reproduced defects are in what happens AROUND the format: when the index is read
relative to the lock, where the pack handler runs relative to the backend connection, parameter validation of
the hand-written scrypt, and a compressibility guess.

Reproduced failures (witness files in `review/witness-storage/`, run with
`go test -run <Name> -count=1 -p 2 ./internal/<package>` after copying the file next to the package):

| Witness | Result on the candidate |
|---|---|
| `lock_dwell_witness_test.go` `TestWitnessLockDwellPreloadedIndexIsStale` | FAIL: lock confirmed, preloaded index still lists a blob whose pack a concurrent prune deleted. `TestWitnessLockDwellStockOrder` (same schedule, stock order) PASS |
| `repair_packs_conn1_witness_test.go` `TestWitnessRepairPacksOneConnection` | FAIL: deadlock (30 s timeout). The same file on a scratch copy of stock: PASS in 0.19 s. Two connections: PASS on both |
| `kdf_witness_test.go` `TestWitnessKDFInvalidN6Panics` | test binary dies: `panic: slice bounds out of range [:512] with capacity 256` at `parallel_scrypt.go:74`. `TestWitnessKDFInvalidN12NoError` FAIL (key derived, no error). `TestWitnessParallelScryptDifferential` PASS |
| `compression_witness_test.go` | FAIL: blob stored raw at 1,048,608 bytes; stock's encoder gives 65,661 bytes (16.0x) |
| `index_witness_test.go` (3 tests) | PASS |
| `chunker_streampack_witness_test.go` (2 tests) | PASS |

## Findings, most severe first

| # | Mechanism | Files:lines | Verdict, severity | Contract or failure scenario | Witness or deciding experiment |
|---|---|---|---|---|---|
| 1 | **Lock-dwell wait work: the index (and the snapshot list, and check's pack sizes) is read between writing the lock file and the competing-lock check, and kept when the check passes** | `internal/repository/lock_file.go:108-156` (work starts at 136-142, check at 146); `internal/repository/lock.go:55-131`; users: `cmd/restic/cmd_backup.go:542-558,598-600`, `cmd/restic/backup_index_load.go:29-45`, `cmd/restic/wait_index_load.go:34-50,169-195`, `cmd/restic/cmd_prune.go:180-221`, `cmd_forget.go:196-203`, `cmd_restore.go:151`, `cmd_ls.go:380`, `cmd_check.go:247` | **UNSOUND, BLOCKER** (silent broken snapshot; narrow timing window; stock is immune) | The second lock scan only proves that no conflicting lock exists AT that moment. Stock reads the repository after it; the candidate reads during the 200 ms before it. Scenario: backup B does its first lock scan (no locks), then its lock-file upload takes more than 200 ms (WAN SFTP, S3, B2, rclone). A prune P next to the storage starts in that gap: neither of P's scans sees B. P finishes (deletes packs, rewrites the index, unlocks) while B is in its 200 ms wait, after B's wait work has read the old index. B's check finds no lock, B is confirmed and deduplicates against blobs P deleted: `backup` reports success, the snapshot references missing packs. Stock in the same schedule loads the index after the check and re-uploads. Needs: B's lock-save latency above 200 ms, and P's whole run shorter than that latency plus 200 ms (a small repository, a fast local prune). Rare, but it is exactly the case the lock exists for. Other pairs fail loudly, not silently: restore/ls get "not found"; prune and check with a stale index abort or report false errors (prune lists snapshots again after confirmation, `cmd/restic/cmd_prune.go:305-320`, and aborts on missing blobs). The parent snapshot chosen from the prepared listing (`backup_index_load.go:49-54`) has the same exposure | Reproduced at API level with the real lock code, real `LoadIndex`, 600 ms injected lock-save latency: `lock_dwell_witness_test.go`. Also reproduced at command level by the read-side reviewer (see Addendum). Not yet run with two real binaries over a slow link: NEEDS-RUN 1 |
| 2 | **One request per pack range, read in windows: the blob handler is called inside the backend `Load` callback** (the reader holds a backend connection while the handler runs) | `internal/repository/stream_pack_coalesced.go:71-204` (Load at 90, handler at 133); every caller of `loadBlobsFromPack`, `internal/repository/repository.go:1513-1571`: restore, `repack.go:123-178`, `repair_pack.go:100-111` | **UNSOUND, HIGH** (deadlock, reproduced; limited to one backend connection) plus SUSPECT MEDIUM for remote backends | (a) Reproduced: `restic repair packs` with `-o <backend>.connections=1`. The handler saves blobs synchronously; the first full pack goes to the single uploader, which waits for the connection that the reader holds; the second full pack waits for the uploader; nothing moves. Witness: one 13 MiB pack re-uploaded with 4 MiB packs. With default pack sizes the same state is reached once three or more packs are repaired in one run (leftovers fill both packers). Stock reads the range into memory, releases the connection, then calls the handler. (b) Not reproduced: on REST/S3/SFTP the HTTP response stays open while the handler waits (prune repack waits for the 8 MiB save budget, `repack.go:158`; restore waits for the disk). A proxy `send_timeout` or an S3 idle timeout then breaks the response; `retry` replays the request from the start of the span (up to 32 MiB, `stream_pack_coalesced.go:17`) and the committed prefix is downloaded again (`:68-70`, `:95`). (c) During prune repack the readers hold connections for the whole range, so uploads get what is left (one connection on most backends). Integrity is not affected: every blob still passes MAC and hash in `packBlobIterator.Next` | (a) `repair_packs_conn1_witness_test.go`, stock control run. (b), (c): NEEDS-RUN 6 |
| 3 | **Parallel scrypt (hand-written lanes, every fourth ROMix state stored)** | `internal/repository/crypto/parallel_scrypt.go:16-52,54-88`; `kdf.go:73` | **UNSOUND, MEDIUM** (crash and behaviour change on a malformed key file; valid parameters are exact) | Used whenever p >= 2 and GOMAXPROCS >= 2, which is the usual restic key (Calibrate raises p). `x/crypto/scrypt.Key` rejects N that is not a power of two; `simple-scrypt` `Params.Check` in `KDF` only rejects odd N. The lanes do no validation: N=6, p=2 indexes past the stride table (`:43`, `:73-74`) and panics in a goroutine nobody can recover; N=12 derives a key and returns no error, where stock stops with `scrypt: N must be > 1 and a power of 2`. A damaged or hostile `keys/` file (unauthenticated JSON) crashes every command that searches keys. For valid parameters the output equals x/crypto for 60 random (N, r, p) plus the candidate's own table. Memory equals stock's (4 workers x a quarter table). Little-endian is written explicitly, so big-endian is fine | `kdf_witness_test.go` (all three tests) |
| 4 | **CompressionAuto sampling: a data blob of 512 KiB or more is stored uncompressed when 16 windows of 2 KiB do not shrink by 1%** | `internal/repository/repository.go:34-54,427-448,482-503` | **UNSOUND, MEDIUM** (behaviour change: larger repository; format valid, stock reads it) | Ruling 2 allows storing INCOMPRESSIBLE blobs raw. The sample only sees redundancy inside 2 KiB; redundancy at longer range (zstd's window is 512 KiB) is missed. Scenario: default `--compression auto`, a chunk made of one incompressible 64 KiB block sixteen times: the shape of an uncompressed tar, zip-store or disk image holding copies of an already compressed file smaller than the 512 KiB minimum chunk. Stock stores 65,661 bytes, the candidate 1,048,608. The second guard (`:438-442`, keep raw if compressed+4 >= raw) is sound. Stock's test helper turns Auto into Fastest (`testing.go`, same in stock), so no stock test runs this path | `compression_witness_test.go` |
| 5 | **Index-load worker cap** `min(connections+GOMAXPROCS, max(2, GOMAXPROCS))` | `internal/repository/index/index_parallel.go:18-21` | **SUSPECT, MEDIUM** (resource regression off the judge's shape) | Stock uses connections+GOMAXPROCS workers because index loading is I/O bound without a cache. On a 1 or 2 core machine with S3/REST (5 connections) stock downloads 6 or 7 index files at once, the candidate 2. First run, `--no-cache`, and every run after a prune pay it. The judge has 4 CPUs and a fast link | NEEDS-RUN 8 |
| 6 | **Checker: extra `check --read-data` worker with bounded buffers; refused packs are handed to the ordinary workers** | `internal/repository/checker.go:301-456,467-474`; `checker_capacity.go:15-76` | **NEEDS-RUN** (reads sound) | By reading: every pack goes to exactly one of two queues (`:432-447`); a refusal (`errCheckCapacity`, zstd window or size limit) is never a verdict, the pack is checked again in full by an ordinary worker with the earlier error attached (`:402-412`, `:335-337`); the extra worker's producer finishes before the ordinary queue is closed (`:451-455`). What reading cannot settle: races between the three goroutine groups under cancellation, and that a corrupted pack is reported on each of the three routes | NEEDS-RUN 2 and 5 |
| 7 | **Prune index rewrite keeps "unchanged" index files without reading them again** (decision from pack IDs and blob counts noted at load or save time) | `internal/repository/index/index_rewrite_keep.go:25-39,57-100`; `master_index.go:341,437-446,683`; `index_rewrite_saved.go` | SOUND-UNDER-CONTRACT | Equivalent to stock's loop when (1) the note for a file is the content of that file: true, the file name is the hash of its content and `LoadRaw` checks it; (2) nothing else changed the index files since they were loaded: true under a confirmed exclusive lock, NOT guaranteed while finding 1 stands, because the index is loaded before confirmation. A pack ID listed twice, or any file without a note, sends everything through stock's loop (`:63-66,90`). Difference from stock: a kept file that has become unreadable is no longer noticed by prune (stock aborts before deleting packs). Cost: a copy of every pack ID per index file is kept for the life of every command (`:27`), plus a transient map of all pack IDs in prune (`:61`): a few percent on top of the index | Read. Candidate tests `rewrite_identity_probe_test.go`, `rewrite_interruption_test.go` pass. NEEDS-RUN 4 (kill during rewrite) |
| 8 | **Streamed index save, verified against a SECOND encode instead of the saved buffer** | `internal/repository/repository.go:651-711,811-832`; `index_verify_direct.go:13-33`; `index/index.go:368-496` | SOUND-UNDER-CONTRACT | Contract: `Index.Encode` is deterministic and the index is not modified between the two calls (it is final, both calls take the read lock), and `json.Encoder.Encode` issues exactly one `Write` per value ending in a newline (`jsonPackWriter`, `index.go:435-460`; true for the Go toolchains in use, but an assumption about the standard library: a split write would put commas inside a pack object and the self-comparison would not notice). Under the contract the check is at least as strong as stock's: decrypt, decompress, compare every byte, read the decoder to EOF. Index bytes equal stock's | `index_witness_test.go` `TestWitnessEncodeIdentity` PASS (200 random indexes, byte identical, second encode identical) |
| 9 | **Known-blob completion lane and index-only lookup** | `internal/repository/known_completion_lane.go:28-80`; `repository.go:918-937,983-991`; `index/master_index.go:76-87` | SOUND-UNDER-CONTRACT | A blob is reported known only when a loaded or already uploaded index entry has it (pending saves excluded), the same guarantee as stock's `AddPending` returning false. Contract for the caller (archiver, other area): callbacks must not block and `SaveKnownBlobAsync` must not be called after the uploader callback returns (`submit` after `finish` is a send on a closed channel, `:74,78`). The index must be the one valid under the lock: see finding 1 | Read. `go test -race`: NEEDS-RUN 2 |
| 10 | **Reused and pooled range buffers** (one buffer per stream, two retained across streams, cleared before reuse) | `internal/repository/repository.go:1450-1480,1616-1671,1678-1711` | SOUND-UNDER-CONTRACT | Contract: a handler must not keep `buf` after it returns. In stock an uncompressed blob's slice stayed valid for ever; here it is overwritten by the next window and zeroed when pooled. All current handlers obey: restore writes synchronously (`internal/restorer/filerestorer.go:430-469`), repack copies before the asynchronous save (`repack.go:165`), repair packs saves synchronously | Read |
| 11 | **Asynchronous repack saves with an 8 MiB copy budget** | `internal/repository/repack.go:18,110-204` | SOUND-UNDER-CONTRACT | Contract: `SaveBlobAsync` always calls its callback (it does, including on cancellation), so `pending.Wait()` (`:179`) cannot hang; blobs above the budget take the synchronous path (`:148`), so `Acquire` never asks for more than the budget. A failed save fails the worker, prune stops before deleting packs, and the stock "was not repacked" check remains. Durability order (packs and index flushed before removal) is unchanged | Read. Candidate tests `TestRepack*` pass |
| 12 | **Restore hold budget, additional load slot, restore decoder** | `internal/repository/restore_window_hold.go:23-91`; `restore_decoder.go:16-52`; `internal/restic/restore_hold_budget.go`; `repository.go:1513-1524,1696-1711` | SOUND-UNDER-CONTRACT | Accounting only; decoding and verification are the ordinary path. Contract on the restorer's budget (other area): `Acquire` must admit a request larger than the whole budget when nothing is held, otherwise the additional worker waits for ever inside `Load`. An explicit `-o local.connections=N` disables the extra slot | Read. NEEDS-RUN 2 |
| 13 | **Prepared snapshot** (one quiet load during the lock wait, replayed into the ordinary load) | `internal/repository/prepared_snapshot.go:13-75` | SOUND-UNDER-CONTRACT | Bytes are hash-checked here (`:50`) and again by the ordinary `LoadUnpacked`. Contract: the caller joins the load before unlocking. A snapshot forgotten after the quiet load is still served from memory (same root as finding 1) | Read. Candidate tests `prepared_snapshot*_test.go` exist |
| 14 | **Checker pack sizes prepared from the index before use** | `internal/repository/checker_pack_sizes.go:13-48`; `checker.go:198-205,264-272`; `internal/checker/checker.go:304-333` | SOUND-UNDER-CONTRACT | Same numbers as `pack.Size` on the same index. Contract: that index is the confirmed one (finding 1: a stale one gives false "not referenced in any index" errors, loud) | Read |
| 15 | **Hash-on-write pack ID** (SHA-256 collected while writing the temporary pack, when the backend has no hasher) and closing of temporary files | `internal/repository/packer_manager.go:24-30,58-152,241-264,267-338`; `repository.go:959-963` | SOUND | The hash writer sees every byte that reaches the temp file: blobs, merged packers (`io.Copy` into the `bufio.Writer`; `hashing.Writer` has no `ReadFrom`, so nothing bypasses it), header. The ID is the hash of the bytes written; if the temp disk returns other bytes on upload, `check --read-data` reports an unexpected pack ID, where stock would have named the pack after the damaged bytes. Stock's read-back pass remains for backends with their own hasher. Temp files are now closed on every error path (stock leaked them) | Read. Candidate test `TestPackerWriteThroughHashIncludesMergedPayloadAndFinalHeader` passes |
| 16 | **Save verification, raw blobs of 256 KiB or more: authenticate, then hash a streamed decryption** | `internal/repository/save_verify_stream.go:12-35`; `crypto/open_reader.go:17-40`; `repository.go:505-511` | SOUND | `OpenReader` performs the same checks as `Open` in the same order, including the Poly1305 MAC over the whole ciphertext before any plaintext is produced; then SHA-256 of AES-CTR output against the blob ID. Same bytes, same checks as stock | Read. Candidate tests `TestSaveVerify*`, `open_reader_test.go` pass |
| 17 | **Save verification, compressed blobs of 2 MiB or more: hash the streamed decompression** | `internal/repository/save_verify_compressed.go:12-51`; `save_verify_hash_writer.go`; `repository.go:518-523` | SOUND | MAC as stock, then a streaming decoder into SHA-256. Any decoder error or oversize declines and stock's `DecodeAll` path decides; a mismatch is an error. No partial hash can pass | Read |
| 18 | **`verifyUnpacked` for index files: compare the decompressed stream with the expected bytes** | `internal/repository/repository.go:762-771,787-805,870-897` | SOUND | Every byte compared, length compared, decoder read to EOF | Read |
| 19 | **Lock files written as a raw-block zstd frame** | `internal/repository/repository.go:550-609,716-720` | SOUND | Valid single-segment frame for every size class; stock's decoder reads it; only lock files, only repository version 2 | Read. Candidate tests `TestAppendRawZstdFrame`, `TestCompressLockUnpackedUsesRawZstdFrame` pass |
| 20 | **Index JSON written one pack at a time** | `internal/repository/index/index.go:368-433` | SOUND (with the `json.Encoder` assumption of finding 8) | Bytes identical to stock, including mixed packs, duplicate entries and the empty index | `index_witness_test.go` `TestWitnessEncodeIdentity` PASS |
| 21 | **Index decoders: canonical two-pass, canonical streaming, per-pack `json.Decoder`** | `internal/repository/index/index_decode_eager.go`; `index_decode_canonical_stream.go`; `index_decode_reader.go`; `index_hex.go`; `internal/repository/index_load_ordinary_stream.go:28-50`; `index_load_stream.go:21-52`; `index/index_parallel.go:26-36,44-67` | SOUND | Each fast decoder accepts only restic's own serialisation and otherwise hands the complete plaintext to `DecodeIndex`. Old or foreign writers (key order, whitespace, `supersedes`, upper-case hex) take stock's decoder. No partial index escapes; trailing bytes and decoder errors after the document decline | `index_witness_test.go` `TestWitnessDecodeDifferential` PASS: 1029 documents, every acceptance equal to `DecodeIndex`, `decodeIndexEager` agrees on errors |
| 22 | **Index merge with combined lookup and insert, adoption of the first loaded index, slot reservation, `maphash.Bytes`** | `internal/repository/index/index.go:150-153,602-609,628-649`; `index_merge.go`; `index_adopt.go:7-59`; `index_reserve_slots.go`; `indexmap.go:179` | SOUND | Same deduplication key as stock (ID, type, pack, offset, length, uncompressed length). Adoption only when the destination is empty and the index has no exact duplicate inside | Read. Candidate tests `index_merge*_test.go`, `merge_growth_boundary_test.go` pass |
| 23 | **Prune planning addresses the used-blob set by index position** | `internal/repository/index/associated_position.go:37-184`; `associated_count.go`; `associated_data.go:153-156`; `internal/repository/prune_position.go:19-36`; `prune.go:153-160,195-200,242-259,302-338` | SOUND | The slot is the first position of the ID in the merged index, exactly what `blobIndex` returns; other indexes fall back to the lookup. A wrong slot here would let prune treat a used blob as unused, so this was tested | `index_witness_test.go` `TestWitnessSlotEqualsBlobIndex` PASS (duplicates, late merges, non-final indexes, the counting pass both ways) |
| 24 | **Pack header sizes computed in the background during used-blob search** | `internal/repository/prune_header_overlap.go:26-53`; `prune.go:122-131,226-231`; `index/repack_values.go:47-63` | SOUND | Same arithmetic as `pack.Size(..., true)`; joined before use and on every return | Read |
| 25 | **Bounded single traversal for the repack pack list** | `internal/repository/index/list_packs_bounded.go:13-57`; `master_index.go:702-733` | SOUND | Publishes nothing unless complete; otherwise stock's 16-pass path | Read. Candidate test `list_packs_bounded_test.go` passes |
| 26 | **Checker `NextHash`: verify without keeping decompressed data** | `internal/repository/repository.go:1917-1958`; `checker.go:571` | SOUND | MAC, then SHA-256 of the streamed decompression (or of the plaintext) against the blob ID; identical error text to `Next` | Read |
| 27 | **Checker ordinary reader decrypts in place inside the buffered reader; stream buffer 4 MiB to 1 MiB** | `internal/repository/checker_cipher_reader.go:13-53`; `checker.go:23-27,553-564,690-707` | SOUND | The pack hash reader has hashed the ciphertext before it is decrypted in place; the plaintext is consumed before the next read | Read |
| 28 | **Checker header from the captured tail, large-header variant, membership test without result objects** | `internal/repository/pack/owned_tail.go:14-63`; `pack/owned_tail_entries.go`; `checker_large_header.go:17-35`; `index/pack_membership.go`; `checker.go:644-664` | SOUND | Same checks, order and messages as `pack.List` on the same bytes (stock also passed the tail length as file size); header MAC verified; called only after the whole-pack hash matched | Read. Candidate tests `owned_tail*_test.go`, `checker_*contract_test.go` pass |
| 29 | **Checker index load in one grouped pass** | `internal/repository/checker.go:128-150`; `index/index_check_groups.go`; `index/pack_hash_owned.go` | SOUND | Same groups, same hash bytes as `PackBlobsHash`; cancellation checked after the loop | Read |
| 30 | **Chunker: re-implemented Rabin scan, two independent chains per 512 bytes, reset by copying a snapshot** | `internal/repository/chunker.go:8-20,51-53`; `chunker_exact.go:44-121`; `chunker_segments.go:14-97` | SOUND | Split points identical to `restic/chunker` v0.5.0 `BaseChunker`: the fingerprint depends only on the last 64 bytes, so a chain can start from them. Polynomial degrees outside 8..53 keep the library chunker | `chunker_streampack_witness_test.go` `TestWitnessChunkerDifferential` PASS: 3 polynomials, 8 streams, forced MaxSize cuts, Reset in mid-chunk, about 40,000 calls with buffer sizes around 64/256/512/1024 |
| 31 | **Windowed `streamPack` with a 4 MiB part cap** (the frozen function) and the production entry `streamPackWithPolicy` | `internal/repository/repository.go:1608-1671`; `stream_pack_coalesced.go:21-66` | SOUND as to results (see finding 2 for where the handler runs) | Stock's `TestStreamPack` calls `streamPack`, which production no longer calls. Run against the production entry, stock's table passes, including the short-read retry and the three error cases | `chunker_streampack_witness_test.go` `TestWitnessStockStreamPackTableOnProductionPath` PASS, versions 1 and 2 |
| 32 | **Small-restore bounded decoder with fallback** | `internal/repository/repository.go:1525-1600,1741-1752,1886-1898`; `internal/restic/restore_load.go` | SOUND | A size-limited decoder first; on any error the ordinary decoder decides; hash check unchanged | Read. NEEDS-RUN 2 |
| 33 | **Forced GC before index load** | `internal/repository/repository.go:1094-1096` | SOUND | A few milliseconds per command to lower the peak the judge measures; harmless | Read |
| 34 | **Lock context created before acquisition** (so wait work is tied to the lock) | `internal/repository/lock.go:62-131` | SOUND | Same lifetime as stock's context after acquisition; cancelled on every failed attempt. The 200 ms wait is unchanged (`lock_file.go:144`) | Read. Candidate tests `TestLock*`, `TestNewLock*` pass |

Platforms and scale, for the whole area: the area is portable Go. `GOOS=linux GOARCH=386 go build` of the three
package trees succeeds; `go vet` is clean. Endianness is written explicitly wherever bytes are assembled
(`parallel_scrypt.go:62,86`, `pack_hash_owned.go:20-23`, `repository.go:604-610`). No 64-bit atomics were
added. Pack offsets remain 32 bit as in stock. At tens of millions of blobs the additions are the per-file
pack-ID notes and prune's pack-ID map (finding 7); nothing is quadratic.

## NEEDS-RUN experiments (Linux box; `CAND` and `STOCK` are the two binaries, `$T` the candidate tree)

1. **End-to-end lock race (decides finding 1).** Repository on local disk; prune uses the local path, backup
   uses `sftp:localhost:` with loopback delay so its lock save exceeds 200 ms:
   ```
   tc qdisc add dev lo root netem delay 120ms
   for i in $(seq 500); do
     echo $i > /src/changed.txt
     CAND -r sftp:localhost:/repo backup /src &            # slow lock save
     sleep 0.$((RANDOM % 9))
     CAND -r /repo forget --keep-last 1 --prune            # fast, local
     wait
     STOCK -r /repo check --read-data || echo "BROKEN round $i"
   done
   tc qdisc del dev lo root
   ```
   Any `BROKEN` after a backup that exited 0 confirms the data loss. Repeat with `STOCK` as the backup binary:
   expected zero.
2. **Data races.** `cd $T && go test -race -count=3 ./internal/repository/... ./internal/checker/... ./internal/restic/... ./internal/backend/sema/...`
3. **32 bit.** `cd $T && GOARCH=386 go test -count=1 ./internal/repository/... ./internal/checker/...`
4. **Kill at any point, then stock check.** For each of `backup`, `forget --keep-last 1 --prune`,
   `prune --max-unused 0`, `repair index`: 200 rounds of
   `CAND ... & p=$!; sleep 0.$((RANDOM % 99)); kill -9 $p; STOCK -r /repo unlock; STOCK -r /repo check --read-data`
   and then a full `STOCK restore latest` compared with the source.
5. **Corruption detection on each checker route.** Flip one byte in the middle of (a) a pack whose blobs are all
   at most 1 MiB, (b) a pack with a blob between 1 and 2 MiB, (c) a pack with a blob above 2 MiB, (d) the header
   of a pack with more than 2,000 blobs (header above 64 KiB). `CAND -r /repo check --read-data` must name each
   pack and exit non-zero; 50 rounds each, also with `GOMAXPROCS=1` and with `-o local.connections=1`.
6. **Slow consumer on a remote backend (finding 2 b, c).** rest-server behind nginx with `send_timeout 5s`:
   `CAND -r rest:http://host/repo prune --max-unused 0 --limit-upload 200` and
   `CAND -r rest:... restore latest --target /slow` (target on a `dm-delay` device). Compare bytes downloaded,
   retries printed and wall time with `STOCK`. Then `CAND -r /repo -o local.connections=1 repair packs <id1> <id2> <id3>`
   with a 60 s timeout: a hang confirms the witness with the real binary.
7. **Cross-read matrix.** `CAND` writes repositories with `init --repository-version 1` and `2`, each with
   `--compression off|auto|max` and `--pack-size 4|128`; `STOCK check --read-data`, `STOCK restore`, `STOCK prune`
   on each. The reverse on stock-written repositories, including the fixtures under
   `internal/checker/testdata` and `internal/repository/testdata` (mixed packs, duplicate blobs, old index
   files) and a repository whose index was written by rustic.
8. **Index load with few cores (finding 5).** A repository with 500 or more index files behind 50 ms latency:
   `taskset -c 0 CAND -r rest:... --no-cache snapshots` and `taskset -c 0,1 ...` against `STOCK`.
9. **The wait-work path under the stock integration tests.** The stock `cmd/restic` tests set the lock wait to
   0, and wait work only starts when the wait is above 0 (`lock_file.go:140`), so they never run the code every
   real command runs. Run `cd $T && go test -count=1 ./cmd/restic` with `TestSetLockTimeout(t, 200*time.Millisecond)`
   in `testRunInit` and a thread-safe list-once backend.
10. **Scale.** A synthetic repository with 30 million blobs: peak memory and time of `prune --dry-run`, `check`
    and `backup` (unchanged) against `STOCK`.

## Changed stock test files

All five diffs are additions only (`git diff --numstat`: 49/0, 65/0, 128/0, 92/0, 320/0). No stock assertion was
removed, relaxed or skipped.

| File | What was added |
|---|---|
| `internal/repository/crypto/kdf_test.go` | `TestParallelScryptKeyMatchesXCrypto` (7 parameter sets against x/crypto), `TestKDFRejectsInvalidScryptParams` (N=1, r=0, p=0 only: it does not cover an even N that is not a power of two, finding 3) |
| `internal/repository/index/index_internal_test.go` | `TestEncodeMatchesLegacyPackListEncoding` (two packs, byte equality with stock's encoding, also through a one-byte writer), `TestEncodeReturnsShortWrite` |
| `internal/repository/lock_file_test.go` | `TestNewLockRejectsLockThatAppearsBeforeConfirmation`, `TestNewLockWaitWorkIsDiscardedBeforeUnlockOnConflict`. Both cover a lock that is still present at the check; neither covers a competitor that comes and goes inside the wait (finding 1) |
| `internal/repository/packer_manager_test.go` | `TestPackerWriteThroughHashIncludesMergedPayloadAndFinalHeader`, `TestPackerManagerClosesMergedFilesWhenQueueFails` |
| `internal/repository/repack_test.go` | helper uploaders and `TestRepackWaitsForAsyncSaveError`, `TestRepackCancelsPendingAsyncSave`, `TestRepackKeepsAsyncInputAfterStreamAdvances`, `TestRepackUsesSynchronousPathForOversizedBlob` |

Two coverage facts that matter more than the diffs: stock's `TestStreamPack`, `TestBlobVerification` and
`TestStreamPackFallback` exercise `streamPack` (`repository.go:1608`), which only tests call now; and the in-memory
test backend has its own hasher, so hash-on-write (`repository.go:961`) runs only on the local backend and in its
dedicated test.

## Things that should not ship as they are (LOW)

- `internal/repository/repository.go.orig`: 1,998 lines, a stale copy of the file from before a merge.
- `gofmt -l` lists four non-test files in the area: `internal/repository/index_load_ordinary_stream.go`,
  `index/index_decode_canonical_stream.go`, `index/index_rewrite_saved.go`, `packer_manager.go` (and 15 test files).
- `debug.IsEnabled()` selects stock's code instead of the new code in `index/index_decode_eager.go:18`,
  `index_load_ordinary_stream.go:29`, `prune.go:126` (and `cmd/restic/lock.go:19`, `cmd_backup.go:543`,
  `cmd_check.go:332`): a debug build does not run what a release build runs.
- Test-only survivors of the frozen API: `streamPack`, `streamPackWindowed`, `streamPackPart`
  (`repository.go:1608-1676`). Either production uses them or the stock tests should be pointed at
  `streamPackWithPolicy`.
- `internal/restic/restore_hold_budget.go:13-15`: the file is kept to type declarations because moving code
  changed where the key derivation lands in the binary ("3% slower or faster ... 6 ms per process"). That is
  tuning to one build on one machine.
- Judge-shaped constants to revisit with real users in mind: the index-load worker cap (finding 5), the forced
  GC before index load (finding 33), the CompressionAuto sample (finding 4).
- League names in shipped test files (`b06_`, `b17_`, `*_b23_test.go`).

Scratch material of this review: `review/scratch-storage/tree` (candidate copy with the witness files) and
`review/scratch-storage/stock-tree` (stock copy with `repair_packs_conn1_witness_test.go`); both can be deleted.

## Addendum (2026-10-06, after the hand-off): two items from the read-side review that land in this area

The read-side reviewer (`review/REVIEW-readside.md`) reported the following. I read the cited lines and the
witness logs; I did not re-run their witnesses.

- **Finding 1 is corroborated at command level.** `review/witness-readside/cmd_lock_wait_stale_witness_test.go`
  drives the real `backup`, `prune` and `restore` command functions (settle delay scaled to 1 s, same
  precondition: the first process's lock write outlasts the delay). Logs: candidate 3 of 3 FAIL
  (`cmd_lock_wait_stale_witness.candidate.log`), stock 3 of 3 PASS (`...stock.log`). Backup beside prune loses
  the backup silently; prune and restore fail where stock succeeds. Their write-up is section M1. The verdict
  here (UNSOUND, BLOCKER) stands, and it no longer rests on the API-level witness alone.
- **Finding 2 has two more consequences of the same design** (decoding inside the `Load` callback), both
  reproduced in `review/witness-readside/repository_stream_coalesced_witness_test.go` (their section X1, rated
  LOW there):
  - (d) A decode error that is not a transport error (for example `invalid blob length` from `readBlob`,
    `internal/repository/repository.go:1990-1993`) is returned from inside the `Load` consumer
    (`stream_pack_coalesced.go:145-147`), so the retry backend treats it as a failed download and requests the
    range again until its budget ends: 8 requests in the test's fast-retry mode against 1 in stock, 15 minutes
    in the binary. Afterwards the circuit breaker is open for that pack, so later reads of the same pack in the
    same process fail, where stock can still read it. My weighting: LOW to MEDIUM. It needs a damaged index
    entry, but then a restore stalls for the retry budget and loses access to the other blobs of that pack.
  - (e) Fail-open: if a backend's `Load` returns nil without calling the consumer, `streamPackResponse` returns
    nil with no blob delivered (`:152-157`, `:193-203`: nothing compares `committed` plus the final window with
    the request). Stock and the single-window path fail closed (every blob fails its MAC on a zero buffer).
    Prune still catches it ("blobs were not repacked"); restore has no such check when the loader returns nil.
    LOW: it needs a backend that breaks the `Load` contract.
  Blob MAC and hash checks are intact in both cases. The severity of finding 2 as a whole stays HIGH because of
  the reproduced deadlock (a).
