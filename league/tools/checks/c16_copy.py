#!/usr/bin/env python3
"""C16. `copy` between two repositories, end to end, and stock's TestCopy on Linux.

Part 1, real binaries. Source repositories written by stock and by the tree (two snapshots of the generated corpus,
edits in between). For every pair (who wrote the source, who copies): the copier initialises the destination with
--copy-chunker-params and copies everything; then
  stock `check --read-data` of the destination passes
  the destination has the same snapshots: same time, host, paths and TREE ID, and stock's `ls --json` of every
    snapshot is identical in source and destination
  stock restores the latest snapshot from the destination: equal to the source directory (content and metadata)
  a second `copy` exits 0 and adds no snapshot and no pack
  `copy <one snapshot id>` into a fresh destination copies exactly that snapshot
Pairs: stock -> tree copies, tree -> stock copies, tree -> tree copies, stock -> stock copies (control).
(`copy` through the REST server with one and two connections is in C8.)

Part 2, stock's test file cmd/restic/cmd_copy_integration_test.go against the tree (TestCopy and its siblings): as
root, as uid 65534 and with RESTIC_TEST_LOCK_WAIT=200ms; the same tests in the stock tree as the control. On the
Windows workstation TestCopy fails in every tree for lack of privileges, so this is its first real run."""
import hashlib
import json
import os
import random
import shutil
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
import gotest  # noqa: E402
from chk import (NOBODY, PASSWORD, QUICK, STOCK_TREE, Check, Server, chkrest_bin, copy_repo, copy_tree, diff_manifests, gen_corpus,  # noqa: E402
                 index_packs, manifest, must, mutate_corpus, prepare, prepare_stock, repo_files, restic, rmtree, short_tmp)

c = Check('c16', 'copy between repositories end to end, both binaries; stock TestCopy on Linux')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
BIN = {'stock': stock.bin, 'tree': cand.bin}
chkrest_bin()
ws = c.work / 'ws'
env = {'RESTIC_FROM_PASSWORD': PASSWORD}
corpus = c.work / 'corpus'
gen_corpus(corpus, seed=16, small=250, text_files=3, text_mb=3, big=(7, 4), special=True)
sources = {}
for writer, binary in BIN.items():
    src = c.work / ('src-' + writer)
    subprocess.check_call(['cp', '-a', str(corpus), str(src)])
    repo = c.work / ('a-' + writer)
    must(restic(binary, repo, 'init', work=ws))
    must(restic(binary, repo, 'backup', '--no-cache', '--host', 'h', '--time', '2026-04-01 00:00:00', src, work=ws))
    mutate_corpus(src, seed=161)
    must(restic(binary, repo, 'backup', '--no-cache', '--host', 'h', '--time', '2026-04-02 00:00:00', src, work=ws))
    sources[writer] = (repo, src)


def snapshot_view(repo):
    snaps = sorted(json.loads(must(restic(stock.bin, repo, 'snapshots', '--json', '--no-cache', work=ws)).out), key=lambda s: s['time'])
    view = []
    for sn in snaps:
        out = must(restic(stock.bin, repo, 'ls', '--json', sn['id'], '--no-cache', work=ws)).out
        nodes = '\n'.join(line for line in out.splitlines() if '"struct_type":"node"' in line)
        view.append({'time': sn['time'], 'hostname': sn.get('hostname'), 'paths': sn.get('paths'), 'tree': sn['tree'],
                     'ls': hashlib.sha256(nodes.encode('utf-8', 'surrogateescape')).hexdigest(), 'id': sn['id']})
    return view


def comparable(view):
    return [{k: v for k, v in item.items() if k != 'id'} for item in view]


PAIRS = [('stock', 'tree'), ('tree', 'stock'), ('tree', 'tree'), ('stock', 'stock')]
if QUICK:
    PAIRS = PAIRS[:2]
