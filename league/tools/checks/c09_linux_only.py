#!/usr/bin/env python3
"""C9. The Linux-only code and the experiments that need a real Linux process.

1. internal/fs (path_stat_linux.go, file_open_linux.go) with the backup reviewer's Linux witness: the package tests
   plus the witness as root, the same test binary as uid 65534, and GOARCH=386.
   Also on 386: the lock-wait (R1) tests of cmd/restic and the tests of internal/repository/index, each from a
   copy without the one test file that does not compile for 32 bit (a stock file; the league's file of F1).
2. The three wait-work tests of cmd/restic that could not run on Windows.
3. R6, output that cannot be written: stdout on /dev/full (ENOSPC on every write), stdout closed, stderr on
   /dev/full, both; for ls, ls --json, snapshots, check, backup -v, cat config, dump and a failing command. Exit code
   and what still arrives on the other stream must equal stock; no hang, no memory growth (peak RSS against stock).
4. R7, a tree blob whose node name holds a raw double quote or a raw newline (written with chktool; restic itself
   never writes one): check, ls, ls --json, restore and find by stock and by the tree must agree. The unrepaired
   tree is run too (it is expected to accept what stock rejects).
5. R9, a wrong RESTIC_PASSWORD against a repository with real scrypt parameters: time, exit code and message
   against stock (the unrepaired tree searched the keys twice)."""
import json
import os
import re
import shutil
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
import gotest  # noqa: E402
from chk import (CHECKS, NOBODY, QUICK, Check, blur_recent_mtimes, chktool_bin, copy_repo, copy_tree, diff_manifests,  # noqa: E402
                 go_env, manifest, must, normalize_output, prepare, prepare_before, prepare_stock, restic, restic_env,
                 rmtree, run, run_rusage, short_tmp, write_file)

c = Check('c09', 'Linux-only code: fs witness (root, uid 65534, 386), wait-work tests, R6 on /dev/full, R7 raw quote')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
before = None
try:
    before = prepare_before()
except Exception as exc:
    c.note('the unrepaired tree could not be built: %r' % (exc,))
ws = c.work / 'ws'
DURATION = re.compile(r' in \d+:\d+(:\d+)?| \(took [^)]*\)')
c.status('fs witness', 0, 6)

# ---------------------------------------------------------------- 1. internal/fs
src = c.work / 'src'
copy_tree(c.tree, src)
shutil.copyfile(CHECKS / 'witness' / 'backup' / 'path_stat_linux_witness_test.go', src / 'internal' / 'fs' / 'zzw_backup_path_stat_linux_witness_test.go')
parts = {}
out = c.res / 'fs-root.json'
gotest.go_test(src, ['./internal/fs/...'], out, tmp=short_tmp(c.label, 'c09', 'fsroot'), extra=['-v'])
parsed = gotest.parse(out)
parts['fs as root'] = gotest.counts(parsed)
fails = gotest.failures(parsed)
witness = {t: a for p in parsed.values() for t, a in p['tests'].items() if t.startswith('TestWitnessLinux') and '/' not in t}
parts['fs witness as root'] = witness
for package, test, kind in fails:
    ev = c.res / ('fail-fs-root-%s.txt' % gotest.safe(test or 'PACKAGE'))
    ev.write_text(gotest.test_text(parsed, package, test, limit=60000))
    c.fail('internal/fs as root: %s [%s]' % (test or '(package)', kind), evidence=ev, stock='see C1 for the stock run of this package',
           repro='cd <tree copy with the witness> && go test -count=1 -v -run "^%s$" ./internal/fs' % (test or '.'))
if not witness:
    c.fail('the Linux fs witness did not run (compile problem?)', evidence=str(out) + '.stderr', stock='the witness has no stock counterpart')
# the same tests as an unprivileged user: build the test binary as root, run it as uid 65534 in the package directory
# (owned by that user in the scratch copy, as it would be for a developer); stock's fs tests the same way as control


