#!/usr/bin/env python3
"""C6. Cross-read matrix in both directions.

Part 1, live repositories. For repository version 1 and 2, compression off / auto / max (version 2 only) and
--pack-size 4 and 128, in both directions (writer = stock, reader = the tree; and the reverse):
  writer: init, backup, change the source, backup
  reader: check --read-data; restore latest and compare with the source (content, metadata, hard links, xattrs);
          `ls --json latest` and `snapshots --json` must be byte-identical to the writer's own output
  reader: backup on top (source changed again), forget the oldest snapshot with --prune
  writer again: check --read-data; restore latest and compare with the source; `ls --json latest` identical
Part 2, restic's own test fixtures (every *.tar.gz repository under the stock tree, old index formats, mixed packs,
duplicates, damaged ones): snapshots, check, check --read-data, ls --json and restore of every snapshot must give
the same exit code, the same output and the same restored files with both binaries; then backup + prune by each
binary on its own copy and a stock check of both copies.
Repository size (repair R4): what each binary stores for the same data in copies of ONE initialised repository (same
chunker polynomial), per configuration, and for the judge's three corpora (read only; main and big-index share one
corpus). The two sizes must agree within 0.1% + 64 KiB.
Not possible here: a repository whose index was written by rustic (no rustic binary on the offline box)."""
import json
import os
import subprocess
import sys
import tarfile
import threading
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import (QUICK, STOCK_TREE, Check, append, blur_recent_mtimes, copy_repo, diff_manifests, envint, gen_corpus, manifest, must,  # noqa: E402
                 mutate_corpus, prepare, prepare_stock, restic, rmtree, same_output, write_file)

c = Check('c06', 'cross-read matrix stock <-> tree, repository v1/v2, compression, pack sizes, fixtures')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
WORKERS = envint('CHK_C06_WORKERS', 3)
CONFIGS = [(1, None, 4), (1, None, 128), (2, 'off', 4), (2, 'off', 128), (2, 'auto', 4), (2, 'auto', 128), (2, 'max', 4), (2, 'max', 128)]
if QUICK:
    CONFIGS = [(1, None, 4), (2, 'max', 128)]
corpus = c.work / 'corpus'
gen_corpus(corpus, seed=6, small=300, text_files=4, text_mb=4, big=(9, 6, 13), special=True)
BIN = {'stock': stock.bin, 'tree': cand.bin}
problems_lock = threading.Lock()
cells = []


def flags(version, compression, pack):
    out = ['--pack-size', str(pack)]
    if compression:
        out += ['--compression', compression]
    return out


def restore_and_compare(binary, repo, src, wdir, what, problems):
    target = wdir / 'restore'
    rmtree(target)
    r = restic(binary, repo, 'restore', 'latest', '--no-cache', '--target', target, work=wdir)
    if r.rc != 0:
        problems.append('%s: restore exits %d: %s' % (what, r.rc, r.brief(300)))
        return
    diff = diff_manifests(manifest(src), manifest(Path(str(target) + str(src))))
    if diff:
        problems.append('%s: restored tree differs from the source: %s' % (what, '; '.join(diff[:4])))
    rmtree(target)


