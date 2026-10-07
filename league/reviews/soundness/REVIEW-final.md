# REVIEW-final: soundness of `repaired..final` (16 commits, tip 7461eb0)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Scope: `work\final`, `git diff repaired..final`: 54 files, of them 24 non-test files (+955 / -191 lines) and 30 test
files. Every non-test hunk was read; the two ports named as hardest (A16, A5) line by line together with the code
they call (`saveBlobAsync`, `WithBlobUploader`, `flush`, `streamPack`, the callers in `prune.go` and `cmd_copy.go`,
every use of a `crypto.Key`). Compared against `trees\stock` (ba802d42), `trees\S020`, `trees\S019-merge` and the
`repaired` branch.
Method: four witness test files written and run (three of them differential), 17 mutations of the ported code run
against the author's own tests, the tree's own tests of the touched packages run on amd64 and as 32-bit binaries.
Workstation only (Windows, go1.27.0): no box access, no API calls, $0. No race detector here (no cgo toolchain).
Date: 2026-10-07. Step log: `review\PROGRESS-final.md`. Scratch trees: `review\scratch-final\tree` (final) and
`review\scratch-final\repaired`, both git worktrees of `work\final`; no branch of `work\final` was changed.
Reviewed state: branch `final` at 7461eb0. The three commits of `final..final2` (tip 3509049: the restore-memory fix
F5 and two test commits) were reviewed afterwards: "Addendum: final..final2" at the end of this file. Verdict there:
SOUND, nothing above LOW. The eleven commits of `final2..final3` (tip b8cf439: nine fixes after an independent red
team, a restore change and the version string) are reviewed in "Addendum 2: final2..final3": nothing above LOW; the
contract of the lock-wait validation is stated there (B3).

## 1. Summary

**Nothing above LOW was found. No BLOCKER, no HIGH, no MEDIUM, nothing reproduced that fails.**

Verdicts by commit (16):

| Verdict | Count | Commits |
|---|---|---|
| UNSOUND or SUSPECT (BLOCKER / HIGH / MEDIUM) | 0 | |
| NEEDS-RUN as a verdict | 0 | (section 8 lists what this machine could not run) |
| SOUND-UNDER-CONTRACT | 3 | 0dc6aea (A5); 0740f00 and 5c3d49b (the two contracts REVIEW-s020 already stated, unchanged) |
| SOUND | 13 | 05a0be7, 04a2449, ed23471, 3e7c0df (R4 cost), cb4a2e9 (A3), 83c3ebf, b9ac55c, a1ce558 (A16), 61d8c0e (A14), 8a6a8d4 (R3 layout), 10d1df4 (F2), 3cddb8e (F1), 7461eb0 (F3) |
| LOW observations (hygiene, coverage, cost; not verdicts on a commit) | 6 | section 3, L1 to L6 |

The short version:

- **A16 (prune's repack) keeps the one property that matters.** Repack returns nil only after every save it handed
  to the uploader has called back without an error, on every path. Witnessed on 400 random failure schedules through
  the production entry point (171 nil returns followed by deleting the source packs and reloading every kept blob);
  on 300 schedules against a verbatim copy of the repaired worker (same result in all 300); and as `copy` between
  two repositories (150 schedules). Five mutations of the loop, among them "last pack not settled" and "save error
  ignored", are all caught by the author's own tests.
- **A5 (the key cache) cannot use the schedules of other key bytes.** 60,000 random steps on eight `Key` values
  that share cached pairs in every way the type allows (copies by value, changed bytes, JSON round trips into a
  prepared key) give stock's bytes, stock's errors and stock's panics at every step; so do Key values with
  different bytes sharing one pair across 12 goroutines. Six mutations (comparison dropped, half of it dropped, MAC
  and encryption schedule swapped) are all caught by the author's own tests.
- **F2: what is stored is what stock stores.** 1,092 stored entries read back from the pack files equal the output
  of an encoder built from stock's three options, in all five compression modes, both blob types, repository
  versions 1 and 2. The tree also reads what it now writes: large compressed entries that are longer than their
  data restore, verify and check without error.
- **The five carried S020 commits are the reviewed code.** Of the seven new files five equal `trees\S020` byte for
  byte, one up to gofmt, one has the R7 rule added; one dead parameter was removed. No file of R1, R2 or R7 is touched by any of the 16 commits.
- **The author's tests guard what they claim to guard**: 17 of 17 mutations fail at least one of the author's
  tests (section 6).

What this review could not do: race detector, Linux, real binaries, kill tests, a real remote backend (section 8).

## 2. Witness results

Files are in `review\witness-final\`. The prefix of a file name is the package it belongs in: copy it into that
package directory of the final tree and run the command from the tree root. Logs: `review\witness-final\logs\`.

| Witness file (copy to) | Test | Result on final |
|---|---|---|
| `repository_final_a16_repack_witness_test.go` (`internal/repository`) | `TestWitnessFinalA16RandomSchedules` | PASS. 400 schedules through `CopyBlobs` on real repositories: 214 nil returns, 186 errors. Of the nil returns 171 were followed by deletion of the source packs and a reload of every kept blob; 31 had a failed pack upload and 12 were cancelled after repack had returned, and in all 43 `WithBlobUploader` returned the error. Scheduled: 63 with cancellation at a random moment, 173 with saves reported failed, 107 with an unreadable pack; 1 to 4 workers |
| same file | `TestWitnessFinalA16DifferentialOrdered` | PASS. 300 schedules, the final loop against a verbatim copy of the repaired loop on two copies of one repository, one worker, same pack order: same result in all (168 both nil, 52 the same read error, 80 the same save error). The final loop read a pack beside unreported saves of the pack before it 378 times, never beside two; the repaired loop never |
| same file | `TestWitnessFinalA16ByteBudget` | PASS. Packs of 9 MiB, slow callbacks, one blob of 8 MiB + 1: at most 8,388,608 bytes pending in asynchronous saves (the budget), with 1 and with 3 workers |
| same file | `TestWitnessFinalA16CopyToOtherRepository` | PASS. 150 schedules with a second repository as destination: 75 nil returns, every kept blob loaded from the destination alone; 495 stored entries authenticate with the destination's key and not with the source's |
| `crypto_final_a5_cipher_preparation_witness_test.go` (`internal/repository/crypto`) | `TestWitnessFinalA5Differential` | PASS. 60,000 steps (21,504 changes of key bytes, 4,240 by-value copies, 4,244 JSON round trips) against stock's `Seal`, `Open` and poly1305 helpers copied verbatim: results, errors and panic values equal, also in place and for damaged, short and foreign input |
| same file | `TestWitnessFinalA5SharedPairConcurrent` | PASS. 12 goroutines, Key values with different bytes sharing one holder, 4,000 rounds each |
| same file | `TestWitnessFinalA5KnownAnswers` | PASS. The cr.yp.to Poly1305-AES vectors and restic's vector through the functions production now calls, prepared and unprepared, first and second use |
| same file | `TestWitnessFinalA5InvalidKeyAndJSON` | PASS. A prepared zero key panics and fails as stock and keeps no pair; JSON of a prepared key is stock's JSON |
| `repository_final_f2_stored_form_witness_test.go` (`internal/repository`) | `TestWitnessFinalF2StoredForm` | PASS. 1,092 stored entries (483 compressed, 609 raw) equal to stock's; 223 compressed entries are not smaller than their data and are stored compressed all the same; 267 went through the presized destination |
| `restorer_final_f2_incompressible_readback_witness_test.go` (`internal/restorer`) | `TestWitnessFinalF2RestoreAndCheckIncompressible` | PASS. Files of incompressible parts of 512 KiB to 8 MiB: restored and compared byte for byte, `VerifyFiles`, a second restore over the first, check with all data read; with and without the additional load slot; auto, max, off |
| `a16_mutations.py`, `a5_mutations.py` | mutation tools | section 6 |

All four test files also PASS as 32-bit binaries (`GOARCH=386`, `logs\final_windows_386.log` and
`final_windows_386_witnesses.log`). Counts that depend on timing (cancellation, overlaps) move by a few between runs;
the numbers above are those of `logs\a16_witness_final.log`.

Run on Windows from `review\scratch-final\tree`:

```
go test -run TestWitnessFinal -count=1 -p 2 -v ./internal/repository ./internal/repository/crypto ./internal/restorer
```

The tree's own tests, run once: `go test ./internal/repository/...` (5 packages), `./internal/data`,
`./internal/ui/backup`: all pass. `./internal/restorer`: 9 tests fail, the same 9 on `repaired` (L6). `cmd/restic`,
targeted (`TestPrune*`, `TestCopy*`, `TestCheck*`, the R1 witnesses and `TestLockWaitSchedules`): 22 pass,
`TestCopy` fails in its restore step, the same on `repaired` (L6).

One fact found while building the differential, for whoever writes the next one: two repositories built from the
same saves are not twins. Stock's packer manager spreads blobs over several packers and merges them at flush, so
the order of the blobs inside a pack file is not the order they were saved in. The witness therefore compares the
two loops on two copies of one repository's files.

## 3. Findings, most severe first

| # | Commit | Files:lines | Verdict, severity | Contract or scenario | Witness or deciding experiment |
|---|---|---|---|---|---|
| L1 | 0dc6aea A5 | `internal/repository/crypto/crypto.go:53-90`; `crypto_int_test.go:46-61` | LOW, coverage (R11 class) | `Seal`, `Open` and `OpenReader` no longer call `poly1305MAC`, `poly1305Verify` and `poly1305PrepareKey`; they call `preparedCipherPair.sum` and `verify` (`cipher_preparation.go:77-98`). The three old functions are left in production code with test callers only, and stock's `TestPoly1305` (the cr.yp.to known-answer vectors) still tests them: the published vectors no longer reach the MAC production computes. Production's path is correct (next column); the gap is that stock's test would not notice if it stopped being so. `TestCrypto` (restic's own vector, through `Open`) still covers it | `TestWitnessFinalA5KnownAnswers` runs the same vectors through `sum` / `verify`: PASS. Repair: point `TestPoly1305` at `preparedCiphers().sum / verify`, or move the three helpers into the test file as the reference |
| L2 | 8a6a8d4 R3 layout | `crypto/scrypt_params.go:7-22`; `crypto/parallel_scrypt.go:13-20` | LOW, keyed to the benchmark (the commit says so itself) | The split exists so that `parallelSalsaXOR` lands on one code alignment in one build. No behaviour changes. What it costs: the function that indexes past its tables on bad parameters is now callable without the checks, and "nothing else may call it" is a comment | Read: one caller each (`kdf.go:73` -> `parallelScryptKey` -> `parallelScryptLanes`); the checks equal `scrypt.Key` of x/crypto v0.54.0 line for line; the lanes body is S019-merge's (`diff`: identical). Mutation "wrapper without the checks": R3's test panics |
| L3 | 7461eb0 F3 | `internal/restorer/restorer.go:31-36, 671` | LOW, hygiene | A field of the production struct that production only writes; its one reader is a test. The map it points to stays reachable until the Restorer is dropped (it is empty by then) | Read: `freshDirs := make(...)` is still a new map for every `RestoreTo`, the field is assigned once and read nowhere (`grep freshDirs`): no state passes from one restore to the next, no goroutine reads it |
| L4 | 3e7c0df R4 cost | `internal/repository/compress_presized.go:19-25` | LOW, cost | For a blob of 1 MiB or more that compresses well the destination is now as large as the input (at most 8 MiB plus a few hundred bytes, garbage after the save); stock grows it to about the output size. Below 1 MiB stock allocates the input size itself. No functional effect | Read |
| L5 | a1ce558 A16 | `internal/repository/repack_completion_overlap.go:69-75` | LOW, behaviour on the error path only | After a save of pack N has failed, the worker may still read pack N+1 and hand its blobs to the uploader before it returns N's error (the repaired loop returned first). Nothing is deleted after an error; the extra blobs sit in a pack that is not indexed, as after any failed prune. The progress counter is one pack per worker behind while running, exact at the end | `TestWitnessFinalA16DifferentialOrdered`: same returned error in all 132 failing schedules |
| L6 | (environment) | `internal/restorer`, `cmd/restic` tests | LOW, not a regression | On this workstation 9 restorer tests (`TestCreateFile`, `TestRestorerOverwriteSpecial`, `TestRestorerLongPath`, six attribute tests) and `cmd/restic` `TestCopy` fail: symlink privilege not held, "Access is denied" restoring the timestamp of a directory. The same tests fail the same way on `repaired`. Consequence: `TestCopy`'s comparison after the copy did not run here | `review\scratch-final\logs\repaired_restorer_summary.log` against `final_restorer_summary.log`; `logs\repaired_cmd_testcopy.log`. The copy witness above covers the copy itself; section 8 item 5 runs `TestCopy` on Linux |
| C1 | 0dc6aea A5 | `crypto/cipher_preparation.go:46-74`; `crypto/crypto.go:36-42, 94, 265-270, 309-317`; `crypto/open_reader.go:30-34`; `repository/key.go:124-126` | **SOUND-UNDER-CONTRACT** | Contract: `Prepare` is called before the Key is used by a second goroutine (it writes a plain pointer field). True at both places a holder is made: `NewRandomKey` (in the constructor) and `openKey` (before the key is returned). Everything else needs no contract, see section 5 | `TestWitnessFinalA5*` PASS; mutations K1 to K6 caught |
| C2 | 0740f00 S020 index | `internal/repository/index/repack_routes.go`; `master_index.go:709-743` | **SOUND-UNDER-CONTRACT**, as in REVIEW-s020 finding 10 | Unchanged contract: after the routes are built no index entry is added for a selected pack and entries are never moved. A16 does not touch it: saves add entries for new packs only | The S020 reviewer's `listpacks_witness_test.go` is in the tree unchanged (`diff`: 0 lines) and passes |
| C3 | 5c3d49b S020 restore | `internal/data/tree_metadata.go:15-136`; `internal/restorer/restorer.go:833-845` | **SOUND-UNDER-CONTRACT**, as in REVIEW-s020 finding 8 | Unchanged contract: no visitor of the second pass reads `Node.Content`. Checked again in the final tree, where repair R8 changed the second pass: the only reads are `restorer.go:806` (first pass) and `:1201-1203` (`verifyFile`, reached from the second pass only for a regular file that is not a hard link, which the second pass never passes: `:878`, `:890`). The contract is now written at the call site (`:835-839`) | `tree_metadata_witness_test.go` (17,842 trees) and `restorer_hardlink_linkcount_test.go` pass |
| S1 | a1ce558 A16 | `internal/repository/repack_completion_overlap.go:48-84, 93-186`; `repack.go:112-119` | **SOUND** | Section 4 | `TestWitnessFinalA16*` PASS; mutations M1 to M5 caught |
| S2 | 10d1df4 F2 | `internal/repository/repository.go:399-415`; `compress_presized.go` | **SOUND** | `saveAndEncrypt` decides exactly as stock (`trees\stock\internal\repository\repository.go:388-398`): compressed unless version 1, an empty blob, or a data blob under `off`. The store-raw rule and its constant are gone (the build would fail on a leftover use). The encoder is built with stock's options (`getZstdEncoder`: level, CRC off, 512 KiB window) | `TestWitnessFinalF2StoredForm`, `TestWitnessFinalF2RestoreAndCheckIncompressible` PASS. Mutation "store-raw rule back": both of the author's tests fail |
| S3 | 3e7c0df R4 cost | `compress_presized.go:19-30` | **SOUND** | Claim: same bytes as `EncodeAll(data, nil)`. klauspost/compress v1.18.7 `encodeAll` read: a non-nil destination only switches off the "allocate `len(src)` up front below 1 MiB" branch; above one block (128 KiB; the presized path starts at 512 KiB) every block is encoded into the block's own buffer and appended, so the capacity of the destination is never read. `MaxEncodedSize` is int arithmetic that fits 32 bit for these sizes | same witness (267 presized entries); the author's `TestEncodeBlobEqualsEncodeAll` |
| S4 | cb4a2e9 A3 | `internal/repository/checker_extra_slot.go:14-21`; `checker.go:435-449` | **SOUND** | A pack is sent on the extra worker's channel (`continue`) or on the ordinary channel, never both, never neither; with no slot every pack goes to the ordinary workers and the extra goroutine ends when its channel is closed. The condition is the one restore already uses (`AdditionalRestoreWorker`), found through every wrapper (`sema`, `logger`, `retry`, `cache`, `limiter` all implement `Unwrap`) and consistent with what `sema.NewBackend` grants | The author's `TestCheckWorkersFollowConnectionsWithoutExtraSlot`; mutation "always true": fails |
| S5 | 61d8c0e A14 | `internal/ui/backup/text.go:93-98` | **SOUND** | One constructor (`:26-32`) gives the same verbosity to the printer and to the field; `VV` prints from 3 (`ui/progress/terminal.go:95-99`); everything below the new return is `Quote`, `FormatBytes` and `VV`. JSON progress is another type | The author's `TestCompleteItemPrintsWhatStockPrints`; mutation "below 4": fails |
| S6 | 05a0be7, 04a2449, ed23471 | `prune_pack_run.go`; `prune.go:203-210, 239-347`; `index/associated_count_validate.go`; `checker_pack_types.go`; `checker.go:115, 136-138, 163-169` | **SOUND** as carried | Section 5a | The S020 reviewer's witnesses in the tree, unchanged, pass |
| S7 | 83c3ebf, b9ac55c | `index/master_index.go:702-743`; `index/list_packs_bounded.go:55-57`; test files | **SOUND** | The stock loop that nothing called and its always-true `collect` parameter are gone; `ListPacks` and `ListPacksForRepack` take the paths REVIEW-s020 judged. The four witness files are the S020 reviewer's, byte for byte | `diff` of the four files: 0 lines |
| S8 | 3cddb8e F1 | `index/pack_membership_test.go:80-84` | **SOUND** (test only) | A run-time value instead of a constant that does not fit 32 bit | `GOARCH=386 go test ./internal/repository/index`: ok |
| S9 | 7461eb0 F3 | `restorer.go:31-36, 671`; `restorer_directory_state_test.go` | **SOUND** | See L3. The rewritten test still guards the M22 repair | Mutation "entry not deleted on leaveDir": the test reads 3,011 and 12,041 entries and fails |

## 4. A16, case by case

`runRepackCompletionOverlap` (`repack_completion_overlap.go:48-84`) keeps at most one unsettled pack (`previous`).
`settle` joins its saves (`finish` -> `pending.Wait()`), clears it and returns its result. `previous` is assigned at
one place (`:76`), directly after a `settle` that has cleared it: no pack's completion is ever dropped.

Why the join matters: `WithBlobUploader` flushes as soon as its callback returns nil, and `flush` does not wait for
asynchronous saves (`flushBlobSaver` waits on a group nothing adds to, in stock as well). A save still running at
that moment would put its blob into a packer after the flush, or dereference the cleared packer manager. Mutation
M2 (last pack not settled) shows exactly that: a nil pointer panic in `(*packerManager).SaveBlob`, called from
`saveBlobAsync` after the flush. Repack's own join is the
guard; A16 keeps it on every path.

| Case | What the code does | Evidence |
|---|---|---|
| A save of pack N fails after pack N+1 was read | `:71` `settle` returns N's save error; `:73` `drain` joins N+1's saves; the worker returns the error. Same error as the repaired loop, which returned before reading N+1 | Differential: 80 schedules, same error. The author's `TestRepackCompletionNextPackAndFailure` |
| The read of pack N+1 fails while N's saves are pending | `start` returns with `readErr`; `:71` joins N (counted if it succeeded); `:77-79` settles N+1 at once and returns its read error | Differential: 52 schedules, same error |
| Cancellation before or during a read | `streamPack` returns the context error; `finish` returns a save error if there is one, else the cancellation: never nil | Random: 63 schedules |
| Cancellation while waiting for the byte budget | `Acquire(wgCtx)` fails, the result is marked failed, the handler returns it | The author's `TestRepackExhaustedBudgetCancellationJoinsCallbacks` |
| Cancellation while a worker waits for the next pack with saves pending | The lister returns the context error and closes the queue; the worker settles what it holds. If those saves had all succeeded it returns nil; the lister's error makes `repack` return non-nil unless every pack had been handed out, in which case nil is the truth | Read (`repack.go:88-109`) |
| Cancellation after all saves reported success | Repack returns nil, correctly. The flush or the index rewrite that follows fails on the cancelled context; nothing is deleted | Random: 12 such schedules (nil return, the uploader then failed) |
| A pack is received and not processed | Only at `:65-67`, with an error returned. No path receives a pack, skips it and returns nil | Read; the author's model test (20,000 sequences) |
| Two or more workers | Each has its own `previous`; `errgroup.Wait` returns after all of them, so `repack` returns only after every worker has joined everything. A blob with copies in two packs is claimed under `keepMutex` by one worker | Random: 1 to 4 workers, every kept blob handed over exactly once |
| The last pack | `:83` `return settle()` | Mutation M2 caught by 2 unit tests, the prune test and a panic |
| A pack with nothing to keep | `streamPack` returns nil without a backend request; 0 descriptors; counted | Random: such packs in most schedules, counter equals the number of packs on every nil return |
| A pack larger than the byte budget | The handler waits in `Acquire` for callbacks of earlier saves (its own pack's or the predecessor's), which need nothing from this worker; a blob above 8 MiB is saved synchronously inside the handler | `TestWitnessFinalA16ByteBudget`: 8,388,608 bytes at most |
| More than 512 blobs in the two packs | `:64-68` settles the predecessor first; a single pack above 512 is settled right after its read (`:77`). For such packs the loop is the repaired loop | The author's `TestRepackCompletionOverlapDescriptorBound` |
| R2: handler outside the backend's `Load` | `startRepackPackCompletion` reads through `loadBlobsFromPackReleased` (`:103`), as before; `streamPackPartWithBuffer` only copies bytes inside `Load` (`repository.go:1647-1656`). A worker waiting for the budget with two packs open holds no connection | Read. `git diff repaired..final` touches none of `stream_pack_*.go`, `repair_pack.go` |
| Upload of a new pack fails | A save is reported done when the blob is in a packer, in stock as here: repack may return nil. `WithBlobUploader` returns the upload error | Random: 31 schedules, the uploader failed in all 31 |
| Kill at any point | Unchanged order: packs are deleted only after repack returned nil, the uploader flushed and the index was rewritten (`prune.go:630-671`) | Not run here: section 8 item 3 |

What the callers do with an error: `prune` (`prune.go:630-635`) returns a fatal error before it merges the repacked
packs into the removal list; `copy` (`cmd_copy.go:326-329`) returns a fatal error before the snapshot is saved;
`repair packs` does not call `repack` (it uses `loadBlobsFromPackReleased` itself) and is untouched.

Not found: a new wait-for edge. A worker that holds unreported saves waits only for the queue (fed by the lister,
which holds no lock while it sends) or for the budget (released by callbacks that run on the uploader's goroutines).

## 5. A5, case by case

`preparedCiphers` (`cipher_preparation.go:53-74`) returns the kept pair only if both recorded keys equal the Key's
keys at that call (`:56`: 32 + 32 bytes compared, K and R included). Otherwise it builds a pair from its OWN copy
of the bytes (`:59-69`), so a pair's schedules always belong to the bytes recorded in it. The pair is never written
after `Store`. The MAC's `r` is taken from the pair (`:80`), which the comparison has just tied to the Key.

| Question | Answer | Evidence |
|---|---|---|
| A `Key` copied by value | Production copies none: `crypto.Key` is passed as a pointer everywhere (`grep`: the only values are `testKey` and `TestInjectKey` in test code). A copy shares the holder; if its bytes differ the two keys replace each other's pair and each call still compares | Differential: 4,240 copies; concurrent test: 12 Key values on one holder |
| Key bytes changed after first use | Tests do; production does not. The fields are written in `NewRandomKey`, `KDF` and `json.Unmarshal` into a fresh Key (`key.go:112-113`), each before the key's first use and, in `openKey`, before `Prepare` | Differential: 21,504 changes, 4,244 JSON round trips into prepared keys |
| Keys of two repositories in one process (`copy`) | Two `openKey` calls, two Keys, two holders | `TestWitnessFinalA16CopyToOtherRepository`: 495 entries authenticate with the destination's key only |
| Concurrent first use | Both goroutines build a pair, the last `Store` wins, each uses the pair it built | Concurrent witness; race detector: section 8 item 1 |
| The zero Key | `Valid()` is tested before `preparedCiphers` in `Seal` (`crypto.go:247`), `Open` (`:287`) and `OpenReader` (`open_reader.go:16`): panic, error, error, as stock; no pair is kept | `TestWitnessFinalA5InvalidKeyAndJSON` |
| MAC schedule and encryption schedule swapped or shared | Two fields, filled from two different keys of different length (AES-256 from `EncryptionKey`, AES-128 from `MACKey.K`), each used at one place | Known answers PASS; mutation K5 (swapped) fails 3 of the author's tests and stock's `TestCrypto` |
| A check removed or reordered | No. `Seal`: valid key, no additional data, nonce length, nonce not zero, then the ciphers. `Open`: valid key, nonce length, nonce not zero, length, MAC, then decryption. The only change of order is that both AES keys are expanded before the MAC is verified; expansion of a 16 or 32 byte key cannot fail | `diff` against stock: 5 hunks; differential on errors and panic values |
| Only `aes.NewCipher(key)` is cached | Yes, plus a copy of the 64 key bytes. The CTR stream, the Poly1305 key and the tag are built per call from the nonce (`:77-98`, `crypto.go:266, 316`). `cipher.NewCTR` copies the block (go1.24 and later) or only reads it | Read |
| An unprepared key | Same result; one small allocation more per call. Concerns the user key: two calls per process | Differential: the "never prepared" key |
| Stored form | The field is unexported: the JSON of a key is unchanged | `TestWitnessFinalA5InvalidKeyAndJSON` |

## 5a. The five carried S020 commits

| Question | Answer |
|---|---|
| Carried faithfully? | `prune_pack_run.go`, `checker_pack_types.go`, `index/associated_count_validate.go`, `index/repack_fallback.go`, `index/repack_ordinals.go`: identical to `trees\S020`. `index/repack_routes.go`: gofmt only. `index/list_packs_bounded.go`, `index/master_index.go`: the `collect` parameter (always true in S020) and the unreachable stock loop removed. `data/tree_metadata.go`: one condition, see R7 below |
| `prune.go` | Equals S020's except `:124-126`, where repair R10 removed the debug switch around the header-size preparation. The three S020 hunks (`:203-210`, `:239-339`, `:347-349`) are S020's text; `decidePackAction` and `Execute` are not touched |
| `master_index.go` | `ListPacks` -> selective fallback; `ListPacksForRepack` -> bounded collector, routes, fallback: S020's order with `collect` fixed to true, which is what S020's only caller passed |
| `checker.go` | S020's three pack-type hunks; the check-wait hooks of S020 (`waitPack`, `checkWaitPackOffer`) are not carried, as REVIEW-s020 asked. With the R1 repair an incremental load is possible: then the master is not empty, `freshCheckPackTypes` returns nil and the stock walk runs (`:163-169`), exactly as on `repaired` |
| `restorer.go` | One call changed (`:841`), plus F3's field. See C3 |
| R1 validation intact? | Yes by construction: no file under `cmd/` and not `internal/repository/lock_file.go` is in the diff. Run on the final tree: `TestWitnessLockWaitStale*` (3 of 3), `TestWitnessLockDwell*` (3 of 3), `TestLockWaitSchedules`: PASS |
| R2 pack stream intact? | Yes: `stream_pack_coalesced.go`, `stream_pack_released.go`, `repair_pack.go` are not in the diff; A16 reads through the released path |
| R7 strict names intact, and added where S020 lacked it? | `parent_tree.go`, `tree.go`, `node_name_plain.go` are not in the diff. `tree_metadata.go:122-129` now skips `strconv.Unquote` only for a plain name (`nodeNameIsPlain`: no backslash, quote or control byte, valid UTF-8). The S020 reviewer's `TestWitnessS020MetadataTreeNameLeniency`, which failed on S020, passes; with the S020 condition put back it fails again, and so does the author's `TestMetadataTreeAgreesWithStockOnAdversarialNames` |
| The dropped S020 mechanisms | Absent: no `check_wait*`, `backup_compression*`, `prepared_range*`, `restore_wait*`, `restore_snapshot_wait*`, `compression_preparation*` file, no `debug.IsEnabled`, no `.orig` file |

## 6. Do the author's tests catch their own failures?

Each mutation was applied to the scratch copy, the package's tests run, the file restored. "Caught" names the
author's tests that failed; the witnesses of this review were in the package as well and are not counted.

| Port | Mutation | Caught by the author's tests |
|---|---|---|
| A16 | M1: `current.drain()` removed | yes: `TestRepackCompletionOverlapRandomSequences`, `...HeldPredecessor`, `TestRepackOverlapFailureJoinsHeldSuccessor` |
| A16 | M2: last pack not settled (`return nil`) | yes: `...RandomSequences`, `...ImmediateErrorAndEOF`, `TestPruneRepackUncompressedComesToRest`, then a nil pointer panic in `(*packerManager).SaveBlob` |
| A16 | M3: `finish` does not wait | yes: the same nil pointer panic, in the first repack test that saves |
| A16 | M4: save error ignored in `finish` | yes: `TestRepackCompletionNextPackAndFailure` (one test) |
| A16 | M5: predecessor replaced without being settled | yes: three unit tests, then a hang until the test timeout |
| A5 | K1: key-bytes comparison removed | yes: `TestPreparedKeyFollowsItsKeyBytes` |
| A5 | K2: only the encryption key compared | yes: same test |
| A5 | K3: only the MAC key compared | yes: same test |
| A5 | K4: R of the MAC key not compared | yes: same test |
| A5 | K5: the MAC uses the encryption key's schedule | yes: `TestPreparedKeyEqualsStock`, `...FollowsItsKeyBytes`, `...ConcurrentUse`, stock's `TestCrypto` |
| A5 | K6: the pair is never kept | yes: `TestPreparedKeyFollowsItsKeyBytes` (it asserts reuse) |
| F2 | store-raw rule back in `saveAndEncrypt` | yes: `TestCompressionAutoStoresWhatStockStores`, `TestPruneRepackUncompressedComesToRest` |
| A3 | `extraCheckWorker` always true | yes: `TestCheckWorkersFollowConnectionsWithoutExtraSlot`, `TestExtraCheckTaskNeedsSlot` |
| A14 | return below verbosity 4 | yes: `TestCompleteItemPrintsWhatStockPrints` |
| R3 | wrapper without the checks | yes: panic (slice bounds) in R3's own test |
| F3 | M22 repair removed | yes: `TestRestoreKeepsNoPathPerDirectory` (3,011 and 12,041) |
| S020 R7 | S020's backslash-only rule back in `tree_metadata.go` | yes: `TestMetadataTreeAgreesWithStockOnAdversarialNames` |

17 mutations (logs: `a16_mutations.log`, `a5_mutations.log`,
`f2_mutation.log`, `a3_a14_r3_mutations.log`, `f3_mutation.log`, `r7_metadata_mutation.log`). Thinnest guard: M4,
caught by a single test of the tree.

## 7. Recommendation per port

| Port | Recommendation | Reason |
|---|---|---|
| S020 prune / index / check pack types (05a0be7, 04a2449, 0740f00, ed23471, 83c3ebf, b9ac55c) | **KEEP** | The reviewed code, carried faithfully |
| S020 metadata tree with R7 (5c3d49b) | **KEEP** | The repair REVIEW-s020 asked for is in and tested |
| R4 cost (3e7c0df) | **KEEP** | Same bytes; one allocation |
| A3 (cb4a2e9) | **KEEP** | Removes one check worker where it had no connection of its own; no pack is skipped |
| A16 (a1ce558) | **KEEP** | Section 4 |
| A14 (61d8c0e) | **KEEP** | No output changes |
| A5 (0dc6aea) | **KEEP WITH THIS REPAIR** (test code only, not a condition for shipping) | L1: make `TestPoly1305` test what production computes, or move the three unused helpers into the test file. `TestWitnessFinalA5KnownAnswers` can be taken as it is |
| R3 layout (8a6a8d4) | **KEEP** or drop, no soundness difference | L2: it serves one build's code alignment. If the measured 6 ms is wanted, keep; nothing is lost by putting the checks back inside the lanes function |
| F2 (10d1df4) | **KEEP** | Stored form is stock's |
| F1 (3cddb8e), F3 (7461eb0) | **KEEP** | Test repairs; L3 is cosmetic |

## 8. NEEDS-RUN on the Linux box

None of these decides an open verdict; they are what this machine could not do. Set up once:

```
T=/path/to/final-tree; W=/path/to/review/witness-final
cp $W/repository_final_a16_repack_witness_test.go $W/repository_final_f2_stored_form_witness_test.go $T/internal/repository/
cp $W/crypto_final_a5_cipher_preparation_witness_test.go $T/internal/repository/crypto/
cp $W/restorer_final_f2_incompressible_readback_witness_test.go $T/internal/restorer/
cd $T
```

1. **Race detector over the new concurrent code** (expected: no report).
   ```
   CGO_ENABLED=1 go test -race -count=1 -run 'Repack|TestWitnessFinalA16|TestCheckWorkers|TestCheckExtra|TestPrune' ./internal/repository
   CGO_ENABLED=1 go test -race -count=1 ./internal/repository/crypto
   CGO_ENABLED=1 go test -race -count=1 -run 'TestWitnessFinalF2|TestRestoreKeepsNoPathPerDirectory|Hardlink' ./internal/restorer
   ```
2. **The witnesses on linux/amd64 and as 32-bit** (here: Windows amd64 and windows/386).
   ```
   go test -run TestWitnessFinal -count=1 -p 2 -v ./internal/repository ./internal/repository/crypto ./internal/restorer
   GOARCH=386 go test -run TestWitnessFinal -count=1 -p 2 ./internal/repository ./internal/repository/crypto ./internal/restorer
   ```
3. **Kill during prune's repack, real binaries** (expected: stock `check --read-data` passes after every kill).
   ```
   export RESTIC_PASSWORD=x; S=./restic-stock; F=./restic-final
   rm -rf /tmp/kr /tmp/ksrc; mkdir -p /tmp/ksrc; for i in $(seq 1 600); do head -c 300K /dev/urandom > /tmp/ksrc/f$i; done
   $S init -r /tmp/kr && $F -r /tmp/kr backup /tmp/ksrc && rm /tmp/ksrc/f1* /tmp/ksrc/f2* && $F -r /tmp/kr backup /tmp/ksrc && $F -r /tmp/kr forget --keep-last 1
   for d in 0.02 0.05 0.1 0.2 0.4 0.8 1.5; do
     rm -rf /tmp/kr.$d; cp -a /tmp/kr /tmp/kr.$d
     ( $F -r /tmp/kr.$d prune --max-unused 0 & p=$!; sleep $d; kill -9 $p ) 2>/dev/null
     $S -r /tmp/kr.$d unlock >/dev/null; $S -r /tmp/kr.$d check --read-data >/dev/null 2>&1 && echo "ok after kill at $d" || echo "FAIL after kill at $d"
     $F -r /tmp/kr.$d prune --max-unused 0 >/dev/null && $S -r /tmp/kr.$d check --read-data >/dev/null 2>&1 && echo "ok after second prune ($d)" || echo "FAIL after second prune ($d)"
   done
   ```
   The same with `-o local.connections=2` (one repack worker) and with SIGINT instead of `kill -9`.
4. **Backend failure during repack, real binaries** (expected: prune fails, no pack deleted, stock check passes).
   As a user that is not root:
   ```
   rm -rf /tmp/kf; cp -a /tmp/kr /tmp/kf
   ( $F -r /tmp/kf prune --max-unused 0 & p=$!; sleep 0.15; chmod -R a-w /tmp/kf/data; wait $p; echo "prune exit $?" )
   chmod -R u+w /tmp/kf/data; $S -r /tmp/kf unlock; $S -r /tmp/kf check --read-data
   ```
   And over REST with 2 connections, the server killed mid-run:
   `rest-server --path /tmp/kf2 --no-auth & ... $F -r rest:http://127.0.0.1:8000/ -o rest.connections=2 prune --max-unused 0`.
5. **`copy` end to end** (`TestCopy` could not finish on Windows; expected PASS).
   ```
   go test -run 'TestCopy' -count=1 -v ./cmd/restic
   $S init -r /tmp/kc && $F -r /tmp/kc copy --from-repo /tmp/kr --from-password-file <(echo x) && $S -r /tmp/kc check --read-data
   diff <($S -r /tmp/kr ls latest | tail -n +2) <($S -r /tmp/kc ls latest | tail -n +2) && echo "same listing"
   rm -rf /tmp/kc.out; $S -r /tmp/kc restore latest --target /tmp/kc.out && diff -r /tmp/ksrc /tmp/kc.out/tmp/ksrc && echo "same content"
   ```
6. **The release checks on `final` again**, in particular C11 (prune differential and `--repack-uncompressed` twice:
   expected `to repack: 0 blobs` on the second run) and the cross-read matrix (stock `check --read-data` and
   `stats --mode raw-data` on repositories written by `final` in every compression mode; expected sizes equal to
   stock's): `python3 c11_prune_differential.py $T final`.
7. **A3 with real backends** (expected: never more pack requests in flight than connections; with the default local
   backend one more).
   ```
   go test -run 'TestCheckWorkers|TestCheckExtraWorker|TestExtraCheckTask' -count=1 -v ./internal/repository
   rest-server --path /tmp/kr-rest --no-auth --log /tmp/rest.log &     # /tmp/kr-rest: a copy of /tmp/kr
   $F -r rest:http://127.0.0.1:8000/ -o rest.connections=2 check --read-data
   ```
   In `/tmp/rest.log` no more than two requests for `/data/` may overlap. Then the same check with stock, for the
   run time.
8. **A14 with real binaries** (expected: no difference but times and IDs).
   ```
   for v in "" -v -vv -vvv; do $S -r /tmp/ka backup $v /tmp/ksrc > /tmp/a$v.txt 2>&1; $F -r /tmp/kb backup $v /tmp/ksrc > /tmp/b$v.txt 2>&1; diff <(sed -E 's/[0-9.]+s|[0-9a-f]{8}|[0-9]+:[0-9]+//g' /tmp/a$v.txt | sort) <(sed -E 's/[0-9.]+s|[0-9a-f]{8}|[0-9]+:[0-9]+//g' /tmp/b$v.txt | sort) && echo "same output $v"; done
   ```
   (`/tmp/ka` and `/tmp/kb`: two fresh repositories initialised by stock.)
9. **The 9 restorer tests and `TestCopy`** that fail on this workstation for want of a privilege: `go test ./internal/restorer`
   on Linux, expected PASS.

## 9. Should not ship (the 16 commits)

Nothing in these commits has to be held back for soundness. Hygiene, in order of worth:

- **L1**: three functions in `crypto/crypto.go:53-90` that only tests call, and a stock test that now tests them
  instead of production's MAC path.
- **L3**: `Restorer.freshDirs` exists for a test.
- **L2**: the comment in `crypto/scrypt_params.go:7-16` describes one build's binary layout; REPAIRS.md R10 lists a
  comment of the same kind in `restic/restore_hold_budget.go` (still there).
- Outside this diff, still in the tree: 20 test files with league season names (`_b01_`, `_b18_`, `_b23_`,
  for example `internal/archiver/chunker_exact_b23_test.go`). The diff renamed the one it touched
  (`repack_routes_settlement_b26_test.go`).
- gofmt and `go vet` are clean for every file of the diff.

## 10. Limits of this review

- No race detector. The concurrency verdicts for A16 and A5 rest on reading, on the schedules above and on the
  structure (one WaitGroup per pack, an immutable pair behind an atomic pointer). Section 8 item 1 is the check.
- Windows only, memory backend only. No real binary was run against a real repository, nothing was killed, no
  remote backend was used. The witnesses inject failures at the uploader's callback, at the backend's `Load` and
  `Save` and through the context.
- Code outside the 16 commits was read only where the ports call it (`saveBlobAsync`, `WithBlobUploader`, `flush`,
  `streamPack`, `MasterIndex.Load`, the sema and cache wrappers). It was not reviewed again.
- The S020 code was not reviewed a second time: the question here was whether it was carried faithfully and whether
  the merge changed it. REVIEW-s020 stands for its content.
- The measured gains the commit messages give were not checked.
- This review covers the branch `final` at 7461eb0 and nothing later. When it ended, the main work tree of
  `work\final` stood on a branch `final2` (same commit) with uncommitted changes that are not this reviewer's:
  `internal/restorer/restorer.go` modified, `internal/restorer/tree_id_list.go` and its test new. They were not read
  then. They are commit 9ca10f2 now and are reviewed in the addendum at the end of this file (SOUND).

## Addendum: final..final2 (3 commits, tip 3509049)

Reviewed after the report above, same rules, same workstation (2026-10-07). Scratch tree:
`review\scratch-final\tree2`, a git worktree of `work\final` at `final2`; no branch was changed.
`git diff --stat final..final2 -- . ':!*_test.go'` shows two files, both of 9ca10f2:
`internal/restorer/tree_id_list.go` (new, 47 lines) and `internal/restorer/restorer.go` (23 lines).

(Later: commit d4f7e81 on final2 raised the bound from 65,536 to 524,288 tree IDs, 16 MiB. The numbers below are
those of the commit reviewed here; see the head of Addendum 2.)

### A1. Verdicts

| Commit | Files:lines | Verdict | Basis |
|---|---|---|---|
| 9ca10f2 "F5": the first pass of a restore records at most 65,536 tree IDs for the second pass's prefetch | `internal/restorer/tree_id_list.go:21-47`; `restorer.go:38-44, 740-751, 848` | **SOUND** | A2 to A4. Nothing reproduced that fails |
| 9ed8618 (A5 test) | `internal/repository/crypto/cipher_preparation_witness_test.go` | test only | The file is this review's witness, byte for byte (`diff`: 0 lines); the package passes |
| 3509049 (A16 tests) | `internal/repository/repack_completion_save_error_test.go` | test only | Three tests, all pass. With mutation M4 of section 6 (save error ignored in `finish`) the first and the third fail beside the existing one, as its message says: that guard is no longer a single test |

Recommendation: **KEEP** all three. Section 10's last item is settled by this addendum.

### A2. Why a restore across the bound is the same restore

`tree_prefetch.go` is not in the diff. Its producer sends the trees of its list in order through one channel and
closes the channel when the list ends (`tree_prefetch.go:49-69`). The second pass's `Load` takes the k-th item for
its k-th request; when the channel is closed it sets `fallback` and from then on loads every tree itself, with the
same loader and its own context (`:143-145, 155-158`). That branch is older than this commit: it already served a
list that ended early behind a tree that failed to load, and a second pass that visits more trees than the first.
The commit changes one thing: where the list ends. Crossing the bound is that old branch, entered at tree 65,536.

What the fallback hands out is what the prefetch would have handed out: the same loader (`LoadMetadataTree`), so
the same nodes, the same error behind the same node, the same load error, at the moment the traversal asks. The
only trees that come from somewhere else are the carried ones, and the carry never reaches the bound: its budget
is cumulative, 512 KiB at 256 bytes or more per tree, so it ends within the first 2,048 trees
(`tree_carry.go:15-16, 40-43`).

### A3. The questions

| Question | Answer | Evidence |
|---|---|---|
| Content, metadata, hard links (repair R8), order of operations, order of reported errors | The same | `TestWitnessFinal2RestoreAcrossBoundDifferential`: 300 random snapshots, 2,267 real restores. For each snapshot the restore with the production bound (the list holds every tree) against restores with the bound lowered to 1, to one below, exactly at and one above the number of trees, and to two random values. Compared as ONE ordered trace: every selection-filter call of both passes, every reported error, every warning, every progress event of the traversal (files added, skipped, directories restored, deletions); then the returned error, the file count, the progress of file contents and every entry of the target (kind, permissions, size, content, whether its time is the snapshot's, the path it is hard-linked to). Equal in all. The references hold 231 paths restored as hard links (groups with mixed link counts among them), 5,239 entries with the snapshot's time, 544 deletions (`--delete` with unexpected files), 170 skipped files (four overwrite modes with files already there) |
| A tree that fails to load | The same error at the same place, whether the tree is on the list or behind it; the restore continues or stops as below the bound | Same test: 185 scenarios with one to three trees failing to load or decoding only k nodes in the second pass (363 reported errors in the references), 104 of them stopping at the first, second or third error; trees before, at and behind every bound; 185 scenarios with one tree ID in several directories |
| Cancellation | The same: `Load` tests the context before it looks at the queue or loads (`:140-142`), on both paths | Same test: 52 scenarios cancelled at a random node of the second pass. In-memory test: 3,102 cancellations |
| A filter that answers differently in the second pass | The same: on the list the mismatch branch (`:159-167`), behind it the fallback; both load the tree asked for | Same test: 46 scenarios. In-memory test: 3,108 requests for an unexpected tree |
| Races and deadlocks at the hand-over | None found, and none possible in the channel protocol: one producer, one consumer, one channel, closed by the producer after its last send. A receive on a closed channel reports "closed" only after every sent item was received, so a request that arrives while the queue is being closed either gets the last tree or finds the queue closed, nothing between. `fallback` is touched by the consumer alone. The producer is joined (`Close`, deferred last, so run first) before the carry is closed and before the list is cleared (`restorer.go:746, 757, 859`) | `TestWitnessFinal2PrefetchHandOver`: 4,000 random sequences of trees (ordinary, failing, damaged behind k nodes, too large to prepare, repeated IDs), each through a prefetch whose list holds the first b trees for b = 0, 1, n-1, n, n+1 and a random value, loader and consumer delayed at random: 50,402 requests behind the end of the list, 11,962 of them the first one behind it, 25,873 requests on the list after a failed tree. Every tree as the loader alone gives it; a tree behind the list is loaded once per request, by the consumer; the producer loads nothing that is not on its list; `Close` (twice) returns; a 20 s watchdog never fired. No race detector here (A5 item 2) |
| The tree at index 65,535, at 65,536, a restore with exactly 65,536 directories | 65,535 is the last item of the queue. 65,536 is the first request that finds it closed. With exactly the bound's number of trees (the root counts as one) the list holds every tree and no request comes behind it: the restore is the one before this commit | The bounds n-1, n and n+1 in both tests, with n the number of trees. The real number was not run (A5 item 4) |
| Can anything still grow with the number of directories | Not in the restorer, by reading. The list: at most 65,536 IDs, 2 MiB, and never capacity beyond that (`tree_id_list.go:35-47`; the plain `append` it replaces would round up). The carry: 512 KiB of estimates. The prefetch: two prepared trees of at most 256 KiB. The first pass's look-ahead: two batches of 128 nodes. `freshDirs`: the depth of the tree (M22). What grows with the number of FILES is stock's (planned files, the file list, the hard-link index) | Read; the author's `TestRestoreTreeIDListNeverExceedsItsLimit` (2,020,200 additions: 65,536 IDs, capacity 65,536). Peak memory was not measured here: A5 item 1 |
| Do the two new tests catch a removed bound and a wrong hand-over | Yes, 7 of 7 | A4 |

### A4. Mutations (`witness-final\final2_mutations.py`, `logs\final2_mutations.log`)

| Mutation | Caught by the author's tests |
|---|---|
| X1: bound removed (`add` appends always) | yes: `TestRestoreTreeIDListNeverExceedsItsLimit`, `TestRestoreKeepsBoundedTreeIDList` |
| X2: length bounded, capacity not (plain `append`) | yes: the same two |
| X6: list not cleared when `RestoreTo` returns | yes: `TestRestoreKeepsBoundedTreeIDList` |
| X3: the first tree behind the list handed out empty | yes: `TestRestoreAcrossTreeIDBoundEqualsRestoreBelowIt` |
| X4: every tree behind the list handed out empty | yes: same test |
| X5: an error instead of a load behind the list | yes: same test |
| X7: the first tree behind the list is the tree asked for before it | yes: same test |

The witness of this review fails under all seven as well. The older prefetch unit tests (`TestRestoreTreePrefetch*`)
pass under X3 to X7: before this commit nothing tested a list shorter than the pass.

### A5. Observations (LOW) and what is left for the box

- **N1, performance only.** Behind 65,536 directories the second pass loads each tree itself when it reaches it,
  one at a time, as stock does for every tree. The read-ahead ends there; nothing else changes.
- **N2, hygiene.** Two more fields of the production struct exist for tests: the list as a field and
  `prefetchTreeLimit`, the bound a test can lower (`restorer.go:38-44`). Same kind as L3.
- **N3, environment.** `./internal/restorer` on `final2`, run whole: the same 9 tests fail as on `final` and on
  `repaired` (L6: symlink privilege, attribute tests); everything else passes, the three tests of this commit
  included. The author's list test and both witnesses also pass as 32-bit binaries
  (`logs\final2_other_runs.log`).

NEEDS-RUN, none of them deciding an open verdict:

1. **F5 again on `final2`**, the measurement this fix exists for: 2,020,200 directories, expected about stock's
   62 MiB plus the 2 MiB of the list (the harness and its command are the release checks').
2. **Race detector** (copy `witness-final\restorer_final2_tree_id_bound_witness_test.go` to `internal/restorer/`):
   ```
   CGO_ENABLED=1 go test -race -count=1 -run 'TestWitnessFinal2|TestRestoreAcrossTreeIDBound|TestRestoreTreeIDList|Prefetch|TreeCarry' ./internal/restorer
   ```
3. **The witness on Linux**, where its snapshots also hold symlinks, which the second pass restores (none on
   Windows for want of the privilege):
   ```
   RESTIC_WITNESS_SCENARIOS=300 go test -run TestWitnessFinal2 -count=1 -v ./internal/restorer
   ```
4. **Real binaries across the real bound** (70,700 directories; expected: same content, same metadata, peak
   memory near stock's):
   ```
   export RESTIC_PASSWORD=x; S=./restic-stock; F=./restic-final2
   rm -rf /tmp/big /tmp/rb /tmp/out-stock /tmp/out-final2; mkdir -p /tmp/big
   for i in $(seq 1 700); do mkdir -p /tmp/big/d$i; for j in $(seq 1 100); do mkdir /tmp/big/d$i/s$j; done; echo $i > /tmp/big/d$i/f; ln /tmp/big/d$i/f /tmp/big/d$i/s1/hard; done
   $S init -r /tmp/rb && $S -r /tmp/rb backup /tmp/big
   /usr/bin/time -v $S -r /tmp/rb restore latest --target /tmp/out-stock 2> /tmp/time-stock.txt
   /usr/bin/time -v $F -r /tmp/rb restore latest --target /tmp/out-final2 2> /tmp/time-final2.txt
   diff -r /tmp/out-stock /tmp/out-final2 && echo "same content"
   diff <(cd /tmp/out-stock && find . -printf '%p %y %m %s %T@ %n\n' | sort) <(cd /tmp/out-final2 && find . -printf '%p %y %m %s %T@ %n\n' | sort) && echo "same metadata"
   grep 'Maximum resident' /tmp/time-stock.txt /tmp/time-final2.txt
   ```

Limits of this addendum: Windows, memory backend, no race detector, no symlinks or special files in the witness's
snapshots here, the bound crossed with lowered values and not with 65,537 real directories, peak memory not
measured.

## Addendum 2: final2..final3 (11 commits, tip b8cf439)

Reviewed after Addendum 1, same rules, same workstation (2026-10-07 and 08). Scratch trees: `review\scratch-final\tree3`
(final3) and `review\scratch-final\tree2` (final2 at d4f7e81, for the reviewers' original tests); no branch changed.
Sources read first: `review\redteam\TRIAGE.md` and the three reports `redteam\seatN\REVIEW.md`.
`git diff --stat final2..final3 -- . ':!*_test.go'`: 21 files, +652 / -429, every one of them named by the commit
that changes it; no commit touches a non-test file its message does not describe.

One commit of final2 had not been reviewed: **d4f7e81** raises the bound of Addendum 1 from 65,536 to 524,288 tree
IDs (`restorer/tree_id_list.go:29`, at most 16 MiB). Only the constant, its comment and the size its test expects
change: SOUND. Where Addendum 1 says 65,536 and 2 MiB, read 524,288 and 16 MiB; its argument does not depend on the
number, and its witness passes on final2 at d4f7e81 and on final3.

### B1. Verdicts

**Nothing above LOW was found in the eleven commits. Nothing reproduced that fails.** One contract of the lock-wait
validation is stated more exactly than the tree states it (B3); it is the contract of repair R1 itself, older than
these commits.

| Commit | Files:lines (final3) | Verdict | KEEP / REPAIR / REMOVE |
|---|---|---|---|
| c89be23 fix 9: prefetch budget counts the hidden tail of a node's extended attributes | `restorer/tree_prefetch.go:171-178`; `tree_carry.go:130-131` | **SOUND** | KEEP |
| 5b2d540 fix 8: collected terminal output keeps message boundaries | `ui/termstatus/status.go:530-569`; `stdio_wrapper.go:85-88, 181-204` | **SOUND** (LOW: B4 N5) | KEEP |
| 02ee138 fix 3: the index releases its per-file notes when cleared | `repository/index/master_index.go:36-38` | **SOUND** | KEEP |
| 548f142 fix 7: an index file the rewrite keeps unread is first compared with its name | `index/master_index.go:433-441, 476-482`; `index_rewrite_keep.go:106-121` | **SOUND** | KEEP |
| a405f78 fix 2: repack and copy save on the context of their workers | `repository/repack.go:72, 118`; `repack_completion_overlap.go:131, 152`; `repack_scope.go:16-31`; `repository.go:1376, 1380` | **SOUND** (LOW: B4 N2, a test the tree lacks) | KEEP WITH THIS REPAIR (test only): take `TestWitnessFinal3Fix2SuppressionIsNarrow` into the tree |
| 35e89b5 fix 1: check's streaming decode keeps upstream's ceiling | `repository/check_decode_limit.go:15-59`; `repository.go:1815, 1889` | **SOUND** (B4 N3) | KEEP |
| 02e3df1 fix 6: files kept from the lock wait are compared by size too; check keeps nothing | `cmd/restic/wait_index_load.go:100-104`; `snapshot_metadata_wait.go:70-89`; `cmd_check.go:245-260, 318-322`; `index/master_index.go:311-351`; `index_rewrite_keep.go:123-140` | check: **SOUND** (upstream's lock call and order again). The reads the other commands keep: **SOUND-UNDER-CONTRACT** (B3) | KEEP |
| abda288 fix 4: validating listings are taken where upstream lists | `cmd/restic/wait_index_load.go:76-112, 203-211, 279-288, 301-337` | **SOUND-UNDER-CONTRACT** (B3) | KEEP; correct one sentence of the comment (B3) |
| 0f32644 fix 5: nothing is heard of the reads of the wait unless they are used | `backend/retry/held_reports.go`; `backend_retry.go:151, 156`; `wait_index_load.go:181-186, 319-327, 355` | **SOUND** (LOW: B4 N4) | KEEP |
| 8669c54 restore hands work over per batch, not per directory | `restorer/node_lookahead.go:121-154`; `tree_prefetch.go:22, 61-107, 189-234, 261-270` | **SOUND** (LOW: B4 N6) | KEEP |
| b8cf439 the build names itself | `global/global.go:43` | **SOUND** (B4 N7: two things users will see) | KEEP |

### B2. What was run

| Check | Result |
|---|---|
| The reviewers' ORIGINAL tests on final2 (d4f7e81), files copied from `redteam\seatN\tree\` | Every finding reproduces: 16 test functions fail as the reports say (`logs\final2_original_redteam_tests.log`) |
| The reviewers' ORIGINAL tests on final3, one file at a time | Fixed findings: `TestSeat2RewriteRejectsDamagedKeptIndex` PASS (fix 7), `TestSeat2RepackReadErrorCancelsQueuedSave` PASS (fix 2), `TestSeat2IndexNotesReleasedOnReload` PASS (fix 3), `TestReviewSeat3PrefetchHiddenAttributeBudget` PASS (fix 9), `TestReviewFailedLockLeaksSpeculativeError` PASS (fix 5), `TestSeat2LargeCheckRejectsUnrestorableBlob` PASS (fix 1 at the production size, 64 GiB + 1 byte, 30 s). Documented, not fixed, still failing as triaged: the three stored-bytes parity tests (index and lock compression) and `TestReviewSeat3RestoreRechecksTreeIntegrity`. Seven originals cannot show the fix as written, each for a reason that is the fix or the harness, not the defect: see below the table |
| The three R1 witnesses and the three lock-dwell witnesses on final3 | PASS, 6 of 6 |
| `TestLockWaitSchedules`, 3,000 schedules in each of the four lag modes (none, uniform, random, nonatomic) | PASS: 12,000 schedules, 16,469 confirmed locks, 12,289 of them with reads during the wait; stale 0, missing 0, outcome 0, final 0, goroutine leak 0 (`logs\final3_r1_schedules.log`) |
| The A16 witness on final3, with its ordered driver set to final3's context arrangement (`repository_final3_fix2_repack_witness_test.go`) | PASS: 400 schedules through `CopyBlobs` (212 nil returns, 188 errors; in the 33 schedules where an unreadable pack is the only failure, repack and the uploader both return that read error); 300 schedules against the verbatim worker of the repaired tree, same result in all; byte budget; 150 copies between two repositories |
| The Addendum 1 witness on final3 | PASS: hand-over 4,000 sequences x 6 bounds; 300 snapshots, 2,267 restores across lowered bounds |
| **The restore of final2 against the restore of final3** | 300 random scenarios (the same as above: faulty trees, stop or continue, cancellation, `--delete`, overwrite modes, filters, hard links). For each, a digest of the reference restore's ordered trace (every filter call of both passes, reported errors, warnings, traversal progress), returned error, file count and target was written by both builds: **300 of 300 digests identical** (`logs\restore_digests_tree2.txt`, `restore_digests_tree3.txt`) |
| Packages of the touched code on final3 | `./internal/repository/...`, `backend/retry`, `ui/termstatus`, `checker`, `global`, `data`: all pass. `./internal/restorer`: the 9 environment-bound failures of L6, the same on final2. `cmd/restic` (`TestPrune*`, `TestCopy*`, `TestCheck*`, `TestForget*`, `TestLs*`, `TestRestore*`, backup): 33 pass, `TestCopy` fails in its restore step as on final2; the only differences to final2 are the tests the commits add and remove |
| The new tests of the commits and every witness as 32-bit binaries (`GOARCH=386`) | PASS (`logs\final3_windows_386.log`); `cmd/restic` tests do not build for 32 bit in stock either |
| 25 mutations (`witness-final\final3_mutations.py`, `logs\final3_mutations.log`) | B5: 23 caught by the tree's own tests, 2 only by a witness of this review |

The seven originals that cannot show the fix as written:

- `TestSeat2CheckDecoderRetainsCumulativeLimit` (quick test of finding 1) still FAILS on final3. It sets its small
  ceiling as an option of the decoder, which the streaming path cannot read; the fix counts against a constant, 64
  GiB, upstream's default (N3). The reviewer's production-size test of the same finding passes.
- `review_seat3_batch_test.go` does not compile: `writeBatch` takes the message ends now. The carried
  `TestBatchErrorAtMessageBoundary` passes.
- Seat 1's and seat 2's lifecycle tests of the check finding (`TestReviewIndexCorruptionBeforeConfirmation`,
  `TestSeat2CheckRejectsIndexDamagedDuringLockWait`) call `startCheckerIndexLoad`, which no longer exists, and seat
  1's lifecycle test of the restore finding reads a field of the validation that is gone.
  Seat 1's command-level `TestReviewCheckCommandAcceptsIndexDamagedDuringWait` compiles (with those tests cut out)
  and fails on its own precondition: its backend waits five seconds for check to read an index during the settle
  delay and then gives up without damaging anything; check no longer reads then. The carried
  `TestCheckReportsIndexDamagedBeforeLockConfirmation` damages the file at confirmation regardless and passes.
- Seat 1's `TestReviewRestoreCommandIndexVisibilityDuringSnapshotRead` fails in its harness: it holds the snapshot
  read until the index was listed first and gives up after five seconds; that early listing is what fix 4 removed.
  The carried `TestRestoreListsIndexAfterSnapshotRead` passes.

### B3. Fix 4 and fix 6 together: is repair R1 still complete?

**Yes, for backup, restore, ls, forget and prune, under one contract; and check does nothing before confirmation.**

What is read during the settle delay, and what becomes of it (`wait_index_load.go`):

| Read during the wait | Use after confirmation |
|---|---|
| The repository index (`repo.LoadIndex` on the command's own repository object, `:193`) | Only through `consume` (`:301-337`), which the command calls where upstream calls `LoadIndex`. Before that no command touches the index: restore and forget read snapshot files, backup looks for its parent, ls lists. On every exit without `consume` the deferred `discard` empties it (`:348-357`) |
| The snapshot listing | Compared only (`:86`). The command works with a listing taken after confirmation (`snapshotLister`, `:203-211`) |
| One snapshot file, by ls (at most 64 KiB, below the retrying backend, hash checked) | Served only if the listing taken after confirmation lists that name with that size (`snapshot_metadata_wait.go:70-89`), and then through the ordinary load, which hashes it again |
| check | Nothing. `cmd_check.go:247` is upstream's lock call; snapshots and index are read after it, in upstream's order; the pack sizes are computed after the index is loaded (`:321`). `diff` against stock's `runCheck` from the lock call to the index errors: the comment only |

Is anything validated against a listing taken EARLIER than upstream's? No. Nothing is listed at confirmation any
more (`settle`, `:279-288`). The snapshot files are listed in the argument of the call in which upstream lists them
(`cmd_restore.go:163`, `cmd_forget.go:216`, `cmd_ls.go:405`, `cmd_backup.go:564`); the index files are listed at
the top of `validateLockWaitReads` (`:93`), called from `consume`, at upstream's `LoadIndex`. Witnessed with a
backend that records its requests (`TestWitnessFinal3LockWaitListingsAreNotTakenBeforeStockTakesThem`): after
confirmation the backend sees exactly "list snapshots" when the command selects, then "list index" when it loads,
once each. Upstream lists no snapshot files for a full snapshot ID or with `backup --force`; this build lists them
once more in the first case: one request, no difference in what the command decides.

**The contract (SOUND-UNDER-CONTRACT).** The index of the wait is kept when every file it was built from is still
*listed* with its name and size. The files are not read again; upstream lists at the same point and then *reads*
them. So: *a listing taken after confirmation shows an index file under its old name and size only if that file can
still be read with the content it had.* Two ways to break it:

1. Damage in place that leaves the length alone. TRIAGE.md finding 2 documents it; fix 6 removed the cases that
   change the length and took check out altogether.
2. **A listing that lags behind a deletion**: a directory cache of a network mount, an eventually consistent object
   store. This case is not in the triage. With it the outcome R1 was repaired for is possible again on such a
   backend: this process's lock file takes long to write; a prune that was confirmed meanwhile deletes packs and
   replaces the index inside this process's settle delay; the listing after confirmation still shows the old index
   file; the index read before the prune is kept, and a backup deduplicates against deleted packs and reports
   success. `TestWitnessFinal3LockWaitTrustsAListingThatLagsADeletion` shows the step that matters (a load after
   confirmation fails with "not found"; this build keeps the index and takes the deleted blob for stored).

How far this reaches: it needs the slow lock write of the original R1 scenario AND a listing that lags a deletion
AND the old index file absent from the local cache (upstream reads index files through the cache, and with the file
cached it goes on with the stale index as well). restic's lock protocol itself decides by listings, so such a
backend is outside what locking can make safe in upstream too. On backends with consistent listings (local disk,
SFTP, REST server, S3, B2, Azure, GCS as they are today) R1 is closed: 12,000 schedules, 0 failures. This is the
design of the R1 repair ("validate"), unchanged since `repaired`; fixes 4 and 6 only narrow it.

What to do with it: **document it** beside the remainder TRIAGE.md already documents, and change the sentence in
`wait_index_load.go:24-27` and in fix 4's message, "with any backend ... this build knows at each of these points
what stock knows there": it knows what stock's *listing* knows; stock also reads. A repair exists if wanted: one
`Stat` per kept index file after confirmation (existence and size from the object itself, not from a listing).

### B4. The other questions, and the LOW observations

**Fix 2: can the marked context swallow a real save error, or let repack return nil with a save unreported?** No.
`asyncSaveResult` (`repack_scope.go:26-31`) withholds an error from the uploader's group only when the save's
context is a repack's and that context is cancelled. The save's callback gets the error in every case, before
that decision (`repository.go:1375-1380`), and the callback is what repack's result is made of. A repack context is
cancelled in two ways only. (a) A function of the repack's group returned an error: then `wg.Wait()` returns that
error whatever the workers do with their callbacks, so the command fails with the reason, which is what the fix is
for. (b) The parent was cancelled: then the worker returns the callback's error (the A16 rule, which three tests
guard since Addendum 1), and the flush and everything after it run on a cancelled context. After a repack has
returned nil its group context is cancelled as well, but by then every save has called back, and a save that
returned no error reports none. Evidence: the 400 + 300 + 150 schedules above with fix 2 in place, among them 31
with failing uploads (the uploader failed in all 31) and 33 with a read error as the only failure;
`TestWitnessFinal3Fix2NoSilentLossThroughTheMark` (the blob of a save that was given up is not in the index).
Saves on the group context are upstream's arrangement (`stock ... repack.go:129`); the old comment's worry that
this context "can be canceled as workers finish" does not hold: an errgroup cancels on the first error or when
`Wait` returns, not when a worker returns nil.

**Fix 7: the right name, read errors, short reads, missing files, other routes.** The kept file is read with
`LoadRaw(ctx, IndexFile, id)` with its own ID (`index_rewrite_keep.go:118-121`). `LoadRaw` is upstream's, line for
line (`repository/raw.go:16`): it hashes what it read and compares with `id`, retries once after dropping the
cached copy, and returns an error for a read error, for content that does not hash to the name (a short read is
that) and for a missing file. The error goes out in upstream's wrapping (`LoadUnpacked(<id>): ...`), the errgroup
stops the rewrite, and obsolete index files are removed only after `wg.Wait()` returned nil (`master_index.go:600-
612`); prune deletes packs after that. Both routes on which a file stays are covered: kept unread (verified,
`:476-482`) and the ordinary loop (upstream's reload and decode). A repository wrapper without `LoadRaw` gets the
ordinary loop for every file (`:437-440`); production passes `*internalRepository`, which has it. Like upstream's
reload, the raw read goes through the local cache.

**Fix 5: can a real error now be lost?** No diagnostic upstream prints is dropped. Held reports are released
exactly when the index of the wait takes the place of upstream's load (kept or extended, `:319, 322`). They are
dropped when the lock is refused or the reads are discarded: upstream makes no such reads then, or the reread
reports for itself. Mutation F5c (dropped when kept) fails `TestWaitDiagnosticsFollowTheUseOfTheReads`.

**8669c54: order of operations and errors, cancellation, the bound.** First pass: the traversal decodes nodes
itself until one is not a directory and asks the helper from there on; it calls `next` only while no request is
pending (`node_lookahead.go:122-154`), and no request is pending while a directory is descended into, as before.
Second pass: a batch is a slice of consecutive prepared trees in list order; `Load` serves them front to back,
falls back on a tree it does not expect and after the queue is closed, exactly as before (`tree_prefetch.go:189-
234`). A failed tree is the last of its batch. The 300 digests identical to final2's are the evidence that nothing
a caller can observe moved; the Addendum 1 witness shows the bound and the hand-over still hold.

LOW observations:

- **N2, a test the tree lacks (fix 2).** Mutations F2c (every cancelled save is withheld, the archiver's too) and
  F2d (every error of a repack save is withheld, also on a live context) pass all of the tree's tests; only
  `TestWitnessFinal3Fix2SuppressionIsNarrow` fails. Neither loses an error by itself (callbacks still deliver),
  but they remove the second line of defence the uploader's group is. Take the witness into the tree.
- **N3 (fix 1).** The ceiling is the constant 64 GiB, upstream's default, not the option of the decoder at hand.
  All of check's decoders have upstream's default today; a decoder given another limit would not be followed.
- **N4 (fix 5).** One diagnostic of the wait is not held: the cache layer's "error clearing index files in cache"
  goes to `printer.E` directly (`backend/cache/backend.go:248`), and the clean-up of the local cache it reports on
  runs during the wait. Local files only.
- **N5 (fix 8).** A message of upstream (the complete lines of one `Write`) that spans a flush of the collector, at
  its size cap or around one over-long line, is two or three messages here. It shows only when a write fails inside
  it: the rest is written where upstream gives the message up.
- **N6 (8669c54), performance only.** The producer offers its batch after each tree it has prepared. A traversal
  that runs out of trees while the producer is in the middle of preparing the next one waits for that tree,
  although an earlier one is ready in the producer's hands.
- **N7 (b8cf439).** Two things users will see. Snapshots written by this build carry
  `"program_version": "restic 0.19.1-dev-dealer.1 (compiled manually)"`: by design, and the one place where a
  stored file differs in content from what upstream writes for the same backup. `restic self-update` compares that
  string with upstream's latest release and would replace this build with upstream's binary.

### B5. Mutations

| Fix | Mutation | Caught by the tree's own tests |
|---|---|---|
| 9 | hidden tail not counted | yes: `TestPrefetchBudgetCountsHiddenAttributeTail` |
| 8 | the message after a failure on a boundary is lost | yes: 3 tests |
| 8 | a repeated error is reported twice | yes: `TestBatchOnFullDiskFollowsMessages`, `TestCollectedOutputFailsLikeLineByLineOutput` |
| 8 | the message with the failure is written again | yes: 5 tests |
| 3 | notes kept on clear | yes: `TestIndexNotesReleasedOnReload` |
| 7 | kept index file not verified | yes: `TestRewriteKeepsUnread`, `TestRewriteFailsOnChangedKeptFile`, `TestRewriteRejectsDamagedKeptIndex` |
| 2 | saves on the uploader's context again | yes: `TestRepackReadErrorCancelsQueuedSave` |
| 2 | cancelled saves fail the uploader (no mark) | yes: same test (the command gets "context canceled") |
| 2 | every cancelled save withheld, also the archiver's | **no**; only this review's witness |
| 2 | every error of a repack save withheld | **no**; only this review's witness |
| 1 | no limit | yes: `TestCheckStreamKeepsCumulativeDecodeLimit` |
| 1 | declared size not refused | yes: `TestCheckStreamRefusesDeclaredOversize` |
| 1 | limit one byte low | yes: `TestCheckStreamKeepsCumulativeDecodeLimit` |
| 6 | size of index files not compared | yes: `TestLockWaitReadsRejectResizedIndexFile` |
| 6 | prepared snapshot kept whatever its size | yes: `TestPreparedSnapshotKeptOnlyWithListedSize` |
| 4 | index listed at confirmation again | yes: `TestRestoreListsIndexAfterSnapshotRead`, `TestLockWaitIndexListedAfterSnapshotRead` |
| 5 | reports shown when the lock is refused | yes: both tests of the fix |
| 5 | reports not held | yes: both |
| 5 | reports dropped when the reads are kept | yes: `TestWaitDiagnosticsFollowTheUseOfTheReads` |
| restore | batch never handed over early | yes: 3 tests |
| restore | batch byte budget ignored | yes: `TestTreePrefetchBatchKeepsByteBudget` |
| restore | trees of a batch served last first | yes: `TestTreePrefetchHandsOverBatches` (the restore itself stays right: the unexpected tree sends it to the fallback) |
| restore | an unexpected tree served from the batch | yes: `TestRestoreTreePrefetchFallsBackWhenTraversalOrderChanges` |
| restore | helper asked for directories too | yes: `TestNodeLookaheadDecodesDirectoryRunsInline`, `TestNodeLookaheadRandomTreesKeepOrder` |
| restore | node after a directory skipped | yes: the same two |

### B6. NEEDS-RUN on the box, and limits

None decides an open verdict. Copy from `review\witness-final\` into the final3 tree:
`repository_final_a16_repack_witness_test.go`, `repository_final3_fix2_repack_witness_test.go`,
`repository_final_f2_stored_form_witness_test.go` to `internal/repository/`;
`restorer_final2_tree_id_bound_witness_test.go`, `restorer_final_f2_incompressible_readback_witness_test.go` to
`internal/restorer/`; `cmd_final3_lock_wait_listing_contract_witness_test.go` to `cmd/restic/`.

1. **Race detector over the new concurrent code** (expected: no report).
   ```
   CGO_ENABLED=1 go test -race -count=1 ./internal/backend/retry ./internal/ui/termstatus
   CGO_ENABLED=1 go test -race -count=1 -run 'TestRepack|TestWitnessFinalA16|TestWitnessFinal3Fix2|TestCheckStream|TestRewrite' ./internal/repository ./internal/repository/index
   CGO_ENABLED=1 go test -race -count=1 -run 'TestTreePrefetch|TestNodeLookahead|TestRestoreTreePrefetch|TestWitnessFinal2|TestRestoreAcrossTreeIDBound' ./internal/restorer
   CGO_ENABLED=1 go test -race -count=1 -run 'TestLockWait|TestWitnessLock|TestWitnessFinal3LockWait|TestRefusedLock|TestWaitDiagnostics|TestRestoreListsIndex|TestCheckReportsIndexDamaged' ./cmd/restic
   ```
2. **The schedule test at length and the witnesses on Linux.**
   ```
   for lag in "" uniform random nonatomic; do RESTIC_R1_SCHEDULES=3000 RESTIC_R1_SCHEDULE_LAG=$lag go test -run TestLockWaitSchedules -count=1 -v ./cmd/restic; done
   go test -run 'TestWitnessFinal' -count=1 -p 2 -v ./internal/repository ./internal/restorer ./cmd/restic
   ```
3. **The restore of final2 against the restore of final3 on Linux**, where the witness's snapshots also hold
   symlinks (expected: no difference). In each of the two trees, with the witness copied in:
   ```
   RESTIC_WITNESS_SCENARIOS=300 RESTIC_WITNESS_DUMP=/tmp/digests-$(git rev-parse --short HEAD).txt go test -run TestWitnessFinal2RestoreAcrossBoundDifferential -count=1 ./internal/restorer
   diff /tmp/digests-d4f7e81.txt /tmp/digests-b8cf439.txt && echo "same restores"
   ```
4. **R1 with real binaries** (REPAIRS.md experiment 1) on final3: a backup whose lock upload is delayed beside a
   local `forget --prune`, then stock `check --read-data`; and `check` with an index file cut short during its
   settle delay (expected: check reports it).
5. **Fix 1 at size**: `RESTIC_LARGE_CHECK_TEST=1 go test -run TestLargeCheckRejectsUnrestorableBlob -count=1 -v ./internal/repository`.
6. **The measurements the commits quote**: the many-directory restore of 8669c54 and release check F5 with the
   bound of d4f7e81.

Limits: Windows, memory backend, no race detector; the lagging backends of findings 2 and 7 and of B3 are models;
the red team's reports were taken as written for what they judged correct; `cmd/restic` integration tests that
need a symlink fixture do not run here; the measured gains were not checked.