def fs_as_nobody(name, tree_src):
    binary = c.work / ('fs-%s.test' % name)
    rc, o, e, _ = run(['go', 'test', '-c', '-tags', gotest.TAGS, '-o', str(binary), './internal/fs'], env=go_env(), cwd=tree_src, timeout=1800)
    if rc != 0:
        return None, e
    tmp = short_tmp(c.label, 'c09', 'fsnobody', name)
    subprocess.check_call(['chown', '-R', '%d:%d' % (NOBODY, NOBODY), str(tmp), str(tree_src / 'internal' / 'fs')])
    env = {'PATH': '/usr/bin:/bin', 'HOME': str(tmp), 'TMPDIR': str(tmp), 'TZ': 'UTC', 'RESTIC_TEST_FUSE': '0'}
    rc, o, e, secs = run([str(binary), '-test.v', '-test.count=1'], env=env, cwd=tree_src / 'internal' / 'fs', timeout=1800, user=NOBODY)
    (c.res / ('fs-nobody-%s.txt' % name)).write_text(o + e)
    failed = sorted({line.split()[2] for line in o.splitlines() if line.startswith('--- FAIL')})
    return {'rc': rc, 'passed': sum(1 for line in o.splitlines() if line.startswith('--- PASS')), 'failed': failed,
            'skipped': sum(1 for line in o.splitlines() if line.startswith('--- SKIP'))}, ''


stock_src = c.work / 'stock-src'
copy_tree(stock.tree, stock_src)
tree_run, err_text = fs_as_nobody('tree', src)
stock_run, _ = fs_as_nobody('stock', stock_src)
if tree_run is None:
    c.fail('internal/fs test binary does not build: ' + err_text[-400:], evidence=c.res / 'progress.log', stock='n/a')
else:
    parts['fs as uid 65534'] = tree_run
    parts['fs as uid 65534, stock'] = stock_run
    for test in tree_run['failed']:
        if stock_run and test in stock_run['failed']:
            c.note('internal/fs as uid 65534: %s fails on stock too (not ours)' % test)
            continue
        c.fail('internal/fs as uid 65534: %s' % test, evidence=c.res / 'fs-nobody-tree.txt', stock='stock as uid 65534: passes or does not have the test',
               repro='go test -c ./internal/fs && (as an unprivileged user, in internal/fs) ./fs.test -test.v -test.run "^%s$"' % test)
    if tree_run['rc'] != 0 and not tree_run['failed']:
        c.fail('internal/fs test binary as uid 65534 exits %d' % tree_run['rc'], evidence=c.res / 'fs-nobody-tree.txt', stock='stock rc %s' % (stock_run or {}).get('rc'))
c.tick()
out = c.res / 'fs-386.json'
gotest.go_test(src, ['./internal/fs/...'], out, goarch='386', tmp=short_tmp(c.label, 'c09', 'fs386'), extra=['-v'])
parsed = gotest.parse(out)
parts['fs GOARCH=386'] = gotest.counts(parsed)
for package, test, kind in gotest.failures(parsed):
    ev = c.res / ('fail-fs-386-%s.txt' % gotest.safe(test or 'PACKAGE'))
    ev.write_text(gotest.test_text(parsed, package, test, limit=60000))
    c.fail('internal/fs GOARCH=386: %s [%s]' % (test or '(package)', kind), evidence=ev, stock='see C1 (386 run of the touched packages on stock)',
           repro='GOARCH=386 go test -count=1 -run "^%s$" ./internal/fs' % (test or '.'))
c.tick()

# ---------------------------------------------------------------- 1b. 32 bit, around the test files that do not compile
# cmd/restic: one STOCK test file does not compile for 386 (in stock as well: a test seed that does not fit an
# int), so the R1 tests of the lock wait cannot run there as the tree stands. internal/repository/index: one test
# file of the league does not compile (FAILURES.md F1). Both packages are run here on 386 from a scratch copy in
# which the seed is shortened and the league's file is left out, so that the rest of their tests is not lost.
src32 = c.work / 'src32'
copy_tree(c.tree, src32)
LEAVE_OUT = {'./cmd/restic': ('cmd_repair_snapshots_integration_test.go', 'TestLockWait|TestWitnessLock|Wait|Prepared|Confirmation', ('1234567890123', '123456789')),
             './internal/repository/index': ('pack_membership_test.go', None, None)}
