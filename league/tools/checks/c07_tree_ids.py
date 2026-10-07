#!/usr/bin/env python3
"""C7. Tree-ID equality, stock against the tree, for backups of special files; then restore by both binaries.

Fixture (on ext4, as root): a 5 GiB incompressible file, a 5 GiB hole, a sparse file with data islands, a 64 MiB file
with two hard links, an extended attribute and an ACL, symlinks (one with a target that is not UTF-8), a FIFO, a
unix socket, a character and a block device, an empty file, a file name that is not UTF-8, a path of three
200-character directories, a directory with a default ACL, and a file that gains a hard link between two backups.
Both binaries back the same fixture up into two copies of one freshly initialised repository (same keys, same chunker
polynomial), with the same --host and --time, in six steps: plain; after edits (parent used); --force; --ignore-ctime
after a ctime-only change; --ignore-inode after an inode-only change; --read-concurrency 8. After every step the
exit codes, the snapshot tree IDs and the `ls --json` listings (read by stock from both repositories) must be equal.
Then stock `check --read-data` on the tree's repository, and the latest snapshot is restored by both binaries: the
two restored trees must be identical in content, metadata, extended attributes, devices and hard-link sharing.
Unprivileged pass (uid 65534): a fixture with an unreadable file, an unreadable directory and a directory that cannot
be entered; both binaries must exit 3 with the same messages and the same tree ID.
R8: a snapshot in which one inode is recorded with link count 1 on one path and 2 on another (written with chktool)
is restored by both binaries; both must produce one hard-linked file.
32 bit: the tree built with GOARCH=386 backs the same fixture up (tree ID and listing must equal stock's) and restores
the snapshot with the 5 GiB file (must equal the restore by stock)."""
import hashlib
import json
import os
import shutil
import socket
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import (NOBODY, QUICK, Check, chktool_bin, copy_repo, diff_manifests, envint, manifest, must, prepare, prepare_386,  # noqa: E402
                 prepare_stock, restic, restic_env, rmtree, write_file)

c = Check('c07', 'tree IDs stock vs tree on special files, flags, unreadable files; restore by both; R8')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
BIG = envint('CHK_C07_BIG_MIB', 5 * 1024)
if QUICK:
    BIG = 64
TIME = ['--host', 'h', '--time']
fx = c.work / 'fx'
fx.mkdir()


def sh(command):
    subprocess.check_call(command, shell=True)


sh('head -c %dM /dev/urandom > %s/big5g' % (BIG, fx))
sh('truncate -s %dM %s/sparse5g' % (BIG, fx))
with open(fx / 'sparse-islands', 'wb') as f:
    f.truncate(1 << 30)
    for at, size in ((0, 5000), (100 << 20, 12288), (700 << 20, 12288), ((1 << 30) - 4096, 4096)):
        f.seek(at)
        f.write(os.urandom(size))
sh('head -c 64M /dev/urandom > %s/mid' % fx)
os.link(fx / 'mid', fx / 'hard1')
os.symlink('mid', fx / 'sym')
os.symlink(b'bad\xfftarget', os.fsencode(str(fx)) + b'/symraw')
os.mkfifo(fx / 'fifo')
sock = socket.socket(socket.AF_UNIX)
sock.bind(str(fx / 'socket'))
sock.close()
write_file(fx / 'empty', b'')
write_file(os.fsencode(str(fx)) + b'/name\xff\xfe', b'raw name')
write_file(fx / 'long' / ('%0200d' % 1) / ('%0200d' % 2) / ('%0200d' % 3) / 'deep', b'deep')
os.setxattr(fx / 'mid', 'user.k', b'v')
os.setxattr(fx / 'long', 'user.dir', b'on a directory')
devices = True
try:
    os.mknod(fx / 'chr', 0o644 | 0o020000, os.makedev(1, 3))
    os.mknod(fx / 'blk', 0o660 | 0o060000, os.makedev(7, 0))
except OSError as exc:
    devices = False
    c.note('devices could not be created: %r' % (exc,))
acl = subprocess.call('setfacl -m u:nobody:r %s/mid && mkdir %s/acldir && setfacl -d -m u:nobody:rx %s/acldir && echo x > %s/acldir/f' % (fx, fx, fx, fx), shell=True) == 0
if not acl:
    c.note('setfacl failed: ACLs are not part of this run')
