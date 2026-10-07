#!/usr/bin/env python3
"""C8. Restore option matrix against stock's result, and the one-connection paths of repair R2.

One stock-written repository with two snapshots of a source tree that has small and large files, compressible text,
hard links, symlinks, a FIFO, sparse data, extended attributes, odd names and modes; between the snapshots files are
edited, added, removed and change type. Every case is run by stock and by the tree into its own copy of the same
starting directory; exit code, normalised output and the complete manifest of the target (content hashes, sizes,
modes, owners, mtimes, xattrs, link counts, hard-link sharing) must be equal.

Starting directories: empty; an older restore (snapshot 1); the same snapshot already restored; that with local
modifications (edited, truncated, newer, deleted, extra files); files of the wrong type in the way (directory where a
file belongs and the reverse, symlinks to a sentinel file that must not be written through); a FIFO in the way of a
file (60 s limit: stock waits forever for a reader there).
Options: --verify, --sparse, --include/--exclude (also case-insensitive), --include-xattr/--exclude-xattr, --dry-run
-vv, a target that does not exist, snapshot:subfolder, each --overwrite mode with and without --delete.
Routes (three representative cases each): GOMAXPROCS 1 and 64, -o local.connections=1 and =2, through the REST
server with default, 1 and 2 connections, and --limit-download through REST.

Backend failures during restore (REST): downloads cut part way, connections dropped, HTTP 500, a 4 s stall; both
binaries must end with the same exit code and the same restored tree. The same restore, and forget --prune, over the
SFTP backend (sftp-server on a pipe, no network) with 5, 1 and 2 connections.
R2: `repair packs <3 packs>` with -o local.connections=1 must finish (120 s limit; the unrepaired tree is run too,
it is expected to hang); `forget --prune --max-unused 0` and `copy` through REST with 1 and 2 connections; stock
`check --read-data` afterwards."""
import json
import os
import re
import subprocess
import sys
import threading
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import (QUICK, Check, Server, append, blur_recent_mtimes, chkrest_bin, copy_repo, diff_manifests, envint,  # noqa: E402
                 gen_corpus, index_packs, manifest, must, mutate_corpus, normalize_output, prepare, prepare_before,
                 prepare_stock, restic, rmtree, write_file)

c = Check('c08', 'restore option matrix against stock; repair packs / prune / copy with one connection (R2)')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
chkrest_bin()
WORKERS = envint('CHK_C08_WORKERS', 3)
ws = c.work / 'ws'
src = c.work / 'src'
gen_corpus(src, seed=8, small=250, text_files=3, text_mb=3, big=(7, 4), special=True)
repo = c.work / 'repo'
must(restic(stock.bin, repo, 'init', work=ws))
must(restic(stock.bin, repo, 'backup', '--no-cache', '--host', 'h', '--time', '2026-02-01 00:00:00', src, work=ws))
mutate_corpus(src, seed=81)
must(restic(stock.bin, repo, 'backup', '--no-cache', '--host', 'h', '--time', '2026-02-02 00:00:00', src, work=ws))
snaps = sorted(json.loads(must(restic(stock.bin, repo, 'snapshots', '--json', '--no-cache', work=ws)).out), key=lambda s: s['time'])
SNAP1, SNAP2 = snaps[0]['id'], snaps[1]['id']
S = str(src)