around = {}
for package, (name, flt, swap) in LEAVE_OUT.items():
    rc, o, e, _ = run(['go', 'vet', '-tags', gotest.TAGS, package], env=go_env(goarch='386'), cwd=src32, timeout=1800)
    removed = False
    path = src32 / package / name
    if rc != 0 and name in (o + e) and path.is_file():
        if swap:
            path.write_text(path.read_text().replace(swap[0], swap[1]))
        else:
            path.unlink()
        removed = True
    out = c.res / ('386-%s.json' % gotest.safe(package))
    gotest.go_test(src32, [package], out, goarch='386', run_filter=flt, tmp=short_tmp(c.label, 'c09', '386', package), timeout_min=40)
    parsed = gotest.parse(out)
    counts = gotest.counts(parsed)
    around[package] = {'changed_in_the_copy': (name + (': seed shortened' if swap else ': left out')) if removed else None,
                       'tests': counts['tests'], 'passed': counts['passed'], 'failed': counts['failed'],
                       'packages_failed': counts['packages_failed']}
    c.log('386 %s: %s' % (package, json.dumps(around[package])))
    for pkg, test, kind in gotest.failures(parsed):
        ev = c.res / ('fail-386-%s-%s.txt' % (gotest.safe(package), gotest.safe(test or 'PACKAGE')))
        ev.write_text(gotest.test_text(parsed, pkg, test, limit=60000))
        c.fail('GOARCH=386 %s: %s [%s]%s' % (package, test or '(package)', kind, (' (scratch copy: %s)' % around[package]['changed_in_the_copy']) if removed else ''), evidence=ev,
               stock='stock on 386: see C1; cmd/restic tests cannot run there on stock either',
               repro='GOARCH=386 go test -count=1 -run "^%s$" %s' % (test or '.', package))
parts['386 around the test files that do not compile'] = around
c.tick()

# ---------------------------------------------------------------- 2. the three wait-work tests
WAIT = ['TestRestoreFailedLockConfirmationDrainsPreparedIndexBeforeUnlock', 'TestRestoreSnapshotLookupErrorDrainsPreparedIndexBeforeUnlock',
        'TestLsFailedLockConfirmationDiscardsPreparedSnapshotBeforeUnlock']
c.status('wait-work tests')
out = c.res / 'wait-work.json'
gotest.go_test(src, ['./cmd/restic'], out, run_filter='^(%s)$' % '|'.join(WAIT), tmp=short_tmp(c.label, 'c09', 'wait'), extra=['-v'])
parsed = gotest.parse(out)
state = {t: 'not in this tree' for t in WAIT}
for p in parsed.values():
    for t, a in p['tests'].items():
        if t in state:
            state[t] = a
parts['wait-work tests'] = state
for t, a in state.items():
    if a == 'fail':
        ev = c.res / ('fail-wait-%s.txt' % t)
        ev.write_text(''.join(gotest.test_text(parsed, p, t) for p in parsed))
        c.fail('cmd/restic %s fails' % t, evidence=ev, stock='the test is the league\'s own; stock does not have it',
               repro='go test -count=1 -v -run "^%s$" ./cmd/restic' % t)
    elif a != 'pass':
        c.note('wait-work test %s: %s' % (t, a))
c.tick()

# ---------------------------------------------------------------- 3. R6: output that cannot be written
c.status('R6 /dev/full')
r6src = c.work / 'r6src'
for i in range(3000):
    write_file(r6src / ('d%02d' % (i % 30)) / ('file-with-a-reasonably-long-name-%05d.txt' % i), b'x' * (i % 50))
