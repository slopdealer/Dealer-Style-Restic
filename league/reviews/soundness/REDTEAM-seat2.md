# Independent review: prune, repack, index and check

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Baseline: `stock/` (restic 0.19.1-dev, ba802d42); candidate: `tree/`.
Started 2026-10-07 14:12 UTC. Scope: deletion planning and execution, repack completion, index loading/rewrite, and repository health checks. Inspection and tests are local and offline. Production files are unchanged.

Six findings reproduced: **three BLOCKER, two HIGH, one MEDIUM**. BLOCKER follows the requested definition, including omitted upstream integrity checks. No test demonstrated deletion of a needed pack or an incomplete backup reported as successful.

## Findings

### 1. Read-data checking loses the cumulative decompression limit and accepts an unrestorable blob

**BLOCKER — missing upstream check / false healthy result. Confidence: high. Reproduced at the production limit.**

Locations: `tree/internal/repository/checker.go:579`; `tree/internal/repository/repository.go:1865-1903`, particularly `:1883-1885`. Upstream: `stock/internal/repository/checker.go:297` and `:420`; `stock/internal/repository/repository.go:1331`.

Scenario:

1. A format-v2 pack contains an authenticated Zstandard blob whose small decoder window expands to **68,719,476,737 bytes (64 GiB + 1)**. Its plaintext SHA-256, pack SHA-256 and header authentication are correct.
2. The index and header agree on all stored fields, including a deliberately wrong, representable uncompressed-length hint of 1. This is a pathological repository input that check must reject rather than trust its hint.
3. Run full read-data checking. The candidate calls `NextHash`, resets the streaming decoder and hashes through `io.Copy` without counting or limiting total decoded bytes.
4. The entire blob is hashed and accepted. Reading the same blob through the repository's restore/loading path fails with `decompressed size exceeds configured limit`.

Upstream calls `Next` and `DecodeAll`. Its checker decoder has the library's default **64 GiB maximum decoded output**, and rejects this frame before allocating its advertised output. Switching to streaming changes the meaning of `WithDecoderMaxMemory`: it constrains the streaming window, not cumulative output. A small streaming window therefore does not retain the upstream output ceiling. The normal blob loading/restore decoder still has a 16 GiB ceiling and rejects the same blob.

Reproduction: `tree/internal/repository/seat2_review_test.go:155`, `TestSeat2LargeCheckRejectsUnrestorableBlob`. It saves a real **2,097,279-byte pack**, saves its index, verifies that eager decoding and `LoadBlob` reject it, then calls the real `Checker.ReadPacks`. The candidate returns no errors. The run took 89.12 seconds, hashing 64 GiB + 1 twice while retaining only the compressed pack and a small decoder window; no enormous plaintext allocation was attempted.

The faster `TestSeat2CheckDecoderRetainsCumulativeLimit` at `:115` independently compares the unchanged eager iterator against `NextHash`, using three concatenated 8 KiB frames and a 16 KiB ceiling. Eager decoding rejects the cumulative 24 KiB; streaming checking accepts it. The shared iterator is used by ordinary, small and capacity checker paths. The production-size test exercised the ordinary worker route, not every scheduler route.

### 2. Check reuses an index read before lock confirmation without checking its current contents

**BLOCKER — missing integrity check / damaged repository reported healthy. Confidence: high. Reproduced.**

Locations: `tree/cmd/restic/wait_index_load.go:92-108` (validation compares filenames only), `:397-402` (early index load), `:464-478` and `:499-512` (reuse); `tree/cmd/restic/cmd_check.go:246` and `:275-280` (command wiring).

Scenario:

1. Open a valid repository with snapshots and no cache. Start a normal locked `check` or `check --read-data`.
2. The new lock-wait worker successfully loads an index during the lock's settle delay.
3. Before lock confirmation, that index's ciphertext becomes damaged or truncated under its existing filename. All packs and snapshots remain intact. A same-size bit flip suffices; no hash collision is needed.
4. After confirmation, `validateLockWaitReads` lists the index names. It does not reread the index or compare its contents, and considers the earlier index valid because its name is still present.
5. All subsequent phases use the undamaged in-memory index. Even the read-data phase reads packs, not the damaged index. The command can report no errors although the persistent index fails `LoadRaw`'s content-hash check.

