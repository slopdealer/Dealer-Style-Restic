# Independent correctness review

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Scope: backup write path, chunking, save verification, encryption/compression, tree/node decoding, restore, filesystem optimisations, and terminal status. Compared `stock/` (restic 0.19.1-dev, ba802d42) with `tree/`. No network or external APIs used. Review began 2026-10-07 14:12:52 UTC and completed 2026-10-07 14:53:25 UTC, within the requested hour. Test host: Windows/amd64, Go 1.27.0.

**Four confirmed findings: one BLOCKER and three MEDIUM regressions.** Each has an added Go reproduction test. The BLOCKER is a missing integrity check; no incorrect restored file contents or incomplete backup reported as success was otherwise reproduced.

**1. BLOCKER — carried restore trees bypass upstream's second-pass integrity check.**

Files and lines: `tree/internal/restorer/tree_carry.go:32` (retention; publication at line 86), `tree/internal/restorer/tree_carry.go:95` (reuse), `tree/internal/restorer/tree_prefetch.go:54` (successful carry skips the load at line 57), and `tree/internal/restorer/restorer.go:756`, `:858`, `:882` (installation and second-pass use).

Scenario:

1. Start with a valid v2 repository, caching disabled, containing one small root tree and a regular file whose content is in a separate data pack.
2. Restore normally. The first traversal loads, authenticates, hashes and decodes the tree. The candidate retains its decoded nodes.
3. While restoring the file content, a repository disk fault or concurrent change damages one ciphertext byte of that stored tree blob.
4. The candidate's second pass takes the retained record and performs metadata traversal without loading/authenticating the stored tree again. `RestoreTo` returns success.
5. An immediate ordinary `LoadTree` rejects that same stored blob with `ciphertext verification failed`.

Upstream calls `data.LoadTree` in both traversals: `stock/internal/restorer/restorer.go:159`, first pass at line 376 and second pass at line 444. With the identical injected fault, upstream's second traversal rejects the ciphertext and `RestoreTo` returns an error. This is a missing integrity check under the requested drop-in contract; the test does not claim the already authenticated file bytes were restored incorrectly. The retained copy was valid when first read, which is why this finding specifically requires damage between the traversals.

Confidence: **high**. **Reproduced in both versions.** `tree/internal/restorer/review_seat3_tree_integrity_test.go:64`, `TestReviewSeat3RestoreRechecksTreeIntegrity`, fails in the candidate (`restored=1`, `restoreErr=<nil>`) and passes against the actual upstream sources using a temporary Go test overlay, with an authentication error from upstream. No upstream source file was changed; the temporary overlay and build caches were removed.

**2. MEDIUM — lock and streamed-index compression change stored bytes.**

Files and lines: `tree/internal/repository/repository.go:490` (lock compressor), `:508` (raw Zstandard frame), `:604` (streamed index encoder), `:657` (lock dispatch), and `tree/internal/repository/index/index.go:490` (index save dispatch).

Scenario:

1. Open a v2 repository using any of `auto`, `off`, `fastest`, `better`, or `max`.
2. Save a normal lock record. The candidate uses its handwritten raw-block Zstandard frame; upstream uses the configured `EncodeAll` encoder. A 133-byte lock JSON fixture occupies 143 compressed-envelope bytes in the candidate versus 114–116 upstream.
3. Save an index large enough to require streaming blocks. The candidate's writer emits a frame without the content-size field emitted by upstream's `EncodeAll`. An index containing 3,000 pack entries differs in all five modes; for `auto`, 212,092 bytes versus 212,096.

The tests save through the actual repository code and authenticate/decrypt the saved ciphertext before comparing compressed bytes, so this difference is independent of random encryption nonces. Both representations decompress to the original JSON. This violates the explicitly required stored-byte identity and, for locks, changes stored size and ignores the selected compression level. No format unreadability or data loss was demonstrated.

Upstream routes these saves through `compressUnpacked`, which uses `getZstdEncoder().EncodeAll` with the selected compression options: `stock/internal/repository/repository.go:454`, `:462`, `:501`, and `stock/internal/repository/index/index.go:385`.

Confidence: **high**. **Reproduced.** `tree/internal/repository/review_seat3_encoding_test.go:46` and `:72`: `TestReviewSeat3LockStoredBytesParity` and `TestReviewSeat3IndexStoredBytesParity` fail in every compression mode. This comparison isolates the compressed representation for identical JSON; it does not depend on index JSON order or encryption randomness. Repository v1 stays on the original path.

**3. MEDIUM — batched output drops the next message when a write error lands exactly at a newline.**

File and lines: `tree/internal/ui/termstatus/status.go:504`, especially remainder selection at line 511 and the unconditional newline discard at lines 513–517.

Scenario:

1. Redirect output to a regular file, enabling batching, and submit two messages: `first\n` and `second\n`.
2. The combined write returns `n=6` plus an I/O error: the first message was completely written, and the next message has not been attempted. A quota/disk-full boundary is one possible cause.
3. `writeBatch` starts its remainder at byte six, finds the newline of `second`, and discards that entire message as though it belonged to the failed write.
4. If later writes would succeed, `second\n` is nevertheless lost. The injected writer returns success on subsequent calls and demonstrates this exactly.

Upstream performs one write for each `Print` message (`stock/internal/ui/termstatus/status.go:342`). It reports the first write's error and still writes `second\n`. The candidate outputs only `first\n`. The batching code has lost the original message boundary needed to decide which message to abandon.

Confidence: **high**. **Reproduced by deterministic I/O fault injection.** `tree/internal/ui/termstatus/review_seat3_batch_test.go:26`, `TestReviewSeat3BatchErrorAtMessageBoundary`, compares the exact upstream write sequence with the candidate batch routine. Real disk exhaustion was not required or exercised.

**4. MEDIUM — prefetch's byte budget omits retained attributes in a slice's hidden tail.**

Files and lines: `tree/internal/restorer/tree_prefetch.go:89` (budget decision), `:124–125` (only visible attributes' payloads are counted), and `:100` (accepted node is retained). The separate carry estimator already handles this case at `tree/internal/restorer/tree_carry.go:133`.

Scenario:

1. Supply an authenticated tree containing a node with duplicate `extended_attributes` JSON fields. This is noncanonical JSON that both versions accept.
2. The first array has two attributes, with a large value in the second one. The later array has only the first attribute. Go's JSON decoder shortens/reuses the slice, leaving the prior second attribute referenced in its backing tail beyond `len`.
3. `estimatePreparedNodeBytes` counts the capacity of the attribute structs but traverses only the visible entries when counting their names and values.
4. The fixture retains an 8,388,608-byte hidden value while the prefetch estimate is just 520 bytes. `prepare` accepts the node under its 262,144-byte tree budget.
5. Many such nodes, or larger hidden values, can defeat that budget. The prefetch producer can retain a queued tree and a following prepared tree during file restoration, introducing substantial extra memory compared with upstream's traversal.

Upstream does not accumulate decoded future trees in a prefetch queue; it processes the currently loaded tree's nodes as traversal consumes them (`stock/internal/restorer/restorer.go:159`, `stock/internal/data/tree.go:48`). Upstream also has the slice tail, but does not add this new ahead-of-traversal retention. The candidate's own carry estimator demonstrates how to account for the retained values: iterate the backing tail as well as visible entries.

Confidence: **high** for the admission/accounting regression. **Reproduced** in `tree/internal/restorer/review_seat3_prefetch_budget_test.go:13`, `TestReviewSeat3PrefetchHiddenAttributeBudget`, using the real metadata decoder. Actual out-of-memory termination was not exercised. The 16,777,742-byte complete estimate includes allocation slack; the hidden value itself is exactly 8 MiB. This is an extra-memory regression, not evidence that total process memory has literally no upper limit.

**Reproduction commands**

Run from `tree/`, with networking disabled (`GOPROXY=off`, `GOSUMDB=off`, `GOTOOLCHAIN=local`, `GOFLAGS=-mod=readonly`). Each command below intentionally fails on the reviewed candidate by asserting upstream parity or the stated memory budget:

```text
go test -run '^TestReviewSeat3RestoreRechecksTreeIntegrity$' -count=1 ./internal/restorer
go test -run '^TestReviewSeat3(Lock|Index)StoredBytesParity$' -count=1 ./internal/repository
go test -run '^TestReviewSeat3BatchErrorAtMessageBoundary$' -count=1 ./internal/ui/termstatus
go test -run '^TestReviewSeat3PrefetchHiddenAttributeBudget$' -count=1 ./internal/restorer
```

**Examined mechanisms with no other regression found**

| Mechanism | Evaluation and evidence |
| --- | --- |
| Exact and segmented chunkers, factory reset snapshots | Compared recurrence, minimum/maximum cuts, seed/window state and fallback construction with upstream chunker v0.5.0. Independent differential passed for every supported polynomial degree 8–53, fragmented inputs, empty calls and resets. Existing boundary/fragmentation tests passed. This is evidence of parity, not an exhaustive proof for every byte string. |
| Parent-cut reader and long-pair route | Predictions are validated against actual bytes; earlier cuts replay through the ordinary chunker. EOF tails, invalid hints, failed predictions, allocation-base reclamation and cancellation were inspected and tested. |
| File saver and chunk hash pipelines | Unknown uploads get owned copies; borrowed assembly is joined before reuse/growth. Size is a reservation hint and actual EOF sets saved size. Focused changing-file, delayed-callback, cancellation and read-error tests passed. The unchanged-file decision retains upstream's `fileChanged`/blob-presence checks. |
| Buffer pools and hash-lane admission | Pools/lanes are bounded; asynchronous submission keeps ownership through callback and submission completion. Overflow belongs to the file and is released. Source inspection and focused archiver tests found no aliasing regression. |
| Parent-tree retention, reference tree builder, tree saver | Retention charge transfers to save completion; equality checks complete encoded bytes. Data/archiver encoding, error and ownership tests passed. |
| Known-blob completion lane and pack upload | Checked queue bounds, cancellation propagation, joining before flush, pending versus indexed blobs, temporary-file ownership, merge failures and on-write pack hashing. Relevant repository tests passed. No process-kill campaign was performed. |
| Presized compression of data/tree blobs | Uses the same encoder/options and only changes output capacity. Independent stored-compressed-byte comparisons passed in all five modes at the small/large boundaries. The exception is unpacked lock/index compression in finding 2. |
| Raw and compressed save verification | Authentication precedes hashing. Independent comparison against upstream's eager verifier passed for all modes, raw/compressed payloads around both thresholds, wrong IDs, damaged/truncated compressed payloads and trailing data. Stream errors decline to the original decoder; whole-output hashing remains required. |
| Scrypt parameters and parallel lanes | Validation conditions/error order match x/crypto; lane scratch is private and PBKDF2 concatenation/order is preserved. Full crypto suite passed, including differential KDF/invalid-parameter cases. |
| Prepared cipher state, key loading, `OpenReader` | Published AES schedules are immutable and checked against both current keys. CTR/MAC state remains per call; `OpenReader` authenticates all ciphertext before returning a reader. Full crypto suite, key mutation/copy/concurrent-use and corrupted-ciphertext tests passed. |
| Tree/node/parent/metadata decoders | Framing and error replay compared with upstream. Full data suite and independent 20,000-payload differential passed for returned values and error text/types, including duplicate/case-variant fields, unusual types, invalid IDs/base64, Unicode, truncation and trailing input. Metadata intentionally omits content IDs only for the metadata pass. Finding 4 concerns retention, not different decoded visible values. |
| Restore pack streams and blob verification | Checked window reuse, committed-prefix transport retries, alternate-copy recovery, response release before recovery and checksum/hash-before-write ordering. Forty-eight repository mechanism tests and focused restorer tests passed; finding 1 concerns tree revalidation rather than data-blob verification. |
| Additional/bounded restore workers and hold budget | Checked ordinary admission, fallback promotion, decoder ownership, byte accounting/release, cancellation and potential connection/budget cycles. Focused worker/decoder/budget tests passed. The separate prefetch accounting exception is finding 4. |
| Restore offsets, singleton associations, hard links and sparse files | Offset slices grow independently; associations are retired after callbacks. Canonical hardlink indexing/dependencies and sparse-overwrite rules retain upstream behavior. Focused tests passed, including mixed hardlink counts, repeated blobs, partial overwrite and sparse restoration. |
| Restore metadata workers, lookahead and bounded ID list | Checked dependency drains, ordered warning/error reporting, worker settlement before traversal errors, early-stop joins, directory boundaries, changing selection order and retention bounds. Focused tests passed, subject to findings 1 and 4. |
| Filesystem optimisations | Linux stat field/mode conversion compared with the local Go implementation; direct-open flags, EINTR, no-atime fallback, descriptor type checks and non-Linux fallback inspected. Symlink and VSS privilege failures were reproduced in upstream as well. Linux-specific paths were not executed on this Windows host. |
| Terminal/line-writer batching | Lock order, shutdown flush, timer ownership, bounded batches, inter-stream ordering and failure handling inspected. Original termstatus suite passed; independent boundary fault injection found finding 3. |

Passing validation: full crypto suite (26.765 s), full original data suite (6.029 s), independent decoder differential (1.579 s), independent chunker/save-verification differential (2.224 s), 48 targeted repository mechanism tests (16.101 s), focused archiver tests (17.999 s), focused restore tests (57.173 s), and full original termstatus suite (0.786 s). The newly added finding tests are expected failures, listed above.

**Limits of the review**

Full archiver/restorer/filesystem testing is not green on this host: symlink creation lacks privilege; VSS/provider calls return access denied; broader Windows attribute/security-descriptor tests also failed. Symlink/VSS failures have explicit upstream controls; the other Windows failures remain unclassified and are not presented as confirmed candidate regressions. The focused restore/backup suites described above passed. No Linux execution or race-detector run was possible here (cgo disabled, no C compiler found).

Concurrency/cancellation/I/O failures were covered by source inspection and selected tests, not exhaustive interleaving exploration, multiple real restic processes, forced termination at every point, or injected hardware faults throughout the pipeline. The restore-tree fault was specifically injected and compared in both versions. Changes outside this focus area were not audited. No production code was modified; six reproduction/verification test files were added under `tree/`.