write_file(r6src / 'one.bin', os.urandom(300_000))
r6 = c.work / 'r6repo'
must(restic(stock.bin, r6, 'init', work=ws))
must(restic(stock.bin, r6, 'backup', '--no-cache', '--host', 'h', r6src, work=ws))
COMMANDS = {
    'ls latest': ['ls', 'latest', '--no-cache'], 'ls --json latest': ['ls', '--json', 'latest', '--no-cache'],
    'ls -l latest': ['ls', '-l', 'latest', '--no-cache'], 'snapshots': ['snapshots', '--no-cache'],
    'snapshots --json': ['snapshots', '--json', '--no-cache'], 'check': ['check', '--no-cache'],
    'backup -v': ['backup', '-v', '--no-cache', '--host', 'h', str(r6src)], 'backup --json': ['backup', '--json', '--no-cache', '--host', 'h', str(r6src)],
    'cat config': ['cat', 'config', '--no-cache'], 'dump latest file': ['dump', 'latest', str(r6src / 'one.bin'), '--no-cache'],
    'find': ['find', '*-0001*', '--no-cache'], 'stats': ['stats', '--no-cache'],
    'ls of a missing snapshot': ['ls', 'ffffffff', '--no-cache'], 'version': ['version'],
}
STREAMS = {'stdout on /dev/full': 'exec "$0" "$@" > /dev/full', 'stdout closed': 'exec "$0" "$@" >&-',
           'stderr on /dev/full': 'exec "$0" "$@" 2> /dev/full', 'both on /dev/full': 'exec "$0" "$@" > /dev/full 2>&1',
           'stderr closed': 'exec "$0" "$@" 2>&-'}
SUMMARY_LINE = {'backup -v': 'snapshot ', 'backup --json': '"message_type":"summary"'}
# The tree under test may carry its own version string (`restic version`, and the program version in the snapshots
# it writes). That is a declared difference from stock: the version token is masked before `restic version` is compared.
VERSION = re.compile(r'^restic \S+( \(compiled manually\))?')
versions = {}
for arm, binary in (('stock', stock.bin), ('tree', cand.bin)):
    rc, o, e, _ = run([str(binary), 'version'])
    versions[arm] = o.strip()
parts['restic version'] = versions
c.log('restic version: %s' % json.dumps(versions))
if versions.get('stock') != versions.get('tree'):
    c.note('`restic version` differs from stock by design and is not compared: stock %r, tree %r' % (versions.get('stock'), versions.get('tree')))
if QUICK:
    COMMANDS = {k: COMMANDS[k] for k in ('ls latest', 'backup -v', 'ls of a missing snapshot')}