Upstream creates the lock first, then loads snapshots and indexes (`stock/cmd/restic/cmd_check.go:245-258`). Under this timing, its first index read happens after the damage and returns an error. A content-addressed filename does not prove that the bytes currently stored under that filename are intact.

Reproduction: `tree/cmd/restic/seat2_review_test.go`, `TestSeat2CheckRejectsIndexDamagedDuringLockWait`. The test creates a real snapshot in an uncached in-memory repository, completes speculative loading, flips one ciphertext bit under an existing index name, confirms the lock-wait work, and runs index, pack metadata, structure and full read-data checking. The candidate reports zero errors. A fresh post-confirmation checker reports `LoadRaw(<index/...>): invalid data returned`. The test intentionally fails on the candidate. It drives the command's phase helpers, not the CLI lock timer itself.

This finding is conditional on damage/read availability changing in that window; it does not claim that an index already corrupt on the speculative read is accepted.

### 3. Index rewrite skips the upstream reload and integrity checks for full indexes

**BLOCKER — missing integrity check. Confidence: high. Reproduced.**

Locations: `tree/internal/repository/index/master_index.go:436-445`; `tree/internal/repository/index/index_rewrite_keep.go:57-99`. The removed reload is at `stock/internal/repository/index/master_index.go:433-441`.

Scenario:

1. Prune loads a valid full index: at least 50,000 blobs, below the oversized threshold, unique pack membership, no pack from that index selected for removal. Another index can contain unused/repacked packs that trigger rewriting.
2. Before the rewrite stage, the full index becomes corrupted, missing, or unreadable. This can be an I/O/storage fault; no restic process is required to modify a valid index's content.
3. `rewriteKeepsUnread` uses the earlier pack/count notes to skip that file altogether. `Rewrite` reports success without verifying that the retained index is still readable and intact. Prune may proceed with other deletions while retaining damaged index state.

Upstream reloads and decodes **every** input index before deciding to keep it. `LoadUnpacked` performs file hash validation, authentication and decompression; an index error aborts rewriting before indexed packs are removed. The candidate removes those checks from this route. Its comments' immutability premise covers valid content-addressed writes, but not missing files, corruption or read errors.

Reproduction: `tree/internal/repository/seat2_review_test.go`, `TestSeat2RewriteRejectsDamagedKeptIndex`. A saved 50,000-blob index is loaded, one ciphertext bit is flipped, and `Rewrite` is called. A direct `LoadUnpacked` control rejects the same file with `invalid data returned`; rewriting returns nil. This is a rewrite-unit reproduction; its pack locations are synthetic and it does not assert that needed pack payloads were actually deleted.

Findings 2 and 3 share the same incorrect inference from filenames, but affect separate command stages and require separate fixes.

### 4. A repack/copy read error cannot cancel a blocked save, so the command can hang

**HIGH — hang during plausible concurrent I/O failures. Confidence: high. Reproduced with real upload queueing.**

Locations: `tree/internal/repository/repack.go:117` and `:132`; `tree/internal/repository/repack_completion_overlap.go:129`, `:149` and `:162`. Relevant queue/context ownership: `tree/internal/repository/packer_uploader.go:51-58`; `tree/internal/repository/repository.go:852-886`. Upstream uses `wgCtx` at `stock/internal/repository/repack.go:129`.

Scenario reproduced with `copy` between two repositories, with two source and two destination connections:

1. One source worker handles an unreadable pack. Its read error occurs while another worker copies valid blobs.
2. Both destination upload workers are stalled in `Backend.Save`, correctly waiting for their context to be cancelled. The next blob save blocks while queueing its full pack because no uploader is available.
3. The failed source worker returns its error, cancelling repack's `wgCtx`.
4. The candidate submits saves with the outer uploader `ctx`, not `wgCtx`. The queued save remains blocked after the read failure, and its pack completion waits for the callback with `pending.Wait`.
5. Repack cannot finish until that save retires. The enclosing `WithBlobUploader` cannot return the repack error and cancel the outer upload context until repack finishes. The command stays stuck unless the caller cancels or the stalled backend eventually returns independently.

Upstream passes repack's `wgCtx` to synchronous saves. The source failure immediately cancels the blocked `QueuePacker` call. Repack returns its error; the outer uploader group can then cancel the stalled transfers. Both backends in the reproduction honor cancellation; the hang does not require an API violating its context contract.

Reproduction: `tree/internal/repository/seat2_repack_cancellation_test.go:87`, `TestSeat2RepackReadErrorCancelsQueuedSave`. It uses real blob savers, pack managers, the unbuffered upload queue and backend save calls. The injected faults are one source pack read failure and two destination transfers waiting for cancellation. A pass-through uploader records the third save; it neither holds callbacks nor manufactures a save failure.

The control is the `repack` function copied from `stock/internal/repository/repack.go` into the test as `seat2StockRepack`, with only its name changed. It returns the source error without parent cancellation. The candidate remains blocked during the observation interval and returns only after explicit parent cancellation. Both the isolated run and final combined run reproduced this result. Same-repository prune/repack uses the same context/queue code; its corresponding multi-worker case was inspected but not reproduced separately.

### 5. Incremental index reloads retain all historical per-file notes

**HIGH — memory grows without bound over successive index replacements. Confidence: high for retention; eventual OOM not reproduced.**

Locations: `tree/internal/repository/index/index_rewrite_keep.go:25-38` (cloned pack-ID slices), `tree/internal/repository/index/master_index.go:31-35` (clear omits notes), `:341` (new notes on load) and `:380-383` (reload after removed files). Long-lived caller: `tree/internal/fuse/snapshots_dirstruct.go:335`; `tree/cmd/restic/cmd_mount.go:146` permits `--no-lock`.

Scenario:

1. Keep a repository object alive across repeated `LoadIndex` calls, for example a long-running `mount --no-lock` while snapshots change and independent prune processes replace indexes.
2. A previously loaded index filename disappears. `prepareIncrementalLoad` calls `mi.clear` and reloads the current files.
3. The live blob index becomes correct and bounded, but `clear` leaves `fileNotes` intact. Loading each replacement adds a new map entry and clones that file's pack-ID slice.
4. Every obsolete filename and its cloned pack list remain reachable forever. Memory grows with all indexes encountered during the object's lifetime, rather than with the current repository. Large indexes and repeated replacements can exhaust memory.

Upstream resets its live indexes in the same reload case and has no historical-note map. The removed indexes and their pack lists become collectible. Ordinary locked mount prevents concurrent prune; the concrete long-lived command scenario therefore uses the supported `--no-lock` option. The underlying repeated-load retention is independent of FUSE or operating system.

Reproduction: `tree/internal/repository/index/seat2_review_test.go:43`, `TestSeat2IndexNotesReleasedOnReload`. One live file containing one blob is replaced and loaded 100 times. The live index remains one file/blob, but the candidate retains **100 notes and pack-ID slices**, rather than one. The test verifies retention directly; it does not allocate until OOM or run a mount process.

### 6. Streaming index saves change the stored compressed representation

**MEDIUM — violation of the explicitly required stored-byte compatibility. Confidence: high. Reproduced.**

Locations: `tree/internal/repository/repository.go:602-624`; `tree/internal/repository/index/index.go:486-497` (stream-saver dispatch). Upstream uses `EncodeAll` through `stock/internal/repository/repository.go:454-463` and `stock/internal/repository/index/index.go:385-399`.

Scenario:

