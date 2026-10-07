# Soundness review: backup write path and process start-up (`incumbents/S019-merge` vs stock restic ba802d42)

> Copy of a working document of the league, published as a record. Scrubbed: workstation paths, host names, personal names of personas and role identifiers were removed or replaced; nothing else was changed, except that the league box's working prefix reads `/opt/league/` in every published copy. Paths under `/opt/...` are the league box's layout. Where this copy and the documents under `league/docs/` disagree, the documents under `league/docs/` are the later statement.

Reviewer area: section "Area `backup`" of `review/FILES.md` (32 files), plus the chunk scanner and the scrypt change that the brief names (they live in `internal/repository`). Reviewed on the Windows workstation, 2026-10-06. Paths are relative to `trees/S019-merge` unless stated. "Reproduced" means a witness test in `review/witness-backup/` shows it on this machine.

## 1. Summary

**Do not publish as it stands.** One finding can lose data silently, three change behaviour users will meet, and one test package in the tree does not compile.

Counts over 21 mechanisms:

| Verdict | Count |
| --- | --- |
| UNSOUND, BLOCKER (probability low and unmeasured) | 1 |
| UNSOUND, MEDIUM | 3 |
| UNSOUND, LOW | 1 |
| NEEDS-RUN (Linux-only code, not runnable here) | 2 |
| SOUND-UNDER-CONTRACT | 5 |
| SOUND | 9 |

The four findings that matter:

1. **The backup keeps an index it read before its lock was confirmed** (BLOCKER, low probability). Stock reads the snapshot list and the index only after the 200 ms lock check. The candidate reads them during the wait and keeps them. If a prune finishes inside that wait, the backup treats pruned blobs as stored and writes a snapshot that references missing data. Reproduced under a forced schedule by two independent witnesses (mine at repository level, the readside reviewer's at command level, where stock passes and the candidate fails); not reproduced with separate real processes (experiment N4).
2. **A failing stdout silences stderr** (MEDIUM). When stdout cannot be written (full disk under a redirected log, closed descriptor), every later message, error messages included, is held in memory and never printed. Reproduced.
3. **`check` and every other tree reader accept a malformed tree that stock rejects** (MEDIUM, needs an owner ruling). A node name holding a raw `"` or newline fails in stock with `Unquote: invalid syntax`; the candidate accepts it. restic itself never writes such names. Reproduced.
4. **A wrong password costs twice the key derivations** (MEDIUM, small impact). Reproduced.

What held up, with witnesses: chunk boundaries equal stock's on every input tried (2,757 feed runs, 622 cut points, 3 polynomials); the whole rewritten file saver produced stock's chunk IDs for 168 files / 811 MiB with and without parent hints; hashes always matched the bytes stored; node and tree JSON is byte-identical to stock for 300,000 random nodes; the parallel scrypt gives stock's key for 1,278 valid parameter sets.

What this review did not establish: anything under the race detector, anything on Linux-only code paths (`internal/fs/*_linux.go`), Ctrl-C and backend failures with real processes, and files above 4 GiB end to end. Those are section 4.

## 2. Mechanisms, most severe first

| # | Mechanism | Files : lines | Verdict, severity | Contract or failure scenario | Witness or deciding experiment |
| --- | --- | --- | --- | --- | --- |
| 1 | Snapshot list and index loaded during the lock settle wait, kept after the lock is confirmed | `cmd/restic/cmd_backup.go:541-567, 580, 598-602`; `cmd/restic/backup_index_load.go:29-45, 56-75`; `cmd/restic/lock.go:16-50`; `internal/repository/lock_file.go:137-155` | **UNSOUND, BLOCKER** (silent: snapshot references pruned blobs). Probability low and unmeasured. | Start: repository with unreferenced blob X (for example from an interrupted backup). Backup B checks locks (none) and saves its lock file slowly (retried upload, stalled fsync). Prune P locks exclusively, is confirmed because B's lock is not visible yet, and works. B's lock lands, B loads the index (X present). P deletes the index and pack, unlocks. B's check passes. B treats X as known, does not upload it. Stock would load the index now and upload X. Details in 3.1. | `lock_dwell_stale_index_witness_test.go`: **reproduced under a forced schedule** (`known = true`, then `id … not found in repository`). Also reproduced independently at command level by the readside reviewer: `witness-readside/cmd_lock_wait_stale_witness_test.go`, `TestWitnessLockWaitStaleBackupVsPrune` (stock passes, candidate fails, `check` reports missing trees). Separate real processes: N4. |
| 2 | Terminal output queue: batching for regular-file stdout/stderr, one shared retry queue for both streams | `internal/ui/termstatus/status.go:20-28, 122-126, 160-167, 388-577, 584-612`; `internal/ui/termstatus/stdio_wrapper.go:15-189` | **UNSOUND, MEDIUM** | (a) stdout write returns 0 bytes and an error (ENOSPC on the log's disk, EBADF). The record stays at the head of the queue (`status.go:471-479`) and nothing behind it is written, stderr included, not even at shutdown. Stock writes each message independently. (b) Everything printed meanwhile is retained in memory without bound (`status.go:529-532, 541`); stock drops it. (c) The candidate's own tests for this code do not compile, and 3 fail once they do (section 5). | `status_witness_test.go`: **both reproduced** (stderr received only `write failed` lines; 2,160,013 bytes / 20,000 lines retained). |
| 3 | Direct tree decoder used by `LoadTree`, the parent finder and `TreeNodeReader` | `internal/data/parent_tree.go:40-101` (name handling `:87-95`); `internal/data/tree.go:145-151`; `internal/data/parent_finder.go:14-41`; `internal/data/tree_node_reader.go:22-53` | **UNSOUND, MEDIUM** (owner ruling: does this count as a weakened check?) | Stock runs `strconv.Unquote` on every node name; the candidate only when the name contains a backslash. A tree whose node name holds a raw `"` or newline (a foreign or buggy writer; blob hash intact) is rejected by stock everywhere, `check` included (`internal/data/tree_stream.go:60` calls `LoadTree`). The candidate accepts it and restores a file with that name. On trees written by restic the two decoders agree. | `node_tree_witness_test.go`: `TestWitnessTreeIteratorLeniency` **reproduced**; `TestWitnessTreeIteratorEqualsStockOnResticTrees` passes (300 random trees). Found independently by the readside reviewer (`witness-readside/data_tree_name_witness_test.go`, REVIEW-readside.md X3). |
| 4 | Key listing started before the config check and replayed into the key search | `internal/global/open_key_listing.go:19-27, 45-79, 85-117, 121-134, 136-144`; `internal/global/global.go:311-325, 399` | **UNSOUND, MEDIUM** (resource regression on a failure path; small) | Wrong password supplied without a prompt (`RESTIC_PASSWORD`, password file or command). Stock tries each key once. The candidate tries each key from the prefetched list, then, because that failed, runs the whole search again (`open_key_listing.go:138-142`): 2x scrypt work and 2 listings before the same error. With 20 keys that is 40 derivations. Right password: unchanged result, and the one listing used is the prefetched one (a failed prefetch falls back to an ordinary listing). The wrapper stays in the backend chain for the life of the process (`global.go:311-320`). | `open_key_witness_test.go`: **reproduced** (4 keys: stock 4 loads / 1 list, candidate 8 loads / 2 lists, same error text). |
| 5 | Parallel scrypt with a stride-4 memory trade | `internal/repository/crypto/parallel_scrypt.go:16-88`; `internal/repository/crypto/kdf.go:73` | **UNSOUND, LOW** for malformed key parameters. SOUND for every parameter set stock accepts. | Valid parameters: output identical to `x/crypto/scrypt`. Invalid: `simple-scrypt`'s check only requires N even, so N = 6 or 12 reaches the KDF. Stock then fails in `scrypt.Key` ("N must be > 1 and a power of 2"). The parallel path (p >= 2, GOMAXPROCS >= 2) has no check: N = 6 panics in a worker goroutine (`parallel_scrypt.go:74`, process crash); N = 12 and 20 silently derive a non-scrypt key. Only a hand-made key file can carry such N. Memory: `workers x 128·r·N/4` (`:18, :43`), never above stock's `128·r·N`; CPU about 1.75x per lane by arithmetic (not measured). | `kdf_witness_test.go`: parity **passes** (1,278 sets, GOMAXPROCS 1 to 8); the malformed-N divergence is **reproduced**. |
| 6 | Linux: path stat built straight from `syscall.Stat_t` | `internal/fs/path_stat_linux.go:16-76`; `internal/fs/fs_local.go:52-54, 179-197`; `internal/fs/path_stat_other.go:7-19` | **NEEDS-RUN** (Linux only) | By reading: same syscalls, same EINTR retry, same `*os.PathError`, same field expressions as `stat_unix.go`, and `filepath.Base` equals `os`'s basename for every path that can be stat'ed. The node name comes from this struct, so it must be run. `_other` is stock's code moved (`os.Stat`/`os.Lstat` + `extendedStat`). | N2 (`path_stat_linux_witness_test.go`, compile-checked for linux only). |
| 7 | Linux: regular files opened without the runtime poller, `O_NOATIME` at open; helper `OpenRegularFileNoPoller` | `internal/fs/file_open_linux.go:17-50`; `internal/fs/fs_local.go:124-177`; `internal/fs/file.go:59-68`; `internal/fs/file_open_other.go:7-10` | **NEEDS-RUN** (Linux only). One LOW behaviour change on all platforms. | By reading: `unix.Open` adds `O_LARGEFILE` and `O_CLOEXEC`, the descriptor is fstat'ed and must be regular before use, `O_NOATIME` falls back on any error. Behaviour change: if a file stops being regular between lstat and open, the error text is `file "x" changed type while opening; expected a regular file` (`fs_local.go:165`) where stock says `file "x" changed type, refusing to archive`. Exit code is the same (3). `_other` = `os.OpenFile` plus the same check. | N2, N3. |
| 8 | Debug switch: overlapped start-up is turned off when `DEBUG_LOG` / `DEBUG_FILES` / `DEBUG_FUNCS` is set | `internal/debug/debug.go:39-42`; `cmd/restic/cmd_backup.go:543`; `cmd/restic/lock.go:19` | **SOUND-UNDER-CONTRACT**, LOW | Contract: nobody diagnoses a problem in the overlapped path by turning the debug log on, because that switches the path off (finding 1 cannot occur with `DEBUG_LOG` set). The same switch is used at `cmd/restic/cmd_check.go:332`, `internal/repository/prune.go:126`, `internal/repository/index/index_decode_eager.go:18`, `internal/repository/index_load_ordinary_stream.go:29` (other reviewers' areas). | Reading. |
| 9 | Bounded two-size buffer pools, one assembly buffer per file | `internal/archiver/buffer.go:3, 17-27, 41-52`; `internal/archiver/file_saver.go:51-52, 268-279` | **SOUND-UNDER-CONTRACT** | Correct for any worker count. Contract is about cost: the pool keeps 2 buffers (`buffer.go:3`), so with `--read-concurrency` above 2 the other workers allocate a fresh 64 KiB or 2 MiB buffer per file. Stock's `sync.Pool` scaled with the workers. | `file_saver_witness_test.go` (4 workers) passes. |
| 10 | Local backend "additional load slot" flag set from the options | `internal/global/global.go:546-549` | **SOUND-UNDER-CONTRACT** | Operator ruling 4 (higher local worker count allowed). An explicit `-o local.connections=N` clears the flag (`global.go:547`). Consumers are in `internal/backend/sema` (other area). | Reading. |
| 11 | `TreeNodeReader.PreparationSize`, a framing hint for the next node | `internal/data/tree_preparation_size.go:9-59` | **SOUND-UNDER-CONTRACT** | Contract: callers use it only to size a buffer; `Next` stays the authority. String and escape skipping is correct by reading. Consumers are the restorer and walker (other area). | Reading. |
| 12 | `fs.Mkdir` helper | `internal/fs/file.go:16-20` | **SOUND-UNDER-CONTRACT** | Thin wrapper of `os.Mkdir`; used only by the restorer (other area). | Reading. |
| 13 | Exact and segmented chunk scanners replacing `chunker.BaseChunker` | `internal/repository/chunker_exact.go:44-121`; `internal/repository/chunker_segments.go:14-97`; `internal/repository/chunker.go:14-20, 38-41, 51-53` | **SOUND** | Every cut equals stock's for the same bytes and polynomial, for any way the input is split into calls, and after `Reset` mid-chunk. Degrees outside 8..53 fall back to stock's chunker (`chunker_exact.go:48-50`). | `chunker_witness_test.go`: **passes**. 2,742 runs over 55 inputs (random, zeros, 0xFF, periods 1..4096, low entropy, zero runs, sizes around 64 / 1024 / 512 KiB) x 3 polynomials x up to 9 feed patterns x 2 scanners; plus 394 natural cuts on 3 x 192 MiB; checked against `BaseChunker` and the streaming `chunker.New`. |
| 14 | File saver: copy-on-upload instead of handing the pooled buffer to the uploader | `internal/archiver/file_saver.go:83-104, 201-368` (dispatch `:332-339`, join `:209-212, 358-359`); `internal/archiver/file_saver_hash.go:70-88` | **SOUND** | Uploaders receive an exact-size private copy; the assembly buffer is reused only after the chunk was consumed synchronously. Chunk list, sizes and errors equal stock's. | `file_saver_witness_test.go`: **passes** (168 files, 811 MiB, 721 chunks; short reads, data+EOF, wrong stat sizes; cancel, read error and upload error finish without deadlock). Race detector: N1. |
| 15 | Hash computed in the archiver; known blobs settled without their bytes | `internal/archiver/file_saver.go:20-23, 58-65, 332-339`; `internal/archiver/file_saver_hash.go:70-88`; `internal/archiver/parent_pair_hash.go:58-67`; `internal/repository/repository.go:979-991, 1400-1429`; `internal/repository/known_completion_lane.go:28-80` | **SOUND** | "Known" means present in the loaded index, pending blobs excluded (`repository.go:984-986`), so a blob still being uploaded goes through the ordinary path. A precomputed ID is always the hash of the bytes handed over; for unknown blobs the save-time verify still re-hashes. Zero chunk ID equals the hash of 512 KiB of zeros. Depends on the index being current (finding 1). | Same witness: every ID matched its bytes; no blob was reported known unless indexed. |
| 16 | One reusable 1 MiB slot that hashes and submits a chunk off the reader goroutine | `internal/archiver/file_saver_hash.go:10-68` | **SOUND** | The slot is taken before the copy and returned only after both the submission and the callback (`:49-65`), as stock returns its buffer in the callback. | Same witness (chunks of 256 KiB to 1 MiB take this path). |
| 17 | Streaming SHA-256 of an unguided chunk while the next tile is read | `internal/archiver/ordinary_hash_pipeline.go:14-128` | **SOUND** | The hash goroutine reads only bytes below the current length; the reader appends above it and joins before any reallocation (`:113-115`) and before returning (`:79-89`). | Same witness (chunks above 1 MiB without a parent). |
| 18 | Parent-guided cut prediction: two chunkers validate two predicted chunks in parallel, with a shared pair-hash lane; long pairs | `internal/archiver/parent_cut_reader.go:40-259`; `internal/archiver/parent_cut_long.go:16-80`; `internal/archiver/parent_pair_hash.go:13-53`; `internal/archiver/file_saver.go:386-391`; `internal/archiver/archiver.go:596` | **SOUND** | Parent blob sizes are hints only. A predicted chunk is emitted only if a freshly reset chunker cuts exactly there; otherwise the bytes are rescanned in place. Change detection is untouched (`archiver.go:543` is stock's code); `--force` gives no parent. | Same witness: 58 files took this path with parents that were identical, edited (insert, delete, overwrite, append, truncate, prepend, one byte), unrelated, or described by a lying index. |
| 19 | Parent tree plaintext reuse: retention budget, finder without a coroutine, builder that compares against the parent, parent ID passed when bytes are equal | `internal/archiver/archiver.go:285-305, 320-383, 685-783, 866-893, 937-941`; `internal/archiver/tree_saver.go:48-72, 85-98, 148-170`; `internal/archiver/parent_tree_reuse.go:11-46`; `internal/archiver/parent_finder.go:7-14`; `internal/data/tree.go:245-360` | **SOUND** | The builder returns exactly the bytes the plain builder returns, for any reference, and never writes to the reference. The parent's ID is used only after `bytes.Equal` over the whole tree (`tree_saver.go:154`). | `node_tree_witness_test.go`: `TestWitnessTreeBuilderReferenceEqualsPlain` **passes** (400 trees x 9 reference shapes). |
| 20 | Hand-written JSON serializer for nodes without special characters or attributes | `internal/data/node_json_scalar.go:21-127`; `internal/data/node.go:172-174`; `internal/data/tree.go:306-325` | **SOUND** | Byte-identical to stock's reflection path where it applies; everything else (non-ASCII, quotes, `<>&`, xattrs, generic attributes, odd time zones, years outside 0..9999, more than 4096 chunks) falls back to stock's encoder. | `TestWitnessNodeJSONEqualsStock` **passes**: 300,000 random nodes, 30,981 on the scalar path. |
| 21 | Backup progress: separate lock for scan totals; delayed progress counter for the early index load | `internal/ui/backup/progress.go:35-37, 43, 68-73, 98, 116, 154-169`; `cmd/restic/backup_index_load.go:77-160` | **SOUND** | Lock order is always scan lock then archive lock; the updater's start condition equals stock's; the counter is attached at stock's reporting point. | Reading; `go test ./internal/ui/backup` passes. |

### 3. Details for the findings

**3.1 Finding 1, stale index after a confirmed lock.** Stock's order in `runBackup` is: lock (create, wait 200 ms, check), find the parent snapshot, load the index. The candidate passes a callback into the lock code (`cmd_backup.go:544-556`) that starts `startBackupIndexLoadAfterSnapshots` right after the lock file is written (`lock_file.go:140-141`), and after the check succeeds it only waits for that load (`cmd_backup.go:598-599`). Nothing re-lists the index after confirmation.

restic's lock protocol guarantees that no exclusive holder is active *after* the check. It does not guarantee that none was active *during* the wait: a holder that was confirmed before this process's lock file became visible, and that unlocks before this process's check, is invisible. Stock does not care, because it has read nothing yet.

For this to happen three things must line up. The backup's lock file must become visible later than the peer's own 200 ms wait plus the peer's whole run (a retried lock upload with back-off, or an fsync stalled behind the peer's own writes on a slow disk). The backup must then read the index quickly (local repository, or index files already in the local cache). And the peer must delete and unlock inside the backup's 200 ms wait. That is rare, and I cannot put a number on it. The outcome is not benign: the backup exits 0 and its snapshot references blobs that no longer exist. `forget` without `--prune` in the same window leaves a stale snapshot listing; by reading, the parent search then fails loading the removed snapshot, an error rather than silent loss (not tested).

The witness drives the candidate's real `LockRepoWithWaitWork` and `LoadIndex` on an in-memory backend whose only modification is that one lock-file save is held back. Output:

```
after the lock was confirmed: index loaded now (stock order) knows blob X = false ; index kept from the settle wait (candidate order) knows blob X = true
backup saved blob X: reported known = true ; reading X back from the repository afterwards: err = id 61405c2b… not found in repository
```

The same pattern (`startWaitWork`) is used by other commands through `cmd/restic/wait_index_load.go`; there a stale index gives read errors rather than silent loss. That file is in the `readside` area.

**Independent confirmation and why the verdict is UNSOUND, not SUSPECT.** The readside reviewer reached the same finding separately and drove it through the real commands (`witness-readside/cmd_lock_wait_stale_witness_test.go`, REVIEW-readside.md M1). The same test file runs in both trees: stock passes all three scenarios, the candidate fails all three. In the backup scenario the candidate uploads nothing, reports success, and `check` then reports trees not found in the repository. In the other two, a prune and a restore fail where stock succeeds. Both witnesses force the schedule by holding back one lock-file write, so the flaw in the logic is demonstrated twice; what remains unknown is how often separate real processes hit it. I first labelled this SUSPECT for that reason and changed it after the second reproduction: the open question is frequency, not whether the code is wrong.

**3.2 Finding 2, output queue.** `runWithoutStatus` runs whenever stdout is not an interactive terminal (cron, pipes, files, `--quiet`). `flushPending` (`status.go:423-489`) writes the queue head and returns false when a write makes no progress (`:471-479`); `queueOutput` then appends behind it (`:529-532`, `:541`). Stock (`trees/stock/internal/ui/termstatus/status.go`, same function) writes each message once and reports a failed write to stderr. So in the candidate a cron job whose stdout log sits on a full disk shows no restic error at all on stderr, only repeated `write failed` lines, and holds every verbose line in memory until exit.

**3.3 The 100 µs batching itself** (regular-file stdout and stderr only, `status.go:20, 122-126, 160-167`) keeps the order of stdout and stderr records and flushes on `Flush`, on shutdown and after 100 µs idle. Read as correct. It exists because the judge redirects output to files.

## 4. NEEDS-RUN: experiments this review could not run

`$CAND` and `$STOCK` are the two source trees on the Linux box, `W` is `review/witness-backup`. The witness files go into the package named in their header.

```bash
# N1  race detector over the write path, start-up and output (decides races in rows 13-19, 21, 2, 4)
cp $W/file_saver_witness_test.go $CAND/internal/archiver/
cp $W/chunker_witness_test.go $W/lock_dwell_stale_index_witness_test.go $CAND/internal/repository/
cp $W/node_tree_witness_test.go $CAND/internal/data/ ; cp $W/open_key_witness_test.go $CAND/internal/global/
cp $W/kdf_witness_test.go $CAND/internal/repository/crypto/
cd $CAND && go test -race -count=1 ./internal/archiver ./internal/data ./internal/global ./internal/ui/backup ./internal/repository/crypto
cd $CAND && go test -race -count=1 -run 'Witness|Chunker|KnownCompletion|Lock|Uploader|SaveBlob' ./internal/repository
# expected: only the four witnesses that are designed to fail (lock dwell, tree leniency, open key, KDF N) fail; no DATA RACE

# N2  Linux-only fs code (rows 6 and 7); once as a normal user, once as root
cp $W/path_stat_linux_witness_test.go $CAND/internal/fs/
cd $CAND && go test -count=1 -v -run 'TestWitnessLinux|PathStat|FileOpen|TestFSLocal' ./internal/fs

# N3  stock vs candidate on special files: same repository bytes, compare tree IDs, listings and exit codes
F=/tmp/fx; mkdir -p $F && cd $F
truncate -s 5G sparse5g; head -c 5G /dev/urandom > big5g; head -c 64M /dev/urandom > mid; ln mid hard1; ln -s mid sym
ln -s "$(printf 'bad\377target')" symraw; mkfifo fifo; : > empty; touch "$(printf 'name\377\376')"
mkdir -p "$(printf 'long/%0200d/%0200d/%0200d' 1 2 3)"; setfattr -n user.k -v v mid; setfacl -m u:nobody:r mid
sudo mknod chr c 1 3; sudo mknod blk b 7 0; install -m 000 /dev/null unreadable
export RESTIC_PASSWORD=x; restic-stock -r /tmp/r0 init; cp -a /tmp/r0 /tmp/r-stock; cp -a /tmp/r0 /tmp/r-cand
for b in stock cand; do restic-$b -r /tmp/r-$b backup --host h --time '2026-01-01 00:00:00' $F; echo "$b exit $?"; done   # both must exit 3
for b in stock cand; do restic-stock -r /tmp/r-$b cat snapshot latest | jq -r .tree; restic-stock -r /tmp/r-$b ls --json latest | jq -c 'select(.struct_type=="node")' | sha256sum; done   # must be equal
# then change the data and repeat the two backup lines and the compare, once per variant:
#   printf x | dd of=big5g bs=1 seek=3000000000 conv=notrunc ; head -c 1M /dev/urandom >> big5g ; sed -i '1s/^/y/' mid
#   and with: --force ; --ignore-ctime ; --ignore-inode ; --read-concurrency 8
restic-stock -r /tmp/r-cand check --read-data

# N4  finding 1 with real processes. A uniform slow link does NOT reproduce it (the early index read is then
#     slow as well and lands after the prune). Delay only the backup's lock upload, as one retried request would:
cat > /tmp/lockdelay.go <<'GO'
package main
import ("math/rand"; "net/http"; "net/http/httputil"; "net/url"; "strings"; "time")
func main() {
	u, _ := url.Parse("http://127.0.0.1:8000")
	p := httputil.NewSingleHostReverseProxy(u)
	http.ListenAndServe("127.0.0.1:8001", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/locks/") { time.Sleep(time.Duration(300+rand.Intn(2700)) * time.Millisecond) }
		p.ServeHTTP(w, r)
	}))
}
GO
export RESTIC_PASSWORD=x; restic-cand -r /srv/r init
rest-server --path /srv/r --no-auth --listen 127.0.0.1:8000 & go run /tmp/lockdelay.go &
for i in $(seq 5000); do
  head -c 8M /dev/urandom > /tmp/g; restic-cand -r /srv/r backup /tmp/g -q && restic-cand -r /srv/r forget latest -q   # leaves unreferenced blobs
  ( sleep 0.$((RANDOM % 9)); restic-cand -r /srv/r prune -q ) &
  restic-cand -r rest:http://127.0.0.1:8001/ backup /tmp/g -q; b=$?; wait
  [ $b -eq 0 ] && ! restic-stock -r /srv/r check --read-data -q && { echo "REPRODUCED at $i"; break; }
done
# control: the same loop with restic-stock in all three roles must never print REPRODUCED

# N5  Ctrl-C and backend failure in the middle of a backup (deadlock, leak, partial snapshot)
for i in $(seq 200); do timeout -s INT 0.$((RANDOM % 9 + 1))$((RANDOM % 9)) timeout 120 restic-cand -r /tmp/r-cand backup $F; echo $?; done | sort | uniq -c   # no 124 (hang)
restic-stock -r /tmp/r-cand check --read-data && restic-stock -r /tmp/r-cand snapshots
#   backend failure: run the same loop against rest:http://127.0.0.1:8000/ while a second shell kills and restarts rest-server every 2 s;
#   expect non-zero exits, never a hang (124), no snapshot from a failed run, and a clean check --read-data afterwards

# N6  the candidate's own terminal tests (currently do not build; three fail after a one-line fix, section 5)
cd $CAND && go test -count=1 ./internal/ui/termstatus

# N7  other platforms (only cross-compiled here: linux 386/arm/mips64le, darwin/arm64, freebsd, openbsd, netbsd, solaris, aix, windows/386 all build)
go test -count=1 ./internal/fs ./internal/archiver ./internal/data ./internal/global     # macOS and FreeBSD
GOARCH=386 go test -count=1 -run 'Witness|Chunker' ./internal/repository ./internal/archiver   # 32-bit int/uint widths
#   Windows, elevated shell: go test ./internal/fs ./internal/archiver ; restic backup --use-fs-snapshot C:\Users (stock vs candidate, compare ls --json)
```

On this (non-elevated) Windows machine `go test ./internal/archiver ./internal/fs` fails the same tests in stock and in the candidate (symlink privilege, VSS, security descriptors); `./internal/data`, `./internal/global`, `./internal/ui/backup`, `./internal/debug` pass in the candidate.

## 5. Test-file changes

- **`internal/archiver/buffer_test.go`**: 44 lines added, none removed. One import (`sync`) and two new tests, `TestBufferPoolBoundedRetention` and `TestBufferPoolConcurrentReleaseDoesNotBlock`, which pin the new 2-buffer channel pool. Stock's `TestBufferPoolReuse` and `TestBufferPoolLargeBuffers` are untouched. No assertion weakened.
- **`internal/ui/termstatus/status_test.go`**: 280 lines added, none removed. Two imports (`sync`, `time`) and 10 new tests for batching and retry. No stock assertion was edited. **But the file no longer compiles**: line 207 calls `term.Flush()` on the `ui.Terminal` interface, which has no `Flush` (`go vet`: `term.Flush undefined`, same for a linux target). So from the candidate tree none of the package's tests can run, stock's eleven included (9 in this file, 2 in `stdio_wrapper_test.go`). They pass for the judge only because the judge overlays stock's test files. In effect stock's termstatus tests are switched off in this tree.
- With that line patched in the scratch copy (`term.(*terminal).Flush()`), three of the candidate's own tests fail against the candidate's own code: `TestRunWithoutStatusKeepsCustomWriterCallsSeparate` (`status_test.go`), `TestRunWithoutStatusKeepsSuffixAfterZeroProgressRetry` (`status_test.go`) and `TestOutputRetryKeepsSuffixAheadOfLaterStdoutAndStderr` (`output_failure_order_test.go`). The tests and the implementation come from different revisions of the retry logic. Stock's own tests pass.
- Not in my two files but in my brief: `internal/repository/crypto/kdf_test.go` gained `TestParallelScryptKeyMatchesXCrypto` and `TestKDFRejectsInvalidScryptParams` (additions only). The second tests N = 1, r = 0, p = 0 and misses the even non-power-of-two N of finding 5.

## 6. Things that should not ship

- `internal/repository/repository.go.orig` (1,998 lines): a patch backup file left in the tree.
- `internal/ui/termstatus/status_test.go` in its current state, and the three failing tests above.
- Test-only counters in production code: `accepted`, `replayed`, `scheduled`, `capacity` in `internal/archiver/parent_cut_reader.go:37, 129, 146, 156-162, 168` and `parent_cut_long.go:36, 54, 64-76` are written on every guided read and read only by tests.
- Test files named after league batches (`*_b01_test.go`, `*_b06_*`, `*_b17_*`, `*_b18_*`, `*_b23_test.go`), 11 of them in `internal/archiver`, `internal/data`, `internal/global` alone.
- Three files are not gofmt-clean: `internal/archiver/file_saver.go`, `internal/ui/backup/progress.go`, `internal/ui/termstatus/status.go` (stock: none).
- The debug switches of row 8.
- Constants fitted to the judge (4 CPUs, 2 readers, many short processes, output redirected to files). None is wrong; each is a fixed number where stock scaled or had none:
  - `maxPooledBuffers = 2` (`internal/archiver/buffer.go:3`); pool sizes 2 MiB and 64 KiB (`file_saver.go:51-52`).
  - One async hash slot and one pair-hash lane for all readers (`file_saver.go:61-63`); thresholds 256 KiB / 1 MiB (`file_saver_hash.go:10-11`); guided pairs capped at 2 MiB (`parent_cut_reader.go:84`). On a 1 or 2 CPU machine a failed prediction costs a second scan of those bytes for no gain.
  - scrypt `workers = min(4, p, GOMAXPROCS)`, stride 4 (`parallel_scrypt.go:17-18`).
  - Output batching 100 µs / 64 KiB, regular files only (`status.go:20, 126, 160-167`).
  - Parent tree retention 1 MiB (`parent_tree_reuse.go:11`); known-completion queue 16 (`internal/repository/known_completion_lane.go:14`).
  - A forced `runtime.GC()` before every index load to lower the measured peak (`internal/repository/repository.go:1096`, storage area).

## 7. Witness files

All in `review/witness-backup/`; each header says what it shows and where it runs. Copies that were run are under `review/scratch-backup/tree`.

| File | Package to copy into | Result here |
| --- | --- | --- |
| `chunker_witness_test.go` | `internal/repository` | passes |
| `file_saver_witness_test.go` | `internal/archiver` | passes |
| `node_tree_witness_test.go` | `internal/data` | 3 pass; `TestWitnessTreeIteratorLeniency` fails by design (finding 3) |
| `kdf_witness_test.go` | `internal/repository/crypto` | parity passes; `TestWitnessKDFNonPowerOfTwoN` fails by design (finding 5) |
| `lock_dwell_stale_index_witness_test.go` | `internal/repository` | fails by design (finding 1) |
| `status_witness_test.go` | `internal/ui/termstatus` (needs the one-line patch of section 5) | both fail by design (finding 2) |
| `open_key_witness_test.go` | `internal/global` | fails by design (finding 4) |
| `path_stat_linux_witness_test.go` | `internal/fs` | Linux only; compile-checked, not run |