r6_rows = []
arms = [('stock', stock.bin), ('tree', cand.bin)] + ([('before', before.bin)] if before else [])
for cname, args in COMMANDS.items():
    for sname, shell in STREAMS.items():
        got = {}
        for arm, binary in arms:
            copy = c.work / ('r6-' + arm)
            copy_repo(r6, copy)
            res = run_rusage(['/bin/sh', '-c', shell, str(binary), '-r', str(copy)] + args, env=restic_env(ws), timeout=180)
            hide = [(str(copy), '<repo>')]
            got[arm] = {'rc': res['rc'], 'seconds': round(res['seconds'], 2), 'rss_mib': res['rss_kib'] // 1024,
                        'stdout': [VERSION.sub('restic <version>', DURATION.sub('', line)) for line in normalize_output(res['out'], hide)],
                        'stderr': normalize_output(res['err'], hide)}
        s, t = got['stock'], got['tree']
        problems = []
        if s['rc'] != t['rc']:
            problems.append('exit code stock %s, tree %s' % (s['rc'], t['rc']))
        # `backup --json` writes one status message per progress tick: how many there are depends on timing, and with
        # stdout on /dev/full each one is one "write failed" line on stderr (stock printed 1 line in one run and 2 in
        # three others). For that command the DISTINCT stderr lines are compared, for all others the exact lines.
        same_stderr = (sorted(set(s['stderr'])) == sorted(set(t['stderr']))) if cname == 'backup --json' else (s['stderr'] == t['stderr'])
        if 'stderr' not in sname and 'both' not in sname and not same_stderr:
            problems.append('stderr differs: stock %r, tree %r' % (s['stderr'][:3], t['stderr'][:3]))
        if sname.startswith('stderr'):
            marker = SUMMARY_LINE.get(cname)
            if marker:        # ids, times and the number of status lines differ from run to run: the final line must be there
                has = [any(marker in line for line in got[arm]['stdout']) for arm in ('stock', 'tree')]
                if has[0] != has[1]:
                    problems.append('stdout: final line (%s) printed by stock: %s, by the tree: %s' % (marker, has[0], has[1]))
            elif s['stdout'] != t['stdout']:
                problems.append('stdout differs (%d / %d lines): only stock %r; only tree %r' % (
                    len(s['stdout']), len(t['stdout']), [x for x in s['stdout'] if x not in t['stdout']][:2], [x for x in t['stdout'] if x not in s['stdout']][:2]))
        if t['rc'] == -9:
            problems.append('the tree hangs (180 s)')
        if t['rss_mib'] > s['rss_mib'] * 2 + 64:
            problems.append('peak RSS stock %d MiB, tree %d MiB' % (s['rss_mib'], t['rss_mib']))
        row = {'command': cname, 'streams': sname, 'problems': problems,
               **{arm: {k: (v if k not in ('stdout', 'stderr') else v[:5]) for k, v in got[arm].items()} for arm in got}}
        r6_rows.append(row)
        if problems:
            c.fail('R6, `restic %s` with %s: %s' % (cname, sname, '; '.join(problems)[:500]), evidence=c.res / 'r6.json',
                   stock='stock: rc %s, stderr %r' % (s['rc'], s['stderr'][:2]),
                   repro="sh -c '%s' restic -r REPO %s" % (shell, ' '.join(args)))
(c.res / 'r6.json').write_text(json.dumps(r6_rows, indent=1) + '\n')
parts['R6 cases'] = len(r6_rows)
parts['R6 cases that differ from stock'] = sum(1 for r in r6_rows if r['problems'])
if before:
    parts['R6 cases where the unrepaired tree differs from stock'] = sum(
        1 for r in r6_rows if r['before']['rc'] != r['stock']['rc'] or ('stderr' not in r['streams'] and 'both' not in r['streams'] and r['before']['stderr'] != r['stock']['stderr']))
c.log('R6: %d cases, %d differ from stock' % (len(r6_rows), parts['R6 cases that differ from stock']))
c.tick()

# ---------------------------------------------------------------- 4. R7: raw quote / raw newline in a node name
c.status('R7 raw quote')
tool = chktool_bin()
r7src = c.work / 'r7src'
write_file(r7src / 'aaa', b'first file\n')
write_file(r7src / 'bbb', b'second file\n')
write_file(r7src / 'zzz', b'last file\n')
r7 = c.work / 'r7repo'
must(restic(stock.bin, r7, 'init', work=ws))
must(restic(stock.bin, r7, 'backup', '--no-cache', '.', work=ws, cwd=r7src))
snap = sorted(json.loads(must(restic(stock.bin, r7, 'snapshots', '--json', '--no-cache', work=ws)).out), key=lambda s: s['time'])[-1]
tree = json.loads(must(restic(stock.bin, r7, 'cat', 'blob', snap['tree'], '--no-cache', work=ws)).out)
NAMES = {'raw double quote': 'zzz"quote', 'raw newline': 'zzz\nnewline', 'raw quote first': '"zzz', 'raw tab (valid for stock)': 'zzz\ttab',
         'escaped quote (valid)': 'zzz\\"escaped', 'backslash and raw quote': 'zzz\\\\"mixed', 'control byte 0x01': 'zzz\x01ctl'}
r7_rows = {}


STACK_FRAME = re.compile(r'^\t\S.*\.go:\d+')
ADDRESS = re.compile(r'0x[0-9a-f]{6,}')


def drop_frames(lines):
    """Remove the frames of an error's stack trace (function line + tab-indented file:line)."""
    out = []
    for line in lines:
        if STACK_FRAME.match(line):
            if out:
                out.pop()
            continue
        out.append(line)
    return out