1. Save a format-v2 index containing 1,000 or 50,000 normal data-blob entries, using the same JSON and compression setting.
2. Upstream compresses the complete JSON with `EncodeAll`. The candidate sends encoder fragments through a streaming Zstandard writer.
3. The authenticated compressed payloads differ. This is independent of the random encryption nonce; the test decrypts before comparison.

Observed with the test's fastest compression setting: 114,920 JSON bytes produce 35,908 upstream payload bytes versus 35,905 candidate bytes; 5,832,732 JSON bytes produce 1,781,642 versus 1,781,638. A one-blob control produces identical payloads.

Both encodings remain readable; no data loss is claimed. They nevertheless fail the stated requirement to retain the same stored bytes and alter index object lengths/contents. Upstream's complete-input compression path does not have this change.

Reproduction: `TestSeat2StreamedIndexStoredPayloadMatchesUpstream` in `tree/internal/repository/seat2_review_test.go`. The 1,000- and 50,000-blob subtests intentionally fail; the one-blob control passes.

## Coverage: examined and judged correct within this review

These are bounded inspection/test conclusions, not an exhaustive proof of every failure timing or option combination.

| Mechanism | Evaluation |
| --- | --- |
| Prune blob selection (`prune.go`, `prune_position.go`, associated positions/counts) | Slot binding, earliest duplicate position, saturation at 255 and last-copy selection preserve upstream decisions. Conservative fallback handles changed layouts and overflow shadowing. No needed-copy deletion found. |
| Prune pack accounting (`prune_pack_run.go`) | Flushing on pack changes and at traversal completion preserves interleaved revisits, partly used packs, duplicate-only packs from interrupted prune, mixed types and statistics. |
| Prune header prefetch (`prune_header_overlap.go`) | Result/error is joined before selection continues; cancellation/discard relinquishes ownership. No deletion proceeds on a reported prefetch failure. |
| Prune options and existing deletion decisions | Missing needed packs and wrong sizes still fail; missing unused packs and unindexed unused packs follow upstream handling. Dry run, repack budget, maximum unused, cacheable-only, uncompressed, smaller-than and unsafe-recovery branches remain upstream logic after selection. Not every option cross-product was executed. |
| Repack completion and ownership | Each admitted callback is joined; predecessor failure drains the successor; duplicate claims are serialized; asynchronous plaintext is copied; the shared 8 MiB copy budget and synchronous oversized path bound copied payload. No success with an unsaved blob found. Cancellation exception is finding 4. |
| Backend connections | Saving callbacks run after released pack loads; one-connection same-repository repack retains upstream rejection. Separate source/destination copy admits its configured workers. No extra connection deadlock found beyond finding 4. |
| Repack enumeration (`repack_routes.go`, fallback, ordinals, bounded collector and values) | Limits are checked before partial routes escape; pack IDs and complete locations survive duplicate pack slots. Producer locks are released before consumers save indexes. Unsupported layouts fall back. |
| Released/windowed/coalesced streams and repair | Range retries do not repeat committed callbacks; read/decode/handler errors remain distinct; fallback loads run after connection release. Window/pool storage is bounded. Repair retains upstream salvage behavior. Repack uses released windowing, rather than the restore coalescer. |
| Index decoding and publication | Raw object hash/authentication precede eager or streaming decoding. Canonical paths validate complete input and terminal stream errors before publication, with unsupported representations falling back. Invalid IDs and out-of-range numbers are rejected. |
| Index reservation/adoption/merge | Null-slot reservation, full location equality, duplicate handling and ordinal stability inspected. Owned initial-index adoption avoids extra copies without publishing a partial load. Historical metadata exception is finding 5. |
| Index rewrite persistence | Output saves join before obsolete indexes and indexed old packs are removed; saved output IDs are preserved when equal to input IDs. Ordinary read/save errors abort. Retained-file validation exception is finding 3. |
| Checker sizes, types and membership | Prepared size maps preserve snapshot filters. Mixed pack types are collected across all locations. Membership compares ID, type, pack ID, offset, length and uncompressed length; complete header validation precedes membership conclusions. |
| Checker ciphertext, pack hash and headers | Hashing covers encrypted bytes; borrowed buffers preserve adjacent blobs and header data; short/wrong-hash downloads fail. Small/large owned-tail header parsing retains authentication and complete-entry validation. Decoded-size exception is finding 1. |
| Checker extra/capacity workers | Extra-slot policy, task admission, bounded refusal/promotion, prior-error preservation and cancellation/draining inspected. Capacity refusal is not treated as a healthy verdict. Shared decompression-limit exception is finding 1. |
| Command overlap (`cmd_prune`, `cmd_forget`, `cmd_check`, wait work, structure overlap) | Removed snapshot/index names trigger rereading; added index IDs are loaded; discarded work joins before unlock; structure errors are drained/cancelled without a channel hang. Same-name integrity exception is finding 2. |
| `internal/checker` and tree dependency | Snapshot filtering and prepared-size forwarding preserve existing decisions. The direct tree decoder retains framing, double name unquoting, raw link-target handling and semantic error replay. Existing differential cases, including 30,000 corrupted trees, pass. No unusual-tree traversal omission found. |
| 32-bit and large-index arithmetic | Route ordinals are constrained below 2^30; pack/route capacities are bounded before integer doubling; ordinal bitmap bounds and decoder numeric validation inspected. Focused 386 tests pass. No additional overflow regression proved; indexes approaching address-space exhaustion were not allocated. |