c.status('copy pairs', 0, len(PAIRS) + 4)
rows = []
for wrote, copier in PAIRS:
    name = 'source written by %s, copied by %s' % (wrote, copier)
    source, src = sources[wrote]
    binary = BIN[copier]
    dst = c.work / 'dst'
    rmtree(dst)
    problems = []
    i = restic(binary, dst, 'init', '--from-repo', source, '--copy-chunker-params', work=ws, env=env)
    r1 = restic(binary, dst, 'copy', '--from-repo', source, '--no-cache', work=ws, env=env, timeout=1800)
    (c.res / ('copy-%s-by-%s.txt' % (wrote, copier))).write_text('%s\nrc=%d\n%s\n%s' % (r1.cmd, r1.rc, r1.out, r1.err))
    if i.rc != 0 or r1.rc != 0:
        problems.append('init exits %d, copy exits %d: %s' % (i.rc, r1.rc, (r1 if r1.rc else i).brief(300)))
    packs_after_first = len(repo_files(dst, 'data'))
    r2 = restic(binary, dst, 'copy', '--from-repo', source, '--no-cache', work=ws, env=env, timeout=1800)
    if r2.rc != 0:
        problems.append('second copy exits %d: %s' % (r2.rc, r2.brief(300)))
    check = restic(stock.bin, dst, 'check', '--read-data', '--no-cache', work=ws)
    if check.rc != 0:
        problems.append('stock check --read-data of the destination: ' + check.brief(400))
    row = {'pair': name, 'copy_rc': r1.rc, 'copy_seconds': round(r1.secs, 1), 'second_copy_rc': r2.rc}
    if r1.rc == 0:
        a, b = snapshot_view(source), snapshot_view(dst)
        row['snapshots'] = {'source': len(a), 'destination': len(b)}
        if comparable(a) != comparable(b):
            problems.append('snapshots differ between source and destination (count %d / %d, tree IDs %s / %s)' % (
                len(a), len(b), [x['tree'][:8] for x in a], [x['tree'][:8] for x in b]))
        if len(repo_files(dst, 'data')) != packs_after_first:
            problems.append('the second copy added packs (%d -> %d)' % (packs_after_first, len(repo_files(dst, 'data'))))
        target = c.work / 'restore'
        rmtree(target)
        r = restic(stock.bin, dst, 'restore', 'latest', '--no-cache', '--target', target, work=ws)
        diff = diff_manifests(manifest(src), manifest(Path(str(target) + str(src)))) if r.rc == 0 else ['restore exits %d' % r.rc]
        if diff:
            problems.append('restore from the destination differs from the source directory: ' + '; '.join(diff[:3]))
        rmtree(target)
        one = c.work / 'dst-one'
        rmtree(one)
        restic(binary, one, 'init', '--from-repo', source, '--copy-chunker-params', work=ws, env=env)
        r3 = restic(binary, one, 'copy', '--from-repo', source, a[0]['id'], '--no-cache', work=ws, env=env, timeout=1800)
        got = snapshot_view(one) if r3.rc == 0 else []
        if r3.rc != 0 or comparable(got) != comparable(a[:1]):
            problems.append('copy of one snapshot: rc %d, %d snapshots in the destination' % (r3.rc, len(got)))
        row['data_bytes'] = {'source': sum((source / 'data' / n[:2] / n).stat().st_size for n in repo_files(source, 'data')),
                             'destination': sum((dst / 'data' / n[:2] / n).stat().st_size for n in repo_files(dst, 'data'))}
        rmtree(one)
    row['problems'] = problems
    rows.append(row)
    c.log(json.dumps(row))
    if problems and copier == 'stock' and wrote == 'stock':
        c.note('the stock -> stock control has problems (harness or stock): %s' % problems[0][:300])
    elif problems:
        c.fail('copy, %s: %s' % (name, problems[0][:500]), evidence=c.res / ('copy-%s-by-%s.txt' % (wrote, copier)),
               stock='the stock -> stock pair of this run: %s' % ('no problems' if not any(r['problems'] for r in rows if r['pair'].endswith('copied by stock') and 'written by stock' in r['pair']) else 'see notes'),
               repro='restic -r DST init --from-repo SRC --copy-chunker-params; restic -r DST copy --from-repo SRC; restic-stock -r DST check --read-data')
    c.tick()
