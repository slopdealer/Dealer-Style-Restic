# Changes

The patch series, explained for someone who knows restic's code. Upstream is restic 0.19.1-dev at `ba802d42`.

The history has three parts:

1. **Commit 1**: the upstream subset.
2. **Commit 2**: the league's tree after seasons S001 to S019 and two hand merges, as one commit (224 files added,
   51 upstream files changed). The league kept no per-change history that could be replayed. Section 1 below takes
   that commit apart by area. **Its tree must not be used**: it has the defect R1.
3. **The later commits**, one per change: 17 repairs; then the sound part of season S020, four ports, the
   release-check fixes and review follow-ups; then nine fixes that followed the last independent review, a change
   to restore's hand-overs, the version string, and one commit with a comment and a test. Section 3 lists them from
   the patch files. The official judge jobs measured the commit before the last one (RESULTS.md, section 2).

The same content as patches is in `league/series/`: `0000-league-tree-after-S019.patch` is the diff of commit 2
against commit 1, and `0001` onwards are the later commits in `git format-patch` form. They apply to upstream at
`ba802d42` in order.

File names below are those of the published tree. Tests sit beside the code as `*_test.go`; where a mechanism has
a test that compares it with a verbatim copy of upstream's function, that is said. "Contract" is what must stay
equal to upstream at that point. Repairs R1 to R11 and M22 and failures F1 to F5 are described in
[SOUNDNESS.md](SOUNDNESS.md); "finding 1" to "finding 11" and "fix 1" to "fix 9" are those of the last independent
review ([RED-TEAM.md](RED-TEAM.md)); what each change measured is in [RESULTS.md](RESULTS.md) and in the season
reviews under [`../reviews/seasons/`](../reviews/seasons/).

All measurements quoted are at 4 CPUs.

## 1. The league's tree, by area

How the tables were made: the league kept no per-file history. A file is listed under a mechanism where a season
review names it in that season's "files changed" list, where a commit message of the later history names it, or
where the reviews' scope list and the file's own name and header leave no doubt. Seasons are those the league's
inventory gives for the mechanism (section 2). A file may also carry changes of later seasons. Every file named
exists in the published tree; the complete list of added and changed files is the diff of commit 1 against the tree.

### 1.1 Start-up and key derivation

| mechanism | upstream | this tree | files | season |
|---|---|---|---|---|
| parallel scrypt | `scrypt.Key` from `x/crypto` on one core | the same function computed in parallel lanes; the result is bit-identical for valid parameters (1,278 parameter sets compared) | `internal/repository/crypto/parallel_scrypt.go`, `scrypt_params.go`, `kdf.go` | S004 |
| key listing beside the config check | key files are listed and tried after the config is read | the key listing starts while the config is being checked; a wrong password still costs one search | `internal/global/open_key_listing.go`, `global.go` | S019 and its merge |
| file opens without the runtime poller | `os.OpenFile` registers every file with the poller | on Linux regular files are opened directly; other platforms use upstream's code | `internal/fs/file_open_linux.go`, `file_open_other.go`, `file.go`, `fs_local.go` | S013 |
| direct path stat | `os.Lstat` | on Linux a direct `lstat` with one allocation less | `internal/fs/path_stat_linux.go`, `path_stat_other.go` | S016 |
| prepared cipher | `Seal` and `Open` expand both AES keys on every call | a prepared key keeps the two expanded AES keys; the kept pair is used only if its key bytes equal the key's bytes at that call; CTR stream, Poly1305 key and MAC are computed per call as before | `internal/repository/crypto/cipher_preparation.go`, `crypto.go`, `open_reader.go`, `internal/repository/key.go` | port A5 (from a T4 tree of S019) |

Contract: the derived key; the error texts and their order for invalid parameters; every ciphertext byte and every
authentication verdict. Repairs: R3 added the parameter checks the lanes lacked (a malformed key file crashed the
process); "R3 (layout)" moved them out of the lanes function for the alignment reason in METHODS.md, section 4; R9
removed a doubled key search. Tests: `scrypt_params_test.go`; `cipher_preparation_test.go` keeps upstream's `Seal`
and `Open` bodies as the reference; `cipher_preparation_witness_test.go` runs the published Poly1305-AES vectors
through the functions production calls.

### 1.2 Lock wait

Upstream writes its lock file, waits 200 ms and checks for conflicting locks before it reads anything else. This
tree starts reading during the wait.