def without_trace(text):
    """What the user is told, without what differs for reasons of code layout: the goroutine dump under a panic,
    the stack frames restic appends to some errors (also inside --json messages), pointer values."""
    text = ADDRESS.sub('0xADDR', text.split('\ngoroutine ')[0])
    out = []
    for line in drop_frames(text.split('\n')):
        if line.startswith('{') and '"message"' in line:
            try:
                doc = json.loads(line)
                doc['message'] = '\n'.join(drop_frames(doc['message'].split('\n')))
                line = json.dumps(doc, sort_keys=True)
            except (ValueError, KeyError, AttributeError):
                pass
        out.append(line)
    return '\n'.join(out)


for label, name in NAMES.items():
    doc = json.loads(json.dumps(tree))
    doc['nodes'][-1]['name'] = name
    blob = c.work / 'r7-tree.json'
    blob.write_text(json.dumps(doc, separators=(',', ':')) + '\n', encoding='utf-8')
    repo = c.work / 'r7-case'
    copy_repo(r7, repo)
    saved = must(restic(tool, repo, 'chk-savetree', '--file', blob, '--no-cache', work=ws), 'chktool chk-savetree')
    sid = [line.split()[1] for line in saved.out.splitlines() if line.startswith('snapshot ')][0]
    got = {}
    raw = {}
    for arm, binary in arms:
        copy = c.work / ('r7-' + arm)
        hide = [(str(copy), '<repo>')]
        res = {}
        for op, args in (('check', ['check', '--no-cache']), ('ls', ['ls', sid, '--no-cache']), ('ls --json', ['ls', '--json', sid, '--no-cache']),
                         ('find', ['find', 'zzz*', '--snapshot', sid, '--no-cache']), ('diff', ['diff', snap['id'], sid, '--no-cache'])):
            copy_repo(repo, copy)          # a fresh copy per command: a command that crashes leaves its lock behind
            r = restic(binary, copy, *args, work=ws)
            raw['%s %s' % (arm, op)] = '%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err)
            res[op] = (r.rc, normalize_output(without_trace(r.out + '\n' + r.err), hide))
        target = c.work / ('r7-restore-' + arm)
        rmtree(target)
        copy_repo(repo, copy)
        r = restic(binary, copy, 'restore', sid, '--no-cache', '--target', target, work=ws)
        raw['%s restore' % arm] = '%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err)
        entries = blur_recent_mtimes(manifest(target), int(c.started * 1e9)) if target.exists() else {}
        res['restore'] = (r.rc, [DURATION.sub('', line.replace(str(target), '<target>')) for line in normalize_output(without_trace(r.out + '\n' + r.err), hide)], entries)
        rmtree(target)
        got[arm] = res
    ev = c.res / 'r7-outputs' / label.replace(' ', '_').replace('(', '').replace(')', '')
    ev.mkdir(parents=True, exist_ok=True)
    for key, text in raw.items():
        (ev / (key.replace(' ', '_') + '.txt')).write_text(text)
    row = {'stock': {op: v[0] for op, v in got['stock'].items()}, 'tree': {op: v[0] for op, v in got['tree'].items()}, 'problems': []}
    if before:
        row['before'] = {op: v[0] for op, v in got['before'].items()}
    for op in got['stock']:
        a, b = got['stock'][op], got['tree'][op]
        if a[0] != b[0]:
            row['problems'].append('%s: exit code stock %d, tree %d' % (op, a[0], b[0]))
        elif a[1] != b[1]:
            only_stock, only_tree = [x for x in a[1] if x not in b[1]], [x for x in b[1] if x not in a[1]]
            panics = ([x for x in a[1] if x.startswith('panic:')], [x for x in b[1] if x.startswith('panic:')])
            if panics[0] and panics[0] == panics[1]:
                # both crash with the same panic (a stock defect); what was printed before the crash is a race in both
                row.setdefault('notes', []).append('%s: stock and the tree both crash with `%s` (exit %d); lines printed before the crash differ: only stock %r, only tree %r' % (
                    op, panics[0][0], a[0], only_stock[:2], only_tree[:2]))
            else:
                row['problems'].append('%s: output differs; only stock %r; only tree %r' % (op, only_stock[:2], only_tree[:2]))
        if op == 'restore' and diff_manifests(a[2], b[2]):
            row['problems'].append('restore: restored files differ: %s' % '; '.join(diff_manifests(a[2], b[2])[:2]))
    r7_rows[label] = row
    c.log('R7 name with %s: stock rc %s, tree rc %s%s: %s' % (label, row['stock'], row['tree'], (', unrepaired rc %s' % row['before']) if before else '',
                                                                'agree' if not row['problems'] else ' | '.join(row['problems'])[:500]))
    for text in row.get('notes', []):
        c.note('R7, name with %s: %s' % (label, text))
    if row['problems']:
        c.fail('R7, node name with %s: the tree and stock disagree: %s' % (label, row['problems'][0][:500]), evidence=c.res / 'r7.json',
               stock='stock exit codes %s' % row['stock'], repro='chktool -r REPO chk-savetree --file <tree with that name>; restic -r REPO check / ls / restore')