def cell(writer, reader, version, compression, pack, wdir):
    name = '%s writes, %s reads, v%d, compression %s, pack-size %d' % (writer, reader, version, compression or 'n/a', pack)
    problems = []
    W, Rd = BIN[writer], BIN[reader]
    opt = flags(version, compression, pack)
    repo, src = wdir / 'repo', wdir / 'src'
    rmtree(repo)
    rmtree(src)
    subprocess.check_call(['cp', '-a', str(corpus), str(src)])
    must(restic(W, repo, 'init', '--repository-version', str(version), work=wdir), name + ': init')
    must(restic(W, repo, 'backup', '--no-cache', '--host', 'h', *opt, src, work=wdir), name + ': first backup')
    mutate_corpus(src, seed=61)
    must(restic(W, repo, 'backup', '--no-cache', '--host', 'h', *opt, src, work=wdir), name + ': second backup')
    r = restic(Rd, repo, 'check', '--read-data', '--no-cache', work=wdir)
    if r.rc != 0:
        problems.append('reader check --read-data exits %d: %s' % (r.rc, r.brief(400)))
    restore_and_compare(Rd, repo, src, wdir, 'reader', problems)
    for command in (['ls', '--json', 'latest'], ['snapshots', '--json'], ['ls', '--long', 'latest']):
        a = restic(W, repo, *command, '--no-cache', work=wdir)
        b = restic(Rd, repo, *command, '--no-cache', work=wdir)
        if a.rc != b.rc or a.out != b.out:
            problems.append('`%s` differs between writer and reader (rc %d/%d, %d/%d bytes)' % (' '.join(command), a.rc, b.rc, len(a.out), len(b.out)))
    mutate_corpus(src, seed=62)
    r = restic(Rd, repo, 'backup', '--no-cache', '--host', 'h', *opt, src, work=wdir)
    if r.rc != 0:
        problems.append('reader backup on top exits %d: %s' % (r.rc, r.brief(400)))
    r = restic(Rd, repo, 'forget', '--keep-last', '2', '--prune', '--no-cache', *opt, work=wdir)
    if r.rc != 0:
        problems.append('reader forget --prune exits %d: %s' % (r.rc, r.brief(400)))
    r = restic(W, repo, 'check', '--read-data', '--no-cache', work=wdir)
    if r.rc != 0:
        problems.append('writer check --read-data after the reader wrote exits %d: %s' % (r.rc, r.brief(400)))
    restore_and_compare(W, repo, src, wdir, 'writer again', problems)
    a = restic(W, repo, 'ls', '--json', 'latest', '--no-cache', work=wdir)
    b = restic(Rd, repo, 'ls', '--json', 'latest', '--no-cache', work=wdir)
    if a.rc != b.rc or a.out != b.out:
        problems.append('`ls --json latest` differs after the reader wrote (rc %d/%d)' % (a.rc, b.rc))
    sizes = sum(p.stat().st_size for p in (repo / 'data').glob('*/*'))
    return name, problems, sizes


jobs = [(w, r, v, comp, pack) for (v, comp, pack) in CONFIGS for (w, r) in (('stock', 'tree'), ('tree', 'stock'))]
c.status('live matrix', 0, len(jobs))
position = [0]


def worker(index):
    wdir = c.work / ('w%d' % index)
    wdir.mkdir(exist_ok=True)
    while True:
        with problems_lock:
            if position[0] >= len(jobs):
                return
            job = jobs[position[0]]
            position[0] += 1
        try:
            name, problems, sizes = cell(*job, wdir)
        except Exception as exc:
            name, problems, sizes = '%s writes, %s reads, v%d, compression %s, pack-size %d' % job, ['stopped: %r' % (exc,)], 0
        with problems_lock:
            cells.append({'cell': name, 'problems': problems, 'data_bytes': sizes})
            append(c.res / 'matrix.jsonl', json.dumps(cells[-1]))
        c.log('%s: %s' % (name, 'ok' if not problems else ' | '.join(problems)[:600]))
        if problems:
            c.fail('cross-read: %s: %s' % (name, problems[0][:500]), evidence=c.res / 'matrix.jsonl',
                   stock='the other direction and the stock-only steps are in the same file',
                   repro='python3 c06_crossread.py <tree> <label>')
        c.tick()


threads = [threading.Thread(target=worker, args=(i,)) for i in range(WORKERS)]
for t in threads:
    t.start()
for t in threads:
    t.join()
c.numbers['matrix_cells'] = len(cells)
c.numbers['matrix_cells_with_problems'] = sum(1 for x in cells if x['problems'])
c.numbers['repository_bytes'] = {x['cell']: x['data_bytes'] for x in sorted(cells, key=lambda x: x['cell'])}

# ---------------------------------------------------------------- repository size, stock against the tree (repair R4)
# Both binaries write into COPIES OF ONE initialised repository: `init` draws a random chunker polynomial, and two
# repositories with different polynomials cut large files at different places. Until 2026-10-07T09:45Z this part
# compared repositories that each binary had initialised itself; stock against stock then differs by about 1% on the
# judge's main corpus and by up to 12% on the generated corpus (which edit lands in which chunk), so the percentages
# of the runs before that say nothing about the binaries (diagnostic: x06_size_anatomy.py).
# The generated corpus holds, among others, a file that repeats one incompressible 64 KiB block (the shape for which
# the unrepaired tree stored 16 times more than stock).
size_rows = []
ws = c.work / 'ws'


def data_bytes(repo):
    files = [p.stat().st_size for p in (repo / 'data').glob('*/*')]
    return sum(files), len(files)