c.numbers['copy_pairs'] = rows

# ---- a source pack that cannot be read while the destination does not take uploads
# The destination's REST server holds every pack upload for 15 minutes; in the source two data packs are damaged
# (one byte inside a blob) or missing. `copy` must come back with the read error within 90 s instead of waiting for
# its blocked saves. Six runs per binary (the order in which copy reads the packs is not fixed, so in some runs the
# error comes before any save is blocked). Stock is run for the record; a hang of stock is not counted against the tree.
c.status('copy with a failing source pack')
LIMIT = 90
source = sources['stock'][0]
src_packs = index_packs(stock.bin, source, ws)
data_packs = sorted(p for p, blobs in src_packs.items() if all(b['type'] == 'data' for b in blobs))
stalled = []
for arm, binary in BIN.items():
    for n in range(6 if not QUICK else 2):
        kind = 'damaged' if n % 2 == 0 else 'missing'
        rnd = random.Random(1600 + n)
        bad_source = c.work / 'bad-source'
        copy_repo(source, bad_source)
        for pack_id in rnd.sample(data_packs, min(2, len(data_packs))):
            path = bad_source / 'data' / pack_id[:2] / pack_id
            if kind == 'missing':
                path.unlink()
            else:
                blob = max(src_packs[pack_id], key=lambda b: b['length'])
                os.chmod(path, 0o600)
                with open(path, 'r+b') as f:
                    f.seek(blob['offset'] + blob['length'] // 2)
                    old = f.read(1)
                    f.seek(blob['offset'] + blob['length'] // 2)
                    f.write(bytes([old[0] ^ 0x10]))
        dst = c.work / 'dst-stalled'
        rmtree(dst)
        must(restic(binary, dst, 'init', '--from-repo', source, '--copy-chunker-params', work=ws, env=env))
        srv = Server(dst, fail_after=1, fail_count=10**6, fail_mode='stall', fail_method='POST', fail_kind='data', stall_ms=900000)
        r = restic(binary, srv.url, '--pack-size', '4', 'copy', '--from-repo', bad_source, '--no-cache', work=ws, env=env, timeout=LIMIT)
        stats = srv.stop()
        tail = (r.out + r.err).strip().splitlines()
        row = {'binary': arm, 'source': kind, 'run': n + 1, 'rc': r.rc, 'hang': r.rc == -9, 'seconds': round(r.secs, 1),
               'uploads_held_by_the_server': stats.get('injected_failures'), 'last_line': tail[-1][:200] if tail else ''}
        stalled.append(row)
        (c.res / ('copy-stalled-%s-%d.txt' % (arm, n + 1))).write_text('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err))
        c.log('copy with a %s source pack and a stalled destination: %s' % (kind, json.dumps(row)))
c.numbers['copy_failing_source_stalled_destination'] = stalled
tree_rows = [r for r in stalled if r['binary'] == 'tree']
stock_rows = [r for r in stalled if r['binary'] == 'stock']
bad = [r for r in tree_rows if r['hang'] or r['rc'] == 0]
if bad:
    c.fail('copy with a failing source pack while the destination is stalled: %d of %d runs of the tree %s (first: %s)' % (
        len(bad), len(tree_rows), 'hang at the %d s limit or exit 0' % LIMIT, json.dumps(bad[0])), evidence=c.res / ('copy-stalled-tree-%d.txt' % bad[0]['run']),
        stock='stock: %d of %d runs hang, exit codes %s' % (sum(1 for r in stock_rows if r['hang']), len(stock_rows), sorted({r['rc'] for r in stock_rows})),
        repro='chkrest -fail-mode stall -stall-ms 900000 -fail-after 1 -fail-count 1000000 -fail-method POST -fail-kind data (destination); restic -r rest:... copy --from-repo <source with a damaged or missing data pack>')
c.note('copy with a failing source pack and a stalled destination: tree %d runs, %d hang, exit codes %s, %d of them with uploads already held; stock %d runs, %d hang, exit codes %s' % (
    len(tree_rows), sum(1 for r in tree_rows if r['hang']), sorted({r['rc'] for r in tree_rows}), sum(1 for r in tree_rows if r['uploads_held_by_the_server']),
    len(stock_rows), sum(1 for r in stock_rows if r['hang']), sorted({r['rc'] for r in stock_rows})))
c.tick()

# ---------------------------------------------------------------- part 2: stock's copy tests against the tree
NAME = 'cmd/restic/cmd_copy_integration_test.go'
src_tree = c.work / 'tree-src'
copy_tree(c.tree, src_tree)
same = (src_tree / NAME).is_file() and (src_tree / NAME).read_bytes() == (STOCK_TREE / NAME).read_bytes()
if not same:
    c.note('the tree\'s %s differs from stock\'s; stock\'s file is used for this part' % NAME)
    shutil.copyfile(STOCK_TREE / NAME, src_tree / NAME)
stock_src = c.work / 'stock-src'
copy_tree(STOCK_TREE, stock_src)
tests = {}
RUNS = [('stock as root', stock_src, None, None), ('tree as root', src_tree, None, None), ('tree as uid 65534', src_tree, NOBODY, None),
        ('tree with the 200 ms lock wait', src_tree, None, {'RESTIC_TEST_LOCK_WAIT': '200ms'})]
for title, where, uid, extra_env in RUNS:
    out = c.res / ('testcopy-%s.json' % title.replace(' ', '-'))
    gotest.go_test(where, ['./cmd/restic'], out, run_filter='^TestCopy', tmp=short_tmp(c.label, 'c16', title), user=uid, env=extra_env, extra=['-v'])
    parsed = gotest.parse(out)
    tests[title] = {t: a for p in parsed.values() for t, a in p['tests'].items() if '/' not in t}
    c.log('copy tests, %s: %s' % (title, json.dumps(tests[title])))
    if not tests[title]:
        c.note('copy tests, %s: nothing ran (%s)' % (title, ''.join(next(iter(parsed.values()))['pkg_output'])[-300:] if parsed else 'no output'))
    c.tick()
c.numbers['copy_tests'] = tests
c.numbers['copy_test_file_identical_to_stock'] = same
control = tests.get('stock as root', {})
for title, result in tests.items():
    if title.startswith('stock'):
        continue
    for test, action in sorted(result.items()):
        if action != 'pass' and control.get(test) == 'pass':
            c.fail('%s (stock test) fails on the tree, %s' % (test, title), evidence=c.res / ('testcopy-%s.json' % title.replace(' ', '-')),
                   stock='passes on stock as root', repro='go test -count=1 -v -run "^%s$" ./cmd/restic' % test)
        elif action != 'pass':
            c.note('%s, %s: %s (stock: %s)' % (test, title, action, control.get(test)))
ok_pairs = sum(1 for r in rows if not r['problems'])
passed = {title: '%d/%d' % (sum(1 for a in r.values() if a == 'pass'), len(r)) for title, r in tests.items()}
sys.exit(c.finish('copy end to end: %d of %d pairs without a problem; failing source pack with a stalled destination: tree returns the error in %d of %d runs (stock %d of %d); stock copy tests passed: %s' % (
    ok_pairs, len(rows), sum(1 for r in tree_rows if not r['hang'] and r['rc'] != 0), len(tree_rows),
    sum(1 for r in stock_rows if not r['hang'] and r['rc'] != 0), len(stock_rows), json.dumps(passed))))