write_file(fx / 'linkcount' / 'a', os.urandom(300_000))
write_file(fx / 'setuid', b'#!/bin/true\n')
os.chmod(fx / 'setuid', 0o6755)
write_file(fx / 'owned', b'owner 1234')
os.chown(fx / 'owned', 1234, 4321)
install = fx / 'unreadable'
write_file(install, b'mode 000, readable for root only\n')
os.chmod(install, 0)
for i in range(200):
    write_file(fx / 'many' / ('f%03d' % i), os.urandom(1 + i * 37))

r0 = c.work / 'r0'
must(restic(stock.bin, r0, 'init', work=c.work / 'ws'))
repos = {'stock': c.work / 'r-stock', 'tree': c.work / 'r-tree'}
bins = {'stock': stock.bin, 'tree': cand.bin}
for arm in repos:
    copy_repo(r0, repos[arm])


def listing_digest(repo, work):
    out = must(restic(stock.bin, repo, 'ls', '--json', 'latest', '--no-cache', work=work)).out
    nodes = [line for line in out.splitlines() if '"struct_type":"node"' in line]
    return hashlib.sha256('\n'.join(nodes).encode('utf-8', 'surrogateescape')).hexdigest(), len(nodes)


def tree_id(repo, work):
    return latest_snapshot(repo, work)['tree']


def latest_snapshot(repo, work):
    snaps = json.loads(must(restic(stock.bin, repo, 'snapshots', '--json', '--no-cache', work=work)).out)
    return sorted(snaps, key=lambda s: s['time'])[-1]