(c.res / 'r7.json').write_text(json.dumps(r7_rows, indent=1) + '\n')
parts['R7 names'] = len(r7_rows)
parts['R7 names where the tree differs from stock'] = sum(1 for r in r7_rows.values() if r['problems'])
if before:
    parts['R7 names where the unrepaired tree differs from stock (exit codes)'] = sum(1 for r in r7_rows.values() if r['before'] != r['stock'])
c.tick()
# ---------------------------------------------------------------- 5. R9: a wrong password costs one key search
c.status('R9 wrong password')
r9repo = c.work / 'r9repo'
must(restic(stock.bin, r9repo, 'init', work=ws))          # real scrypt parameters (calibrated by init)
r9 = {}
for arm, binary in arms:
    times, codes, texts = [], set(), set()
    for i in range(5):
        r = restic(binary, r9repo, 'cat', 'config', '--no-cache', work=ws, env={'RESTIC_PASSWORD': 'not the password'})
        times.append(r.secs)
        codes.add(r.rc)
        texts.add(r.err.strip().splitlines()[-1] if r.err.strip() else '')
    good = [restic(binary, r9repo, 'cat', 'config', '--no-cache', work=ws).secs for i in range(5)]
    r9[arm] = {'wrong_password_seconds_median': round(sorted(times)[2], 3), 'right_password_seconds_median': round(sorted(good)[2], 3),
               'exit_codes': sorted(codes), 'message': sorted(texts)}
c.log('R9 wrong password: %s' % json.dumps(r9))
parts['R9 wrong password'] = r9
s9, t9 = r9['stock'], r9['tree']
if t9['exit_codes'] != s9['exit_codes'] or t9['message'] != s9['message'] or t9['wrong_password_seconds_median'] > s9['wrong_password_seconds_median'] * 1.6 + 0.1:
    c.fail('R9, wrong password: tree %s, stock %s' % (json.dumps(t9), json.dumps(s9)), evidence=c.res / 'parts.json', stock=json.dumps(s9),
           repro='RESTIC_PASSWORD=wrong restic -r REPO cat config   (time and message against stock)')
c.tick()
c.numbers.update(parts)
(c.res / 'parts.json').write_text(json.dumps(parts, indent=1, default=str) + '\n')
summary = 'fs+witness as root: %s pass / %s fail (witness %s); as uid 65534: %s; 386: %s pass / %s fail; wait-work tests: %s; R6 %d cases, %d differ from stock; R7 %d names, %d differ from stock' % (
    parts['fs as root'].get('passed'), parts['fs as root'].get('failed'), witness or 'not run', parts.get('fs as uid 65534'),
    parts['fs GOARCH=386'].get('passed'), parts['fs GOARCH=386'].get('failed'), state, len(r6_rows),
    parts['R6 cases that differ from stock'], len(r7_rows), parts['R7 names where the tree differs from stock'])
sys.exit(c.finish(summary))
