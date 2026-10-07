# Final release candidate of the Restic League V2 (2026-10-07)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Built on the workstation, measured on the box, 04:42 to 09:46 UTC. $0 API. Step log with every number and job id: `PROGRESS.md`. Tools: `tools\` (workstation) and `/opt/league/restic-final/tools` (box). This file is the merge agent's report, saved by the coordinator (the agent's session could not write it). At the time of saving, an independent review of the 16 commits and three release-check follow-ups (repository size, `repair packs` with one connection, restore memory with millions of directories) are still open: the tree may change again.

**Verdict.** The final tree is branch `final`, commit 7461eb0: `incumbents/S019-merge` + the repairs R1 to R11 and M22 + three release-check fixes (F1, F2, F3) + the sound part of S020 + four ports. At 44 rounds, run alone, it reads **-0.986% against `incumbents/S020`** (7/44 wins) and **+29.464% against stock** (44/44), with every unit verified against the stock oracle. It is below S020 because it pays for soundness: S020 still carries the BLOCKER at three new call sites and stores some blobs in a form stock does not. Without the last release-check fix (stock's stored form) the same tree is level with S020 (-0.116%, 12 rounds).

Box tree: `/opt/league/restic-final/final` (worktree of branch `final`, 7461eb0). The judged copy is `/opt/league/restic-final/trees/rc-5`, byte-identical (sha256 over all files compared).

## Official judging (judge `a770cec8`, official lanes, launcher `tools\judge_final.py`)

| job | id | rounds | candidate vs reference | suite | wins | main | big-index | large-files | remote |
|---|---|---|---|---|---|---|---|---|---|
| repair price, as staged | `3c21cf806c324d4ebbd9f1d61db6bde2` | 12 | `repaired` (01d1452) vs `S019-merge` | -3.152% (int -3.50..-2.77) | 0/12 | -3.52 | -2.09 | -4.59 | -2.42 |
| raw-rule arm | `aca189ed7cbe43159e3e58d2ad04dfaf` | 12 | final tree before F2 (`rc-3`) vs `S020` | -0.116% (int -0.69..+0.28) | 7/12 | +0.04 | +0.76 | +0.14 | -1.41 |
| final, reference | `757b745c69924a67a1ba8c1b06694b24` | 12 | final tree (`rc-5`) vs `S020` | -0.910% (int -1.13..-0.59) | 1/12 | -1.50 | -0.33 | -1.16 | -0.65 |
| **final, promotion length, alone** | `c87fa304e1404dfda35dd162d7dcf0fc` | 44 | final tree (`rc-5`) vs `S020` | **-0.986%** (int -1.12..-0.76) | **7/44** | -0.98 (0/11) | +0.02 (6/11) | -1.23 (1/11) | -1.74 (0/11) |

Void, nothing measured: `5d43382d6e964b55885a85565bcc7772` (candidate path mangled by the Windows shell, failed at start); `ef51c559f4b14c45972257844c801d96` (stopped by the agent in its warm-up: its candidate was on the slow key-derivation alignment).

The first three jobs ran beside research benches; the fourth ran alone with nothing else of this merge on the box.

### Final numbers (44 rounds, job `c87fa304…`)

| | suite | main | big-index | large-files | remote |
|---|---|---|---|---|---|
| time vs S020 | **-0.986%** (7/44; 90% -1.12..-0.76) | -0.98% (0/11) | +0.02% (6/11) | -1.23% (1/11) | -1.74% (0/11) |
| 90% interval vs S020 | | -1.38..-0.84 | -0.15..+0.47 | -1.67..-0.21 | -1.94..-1.29 |
| memory saved vs S020 | -9.90% | -13.59% | -5.03% | -9.98% | -11.02% |
| summed phase peaks, final / S020 MiB | | 647.8 / 571.6 (+76.1) | 1652.1 / 1573.2 (+78.9) | 598.0 / 544.2 (+53.9) | 666.9 / 609.0 (+57.9) |
| time vs stock | **+29.464%** (44/44; 90% +29.33..+29.56) | +30.65% (11/11) | +45.51% (11/11) | +24.07% (11/11) | +17.63% (11/11) |
| memory saved vs stock | +38.26% | +34.98% | +44.56% | +32.27% | +41.23% |
| summed phase peaks, final / stock MiB | | 647.8 / 993.7 | 1652.1 / 2968.3 | 598.0 / 881.0 | 666.9 / 1132.1 |
| median total s, final / S020 / stock | 78.01 / 77.18 / 110.59 | 15.544 / 15.395 / 22.442 | 17.820 / 17.839 / 32.675 | 12.645 / 12.479 / 16.596 | 32.005 / 31.469 / 38.877 |

Correctness: 1,019/1,019 units verified against the stock oracle, compatibility tests passed, cross reader ok.

Per phase vs S020: time saved % (wins of 11), final seconds, peak change in MiB.

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | -3.97 (0) 5.131 s, +24.3 | -3.15 (0) 4.789 s, +32.5 | -6.22 (0) 3.785 s, +28.1 | -0.96 (1) 7.887 s, +22.6 |
| backup-unchanged | +0.69 (9) 1.706 s, -0.5 | +1.15 (10) 1.805 s, -0.9 | -0.27 (4) 1.390 s, -0.4 | -0.61 (0) 2.334 s, +0.8 |
| backup-mutated | +0.17 (7) 1.836 s, +24.2 | -0.48 (3) 1.934 s, +23.2 | +0.29 (7) 2.081 s, +8.0 | -0.56 (4) 2.593 s, +21.1 |
| restore | -0.24 (1) 3.167 s, +10.0 | -0.07 (5) 3.182 s, +7.0 | -0.33 (3) 2.322 s, +3.0 | -0.92 (0) 7.572 s, +6.5 |
| check-read-data | -0.84 (5) 1.640 s, -2.0 | +4.89 (11) 2.951 s, +4.7 | +0.08 (7) 1.471 s, -1.3 | -1.45 (0) 6.801 s, -3.0 |
| forget-prune | +2.34 (10) 1.208 s, +19.3 | +0.86 (9) 2.221 s, +15.5 | +5.89 (11) 1.156 s, +15.7 | -6.01 (0) 3.592 s, +9.9 |
| ls-json | +0.85 (10) 0.849 s, +0.8 | +0.01 (6) 0.927 s, -3.1 | +0.21 (8) 0.444 s, +0.8 | -4.29 (0) 1.285 s, +0.0 |

Phase means over the four workloads: backup-initial -3.57 (1/44), backup-unchanged +0.24 (23/44), backup-mutated -0.15 (21/44), restore -0.39 (9/44), check +0.67 (23/44), forget-prune +0.77 (30/44), ls-json -0.81 (24/44).

Per phase vs stock, time saved % (11/11 wins in every cell):

| phase | main | big-index | large-files | remote |
|---|---|---|---|---|
| backup-initial | +8.06 | +17.98 | +10.78 | +6.50 |
| backup-unchanged | +23.74 | +34.24 | +27.74 | +22.35 |
| backup-mutated | +22.72 | +31.45 | +15.79 | +20.35 |
| restore | +47.28 | +50.93 | +21.23 | +29.83 |
| check-read-data | +35.39 | +42.12 | +32.48 | +6.44 |
| forget-prune | +46.11 | +71.40 | +46.43 | +11.96 |
| ls-json | +42.37 | +52.86 | +38.85 | +37.35 |

Reading the cells: the first backup carries the encoder pass R4 restored and the stored form F2 restored; remote carries R1's two listings per process (ls -4.29%, requests 14 to 16) and R2's ranges (forget-prune -6.01%, requests 117 to 134); forget-prune locally is A16; big-index check is A5 on top of S020's pack types.

### The two arms: what stock's stored form costs (12 rounds each, both vs S020)

| | raw rule (`aca189ed…`) | stock's form, fix (a) (`757b745c…`) |
|---|---|---|
| suite vs S020 | -0.116% (7/12) | -0.910% (1/12) |
| main / big-index / large-files / remote | +0.04 / +0.76 / +0.14 / -1.41 | -1.50 / -0.33 / -1.16 / -0.65 |
| summed peaks vs S020, MiB | +31.5 / +30.0 / +16.0 / +20.2 | +84.6 / +86.3 / +37.9 / +61.3 |
| vs stock | +29.85% (12/12) | +29.383% (12/12) |
| vs stock per workload | +31.14 / +46.23 / +24.14 / +17.88 | +30.69 / +45.35 / +23.73 / +17.77 |
| memory saved vs stock | +41.72% = +37.75 / +45.58 / +38.37 / +45.20 | +38.07% = +34.78 / +43.18 / +32.06 / +42.27 |

Official difference: 0.79 suite points. Remote's difference between the two jobs is the jobs' conditions, not the form (the raw-rule job ran beside benches, and S020's remote check fell on its slow value in the second job: 6.981 against 6.675 s).

Bench, the same pair of trees directly (`rc-5` vs `rc-4`, 10 fixed pairs per cell, forget-prune repeated on the quiet box): pass main -0.94% (-0.84 with the quiet prune cell), big-index -0.14% (-0.30), large-files -1.41% (-1.28), remote -0.32%; mean **-0.70**. Cells beyond their band: backup-initial main -2.34% (0/10), big-index -1.62% (1/10), large-files -4.79% (0/10); backup-mutated main -1.16% (1/10); forget-prune, quiet: main -1.01% (1/10), big-index -1.35% (2/10), large-files -2.94% (0/10). Summed peaks +43.9 / +51.6 / +27.5 / +31.7 MiB (backup-initial +16.6 to +18.3). Cause: a blob stored in compressed form is verified on save through the compressed path (a decrypted copy, and below 2 MiB a decoded copy); a raw blob was decrypted and hashed in a stream with no buffer.

## The price of the repairs

**Official, as staged** (job `3c21cf80…`, `repaired` 01d1452 vs `S019-merge`, 12 rounds, 347/347 verified): suite -3.152% (0/12). Per phase, time saved %: main bi -7.10, bu -0.60, bm -1.86, rs -0.61, ck -0.54, fp -6.16, ls -0.66; big-index bi -5.00, bu -0.80, bm -1.37, rs -0.72, ck -2.35, fp -2.45, ls -1.45; large-files bi -10.28, bu -0.67, bm +0.21, rs -1.39, ck -0.69, fp -11.03, ls -1.65; remote bi -2.47, bu -1.99, bm -2.11, rs -0.55, ck -1.08, fp -7.94, ls -5.71. Summed peaks +40.8 / +74.7 / +45.1 / +64.0 MiB. Vs stock +26.90% (12/12).

That figure contains two costs that were not the price of soundness, both removed in `final`:
- R4 as first written let the encoder grow its destination block by block (profile, main first backup: memmove +0.54 s, madvise +0.24 s, 170 more collections, against +0.74 s in the encoder itself).
- R3's checks were inlined in front of the key derivation's loop and moved it to the slow alignment: about 7 ms in every process (see "Layout").

**Attribution, bench on research lanes** (`repaired` vs `repaired` minus one repair, 10 fixed pairs unless stated; * = beyond the band; negative = the repair costs):

| repair | where it costs | measured | as its report expected? |
|---|---|---|---|
| R1 validate | every process on remote: two listings after confirmation | remote pass 32.05 to 32.41 s (-1.14%): ls -4.36%*, bu -1.49%*, rs -0.37%*, ck -1.17%, bm -1.84%, fp -0.99%, bi -0.72%; requests +1 or +2 per process. Local: main -0.15%, big-index +0.02%, large-files -0.21% (5 pairs, all unresolved) | yes (+29 to +63 ms per process) |
| R2 released pack stream | forget-prune on remote | quiet box: remote 3.410 to 3.524 s (-3.27%*, 0/10), requests 119 to 134, peak +4.9 MiB; main +0.27%, big-index +0.30%, large-files +1.39% (unresolved); peaks +6.4 / +3.0 / +3.0 MiB | yes; the 2 MiB variant would add requests, not built |
| R4 exact rule, first form | first backup, and prune (it saves blobs too) | main bi -8.30%* (+29.4 MiB), large-files bi -10.99%* (+22.8 MiB) | no: more than expected, and on four workloads, not one |
| R4 cost form (in `final`) | same | vs the sampler tree: main bi -2.39%* (+8.8 MiB), large-files bi -2.57%* (+8.4), large-files bm +2.20%*; forget-prune, quiet: main -1.96%*, big-index -1.40%*, large-files -3.83%*, remote +0.36% | the remainder is the encoder's own pass |
| F2 (a) stock's stored form | first backup, prune | -0.70 suite points (table above) | new |
| R5 | index load on remote | remote pass +0.41% (unresolved), +1.7 MiB | yes (none) |
| R7 | tree decode | main bu +0.21, rs -0.49, ck -0.33, ls -0.12 (all unresolved) | yes (tiny) |
| R3, R6, R8, R9, R10, R11, M22 | | not priced singly; inside the totals | |

**Total in final form** (repairs-only branch `x/R4d` = `repaired` + R4 cost form + R3 layout + F2 (a); no S020 piece, no port; vs `S019-merge`; 5 fixed pairs per cell, forget-prune repeated quiet with 10):

| | main | big-index | large-files | remote | mean |
|---|---|---|---|---|---|
| pass time | -2.00% (15.69 to 16.00 s) | -1.72% | -2.55% | -1.66% | **-1.98** |
| backup-initial | -6.07%* | -3.51%* | -7.43%* | -1.71%* | |
| forget-prune, quiet | -2.99%* (0/10) | -2.50%* (1/10) | -6.54%* (0/10) | -6.23%* (0/10) | |
| summed peaks | +71.9 MiB | +84.8 MiB | +39.2 MiB | +68.0 MiB | |

One small reading is unattributed: the repairs-only tree still reads ls-json -0.6 to -0.7% on main and large-files after the layout fix (2 to 6 ms per process, 5 pairs, one cell beyond its band).

## What went in (16 commits on `repaired`, in order)

| commit | what | source | non-test files |
|---|---|---|---|
| 05a0be7 | S020 prune: pack statistics per run of one pack | `incumbents/S020` (T2) | new `repository/prune_pack_run.go`; `prune.go` |
| 04a2449 | S020 prune: count checks on the set's arrays | S020 | new `index/associated_count_validate.go`; `prune.go` |
| 0740f00 | S020 index: repack listings by pack ordinal | S020 | new `index/repack_routes.go`, `repack_fallback.go`, `repack_ordinals.go`; `index/master_index.go` |
| 5c3d49b | S020 restore: second pass decodes trees without content IDs, WITH the R7 repair S020's file lacked | S020 | new `data/tree_metadata.go`; `restorer/restorer.go` (1 line) |
| ed23471 | S020 check: pack types collected during the index load | S020 | new `repository/checker_pack_types.go`; `checker.go` |
| 3e7c0df | R4 (cost): encoder destination allocated once from 512 KiB | this merge | new `repository/compress_presized.go`; `repository.go` (1 line) |
| cb4a2e9 | **A3**: check's extra worker only where it has a slot of its own | prototype `p-ckx`, condition tied to the mechanism | new `repository/checker_extra_slot.go`; `checker.go` |
| 83c3ebf | review: the stock listing loop nothing calls moves to the tests | REVIEW-s020 section 7 | `index/master_index.go`, `list_packs_bounded.go` |
| b9ac55c | review: the reviewer's four witness files as regression tests; comment at the second-pass loader | `review\witness-s020` | `restorer/restorer.go` (comment) |
| a1ce558 | **A16**: a repack worker reads one pack beside the pending saves of the pack before it | T3 S020 M004, re-fitted to R2 | new `repository/repack_completion_overlap.go`; `repack.go` (6 in, 92 out) |
| 61d8c0e | **A14**: backup formats a completed item's line only when it will be printed | T4 S016 M004, `ui/backup/text.go` hunk | `ui/backup/text.go` |
| 0dc6aea | **A5**: a prepared key keeps its two expanded AES keys | T4 S019 M004, cipher preparation only | new `repository/crypto/cipher_preparation.go`; `crypto.go`, `open_reader.go`, `repository/key.go` |
| 8a6a8d4 | R3 (layout): the scrypt checks run in front of the lanes function, not inside it | this merge | `crypto/parallel_scrypt.go`, `scrypt_params.go` |
| 10d1df4 | **F2 (a)**: a blob that does not compress is stored in compressed form, as stock | release check, coordinator's decision | `repository.go`, `compress_presized.go` |
| 3cddb8e | F1: the index tests compile for 32 bit | release check | test only |
| 7461eb0 | F3: the M22 test counts entries instead of sampling the heap | release check | `restorer/restorer.go` (the set became a field) |

Against `repaired`: 24 non-test files (11 new, 13 edited), 30 test files (27 new). `go.mod`, `go.sum` untouched.

Measured effect of each piece, alone, bench with fixed pairs:
- **S020's kept pieces** on top of `repaired` (5 pairs, beside other jobs): big-index check 3.630 to 3.160 s (+13.87%, 5/5); big-index forget-prune +25.39% (5/5; a prune cell beside other jobs: S020's own quiet figure is +6.70%); large-files pass -0.12%.
- **R4 cost form** vs `repaired`: main bi 5.442 to 5.125 s (+5.64%, 10/10), peak -15.7 MiB; large-files bi +7.34% (10/10), peak -22.3 MiB; large-files bm +1.35% (9/10).
- **A3**: remote check 6.845 to 6.761 s (+0.56%, 8/12, unresolved), peak -6.6 MiB, requests equal. Taken as a correctness fix: on every backend but the local one at its default connection count, S019-merge and S020 run one more check worker than there are connections.
- **A16**, forget-prune vs the tree without it. Quiet box, 10 pairs: big-index 2.300 to 2.223 s (+3.62%, 10/10, band 1.02), peak +2.4 MiB; large-files 1.228 to 1.100 s (+10.36%, 10/10), peak +3.6 MiB; remote +0.57% (6/10, unresolved), requests 134 = 134. Main: +4.33% (10/10) and +4.01% (10/10) in two runs that each overlapped other work for seconds (a guard bug), +3.79% (10/10) beside other jobs; T3's own ablation +4.34% (7/7). Oracle on all four workloads passes (same kept snapshots, stock `check --read-data`, stock restore manifest).
- **A14**, three backups, 10 pairs: main 8.87 to 8.80 s (+0.84%: bi +0.62, bu +0.56, bm +0.59, each unresolved alone), big-index +0.99% (bu +1.20%, 9/10, beyond its band), remote +0.66%, large-files -0.06% (few files). Positive in 11 of 12 cells; nothing printed changes.
- **A5**: ablation of T4's tree first (cipher preparation +5.02% on big-index check, 5/5; index decoder recycler +0.72%, unresolved, not taken). Ported alone: big-index check 3.182 to 3.046 s (+4.83%, 10/10); remote bi +1.16% (8/10); pass main +0.26%, big-index +0.66%, large-files -0.02%, remote +0.72%. Micro: Seal + Open of a 300-byte blob 988 to 571 ns, 11 to 5 allocations.
- **R3 layout**: ls-json, same code but for where the checks sit: large-files 0.452 to 0.445 s (+1.36%, 10/10), main +1.21% (9/10).

## What was left out, and why

**S020's three lock-wait mechanisms** (INVENTORY 8.1; verdicts of `review\REVIEW-s020.md`). Measured together first: `incumbents/S020` vs S020 without them, 10 pairs per cell: main -0.29%, big-index +0.04%, large-files +0.14%, remote -0.45%: mean **-0.14**: together they cost time.
- Backup compression preparation: DROP by the review (R1 again in its loader; it opens every source entry, FIFOs and device nodes included, before the lock is confirmed; built on the constants R4 deletes).
- Check reading one pack during the wait, with `retry/prepared_range.go`: DROP by the review (a pack is certified from bytes read before the lock on the strength of `stat` and inotify, which see nothing on NFS, SMB or FUSE mounts).
- Restore tree preparation: DROP by the review (switched off in S020 itself).
- Restore snapshot preload: allowed only with the R1 port and a clear gain. Its cells: remote restore +0.36% (10/10, 0.021 s), the other three unresolved around zero: about +0.02 suite points. Left out. **No lock-wait code was written in this merge, so no R1 wiring was needed.**

**INVENTORY section 1.**
- A1 (check reads packs inside the wait) and A22 (T1's form of it): lock-wait work that carries a clean verdict across the lock confirmation; S020's one-pack form is dropped and was worth about +0.05. Not built.
- A2 (tree pack in five parallel ranges): not built. Its gain exists where each connection has its own rate limit, which is the simulated link. On a link limited as a whole it gains nothing and always costs a size request and four more GETs per uncached tree pack; T4's related transfer stripes changed later GET outcomes under a refusal (S020 digest). For the operator.
- A4 (zero-write skip after fallocate): not built; at most +0.13 on one workload, unisolated, and no cheap bench exists without porting onto the rewritten file restorer.
- A5: cipher preparation taken; index decoder recycler measured unresolved (+0.72%) and not taken; the raw lock frame is not separable in that tree.
- A6 (first-pass tree prefetch, S015 prototype), A7 (prepare-ahead, closed for +69 MiB), A9 (S011-era parent cursor; its AES reuse is what A5 does for every caller), A12 (zero-chunk routes): not built; each sits in code rewritten since, and none has a cheap bench without a port.
- A8 (direct tree-node decoder): not built. It would be a third hand-written tree decoder; two of the existing ones needed the R7 repair.
- A10 (small-pack second repack worker), A11 (repack tail): not built; A16 is the form that measures positive on every local workload (A11 superseded per INVENTORY 8.1).
- M1 to M5: memory payers, score zero, not built (M1 is A13).

**INVENTORY section 10** (read from the box trees).
- A13 (T2 S016 `file_status.go`): the restore file list as sorted slices (M1). Memory only, and it sorts 91k paths per restore. The tree's other edit is a check-only connection count. Not taken.
- A14 (T4 S016): `checker_assist.go` is a decode assistant for check's extra worker, the installed lever: not taken. The `ui/backup/text.go` hunk is taken.
- A15 (T3 S016): a check-only connection count in `backend.Properties`, with a context mark in `internal/backend` (a layout-sensitive package); the installed extra slot's lever. Not taken.

**INVENTORY section 8.**
- A17 (T4, check filtering before descriptors): already in `final` in S020's own form (`ListPacks` through `repack_fallback.go` and `repack_ordinals.go` builds descriptors only for selected packs).
- A18 (T4, check read overlap behind a metadata gate): read. It is NOT lock-wait work (it starts under the confirmed lock); it runs the read-data pool beside the structure check and holds retry reports back (`retry/scoped_reports.go`). About 200 lines in the integrity command with message-order and memory questions and no isolated number (T4's rows suggest up to +0.3). Not built. Open.
- A19 (T1 restore metadata slots): not built; 0 to +0.13, not isolated, memory past the guard in one row.
- A20 (T3) and A21 (T4) prune collection: the lever S020's installed routing holds; not built.
- A23 (ls permission memo): not built; +0.01 suite points.
- A16: taken.

**Sections 2 and 3.** Nothing built. B3 (restore budget 6 MiB per worker) was swept: restore +1.12% main (8/10), +1.68% big-index (9/10), +1.41% large-files (unresolved) for 3 to 6 MiB on main's restore peak. Left for the operator: with the repairs, memory is the tight side.

**Tried and dropped.** The A14 idea at restore's printer (branch `p/A14r`, not in any league tree): restore main +0.15% (5/10), big-index +1.10%, large-files +1.08%, all unresolved: dropped.

## Soundness notes for the ports (the merge agent's claims; an independent review is in `review\REVIEW-final.md` when written)

**A3, check's extra worker** (`repository/checker_extra_slot.go`). Stock contract touched: none of check's verification. The change decides only which worker reads a pack. Every pack still goes to exactly one worker running stock's `checkPack`; a pack not offered to the extra worker goes to the ordinary channel, as every pack does in stock. Where the backend grants no additional load slot the worker set is exactly stock's. Test: `checker_extra_slot_test.go` (without the condition 1 / 2 / 5 connections read 2 / 3 / 6 packs at once; with it 1 / 2 / 5, and 3 with the slot).

**A14, item lines in backup progress** (`ui/backup/text.go`). Contract: output. `VV` prints at verbosity 3 and above only; the early return is taken only below 3, with the verbosity value the printer was built with, so no line is dropped; counters and JSON output are not on this path. Test: `text_complete_item_test.go` compares the terminal output with a verbatim copy of stock's function at verbosity 0 to 4 for every kind of item.

**A5, cipher preparation** (`repository/crypto/cipher_preparation.go`). Contract: encryption and authentication of every blob.
1. What is kept between calls is `aes.NewCipher(key bytes)` for the encryption key and for the MAC key: a pure function of the key bytes, which stock computes on every call.
2. Everything that depends on the call is computed per call as before: the CTR stream from the nonce, the Poly1305 key `r || AES_k(nonce)`, the MAC, and the MAC check before any plaintext is returned.
3. The kept pair is used only if its recorded key bytes equal the `Key`'s bytes at that call (both keys compared every time). No sequence of copies or field changes can make a call run with the schedules of other bytes; a `Key` never prepared takes the old path literally.
4. An aes block is not written after it is created; the pair is immutable and published through an atomic pointer.
5. No check was removed or reordered.
Tests: `cipher_preparation_test.go` keeps the unchanged `Seal` and `Open` bodies as reference: every ciphertext byte, every plaintext, and the verdict on one flipped bit in ciphertext, MAC or nonce, for prepared, unprepared and `NewRandomKey` keys over 12 sizes; 300 random key changes on a key and its copy; 16 goroutines on one key; race detector clean on the box. The oracle then checks the chain end to end (stock reads the candidate's repositories and the reverse).

**A16, repack completion overlap** (`repository/repack_completion_overlap.go`). Contract: prune's repack (also `copy`), the step after which prune deletes packs.
1. Stock's contract: repack returns nil only when every blob it was asked to keep has been handed to the uploader and reported saved; the caller then flushes, saves the index and only then deletes. The port keeps that: the worker loop returns only after `finish` or `drain` has run for every pack it read, and both wait for every save callback of that pack.
2. What changed is when the next pack is read: before the previous pack's saves have called back, not after. At most one pack is read ahead; the pack after it is not read until the first is settled; two packs are open together only if they hold at most 512 blobs between them.
3. Error precedence per pack is unchanged (read error, save error, cancellation) and packs are settled in order, so the first failing pack's error is returned. If a predecessor's save fails after the successor was read, the successor's saves are joined and the error returned: blobs may have been saved without need, which is harmless (nothing is deleted) and already possible in stock between two workers.
4. No new wait cycle: the pack is read through R2's `loadBlobsFromPackReleased`, so the handler never runs inside the backend `Load` callback and a worker waiting for the shared 8 MiB budget holds no connection. T3's file called the coalesced stream; that is the re-fit.
5. Memory stays bounded by the byte budget.
Tests: T3's four test files on the re-fitted code (held predecessor, failure, cancellation, descriptor bound, oversized plaintext); R2's `TestRepackHandlerRunsOutsideLoad` and `TestRepairPacksOneConnection`; a new model test (20,000 random pack sequences with read and save errors: every pack joined exactly once on every path, first failing pack's error returned, never more than one unsettled pack beside the one being read; it fails when the drain is removed or the bound relaxed); race detector clean. **Kill test with real binaries**, pinned to CPUs 2-5: `prune --max-unused 0` killed with -9 at a random moment, then stock `check --read-data` and a stock restore compared with `diff -r`, then the prune completed and both again: 30 runs, 20 killed during prune (15 in "repacking packs"), 0 failures; control with the stock binary 6 of 6. Open from T3's handoff and still open: several simultaneous upload failures on a real backend; a stalled uploader (the worker waits, as stock does).

**R4 (cost) and R3 (layout)** are not ports. R4 cost: same encoder, options and input; only the destination's capacity differs; `compress_presized_test.go` compares the bytes with `EncodeAll(data, nil)` in four compression modes and checks the destination is never outgrown. R3 layout: the lanes function is S019-merge's again byte for byte under the name `parallelScryptLanes`; `parallelScryptKey`, which KDF and R3's tests call, runs the same checks first, in the same order, before any lane; R3's tests pass unchanged.

## Findings

1. **The key derivation's alignment is decided by the league's own function, and `repaired` had it on the slow side.** The derivation runs in `repository/crypto.parallelSalsaXOR` (`parallel_scrypt.go`), not in `x/crypto`'s `scrypt.salsaXOR`, which the earlier check and the merge-S019 tools watch. `S019-merge`: 0x823ba0, `S020`: 0x7effa0, both 32 mod 64. `repaired`: 0x823d80, 0 mod 64, while `scrypt.salsaXOR` sits at 32 mod 64 (0x81f120) in every tree. Cause from the symbol tables (`tools/nmdiff2.py`): R3's checks inlined into `parallelScryptKey` (1,061 to 1,605 bytes: 17 steps of 32 bytes in front of the loop), `debug.Log` back to stock (2 steps less): net 15, odd. It showed as ls-json on large-files -1.65% (0/3) in the official price job and -1.21% (0/5) in a preview, with no ls code changed.
2. **S020's ls-json loss is the binary, not a code path.** None of the three hooks on ls's path costs it alone (ls, 10 pairs, main / big-index: retry hook +1.25 / +0.66; `wait_index_load.go` -0.21 / -0.08; `repository.go` hunks with the compression file +0.43 / -0.30). With all lock-wait files in, ls reads -0.81% (1/10) main, -2.16% (2/10) big-index, -0.65% (1/10) remote, and the binary is laid out differently as a whole: `salsaXOR` 211,968 bytes earlier, with `internal/options` and the inotify syscalls now placed before the key derivation.
3. **S020's unsound parts bought nothing.** The three lock-wait mechanisms together cost 0.14 suite points.
4. **The M22 regression test was flaky, not the restore** (3 of 20 fail alone on four CPUs, on `repaired` as on the merged tree). It is now exact (F3).
5. **Prune cells did not depend on what ran beside them in these benches**: A16 beside / quiet +3.42 / +3.62 (big-index), +10.13 / +10.36 (large-files); R2 remote -3.12 / -3.27; repairs' prune price beside / quiet -3.04 / -2.99, -1.96 / -2.50, -5.36 / -6.54, -6.33 / -6.23. The S020 distortion came from two judge suite jobs at once; a bench beside one judge job did not show it. The 44-round job still ran alone.

## Rules check, tests

- League rules: stock's user-visible defaults unchanged (no sparse restore, no change of `--read-concurrency` or connection defaults, no change to change detection); output, exit codes and every integrity check unchanged; **repository format and now also the stored form of every blob as stock's** (F2); the 200 ms lock wait untouched; snapshots listed before the index; nothing from INVENTORY sections 2 or 3 built; no busy-waiting; only non-test `*.go` under `cmd/` and `internal/` plus own tests changed.
- **Gates of the brief** (44 rounds): oracle on every workload PASS; compatibility tests PASS; own tests PASS; positive against stock on every workload PASS. Time vs S020 net of the repairs' measured price (-2.00 / -1.72 / -2.55 / -1.66): +1.02 / +1.74 / +1.32 / -0.08: no workload pays 1%. **Memory vs S020 net of the repairs' measured price (+71.9 / +84.8 / +39.2 / +68.0 MiB): +4.2 MiB main, -5.9 big-index, +14.7 large-files, -10.1 remote, against a 2% allowance of 11.4 / 31.5 / 10.9 / 12.2 MiB: large-files is over by about 4 MiB on this arithmetic.** The 12-round job read large-files +37.9 MiB in all, which is under it; the exemption is a 5-pair bench figure; the ports' own large-files peaks measure +3.6 MiB (A16) and nothing resolved otherwise. Reported as a possible miss; nothing in the tree is known to cause it. Without the exemption the league's 2% guard is exceeded on every workload, by the repairs.
- The judge's league verdict on the 44 rounds is "not eligible" (below S020, memory guard, 7 wins of 29), as the brief allows.
- Own tests, final tree, on the box as root (`tools/fullchk.sh`): every package: 51 packages ok, 9 without tests; four tests fail and the same four fail on the stock tree in the same environment (`TestBackupErrors`, `TestStdinFromCommand`, `TestStdinFromCommandNoOutput`, `TestArchiverErrorReporting`: root reads unreadable files; no `python` executable); **failing here and not in stock: none**. `cmd/restic` also passes at the production wait (`RESTIC_TEST_LOCK_WAIT=200ms`) but for those three.
- R1 on the final tree: the six witnesses pass; `TestLockWaitSchedules` with 3,000 schedules: 0 failing (4,043 confirmed locks, 3,402 read during the wait, stock-order run 0); settle delay, verdicts, request counts pass.
- Stock tests overlaid (the judge's rule), all 53 packages: 1,683 pass. Judge's per-phase oracle: 7 phases x 4 workloads pass. Race check (quick): TestRestore 0/40, restore latest 0/20, ls latest 0/20 during 26 concurrent stock backups.
- Race detector (`-race`): repository, crypto and index packages on the repack, prepared key, check worker, prune and listing tests: clean; `cmd/restic` lock-wait tests: clean.
- `go vet ./...` clean on linux and darwin (windows: two lines in a file identical to stock's). `gofmt -l cmd internal`: only `internal/backend/s3/s3_test.go`, byte-equal to stock's. linux/386 builds; `GOARCH=386 go test ./internal/repository/index` builds and passes (F1); the new code's tests pass as 386. All 25 release targets build (`merge\fetch\build-matrix-final.log`).
- F2 tests: `TestCompressionAutoStoresWhatStockStores` (80 blobs, 17 not shrunk by the encoder, all stored compressed at stock's size; on the raw rule 15 are stored uncompressed) and `TestPruneRepackUncompressedComesToRest` (second run: 0 blobs; on the raw rule 18 again). F3 test: at most 3 entries with the M22 repair, 3,011 and 12,041 without.

## Layout result

Judge's build of the final tree: `crypto.parallelSmix` 0x8230e0 (32 mod 64), `parallelBlockMix` 0x823a00 (0), `parallelSalsaXOR` 0x823d20 (32): the residues of S019-merge and S020; `scrypt.salsaXOR` 0x81f120 (32). Official confirmation: ls-json on large-files +0.21% (8/11) against S020 at 44 rounds. Fixed the merge-S019 way: nothing is added in front of the derivation. It restores one build's layout and is not a property of the code: a release binary for another platform or toolchain lands where it lands; later edits to `crypto.go`, `open_reader.go`, `kdf.go` or `cipher_preparation.go` (files in front of the loop) can flip it again. A release build script should check `crypto.parallelSalsaXOR`.

## Open items

1. **F2 (a) and the operator.** Stock's stored form costs 0.70 (bench) to 0.79 (official) suite points and +28 to +52 MiB of summed peaks. A streaming form of the compressed save-verify (authenticate, decrypt as a stream into a reused streaming decoder, hash; no full copies) would keep stock's form and the same check; the agent's estimate is most of the memory and perhaps half of the time. Not built, by the coordinator's order: it is new code in the verification-on-save path. Candidate for a later release. Variant (b) of `checks\FAILURES.md` is not built either.
2. **Large-files memory**, see the gates.
3. Left for decision: A2 (request pattern on real backends), B3 (restore budget), A18 (check read overlap; needs its own review), A1 / A22.
4. R1's remote price could fall by one listing in ls and check (the overlap its report parks); R2's by larger ranges at a memory price. Neither touched.
5. The `repaired` branch as staged (01d1452) still has the slow alignment, the raw rule, the flaky test and the 386 test build failure. The fixes are commits on `final`; the repairs-only tree with all of them is branch `x/R4d` in `work\final`.
6. A16: several simultaneous upload failures on a real backend and a stalled uploader are not exercised. Its main reading has no fully quiet run.
7. Not run: the operator's held-out workload; macOS, BSD, Windows (builds only); REST / SFTP with 1 and 2 connections for A16 and R2.
8. Process notes: an unpinned kill test ran 05:51:30 to 05:55:45Z and may have touched the other agent's CPUs (reported then; no judged round overlapped; A5's and one A14r bench did and were treated as screens, A5 being confirmed by an earlier clean ablation and by both official arms). The quiet-job guard had a bug for the last job of the first quiet block (fixed in `tools/worker3.sh`). Two commits had contents that did not match their messages, so `final`'s history was rewritten at the end with the tree unchanged (d708925 became 7461eb0).

## Files

- Box: `/opt/league/restic-final/`: `final` (worktree, branch `final`, 7461eb0), `src/repaired` (the repository), `trees/*` (exports of every measured tree; `rc-5` is the judged one), `out/*.json` (246 bench and check results), `logs/`, `PROGRESS.log`, `tools/`. No unit alive, lane workers stopped, no lane lock held, nothing in /dev/shm.
- Bundle: `final-candidate\work\final.bundle` (`base` 4579a38, `repaired` 01d1452, `final` 7461eb0; `git bundle verify`: complete history). The public patch series is `repaired..final`, 16 commits.
- Local repository: `final-candidate\work\final` (also the measurement branches: `p/A3`, `p/A5`, `p/A14`, `p/A14r`, `p/A16`, `x/abl-R1` … `x/abl-R7`, `x/R4b`, `x/R4c`, `x/R4d`, `x/s020-nolw`, `x/ls-*`, `rc/1` … `rc/5`).
- Evidence: `evidence\restic-final-20261007\<job id>\` for `3c21cf80…`, `aca189ed…`, `757b745c…`, `c87fa304…` and the two void ids; `jobs.json` maps names to ids.
- `final-candidate\merge\`: `PROGRESS.md`, `STATUS.json`, `tools\` (launcher, table and box tools), `fetch\box-out.tgz` (all bench outputs, the box log, kill-test logs), `fetch\build-matrix-final.log`, `fetch\s020teams\` and `fetch\s016\` (the team diffs read for the ports).

---

# Addendum (2026-10-07 20:35 UTC): what changed after 7461eb0

Saved by the coordinator from the merge agent's hand-back (its session does not write report files). The report above
was written for tip 7461eb0 (`final`). Published commit: `final3` = 8f9372b (export rc-9). Judged commit: b8cf439
(export rc-8). The merge agent's last process on the box ended 2026-10-07T20:31:34Z; its last measurement ended
20:27:16Z.

## A1. Commits

`final` stays at 7461eb0. `final2` = d4f7e81 (four commits: F5 9ca10f2, A5 witness 9ed8618, A16 save-error tests
3509049, F5 bound 524,288 IDs d4f7e81), judged as rc-7. `final3` adds twelve commits:

| commit | what | finding |
|---|---|---|
| c89be23 | fix 9: prefetch budget counts the hidden tail of extended attributes | red-team seat 3 finding 4 |
| 5b2d540 | fix 8: batched terminal output keeps message boundaries | red-team seat 3 finding 3 |
| 02ee138 | fix 3: index releases its per-file notes when cleared | red-team seat 2 finding 5 |
| 548f142 | fix 7: index file kept unread by the rewrite is read raw and compared with its name | red-team seat 2 finding 3 |
| a405f78 | fix 2: repack and copy save on the group context | red-team seat 2 finding 4 |
| 35e89b5 | fix 1: check's streaming decode keeps upstream's 64 GiB ceiling | red-team seat 2 finding 1 |
| 02e3df1 | fix 6: kept files compared by size too; check has no wait work | red-team seat 1 R1, seat 2 finding 2 |
| abda288 | fix 4: validating listings taken where stock lists | red-team seat 1 R2 |
| 0f32644 | fix 5: wait-work diagnostics held until the reads are used | red-team seat 1 R3 |
| 8669c54 | restore hands work over per batch, not per directory | many-core finding |
| b8cf439 | `var Version = "0.19.1-dev-dealer.1 (compiled manually)"` | release; JUDGED |
| 8f9372b | kept-index contract stated exactly; reviewer's fix 2 witness as a test | review; PUBLISHED |

Not changed, documented only: red-team seat 3 finding 1 (restore reuses trees verified in the first pass), and the
stored encoding of index and lock files (seat 2 finding 6, seat 3 finding 2).

Fix 4's commit message (abda288) still carries the overclaim "with any backend this build knows what stock knows";
history was not rewritten. The source comment is corrected in 8f9372b.

## A2. Judged commit and published commit

- The league judged b8cf439. The published commit 8f9372b differs by one comment (`cmd/restic/wait_index_load.go`) and
  one test file (`internal/repository/repack_scope_test.go`).
- Built as the judge builds, the two binaries are the same size (32,542,880 bytes) and differ in 60 bytes: 40 in
  `.note.go.buildid`, 20 in `.note.gnu.build-id`.
- Every other section is byte-identical, including `.text` (sha256 95b350a4…), `.rodata`, `.gopclntab`, `.noptrdata`
  and `.data`.
- The output halves of the Go build ID are identical; only the input halves differ, because Go hashes the source text,
  comments included, into the binary.
- SHA-256: rc-8 4095e7ce…41fe7a (the value both official jobs recorded for the candidate), rc-9 958d9ff2…5d3ed3.
- The comment had to keep every code line on its line number, so the full contract text stands at the end of the file.
- Proof files: `Leagues\evidence\restic-final-20261007\proof-rc9\` (PROOF.txt, diff.offsets, proof.sh).

## A3. Things stated exactly

- **Fix 1.** The reviewer's quick test sets its 16 KiB ceiling on the zstd decoder, which a stream cannot read back.
  The carried test sets the same ceiling on the iterator too (one line). The production limit is upstream's 64 GiB,
  same error text. The 64 GiB + 1 byte test passes in 37 s.
- **Fix 2.** Using the group context alone is wrong. The saves run in the uploader's group, so a save cancelled by the
  read error reports "context canceled" first and the command prints that instead of the read error (10 of 10 runs).
  The repack's context is marked so such a save reports nothing to the uploader's group. The carried test compares the
  error text with the stock control.
- **Fix 5.** Diagnostics of wait-work reads are held until the reads are used, at the point where stock loads the
  index. They are dropped when the lock is refused or the reads are discarded after confirmation.
- **Fix 6(b).** `check` has no wait work at all: stock's lock call, stock's order. Five test files of check's wait
  work and the checker's `LoadSnapshotsFromList` are deleted. The reviewers' two lifecycle tests drove that removed
  interface, so their scenario is carried at command level (truncation and same-size flip, with and without cache;
  4 of 4 fail on final2).
- **Fix 6(a) residual.** The other commands compare name and size. Damage that leaves the length alone is not seen
  without re-reading. Stock reads index files through its cache and does not see it either unless the cache is off.
- **Fix 8.** A batch write failing exactly on a message boundary has two readings: the error came after the complete
  message, or on the next message with nothing written (a full disk). Both now match stock: the next message is
  written on its own, and a repeat of the same error is reported once.
- **Kept-index contract (8f9372b).** The index read during the lock wait is validated by a listing, name and size,
  taken where stock lists. It is not re-read. On a backend whose listing still shows a deleted file, an index file
  removed by a concurrent prune goes unnoticed; stock with the file in its local cache (the default) behaves the same.
- **Reviews.** `repaired..final`: nothing BLOCKER, HIGH or MEDIUM, every port KEEP, 17 of 17 mutations caught.
  `final2..final3`: nothing above LOW, KEEP all eleven, 23 of 25 mutations caught; the two misses are now covered by
  `TestAsyncSaveResultSuppressionIsNarrow`.

## A4. Official results (judged commit b8cf439 against S020)

| | 12 rounds (9fc9eafd) | 44 rounds, no other judge job beside it (50388f4e) |
|---|---|---|
| units verified | 347/347 | 1,019/1,019 |
| compatibility tests, cross reader | passed, ok | passed, ok |
| vs stock | +29.330% (12/12; +29.04..+29.45) | **+28.995% (44/44; +28.84..+29.05)** |
| vs S020 | -1.066% (2/12) | **-1.063% (6/44; -1.13..-0.91)** |

44 rounds per workload:

| | main | big-index | large-files | remote |
|---|---|---|---|---|
| vs stock (11/11 each) | +30.63% | +45.13% | +22.96% | +17.26% |
| memory saved vs stock (+38.99% overall) | +34.90% | +44.19% | +34.08% | +42.79% |
| vs S020 | -0.83% (1/11) | -0.18% (5/11) | -1.07% (0/11) | -2.18% (0/11) |
| summed peaks vs S020, MiB | +76.7 | +82.8 | +41.1 | +57.3 |

Against rc-7's 44 rounds (940c4e64: +29.076% vs stock, -0.788% vs S020), rc-8 is 0.08 points lower against stock and
0.27 lower against S020 (a difference between two jobs, not a paired measurement). The release-check harness was at
work on other CPUs of the same machine during both official jobs.

## A5. Cost of the fixes

Nine-fix tree against rc-7, 8 fixed pairs, 4 cores:

- **Fix 4**, restore on remote: -0.30% (1/8, unresolved), same 91 requests.
- **Fix 6**, check: big-index -5.31% (0/8, beyond noise, -0.83 points on that pass); main -0.98%, large-files -1.26%,
  remote -0.98% (all unresolved; remote makes 90 requests instead of 92).
- **Fix 7**, forget-prune on big-index, quiet box: -1.08% (1/8, unresolved).

## A6. Checks of the tree

- The reviewers' original test files, unchanged, on final2: 12 of 12 tests of the nine findings fail. The carried
  tests pass on final3.
- Own tests of the touched packages: 0 failures. All packages against stock in the same environment: nothing fails
  that does not fail in stock (four root-only tests).
- Lock wait: R1 witnesses 6/6; 3,000 schedules with 0 failing; settle delay, verdicts and requests 3/3.
- Oracle 7 x 4 passed (28/28), also with the new program version. Stock tests overlaid: 1,683/1,683.
- Race detector clean on termstatus, retry, index, repository and the cmd/restic lock-wait tests. go vet clean;
  linux/386 builds and tests.
- Layout: `crypto.parallelSalsaXOR` at 0x8241a0, 32 mod 64.
- Commit 8f9372b itself: its new test, vet and gofmt were run. The full checks ran on the ten-commit tree, with build,
  vet, own tests, oracle, compatibility tests and race quick repeated on b8cf439.
- `gofmt -l` reports only `internal/backend/s3/s3_test.go`, as it did for rc-7.

## A7. Release binaries

- Version 0.19.1-dev-dealer.1, built from 8f9372b with the judge's flags and environment plus
  `-X github.com/restic/restic/internal/global.Version=0.19.1-dev-dealer.1`.
- The build procedure reproduces the judge's binaries bit for bit (rc-7, rc-8 and stock match the recorded
  `binary_sha256` values).
- SHA-256: linux/amd64 753924db…, linux/arm64 7ed7c72b…, darwin/amd64 516332fc…, darwin/arm64 e26c5e0b…,
  windows/amd64 7b4538e1… (full values in `release\binaries\SHA256SUMS`).
- linux/amd64: `parallelSalsaXOR` at 0x8241a0 (32 mod 64).
- The release binary's `.text` (eaa7538f…) is NOT byte-equal to the judged `.text` (95b350a4…), though the same size.
  The -X string is 20 bytes shorter than the source value, which moves read-only data and changes address operands.
  All 37,880 text symbols sit at the same addresses with and without -X.
- `ls --json` of a 90,000-file snapshot, release binary against the judged binary, 20 fixed pairs: -1.78 ms (-0.19%),
  12/20: no difference resolved.
- A source build reports "0.19.1-dev-dealer.1 (compiled manually)".
- Known limit: `restic self-update` in this build would fetch upstream's release; the command is unchanged.

## A8. Many cores (see MANY-CORE.md)

- Every league timing, the 44 rounds included, ran with the `performance` frequency governor.
- rc-9 against stock at 8 and 16 whole cores, 5 fixed pairs per cell: all 56 cells are 5 of 5, none slower than stock.
  The smallest is remote check-read-data at 16 cores, +5.51%.
- Pass estimates at 8 / 16 cores (bench tool's estimate, not a judge score): main +31.47 / +33.03, big-index
  +44.99 / +46.27, large-files +28.86 / +32.11, remote +17.64 / +18.51.
- In restore, forget-prune and ls the tree uses more user CPU than stock, and the gap widens with cores (main restore
  6.3 / 5.6 s at 8 cores, 6.9 / 5.7 s at 16).
- Per-pair files: `Leagues\evidence\restic-final-20261007\many-core-rc9\` (23 files, SHA256SUMS).

Many-directory restore (606,060 directories + 3 files):

- **Mechanism:** one hand-over between goroutines per directory in both restore passes; 1.30 million voluntary context
  switches against stock's 0.09 million.
- **Commit 8669c54**, performance governor, before -> after (stock):

| set-up | before | after | stock |
|---|---|---|---|
| tmpfs, 4 cores | 28.7 s | 25.8 s | 34.4 s |
| tmpfs, 8 cores | 30.5 s | 26.7 s | 35.0 s |
| tmpfs, 16 cores | 31.6 s | 27.4 s | 35.6 s |
| ext4, 4 CPUs | 47.6 s | 41.8 s | 51.7 s |
| ext4, 16 logical CPUs | 50.4 s | 43.0 s | 51.7 s |

- **Release-check harness** (schedutil governor, 16 CPUs, run beside the official job): the judged commit was 10.1%
  slower than upstream at 606,060 directories and 4.8% slower at 2,020,200 (one run per build); 26.8% faster on
  4 cores.
- **L3 test:** three performance-governor set-ups, compact and spread over four L3 domains, were 16 to 17% faster than
  stock at 16 CPUs. Cache layout is ruled out.
- **What remains:** the governor and the neighbouring job. A governor test on the check harness's CPUs, authorised by
  the operator, was running when this was saved; its result is added to MANY-CORE.md section 2.3.
- The restore-cell bench for 8669c54 (main +1.01%, big-index +1.66%, large-files -0.35%, remote +0.83%) ran beside
  other runs; the official jobs are the measurement that counts.
- Not measured: the directory-ONLY restore (no files at all) after the hand-over commit. The "about 6.7% slower"
  figure (505,050 empty directories: 60.3 s against 56.5 s) is from rc-7.

## A9. F5 numbers (final2, for the record)

- Restore of 2,020,200 directories, peak RSS: stock 68.2 MiB, final 231.0, rc-7 57.4 (with three files:
  67.5 / 230.6 / 57.3). Manifests identical.
- Bound table (606,060 directories + 3 files, wall / RSS):

| tree-ID bound | wall | RSS |
|---|---|---|
| 65,536 | 74.0, 70.1 s | 56.2 MiB |
| 262,144 | 57.5, 58.4 s | 56.2, 58.5 MiB |
| 524,288 (chosen) | 53.3, 51.2 s | 57.0 MiB |
| none | 52.8 s | 80.0 MiB |
| stock | 64.2 s | 63.0 MiB |

## A10. Where things are

- Local repository `final-candidate\work\final`: branch `final3` at 8f9372b, clean. `work\final.bundle` = base
  4579a38, repaired 01d1452, final3 8f9372b (verifies).
- Official evidence, `Leagues\evidence\restic-final-20261007\`: `9fc9eafd689448c3ad5220374cffd888\` (12 rounds),
  `50388f4ecf434b0d8663aa4a2a740d95\` (44 rounds), `proof-rc9\`, `many-core-rc9\`,
  `release-checks-final3-b8cf439\`, `upstream-identity\`, `machine.txt`.
- Release binaries, `release\binaries\`: five targets built from 8f9372b, SHA256SUMS verified after download.
- `final-candidate\merge\`: MANY-CORE.md (generated by `tools\mkmanycore.py` from `tools\manycore_tail.md`),
  PROGRESS.md, STATUS.json, `fetch\box-out-2.tgz` (wider box evidence).