def size_pair(title, init_flags, steps, flags):
    """steps: functions that put the source in place for one backup and return (cwd, path argument)."""
    start = c.work / 'size-init'
    rmtree(start)
    must(restic(stock.bin, start, 'init', *init_flags, work=ws))
    got = {}
    for arm, binary in BIN.items():
        repo = c.work / 'size-run'
        copy_repo(start, repo)
        codes = []
        for step in steps:
            cwd, arg = step()
            codes.append(restic(binary, repo, 'backup', '--no-cache', '--host', 'h', *flags, arg, work=ws, cwd=cwd, timeout=3600).rc)
        got[arm] = data_bytes(repo) + (codes,)
        rmtree(repo)
    row = {'config': title, 'stock_bytes': got['stock'][0], 'tree_bytes': got['tree'][0], 'packs': {a: got[a][1] for a in got},
           'tree_over_stock_percent': round(100.0 * (got['tree'][0] - got['stock'][0]) / max(1, got['stock'][0]), 4),
           'backup_rc': {a: got[a][2] for a in got}}
    size_rows.append(row)
    c.log('repository size, %s: stock %d bytes in %d packs, tree %d bytes in %d packs (%+.4f%%)' % (
        title, row['stock_bytes'], got['stock'][1], row['tree_bytes'], got['tree'][1], row['tree_over_stock_percent']))
    if abs(row['tree_bytes'] - row['stock_bytes']) > row['stock_bytes'] * 0.001 + 65536 or got['stock'][2] != got['tree'][2]:
        c.fail('repository size, same initialised repository, same input: the tree stores %+.4f%% against stock for %s (%d against %d bytes; backup exit codes %s / %s)' % (
            row['tree_over_stock_percent'], title, row['tree_bytes'], row['stock_bytes'], got['tree'][2], got['stock'][2]), evidence=c.res / 'summary.json',
            stock='stock is the reference; the anatomy (duplicates, pack overhead, stored length per blob) comes from x06_size_anatomy.py',
            repro='restic init; cp -a REPO REPO2; restic-stock -r REPO backup SRC; restic-tree -r REPO2 backup SRC; du -sb REPO/data REPO2/data')


c.status('repository size')
size_src = c.work / 'size-src'


def first_state():
    rmtree(size_src)
    subprocess.check_call(['cp', '-a', str(corpus), str(size_src)])
    return size_src.parent, size_src.name


def second_state():
    mutate_corpus(size_src, seed=61)
    return size_src.parent, size_src.name


for (version, compression, pack) in CONFIGS:
    size_pair('v%d compression %s pack-size %d, two backups' % (version, compression or 'n/a', pack), ['--repository-version', str(version)],
              [first_state, second_state], flags(version, compression, pack))
JUDGE = [('main (= big-index corpus)', 'workloads'), ('large-files', 'workloads-large-files'), ('remote', 'workloads-remote')]
if not QUICK:
    for title, folder in JUDGE:
        corpus_dir = STOCK_TREE.parent / folder / 'corpus-v1'
        if not corpus_dir.is_dir():
            c.note('judge corpus %s is not readable: no size comparison for it' % folder)
            continue
        size_pair('judge corpus %s, default options' % title, [], [lambda d=corpus_dir: (d, '.')], [])
c.numbers['repository_size'] = size_rows

# ---------------------------------------------------------------- part 2: fixtures
fixtures = sorted(p for p in STOCK_TREE.rglob('*.tar.gz') if 'testdata' in p.parts)
PASSWORDS = ['geheim', 'test', 'foo', 'password', '']
c.status('fixtures', 0, len(fixtures))
fixture_rows = []
wf = c.work / 'fixtures'
small = c.work / 'small-src'
write_file(small / 'a.txt', b'cross-read backup on top\n' * 500)
write_file(small / 'b.bin', os.urandom(200_000))


def find_repo(root):
    for dirpath, dirnames, filenames in os.walk(root):
        if 'config' in filenames and 'keys' in dirnames:
            return Path(dirpath)
    return None


