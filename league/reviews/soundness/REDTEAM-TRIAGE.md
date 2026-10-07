# Independent red team of the final tree: findings and triage (2026-10-07)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Three reviewers of a different model family from the earlier reviewers (about 40 minutes each) were given only two source trees, upstream restic at commit ba802d42 and the tree
`final2` at commit d4f7e81, and this contract for the tree: the same repository format and stored bytes, the same
behaviour and defaults, every integrity check upstream performs still performed, safe with concurrent processes, safe
when killed, no data loss, exact restores. They were told to look for any situation where the tree would not meet it.
They were not shown the earlier reviews or the repairs. Reports: `seat1\REVIEW.md` (locking and concurrent processes),
`seat2\REVIEW.md` (prune, repack, index, check), `seat3\REVIEW.md` (backup write path, encryption, compression,
restore). Their reproduction tests are in the matching `seatN\tree\` copies.

**Result.** 13 findings, every one reproduced by its reviewer with a test, 12 distinct (one was found by two seats),
shown below in 11 rows (the two findings about stored encoding, index files from seat 2 and lock files from seat 3,
share row 11).
No reviewer found a case of data loss, a wrong restore, or an incomplete backup reported as success. Five findings
carry the reviewers' label BLOCKER, which the brief defined to include "a missing integrity check": all five are of
that kind (the tree reads once, or does not bound, something upstream reads twice or bounds), not data loss.

The triage below is the coordinator's. "Fix" means a commit on branch `final3` with the reviewer's test as a
regression test, before the final measurement.

| # | seat, their label | finding | triage | reason |
|---|---|---|---|---|
| 1 | seat 2, BLOCKER | `check --read-data` decodes compressed blobs as a stream without upstream's ceiling on total decoded bytes. A crafted blob that expands past the limit (64 GiB + 1 in the test) passes check, while restore and upstream's check reject it | **Fix** | check must reject what upstream rejects |
| 2 | seat 1 BLOCKER, seat 2 BLOCKER | State read during the 200 ms lock wait is validated after confirmation by file NAME only. An index file that is damaged on storage inside the wait window, after this build read it, is not noticed by that run; upstream reads after confirmation and would notice. No restic process can cause it (index names are content hashes); it needs storage damage in that window | **Fix in part, document the rest** | The validation now compares size as well as name, and `check` returns to upstream's order (it verifies index files after confirmation and keeps nothing from the wait). For the other commands the residual is documented: it moves the moment of reading by at most the wait, and damage after upstream's own read goes unnoticed by upstream in the same way |
| 3 | seat 2, BLOCKER | Prune's index rewrite keeps index files that need no change without the re-read upstream performs, so an index that became unreadable or damaged between the load and the rewrite is not noticed and prune goes on to delete packs | **Fix** | Before a file is kept unread it is read raw and its SHA-256 is checked against its name; a failure aborts the rewrite, as upstream does. Prune is the destructive command and should stop where upstream stops |
| 4 | seat 3, BLOCKER | Restore keeps the directory trees it loaded and verified in its first pass and uses them in its second pass; upstream loads them again. A tree damaged on storage between the two passes fails upstream's restore and not this build's | **Document** | What is restored comes from data that was authenticated and hash-checked when read, and the result is the snapshot's content. The difference is that upstream reports storage damage that occurs during the restore and this build does not for these objects |
| 5 | seat 2, HIGH | Repack and copy submit saves with the outer context instead of the worker group's, so a read error cannot cancel a save that is blocked behind stalled uploads: the command hangs where upstream returns the error | **Fix** | a regression on an error path |
| 6 | seat 2, HIGH | The index keeps per-file notes that are never released when the index is cleared and loaded again: memory grows in a long-lived process that sees many index replacements (`mount --no-lock`) | **Fix** | a leak |
| 7 | seat 1, HIGH | After confirmation the validation lists the index before restore has read the selected snapshot; upstream lists the index after. On a backend whose listings lag, a restore of a just-written snapshot fails ("There were 2 errors", nothing restored) where upstream succeeds | **Fix** | For every command the validating listing is now taken no earlier than the point where upstream lists that directory |
| 8 | seat 1, MEDIUM | Diagnostics from unconfirmed wait work (for example a failed index listing) reach stderr even when the lock is then refused; upstream prints only the lock error | **Fix** | output differs from upstream |
| 9 | seat 3, MEDIUM | Batched terminal output drops the next message when a write error lands exactly on a message boundary; upstream still writes it | **Fix** | output differs from upstream on an error path |
| 10 | seat 3, MEDIUM | The restore prefetch's byte budget counts only the visible entries of a node's extended attributes, not the slice's backing tail; non-canonical tree JSON can hide megabytes from the budget | **Fix** | a memory bound that does not hold for crafted input |
| 11 | seat 2 MEDIUM, seat 3 MEDIUM | Index files are compressed by a streaming encoder and lock files by a hand-written raw-block frame: valid Zstandard, readable by upstream, but a few bytes different from what upstream's encoder writes for the same content (and the lock frame ignores the chosen compression level) | **Document** | Data and tree blobs are stored exactly as upstream stores them (checked by reading stored entries back). The published claim is therefore "identical chunking, blob IDs and blob storage; index and lock files decode to the same content", not "the same stored bytes" |

What the reviewers examined and judged correct is listed in each report (lock settle timing in 44 cases, index removal
and addition under current listings, prune's keep / repack / delete decisions, repack completion and ownership, the
chunkers against upstream for polynomial degrees 8 to 53, save verification, key derivation, the prepared cipher state,
tree decoders on 20,000 payloads, restore offsets, hard links and sparse files).

Limits of this red team: one workstation, Windows, no race detector, no real backend with lagging listings (findings 2
and 7 use modelled backends), no multi-process runs; about 40 minutes per seat.

## After the fixes: independent review of the eleven commits (final2..final3)

The eleven commits (the nine fixes, the many-directory restore change, the version string) were reviewed by an
independent reviewer agent (`..\REVIEW-final.md`, "Addendum 2"): nothing above LOW, keep all eleven; 25 mutations of
the fixes, 23 caught by the tree's own tests (the two misses led to one more test). The reviewers' original tests fail
on the tree they reviewed (16 of 16) and the fixed findings no longer reproduce.

That review states one contract of the lock-wait validation that this triage had not written down, and it is a known
limit of the release:

- For the commands other than `check`, an index kept from the lock wait is validated after confirmation by a listing
  (name and size) taken where upstream lists, not by reading the files again. On a backend whose listing can still show
  a file after it was deleted (a network-mount directory cache, an eventually consistent store), a kept index file that
  a concurrent prune has deleted is not noticed, and the original lock-wait outcome returns in that corner: a backup can
  deduplicate against packs a prune just deleted and report success. In this build it needs two things at once: a
  lock-file write slow enough for the prune to finish inside the window, and a listing that lags the deletion. Whether
  upstream differs there depends on a third: upstream without a cached copy of the old index file fails loudly, because
  it tries to read the file; upstream with the file in its local cache, which is the default after a first run, goes on
  with the stale index as this build does. (Corrected 2026-10-07 after the release documents were checked against
  `..\REVIEW-final.md`, Addendum 2, B3: an earlier wording listed the cache as a third condition of the outcome itself.)
  Decision: documented, not changed. One `Stat` per kept index file would close it at the price of one request per
  index file per command on every backend.

