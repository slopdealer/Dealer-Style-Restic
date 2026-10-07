# Repairs R2 to R10 (and M22) on `incumbents/S019-merge`

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Date: 2026-10-06. Workstation only (Windows 10, go1.27.0, not elevated). No box, no API calls.

- Working copy: `final-candidate\work\repairs`, branch `repairs` cut from `base` (4579a38), 11 commits.
- Patches: `final-candidate\repairs\R2-R10\0001 ... 0011-*.patch` (`git format-patch base..repairs`).
- Nothing under `cmd/restic/` and not `internal/repository/lock_file.go` was touched (66 files changed, all under `internal/`).

## 1. Result

| id | commit | state | regression test: base -> after | expected price |
|---|---|---|---|---|
| R2 | 6daff8b (+ window in 2c50cd4) | done | 4 FAIL (deadlock after 30 s; 5 of 6 saves inside `Load`; 8 requests instead of 1; fail-open) -> 5 PASS | prune, copy, repair packs: one request per range of at most 4 MiB instead of one per pack range; range buffer 4 MiB instead of 2 MiB. Restore unchanged |
| R3 | f3761a5 | done | process panic (N=6) and FAIL (N=56 derives a key) -> 2 PASS | none (three integer checks per key derivation) |
| R4 | 493924b | done: sampler removed, exact rule kept | FAIL (22 of 80 adversarial blobs larger than stock, up to 31.9x) -> PASS | one zstd pass per incompressible data blob of 512 KiB or more: 0.8 ms per MiB measured here, plus stock's transient allocation |
| R5 | dca600d | done | FAIL (5 connections: 2 downloads at a time on 1 and 2 CPUs, 4 on 4 CPUs) -> PASS (5, 5, 5) | none on local with 4 CPUs (still 4 workers); on REST with 4 CPUs 7 workers instead of 4 (stock 9) |
| R6 | d365b6e | done: buffering kept, retry queue removed | package did not build; with line 207 patched 9 FAIL -> 27 PASS | none expected: same batching for regular files |
| R7 | 5911fa2 | done | 2 FAIL -> 3 PASS (37,750 adversarial trees agree with stock's decoder, names and error text) | a byte loop over each node name: about 1 to 2 ms per traversal of 91k nodes |
| R8 | 2ade7be | done | FAIL (5 of 7 cases; stock PASS 7 of 7) -> PASS 7 of 7 | none without hard links; with them stock's lookup per file in pass 2 |
| R9 | d9215f3 | done | FAIL (1, 4, 20 keys: 2, 8, 40 key loads; stock 1, 4, 20) -> PASS | none on success; wrong password: one extra key listing, no derivation |
| M22 | 1c357d7 | reproduced, repaired in part | FAIL (251 to 317 bytes per directory; stock 21) -> PASS (43 to 71) | none (one map delete per directory) |
| R10 | 859dac1, 2c50cd4 | internal part done | with `DEBUG_LOG` set 2 league tests FAIL -> PASS; the rest is structural (see 2.10) | none intended |

"Base" results come from a git worktree of `base` with the new test file copied in. Where a test names a helper
that only exists after the repair, the base run used a three-line shim holding base's rule (R3 `checkScryptParams`
= what base's `parallelScryptKey` returns; R4 the constant's old name; R5 and R7 base's formula). Stock controls
(R8, M22) ran in a scratch copy of `trees\stock`. Both scratch trees are deleted again.

### Package tests on `repairs` (`go test -count=1 -p 2 -v`, top-level tests)

| package | base | repairs |
|---|---|---|
| `internal/repository` | 207 pass | 214 pass |
| `internal/repository/crypto` | 13 pass | 15 pass |
| `internal/repository/index` | 71 pass | 73 pass |
| `internal/repository/pack`, `hashing` | 16, 2 pass | 16, 2 pass |
| `internal/ui/termstatus` | build failed | 27 pass (stock's 11 included) |
| `internal/ui`, `ui/backup`, `ui/progress`, `ui/restore`, `ui/stats`, `ui/table` | not run | 7, 12, 4, 24, 2, 1 pass |
| `internal/data` | 55 pass | 58 pass |
| `internal/global` | 32 pass | 34 pass |
| `internal/restic`, `checker`, `walker` | 15 pass, not run, not run | 15, 9, 15 pass |
| `internal/restorer` | 58 pass, 9 fail | 60 pass, 9 fail |
| `internal/archiver` | 114 pass, 5 fail | 114 pass, 5 fail |

The 9 restorer failures (`TestCreateFile`, `TestRestorerOverwriteSpecial`, `TestRestorerLongPath`, the six
`*AttributeCombination*`) and the 5 archiver failures (`TestArchiverSaveTree`, `TestTestCreateFiles`,
`TestTestWalkFiles`, `TestTestEnsureFiles`, `TestTestEnsureSnapshot`) are the same tests by name on base, on
`repairs` and in stock on this machine (symlink privilege, EFS, long paths). No whole-repository run was made.

Other checks on the final tree: `gofmt -l` lists three files (two under `cmd/restic`, one identical to stock, see
2.10); `GOOS=linux go vet ./...` is clean (base: one error); `GOOS=windows go vet ./...` reports two "unkeyed
fields" lines in `internal/fs/node_windows_test.go`, a file identical to stock's; `go build ./...` succeeds for
linux/amd64 and linux/386 (`CGO_ENABLED=0`).

## 2. The repairs

### 2.1 R2: handler inside the backend `Load` callback (HIGH)

**Changed.**
- `internal/repository/stream_pack_released.go` (new, 23 lines): `loadBlobsFromPackReleased`. Each range is read
  into memory, the connection is returned, then the blobs are decoded and handed to the handler.
- `internal/repository/repack.go:124`, `repair_pack.go:102`: prune, copy and repair packs call it. Their handlers
  save or upload blobs.
- `internal/repository/stream_pack_coalesced.go`: lines 81-83, 126-133, 152-161, 175-177 (decode error) and
  223-249 (no data).
- `internal/repository/repository.go:1440-1443`: the contract of the exported `LoadBlobsFromPack` is written
  down. The restorer is its only caller in the tree.

**Why it is sound.**
- A handler that uses the backend now only runs in `streamPackWindowed` / `streamPackPartWithBuffer`, where the
  `Load` callback does nothing but take the hold budget and `io.ReadFull`. Restore keeps the coalesced path; its
  handler writes target files and makes no backend call.
- Decode error: inside the callback, an error after the window was read completely, that is neither a handler
  error, nor the "yield" for recovery, nor a cancellation, comes from the decoder (for example
  `invalid blob length`). It is stored, the request context is cancelled (the retry backend then neither replays
  nor opens its circuit breaker, the same mechanism the code already used for handler errors) and it is returned
  unchanged, as stock returns it. Read errors still return through `Load` and are retried.
- No data: the callback returns nil only after it has read the final window. `Load` returning nil without that
  means nothing was read for the open blobs. They are then decoded from zero bytes through the ordinary window
  code, which is what stock's freshly allocated buffer holds: each blob fails authentication, goes through
  `LoadBlob` recovery if there is one, or reaches the handler with its error.
- No hash or MAC check was touched.

**Window (changed once more in the R10 commit).** R2 first used the restore window (2 MiB). Stock's
`TestStreamPack` table expects one request for a 3 MiB range, so with 2 MiB it cannot be pointed at this path. The
released path is now the frozen `streamPack` itself: ranges of at most 4 MiB (`maxStreamPackChunkSize`), the
behaviour of S004 to S018. Stock's `TestStreamPack`, `TestBlobVerification` and `TestStreamPackFallback` therefore
test the production function of repack and repair again.

**Regression tests** (`internal/repository/stream_pack_released_test.go`, `stream_pack_coalesced_errors_test.go`):
```
go test -count=1 -p 2 -run 'TestRepairPacks(One|Two)Connection|TestRepackHandlerRunsOutsideLoad|TestStreamPackCoalesced(DecodeErrorIsNotRetried|FailsClosedWithoutConsumer)' -v ./internal/repository
```
Base: `TestRepairPacksOneConnection` FAIL (deadlock, 30 s), `TestRepackHandlerRunsOutsideLoad` FAIL ("5 of 6 saves
were requested inside the backend Load callback"), `...DecodeErrorIsNotRetried/decode_inside_Load` FAIL (8
requests, breaker open), `...FailsClosedWithoutConsumer/decode_inside_Load` FAIL (no callback, no error);
`TestRepairPacksTwoConnections` PASS. After: all PASS (one-connection repair in 0.2 s).

**Price.** Restore: none. Prune repack, `copy`, `repair packs`: one request per range of at most 4 MiB instead
of one per pack range (up to 32 MiB), and a range buffer of up to 4 MiB instead of 2 MiB windows (at most 2 MiB
more per repack worker). Local backend: one open, seek and close per range. REST: one round trip per range; the
inventory puts what larger repack requests gained at -0.17 to -0.21 s on remote prune, "inside the noise", which
bounds what can be given back. Phase: `forget-prune` on all four workloads, time and the RSS guard. If the 2 MiB
breaks the guard: `streamPackWindowed(..., restoreStreamWindow, true)` in `stream_pack_released.go` is the
memory-neutral variant (one line; `TestStreamPack` then counts 2 requests in one row).

**Not verified here.** The real binary with `-o local.connections=1`; REST and SFTP with 1 and 2 connections.

**Left as it is.** Restore still decodes and writes inside `Load` (allowed by the brief). The reviewers' item (b),
a slow restore target holding a remote response open, is unchanged and still unreproduced.

### 2.2 R3: parallel scrypt parameter validation (MEDIUM)

**Changed.** `internal/repository/crypto/scrypt_params.go` (new, 24 lines): `checkScryptParams` repeats the three
checks of `scrypt.Key` in `golang.org/x/crypto/scrypt` (same conditions, order and texts).
`parallel_scrypt.go:17-19` calls it before anything else.

**Why it is sound.** For every (N, r, p) the function returns x/crypto's error or nil, so `KDF` returns
`scrypt.Key: <stock's text>` where stock does, and the lanes only see parameters x/crypto accepts (N a power of
two, so the stride table indices are in range).

**Regression test** (`scrypt_params_test.go`):
```
go test -count=1 -p 2 -run 'TestParallelScryptRejectsLikeXCrypto|TestParallelScryptSmallParamsDifferential' -v ./internal/repository/crypto
```
Base: the test binary dies with `panic: slice bounds out of range [:512] with capacity 256` (N=6); the
differential alone FAILs ("N=56 r=2 p=4: parallelScryptKey error <nil>"). After: PASS; 25 rejected sets with the
same error text, 4,000 random small sets (3,853 same error, 147 same key).

**Price.** None.

**Not verified here.** The "too large" boundary values on a 32-bit build (the table is written for both widths):
`GOARCH=386 go test ./internal/repository/crypto` on the box. Large valid parameters cannot be compared against
x/crypto's verdict without running the derivation; the check is three copied lines.

### 2.3 R4: CompressionAuto sampler (MEDIUM)

**Choice: the sampler is removed; the exact rule stays.** A decision that looks at part of a blob cannot exclude
redundancy outside or between the parts it looks at (16 copies of one random 64 KiB block; a zero run between two
windows). A pass that can exclude it has to match across the whole 512 KiB window, which is the encoder's own
work on incompressible data. So no cheap sound sampler exists, and every blob is encoded again, as in stock.

**Changed.** `internal/repository/repository.go`: `autoDataBlobLooksCompressible`, `compressionSample` and its
pool are deleted (64 lines); lines 34-38 and 414-423 hold what remains. The league's
`repository_compression_sample_test.go` tested the deleted function and is removed.

**Bound guaranteed.** Under `--compression auto`, for every data blob:
1. what is stored is never larger than what stock stores (the output of `EncodeAll` with the same encoder options);
2. a blob is stored uncompressed if and only if it has at least 512 KiB and that output plus the four-byte length
   field of a compressed entry is not smaller than the blob. A blob the encoder shrinks by five bytes or more is
   stored compressed, byte for byte as stock stores it.

Ruling 2 (truly incompressible blobs may be stored raw) is kept in this exact form. Other modes, blobs below
512 KiB and tree blobs are as in stock.

**Regression test** (`repository_compression_auto_test.go`):
```
go test -count=1 -p 2 -run 'TestCompressionAutoNeverStoresMoreThanStock|TestCompressionModesOtherThanAutoAlwaysCompress' -v ./internal/repository
```
80 blobs: a random block of 3 KiB to 500 KiB repeated to 512 KiB, 1 MiB and 2 MiB; one stretch repeated once at
distances 3, 70, 300 and 511 KiB; zero runs of 600 bytes to 100 KiB at four positions; text of 6, 40 and 400 KiB
after a random prefix and inside random data; random, text and zero blobs; the two sizes around the threshold;
24 random mixtures. Base: FAIL, 22 blobs stored larger than stock, up to 31.9 times (the witness shape: 1,048,608
bytes against 65,693 with encryption overhead). After: PASS (15 uncompressed, 65 compressed, none larger).

**Price.** Measured here (Ryzen 5 5600, one thread, the repository's encoder): `EncodeAll` on random data runs at
1.2 to 1.3 GB/s for blobs of 1 MiB and more (0.8 ms per MiB) and allocates about four times the blob size while
it grows its output, which is stock's behaviour. The removed sampler cost 11 microseconds per blob. So the price
is about 0.8 s of CPU per GiB of incompressible data in blobs of 512 KiB or more, spread over the save workers,
plus transient garbage. Phase: the first `backup` of `large-files` (incompressible media files), and any backup
that stores new incompressible chunks; check both time and the RSS guard there. What remains of the league's gain
is that raw blobs are verified and restored without a decoder.

**Not verified here.** The size of the effect on the judge. Repository size, stock against candidate, on the four
corpora (REPAIRS.md asks for it): after this repair the candidate must never be larger.

### 2.4 R5: index-load workers (MEDIUM)

**Stock.** `connections + GOMAXPROCS` workers, each downloads and decodes; downloads are limited by the
connection semaphore, so all connections stay busy while every CPU decodes.

**Changed.** `internal/repository/index/index_load_workers.go` (new, 19 lines) and `index_parallel.go:19`:
`max(connections + min(GOMAXPROCS, 2), GOMAXPROCS)` instead of `min(connections+GOMAXPROCS, max(2, GOMAXPROCS))`.

| connections, CPUs | stock | base | repairs |
|---|---|---|---|
| 2, 1 | 3 | 2 | 3 |
| 2, 2 | 4 | 2 | 4 |
| 5, 1 | 6 | 2 | 6 |
| 5, 2 | 7 | 2 | 7 |
| 2, 4 (local, judge) | 6 | 4 | 4 |
| 5, 4 (REST, judge) | 9 | 4 | 7 |
| 2, 8 | 10 | 8 | 8 |

**Why it is sound.** On one and two CPUs the count is stock's, so the download behaviour is stock's by
construction. For every shape the count is at least `connections + 1` (every connection can download while an
index file is decoded), at most stock's, and it never falls when CPUs are taken away. Decoders beyond the
connections are limited to two because each worker holds an index plaintext; the number two is what keeps the
judge's local shape at its four workers. That is a judge-sized constant and is named as one in the code comment.

**Tests** (`index_load_workers_test.go`):
```
go test -count=1 -p 2 -run 'TestIndexLoadWorkers|TestForAllIndexesDownloadsOnEveryConnection' -v ./internal/repository/index
```
Base: FAIL ("GOMAXPROCS=1 connections=5: at most 2 index files were downloaded at the same time, stock downloads
5"; the same on 2 CPUs; 4 on 4 CPUs). After: PASS. The league's `TestIndexLoadAdmissionAndCancellation` asserted
the removed cap (at most `max(2, GOMAXPROCS)` loads with 32 connections); its bound is now the worker count.

**Price.** Local backend on 4 CPUs: none. REST with 5 connections on 4 CPUs: 7 workers instead of 4, so up to
three more index plaintexts are alive during the index load and five downloads run instead of four. Phase: the
index load of every phase on `remote` (time should be equal or better; check the RSS guard).

**Not verified here.** NEEDS-RUN 8 of the storage review (one and two cores, 500 index files behind latency).

### 2.5 R6: output queue (MEDIUM)

**Choice: the batching is kept, the retry queue is removed.** The unsound part was the queue, not the buffering.

**Changed.** `internal/ui/termstatus/status.go`: `runWithoutStatus` (377-495) and the new `writeBatch` (497-527);
the `queuedOutput` type and the retry logic are gone (91 lines added, 144 removed).
- Messages for a stream that is a regular file are still collected: at most 64 KiB, 100 microseconds idle, one
  stream at a time, written before a message for the other stream, a status update, `Flush` and shutdown. The
  `lineWriter` batching for `OutputWriter` (the `ls --json` path) is untouched.
- Every other writer gets stock's code: one `io.WriteString` per message, the error reported.
- Nothing is kept after a write. A failed collected write is reported; the rest of the batch is then written
  line by line, each line once, each failure reported.

**Why it is sound.** Memory is bounded by one batch. Stderr is written by the same loop directly; no stdout
record can stand in front of it. For writers that are not regular files the sequence of `Write` calls equals
stock's for any pattern of errors, partial writes and writes without progress (tested against a model of stock's
loop, 300 random sequences). For a regular file that fills up, the file content and the number of
`write failed` reports equal those of stock's one write per line (tested, 200 random rounds, through `Print` and
through `OutputWriter`).

**One difference from stock that remains, in the failure case only:** a single message that contains several
lines and fails part-way is given up as a whole by stock; here the line that failed is given up and the later
lines of that message are still attempted (and reported one by one if they fail).

**Tests.** `status_test.go:207` called `Flush` on `ui.Terminal`, which has no such method: the package did not
compile, so stock's eleven tests did not run either. It is fixed with a type assertion. Of the league's tests:
- `TestRunWithoutStatusKeepsCustomWriterCallsSeparate`: the test was right (one `Write` per message), the code
  was wrong. It passes unchanged.
- `TestRunWithoutStatusKeepsSuffixAfterZeroProgressRetry` and `TestRunWithoutStatusRetriesPartialBatchWrite`
  (`status_test.go`), `output_failure_order_test.go` (4 tests) and `status_output_retry_test.go` (2 tests) were
  wrong: they assert the retry queue itself, namely that a failed suffix is kept and written by a later call,
  that stderr waits behind an unfinished stdout record, and three attempts with `short write` reports for a
  writer that returns (0, nil). Stock makes one attempt and reports nothing in that case. They are removed.
- New: `status_failing_writer_test.go` (6 tests: the two witnesses in both output modes, the reverse direction,
  the comparison with stock's loop, the full-disk comparison, one partial write).
```
go test -count=1 -p 2 -v ./internal/ui/termstatus
```
Base: `[build failed]`. Base with line 207 patched: 9 FAIL (the 6 new tests and the 3 league tests the review
named). After: ok, 27 tests.

**Price.** None expected. Healthy output takes the same path as before with less bookkeeping. If the box shows
otherwise, the phases are `ls` (`--json` to a file) and every phase with verbose output redirected to a file.

**Not verified here.** A real `ENOSPC` (stdout on a full tmpfs, `/dev/full`) and a closed descriptor on Linux.

### 2.6 R7: tree decoder name handling (MEDIUM)

**Changed.** `internal/data/node_name_plain.go` (new, 27 lines) and `parent_tree.go:86-91`. `tree.go` needed no
change: `LoadTree` uses this decoder and has no fast path of its own.

**Why it is sound.** `strconv.Unquote` is skipped only for a name with no backslash, no double quote, no byte
below 0x20, and valid UTF-8. For such a name `strconv.Unquote("\"" + name + "\"")` returns the name itself: its
own fast path applies. Every other name gets stock's call, with stock's error wrapping. The UTF-8 condition is
stricter than the brief: `Unquote` replaces an invalid byte by U+FFFD. `encoding/json` never hands over such a
name, so this only removes a dependency on that.

**Regression tests** (`node_name_unquote_test.go`):
```
go test -count=1 -p 2 -run 'TestNodeNameIsPlain|TestTreeDecodersAgree' -v ./internal/data
```
Base: `TestNodeNameIsPlainMeansUnquoteIsIdentity` FAIL and `TestTreeDecodersAgreeWithStockOnAdversarialNames`
FAIL (a raw quote is accepted where stock returns `Unquote: invalid syntax`). After: PASS. 37,750 trees built
from 62 name pieces (JSON escapes that leave a raw quote, newline or control character; second-layer escapes
valid and broken; invalid UTF-8; lone surrogates; raw control bytes): 10,073 accepted and 27,677 rejected by
stock's decoder, and `LoadTree` and `TreeNodeReader` return the same names and the same error text for every
one. 5,000 trees written the way restic writes them read back to the original names.

**Price.** A byte loop over each node name instead of one `IndexByte`: 10 to 20 ns per node, 1 to 2 ms per
traversal of 91k nodes. Phases: `ls`, `restore` (both passes), `check`, `backup` with a parent, `forget-prune`.

**Not verified here.** NEEDS-RUN 10 of the read-side review (a foreign tree through `check`, `ls`, `restore`).

### 2.7 R8: hard links (MEDIUM)

**Changed.** `internal/restorer/restorer.go:680-686, 781-789, 871-881`. The first pass records the first path of
each inode that a node reports with more than one link (stock's index, with paths). The second pass uses stock's
condition for every regular file: in the index and not the indexed path itself. The second-pass rebuild is gone.

**Why it is sound.** It is stock's logic. A rebuild during the second pass cannot give stock's result, because a
path that records one link can come before the path that records more. The only addition is a count of indexed
inodes: with none, the lookup cannot succeed and is skipped. The `!= location` comparison the candidate had
dropped is back.

**Regression test** (`restorer_hardlink_linkcount_test.go`, 7 cases, also with different contents and directories):
```
go test -count=1 -p 2 -run TestRestoreHardlinksWithMixedLinkCounts -v ./internal/restorer
```
Stock (scratch copy): PASS 7 of 7. Base: FAIL in 5 cases. After: PASS 7 of 7.

**Price.** Without hard links: one integer comparison per file. With hard links: stock's locked map lookup per
regular file in the second pass, and the index stays alive during the file restore (one map entry with a string
header per hard-linked inode; the path bytes are the ones the restore's file list holds). Phase: `restore`.

**Not verified here.** ext4, APFS; restore manifests with hard links against the stock oracle.

### 2.8 R9: wrong non-interactive password (MEDIUM)

**Changed.** `internal/global/open_key_listing.go:16-20, 46-54, 98-101, 114-117, 149-152, 157-257`.

**How.** The second search stays, because it is what makes a stale staged listing harmless. It now takes its
ordinary key listing first, as far as a search can use it, and compares it with the keys the first search tried.
The first error stands, and no key is derived again, when all of this holds: the first search ended with
`ErrNoKeyFound` or `ErrMaxKeysReached`; none of its key loads failed; the listing is the same set of names (the
same sequence when there are more keys than a search tries, because the order then decides which are tried).
Otherwise the listing just taken is handed to the second search unchanged, entries then error.

**Why it is sound.** A key file is named by the hash of its content and the first search read each one
completely, so the same names mean the same bytes and the same rejection. Both end states used mean that every
tried key failed authentication; any other error, a failed load, or a changed listing is searched again as before.

**Tests.** `open_key_wrong_password_test.go`:
```
go test -count=1 -p 2 -run 'TestOpenKeyWrongPasswordCostsOneSearch|TestOpenKeyChangedListingIsSearchedAgain' -v ./internal/global
```
Base: FAIL ("1 keys: 2 key files loaded and tried, stock tries 1"; 4: 8; 20: 40). After: PASS, same count and
same error text as stock. A key added after the staged listing is still found. Two league tests asserted the
doubled work and are corrected (they fail on base with the correction and pass after):
`TestOpeningActualPathsMatchOrdinary/wrong` (40 key loads against 20) and `TestB01OpeningConfigFailureMatrix`
(2 against 1). The league's refresh tests pass unchanged.

**Price.** None when the password is right. Wrong password: one more key listing than stock, no derivation.

### 2.9 M22: per-directory state in restore (LOW; reproduced)

**What I found.** On snapshots of empty directories the live heap during a restore grows by 251 to 317 bytes per
additional directory on base and by 21 in stock (3,010 against 12,040 directories with names of 95 bytes; forced
GC sampling). 40,040 directories: +11.1 MiB on base, +1.7 MiB in stock. The finding is real.

**Changed.** `internal/restorer/restorer.go:758-767`: the first pass deletes a directory's `freshDirs` entry when
the traversal leaves it.

**Why it is sound.** The entry is read only for the directory itself and for the files directly in it, while the
traversal is inside. A missing entry means "not fresh", which is the path that verifies files as stock does.

**Not repaired.** `restoreTreeIDs` (32 bytes per directory, kept for the second-pass prefetch). Bounding it
needs a cap, which would be a new constant and a cliff. After the repair: 43 to 71 bytes per directory.

**Regression test** (`restorer_directory_state_test.go`; 21 s on NTFS, skipped with `-short`):
```
go test -count=1 -run TestRestoreKeepsNoPathPerDirectory -v ./internal/restorer
```
Base: FAIL (251 and 317 in two runs; limit 150). After: PASS (71, 43, 61). Stock: 21.

### 2.10 R10: hygiene (internal part)

Two commits, so that the formatting can be read apart from the rest.

| item | state |
|---|---|
| `internal/repository/repository.go.orig` | deleted |
| gofmt | `gofmt -w` on the 24 league files under `internal/` (6 non-test, 18 test). Only whitespace, statement separators and the order of two imports changed (checked by comparing the files with whitespace and semicolons removed). Left: `internal/backend/s3/s3_test.go`, identical to stock, where `gofmt -l` lists it too |
| `debug.IsEnabled()` in `index/index_decode_eager.go`, `index_load_ordinary_stream.go`, `prune.go` | removed. With `DEBUG_LOG` set, base fails two of the league's own tests (`TestOrdinaryIndexStreamCompleteLoad`, `TestOrdinaryIndexStreamLargeTerminalErrors`); after: pass (command below the table) |
| counters in `parent_cut_reader.go`, `parent_cut_long.go` | `accepted`, `replayed`, `scheduled`, `capacity` removed from the struct and from both read paths. The 32 test lines that read them take the same numbers from `parent_cut_probe_test.go`, a wrapper around the two chunkers; the assertions are unchanged and pass |
| `streamPack*` survivors | `streamPack` is the production stream of repack, copy and repair packs again (2.1). `streamPackPart` had no caller and is deleted. `TestStreamPackTableOnProductionPaths` runs stock's table (request counts, short first read, the three invalid uses, both repository versions) against the restore path as well: PASS on both |
| comment in `internal/restic/restore_hold_budget.go` | reworded: it states what was measured (code in this package moved the key derivation in the binary, about 3% or 6 ms either way, on one build and one machine) and no longer gives it as the purpose |

The debug-log run (the path must be absolute, tests run in the package directory):
```
DEBUG_LOG=/abs/path/debug.log go test -count=1 -p 2 -run 'Index|Prune|Decode|Stream' ./internal/repository
```
For R10 there is no test that fails on base apart from this run; the other items are checked by `gofmt -l`,
`grep` and the unchanged package tests.

**Listed, behaviour not changed** (what each is sized to, and what I would run on the box):

| where | what | sized to | box test |
|---|---|---|---|
| `repository/repository.go:1048` | `runtime.GC()` before the index load | the judge's peak-memory reading: collects the key derivation's scratch before the index grows | build without it; time per phase (expect a few ms less per process) and peak RSS on all workloads, 44 rounds. Keep it only if the RSS guard needs it |
| `restorer/ordinary_extra_worker.go:26` | 5 MiB hold per configured restore worker | the +2% memory guard on main and large-files | 3, 5, 8 MiB: restore time and RSS; a host with 64 CPUs |
| `restorer/ordinary_extra_worker.go:29-34` | third restore worker only when `GOMAXPROCS` exceeds the connection count | 4 CPUs and 2 local connections | `GOMAXPROCS` 1, 2, 4, 64 with `-o local.connections=1\|2\|4`: restore time, RSS, manifests |
| `restorer/restorer.go:489-490` | 4 metadata workers, 64 pending tasks | 4 CPUs, tmpfs target | 1 and 64 CPUs; a slow target (dm-delay); a target on NFS |
| `repository/restore_stream_budget.go:6` | 2 MiB restore window | the memory guard (stock's 32 MiB ranges: +133 MiB) | 1, 2, 4 MiB: restore time and RSS; restore over REST with latency |
| `restorer/tree_carry.go:15`, `tree_prefetch.go:13` | 512 KiB tree carry, 256 KiB prepared trees | main's directory shape | a snapshot with 2 million directories; one directory of 1 million files |
| `repository/index/index_load_workers.go` | two decoders beyond the connections (R5) | 2 local connections on 4 CPUs | NEEDS-RUN 8; RSS on `remote` |
| `repository/repository.go:1404` | 4 MiB range of the released pack stream (R2) | S004 to S018 prune memory | `forget-prune` time and RSS; the 2 MiB variant |

## 3. Files I could not touch, and the hunks they need

- `cmd/restic/check_structure_overlap.go`, `cmd/restic/backup_progress_publication_test.go`: `gofmt -w`. After
  that `gofmt -l` lists only stock's `s3_test.go`.
- `cmd/restic/lock.go:19-21`, `cmd/restic/cmd_backup.go:543`, `cmd/restic/cmd_check.go:332`: the remaining three
  `debug.IsEnabled()` switches. They sit in lines the R1 repair rewrites. Drop the condition there so that a run
  with the debug log does the same wait work. `IsEnabled` in `internal/debug/debug.go:39-42` then has no caller
  and can be deleted (stock has no such function); I left it because `cmd/restic` still uses it.
- R11, lock-wait half (the integration tests run with the wait at 0): test set-up under `cmd/restic`, not done.
  The stream half of R11 is done (2.10).
- `internal/repository/lock_file.go`: nothing needed by R2 to R10.

## 4. Not repaired, with the reason

- The reviewers' "should not ship" items that are not in the R10 row: league batch names in test files
  (`b06_`, `b17_`, `b18_`, `*_b23_test.go`); the "check-only" comments in `local/config.go`, `sema/backend.go`,
  `limiter_backend.go`; the help text of `local.connections`. Renames and comments, outside R2 to R10.
- R6: the difference for one multi-line message in the failure case (2.5).
- M22: the tree ID list (2.9).
- R2: the restore handler inside `Load` (2.1).

## 5. Linux-only checks owed

1. `go test -race -count=1` over `internal/repository/...`, `internal/ui/termstatus`, `internal/data`,
   `internal/restorer`, `internal/global`, `internal/archiver` with the new tests. Nothing here ran under the
   race detector. The new tests that use goroutines: R2 (repair, repack), R5, R6, the archiver probe.
2. `go test ./internal/...` with the tree's own files on linux/amd64 and `GOARCH=386`; the 9 restorer and 5
   archiver tests that fail here for lack of privileges must pass.
3. R2: `restic -o local.connections=1 repair packs <three ids>` with a 60 s timeout, before and after; prune and
   copy over REST and SFTP with 1 and 2 connections; `forget-prune` time and RSS on all four workloads.
4. R4: first `backup` time and RSS on `large-files`; repository size, stock against candidate, on the four corpora.
5. R5: `taskset -c 0` and `-c 0,1`, 500 index files behind 50 ms latency, against stock; RSS on `remote`.
6. R6: `restic ls latest > /dev/full`, stdout on a full tmpfs, `>&-`: stderr and exit code against stock;
   `ls --json` time to confirm the batching still pays.
7. R7: a tree with a raw quote in a node name through `check`, `ls` and `restore`, both binaries.
8. R8: `TestRestoreHardlinksWithMixedLinkCounts` on ext4; restore manifests with hard links against stock.
9. R9: a wrong `RESTIC_PASSWORD` with real scrypt parameters, time against stock (expected equal).
10. M22: peak RSS of a restore with 2 million directories, against stock.
11. R10: the `cmd/restic` integration tests with `DEBUG_LOG` set, once the three switches there are gone.
12. The 44-round confirmation and the held-out transfer on the repaired tree, per repair where a price is named.

## 6. Notes

- Commits: R2 to R9 one each, M22 one, R10 two (formatting apart). The 2 MiB to 4 MiB change of the R2 window is
  in the second R10 commit, because it is what reconnects stock's test table.
- Run logs of the last package runs are in `work\repairs\.testlogs` (untracked, excluded; logs above 1 MiB deleted).
- The repository's commit identity was not changed; each commit carries a `Co-Authored-By` trailer.