def step(name, flags, when, target=None, user=None, repo_set=None, expect_rc=0):
    """One backup by both binaries; compares exit code, tree ID and listing."""
    target = target or fx
    repo_set = repo_set or repos
    got = {}
    for arm in ('stock', 'tree'):
        work = c.work / ('w-' + arm + ('-u' if user else ''))
        restic_env(work)          # creates home/ and tmp/ before the ownership change
        if user:
            subprocess.check_call(['chown', '-R', '%d:%d' % (user, user), str(work)])
        # relative path from the parent: the snapshot then holds the fixture only, not its ancestor directories
        # (the scratch directory above it changes while the check runs)
        r = restic(bins[arm], repo_set[arm], 'backup', '--no-cache', *TIME, when, *flags, target.name, work=work, user=user,
                   timeout=3600, cwd=target.parent)
        got[arm] = r
        (c.res / ('step-%s-%s.txt' % (name, arm))).write_text('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err))
    row = {'step': name, 'flags': flags, 'rc': {a: got[a].rc for a in got}, 'seconds': {a: round(got[a].secs, 1) for a in got}}
    problems = []
    if got['stock'].rc != got['tree'].rc:
        problems.append('exit code stock %d, tree %d' % (got['stock'].rc, got['tree'].rc))
    if got['stock'].rc != expect_rc:
        c.note('step %s: stock exits %d (expected %d)' % (name, got['stock'].rc, expect_rc))
    if got['stock'].rc in (0, 3) and got['tree'].rc in (0, 3):
        ws = c.work / 'ws'
        ids = {a: tree_id(repo_set[a], ws) for a in got}
        lists = {a: listing_digest(repo_set[a], ws) for a in got}
        row['tree_id'] = ids
        row['nodes'] = lists['stock'][1]
        if ids['stock'] != ids['tree']:
            problems.append('tree ID stock %s, tree %s' % (ids['stock'][:12], ids['tree'][:12]))
        if lists['stock'] != lists['tree']:
            problems.append('ls --json differs (%d / %d nodes)' % (lists['stock'][1], lists['tree'][1]))
        if lists['stock'][1] == 0:
            problems.append('harness: no node lines found in ls --json')
    if expect_rc == 3:
        errs = {a: sorted(l for l in got[a].err.splitlines() if l.strip()) for a in got}
        if errs['stock'] != errs['tree']:
            problems.append('error messages differ: stock %r, tree %r' % (errs['stock'][:4], errs['tree'][:4]))
        row['error_lines'] = len(errs['stock'])
    row['problems'] = problems
    steps.append(row)
    c.log('step %s %s: rc %s, %s' % (name, ' '.join(flags), row['rc'], 'equal' if not problems else ' | '.join(problems)))
    if problems:
        c.fail('backup step "%s" (%s): %s' % (name, ' '.join(flags) or 'no flags', '; '.join(problems)[:500]),
               evidence=c.res / ('step-%s-tree.txt' % name), stock='stock is the reference: step-%s-stock.txt' % name,
               repro='python3 c07_tree_ids.py <tree> <label>   (step %s)' % name)
    c.tick()
    return row


steps = []
c.status('backup steps', 0, 10)
step('1-plain', [], '2026-01-01 00:00:00')
# edits between the first and the second backup
os.link(fx / 'linkcount' / 'a', fx / 'linkcount' / 'b')          # the link count of an unchanged file goes from 1 to 2
with open(fx / 'big5g', 'r+b') as f:
    f.seek(min(3_000_000_000, (BIG << 20) // 2))
    f.write(b'x')
with open(fx / 'big5g', 'ab') as f:
    f.write(os.urandom(1 << 20))
data = (fx / 'mid').read_bytes()
with open(fx / 'mid', 'r+b') as f:                                # same inode (hard1 keeps sharing it), new content
    f.write(b'y' + data[:-1])
os.unlink(fx / 'many' / 'f010')
write_file(fx / 'many' / 'new', b'new file')
step('2-edited', [], '2026-01-02 00:00:00')
step('3-force', ['--force'], '2026-01-03 00:00:00')
os.chmod(fx / 'many' / 'f020', 0o600)                             # ctime-only changes
os.chmod(fx / 'many' / 'f020', 0o644)
os.chown(fx / 'many' / 'f021', 0, 0)
step('4-ignore-ctime', ['--ignore-ctime'], '2026-01-04 00:00:00')
for name in ('f030', 'f031'):                                     # new inode, same size and mtime
    shutil.copy2(fx / 'many' / name, fx / 'many' / (name + '.tmp'))
    os.replace(fx / 'many' / (name + '.tmp'), fx / 'many' / name)
step('5-ignore-inode', ['--ignore-inode'], '2026-01-05 00:00:00')
write_file(fx / 'many' / 'f040', os.urandom(5000))
step('6-read-concurrency', ['--read-concurrency', '8'], '2026-01-06 00:00:00')

c.status('stock check --read-data of the tree repository')
r = restic(stock.bin, repos['tree'], 'check', '--read-data', '--no-cache', work=c.work / 'ws', timeout=3600)
(c.res / 'stock-check-of-tree-repository.txt').write_text('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err))
if r.rc != 0:
    c.fail('stock check --read-data rejects the repository the tree wrote: ' + r.brief(400), evidence=c.res / 'stock-check-of-tree-repository.txt',
           stock='the stock-written twin repository is checked the same way below', repro=r.cmd)
r2 = restic(stock.bin, repos['stock'], 'check', '--no-cache', work=c.work / 'ws', timeout=3600)
if r2.rc != 0:
    c.note('stock check of the STOCK-written twin exits %d: %s' % (r2.rc, r2.brief(300)))
c.tick()

# ---- restore the latest snapshot of the tree's repository with both binaries
c.status('restore by both binaries')
targets = {}
for arm in ('stock', 'tree'):
    targets[arm] = c.work / ('restore-' + arm)
    r = restic(bins[arm], repos['tree'], 'restore', 'latest', '--no-cache', '--target', targets[arm], work=c.work / ('w-' + arm), timeout=3600)
    (c.res / ('restore-%s.txt' % arm)).write_text('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err))
    steps.append({'step': 'restore by ' + arm, 'rc': r.rc, 'seconds': round(r.secs, 1)})
m = {arm: manifest(targets[arm] / fx.name) for arm in targets}
diff = diff_manifests(m['stock'], m['tree'])
source = manifest(fx)
diff_source = diff_manifests(source, m['tree'])
(c.res / 'restore-diff-stock-vs-tree.txt').write_text('\n'.join(diff) + '\n')
(c.res / 'restore-diff-source-vs-tree.txt').write_text('\n'.join(diff_source) + '\n')
c.numbers['restore_entries'] = len(m['tree'])
c.numbers['restore_differences_stock_vs_tree'] = len(diff)
c.numbers['restore_differences_source_vs_tree'] = len(diff_source)
if diff or steps[-1]['rc'] != steps[-2]['rc']:
    c.fail('restore of the special-file snapshot: stock and the tree restore different trees (rc %s/%s): %s' % (
        steps[-2]['rc'], steps[-1]['rc'], '; '.join(diff[:4])[:500]), evidence=c.res / 'restore-diff-stock-vs-tree.txt',
        stock='stock is the reference', repro='restic -r <repo> restore latest --target T   (both binaries), then compare')
if diff_source:
    c.note('both binaries: %d differences between the source and the restored tree (same for stock; e.g. %s)' % (len(diff_source), diff_source[0][:200]))
for t in targets.values():
    rmtree(t)
c.tick()

# ---- the tree as a 32-bit binary: same tree ID for the fixture with the 5 GiB file, and a restore above 4 GiB
c.status('32-bit binary')
try:
    bin386 = prepare_386(cand)
except Exception as exc:
    bin386 = None
    c.note('the 386 binary could not be built: %r' % (exc,))
if bin386:
    r386 = c.work / 'r-386'
    copy_repo(r0, r386)
    w386 = c.work / 'w-386'
    r = restic(bin386, r386, 'backup', '--no-cache', *TIME, '2026-01-06 00:00:00', fx.name, work=w386, timeout=3600, cwd=fx.parent)
    (c.res / 'step-386-backup.txt').write_text('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err))
    row386 = {'backup_rc': r.rc, 'backup_seconds': round(r.secs, 1)}
    problems = []
    if r.rc != 0:
        problems.append('386 backup exits %d: %s' % (r.rc, r.brief(300)))
    else:
        ids = (tree_id(repos['stock'], c.work / 'ws'), tree_id(r386, c.work / 'ws'))
        row386['tree_id_equal_to_stock'] = ids[0] == ids[1]
        if ids[0] != ids[1]:
            problems.append('tree ID of the 386 backup %s differs from stock %s' % (ids[1][:12], ids[0][:12]))
        if listing_digest(repos['stock'], c.work / 'ws') != listing_digest(r386, c.work / 'ws'):
            problems.append('ls --json of the 386 backup differs from stock')
    target = c.work / 'restore-386'
    r = restic(bin386, repos['tree'], 'restore', 'latest', '--no-cache', '--target', target, work=w386, timeout=3600)
    (c.res / 'restore-386.txt').write_text('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err))
    row386['restore_rc'] = r.rc
    d386 = diff_manifests(m['stock'], manifest(target / fx.name)) if (target / fx.name).exists() else ['nothing restored']
    row386['restore_differences_from_stock'] = len(d386)
    if r.rc != steps[-2]['rc'] or d386:
        problems.append('386 restore (rc %d) differs from the restore by stock: %s' % (r.rc, '; '.join(d386[:3])))
    rmtree(target)
    rmtree(r386)
    c.numbers['binary_386'] = row386
    c.log('32-bit binary: %s' % json.dumps(row386))
    if problems:
        c.fail('the tree built for 386: %s' % '; '.join(problems)[:600], evidence=c.res / 'step-386-backup.txt', stock='stock amd64 is the reference',
               repro='GOARCH=386 go build ./cmd/restic; ./restic backup <fixture with a 5 GiB file>; ./restic restore latest')
c.tick()

# ---- unprivileged pass: unreadable entries, exit code 3
c.status('unprivileged pass')
fu = c.work / 'fxu'
write_file(fu / 'ok' / 'readable', b'readable\n' * 100)
write_file(fu / 'ok' / 'unreadable', b'secret')
os.chmod(fu / 'ok' / 'unreadable', 0)
write_file(fu / 'closed-dir' / 'inside', b'inside')
os.chmod(fu / 'closed-dir', 0)
write_file(fu / 'no-exec-dir' / 'inside', b'inside')
os.chmod(fu / 'no-exec-dir', 0o644)
write_file(fu / 'z-last', b'last')
urepos = {'stock': c.work / 'u-stock', 'tree': c.work / 'u-tree'}
for arm in urepos:
    copy_repo(r0, urepos[arm])
    subprocess.check_call(['chown', '-R', '%d:%d' % (NOBODY, NOBODY), str(urepos[arm])])
row = step('7-unreadable-as-nobody', [], '2026-01-07 00:00:00', target=fu, user=NOBODY, repo_set=urepos, expect_rc=3)
c.numbers['unreadable_exit_codes'] = row['rc']
os.chmod(fu / 'closed-dir', 0o755)
os.chmod(fu / 'no-exec-dir', 0o755)

# ---- R8: one inode with link count 1 on one path and 2 on another
c.status('R8 mixed link counts')
tool = chktool_bin()
r8 = c.work / 'r8'
copy_repo(r0, r8)
src8 = c.work / 'src8'
write_file(src8 / 'a', os.urandom(100_000))
os.link(src8 / 'a', src8 / 'b')
write_file(src8 / 'c', b'unrelated')
ws = c.work / 'ws'
must(restic(stock.bin, r8, 'backup', '--no-cache', '.', work=ws, cwd=src8))
snap = latest_snapshot(r8, ws)
tree = json.loads(must(restic(stock.bin, r8, 'cat', 'blob', snap['tree'], '--no-cache', work=ws)).out)
r8_rows = {}
for case, patch in (('a=1,b=2', {'a': 1}), ('a=2,b=1', {'b': 1}), ('a=1,b=1', {'a': 1, 'b': 1})):
    doc = json.loads(json.dumps(tree))
    for node in doc['nodes']:
        if node['name'] in patch:
            node['links'] = patch[node['name']]
    blob = c.work / 'r8-tree.json'
    blob.write_text(json.dumps(doc, separators=(',', ':')) + '\n')
    repo = c.work / 'r8-case'
    copy_repo(r8, repo)
    saved = must(restic(tool, repo, 'chk-savetree', '--file', blob, '--no-cache', work=ws), 'chktool chk-savetree')
    sid = [line.split()[1] for line in saved.out.splitlines() if line.startswith('snapshot ')][0]
    shared = {}
    rcs = {}
    for arm in ('stock', 'tree'):
        target = c.work / ('r8-restore-' + arm)
        rmtree(target)
        r = restic(bins[arm], repo, 'restore', sid, '--no-cache', '--target', target, work=ws)
        rcs[arm] = r.rc
        try:
            sa, sb = os.lstat(target / 'a'), os.lstat(target / 'b')
            shared[arm] = {'same_inode': sa.st_ino == sb.st_ino, 'nlink_a': sa.st_nlink, 'nlink_b': sb.st_nlink,
                           'equal_content': (target / 'a').read_bytes() == (target / 'b').read_bytes()}
        except OSError as exc:
            shared[arm] = {'error': repr(exc)}
        rmtree(target)
    r8_rows[case] = {'rc': rcs, 'result': shared}
    c.log('R8 links %s: %s' % (case, json.dumps(r8_rows[case])))
    if rcs['stock'] != rcs['tree'] or shared['stock'] != shared['tree']:
        c.fail('R8, recorded link counts %s: stock restores %s, the tree restores %s' % (case, shared['stock'], shared['tree']),
               evidence=c.res / 'summary.json', stock='stock is the reference',
               repro='chktool -r REPO chk-savetree --file <tree with links %s>; restic -r REPO restore <snapshot> --target T' % case)
c.numbers['r8'] = r8_rows
c.tick()
c.numbers['steps'] = steps
equal = sum(1 for s in steps if 'problems' in s and not s['problems'])
total = sum(1 for s in steps if 'problems' in s)
summary = '%d of %d backup steps with equal exit code, tree ID and listing (%d MiB file; devices %s, ACL %s); unreadable as uid 65534: exit %s/%s; restore stock vs tree: %d differences in %d entries; R8 cases equal: %d of %d' % (
    equal, total, BIG, 'yes' if devices else 'no', 'yes' if acl else 'no', row['rc'].get('stock'), row['rc'].get('tree'),
    len(diff), len(m['tree']), sum(1 for v in r8_rows.values() if v['result']['stock'] == v['result']['tree']), len(r8_rows))
sys.exit(c.finish(summary))