| mechanism | this tree | files | season |
|---|---|---|---|
| index load inside the wait, backup | the index is loaded while the lock settles | `cmd/restic/backup_index_load.go`, `cmd_backup.go`, `internal/repository/lock.go`, `lock_file.go` | S013 |
| the same for restore, prune, forget, ls | | `cmd/restic/wait_index_load.go`, `lock.go`, `cmd_restore.go`, `cmd_prune.go`, `cmd_forget.go`, `cmd_ls.go` | S015 (the league also did it for `check`; fix 6 removed that: `check` reads nothing during the wait) |
| snapshots listed first, inside the wait | the wait work lists snapshots before it loads the index, as upstream's order requires | the same files | race fix after S016 |
| a snapshot's ciphertext held during the wait | one snapshot file is read during the wait and kept as ciphertext; it is not selected or decoded before the lock is confirmed, and then goes through the ordinary authenticated path | `cmd/restic/snapshot_metadata_wait.go`, `internal/repository/prepared_snapshot.go` | in the league tree after S019; the season is not recorded in the reviews |
| **validation after confirmation** | after the lock is confirmed the snapshot and index files are listed again, each where upstream lists it and no earlier; what was read during the wait is kept only if the same files are still listed with the same sizes; added index files are loaded; anything else is read again in upstream's order | `cmd/restic/wait_index_load.go`, `snapshot_metadata_wait.go`, `internal/repository/index_loaded_ids.go`, `internal/repository/index/master_index.go` | repair R1; sizes: fix 6; where the listings are taken: fix 4 |
| reports of the wait held back | what the retrying backend would print for reads made during the wait is held and shown only if those reads are used | `internal/backend/retry/held_reports.go`, `backend_retry.go` | fix 5 |

Contract: a command must behave as if it had read everything after the lock was confirmed. The wait itself is
unchanged (`internal/repository/lock_file.go` is not edited by the repair). As the league built it, the mechanism
broke that contract and lost data (R1). Tests: six files `cmd/restic/lock_wait_*_test.go` and
`wait_index_load_validate_test.go`, including the randomized schedule test; `RESTIC_TEST_LOCK_WAIT=200ms` runs
upstream's integration tests with the production wait (R11); `lock_wait_damage_test.go`,
`lock_wait_visibility_test.go` and `lock_wait_reports_test.go` are the tests of fixes 6, 4 and 5. What the validation
proves and what it does not is written at the end of `cmd/restic/wait_index_load.go` and in
[KNOWN-LIMITS.md](KNOWN-LIMITS.md), section 5.3: a kept index is validated by a listing and is not read again.

### 1.3 Index

| mechanism | this tree | files | season |
|---|---|---|---|
| streamed encode, verify and save | an index is encoded, verified by a second encode that feeds a comparison, and saved as a stream | `internal/repository/index/index.go`, `internal/repository/index_verify_direct.go`, `repository.go` | S009 to S011 |
| direct decode and adoption | index JSON is decoded by a direct decoder into the in-memory form and adopted without a second pass | `index/index_decode_eager.go`, `index_decode_reader.go`, `index_adopt.go` | S017 merge |
| canonical streaming decode | the decoder reads the index as a stream instead of holding the whole file; hex is decoded by a hand-written routine | `index/index_decode_canonical_stream.go`, `index_hex.go`, `index_reserve_slots.go`, `index_rewrite_saved.go`, `pack_hash_owned.go`, `internal/repository/index_load_ordinary_stream.go`, `index_load_stream.go` | S018 |
| position and membership tables | blobs are addressed by position in the index for prune's planning and for pack membership | `index/associated_position.go`, `associated_count.go`, `associated_data.go`, `pack_membership.go`, `indexmap.go` | S017 merge |
| index merge and bounded pack listings | `ListPacks` for a selection walks the index once within a bound | `index/index_merge.go`, `index_check_groups.go`, `list_packs_bounded.go`, `repack_values.go` | S017 |
| index rewrite without reload | prune's index rewrite keeps index files that hold nothing to remove and does not load them again | `index/index_rewrite_keep.go`, `index_rewrite_saved.go` | S017 merge |
| listings for repack by pack ordinal | the packs selected for repacking are recorded once as ordinals | `index/repack_routes.go`, `repack_ordinals.go`, `repack_fallback.go`, `associated_count_validate.go` | S020 (kept) |
| workers follow connections | index-load workers follow the backend's connections | `index/index_load_workers.go`, `index_parallel.go` | repair R5 |

