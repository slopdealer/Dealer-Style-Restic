# Restic League V2: inventory for the final candidate

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Compiled 2026-10-06 from local records only (no box access, no builds, no benchmarks). Base tree: `incumbents/S019-merge`
(suite +28.99% vs stock = main +31.11 / big-index +41.96 / large-files +24.22 / remote +18.68; memory +43.16%).
Progress log: `INVENTORY-PROGRESS.md`. S020 is still running: its results are not in this file (section 8).

**Counts.** A: 12 ports with code (3 solid, 9 weak or unconfirmed) plus 5 memory payers. B: 10 gains blocked by a
league rule or ruling. C: 17 levers never built. D: closed ground in 14 groups. E: 21 points to re-examine.

## 0. How to read this file

**Scale** (`cost-map-10/SPEED-MAP.md` section 0, measured on `incumbents/S019`): 1 s saved = **+1.61 suite points on
main, +1.32 on big-index, +1.99 on large-files, +0.80 on remote**. A suite point is 1% of time saved against the
incumbent, averaged over the four workloads. One point against the incumbent is about 0.71 points against stock.
My estimates ("est. now") are seconds from the cited record times these factors. They are estimates, not measurements.

**Memory guard** (same source): +2% of the 7 summed phase peaks per workload = about **11.3 MiB on main, 31.4 on
big-index, 10.8 on large-files, 12.2 on remote**. The guard is measured against the incumbent, so a final candidate
judged against `S019-merge` starts with the full amount. The directive advises spending half.

**Evidence tags.** Results in this league have inverted between screens and confirmations. Numbers in sections 1 to 3 carry one:

| tag | meaning |
|---|---|
| `[44r]` / `[42r]` | official promotion job, 11 or 14 rounds per workload: confirmed |
| `[10p]` / `[5p]` | builders' `bench` on a research lane, 10 or 5 fixed pairs: measured, not judged |
| `[row]` | ONE match row (2 rounds per workload): **unconfirmed single screen** |
| `[old:Sxxx]` | measured on an older incumbent; the code under it has changed since |
| `[bound]` | a knob or forbidden edit used to size a lever; not a candidate |

**Sources** (all under ``): `CG-x` = `cost-map-10/data/closed-ground.md` section x;
`SM10`, `SM9`, `SM8`, `SM7`, `SM6`, `SM5`, `SM4` = `cost-map-N/SPEED-MAP.md`; `M19-R` / `M19-P` =
`merge-S019/REPORT.md` / `PROGRESS.md` (with the UTC time of the line); `M17-R` / `M17-P` = `merge-S017/`;
`DIR20` = `runtime/S020-directive.md`; `UNP18` = `cost-map-9/data/s018-unpromoted.md`; `RF` = `race-fix/REPORT.md`;
`REM` = `judge/REMOTE.md`; `BTB` = `reviews\BACKUP-TOOLS-BENCH\RESULTS.md`; `REV-Sxxx` =
`reviews/Sxxx-review.md`; `INT` = `INTERFACE.md`.

**Box paths** (not reachable from here; cited by pattern for the merge agent):

| short | path |
|---|---|
| `TEAMS/<T>/<S>/<M>` | `/opt/league/restic-league-v2/league/space-bunny/restic-v2/workspace/teams/<T>/<S>/<M>/candidate-team` (review copies `candidate-review-B<nn>` beside it) |
| `INC/<name>` | `/opt/league/restic-league-v2/league/space-bunny/restic-v2/workspace/incumbents/<name>` |
| `CM10/p-x` ... `CM8/p-x` | `/opt/league/restic-costmap10/src/p-x`, `restic-costmap9/src/p-x` (also `src/T?-M00?`, copies of S018 team trees), `restic-costmap8/src/p-x` |
| `CM7`, `CM6`, `CM5`, `CM4` | `/opt/league/restic-costmap7/` (`src/`, `bin/`), `restic-costmap6/`, `restic-costmap5/`, `restic-costmap4/` |
| `MERGE19`, `MERGE17` | `/opt/league/restic-merge-s019/` (`final`, `final-b4`, `steps/*`), `/opt/league/restic-merge-s017/` (`final`, `steps/{fp,fpdec,fpdecck,all}`). `M19-R` says `final-b4`, `steps/`, `nm/`, `work/` "can be deleted": they may be gone |

Local copies of the code exist for most rows and are named in the tables: prototype diffs in
`cost-map-N/data/protos/*.diff`; S019 team M004 diffs in `cost-map-10/data/s019/diffs/`; S018 team diffs in
`cost-map-9/data/s018/`; S016 early diffs in `cost-map-7/data/s016/`; merge sources in `merge-S019/diffs/` and `src/`.

**"Not installed" checks.** Where a row says "checked", I searched `final-candidate/trees/S019-merge` (73 new and 43
edited non-test `.go` files against `trees/stock`) for the idea's file, function or constant.

## 1. Port list (A): measured gain, code exists, inside the league's rules

### A-1. Solid: measured on `incumbents/S019` with 5 to 12 pairs