## Tests and reproducibility

Run commands from `tree/`, using `GOPROXY=off` and `GOSUMDB=off`. Go build cache and temporary directories were placed under `tree/` for the review and removed after verification. No dependencies were downloaded.

Passing validation before adding the intentionally failing reproduction tests, unless otherwise stated:

- `go test -count=1 ./internal/repository ./internal/repository/index ./internal/checker`: PASS, 37.950s / 19.278s / 1.835s.
- Focused prune/repack tests in repository and index packages: PASS.
- `GOARCH=386 go test -run 'TestPrune|TestRepack|TestAssociated|TestCheck' -count=1 -timeout 8m ./internal/repository ./internal/repository/index ./internal/checker`: PASS, 10.306s / 0.876s / 0.587s. Native 386 execution, not merely compilation.
- `go test -count=1 -timeout 5m ./internal/data`: PASS, 2.767s. This includes existing direct-versus-ordinary tree semantic/framing/ownership tests and the 30,000-case corruption comparison; run after repository/check reproductions were added.
- `go test -run '^TestEdgeCaseRepos$' -count=1 -v ./cmd/restic`: PASS, 7.547s. All eight cases: no-index, index-missing-blob, missing-data, missing-unused-data, unreferenced-data, obsolete-index, mixed-packs and duplicates. Run after the checker reproduction was added.

Other attempted command integration tests, including prune/JSON/filter/forget cases, failed during fixture extraction because the available Windows tar could not create `testfile-symlink`. They did not exercise the commands and are not evidence of a candidate regression. Their coverage remains unverified here.

Reproduction files added, with assertions expecting upstream-compatible behavior:

- `tree/internal/repository/seat2_review_test.go`
- `tree/internal/repository/seat2_repack_cancellation_test.go`
- `tree/internal/repository/index/seat2_review_test.go`
- `tree/cmd/restic/seat2_review_test.go`

`go test -run '^TestSeat2' -count=1 -v -timeout 2m ./internal/repository ./internal/repository/index ./cmd/restic` reproduces findings 2–6 and the scaled guard omission in finding 1. These failures are intentional evidence, not fixes. The production-size test skips unless explicitly enabled:

```powershell
$env:RESTIC_SEAT2_LARGE_CHECK='1'
go test -run '^TestSeat2LargeCheckRejectsUnrestorableBlob$' -count=1 -v -timeout 8m ./internal/repository
```

That command was also run and failed as expected, confirming finding 1 with actual saved pack/index data. The suite was not rerun wholesale after adding tests whose purpose is to fail on this candidate. No crash-at-every-instruction campaign, full concurrent-process matrix or release-wide audit was performed.