# ---- starting directories
pre = {'empty': None}
pre['older'] = c.work / 'pre-older'
must(restic(stock.bin, repo, 'restore', SNAP1, '--no-cache', '--target', pre['older'], work=ws))
pre['same'] = c.work / 'pre-same'
must(restic(stock.bin, repo, 'restore', SNAP2, '--no-cache', '--target', pre['same'], work=ws))
write_file(pre['same'] / 'sentinel', b'must never change\n')
pre['modified'] = c.work / 'pre-modified'
subprocess.check_call(['cp', '-a', str(pre['same']), str(pre['modified'])])
root = Path(str(pre['modified']) + S)
files = sorted((root / 'small').rglob('*.dat'))
for i, path in enumerate(files[:60]):
    st = path.stat()
    kind = i % 6
    if kind == 0 and st.st_size > 10:
        with open(path, 'r+b') as f:                      # same size, same mtime, other content
            f.seek(st.st_size // 2)
            f.write(b'\xaa\xbb')
        os.utime(path, ns=(st.st_atime_ns, st.st_mtime_ns))
    elif kind == 1:
        with open(path, 'ab') as f:                       # longer and newer
            f.write(b'appended')
        os.utime(path, ns=(2_000_000_000 * 10**9, 2_000_000_000 * 10**9))
    elif kind == 2:
        with open(path, 'r+b') as f:                      # shorter, older
            f.truncate(st.st_size // 2)
        os.utime(path, ns=(1_000_000_000 * 10**9, 1_000_000_000 * 10**9))
    elif kind == 3:
        path.unlink()
    elif kind == 4:
        os.chmod(path, 0o600)
        os.chown(path, 4242, 4242)
    else:
        os.utime(path, ns=(2_000_000_000 * 10**9, 2_000_000_000 * 10**9))   # only newer
write_file(root / 'small' / 'extra-not-in-snapshot.dat', b'extra')
write_file(root / 'extra-dir' / 'deep' / 'file', b'extra tree')
with open(root / 'big' / 'b1.bin', 'r+b') as f:
    f.seek(1_000_000)
    f.write(b'damaged block')
pre['wrongtype'] = c.work / 'pre-wrongtype'
subprocess.check_call(['cp', '-a', str(pre['same']), str(pre['wrongtype'])])
root = Path(str(pre['wrongtype']) + S)
victims = sorted((root / 'small' / 'd03').glob('*.dat'))
victims[0].unlink()
victims[0].mkdir()                                        # directory where a file belongs
write_file(victims[0] / 'inside', b'in the way')
victims[1].unlink()
os.symlink(os.path.relpath(pre['wrongtype'] / 'sentinel', victims[1].parent), victims[1])   # symlink to the sentinel where a file belongs
subprocess.check_call(['rm', '-rf', str(root / 'small' / 'd05')])
write_file(root / 'small' / 'd05', b'file where a directory belongs')
subprocess.check_call(['rm', '-rf', str(root / 'small' / 'd06')])
os.symlink(os.path.relpath(pre['wrongtype'], root / 'small'), root / 'small' / 'd06')  # symlink (to the top of the target) where a directory belongs
(root / 'special' / 'linked-a').unlink()
write_file(root / 'special' / 'linked-a', b'no longer the same inode')
# A FIFO where a regular file belongs gets its own starting directory and a 60 s limit: stock restore opens what is
# in the way for writing, and opening a FIFO for writing waits for a reader that never comes.
pre['fifo'] = c.work / 'pre-fifo'
subprocess.check_call(['cp', '-a', str(pre['same']), str(pre['fifo'])])
victim = sorted((Path(str(pre['fifo']) + S) / 'small' / 'd04').glob('*.dat'))[0]
victim.unlink()
os.mkfifo(victim)

BASE = [
    ('empty', []), ('empty', ['--verify']), ('empty', ['--sparse']),
    ('empty', ['--include', S + '/small/d01', '--include', '*.txt']), ('empty', ['--exclude', '*.bin', '--exclude', S + '/special']),
    ('empty', ['--iinclude', '*.TXT']), ('empty', ['--iexclude', '*.DAT']),
    ('empty', ['--exclude-xattr', 'user.chk.*']), ('empty', ['--include-xattr', 'user.chk.one']),
    ('empty', ['--dry-run', '-vv']), ('empty', ['--delete']), ('empty', ['--overwrite', 'never']),
    ('missing', []), ('subfolder', []),
    ('same', []), ('same', ['--overwrite', 'if-changed']), ('same', ['--verify']), ('same', ['--overwrite', 'if-changed', '-vv']),
]
for mode in ('always', 'if-changed', 'if-newer', 'never'):
    BASE += [('older', ['--overwrite', mode]), ('older', ['--overwrite', mode, '--delete']),
             ('modified', ['--overwrite', mode]), ('modified', ['--overwrite', mode, '--delete', '--verify']),
             ('wrongtype', ['--overwrite', mode]), ('wrongtype', ['--overwrite', mode, '--delete'])]
BASE += [('fifo', ['--overwrite', 'always']), ('fifo', ['--overwrite', 'if-changed']), ('fifo', ['--overwrite', 'never']), ('fifo', ['--overwrite', 'always', '--delete'])]
BASE += [('older', ['--delete', '--dry-run', '-vv']), ('modified', ['--overwrite', 'if-changed', '--dry-run', '-vv']), ('older', ['--sparse', '--delete'])]
ROUTES = [('GOMAXPROCS=1', 'local', {'GOMAXPROCS': '1'}, []), ('GOMAXPROCS=64', 'local', {'GOMAXPROCS': '64'}, []),
          ('local.connections=1', 'local', {}, ['-o', 'local.connections=1']), ('local.connections=2', 'local', {}, ['-o', 'local.connections=2']),
          ('local.connections=1 GOMAXPROCS=64', 'local', {'GOMAXPROCS': '64'}, ['-o', 'local.connections=1']),
          ('rest default', 'rest', {}, []), ('rest.connections=1', 'rest', {}, ['-o', 'rest.connections=1']),
          ('rest.connections=2', 'rest', {}, ['-o', 'rest.connections=2'])]
ROUTED = [('empty', []), ('older', ['--overwrite', 'if-changed', '--delete']), ('modified', ['--overwrite', 'always', '--verify'])]
cases = [(p, a, 'default', 'local', {}, []) for p, a in BASE]
for p, a in ROUTED:
    for name, backend, env, options in ROUTES:
        cases.append((p, a, name, backend, env, options))
cases.append(('empty', ['--include', S + '/small', '--limit-download', '2048'], 'rest default', 'rest', {}, []))
cases.append(('empty', ['--include', S + '/small', '--limit-download', '2048'], 'rest.connections=1', 'rest', {}, ['-o', 'rest.connections=1']))
if QUICK:
    cases = cases[::9]
DURATION = re.compile(r' in \d+:\d+(:\d+)?| \(took [^)]*\)')
server = Server(repo)
lock = threading.Lock()
position = [0]
rows = []


def run_case(index, case, wdir):
    start, args, route, backend, env, options = case
    name = 'start=%s | %s | %s' % (start, ' '.join(args) or '(no option)', route)
    sparse = '--sparse' in args
    got = {}
    for arm, binary in (('stock', stock.bin), ('tree', cand.bin)):
        target = wdir / ('t-' + arm)
        rmtree(target)
        if start in ('empty',):
            target.mkdir()
        elif start == 'missing':
            target = target / 'does' / 'not' / 'exist'
        elif start == 'subfolder':
            target.mkdir()
        else:
            subprocess.check_call(['cp', '-a', str(pre[start]), str(target)])
        snapshot = SNAP2 + (':' + S + '/small' if start == 'subfolder' else '')
        where = repo if backend == 'local' else server.url
        r = restic(binary, where, *options, 'restore', snapshot, '--no-cache', '--target', target, *args, work=wdir, env=env,
                   timeout=60 if start == 'fifo' else 600)
        top = wdir / ('t-' + arm)
        entries = blur_recent_mtimes(manifest(top, blocks=sparse), int(c.started * 1e9)) if top.exists() else {}
        blocks = {k: v.pop('blocks') for k, v in entries.items() if 'blocks' in v}
        hide = [(str(target), '<target>'), (str(top), '<target>'), (str(where), '<repo>')]
        text = [DURATION.sub('', line) for line in normalize_output(r.out + '\n' + r.err, hide)]
        got[arm] = (r, entries, blocks, text)
    problems = []
    (rs, ms, bs, ts), (rt, mt, bt, tt) = got['stock'], got['tree']
    if rs.rc == -9 or rt.rc == -9:
        # a hang (killed at the limit) leaves a half restored target: nothing more to compare
        for arm in got:
            rmtree(wdir / ('t-' + arm))
        row = {'case': name, 'rc': rs.rc, 'entries': len(ms), 'problems': [], 'hang': {'stock': rs.rc == -9, 'tree': rt.rc == -9}}
        if rt.rc == -9 and rs.rc != -9:
            row['problems'] = ['the tree hangs (limit reached), stock exits %d' % rs.rc]
            row['command'] = rt.cmd
        return row
    if rs.rc != rt.rc:
        problems.append('exit code stock %d, tree %d' % (rs.rc, rt.rc))
    diff = diff_manifests(ms, mt)
    if diff:
        problems.append('targets differ (%d): %s' % (len(diff), '; '.join(diff[:3])))
    if sorted(ts) != sorted(tt):
        only_s = [x for x in ts if x not in tt][:4]
        only_t = [x for x in tt if x not in ts][:4]
        problems.append('output differs; only stock: %r; only tree: %r' % (only_s, only_t))
    if sparse:
        worse = [k for k in bs if k in bt and ms[k].get('size', 0) > 4096 * 16 and bt[k] > bs[k] * 1.1 + 256]
        if worse:
            problems.append('--sparse: the tree allocates more blocks than stock for %d files, e.g. %s: %d vs %d' % (len(worse), worse[0], bt[worse[0]], bs[worse[0]]))
    row = {'case': name, 'rc': rs.rc, 'entries': len(ms), 'problems': problems}
    if problems:
        ev = c.res / 'evidence' / ('case%03d' % index)
        ev.mkdir(parents=True, exist_ok=True)
        for arm in got:
            r = got[arm][0]
            (ev / (arm + '.txt')).write_text('%s\nenv=%s\nrc=%d\n%s\n%s' % (r.cmd, env, r.rc, r.out, r.err))
        (ev / 'differences.txt').write_text(name + '\n' + '\n'.join(problems) + '\n\n' + '\n'.join(diff) + '\n')
        row['evidence'] = str(ev)
        row['command'] = got['tree'][0].cmd
    for arm in got:
        rmtree(wdir / ('t-' + arm))
    return row


def worker(n):
    wdir = c.work / ('w%d' % n)
    wdir.mkdir(exist_ok=True)
    while True:
        with lock:
            if position[0] >= len(cases):
                return
            index = position[0]
            position[0] += 1
        try:
            row = run_case(index, cases[index], wdir)
        except Exception as exc:
            row = {'case': str(cases[index][:3]), 'problems': ['harness error: %r' % (exc,)], 'harness_error': True}
        with lock:
            rows.append(row)
            append(c.res / 'cases.jsonl', json.dumps(row))
        if row['problems']:
            c.log('case %s: %s' % (row['case'], ' | '.join(row['problems'])[:700]))
            if row.get('harness_error'):
                c.note('harness error in case %s: %s' % (row['case'], row['problems'][0][:300]))
            else:
                c.fail('restore, %s: %s' % (row['case'], row['problems'][0][:500]), evidence=row.get('evidence', ''),
                       stock='stock is the reference (stock.txt in the evidence folder)', repro=row.get('command', ''))
        c.tick()


c.status('restore cases', 0, len(cases) + 8)
c.log('%d restore cases, each by stock and by the tree' % len(cases))
threads = [threading.Thread(target=worker, args=(i,)) for i in range(WORKERS)]
for t in threads:
    t.start()
for t in threads:
    t.join()
server.stop()
hangs = [r for r in rows if r.get('hang')]
both = [r['case'] for r in hangs if r['hang']['stock'] and r['hang']['tree']]
only_stock = [r['case'] for r in hangs if r['hang']['stock'] and not r['hang']['tree']]
if both:
    c.note('stock and the tree both hang in %d cases (not ours): a FIFO is in the way of a regular file and restore opens it for writing: %s' % (len(both), '; '.join(both)))
if only_stock:
    c.note('stock hangs and the tree does not in %d cases: %s' % (len(only_stock), '; '.join(only_stock)))
c.numbers['restore_cases_where_both_hang'] = len(both)
# A restore that was killed at its limit (the FIFO cases) leaves its lock file behind. The steps below work on
# copies of this repository and need exclusive locks: no lock file may be left (2026-10-07: with the stale lock every
# binary answered `repair packs` with exit 11, "repository is already locked", and the step said nothing).
stale = list((repo / 'locks').glob('*'))
for path in stale:
    path.unlink()
c.log('lock files left behind by the restore cases and removed: %d' % len(stale))
c.numbers['restore_cases'] = len(rows)
c.numbers['restore_cases_with_differences'] = sum(1 for r in rows if r['problems'] and not r.get('harness_error'))

# ---------------------------------------------------------------- restore while the backend fails (REST)
c.status('restore with backend failures')
since = int(c.started * 1e9)
fault_rows = []
for mode, count in (('half', 3), ('drop', 2), ('500', 3), ('stall', 1)):
    for after in ((2, 7) if not QUICK else (2,)):
        got = {}
        for arm, binary in (('stock', stock.bin), ('tree', cand.bin)):
            target = c.work / ('f-' + arm)
            rmtree(target)
            srv = Server(repo, fail_after=after, fail_count=count, fail_mode=mode, fail_method='GET', fail_kind='data', stall_ms=4000)
            r = restic(binary, srv.url, 'restore', SNAP2, '--no-cache', '--target', target, work=ws, timeout=900)
            stats = srv.stop()
            got[arm] = (r, blur_recent_mtimes(manifest(target), since) if target.exists() else {}, stats.get('injected_failures', 0))
            rmtree(target)
        (rs, ms, ns), (rt, mt, nt) = got['stock'], got['tree']
        diff = diff_manifests(ms, mt)
        row = {'failure': '%s x%d from data download %d' % (mode, count, after), 'rc': {'stock': rs.rc, 'tree': rt.rc},
               'injected': {'stock': ns, 'tree': nt}, 'seconds': {'stock': round(rs.secs, 1), 'tree': round(rt.secs, 1)}, 'differences': len(diff)}
        fault_rows.append(row)
        c.log('restore with backend failures: %s' % json.dumps(row))
        if rs.rc != rt.rc or diff or rt.rc == -9:
            ev = c.res / 'evidence' / ('fault-%s-%d' % (mode, after))
            ev.mkdir(parents=True, exist_ok=True)
            (ev / 'stock.txt').write_text('%s\nrc=%d\n%s\n%s' % (rs.cmd, rs.rc, rs.out, rs.err))
            (ev / 'tree.txt').write_text('%s\nrc=%d\n%s\n%s' % (rt.cmd, rt.rc, rt.out, rt.err))
            (ev / 'differences.txt').write_text('\n'.join(diff) + '\n')
            c.fail('restore through REST with %s: tree rc %d, stock rc %d, %d differences in the restored tree' % (row['failure'], rt.rc, rs.rc, len(diff)),
                   evidence=ev, stock='stock rc %d' % rs.rc, repro='chkrest -fail-mode %s -fail-after %d -fail-count %d -fail-method GET -fail-kind data; restic -r rest:... restore' % (mode, after, count))
        if not nt:
            c.note('restore with %s: no failure was injected for the tree (it made fewer than %d data downloads)' % (row['failure'], after))
c.numbers['restore_with_backend_failures'] = fault_rows
c.tick()

# ---------------------------------------------------------------- the same paths over SFTP (no network: sftp-server on a pipe)
SFTP = '/usr/lib/openssh/sftp-server'
sftp_rows = {}
if not os.path.exists(SFTP):
    c.note('no %s on this box: the SFTP backend was not exercised' % SFTP)
else:
    c.status('sftp backend')
    sftp = ['-o', 'sftp.command=' + SFTP]
    for connections in (5, 1, 2):
        got = {}
        for arm, binary in (('stock', stock.bin), ('tree', cand.bin)):
            target = c.work / ('s-' + arm)
            rmtree(target)
            r = restic(binary, 'sftp:localhost:' + str(repo), *sftp, '-o', 'sftp.connections=%d' % connections, 'restore', SNAP2, '--no-cache', '--target', target, work=ws, timeout=900)
            got[arm] = (r, blur_recent_mtimes(manifest(target), since) if target.exists() else {})
            rmtree(target)
        diff = diff_manifests(got['stock'][1], got['tree'][1])
        sftp_rows['restore sftp.connections=%d' % connections] = {'rc': {a: got[a][0].rc for a in got}, 'differences': len(diff)}
        if got['stock'][0].rc != got['tree'][0].rc or diff:
            c.fail('restore over SFTP with sftp.connections=%d: tree rc %d, stock rc %d, %d differences' % (connections, got['tree'][0].rc, got['stock'][0].rc, len(diff)),
                   evidence=c.res / 'summary.json', stock='stock rc %d' % got['stock'][0].rc,
                   repro='restic -r sftp:localhost:REPO -o sftp.command=%s -o sftp.connections=%d restore <snapshot> --target T' % (SFTP, connections))
    for connections in (1, 2):
        got = {}
        for arm, binary in (('stock', stock.bin), ('tree', cand.bin)):
            copy = c.work / ('sp-' + arm)
            copy_repo(repo, copy)
            r = restic(binary, 'sftp:localhost:' + str(copy), *sftp, '-o', 'sftp.connections=%d' % connections, 'forget', '--keep-last', '1', '--prune',
                       '--max-unused', '0', '--no-cache', work=ws, timeout=600)
            check = restic(stock.bin, copy, 'check', '--read-data', '--no-cache', work=ws)
            got[arm] = {'rc': r.rc, 'seconds': round(r.secs, 1), 'stock_check_rc': check.rc}
            rmtree(copy)
        sftp_rows['prune sftp.connections=%d' % connections] = got
        if got['tree']['rc'] != got['stock']['rc'] or got['tree']['stock_check_rc'] != got['stock']['stock_check_rc'] or got['tree']['rc'] == -9:
            c.fail('forget --prune over SFTP with sftp.connections=%d: tree %s, stock %s' % (connections, json.dumps(got['tree']), json.dumps(got['stock'])),
                   evidence=c.res / 'summary.json', stock=json.dumps(got['stock']), repro='restic -r sftp:localhost:REPO -o sftp.command=%s -o sftp.connections=%d forget --keep-last 1 --prune --max-unused 0' % (SFTP, connections))
    c.log('SFTP backend: %s' % json.dumps(sftp_rows))
c.numbers['sftp'] = sftp_rows
c.tick()

# ---------------------------------------------------------------- R2: one-connection paths
c.status('R2 one connection')
packs = index_packs(stock.bin, repo, ws)
# the three largest data packs: with --pack-size 4 the salvaged blobs of each fill several new packs, which is what
# made the unrepaired tree wait for its only connection
sizes = {p: sum(b['length'] for b in blobs) for p, blobs in packs.items() if all(b['type'] == 'data' for b in blobs)}
data_packs = sorted(sizes, key=lambda p: -sizes[p])[:3]
c.numbers['repair_packs_pack_bytes'] = [sizes[p] for p in data_packs]
r2 = {}
arms = [('stock', stock.bin), ('tree', cand.bin)]
try:
    arms.append(('before', prepare_before().bin))
except Exception as exc:
    c.note('the unrepaired tree could not be built for the R2 before/after: %r' % (exc,))
for arm, binary in arms:
    copy = c.work / ('r2-' + arm)
    copy_repo(repo, copy)
    cwd = c.work / ('r2-cwd-' + arm)       # repair packs writes backup copies of the packs into the current directory
    cwd.mkdir()
    r = restic(binary, copy, '-o', 'local.connections=1', '--pack-size', '4', 'repair', 'packs', *data_packs, '--no-cache', work=ws, timeout=120, cwd=cwd)
    check = restic(stock.bin, copy, 'check', '--read-data', '--no-cache', work=ws) if r.rc != -9 else None
    r2[arm] = {'rc': r.rc, 'seconds': round(r.secs, 1), 'hang': r.rc == -9, 'stock_check_rc': check.rc if check else None}
    (c.res / ('repair-packs-conn1-%s.txt' % arm)).write_text('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err))
    c.log('repair packs with local.connections=1, %s: %s' % (arm, json.dumps(r2[arm])))
    c.tick()
c.numbers['repair_packs_one_connection'] = r2
if r2['stock']['rc'] != 0:
    c.fail('harness: stock `repair packs` exits %d on the test repository, the R2 step says nothing' % r2['stock']['rc'],
           evidence=c.res / 'repair-packs-conn1-stock.txt', stock='rc %d' % r2['stock']['rc'], repro='see repair-packs-conn1-stock.txt')
if r2['tree']['hang'] or r2['tree']['rc'] != r2['stock']['rc'] or r2['tree']['stock_check_rc'] != 0:
    c.fail('repair packs with -o local.connections=1: tree %s, stock %s' % (json.dumps(r2['tree']), json.dumps(r2['stock'])),
           evidence=c.res / 'repair-packs-conn1-tree.txt', stock='stock: rc %s in %s s' % (r2['stock']['rc'], r2['stock']['seconds']),
           repro='restic -r REPO -o local.connections=1 --pack-size 4 repair packs %s' % ' '.join(p[:8] for p in data_packs))
remote = {}
for connections in (1, 2):
    for arm, binary in (('stock', stock.bin), ('tree', cand.bin)):
        copy = c.work / ('prune-%s-%d' % (arm, connections))
        copy_repo(repo, copy)
        with Server(copy) as srv:
            r = restic(binary, srv.url, '-o', 'rest.connections=%d' % connections, 'forget', '--keep-last', '1', '--prune',
                       '--max-unused', '0', '--no-cache', work=ws, timeout=600)
        check = restic(stock.bin, copy, 'check', '--read-data', '--no-cache', work=ws)
        remote['prune rest.connections=%d %s' % (connections, arm)] = {'rc': r.rc, 'seconds': round(r.secs, 1), 'stock_check_rc': check.rc}
        dst = c.work / ('copy-%s-%d' % (arm, connections))
        rmtree(dst)
        with Server(dst) as srv:
            env = {'RESTIC_FROM_PASSWORD': 'chk'}
            i = restic(binary, srv.url, 'init', '--from-repo', repo, '--copy-chunker-params', work=ws, env=env)
            r = restic(binary, srv.url, '-o', 'rest.connections=%d' % connections, 'copy', '--from-repo', repo, '--no-cache', work=ws, env=env, timeout=900)
        check = restic(stock.bin, dst, 'check', '--read-data', '--no-cache', work=ws)
        remote['copy rest.connections=%d %s' % (connections, arm)] = {'init_rc': i.rc, 'rc': r.rc, 'seconds': round(r.secs, 1), 'stock_check_rc': check.rc}
    for op in ('prune', 'copy'):
        a, b = remote['%s rest.connections=%d stock' % (op, connections)], remote['%s rest.connections=%d tree' % (op, connections)]
        c.log('%s through REST with %d connection(s): stock %s, tree %s' % (op, connections, json.dumps(a), json.dumps(b)))
        if b['rc'] != a['rc'] or b['stock_check_rc'] != a['stock_check_rc'] or b['rc'] == -9:
            c.fail('%s through REST with rest.connections=%d: tree %s, stock %s' % (op, connections, json.dumps(b), json.dumps(a)),
                   evidence=c.res / 'summary.json', stock=json.dumps(a), repro='restic -r rest:... -o rest.connections=%d %s' % (connections, op))
    c.tick()
c.numbers['remote_one_two_connections'] = remote
before = r2.get('before', {})
summary = '%d restore cases by both binaries: %d differ; repair packs with one connection: tree rc %s in %s s (unrepaired tree: %s), stock rc %s; prune and copy through REST with 1 and 2 connections: %s' % (
    len(rows), c.numbers['restore_cases_with_differences'], r2['tree']['rc'], r2['tree']['seconds'],
    'hangs' if before.get('hang') else 'rc %s' % before.get('rc'), r2['stock']['rc'],
    'equal to stock' if not any('through REST' in f['what'] for f in c.fails) else 'DIFFER')
sys.exit(c.finish(summary))