| id | idea | source and code | files | measured effect | why not installed | overlap or conflict with the incumbent | est. now (suite points) | memory | effort | release risk |
|---|---|---|---|---|---|---|---|---|---|---|
| A1 | **Check reads packs inside the lock wait.** A pack that verifies clean is skipped by the ordinary pass; a pack with any error is read again and reported as before; nothing is printed early | prototype `p-ckwait` (ROUGH), `CM10/p-ckwait`; local `cost-map-10/data/protos/p-ckwait.diff` | new `repository/cm10_check_preread.go` (80 lines) + hooks in `repository/checker.go`, `cmd/restic/cmd_check.go`, `cmd/restic/wait_index_load.go` | check **-0.119 s main (5/5), -0.113 s large-files (5/5), -0.173 s remote (11/12)**; **big-index +0.120 s (0/5), +11.8 MiB**; oracle passes on all four, corrupted pack detected `[5p]`/`[12p remote]` (SM10 section 4; CG-S) | prototype from the last map; no team had built it before S020 | Shares the wait with the installed index load and check pack sizes, and `cmd_check.go` with the early structure pass (`check_structure_overlap.go`). As built it lists the packs of a large index a second time and keeps reading beside the structure pass: stop at the confirmed check or share one listing. Checked: no pre-read file in the incumbent | **+0.55** if big-index is made neutral (0.19 + 0.22 + 0.14); +0.40 as built | -2.0 / +11.8 / +1.5 / -1.9 MiB | M | Medium. Work inside the lock wait must be read-only, discarded when the lock check fails, and safe beside a concurrent writer (rulings 1 and 7). Skipping the second read of a clean pack must not weaken `check --read-data` |
| A2 | **The tree pack read in five parallel ranges on a remote backend** (a pack the cache fetches whole) | prototype `p-treepar` (ROUGH), `CM10/p-treepar`; local `protos/p-treepar.diff` | `backend/cache/backend.go` (45 lines) | remote bu **-0.071**, ls **-0.074**, rs **-0.081** (10/10 each), ck -0.076 (8/10), bm -0.028 (7/10), fp +0.026 (2/10); memory flat; not active locally (two connections) `[10p]` (SM10 section 4) | prototype only | None: `backend/cache/backend.go` is unchanged from stock in the incumbent (checked) | **+0.24** as built; about **+0.35** without the size request (the index knows the pack's size) | flat | S | Medium for users: 5 more requests per process. On a pay-per-request backend that is cost, and the gain depends on spare connections. Needs a rule for when to split |
| A3 | **The extra check worker takes nothing when the backend has more than two connections** | prototype `p-ckx` (TEST), `CM10/p-ckx`; local `protos/p-ckx.diff` | `repository/checker.go` (one condition) | remote check **6.65-6.73 s in 12 runs** (incumbent in the same job 6.63-6.92), paired -0.048 s (9/12), **peak -6.3 MiB**; local unchanged by construction; oracle passes on remote `[12p]` (SM10 section 4; CG-S) | test prototype, built to find the cause of the two-valued remote check | Touches the same worker set-up in `checker.go` as A1 (small). The extra worker has its own slot only on the local backend (`sema.WithAdditionalLoadSlot`); elsewhere six workers share five connections (checked: `local/config.go` grants the slot only at `Connections == 2`) | **+0.1 to +0.2**, and it takes a 0.3 s coin out of every remote check | -6.3 MiB on remote check | S | Low. It is also a fix: on every non-local backend the incumbent runs one more check worker than it has connections |

### A-2. Weak or unconfirmed: code exists, but the number is old, unisolated, a single screen, or mixed

| id | idea | source and code | files | measured effect | why not installed | overlap or conflict with the incumbent | est. now (suite points) | memory | effort | release risk |
|---|---|---|---|---|---|---|---|---|---|---|
| A4 | **Zero-write skip in restore**: a new file is created exclusive and `fallocate`d; a verified zero chunk inside that extent is not written (the blob is still downloaded, decrypted, verified) | T3 S018 M004, `TEAMS/T3/S018/M004`; copy `CM9/T3-M004`; local `cost-map-9/data/s018/T3-M004-vs-S018.diff` | new `restorer/allocated_zero.go`; hunks in `fileswriter.go`, `filerestorer.go` | large-files restore **-0.064 s** (5/5, inside the band) against S018, in a tree that also carried decode buffers: **not isolated** `[5p][old:S018]` (SM9 section 7; UNP18 T3-D) | "not ported (time)"; listed OPEN (M19-R; CG-T) | `filerestorer.go` now carries the third-worker hunks of the merge; `fileswriter.go` carries the opens without the poller. Checked: no `allocated_zero` in the incumbent | **at most +0.13** (large-files only) | flat | S to M | Medium. Any other write to the range must revoke the proof. Sound only where `fallocate` really yields zero-filled blocks; other platforms must stay on the stock path |
| A5 | **Index decoder recycler, prepared cipher schedules, lock verify by raw frame** | T4 S019 M002-M004, `TEAMS/T4/S019/M004`; local `cost-map-10/data/s019/diffs/T4-M004-vs-S019.diff` | new `repository/index_decoder_recycler.go`, `repository/crypto/cipher_preparation.go`, `repository/verify_lock_raw_frame.go`; edits in `crypto/crypto.go`, `crypto/open_reader.go`, `repository/key.go`, `index_load_ordinary_stream.go` | big-index check **-0.101 s in M002** (the tree held the cipher preparation only), **-0.030 s in M003** (both), **-0.096 s in M004** (both); M004 check big-index +2.72%, main +1.12%, while the incumbent's own check is 0.065 s behind S018's `[row x3]`, **not isolated** (CG-R; `cost-map-10/data/s019-rows.txt` file lists; SM10 section 7; M19-P inventory row 11). The cipher preparation is in both good rows, the recycler in one | "not looked at (time)"; OPEN (M19-R; CG-T) | New files; the recycler hooks the index load, which the incumbent decodes with `index_decode_canonical_stream.go`. None of the files is in the seven layout-sensitive packages. Checked: none of the three files in the incumbent | **0 to +0.15** (big-index check, plus about +0.03 on main). Run `ablate_changes` first | not attributed (T4 M004 read memory -1.9% main, -2.1% large-files for the whole tree) | M | Low to medium (cipher state reuse must stay per key and per goroutine) |
| A6 | **Bounded first-pass tree prefetch in restore**: depth-first order predicted with a stack, 3 loaders, at most 64 decoded trees held | prototype `p-pf1b`, `CM7` (`src/`, `tools/proto_edit7.py`); local `cost-map-7/data/protos/p-pf1b.diff` | `restorer/cm7_pf.go`, `restorer.go` (first traversal) | restore **-0.154 s (8/8), memory flat (-2.3 MiB)** on main `[old:S015]`, clock-held lane; stock restorer tests pass (SM7 section 7; CG-L) | "Partly installed": S016 added a node lookahead instead (`restorer/node_lookahead.go`). Nobody re-measured the prefetcher after it | Pass 1 is still serial: **0.40 s at 1.25 cores on main, 0.39 s on big-index, 0.56 s on remote** on S019 (SM10 sections 2 and 6), against about 0.42 s on S015. So most of the lever may remain. Conflicts with `node_lookahead.go` and with the restore lock-wait job (C1) | **+0.1 to +0.4** (main and big-index). Unconfirmed on the current tree | flat as built | M | Low to medium. Pass 1's error order must hold: an error surfaces when the walk asks for that tree |
| A7 | **Restore prepare-ahead**: while a decoded blob of 256 KiB or more is hashed, a goroutine reads and decrypts the next blob | T4 S018 M003/M004, `TEAMS/T4/S018/M003`; copies `CM9/T4-M003`, `CM9/T4-M004`; local `cost-map-9/data/s018/T4-M004-vs-S018.diff` | new `repository/restore_prepare_ahead.go` (its partner `restore_range_prefetch.go` is moot now that ranges are windows of one response) | restore **-0.048 s main, -0.078 s large-files** (5/5 each, inside the band); **big-index +0.074 s and +69 MiB** `[5p][old:S018]` (SM9 section 7; CG-O) | CLOSED in the merge for "+69 MiB on big-index" (CG-T). The record also says that tree "lacks the incumbent's index memory work" (CG-O), so the 69 MiB was **never attributed to this mechanism** | Sits in the restore stream path, rewritten by S019 (`stream_pack_coalesced.go`) and the merge (`restore_window_hold.go`). Restore memory is the limit | **0 to +0.2**; re-measure alone on the current tree | unknown for the mechanism alone | M | Medium (extra goroutine per stream; must join before return) |
| A8 | **Direct tree-node decoder** (hand-written node decode in place of `encoding/json`) | S012 T4 M004, `TEAMS/T4/S012/M004`; prototype `p-t4dec` in `CM5` | `data/tree_decode.go` | on S013: bu -0.079 (6/10), ls -0.045 (9/10), rs -0.016 (5/10) on a schedutil lane = about **-0.09 judge s on main** `[old:S013]`; the map calls it "small and unconfirmed" (SM5 section 7; CG-H) | never installed; every team shipped the parent-tree iterator instead (CG-F) | Checked: `data/tree_node_reader.go` in the incumbent still decodes through `json.NewDecoder`. ls now decodes in batches on a helper goroutine (T4 S019) and restore has the node lookahead, so ls and restore gain less than in 2026-10-02 | **0 to +0.3** (main, big-index, remote share the corpus) | about +2 MiB then | M | Medium: tree, node and JSON output must stay byte-identical on every input |
| A9 | **Iterator-local AES reuse, private parent decoding, direct parent cursor** in repeat backups | S012 T3 M003/M004, `TEAMS/T3/S012/M004` | `crypto/opener.go`, `data/parent_decode.go`, `archiver/archiver.go` | M004 row: **bm t+3.6%, bu t+2.7%** (whole row t-0.55 under T3's slower buffers) `[row][old:S011]` (CG-B) | REOPEN-FOR-SPEED, "still unported" in the S016 and S017 directives (item 10/11) | The parent path has been rewritten three times since (S013 parent-tree iterator, S015 unquote skip, S017 parent tree reuse) | **0 to +0.2** | flat | M | Low |
| A10 | **Small-pack second repack worker** in prune | T2 S017 M002-M004, `TEAMS/T2/S017/M002`; local `cost-map-8/data/s017/` | `repository/repack_small.go` | big-index forget-prune **-0.15 s** `[row][old:S016-racefix]`, before the prune rewrite cut that phase from 6.98 to 2.86 s (CG-M) | left out of the S017 hand merge as "not the named idea" and overlapping (M17-R) | Checked: `repack.go` still runs `Connections()-1` = one worker, and there is no `repack_small.go`. It is the bounded form of half of B6 (unrestricted second repack worker: prune -0.130 / -0.140 / -0.224 s) | **0 to +0.3**; re-measure with the local slot | unknown (unrestricted: +7 to +13 MiB) | M | Low to medium |
| A11 | **Repack with a successor read and a retained tail** | T4 S019 M004, `TEAMS/T4/S019/M004`; local `T4-M004-vs-S019.diff` | `repository/repack.go`, new `repack_tail.go` | large-files prune **+3.70% (7/7)** in the team's own ablation; **big-index prune +0.126 s slower** `[row]`; main RSS +2.9% in a probe (CG-R; M19-P row 7) | "REOPEN with a bound" (CG-R), "CLOSED as built" (CG-T): negative on big-index, memory unattributed | Same file as A10 | **+0.08 on large-files, -0.17 on big-index as built**: only with a gate that keeps it off large indexes | unattributed | M | Medium |
| A12 | **Zero-chunk routes in backup**: after one natural all-zero 512 KiB cut, later zero runs are confirmed by a zero-prefix scan (no rolling hash, no SHA, no copy) | T4 S018 M004, `TEAMS/T4/S018/M004`; copy `CM9/T4-M004` | `archiver/ordinary_zero_chunk.go`, `parent_chunk_verify.go`, `repository/chunker_zero_descriptor.go` | large-files bm **-0.020 s (4/5, unresolved)** `[5p][old:S018]` (SM9 section 7; UNP18 T4-5) | never taken up; "relies on zeros always cutting at the minimum size" | The incumbent's S019 exact scanner replaced the code it hooks. It is a partial build of C9 (hole-aware reading) | **0 to +0.05** | flat | M | Medium to high: a second route to chunk boundaries |

### A-3. Memory payers: measured memory gain, zero under the time score, never installed

They matter only if the operator keeps the memory guard (decision 1). All are old and sit in code that has been
rewritten since; port the idea, not the diff. Percentages are of that phase's peak under the old equal-weight score.

| id | idea | source and code | measured | status in the incumbent | pays for |
|---|---|---|---|---|---|
| M1 | Restore file list as sorted slices (with shared directory prefixes) | S009 T3; S011 T3 M001, `TEAMS/T3/S011/M001` | restore memory +4% `[row][old:S009]`; S011's slice was "rejected on probe" (one round) (CG-B) | Checked: `restorer.go` still holds `fileList map[string]bool`. The list is 9.5 + 4.5 MiB in use at main's restore peak, and 1 live MiB = 1.5 MiB of peak (SM10 section 3; SM7 section 6) | B3, B5 (restore on main) |
| M2 | Per-parent restore status vectors | S010 T3 M002-M004, `TEAMS/T3/S010/M004` | restore memory +6.3 / +8.0 / +7.1%, restore time -1.2 / -1.3 / +1.6% `[row x3][old:S009]` (CG-B) | not installed | B3, B5 |
| M3 | Drain `streamPack` buffers after repack | S009 T4; S011 T4 M004; S012 T2 M001 | prune memory +4 to +6%; S012 T2 row t-2.15 `[row][old]` (CG-B) | Checked: no drain in `repack.go` | B6 (prune) |
| M4 | Root-tree JSON cache capped at 1 MiB | S007 T1 | +0.63 old score, memory gain in backup-initial and backup-mutated `[row][old:S006]` (CG-B) | unknown; the tree saver was rewritten in S012 and S017 | B1, B2 |
| M5 | Check header read into the dead ciphertext buffer | T3 S018, `TEAMS/T3/S018/M004`, `repository/checker_header_reuse.go` | not measured; "only the header borrow is missing" (UNP18 T3-F) | not installed | B6 (check) |

## 2. Blocked by a league rule (B): real gains, excluded by the memory guard, a ruling, or scoring

Each row is a yes/no for the operator. "Price" is exact where the record gives it. The guard is 11.3 / 31.4 / 10.8 /
12.2 MiB summed over the 7 phases (main / big-index / large-files / remote), so one phase at +13 MiB alone breaks it
on main. For scale: S019's peaks are 57 to 106 MiB per phase on main (DIR20 section 0), and S018's median
phase peak there was 82 MiB against stock's 143 (REV-S018), so every row below still leaves the release far under
stock's memory. B3 and B5 were over the guard only against S019 at merge time: against `S019-merge` as the
incumbent each fits a fresh guard, although the coordinator's note says to free restore memory first (CG-T).

| id | choice | gain (suite points) | exact price | blocked by | code | evidence | note for a public release |
|---|---|---|---|---|---|---|---|
| B1 | **A third file reader for files up to 256 KiB** (larger files keep two readers) | **+1.0**: bi -0.159 / -0.155 / -0.003 s, bu -0.106 / -0.107 / -0.030, bm -0.039 / -0.023 / -0.013; remote bi -0.144 (9/10), bm -0.065 (9/10) | backup-initial **-0.2 / +0.7 / +7.3 MiB**; backup-unchanged **+13.3 / +9.6 / +11.1 MiB**; backup-mutated +4.9 / -3.8 / -1.1; remote bi +1.9, bm +4.8 | memory guard on main and large-files (backup-unchanged), and an operator ruling: the default of `--read-concurrency` is a user-visible setting outside ruling 4 (DIR20 section 3 item 3, section 6) | `CM10/p-rc3s` (ROUGH; `archiver/file_saver.go` and `archiver.go`, 75-line diff); local `cost-map-10/data/protos/p-rc3s.diff` | `[5p]`, remote `[10p]`: SM10 section 4; CG-S | The backup-unchanged memory is not chunk buffers: a zstd encoder (5.5 MiB) alive at the peak, one more pool buffer, 17 MiB more heap in use with fewer collections. A file-size limit cannot bound it; a 2 MiB limit gives nothing more (CG-S) |
| B2 | **Read concurrency 3 for every file** | **+1.7**: bi -0.192 / -0.150 / -0.034 (3/5), bu -0.100 / -0.105 / **-0.299**, bm -0.068 / -0.081 / +0.001; remote bi -0.082 (9/10) | **+14.3 / +14.3 / +10.2 MiB** on main's three backups; +13.4 / +11.1 / +8.1 on big-index; +6.1 / +12.1 / +2.1 on large-files; remote bi +9.1 | the same two | one constant (checked: `archiver.go` sets `ReadConcurrency = 2`); `CM10/p-rc3` | `[5p][bound]` SM10 section 4 | The existing flag `--read-concurrency 3` gives a user exactly this today. A fourth reader adds nothing on large-files and costs more (SM9 section 4). B1 and B2 exclude each other |
| B3 | **Restore budget 5 -> 6 MiB per configured worker** (`ordinaryRestoreHoldPerWorker`; 10 -> 12 MiB on a local repository) | about **+0.26**: restore +6.1 / +6.5 / +12.8% against the installed +5.0 / +4.6 / +10.4% | **+2 to +4 MiB more on main's restore peak** (10 pairs on main: 10 MiB budget +4.44% at +4.3 MiB, 12 MiB +6.10% at +8.3 MiB) | memory guard on main: the merge sat at -0.74% of the 2% | one constant in `restorer/ordinary_extra_worker.go` (checked: `5 << 20`) | `[10p]` M19-P 04:09Z and 04:14Z; CG-T | The 4 MiB twin (`final-b4`) lost more than half the time for 1 to 2 MiB, and the judge still read it past the guard on main (-2.91% at 12 rounds): peak readings scatter by about 4 MiB (M19-P 05:08Z, 05:33Z) |
| B4 | **Restore budget 16 to 20 MiB** (close to an unrestricted third worker) | about **+0.5 to +0.6** over the installed tree: restore +7.9 / +7.6 / +15.0% at 16 MiB, +7.4 / +8.1 / +15.8% at 20 MiB | restore peak **+16.6 / +18.4 / +4.5 MiB** at 16 MiB and **+18.4 / +23.4 / +7.0** at 20 MiB against S019 (the installed tree: +6.3 / +8.5 / +3.4) | memory guard on main | the same constant | `[5p]`, measured before the restore decoder was cut to two block decoders (M19-P 04:02Z) | Diminishing: nearly all of main's and big-index's gain is there at 12 MiB. Pooled blob-sized buffers (`p-rs3m`: large-files restore -0.568 s for +33.5 MiB) are the same trade and worse (CG-P). B3 and B4 exclude each other |
| B5 | **Restore dispatch look-ahead** (the bounded worker may pass up to 16 plan positions) | about **+0.1**: main restore +1.86% (10/10); big-index +1.05% (7/10, unresolved); large-files -0.47% (2/10, unresolved) | **+5.1 MiB on main's restore peak** | memory guard on main ("where the guard binds") | T4 S019, `TEAMS/T4/S019/M004` `restorer/pack_dispatch.go`; merge step `MERGE19/steps/r3`; local `merge-S019/diffs/T4-S019-vs-S019inc.diff` | `[10p]` M19-P 04:20Z; CG-T "LEFT OUT" | Small. Take it only after M1 or M2 has freed restore memory |
| B6 | **A third check worker and a second repack worker without a bound** on local repositories | **+1.45**: check -0.067 / -0.197 / -0.123 s; prune -0.130 / -0.140 / -0.224 s | check **+6.9 / +9.6 / +7.5 MiB**; prune **+9.3 / +7.1 / +13.0 MiB** | memory guard on main and large-files. Ruling 4 already allows more local workers inside the guard | `CM10/p-conn3` (BOUND = local `Connections` 3); bounded partial builds: A10, A11 | `[5p][bound]` SM10 section 4; CG-S | Inside the guard it needs a byte budget like the restore worker's (C5). For users this is the local connection count: three readers on a repository that may sit on one spinning disk or a network share |
| B7 | **Sparse restore by default** | **+0.9**: large-files restore **-0.456 s** `[old:S018]` (-0.551 s `[old:S016-racefix]`); main -0.026, big-index 0.000 | memory flat. Restored files occupy fewer blocks than stock's (holes in place of written zeros). The oracle passes because the manifest does not see blocks | pending operator ruling (block allocation is product behaviour) | `CM9/p-sparse` (`restore --sparse` forced on); local `cost-map-9/data/protos/p-sparse.diff` | `[5p][bound]` SM9 section 4; SM8 section 6; CG-N, CG-P | `restore --sparse` already exists as a flag. The in-rules form (A4) is small because the time is block allocation, not the writes |
| B8 | **Do not re-read a file whose only change is its ctime** | bound: bu **-0.335 s main, -1.266 s large-files** `[old:S018]`; -0.383 / -0.388 / -1.529 `[old:S016-racefix]`. Not re-measured on S019, whose scanner has since taken 0.33 s of large-files bu. My estimate today: +1.5 to +2.7 | **the oracle FAILS** (the snapshot summary differs). "A ctime change means the content may have changed; restic must read it" (SM8 section 6 notes) | ruling: "restic must read a file whose ctime changed" (CG-N; DIR20 section 5) | `CM9/p-ictime`, `CM8/p-ictime` (`--ignore-ctime` forced on) | `[5p][bound]` SM9 section 4; SM8 headline 4 | **A judge artefact.** The judge builds corpus B as a hardlink farm of A; mutating B changes the ctime of A's copies, so "backup-unchanged" re-reads every file the mutation set touches (SM8 headline 4). A real unchanged backup does not. `--ignore-ctime` exists as a flag. Recommend no |
| B9 | **Give back memory bought under the old equal-weight score**: S012's 2 MiB assembly pool back to `MaxChunkSize`; S008's bounded backup read buffers | pool: bi **-0.19 s** on main (5 reps), about +0.3 on main if it still holds. Read buffers: bu +0.05, bm +0.10 s ("noise level") on S012; bu -2.2%, bm -1.6% at 42 rounds in S008 | pool **+37 MiB**; read buffers +20 to +26% of each backup peak | memory guard (CG-E: "CLOSED as a revert") | prototype `bigpool` in `CM4`; `INC/S011` holds the code before the pool | `[5p][old:S012]`, main only: SM4 sections 5 and 7; CG-C | `file_saver.go` has been rewritten in S018 and S019 since. Poor value per MiB. Recommend no unless re-measured |
| B10 | Pooled `EncodeAll` destination, save scratch leases, concurrent packer `Add` | time +2.36% in the one row that died on the cap; the family ranged -1.99 to +2.88 (old score) | memory -6.33% in that row | the 5% axis cap of S001-S012 scoring; "time never isolated" | S004-S006 T4, `TEAMS/T4/S004/M001` | `[row][old:S001]`, main only: CG-B; REV-S004 | Ancient and unisolated. Listed so that it is not dropped silently |

**The 1% per-workload gate.** No isolated mechanism is known to have been excluded only by this gate. Rows past it:
T3 S019 M001 (large-files -1.31% beside big-index +1.94%, of it backup-initial -0.352 s) and T3 S019 M002 (big-index
-1.18%). T3's M001 tree changed restore files only (`cost-map-10/data/s019-rows.txt`), so its backup-initial reading
cannot come from its code, and T3's later matches did not repeat it (big-index backup-initial +0.034, -0.018,
-0.029 s). Both rows read as single-screen noise `[row]` (CG-R).

**Scoring.** Under the time-only score since S013, memory-only work scores zero (table A-3). Under the equal-weight
score before it, S012 T4 M004 (time +2.93%, the only clear time gain of that season) was not nominated (REV-S012); its
parts were harvested later or are row A8.

## 3. Never built, with measured bounds (C), ranked by bound

A bound is what a forbidden or unrestricted edit measured; a ceiling is a measured stage time a perfect version
would remove. Neither is a forecast. "Built before" says what exists. Points use the scale of section 0.

| rank | lever | bound or ceiling (suite points) | where | built before? | what building it takes | source |
|---|---|---|---|---|---|---|
| C1 | **Use the lock wait, per command.** The 200 ms wait cut to nothing reads -0.17 to -0.21 s in every one of the 14 cells of main and large-files (-1.28 s and -1.41 s per pass); null on big-index (its index load fills the wait); -0.07 to -0.10 s in four remote phases. Five separate jobs: **backup +2.2** (three phases), **restore +0.7**, **check +0.7**, **forget-prune +0.65**, **ls +0.7** | **+4.9 bound** (+0.3 more on remote) `[5p][bound]` | main, large-files, remote | Only check, roughly (A1: 0.12 of the 0.19 s in its cell). The other four have no rough version. The index load, snapshot list and check pack sizes already run inside the wait | Each command's flow after the lock must start before it: backup = source scan, reads, chunking, hashing, compression into packs whose uploads wait; restore = snapshot, trees, plan, first pack reads into a bounded buffer; prune = the plan; ls = tree loads and decoded nodes into a bounded buffer. Read-only work only; every write and output line waits for the confirmed check; discard on failure; safe beside a concurrent writer (rulings 1 and 7). Hooks: `repository.LockRepoWithWaitWork`, `cmd/restic/wait_index_load.go`, `backup_index_load.go`. Effort L per command; backup is the largest and the riskiest | SM10 sections 4 and 5; CG-S; DIR20 item 1 |
| C2 | **The extra verify on save with less CPU** (decrypt, decompress, hash, compare). Verify off reads backup-initial -0.600 / -0.561 / -0.454 s, large-files backup-mutated -0.169 s | **+3.0 bound** `[5p][bound]`; unchanged from S018 (-0.53 / -0.51 / -0.52) | main, big-index, large-files | Four variants in S019 (reader, reuse, overlap, hash writer: T1, T2, T3, T4). **The bound did not move** | Overlap gives little: backup-initial already runs at 3.5 of 4 cores. Only less CPU for the same check counts (`verifyCiphertext` is 2.36 of main's 16.6 CPU-s). No design on record. Removing or weakening it is never allowed | SM10 sections 3 and 4; CG-S |
| C3 | **A third file reader inside a memory bound** | **+1.7 bound; +1.0 measured rough** | all four | B1 (small files only) and B2 (unrestricted) | Bound what is alive at the backup-unchanged peak (the zstd encoder, a pool buffer, collection pacing), not the file size. Then it is inside the guard and only the ruling on the default remains | SM10 section 4; CG-S |
| C4 | **Restore's metadata pass during the files stage.** The pass (owner, mode, times) takes 0.46 s on main and big-index and 0.47 s on remote, 1.4 CPU-s, and starts when the last file is written; the files stage before it leaves 3.0 / 3.3 core-s idle and uses 0.95 of 4 cores on remote | **+1.7 ceiling** (no prototype) | main, big-index, remote | Never. Related and unmeasured: more metadata workers, or one task per directory (S017 directive item 3) | Set a file's metadata when its last blob is written; directories and hardlinks stay in the pass. The cost is what is kept per file until then, on the phase where the guard binds: measure the restore peak on all four workloads. Files `restorer/restorer.go`, `filerestorer.go`. S009's ordered error settlement must hold. Effort M to L | SM10 sections 2 and 5; DIR20 item 2 |
| C5 | **A third check worker and a second repack worker for large packs, inside a byte budget** | **+1.45 bound** (check +0.61, prune +0.84) | main, big-index, large-files | For small and mixed packs only (installed: S016 check slot, S018 `checker_capacity.go`). Partial repack builds: A10, A11. Unrestricted: B6 | A shared byte budget on which only the additional worker waits, as the merge built for restore (`restorer/restore_hold_budget.go`, `repository/restore_window_hold.go`). A per-pack size gate is a cliff and one budget for all workers is slower (both closed for restore, CG-T). Effort M | SM10 section 4; CG-S, CG-T |
| C6 | **backup-initial on remote**: the snapshot stage takes 7.09 s at 2.2 cores and 4.0 of 5 connections; the same bytes fill five connections in 5.67 s | **+1.1 ceiling** (unprototyped) | remote | Never. Readers are not the limit: three read -0.08 s, eight -0.25 s at +65 MiB | Likely, not measured: the link is full while large files are read and idle while small files are (the corpus is read in directory order: 6 large files, 161 medium, then 90,000 small). First a `phase_timeline` of pack uploads against file sizes. Effort unknown | SM10 section 6; CG-S |
| C7 | **Re-reading ctime-changed files faster** (the in-rules part of B8) | bound +3.3 on the S018 scale; S018's readers took about a quarter, S019's scanner took large-files bu -0.33, bm -0.26, bi -0.24 s. **What is left is not measured on S019** | large-files mostly | Three parent-guided readers in S018 (T1's installed), the exact scanner and overlapped hashing in S019 (installed) | Re-measure the bound on the current tree first (`p-ictime` against `S019-merge`). The remaining idea on record: hash at the parent's chunk boundaries on all cores with no chunker at all | SM9 sections 4 and 5; SM8 section 7 lever 2; S018 directive item 3 |
| C8 | **big-index**: the index load runs 0.06 to 0.08 s past the wait in every phase; prune plan 1.07 s at 1.7 cores and execute 1.53 s at 1.6 (2.5 and 3.7 idle core-s); check is 0.065 s behind S018's | **+0.6 for the tail**; prune unmeasured; check +0.09 | big-index | It moved least in S019 (+0.89). A5 may hold the check part | Cheaper decode of the canonical index JSON, sizing the tables once; prune's index walks (`Values`, `blobIndex`, `firstIndex`, `PlanPrune`) are single loops. A shorter load also opens C1 on big-index | SM10 sections 2, 3 and 5; DIR20 item 7 |
| C9 | **Hole-aware reading in backup** (`SEEK_DATA`, the known zero chunk in place of reading holes): 45% of large-files' bytes are holes, 1.3 GiB per backup, still read and chunked | the map's estimate **+0.4 to +1.3** on today's scale (it wrote +0.5 to +1.5 on the three-workload scale). **Unmeasured**; the S019 scanner has since cut the chunker share | large-files, three backups | Never. A12 is a partial build. Checked: no `SEEK_DATA` in the incumbent | Chunk boundaries and blob IDs must equal stock's on every input, including a hole that ends inside a chunk. Linux-specific; other platforms stay on the stock path. Effort M, risk high | SM8 section 5 and section 7 lever 12; CG-N |
| C10 | **forget-prune on remote**: execute takes 2.33 s for work that fills five connections in 1.76 s (94 requests, 38 of them deletes, 272 MiB, 3.6 connections busy) | **+0.45 ceiling** (unprototyped) | remote | Never. T3 S019's larger repack requests read inside the noise | Overlap deletes with uploads and reads; `phase_timeline` first | SM10 section 6; REM section 6.3 point 5 |
| C11 | **What every process sends before work on remote**: 0.62 s, 12 requests; about 0.56 s is the least in the required order | **+0.3** | remote, all 7 phases | Four builds in S019; the incumbent's is equal or ahead | Still one after another: `GET locks/` then `POST locks`; the two `GET index`; `GET snapshots` after the lock. On repeat backups the tree pack, the index and the snapshot are uploaded in turn (0.1 s per phase, unmeasured; the index must stay before the snapshot). Ruling 7 binds | SM10 section 6; SM9 section 6; CG-Q |
| C12 | **One traversal instead of two in backup** (the progress scanner walks and stats the whole source beside the archiver: 0.34 CPU-s, 91k extra `newfstatat`) | bound **-0.085 s on main** over the three backups (bi -0.053, bu -0.014, bm -0.018; 8/8 each) `[old:S015][bound]` = about +0.14 on main; perhaps +0.3 with big-index and remote, which share the corpus | main, big-index, remote | An unjudged review copy: S016 T2 seat B15, `TEAMS/T2/S016/M001/candidate-review-B15`; local `cost-map-7/data/s016/T2-M001-review-B15.diff`. Checked: `--no-scan` is still off by default | Count in the archiver. Progress and `--json` output must not change, and the scanner's totals feed both: that is the hard part. Effort M | SM7 sections 5 and 7; CG-L; S017 directive item 8 |
| C13 | **Less work per unchanged entry in repeat backups** (what another backup tool does: unchanged backup 0.52 s against 1.51 s, mutated 1.11 against 2.01 s on S015) | "about 1.9 s per round on main" seen elsewhere `[old:S015]`. Most of it is not available: no lock wait there, coarser chunks, dropped metadata (that tool failed restore correctness 10/10), and the re-reads are the ctime artefact (B8) | all | S017 parent tree reuse and compare, four teams: -0.01 to -0.04 s per repeat backup on main (installed, small) | Measure first how much of the 1.3 s snapshot stage is the second stat, `llistxattr`, parent tree decode and re-encoding tree blobs that already exist. Reusing a parent node's metadata without reading it again is a product change (owner first) | BTB verdict; S017 directive item 13; CG-M |
| C14 | **backup-initial CPU, small items**: two `lstat` per file (183k), 155k `getrandom` (one nonce per blob), futex hand-offs, the pack hash and bufio copy (1.3 CPU-s), handing over the local temp pack instead of copying | ceilings -0.1 to -0.3 s, "a measured share times a plausible reduction", not bounds | main, big-index | S016 installed a direct path stat (one allocation less, not one stat less). Checked: `crypto.NewRandomNonce` still calls `rand.Read` on every call | Each is a separate small job; bring a `go_bench` first. backup-initial is CPU-bound at 3.5 of 4 cores, so removed CPU counts | SM7 section 8 levers 9 and 10; SM6 section 8; CG-D |
| C15 | **Restore per-file syscalls**: skip the owner change where the new file's owner already matches; one open per file | ceiling 0 to -0.2 s (estimate) | main, big-index | Never. No `fallocate` for small files: null, closed. `llistxattr` skip: null, closed | The manifest (modes, owners, times, xattrs) must equal stock's. Checked: `fs.lchown` is still unconditional | SM6 section 8 lever 12; S016 directive item 12 |
| C16 | **A fixed order for check's packs on remote** without listing every pack first. Largest first in a fixed order reads 6.66-6.68 s in 12 of 12 runs against 6.68-7.02 | **+0.1 to +0.2**, shared with A3 | remote | `p-cksort` (TEST): closed as built, +0.18 / +0.85 / +0.14 s locally and +130 MiB on big-index | Order without holding every blob list. A3 removes the same coin more cheaply; build this only if A3 does not | SM10 section 4; CG-S |
| C17 | **Index representation** (upstream issue 1988), lazy index load | not measured | big-index | Never | A structural change of `repository/index/`; out of reach for a hand merge | CG-D; REV-S006 |

## 4. Closed (D): one line each, with the reason

Source for every line: `closed-ground.md` (section letter in brackets) unless another file is named.

**D1. Closed by a ruling or a rule, whatever the score.**
- The lock wait removed or shortened: ruling 1. Its bound is C1. [A, S]
- `GOGC` / `GOMEMLIMIT` either way: `GOGC=off` was slower in 6 of 7 phases. `--no-cache`. [A, E]
- KDF changes: the parameters belong to the repository; parallel scrypt is installed; lower-memory variants cost time. [A]
- The extra verify on save removed or weakened: integrity. Its bound is C2. Hash-on-write removed: -3.2 (S007). [A, S]
- Workload or fixture detection; changed output bytes, order or exit codes; chunk boundaries or IDs unlike stock's. [A]
- Busy-waits or dummy goroutines to hold CPU clocks: ruling 3. (DIR20 section 6)
- More than 5 REST connections: the judge's server admits 5, and the default is a user-visible setting. [Q]

**D2. Installed in `S019-merge` (do not rebuild).** S004: parallel scrypt, batched stdout, raw zstd lock frame, GC
before index load, `streamPack` part cap, checker `NextHash`, `ensureDir` cache. S005: exact-size chunk copy,
known-blob admission, raw storage of incompressible blobs. S006: range pool, hash-on-write. S008: bounded backup read
buffers, async repack saves, checker stream reader. S009-S011: parallel second restore pass, streamed index encode,
verify and save, verify-open skip under a fresh directory. S012: assembly pools, node serializer, chunker reset
snapshot. S013: index load inside the wait (backup), opens without the poller, parent-tree iterator. S014: decoded
tree prefetch for pass 2. S015: index load inside the wait for every command. S016: bounded third check worker,
restore window 64, node lookahead, direct path stat; the race fix. S017 and its merge: small-blob restore worker,
check pack sizes and early structure pass, prune planning by position, index rewrite without reload, direct index
decode and adopt, parent tree reuse, tree carry. S018: large chunks hashed off the reader, parent-cut readers, check
on large and mixed packs, canonical streaming index decode, save verify without a second buffer. S019 and its merge:
one request per pack span read in windows, exact chunk scanner, overlapped hashing, key listing beside the config
check, third ordinary restore worker in a byte budget, ls decode batches, one-blob lookup skip. [B to T]

**D3. Restore.**
- Second-pass window above 64: flat (-0.305 / -0.305 / -0.313 s at 64 / 256 / 1024). [L]
- No `fallocate` for small files: null on S014. `llistxattr` skip: the second pass absorbs it. [H, J]
- Raw tree payload prefetch: the decode is the cost, not the read. Replay of pass 1, 4 MiB replay cache: reversed, no increment. [G, B]
- Decode each tree once (tree carry): installed in the S017 merge, measures null. (M17-R)
- A second metadata pool: duplicate of the installed one. Seven `ensureDir` variants: none repeatable. [B]
- Third worker bounds that failed: a per-pack blob-size gate (a cliff: nothing at 2 MiB, over the guard at 3 MiB); one budget on which every worker waits (11 / 14 / 22% slower); dropping window buffers above 2 MiB (no memory gain, slower); zstd low-memory decoder (no help); smaller stream ranges (a blob is never split). [T, L]
- Decode output carried across windows or pack streams (T2 S018, T3 S018 and S019): the peak rises 2 to 8 MiB, T3's by 15.6 / 20.1 / 7.8. [T]
- Pooled blob-sized buffers (`p-rs3m`): +26 to +34 MiB. Stock's 32 MiB ranges (`p-range32`): +133 MiB and 1.1 s less gain than windows. [P, Q]
- An additional worker on remote: +0.3% for 5 to 8 MiB. T1 S019's compressed-pack route: subsumed by the third worker. [T]
- T3 S017's small-restore loader and T4 S017's loader with a download budget: same code as the installed worker; large-files restore +0.08 s. (M17-R; [M])
- T4 S018's range prefetch: moot since ranges became windows of one response (its partner is row A7). [O]

**D4. Check.**
- Buffered task channel: nothing (the producer wait goes with a worker, not a buffer). A worker without a backend slot: never runs. [I, J]
- Per-worker blob-buffer reuse: null on both axes. Decoded-blob scratch reuse, four variants: time-negative. [B]
- Hash pipeline (T2 S018), blob-hash ring and pack hash ahead (T4 S018), T3's bounded worker: 0.10 to 0.21 s slower than the incumbent's check on large-files. [O, P]
- Packs listed first and handed out largest first (`p-cksort`): +0.18 / +0.85 / +0.14 s locally, +130 MiB. Small packs held back (`p-cklast`): still two values. [S]
- `HasPackBlob`, pack-size cache (before S004): no gain. [B]

**D5. Backup.**
- Save verify by reader, reuse, overlap or hash writer (four S019 builds): the bound did not move. [S]
- Chunk scanners other than the incumbent's (T1 S019's whole-chunk scanner, T4 S018's receipt chunker), a second hash slot (T4 S019): the incumbent's backups are ahead. [R, T]
- T2 S018's two-tile boundary scan and T3 S018's cold-parent reader: -0.12 to -0.15 s on large-files bu against S018, then overlapped by S019's exact scanner (-0.33 s there). [O, T]
- Minimum-window reader: judged nil. In-flight known-blob check (four builds): null at 42 rounds. Inline known-blob completion (five builds): noise. [O, B]
- Parent-blob cache: backups slower. Parent snapshot lookup inside the wait: milliseconds. [B, G]
- Zero-copy leases, `AllBlobsPresent` caches, scanner skip, direct `MarshalJSON` (before S004): inverted. [B]
- Smaller initial file buffers, bounded ownership spans, chunk-output caps, packer entropy: time-negative memory trades. [B]
- Third reader with a 2 MiB limit: nothing over 256 KiB, more memory. A file-size limit as the only bound: backup-unchanged +10 to +13 MiB. Four or eight readers: no more than three on large-files; on remote eight read -0.25 s for +65 MiB. [S, P]
- Three connections for backups: null. A third pack uploader: nothing. [J]

**D6. Prune and forget.**
- Used size from header arithmetic (three builds): null. `SaveIndex` retry robustness: noise. [B]
- Index loader and saver caps: memory only, then overtaken by the S017 merge's rewrite. [B, N]
- Index save verified by a stream hash (`idx1enc`): null. [E]
- T3 S019's repack through larger requests: remote prune -0.172 s beside the incumbent's own -0.212: inside the noise. T4's successor repack as built: negative on big-index (row A11). [R, T]

**D7. ls.**
- Pre-batched output tagging: gain too small. Pull-iterator walk: null. Tree read-ahead: null. [B, G]
- The output path: the FIFO-to-file gap is the CPU clock, not the code. [E]
- Three connections for ls: null. Skipping index rewrite notes (T2 and T4 S018): judged nil. [L, O]

**D8. Index.**
- Streaming `DecodeIndex` for the whole load (S009): time-negative; later forms are installed. [B]
- Pack group without the stored pack ID: null. Slab, bitset, `ListPacks` batching (before S004): +1.09 at best. [B]
- T2 S017's index segments and bounded tree loads, T3 S017's decode reserve, T2 S018's hex decode without validation: equal to or behind S018's installed decoder. (M17-R; UNP18)

**D9. Lock wait and start-up.**
- Lock-cleanup error path: no gain. [B]
- Index preparation inside the wait, three S012 builds (T2, T3, T4): overtaken by the installed index load inside the wait. [B, F, I]
- Start-up requests prepared or deferred beyond the incumbent's (four S019 builds, and `p-ctl`): equal or behind; 0.06 s per process is left. [R, T]
- Pack opens in the local backend without the poller: a few hundred opens, nothing. [F]

**D10. Workers and connections.** Connections 4: nothing over 3. Unrestricted third workers: over the guard (B4, B6). [B, P, S]

**D11. Remote.** Fewer requests than stock's: finished (439 per pass against 438). More file readers: C6. [S]

**D12. Memory-only and null.** Clearing consumed prefetched nodes (S015 T2 M002): time null, memory reading unclear in the record. [I]

**D13. S012 T4 M004's other parts.** Index plaintext prepared inside the wait and the canonical index decoder:
overtaken by installed forms. The committed-hit chunk cache (`archiver/file_chunk_cache.go`): never isolated, then
buried under five seasons of `file_saver.go` rewrites. [B, E]

**D14. Gaps in the local record** (nothing here is known to be a lost gain; the merge agent can check on the box).
- **S016 T2, T3 and T4 M003/M004** (+3.13 to +4.04 on main against S015, 6/6; T4 "led the table on phase cells
  (backups, ls, forget-prune)", REV-S016). Local diffs stop at M002 (`cost-map-7/data/s016/`). The S017 automatic
  merge offered the three M004 trees and found that "their changes sit in code the incumbent has since rewritten"
  (REV-S017). Trees: `TEAMS/{T2,T3,T4}/S016/M004`.
- **S019 M001-M003 trees**: I read the changed-file list of all sixteen rows (`cost-map-10/data/s019-rows.txt`).
  Every mechanism file of M001-M003 is still in its team's M004 tree, so section R's M004 table covers them.
- **The S017 merge's open lead**: main's non-prune phases read -0.2 to -0.4% each at 42 rounds (3 to 13 ms per
  process), never attributed (M17-R). `S017-merge` is on the fast binary layout (M19-P 04:51Z), so layout is not the cause.

## 5. In the incumbent, to re-examine before a public release (E)

Only what the records show, plus presence checks in the local tree where marked "checked". A code review runs
separately. Context first: the gain is not fitted to the visible workloads alone. On the operator's held-out workload
`incumbents/S019` reads **+30.76% time against stock (12/12), memory +39.3%** (S018 +29.91%, S017 +28.75%;
`runtime/S020-boundary-PROGRESS.md` 04:00Z). Every number in the league is linux/amd64, go1.27.0, `GOMAXPROCS=4`,
the `performance` CPU governor, corpus, repository and restore target on tmpfs, one restic process at a time (INT).

| id | what is in the incumbent | why it is tuned to the judge, or a risk for users | evidence | to do |
|---|---|---|---|---|
| E1 | **The key derivation's speed depends on where the linker puts `scrypt.salsaXOR`.** It must start at 32 mod 64 (0x81f160 in S018, S019 and the merged tree); code added to seven early packages shifts it in 32-byte steps and an odd number of steps costs 3% of the derivation: 6 ms per process, about 0.9 suite points | Pure binary layout of one build: the judge's toolchain, flags and platform. A release binary for another OS, architecture or Go version lands wherever it lands. The league's cure (no function bodies in those packages) only holds the judge's build still | M19-R "Finding"; M19-P 03:50Z, 04:22Z, 04:51Z; CG-T | Check the symbol address in every release binary (`tools/nm.sh`, `nmdiff.py` in `merge-S019/tools`). Do not count the 0.9 points as a property of the code |
| E2 | **Parallel scrypt**: the derivation runs 0.20 s at 4 cores where stock takes 0.48 s at 1 core | The 0.28 s in every process (7 per pass) assumes four free cores. Checked: `crypto/parallel_scrypt.go` uses `min(4, p, GOMAXPROCS)` workers | SM10 section 1; CG-B (S004 T1) | State the core-count dependence; measure on 1 and 2 cores |
| E3 | **Work inside the 200 ms lock wait**: index load, snapshot listing, check pack sizes, the ls snapshot fetch | It exists only because of the wait: with no wait nothing is started (RF fix part 2), and debug mode keeps the synchronous path (SM7 section 5; checked: `cmd/restic/lock.go` drops the wait work when `debug.IsEnabled()`). It carried a **real concurrency bug for four seasons** (index listed before snapshots: `restore latest` and `ls latest` failed about one run in three beside a running backup) that the judge never saw because it runs one command at a time. Fixed in `S016-racefix`; ruling 7 | RF; REV-S016; INT 2026-10-03 | Every later addition to the wait (S017 `checker_pack_sizes.go`; S019 `snapshot_metadata_wait.go`, `prepared_snapshot.go`, `retry/prepared_load.go`) and the start-up overlap before the lock (S019 `global/open_key_listing.go`) was race-checked only with `TestRestore`, `restore latest` and `ls latest` beside concurrent backups (M19-R). Check, prune and backup beside concurrent writers were not exercised |
| E4 | **Own tests of the wait work that fail or do not compile** | `TestRestoreFailedLockConfirmationDrainsPreparedIndexBeforeUnlock` panics with a nil pointer and `TestRestoreSnapshotLookupErrorDrainsPreparedIndexBeforeUnlock` fails on S015; on S017 the first still fails and `internal/repository/lock_file_wait_work_test.go` does not compile against the stock helpers. They cover the path a user hits when the lock check fails. Their state on `S019-merge` is not recorded (its report says "7 known sandbox failures of stock ignored by the tool") | RF "Verification"; M17-R "Tests"; M19-R | Run the tree's own tests as one suite and settle each failure. No `-race` run was ever made (the test backend is not thread-safe, RF) |
| E5 | **The extra check worker has a slot of its own only on the local backend** | On every other backend it joins the ordinary semaphore, so six workers share five REST connections; this is the cause of the two-valued remote check | SM10 section 4 notes; CG-S | Port A3 |
| E6 | **The third ordinary restore worker and its 5 MiB budget** | Runs only on the local backend at its default connection count and only "when a core is free"; the constant was set by the judge's memory guard on main, not by any user need (4 MiB and 5 MiB twins were both sent to the judge). The restore decoder holds as many zstd block decoders as configured workers | M19-R "How idea 1 was finished"; M19-P 04:17Z, 04:43Z, 05:33Z | Decide B3/B4. Confirm the behaviour with `-o local.connections=N` and on 1- and 2-core machines |
| E7 | **Local repositories are read with three workers in check and restore unless `-o local.connections` is given** | Ruling 4 allowed it in the league "without a storage-media argument". A user's local repository may be a single spinning disk or a network share | INT 2026-10-02; DIR20 section 6 ruling 4; checked: `local/config.go` `AllowAdditionalLoadSlot` | Operator decision 3 |
| E8 | **One backend request per pack span, read as 2 MiB windows from the open response** (remote restore 10.52 -> 7.51 s) | The prototype left open: a retried or failed request, a handler error mid-body, and "the connection is held while blobs are written to disk". The installed form is T2's; the records do not say how it settled these. The judge's link has no TLS, loss, jitter or server time-outs | SM9 section 6 notes; REM section 2 ("Not simulated"); CG-Q, CG-R | Test the retry and mid-body error paths; think about a slow restore target holding a response open on a real backend |
| E9 | **Thresholds fitted to the judge's corpus**: restore window 64; bounded restore worker for uncompressed blobs up to 3 MiB; 2 MiB windows; check's extra worker for packs of blobs up to 1 MiB and mixed packs (2 MiB ciphertext, 256 KiB header); chunks of 256 KiB to 1 MiB hashed off the reader; tree prefetch 256 KiB per tree | Each was swept on one corpus (91k small files plus about 8 MiB blobs in 16 MiB packs). The record shows how sharp they can be: a 3 MiB gate was a cliff "because a 16 MiB pack holds about 10 chunks; 41% of packs have none above 3 MiB" | M19-P 03:43Z; CG-K, CG-L, CG-O, CG-R; UNP18 | Not wrong, only unproven elsewhere. Say so in the release notes, or test on a second corpus |
| E10 | **"backup-unchanged" is not an unchanged backup on the judge** | The mutation set changes the ctime of the parent corpus's copies, so the phase re-reads every mutated file. The parent-guided readers, the exact scanner's two segments and the overlapped hashing (large-files backups -0.16 / -0.33 / -0.25 s in S019) work on that case. A user's unchanged backup skips those files and gains nothing from them | SM8 headline 4; CG-N; DIR20 section 0 | Describe the gain as "re-reading changed or ctime-touched large files", not "unchanged backups" |
| E11 | **A second chunk-boundary implementation** (`repository/chunker_exact.go`, `chunker_segments.go`, table-driven, two independent segments) and parent-predicted cuts (`archiver/parent_cut_reader.go`, `parent_cut_long.go`, `parent_pair_hash.go`) | "Boundaries must equal stock's on every input." The oracle compares tree IDs on the judge's four corpora only. The records warn that the S018 scanners of other teams hard-coded the chunker's constants | UNP18 T2-A, T3-A; S019 directive item 5; CG-R | Differential test against the stock chunker on random and adversarial input and on more than one polynomial |
| E12 | **Prune: planning by position and index files kept unread in the rewrite** | The destructive command. The rewrite skips a file only under four conditions and falls back to the stock loop for a duplicate pack ID (old repositories, an interrupted prune, `repair index` leftovers); the two prototype gaps were closed by hand with two own tests | M17-R "How the two prototype gaps were closed" | Review once more with a repository that has duplicates |
| E13 | **Restore verify-open skip under a directory this process just created** (restore +6.4% at 42 rounds in S011) | A check removed on the strength of "this process made the parent" | CG-B (S011 T1 M001) | Review for a target that another process can write to during the restore |
| E14 | **Memory bought with time under the old score** (S008-S012): bounded backup read buffers, the 2 MiB assembly pool with copy on growth (bi +0.14 s against S011), checker reader of 1 MiB, the forced `runtime.GC()` around the index load | Fitted to the judge's metric (peak RSS per process, `ru_maxrss`), under a score that weighed memory as much as time | CG-C; SM4 section 5; RF ("S015's `LoadIndex` starts with `runtime.GC()`") | B9. Checked: `runtime.GC()` at `repository.go:1096` and `:1104` where stock has one |
| E15 | **Incompressible blobs stored uncompressed under automatic compression** (ruling 2) | Product behaviour: the bytes in the repository differ from stock's for such data; stock reads them | DIR20 section 6 ruling 2; CG-B (S004-S005 T4) | Release note |
| E16 | **Opens without the Go poller and a direct path stat on Linux** (`fs/file_open_linux.go`, `path_stat_linux.go`, with `_other.go` fallbacks) | "A real version must keep Windows and other platforms on the existing path, keep `O_CREATE` permission semantics." Only Linux was ever judged | SM4 section 7 notes; CG-F | Build and test the other platforms (`final-candidate/build-matrix.log` shows a matrix was started) |
| E17 | **Batched stdout and terminal status changes** (`ui/termstatus/status.go`, `stdio_wrapper.go`); ls output batching | Right for a regular file, which is what the judge writes to. Interactive terminals and pipes were measured once (FIFO, `/dev/null`) and never judged | SM4 section 6; CG-B, CG-E | Try progress output on a terminal and through a pipe |
| E18 | **Stray and league-only files** | Checked in the local tree: `internal/repository/repository.go.orig` (came along in S018, REV-S018); 150 own `_test.go` files, some named after builder seats (`b06_peer_contract_test.go`), one reading the environment variable `RESTIC_TEST_STOCK_BINARY` (`cmd/restic/save_verify_interruption_test.go`). The box tree also carries S019's evidence folders (M19-R). No new literal `os.Getenv` name appears in non-test code (checked) | REV-S018; M19-R "Before installing" | Delete the `.orig` file; decide which own tests ship |
| E19 | **`debug.IsEnabled` and the synchronous path in debug mode** | A debug build takes a different lock path from a release build, so a debug log does not show the code users run | SM7 section 5; checked: `internal/debug/debug.go` adds `IsEnabled` | Keep, but know it when reading a user's debug log |
| E20 | **Two unattributed readings on the merged tree** | Remote reads -0.25% (3/11) with only ls changed (+3.10%, 11/11); big-index check peak +12.7 MiB with unchanged check code (the 12-round job read the opposite sign). Research-lane repeats read null | M19-R "Before installing"; M19-P open points | Watch both in the final judging |
| E21 | **The remote workload is one simulated profile** (30 ms, 48 MiB/s per connection, 5 connections; no TLS, HTTP/2, loss or jitter) and `restore`'s extra worker is off there | The +18.68% on remote is that profile's number. At the first profile (12 MiB/s) the same trees kept "an eighth of the percentage" | REM sections 2 and 6.2 | Quote remote gains with the profile |

## 6. Suggested merge order

One wave = one bench. After every wave: `oracle_check` on the phases touched, all four workloads with the 7 peaks,
`race_check` for anything inside the lock wait, and the 11-second layout probe (ls-json on large-files, 10 reps;
-1.5% means the slow layout, E1). Gains are in suite points against `S019-merge`; the ranges are my estimates from
section 1 to 3, not measurements. Within each track the largest expected gain comes first.

**Files that more than one row touches** (do these rows in the order given, in one wave each):
`repository/checker.go`: A1, A3, C5. `restorer/filerestorer.go` and the restore stream path: A4, A6, A7, B3 to B5,
M1, M2. `repository/repack.go`: A10, A11, C5. `archiver/file_saver.go`: B1/B2, A12, C9. `archiver/archiver.go`,
`cmd/restic/cmd_backup.go`, `data/tree.go`: A8, A9, C12.

### Track 1: ports inside the league's rules (no decision needed)

| wave | rows | bench | expected | memory |
|---|---|---|---|---|
| 0 | hygiene: delete `repository.go.orig`, settle the failing own tests (E4, E18) | tests only | 0 | 0 |
| 1 | **A1 check inside the lock wait** (finished: stop at the confirmed check or share one pack listing), then **A3** | check on four workloads; remote with 12 reps | **+0.5 to +0.75** (+0.40 if big-index stays as built) | about -2 main, +1.5 large-files, -8 remote; big-index 0 once fixed (+11.8 as built) |
| 2 | **A2 tree pack in parallel ranges**, without the size request | remote, six phases, 10 reps | **+0.24 to +0.35** | flat |
| 3 | restore, one at a time: **A6** first-pass prefetch, **A7** prepare-ahead, **A4** zero-write skip | restore on four workloads with peaks | +0.1 to +0.5 (all three unconfirmed on this tree) | A6 flat as built; A7 unknown: restore is where the guard binds |
| 4 | backup and tree decode: **C12** one traversal (from the B15 review copy), **A8**, **A9** | three backups and ls on four workloads | 0 to +0.5 | flat to +2 MiB |
| 5 | prune: **A10** small-pack repack worker; **A11** only with a gate | forget-prune on four workloads | 0 to +0.3 | unknown; must stay bounded |
| 6 | big-index check: **A5** (ablate the cipher preparation and the recycler apart) | check on big-index and main; every phase once for layout | 0 to +0.15 | unknown |

**Expected total of track 1: +0.7 to +1.1 from the three solid rows (A1 to A3). With the weak rows my expectation
is +1.2 to +1.8; if every weak row held its upper value it would be about +3.** Against stock that is roughly
+29.5% to +30.3% (one point against the incumbent is about 0.71 against stock).

### Track 2: rows that need an operator decision (section 7)

| wave | rows | expected | memory, summed over 7 phases | fits the league guard (11.3 MiB on main, 10.8 on large-files)? |
|---|---|---|---|---|
| 7 | **B6 third check and second repack worker** | +1.45 as an unrestricted bound | +16.2 MiB main, +16.7 big-index, +20.5 large-files | no as built. Inside the guard only as C5 (a byte budget, to be built) |
| 8 | **B1 third reader for small files** | +1.0 | +18.0 MiB main, +6.5 big-index, +17.3 large-files, +6.7 remote | no: backup-unchanged alone is +13.3 on main |
| 9 | **B7 sparse restore by default** | +0.9 | flat | yes: it needs the ruling only |
| 10 | **B3 restore budget 6 MiB per worker**, or **B4 16 MiB** in its place | +0.26, or +0.5 to +0.6 | B3: +2 to +4 MiB on main. B4: about +10 MiB more than installed on main and big-index restore | B3 yes. B4 no (10 to 12 of main's 11.3 by itself) |
| 11 | **B5 dispatch look-ahead** | +0.1 | +5.1 MiB on main | alone yes; beside B3 it is 7 to 9 of main's 11.3 MiB, more than the half the directive advises, so M1 or M2 should pay first |

**Where the memory headroom runs out.** Under the league's guard it runs out on main and large-files as soon as
track 2 starts: track 1 is memory-neutral, B7 costs nothing, B3 uses 2 to 4 of main's 11.3 MiB and B5 another 5.1,
and each of B6, B1 and B4 alone is at or over the guard on main (B6 and B1 on large-files too). Big-index (31.4 MiB)
and remote (12.2 MiB) are not the limit. The guard resets at every promotion, so memory can also creep 2% at a time:
that is part of decision 1. If the operator sets the guard aside, B6, B1, B7, B4 and B5 together are about **+4.0
points for about +49 MiB summed on main** (+9% of main's roughly 565 MiB of summed peaks), about +39 MiB on
large-files and about +33 MiB on big-index.

**Expected total of tracks 1 and 2 with every decision "yes": about +5 to +6 points, roughly +32.5% to +33% against
stock.** B7 and B3 alone ("yes" to sparse, guard kept) add about +1.2 to track 1.

### Track 3: new construction (only if there is time; nothing to port)

C1 for ls (+0.7 bound), forget-prune (+0.65), restore (+0.7) and backup (+2.2), in that order of rising risk; C4, the
restore metadata pass (+1.7 ceiling); C5, the byte budget that brings B6 (wave 7) inside the guard. These are bounds and
ceilings. The one measured capture ratio is check's: 0.12 of 0.19 s (A1).

## 7. Open decisions for the operator

1. **Memory guard for the release**: keep the league's 2% per workload, or accept about +49 MiB summed on main (about +9%, still far below stock) for about **+4.0 points** (B1, B4, B5, B6, B7)?
2. **A third file reader by default** (small files only, B1): **+1.0 points** for +13 MiB on backups after the first; or leave it to the existing `--read-concurrency` flag?
3. **Three workers on local repositories in check and prune** (B6): **+1.45 points** for +7 to +13 MiB per phase; restore already has its third worker (E7).
4. **Sparse restore by default** (B7): **+0.9 points** (large-files restore -0.46 s), no memory; restored files get holes in place of written zeros.
5. **ctime-only changes** (B8): keep stock's re-read (recommended; the bound of roughly +1.5 to +2.7 is a judge artefact and fails the oracle), or not?
6. **Scope**: ports only (track 1, about **+1.2 to +1.8**) or also new work inside the lock wait and restore's metadata pass (track 3, bounds **+4.2 and +1.7**, effort L per command, the riskiest code in the tree)?

## 8. S020 (added 2026-10-07 at season end)

Sections 0 to 7 are unchanged and still written against `S019-merge`; nothing above is rebased. S020 promoted T2's
M004 tree. The new incumbent `INC/S020` is byte-identical to `TEAMS/T2/S020/M004` on all 431 non-test Go files
(`CMP`). There is no S021. Saved by the coordinator from the boundary agent's draft; the confirmation's per-phase
cells were re-checked against the record, the handoff contents and ablation figures were not.

**Sources** (box records read 2026-10-07 04:40 to 04:55 UTC; `SEV` =
`/opt/league/restic-league-v2/league/evidence/seasons/space-bunny/restic-v2/S020`): `PROG` =
`SEV/progress.jsonl` (`team-measured` events = the 16 match rows with their job ids; `rescreen`;
`season-merge-archive`; `idea-credit`); `PC` = `SEV/promotion-candidates.json` (the confirmation, job
`7398d456ee4e4911ba83654ded3ad77f`); `RS-T2M004`, `RS-T2M003`, `RS-T4M004` = the rescreen results, jobs
`25b0f0d693cc4264ab7f70a7879dd07b`, `b82e5ff8d1f84826a85298e47fcbf9ec`, `92da7c0bd71c453c8f1005cdee070771`
(`supervisor-jobs/<id>/evidence/result.json`); `KD` = `.../knowledge/S020.md`; `HO-T1` = `TEAMS/T1/S020/M004`
`b19-final-integration/HANDOFF.md`; `HO-T3` = `TEAMS/T3/S020/M004` `s020-m004-final/HANDOFF.md`; `HO-T4` =
`TEAMS/T4/S020/M004` `reference-evidence/final-b02/HANDOFF.md`; `CH-T3` = `SEV/channels/T3/M004.jsonl` (post
number); `CMP` = file comparison of the 16 team trees with `INC/S019-merge` and `INC/S020` (sha1 of every non-test
`.go` file under `cmd/` and `internal/`); `REV-S020` = `reviews/S020-review.md`.

**Tags added here.** `[row]` in S020 = one match row of 8 rounds, 2 per workload. `[12r]` = official 12-round
rescreen, 3 per workload. `[7p]`, `[5p]`, `[3p]` = builders' ablation or phase screen on a research lane.
**`[lock-wait]`** = the mechanism reads repository state during the 200 ms lock wait: it needs the R1 validation
(`REPAIRS.md`) and a review before it can ship.

**Limits.** Box access ended before the M001-M003 handoffs of T1, T3 and T4 were read. "By file name" means the
mechanism is described from the tree's file list and its M004 handoff only. No source file of an unpromoted tree was
read. Worth reading before a port: `TEAMS/T4/S020/M003` `reference-evidence/resumed-integration/HANDOFF.md`.

**Reading S020's prune cells.** Big-index forget-prune read +22.54% (T2 M004 `[row]`) and +23.83% (`RS-T2M004`) in
jobs that ran beside another judge job, and +6.70% (11/11) in `PC`, which ran alone. T4 M004 read +22.51% `[row]`
and +4.75% (3/3) in `RS-T4M004`, which ran alone (start and end times in the results). Every prune number below that
comes from a match row was taken with the second lane busy. Do not subtract `PC`'s cells from them; repeat on a quiet
lane.

### 8.1 The new incumbent

| | suite | main | big-index | large-files | remote | source |
|---|---|---|---|---|---|---|
| S020 vs `S019-merge`, time `[44r]` | **+1.033%** (35/44; 90% +0.89..+1.14) | +0.325 (8/11) | +3.456 (11/11) | +0.244 (10/11) | +0.108 (6/11) | `PC` |
| S020 vs `S019-merge`, memory | +0.511 | +1.31 | -0.46 | +0.49 | +0.71 | `PC` |
| S020 vs stock, time | **+29.936%** (44/44) | +31.28 | +45.55 | +23.97 | +18.95 | `PC` |
| S020 vs stock, memory | +43.91 | +43.18 | +46.34 | +39.45 | +46.67 | `PC` |
| median total seconds, S020 | 76.51 | 15.25 | 17.48 | 12.35 | 31.44 | `PC` arms |
| scale on S020: 1 s saved = suite points | | +1.64 | +1.43 | +2.02 | +0.80 | 100 / (4 x median total) |
| memory guard on S020: 2% of the 7 summed phase peaks | | 11.4 MiB | 31.7 MiB | 10.8 MiB | 12.2 MiB | `PC` arms, candidate phase medians |

Installed by S020 (do not rebuild). Per-phase table: `REV-S020`.

| in `INC/S020` | files | confirmed cells `[44r]` (`PC`) | tag |
|---|---|---|---|
| Bounded repack routing; consecutive pack updates without hashing the ID per blob; count check without enumeration | `index/repack_routes.go`, `repack_ordinals.go`, `repack_fallback.go`, `master_index.go`, `repository/prune_pack_run.go`, `index/associated_count_validate.go`, `prune.go` | forget-prune big-index +6.70 (11/11), main +3.38 (10/11), large-files +0.01 (6/11), remote +0.31 (7/11) | |
| Pack-type summaries during the index load | `repository/checker_pack_types.go`, `checker_capacity.go`, `checker.go` | check big-index +13.88 (11/11); that phase's peak 280.9 -> 292.6 MiB (-3.82%, 2/11) | |
| Metadata tree decode without content IDs | `data/tree_metadata.go`, `restorer/restorer.go` | with the restore row below | |
| Backup: compression preparation during the wait | `cmd/restic/backup_compression_wait.go`, `backup_compression_index.go`, `backup_compression_source_linux.go`/`_other.go`, `repository/compression_preparation.go`, `cmd_backup.go`, `repository.go` | backup-initial -0.08 (19/44), backup-unchanged -0.24 (16/44), backup-mutated +0.60 (28/44); team ablation -0.14 / +0.14 / -0.55%, unresolved (`KD`) | `[lock-wait]` |
| Check: one pack read during the wait | `cmd/restic/check_wait_pack.go`, `repository/check_wait_pack.go`, `check_wait_range.go`, `check_wait_identity_*.go`, `check_wait_watch_*.go`, `retry/prepared_range.go`, `prepared_load.go`, `cmd_check.go` | check main +1.05 (8/11), large-files +0.70 (7/11), remote -0.12 (3/11) | `[lock-wait]` |
| Restore: snapshot and a tree prefix read during the wait | `cmd/restic/restore_snapshot_wait.go`, `repository/restore_wait_reader.go`, `restorer/restore_wait_trees.go`, `cmd_restore.go`, `wait_index_load.go` | restore main -0.05 (5/11), big-index -0.20 (5/11), large-files +0.48 (7/11), remote +0.28 (11/11) | `[lock-wait]` |

The three `[lock-wait]` rows are unreviewed (`REV-S020`). Their cells net to about zero: at most about +0.15 suite
points on the positive ones (main and large-files check, large-files restore, backup-mutated on large-files and
remote) against about -0.1 on the negative ones (backup-initial and backup-unchanged). About +1.0 of the +1.03 is
big-index check (0.51 s), big-index prune (0.15 s), main prune (0.04 s) and main check (0.02 s), from `PC` medians.

Earlier rows after S020: **A1** built twice: T2's one-pack read (installed, smaller than the prototype) and T1's
verification with clean-ID transfer (row A22). **A3**: `checker_worker_admission.go` is in T1's and T4's trees (by
file name; not installed; not measured alone). **A2**: T4 tried metadata transfer stripes in M003 and excluded them
(a scripted refusal changed later GET outcomes, `KD`). **A11**: superseded by A16. **C1**: backup T2 (installed, no
gain) and T1 (loss); restore T2 (installed), T1, T4; check T2, T1; ls T3 (loss); prune: no build in any file list.
**C4**: refuted as designed (`KD`: early per-file metadata changes failure behaviour). **C8**: installed in part
(big-index check and prune). **C5**: A16 is a bounded relative for repack. **C2, C3, C6, C10, A5**: no build seen in
any tree's file list (`CMP`). **E20**: the big-index check peak now has a known owner (type summaries, +11.7 MiB);
remote reads +0.11% (6/11). Whether the remote check is still two-valued was not examined. **Gates**: T1 M003 was
past the memory guard on large-files (-2.12%, `PROG`); no row was past the 1% time gate (nearest T2 M002, large-files
-0.96%). No new B candidate.

### 8.2 The sixteen trees

Suite = main / big-index / large-files / remote (wins of 8); phases = backup-initial, backup-unchanged,
backup-mutated, restore, check, forget-prune, ls, suite-wide (wins of 8). All cells `[row]` from `PROG`; files and
the last column from `CMP`. Each tree holds its team's earlier matches.

| tree `TEAMS/<T>/S020/<M>` | added in this match | suite; memory | phases bi / bu / bm / rs / ck / fp / ls | in `INC/S020`? |
|---|---|---|---|---|
| T1 M001 (job `e52886d6…`) | check verification inside the wait, clean pack IDs transferred (`cmd/restic/check_wait.go`, `repository/checker_wait.go`) `[lock-wait]`; worker admission gate (`checker_worker_admission.go`); metadata slot reuse (`restorer/metadata_task_recycler.go`) | -0.19 = +0.16 / -0.27 / -0.57 / -0.09 (2); +0.10 | +0.04 (3) / -0.71 (3) / -0.31 (4) / -0.20 (4) / +0.05 (2) / -0.47 (4) / +0.02 (2) | no: 8 files, none identical |
| T1 M002 (`04f7655c…`) | restore snapshot read in the wait (`cmd/restic/restore_snapshot_wait.go`, 53 lines, T1's own) `[lock-wait]`; `prune_kept_pack.go`, `prune_pack_accumulator.go`; `restorer/metadata_task_payload.go` | +0.26 = +0.01 / +0.30 / +0.40 / +0.33 (7); +0.96 | -0.43 (3) / -0.28 (3) / +0.10 (6) / +0.34 (6) / **+2.48 (7)** / -0.48 (2) / +0.15 (4) | no: 14 files, none identical |
| T1 M003 (`c7ba9319…`) | backup file preparation in the wait (`cmd/restic/backup_file_wait.go`, `archiver/wait_file_preparation.go`, `wait_file_open_*.go`, `archiver.go`, `file_saver.go`) `[lock-wait]`; `index/associated_count_validation.go`; `restorer/tree_oversize_hint.go` | +0.19 = +0.29 / +0.60 / -0.45 / +0.33 (5); -0.57, **large-files -2.12 (past the guard)** | -0.44 (4) / -1.02 (0) / +0.02 (4) / +0.65 (6) / +0.78 (6) / +1.26 (4) / -0.35 (2) | no: 23 files, none identical |
| T1 M004 (`93459dd2…`) | `backup_file_wait_admission.go`, `archiver/wait_file_retirement.go`; membership-only final prune check (`index/associated_empty.go`); `restorer/planned_large_file.go`; edits `repack.go`, `filerestorer.go` | +0.46 = +0.11 / +1.66 / -0.31 / +0.39 (6); -0.11 | +0.09 (5) / -0.71 (3) / -0.36 (4) / +0.64 (6) / +0.47 (4) / +2.61 (6) / +0.11 (4) | no: 29 files, none identical |
| T2 M001 (`15253c9c…`) | `data/tree_metadata.go`; `index/associated_count_validate.go`; `prune_pack_run.go`; `prune.go` | +0.41 = +0.03 / +1.07 / +0.47 / +0.08 (5); +0.53 | -0.15 (3) / +0.43 (7) / +0.49 (5) / +0.08 (5) / +1.33 (4) / +0.96 (4) / -0.12 (5) | yes: 4 of 5 files identical |
| T2 M002 (`faf46225…`) | restore snapshot and tree reads in the wait (`restore_snapshot_wait.go`, `restore_wait_reader.go`, `restore_wait_trees.go`) `[lock-wait]`; `checker_pack_types.go` | -0.17 = -0.26 / +0.59 / -0.96 / -0.03 (3); +0.75 | +0.32 (6) / +0.23 (5) / +0.04 (3) / -1.37 (3) / -1.00 (3) / +0.61 (3) / -0.40 (5) | yes: 10 of 12 identical |
| T2 M003 (`b5fe3e38…`) | bounded repack routing (`index/repack_routes.go`, `master_index.go`) | +1.89 = +0.56 / +5.14 / +1.62 / +0.26 (7); -0.16. `[12r]` +1.38 (9/12) | +0.30 (5) / +0.19 (4) / -0.22 (5) / **+2.62 (7)** / +0.95 (6) / +7.03 (5) / -0.36 (3) | yes: 12 of 14 identical |
| T2 M004 (`3cd102e4…`) | backup compression preparation in the wait `[lock-wait]`; check one-pack read in the wait `[lock-wait]`; `repack_ordinals.go`, `repack_fallback.go`; `checker_capacity.go` | +1.81 = +0.83 / +5.68 / +0.36 / +0.38 (7); +0.86. `[12r]` +1.41 (7/12). `[44r]` +1.03 | +0.17 (6) / -0.52 (2) / -0.32 (3) / +0.93 (6) / **+5.11 (8)** / +5.98 (5) / -1.18 (0) | **it is `INC/S020`** (34 of 34) |
| T3 M001 (`48c0e292…`) | repack group values (`index/repack_group_values.go`, `master_index.go`, `repack.go`); restore metadata lookahead and projection (`restorer/metadata_lookahead.go`, `metadata_projection.go`) | +0.58 = +0.11 / +2.16 / -0.39 / +0.44 (5); -0.31 | +0.36 (4) / +0.14 (4) / +0.36 (5) / -0.03 (4) / -0.76 (3) / +3.47 (6) / -0.03 (5) | no: 6 files, none identical |
| T3 M002 (`05572f7a…`) | exact-capacity collection (`index/repack_exact_capacity.go`); ls tree loads in the wait (`cmd/restic/listing_wait.go`, `repository/listing_wait_source.go`, `walker/listing_wait_prefix.go`, `data/metadata_tree_reader.go`, `cmd_ls.go`) `[lock-wait]` | +0.90 = 0.00 / +3.77 / +0.15 / -0.34 (4); -0.14 | -0.52 (2) / -0.01 (5) / +0.21 (5) / +0.50 (5) / +0.02 (5) / +5.98 (5) / -0.69 (0) | no: 16 files, none identical |
| T3 M003 (`78cf7fd4…`) | repack plan hints and bitmap consumption (`index/repack_plan_hints.go`, `prune_repack_hints.go`, `repack_consumption.go`, `prune.go`); `restorer/metadata_paths.go` | +0.69 = +0.34 / +2.39 / -0.04 / +0.09 (6); +0.02 | +0.38 (4) / +0.02 (5) / -1.29 (3) / +0.15 (6) / +0.67 (6) / +4.39 (7) / -0.85 (2) | no: 21 files, none identical |
| T3 M004 (`edd9f5b7…`) | selected-pack ordinal rejection (`index/repack_selected_values.go`); **repack completion overlap** (`repository/repack_completion_overlap.go`); ls permission memo (`cmd/restic/listing_permissions.go`) | +0.50 = +0.36 / +2.17 / -0.03 / -0.52 (5); -0.43 | -0.65 (2) / -0.51 (2) / +0.07 (4) / -0.20 (3) / -1.39 (5) / **+7.74 (7)** / -0.24 (1) | no: 24 files (16 new, 8 edited), none identical |
| T4 M001 (`92d63ac9…`) | by file name: `checker_worker_admission.go`, `index/associated_values.go`, `prune_pack_sizes.go`, `restorer/metadata_preparation.go`, edits `tree_prefetch.go`, `prune.go`, `checker.go` | +0.01 = -0.31 / +0.43 / +0.66 / -0.75 (4); +0.97 | +0.32 (5) / +0.32 (5) / -0.41 (3) / -0.57 (1) / +0.40 (4) / -0.08 (5) / -0.12 (2) | no: 8 files, none identical |
| T4 M002 (`80644470…`) | by file name: restore snapshot read and decode in the wait (`cmd/restic/restore_snapshot_wait.go`, 48 lines, T4's own; `repository/restore_snapshot_decode.go`) `[lock-wait]`; `restorer/blob_writer_lease.go`, `fileswriter.go`, `filerestorer.go`; `prune_pack_run.go` (T4's own) | +0.32 = +0.37 / +0.79 / -0.17 / +0.28 (5); +1.11 | -0.01 (3) / +0.36 (5) / **+0.75 (4)** / +0.02 (5) / +0.56 (4) / +0.65 (5) / -0.35 (2) | no: 15 files, none identical |
| T4 M003 (`72ba93b5…`) | by file name: check read overlap behind a metadata gate (`cmd/restic/check_read_overlap.go`, `repository/check_metadata_gate.go`, `retry/scoped_reports.go`, `backend_retry.go`, `cmd_check.go`); filtered repack values (`index/repack_filtered_values.go`, `prune_pack_membership.go`, `master_index.go`) | +1.36 = +0.42 / +4.47 / -0.12 / +0.67 (7); +0.24 | -0.13 (4) / -0.26 (3) / -0.45 (3) / +0.96 (7) / +2.45 (7) / +5.53 (4) / -0.36 (1) | no: 23 files, none identical |
| T4 M004 (`4a5084f5…`) | check-only filtering before descriptor construction (`index/check_filtered_packs.go`, `repository/check_filtered_packs.go`); bounded occurrence directory and replay for prune (`index/repack_occurrences.go`) | +1.80 = -0.06 / +5.40 / +0.28 / +1.58 (6); +1.04. `[12r]` +1.05 (10/12) | +0.16 (6) / -0.56 (3) / +0.54 (5) / +0.68 (2) / +4.64 (7) / +5.45 (4) / -1.15 (0) | no: 26 files, none identical |

### 8.3 The merge archive's per-phase bests (`PROG` `season-merge-archive`)

| phase | archive best | what produced it | at more rounds | in `INC/S020`? |
|---|---|---|---|---|
| check-read-data | T2 M004 +5.11% (8/8) | big-index +13.57 (2/2), main +2.40, large-files +2.57, remote +1.93: the pack-type summaries plus M004's other check additions (one-pack read in the wait, capacity) | `[12r]` +3.06 (6/12); `[44r]` +3.88 (29/44) = +1.05 / +13.88 / +0.70 / -0.12 | yes: it is the promoted tree |
| forget-prune | T3 M004 +7.74% (7/8) | main +5.25 (2/2), big-index +13.70 (2/2), large-files +11.61 (2/2), remote +0.42 (1/2): T3's collection work for big-index and the repack completion overlap for main and large-files | never rescreened; `[7p]` and `[3p]` in rows A16 and A20 | **no** (A16, A20) |
| restore | T2 M003 +2.62% (7/8) | large-files +8.54 (2/2); main +0.50, big-index +0.70, remote +0.75. The tree's restore work is the snapshot and tree reads in the wait and the metadata tree decode | same tree `[12r]`: restore +0.02 (8/12), large-files +0.09; successor `[44r]`: +0.13 (28/44), large-files +0.48 | yes, the files are identical in `INC/S020`; the +2.62 did not repeat |
| backup-mutated | T4 M002 +0.75% (4/8) | remote +3.52 (1/2), large-files +0.62, main -0.61, big-index -0.54. **The tree changed no backup file** (`CMP`: check, prune and restore files only). Noise | T4's later trees with the same files: -0.45 (M003), +0.54 (M004), `[12r]` -0.08 (5/12) | no, and nothing to port |

### 8.4 Port list, continued: S020 ideas not in `INC/S020` with a measured gain

Same columns as section 1. Estimates use the S020 scale of 8.1 and are the boundary agent's. Largest first.

| id | idea | source and code | files | measured effect | why not installed | overlap or conflict with the incumbent | est. now (suite points) | memory | effort | release risk |
|---|---|---|---|---|---|---|---|---|---|---|
| A16 | **Repack completion overlap** (T3's prune execution work): each repack reader streams one successor pack while its predecessor's save callbacks finish; at most 512 descriptors for the pair; reader count, the shared 8 MiB plaintext budget and uploader limits unchanged; every admitted save joins before return; upload and index completion still precede deletion | T3 S020 M004, `TEAMS/T3/S020/M004`; isolated diff in the tree's `s020-m004-final/runtime-above-m003.diff` | new `repository/repack_completion_overlap.go` (168 lines), `repack_consumption.go` (36, from M003); edits in `repack.go` | new-file-only ablation, 7 rotated pairs each: prune **main +4.34% (7/7, band 1.16), big-index +2.70% (7/7, band 1.34), large-files +10.25% (7/7, band 1.20)**, remote unresolved `[7p]` (`CH-T3` 28, 29). Whole tree: prune main +5.25, big-index +13.70, large-files +11.61 (2/2 each), remote +0.42 `[row]` (`PROG`); main +6.09, big-index +27.95, large-files +11.79, remote -0.41 unresolved `[3p]` (`HO-T3`). Remote requests and bytes equal to the incumbent's (117 requests) | T3's tree was third at +0.50 and never rescreened; the automatic merge was skipped | None in code: `repack.go` in `INC/S020` equals `S019-merge`'s (`CMP`), and the installed prune gain is in planning and collection, a different stage. `INC/S020` reads large-files prune +0.01 and main +3.38 `[44r]`. Same file as A10, A11, C5; it supersedes A11 (positive on big-index where A11 was negative). NOTE: repair R2 changes how repack reads packs (ranges of at most 4 MiB, handler after `Load`); the overlap must be re-fitted to that | **about +0.4** if the ablation holds on `INC/S020`: large-files +0.24, main +0.09, big-index +0.08. Repeat on a quiet lane first | overlap alone: big-index -1.24% inside its band (`CH-T3` 28). Whole tree, prune peak: large-files 80.4 against 77.1 MiB, remote 99.1 against 96.5, unresolved `[3p]` (`HO-T3`) | M | Medium to high. Prune is the destructive command. Open in the handoff: simultaneous upload failures during the overlap, stalled-consumer lifetimes. Not `[lock-wait]` |
| A17 | **Check-only filtering before descriptor construction**: selected packs are filtered by nibble and selection before any descriptor is built; grouping, order, duplicates and empty packs preserved | T4 S020 M004, `TEAMS/T4/S020/M004`; `reference-evidence/final-b02/addition.diff` | new `repository/index/check_filtered_packs.go` (48), `repository/check_filtered_packs.go` (12); edits in `index/master_index.go`, `checker.go` | ablation against T4's M003 base, 7 pairs: **check +7.72% (7/7)**, phase memory +3.15% saved, unresolved; an independent repeat +9.28% `[7p]` (`HO-T4`; workload not named, big-index by context). Whole tree, big-index check: +10.29 (2/2) `[row]`, **+13.45 (3/3)** `[12r]` (`RS-T4M004`) | T4 M004 rescreened third (+1.05, 10/12) | Same cell as the installed pack-type summaries (`INC/S020` big-index check +13.88 `[44r]`). Both cut index interpretation in check; whether they add is not measured. `master_index.go` differs in both trees | **0 to +0.3** (+0.33 if fully additive: 7.72% of 3.03 s) | whole tree, big-index check peak **-9.15% (0/3), about +26 MiB** `[12r]`; `INC/S020` already spends +11.7 MiB there; the guard is 31.7 MiB | M | Low to medium (check must still see every pack and blob; the oracle with a corrupted pack passed in the team's run) |
| A18 | **Check read overlap behind a metadata gate** (by file name; mechanism not read) | T4 S020 M003, in `TEAMS/T4/S020/M003` and `M004` | new `cmd/restic/check_read_overlap.go` (114), `repository/check_metadata_gate.go` (31), `backend/retry/scoped_reports.go` (54); edits in `retry/backend_retry.go`, `cmd_check.go`, `checker.go` | T4 M003 check: main +2.12 (2/2), big-index +4.55 (2/2), remote +3.11 (2/2), large-files +0.03 `[row]`. T4 M004 check: **main +4.63 (3/3), large-files +2.51 (2/3), remote +2.31 (3/3)** `[12r]`, not isolated from A17 | as A17 | `INC/S020` reads check main +1.05, large-files +0.70, remote -0.12 `[44r]`, with its own one-pack read in the wait in `cmd_check.go`: the two must be reconciled in that file. A related transfer-stripe prototype of the same match was excluded (`KD`) | **0 to +0.3** (difference of the two jobs: main +0.10, large-files +0.06, remote +0.13) | not attributed | M | Unknown until read. **Whether it reads during the lock wait is not established: check before porting, and tag it if it does** |
| A19 | **Restore: selected-occurrence reuse, metadata task slots reused after ordered settlement, bounded oversized-tree hints** | T1 S020 M001-M004, `TEAMS/T1/S020/M004` | new `restorer/metadata_task_recycler.go`, `metadata_task_payload.go`, `tree_oversize_hint.go`, `planned_large_file.go`; edits in `restorer.go`, `filerestorer.go`; the tree's `cmd/restic/restore_snapshot_wait.go` (53 lines) is a snapshot read in the wait `[lock-wait]` | restore main +1.18 and +1.20 (2/2 both), big-index +1.33 (2/2) and +0.82, remote +0.74 and +0.87 (2/2 both), large-files -0.65 and -0.33 `[row x2]` (M003, M004); remote restore +0.30% (5 pairs, resolved) against the team's M002 base (`HO-T1`) | T1 was the season bottom | `INC/S020` reads restore main -0.05, big-index -0.20, remote +0.28 `[44r]` with its own wait reads in the same files. Metadata stays behind the global file-stage success | **0 to +0.13**, not isolated | whole tree: large-files -2.12% in M003 (past the guard), -1.00% in M004; main -1.33% (`PROG`): not attributed | M | Low for the restorer parts; the snapshot read is `[lock-wait]` and can be left out |
| A20 | **Prune collection, T3's route**: repack group values, exact-capacity collection, plan hints with bitmap consumption, selected-pack ordinal rejection | T3 S020 M001-M004, `TEAMS/T3/S020/M004` | new `index/repack_group_values.go`, `repack_exact_capacity.go`, `repack_plan_hints.go`, `repack_selected_values.go`, `repository/prune_repack_hints.go`; edits in `index/master_index.go`, `prune.go` | big-index prune +11.82, **+23.63**, +13.26, +13.70 (2/2 each) `[row x4]`; complete prune big-index +24.84% (3/3), main +1.72% (5/5) `[5p]` (`CH-T3` 21); ordinal rejection alone unresolved on large-files `[7p]` (`CH-T3` 42) | as A16 | The same repository-sized work that the installed routing removes (`INC/S020` big-index prune +6.70 `[44r]`); `master_index.go` and `prune.go` differ in both trees | **0 to +0.1** | large-files hint memory signal open (`HO-T3`) | M to L | Medium |
| A21 | **Prune: bounded occurrence directory and replay** after the small collector refuses (256 KiB admission; capture refusal publishes nothing) | T4 S020 M003-M004, `TEAMS/T4/S020/M004` | new `index/repack_occurrences.go` (153), `repack_filtered_values.go` (44), `repository/prune_pack_membership.go`; edits in `master_index.go`, `prune.go` | directory and hook: **prune +2.82% (7/7)**, phase memory -1.94% unresolved; an independent repeat +3.36% `[7p]` (`HO-T4`). Whole tree big-index prune +22.51 `[row]`, **+4.75 (3/3) `[12r]` on a quiet lane** | as A17 | Same lever as the installed routing, which read +6.70 `[44r]` on a quiet lane: T4's is behind it | **0 to +0.08** | unresolved | M | Medium |
| A22 | **Check verification inside the lock wait with transfer of confirmed clean pack IDs** (the form of prototype A1) | T1 S020 M001, `TEAMS/T1/S020/M004` | new `cmd/restic/check_wait.go`, `repository/checker_wait.go` (175), `checker_worker_admission.go`; edits in `cmd_check.go`, `wait_index_load.go`, `checker.go` | check main +0.99, **+4.28 (2/2)**, +0.74, +2.30; large-files +0.39, +3.55, +2.44, -1.09; remote -0.36, +1.66, +0.35, -0.28 `[row x4]` (`PROG`); the team closed its attribution (`HO-T1`) | T1 was the season bottom | Replaces the installed one-pack read (`INC/S020` main +1.05, large-files +0.70 `[44r]`) | **0 to +0.05** over the installed form | not attributed | M | **High. `[lock-wait]`.** A clean result is carried past the wait; T3's cached structural check of the same class was refuted by a same-size corrupt pack that the cached path missed (`KD`) |
| A23 | **ls: command-local permission-string memo** | T3 S020 M004, `TEAMS/T3/S020/M004`; `experiments-B14/s020-m004-b14/new-only.diff` | new `cmd/restic/listing_permissions.go` (32); edit in `cmd_ls.go` | main ls **+0.82% (7/7)**, just past its 0.79% band `[7p]` (`CH-T3` 31) | as A16 | none (`cmd_ls.go` in `INC/S020` equals `S019-merge`'s) | **+0.01** | flat | S | Low. Not `[lock-wait]`; take it without T3's listing wait |

### 8.5 S020 work not in `INC/S020` with no measured gain (new closed ground, as built)

| what | tree | record | tag |
|---|---|---|---|
| Backup file preparation during the wait (source files opened, read and chunked before the confirmed lock, admitted after the empty snapshot listing) | T1 M003, M004 | 7-run ablation: main backup-unchanged +0.14% time for 6.02% more memory, backup-mutated -0.41%, unresolved; base comparison: main unchanged -0.68%, main mutated -1.15%, large-files unchanged -0.81%, remote unchanged -0.58% (`HO-T1`); rows backup-unchanged -1.02 (0/8), -0.71 (3/8); M003 past the memory guard | `[lock-wait]` |
| ls tree loads during the wait (C1 for ls) | T3 M002-M004 | ls -0.69 (0/8), -0.85 (2/8), -0.24 (1/8) `[row x3]`; 17 requests against 14 on remote; big-index ls peak 189.0 against 176.9 MiB (`HO-T3`) | `[lock-wait]` |
| Restore snapshot read and decode during the wait; blob writer lease | T4 M002-M004 | restore +0.02 (5/8), +0.96 (7/8), +0.68 (2/8) `[row]`; **-0.44 (2/12), big-index -1.38 (0/3)** `[12r]` | `[lock-wait]` (the snapshot read) |
| Prune kept-pack and pack accumulator; count validation | T1 M002-M004 | big-index prune +8.44 (2/2) `[row]`, +8.46% (3 pairs) (`HO-T1`); the same work as the installed `prune_pack_run.go` and `associated_count_validate.go` | |
| Early per-file metadata before global restore success (C4 as designed) | T2, T3 M001 experiments | refuted: it changes what a later fatal error leaves behind (`KD`) | |
| Cached reuse of a speculative structural check | T3 M001 review | refuted: zero cached errors against one fresh error on a same-size corrupt pack (`KD`) | `[lock-wait]` class |
| Metadata transfer stripes with refusal fallback (a relative of A2) | T4 M003 experiments | excluded: a scripted refusal changed later GET outcomes, diagnostics or cache publication (`KD`) | |
| Restore metadata preparation and compact decoding | T2, T3 M001-M003 | inconclusive: metadata tail 0.469 against 0.470 s (`KD`) | |

## 9. Not from the record

My own remarks, not league findings. None is measured.

- **Flags already cover four B rows.** `--read-concurrency 3` is B2, `-o local.connections=3` is B6 plus an unbudgeted third
  restore worker (the installed extra slots stand down when the option is given), `restore --sparse` is B7, `backup --ignore-ctime`
  is B8. A release can keep stock's defaults and name these in its notes. Checked in `trees/stock`: all four exist.
- **`--no-extra-verify` exists in stock** (`internal/global/global.go`), so C2's bound (+3.0) is already a user's
  informed choice. It must not become a default.
- **Layout (E1)**: add the symbol-address check to the release build script for every target, since each has its own layout.
- **Races (E3, E4)**: run the lock-wait paths under `go test -race` on a thread-safe in-memory backend; the league
  never could.
- **C1 arithmetic**: if the other four commands captured what check's rough version does (0.12 of 0.19 s), C1 beyond
  check would be worth about +2.5 of its +4.2 bound.

## 10. Coordinator addenda (2026-10-06, after the inventory agent's report)

**S016 unpromoted trees, checked on the box** (read-only file comparison against `incumbents/S015`; the inventory
agent had no local record of them). All three touch the same files as the promoted T1 M004 tree
(`backend/sema/backend.go`, `repository/checker.go`, `restorer/restorer.go`, `backend/local/config.go`,
`global/global.go`): the third check worker and the restore window, which are installed. Pieces that exist only in
these trees, none of them in `S019-merge`; the merge agent should read each and bench it alone before deciding:

| id | tree | only-here files | status |
|---|---|---|---|
| A13 | `TEAMS/T2/S016/M004` (rows +3.5 to +3.9 on main vs S015 `[row][old:S015]`) | new `internal/restorer/file_status.go` (71 lines); edits in `repository/repository.go` | mechanism unread; unmeasured alone |
| A14 | `TEAMS/T4/S016/M004` | new `internal/repository/checker_assist.go` (121 lines); edit in `internal/ui/backup/text.go` | mechanism unread; unmeasured alone |
| A15 | `TEAMS/T3/S016/M004` | edits in `internal/backend/backend.go`, `internal/backend/local/local.go` | mechanism unread; unmeasured alone |

**Platform build check** (`build-matrix.log`, Go 1.27.0 on the workstation): `incumbents/S019-merge` builds for all
25 of restic's release targets (windows, linux, darwin, freebsd, openbsd, netbsd, solaris, aix; amd64, 386, arm,
arm64, mips*, ppc64le, riscv64, s390x). 25 of 25 pass.

**`go vet` finding (release blocker until fixed):** `internal/ui/termstatus/status_test.go:207` calls `term.Flush`,
which `ui.Terminal` does not have: that test package does not compile in the incumbent tree on any OS (stock is
clean on linux and darwin). The judge never saw it because it builds the candidate's non-test files with stock's
tests. The league changed eight stock test files in all (`review/FILES.md`); the published tree must pass
`go test ./...` with its OWN test files, so run exactly that on the box as a release check. The two unkeyed-field
messages on windows are upstream's.