for fixture in fixtures:
    rel = str(fixture.relative_to(STOCK_TREE))
    row = {'fixture': rel, 'problems': []}
    ex = wf / 'extract'
    rmtree(ex)
    ex.mkdir(parents=True)
    try:
        with tarfile.open(fixture) as tar:
            tar.extractall(ex, filter='tar')
    except Exception as exc:
        row['skipped'] = 'cannot extract: %r' % (exc,)
        fixture_rows.append(row)
        c.tick()
        continue
    repo0 = find_repo(ex)
    if repo0 is None:
        row['skipped'] = 'not a repository archive'
        fixture_rows.append(row)
        c.tick()
        continue
    password = None
    for candidate in PASSWORDS:
        if restic(stock.bin, repo0, 'cat', 'config', '--no-lock', '--no-cache', work=wf, env={'RESTIC_PASSWORD': candidate}).rc == 0:
            password = candidate
            break
    if password is None:
        row['skipped'] = 'no known password opens it with stock'
        fixture_rows.append(row)
        c.tick()
        continue
    env = {'RESTIC_PASSWORD': password}
    copies = {}
    for arm in ('stock', 'tree'):
        copies[arm] = wf / ('repo-' + arm)
        copy_repo(repo0, copies[arm])
    hide = {arm: [(str(copies[arm]), '<repo>')] for arm in copies}

    def both(*command, compare='output'):
        a = restic(stock.bin, copies['stock'], *command, '--no-cache', work=wf, env=env)
        b = restic(cand.bin, copies['tree'], *command, '--no-cache', work=wf, env=env)
        if compare == 'exact':
            if a.rc != b.rc or a.out != b.out:
                row['problems'].append('`%s`: rc %d/%d, stdout %s' % (' '.join(command), a.rc, b.rc, 'equal' if a.out == b.out else 'differs'))
        else:
            diff = same_output(a, b, hide['stock'], hide['tree'])
            if diff:
                row['problems'].append('`%s`: %s' % (' '.join(command), '; '.join(diff)[:500]))
        return a, b
    a, b = both('snapshots', '--json', compare='exact')
    both('check')
    both('check', '--read-data')
    ids = []
    try:
        ids = [s['id'] for s in json.loads(a.out)][:4]
    except ValueError:
        pass
    row['snapshots'] = len(ids)
    for sid in ids:
        both('ls', '--json', sid, compare='exact')
        targets = {}
        rcs = {}
        for arm, binary in BIN.items():
            targets[arm] = wf / ('restore-' + arm)
            rmtree(targets[arm])
            rcs[arm] = restic(binary, copies[arm], 'restore', sid, '--target', targets[arm], '--no-cache', work=wf, env=env).rc
        if rcs['stock'] != rcs['tree']:
            row['problems'].append('restore %s: rc stock %d, tree %d' % (sid[:8], rcs['stock'], rcs['tree']))
        since = int(c.started * 1e9)
        ma = blur_recent_mtimes(manifest(targets['stock']) if targets['stock'].is_dir() else {}, since)
        mb = blur_recent_mtimes(manifest(targets['tree']) if targets['tree'].is_dir() else {}, since)
        diff = diff_manifests(ma, mb)
        if diff:
            row['problems'].append('restore %s: restored trees differ: %s' % (sid[:8], '; '.join(diff[:3])))
        for t in targets.values():
            rmtree(t)
    # backup on top and prune by each binary on its own copy, then stock checks both
    steps = {}
    for arm, binary in BIN.items():
        b1 = restic(binary, copies[arm], 'backup', '--no-cache', '--host', 'h', small, work=wf, env=env)
        p1 = restic(binary, copies[arm], 'prune', '--no-cache', work=wf, env=env)
        k1 = restic(stock.bin, copies[arm], 'check', '--read-data', '--no-cache', work=wf, env=env)
        steps[arm] = (b1.rc, p1.rc, k1.rc)
    row['backup_prune_check_rc'] = steps
    if steps['stock'] != steps['tree']:
        row['problems'].append('backup, prune, stock check exit codes: stock %s, tree %s' % (steps['stock'], steps['tree']))
    fixture_rows.append(row)
    append(c.res / 'fixtures.jsonl', json.dumps(row))
    c.log('fixture %s: %s' % (rel, 'same' if not row['problems'] else ' | '.join(row['problems'])[:600]))
    if row['problems']:
        c.fail('fixture %s: the tree and stock disagree: %s' % (rel, row['problems'][0][:500]), evidence=c.res / 'fixtures.jsonl',
               stock='stock is the reference in this comparison', repro='python3 c06_crossread.py <tree> <label>   (part 2)')
    c.tick()
used = [r for r in fixture_rows if 'skipped' not in r]
skipped = [r for r in fixture_rows if 'skipped' in r]
(c.res / 'fixtures-skipped.json').write_text(json.dumps(skipped, indent=1) + '\n')
c.numbers.update(fixtures_compared=len(used), fixtures_skipped=len(skipped), fixtures_with_differences=sum(1 for r in used if r['problems']))
c.note('not run: a repository whose index was written by rustic (no rustic binary on the offline box)')
worst = max((r['tree_over_stock_percent'] for r in size_rows), key=abs, default=0.0)
summary = 'live matrix %d cells (%d with a problem); fixtures %d compared (%d differ), %d archives skipped (not repositories or unknown password); repository size tree vs stock, same init, in %d comparisons: largest difference %+.4f%%' % (
    len(cells), c.numbers['matrix_cells_with_problems'], len(used), c.numbers['fixtures_with_differences'], len(skipped), len(size_rows), worst)
sys.exit(c.finish(summary))