Contract: the set of blobs and packs an index describes; the order and content of listings; the bytes of an index
file after decoding. **Not equal to upstream: the compressed bytes of an index file** (finding 11: valid, readable
by upstream, a few bytes different; documented, not changed). Repairs: R10 removed switches that ran upstream's
decoder whenever the debug log was on; F1 fixed a test that did not compile for 32 bit; fix 7 makes the rewrite read
a kept index file raw and compare its SHA-256 with its name before keeping it (`index/index_rewrite_keep.go`); fix
3 releases the per-file notes when the index is cleared. Tests: `index/*_test.go`, among them
`listpacks_witness_test.go` (three listing paths against upstream's loop on 472 selections).

### 1.4 Backup write path

| mechanism | upstream | this tree | files | season |
|---|---|---|---|---|
| exact-size chunk copies, bounded read buffers | a pooled buffer is held until the upload callback | the chunk is copied to an exact-size allocation and the pooled buffer returned at once; read buffers are bounded | `internal/archiver/file_saver.go`, `buffer.go` | S001, S005, S008 |
| known-blob admission | every chunk goes through the save path | a chunk the index already holds completes on a short lane | `internal/repository/known_completion_lane.go`, `packer_manager.go` | S005 |
| hash-on-write for packs | the pack is hashed after it is written | the pack is hashed while it is written | `internal/repository/packer_manager.go` | S006 |
| hashing off the reader | the reading goroutine hashes each chunk | large chunks are hashed on a separate lane (S018), and hashing overlaps reading (S019) | `internal/archiver/file_saver_hash.go`, `ordinary_hash_pipeline.go`, `parent_pair_hash.go` | S018, S019 |
| reading at the parent's cuts | every file is chunked from scratch | in a repeat backup a changed file is first read at the chunk boundaries its parent snapshot had; predictions are checked against the actual bytes and a miss falls back to the ordinary chunker | `internal/archiver/parent_cut_reader.go`, `parent_cut_long.go` | S018 |
| exact chunk scanner | the chunker library's scan | a scanner that finds the same cut points with less work; compared with the chunker for every supported polynomial degree | `internal/repository/chunker_exact.go`, `chunker_segments.go`, `chunker.go` | S019 |
| parent tree reuse | each tree is rebuilt and encoded | a tree whose entries are all unchanged reuses the parent's encoded tree | `internal/archiver/parent_tree_reuse.go`, `parent_finder.go`, `tree_saver.go`, `internal/data/parent_tree.go`, `parent_finder.go` | S013, S017 |
| node and tree serialisation | `encoding/json` | a direct serialiser for nodes and a streaming tree encoder; output compared with upstream's bytes | `internal/data/node_json_scalar.go`, `node.go`, `tree.go`, `tree_preparation_size.go` | S012, S016 |
| pack tails | | a captured pack tail is listed by its owner, and its header decrypted in place | `internal/repository/pack/owned_tail.go`, `owned_tail_entries.go` | by S018 |
| verification on save without a second buffer | the saved blob is decrypted and decoded into a buffer and hashed | the same checks as a stream | `internal/repository/save_verify_stream.go`, `save_verify_compressed.go`, `save_verify_hash_writer.go` | S018 |
| encoder destination sized once | `EncodeAll` grows its output block by block | from 512 KiB the destination is allocated once at the encoder's own bound; same bytes | `internal/repository/compress_presized.go` | "R4 (cost)" |
| item lines formatted only when printed | every completed item's line is formatted, then dropped below verbosity 3 | the formatting is skipped when the line will not be printed | `internal/ui/backup/text.go`, `progress.go` | port A14 (from a T4 tree of S016) |

Contract: chunk boundaries, blob IDs, tree JSON and tree IDs equal to upstream's (the judge compares the root tree
ID in every round; check C07 on special files); the stored form of every data and tree blob equal to upstream's (F2;
check C06); the save is verified before it is reported. **Removed by repairs:** the compression sampler (R4) and the
rule that stored an incompressible blob raw (F2). The first backup therefore compresses everything, as upstream
does. Tests: `internal/archiver/*_test.go` (chunker and parent-cut differentials), `internal/data/*_test.go`,
`internal/repository/repository_compression_auto_test.go`, `compress_presized_test.go`,
`internal/ui/backup/text_complete_item_test.go` (against a verbatim copy of upstream's function at verbosity 0 to 4).

### 1.5 Restore

| mechanism | this tree | files | season |
|---|---|---|---|
| parallel second pass | metadata, special files and hard links are restored by workers with ordered error reporting | `internal/restorer/restorer.go`, `cmd/restic/cmd_restore.go` | S009 to S011 |
| node look-ahead and a wider window | upcoming nodes are decoded while the ordered walk handles the current files; the second-pass window is 64 | `internal/restorer/node_lookahead.go`, `internal/data/tree_node_reader.go` | S016 |
| tree prefetch | trees for the second pass are decoded ahead on a list of at most 524,288 IDs | `internal/restorer/tree_prefetch.go`, `tree_id_list.go` | S014; bound: F5 |
| tree carry | trees verified in the first pass are kept, inside a byte budget, and used in the second | `internal/restorer/tree_carry.go` | S017 merge |
| metadata tree decoder | the second pass decodes trees without keeping content IDs | `internal/data/tree_metadata.go`, `node_name_plain.go` | S020 (kept), with the R7 name rule |
| one request per pack span, read in windows | a pack's needed range is fetched in one request and decoded in windows, with committed-prefix retries | `internal/repository/stream_pack_coalesced.go`, `restore_window_hold.go`, `restore_decoder.go`, `restore_stream_budget.go`, `internal/backend/retry/prepared_load.go`, `backend_retry.go` | S019 and its merge |
| extra workers inside a byte budget | a bounded worker for packs of small blobs and a third ordinary worker, both admitted against a budget | `internal/restorer/ordinary_extra_worker.go`, `restore_hold_budget.go`, `filerestorer.go`, `internal/restic/restore_load.go`, `restore_hold_budget.go`, `internal/backend/sema/backend.go`, `semaphore.go` | S017, S019 |
| file writer | per-file state kept in slices; a single-blob file is planned without a map entry | `internal/restorer/fileswriter.go`, `restore_offsets.go`, `planned_singleton.go` | S013 and later |

Contract: the restored tree, entry by entry (the judge compares the full manifest with upstream's restore in every
round; check C08: 75 option cases, 8 backend failure patterns, SFTP); each blob is authenticated and hash-checked
before it is written; error order. **Not equal to upstream: finding 4**, the tree carry means a carried tree is
read from the repository once per restore, not twice (documented, not changed; KNOWN-LIMITS.md, section 5.1).
Repairs: R2 (handlers that use the backend no longer run inside `Load`; a `Load` without data fails closed), R8
(hard-link index complete before the second pass), M22 and F5 (memory per directory), fix 9 (the prefetch budget
counts the hidden tail of a node's extended attributes). The commit "Many directories" changed how the two passes
hand work between goroutines: the first pass decodes runs of directory nodes itself, the second receives prepared
trees in batches of up to 64 trees and 256 KiB (`node_lookahead.go`, `tree_prefetch.go`; tests in
`directory_handover_test.go`). Tests: `internal/restorer/*_test.go`, among them
`tree_id_list_test.go` and `restorer_hardlink_linkcount_test.go`; `internal/repository/stream_pack_table_test.go`
runs upstream's pack-stream test table against the production paths.

### 1.6 Check

| mechanism | this tree | files | season |
|---|---|---|---|
| extra worker on an extra slot | `check --read-data` runs one more pack reader where the backend grants an additional load slot (the local backend at its default connection count), and only there | `internal/repository/checker.go`, `checker_extra_slot.go`, `internal/backend/local/config.go`, `limiter/limiter_backend.go`, `sema/backend.go` | S016; the condition: port A3 |
| pack sizes and types prepared early | pack sizes are listed early; pack-type summaries are taken during the index load instead of a second walk | `internal/repository/checker_pack_sizes.go`, `checker_pack_types.go`, `internal/checker/checker.go` | S017, S020 (kept) |
| large packs | fixed limits for the extra worker's decoder and ciphertext buffer; a cipher reader and header handling for large and mixed packs | `internal/repository/checker_capacity.go`, `checker_cipher_reader.go`, `checker_large_header.go`, `crypto/open_reader.go` | S018 |
| structure pass overlapped | the snapshot and tree structure check runs beside the pack reads, under the confirmed lock | `cmd/restic/check_structure_overlap.go`, `cmd_check.go` | S017 merge |

Contract: every pack and every blob is verified as upstream verifies it, and the same errors are reported. Check C05
sent a corrupted pack through every route (1,200 comparisons, none differ). Before A3 the extra worker ran on every
backend, one worker more than the backend had connections. Fix 6 returned `check` to upstream's lock call and
order: it reads snapshots and index after the lock is confirmed. Fix 1 gave the streaming decode of
`check --read-data` upstream's ceiling of 64 GiB of decoded bytes (`internal/repository/check_decode_limit.go`).
Tests:
`internal/repository/checker_*_test.go`.

### 1.7 Prune

| mechanism | this tree | files | season |
|---|---|---|---|
| asynchronous repack saves | a repack worker hands blobs to the uploader and continues | `internal/repository/repack.go` | S008 |
| planning by position | prune plans from index positions instead of per-blob map updates | `internal/repository/prune.go`, `prune_position.go`, `prune_pack_run.go` | S017 merge, S020 (kept) |
| header reads overlapped | pack headers are read beside the planning | `internal/repository/prune_header_overlap.go` | S017 |
| one pack read ahead | a repack worker reads the next pack while the saves of the pack before it finish; never more than one ahead, and it returns only after every save it admitted has called back | `internal/repository/repack_completion_overlap.go`, `stream_pack_released.go` | port A16 (from a T3 tree of S020), fitted to R2 |

Contract: repack returns success only when every blob it was asked to keep has been stored; the caller saves the
index and only then deletes packs. Check C11 compared 12 prunes with upstream on a 1.5-million-blob index (plan,
packs kept and removed, blobs, stored form). On the judged commit `b8cf439`, C12 and C14 exercised 89 prune trials:
40 kills and 49 backend disturbances, with no repository that upstream's checks reject afterwards. The controls ran
separately on upstream: 10 kills and 24 disturbances, no problem either. Repairs: R2 (the deadlock of `repair packs` with one connection); fix 2 (saves of repack and
copy run on the context of their workers, so a read error cancels a save that is blocked;
`internal/repository/repack_scope.go`). Tests: `repack_read_error_cancellation_test.go`, `repack_scope_test.go`, `repack_completion_overlap_model_test.go` (20,000 random pack sequences with read and
save errors), `repack_completion_save_error_test.go`, `prune_witness_test.go` (600 states against upstream's
function, 13 real prunes read back in full).

### 1.8 Output

| mechanism | this tree | files | season |
|---|---|---|---|
| batched standard output | with output not on a terminal, messages for a regular file are collected (64 KiB, 100 microseconds idle) and written together | `internal/ui/termstatus/status.go`, `stdio_wrapper.go` | S004 |
| ls in batches | `ls --json` decodes and prints nodes in batches | `internal/walker/listing_preparation.go`, `listing_walk.go`, `cmd/restic/cmd_ls.go` | S019 |

Contract: every byte of output, the order of lines across stdout and stderr, exit codes. The judge compares parsed
`ls --json` output in every round; check C15 compared 1,533 lines of backup output. Repair: R6 (a failed write no
longer holds back everything behind it); fix 8 (collected output keeps the message boundaries, so the message
after a failed write is still written). Tests: `internal/ui/termstatus/status_batch_boundary_test.go`,
`status_failing_writer_test.go`.

## 2. What the league installed, season by season

From the league's inventory ([`../reviews/INVENTORY.md`](../reviews/INVENTORY.md), section 4, D2). Some of these
were later removed by repairs; those are marked.

| season | installed |
|---|---|
| S004 | parallel scrypt; batched stdout; a raw Zstandard frame for lock files; a garbage collection before the index load; a part cap for the pack stream; a checker hashing change; a directory cache in restore |
| S005 | exact-size chunk copy; known-blob admission; raw storage of incompressible blobs (**removed: R4, F2**) |
| S006 | a range pool; hash-on-write |
| S008 | bounded backup read buffers; asynchronous repack saves; a stream reader in check |
| S009 to S011 | parallel second restore pass; streamed index encode, verify and save; no verify-open under a freshly created directory |
| S012 | assembly pools; the node serialiser; chunker reset snapshots |
| S013 | index load inside the wait for backup (**repaired: R1**); opens without the poller; a parent-tree iterator |
| S014 | decoded tree prefetch for the second pass |
| S015 | index load inside the wait for every command (**repaired: R1**) |
| S016 | a bounded third check worker; restore window 64; node look-ahead; direct path stat; the race fix |
| S017 and its merge | small-blob restore worker; check pack sizes and early structure pass; prune planning by position; index rewrite without reload; direct index decode and adopt; parent tree reuse; tree carry |
| S018 | large chunks hashed off the reader; parent-cut readers; check on large and mixed packs; canonical streaming index decode; save verify without a second buffer |
| S019 and its merge | one request per pack span read in windows; the exact chunk scanner; overlapped hashing; key listing beside the config check; a third ordinary restore worker in a byte budget; ls decode batches; a one-blob lookup skip |

## 3. The later commits

<!-- BEGIN GENERATED:later-commits -->
| commit | patch | subject | files | non-test files |
|---|---|---|---|---|
| 3 | `0001` | R2: pack handlers that use the backend run after Load has returned; decode errors inside Load are not retried; a Load without data fails closed | 7 | 5 |
| 4 | `0002` | R3: parallel scrypt validates its parameters exactly as x/crypto's scrypt.Key | 3 | 2 |
| 5 | `0003` | R4: CompressionAuto decides from the encoder's output for the whole blob; the sampler is removed | 3 | 1 |
| 6 | `0004` | R5: index-load workers follow the backend's connections, as in stock | 4 | 2 |
| 7 | `0005` | R6: output without status lines writes every message once, as stock; nothing is queued behind a failed write | 5 | 1 |
| 8 | `0006` | R7: the direct tree decoder skips strconv.Unquote only for names it returns unchanged | 3 | 2 |
| 9 | `0007` | R8: restore builds the hard-link index in the first pass and looks every file up in it, as stock | 2 | 1 |
| 10 | `0008` | R9: a wrong password that needs no prompt costs one key search, as in stock | 4 | 1 |
| 11 | `0009` | M22: restore forgets a created directory when the traversal leaves it | 2 | 1 |
| 12 | `0010` | R10 (1/2): gofmt for the league's files under internal | 24 | 7 |
| 13 | `0011` | R10 (2/2): hygiene under internal | 17 | 9 |
| 14 | `0012` | R1 (validate): compare lock-wait reads with the repository after confirmation | 10 | 10 |
| 15 | `0013` | R1 tests: the three reviewers' witnesses as regression tests | 2 | test only |
| 16 | `0014` | R1 tests: randomized schedules, verdicts, request counts, settle delay | 4 | test only |
| 17 | `0015` | R10 (cmd/restic): the debug log no longer selects another code path; gofmt | 4 | 3 |
| 18 | `0016` | R11 (cmd/restic): the integration tests can run with the production lock wait | 3 | test only |
| 19 | `0017` | R10 (3/3): internal/debug/debug.go back to stock (IsEnabled has no caller left; the reordering in Log did nothing) | 1 | 1 |
| 20 | `0018` | S020 (prune): pack statistics are accumulated per run of one pack | 3 | 2 |
| 21 | `0019` | S020 (prune): the two count checks of the used-blob set read its arrays | 4 | 2 |
| 22 | `0020` | S020 (index): pack listings for repack route by pack ordinal | 9 | 4 |
| 23 | `0021` | S020 (restore): the second pass decodes trees without keeping content IDs | 5 | 2 |
| 24 | `0022` | S020 (check): pack types are collected while the index files are loaded | 3 | 2 |
| 25 | `0023` | R4 (cost): the encoder's destination is allocated once for blobs that may be stored uncompressed | 3 | 2 |
| 26 | `0024` | A3: check's extra worker takes packs only where it has a slot of its own | 3 | 2 |
| 27 | `0025` | S020 review (index): the stock listing loop that nothing calls moves to the tests | 5 | 2 |
| 28 | `0026` | S020 review: the reviewer's witnesses for the kept S020 pieces become regression tests | 5 | 1 |
| 29 | `0027` | A16: a repack worker reads one pack beside the pending saves of the pack before it | 7 | 2 |
| 30 | `0028` | A14: backup formats a completed item's line only when it will be printed | 2 | 1 |
| 31 | `0029` | A5: a prepared key keeps its two expanded AES keys between calls | 5 | 4 |
| 32 | `0030` | R3 (layout): scrypt's parameter checks run in front of the lanes function, not inside it | 2 | 2 |
| 33 | `0031` | F2: a blob that does not compress is stored in compressed form, as stock stores it | 4 | 2 |
| 34 | `0032` | F1 (test): the index tests compile for 32 bit | 1 | test only |
| 35 | `0033` | F3 (M22 test): the directory-state test counts entries instead of sampling the heap | 2 | 1 |
| 36 | `0034` | F5: restore keeps a bounded list of tree IDs, not one for every directory | 3 | 2 |
| 37 | `0035` | Review of the ports (A5 test): the reviewer's witness for the cipher preparation becomes a test of the tree | 1 | test only |
| 38 | `0036` | Review of the ports (A16 test): a second, independent guard for a save error that arrives after the pack was read | 1 | test only |
| 39 | `0037` | F5 (bound): the tree-ID list holds up to 524,288 IDs, the size at which nothing is lost that was measured | 2 | 1 |
| 40 | `0038` | Red team fix 9: the restore prefetch budget counts the hidden tail of a node's extended attributes | 3 | 2 |
| 41 | `0039` | Red team fix 8: collected terminal output keeps the message boundaries | 3 | 2 |
| 42 | `0040` | Red team fix 3: the index releases its per-file notes when it is cleared | 2 | 1 |
| 43 | `0041` | Red team fix 7: an index file the rewrite keeps unread is first compared with its name | 4 | 2 |
| 44 | `0042` | Red team fix 2: repack and copy save on the context of their workers | 5 | 4 |
| 45 | `0043` | Red team fix 1: check's streaming decode keeps upstream's ceiling on decoded bytes | 3 | 2 |
| 46 | `0044` | Red team fix 6: files kept from the lock wait are compared by size too, and check keeps nothing | 17 | 8 |
| 47 | `0045` | Red team fix 4: the listings that validate lock-wait reads are taken where stock lists | 3 | 1 |
| 48 | `0046` | Red team fix 5: nothing is heard of the reads of the lock wait unless they are used | 5 | 3 |
| 49 | `0047` | Many directories: restore hands work between its goroutines per batch, not per directory | 3 | 2 |
| 50 | `0048` | Release: the build names itself, and a release build can set the version string | 1 | 1 |
| 51 | `0049` | Review of final3: the kept index's contract stated exactly, and a witness for fix 2's suppression | 2 | 1 |

49 commits. Patch numbers are the file names in `league/series/`.
<!-- END GENERATED:later-commits -->

In groups: commits 3 to 19 are the repairs R1 to R11 and M22. The commits whose subject starts with "S020" are the
kept work of the last season. A3, A5, A14 and A16 are the ports. "R4 (cost)" and "R3 (layout)" remove two costs the
repairs had introduced. F1, F2, F3 and F5 are the release-check fixes. The commits named "review" turn reviewers'
witnesses into tests of the tree. "Red team fix 1" to "Red team fix 9" follow the last independent review (the
numbers are those of the fixes, not of the findings: [RED-TEAM.md](RED-TEAM.md), sections 2 and 3). "Many
directories" changes restore's hand-overs. "Release" makes the version string a variable with the value
`0.19.1-dev-dealer.1 (compiled manually)` (`internal/global/global.go`). The last commit rewrites one comment and
adds one test. Each commit message says what the code did before, what it does now and which tests show it.

One commit message says more than the code does and was left as written: "Red team fix 4" ends "With any backend
this build knows at each point what stock knows there". It knows what upstream's listing knows; upstream also reads
the files. The last commit corrects the same sentence in the source.

## 4. League work that is not in this tree

- **S020's three lock-wait mechanisms** (compression prepared during the wait, a pack read by `check` during the
  wait, snapshot and trees read by `restore` during the wait): dropped by the review. SOUNDNESS.md, section 5.
- **`check`'s work during the lock wait** (in the league's tree since S015): removed by fix 6 of the last review.
- **Parts of the ports' source trees that were not taken:** an index decoder recycler and a raw lock frame (the tree
  A5 came from); a decode assistant for check's extra worker (the tree A14 came from); a package-level registration
  variable in the repack port (A16 calls the loop directly).
- **Ideas with a measured gain that were not built or not ported:** a tree pack fetched in parallel ranges, a zero-
  write skip after `fallocate`, a first-pass tree prefetch, a direct tree-node decoder (it would have been a third
  hand-written tree decoder; two of the existing ones needed the R7 repair), a second repack worker for small packs,
  a check read overlap, the memory-only changes. Each with its number and reason in
  [`../reviews/merge/FINAL-MERGE-REPORT.md`](../reviews/merge/FINAL-MERGE-REPORT.md), "What was left out, and why".
- **Everything closed during the seasons:** RESULTS.md, section 11.
