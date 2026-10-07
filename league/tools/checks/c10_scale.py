#!/usr/bin/env python3
"""C10. Scale, as far as 16 GiB of memory and the check CPUs allow. Runs alone (run_all starts it last).

A. R5, index load with few CPUs behind latency: a repository with about 500 index files, served by chkrest with
   50 ms per request; `ls latest --no-cache` pinned to 1 CPU and to 2 CPUs; wall time (median of 3) and the largest
   number of index downloads in flight, for stock, the tree and the unrepaired tree.
B. M22 / F5, restore of snapshots with 606,060, 1,010,100 and 2,020,200 directories (and 3, 5, 10 files): peak RSS
   and wall time for stock, the tree and the trees named in CHK_EXTRA_TREES; the unrepaired tree at the largest size;
   the tree's restored directory tree of the largest size is compared with the source.
C. One 50 GiB sparse file: restore with --sparse and without, peak RSS and time, stock and tree; size and SHA-256 of
   every restored file against the source.
D. Index size: synthetic index files for 3, 10 and 30 million invented blobs (chktool chk-fakeindex; no pack files
   exist), then a small `backup` (it loads the whole index): peak RSS and time, stock and tree. A size is skipped,
   and reported as not reached, when the previous size predicts more than 11 GiB for either binary.
   prune and check cannot be measured this way (they need the packs); C11 covers prune on the judge's
   1.5-million-blob repository.
A part fails when the tree needs more than 1.3 x stock's peak RSS + 64 MiB, or (A) is slower than 1.25 x stock + 0.5 s
or has fewer index downloads in flight than stock."""
import hashlib
import json
import os
import random
import statistics
import subprocess
import sys
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import (CPUS, QUICK, Check, Server, chkrest_bin, chktool_bin, envint, manifest, diff_manifests, must, prepare,  # noqa: E402
                 prepare_before, prepare_stock, repo_files, restic, restic_env, rmtree, run_rusage, write_file)

c = Check('c10', 'scale: index load on 1-2 CPUs behind latency (R5), 2M directories (M22), 50 GiB file, big index')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
arms = [('stock', stock.bin), ('tree', cand.bin)]
try:
    arms.append(('before', prepare_before().bin))
except Exception as exc:
    c.note('the unrepaired tree could not be built: %r' % (exc,))
# CHK_EXTRA_TREES="name=/path,...": more binaries for part B (for example the tree before a memory repair)
extra_arms = []
for item in [x for x in os.environ.get('CHK_EXTRA_TREES', '').split(',') if '=' in x]:
    name, path = item.split('=', 1)
    try:
        extra_arms.append((name, prepare('_x_' + name, path).bin))
    except Exception as exc:
        c.note('%s (%s) could not be built: %r' % (name, path, exc))
chkrest_bin()
ws = c.work / 'ws'
INDEX_FILES = envint('CHK_C10_INDEX_FILES', 500)
DIRS = envint('CHK_C10_DIRS', 2_000_000)
SPARSE_GIB = envint('CHK_C10_SPARSE_GIB', 50)
FAKE = [int(x) for x in os.environ.get('CHK_C10_FAKE_BLOBS', '3000000,10000000,30000000').split(',') if x]
if QUICK:
    INDEX_FILES, DIRS, SPARSE_GIB, FAKE = 40, 20_000, 1, [200_000]
c.status('A index load', 0, 4)


def too_much(tree_rss, stock_rss):
    return tree_rss > stock_rss * 1.3 + 64 * 1024


# ---------------------------------------------------------------- A. R5
repo = c.work / 'a-repo'
must(restic(stock.bin, repo, 'init', work=ws))
lock = threading.Lock()
left = [INDEX_FILES]


def feeder(n):
    w = c.work / ('a-w%d' % n)
    src = w / 'src'
    while True:
        with lock:
            if left[0] <= 0:
                return
            left[0] -= 1
            k = left[0]
        write_file(src / 'f', os.urandom(10_000))
        restic(stock.bin, repo, 'backup', '--no-cache', '--host', 'h%d' % n, src, work=w)


threads = [threading.Thread(target=feeder, args=(i,)) for i in range(6)]
for t in threads:
    t.start()
for t in threads:
    t.join()
index_files = len(repo_files(repo, 'index'))
c.log('A: repository with %d index files and %d snapshots' % (index_files, len(repo_files(repo, 'snapshots'))))
physical = [x for x in CPUS if x < 48] or CPUS
cpu_sets = {'1 CPU': [physical[0]], '2 CPUs': physical[:2] if len(physical) > 1 else CPUS[:2]}
a_rows = {}
for cpus_name, cpus in cpu_sets.items():
    for arm, binary in arms:
        times, flights, requests = [], [], []
        for rep in range(3 if not QUICK else 1):
            srv = Server(repo, latency_ms=50)
            res = run_rusage(['taskset', '-c', ','.join(map(str, cpus)), str(binary), '-r', srv.url, 'ls', 'latest', '--no-cache'],
                             env=restic_env(ws), timeout=1800)
            stats = srv.stop()
            if res['rc'] != 0:
                c.note('A: %s on %s exits %d: %s' % (arm, cpus_name, res['rc'], res['err'][-200:]))
            times.append(res['seconds'])
            flights.append(stats.get('by', {}).get('GET index', {}).get('max_in_flight', 0))
            requests.append(stats.get('requests', 0))
        a_rows['%s | %s' % (cpus_name, arm)] = {'seconds_median': round(statistics.median(times), 2), 'seconds': [round(x, 2) for x in times],
                                                'index_downloads_in_flight_max': max(flights), 'requests': requests[0]}
        c.log('A: %s, %s: %s' % (cpus_name, arm, json.dumps(a_rows['%s | %s' % (cpus_name, arm)])))
    s, t = a_rows[cpus_name + ' | stock'], a_rows[cpus_name + ' | tree']
    if t['index_downloads_in_flight_max'] < s['index_downloads_in_flight_max'] or t['seconds_median'] > s['seconds_median'] * 1.25 + 0.5:
        c.fail('R5, index load of %d index files behind 50 ms on %s: tree %s, stock %s' % (index_files, cpus_name, json.dumps(t), json.dumps(s)),
               evidence=c.res / 'summary.json', stock=json.dumps(s),
               repro='chkrest -latency-ms 50 ...; taskset -c %s restic -r rest:http+unix://SOCK:/ ls latest --no-cache' % ','.join(map(str, cpus)))
c.numbers['A_index_load'] = {'index_files': index_files, 'latency_ms': 50, 'rows': a_rows}
rmtree(repo)
c.tick()

# ---------------------------------------------------------------- B. directories: 0.6, 1 and 2 million
# t0000/m00/d00, 100 x 101 directories under each top directory, one small file in every twentieth top directory:
# 60 tops = 606,060 directories and 3 files (the case the bound of the restore's tree-ID list was chosen on),
# 100 tops = 1,010,100, 200 tops = 2,020,200. Restores by stock, the tree and the extra trees; the unrepaired tree
# only at the largest size. The smallest size is restored twice per binary (wall time).
c.status('B directories')
TOPS = [int(x) for x in os.environ.get('CHK_C10_DIR_TOPS', '60,100,200').split(',')] if not QUICK else [2, 4]
b_cases = {}
dirs_largest = 0
for tops in TOPS:
    src = c.work / 'b-src'
    rmtree(src)
    began = time.time()
    made = 0
    files = 0
    for i in range(tops):
        for j in range(100):
            base = src / ('t%04d' % i) / ('m%02d' % j)
            os.makedirs(base)
            for k in range(100):
                os.mkdir(base / ('d%02d' % k))
            made += 101
        if i % 20 == 0:
            write_file(src / ('t%04d' % i) / 'file.txt', b'a file among the directories' + bytes([10]))
            files += 1
    dirs = made + tops
    dirs_largest = dirs
    c.log('B: %d directories and %d files created in %d s' % (dirs, files, time.time() - began))
    repo = c.work / 'b-repo'
    rmtree(repo)
    must(restic(stock.bin, repo, 'init', work=ws))
    res = run_rusage([str(stock.bin), '-r', str(repo), 'backup', '--no-cache', '--host', 'h', src.name], env=restic_env(ws), timeout=7200, cwd=src.parent)
    row = {'directories': dirs, 'files': files, 'backup_by_stock': {'rc': res['rc'], 'seconds': round(res['seconds']), 'rss_mib': round(res['rss_kib'] / 1024, 1)}}
    c.log('B: %d directories: stock backup %s' % (dirs, json.dumps(row['backup_by_stock'])))
    source_count = int(subprocess.check_output('find %s | wc -l' % src, shell=True))
    largest = tops == TOPS[-1]
    repeats = 2 if tops == TOPS[0] and len(TOPS) > 1 else 1
    for arm, binary in arms[:2] + extra_arms + (arms[2:] if largest else []):
        runs = []
        for n in range(repeats):
            target = c.work / 'b-restore'
            rmtree(target)
            res = run_rusage([str(binary), '-r', str(repo), 'restore', 'latest', '--no-cache', '--target', str(target)], env=restic_env(ws), timeout=7200)
            count = int(subprocess.check_output('find %s | wc -l' % (target / src.name), shell=True)) if (target / src.name).exists() else 0
            runs.append({'rc': res['rc'], 'seconds': round(res['seconds'], 1), 'rss_mib': round(res['rss_kib'] / 1024, 1), 'entries_restored': count})
            if arm == 'tree' and largest and res['rc'] == 0:
                diff = diff_manifests(manifest(src), manifest(target / src.name))
                runs[-1]['differences_from_source'] = len(diff)
                if diff:
                    c.fail('the restored tree of %d directories differs from the source: %s' % (dirs, '; '.join(diff[:3])), evidence=c.res / 'summary.json',
                           stock='see the stock cell of the same size', repro='restic restore latest --target T on a snapshot with %d directories' % dirs)
            rmtree(target)
        row[arm] = {'rc': runs[0]['rc'], 'seconds': min(r['seconds'] for r in runs), 'seconds_all': [r['seconds'] for r in runs],
                    'rss_mib': max(r['rss_mib'] for r in runs), 'entries_restored': runs[0]['entries_restored'], 'entries_source': source_count}
        if 'differences_from_source' in runs[-1]:
            row[arm]['differences_from_source'] = runs[-1]['differences_from_source']
        c.log('B: %d directories, restore by %s: %s' % (dirs, arm, json.dumps(row[arm])))
    s, t = row['stock'], row['tree']
    row['tree_minus_stock_mib'] = round(t['rss_mib'] - s['rss_mib'], 1)
    row['tree_seconds_over_stock_percent'] = round(100.0 * (t['seconds'] - s['seconds']) / max(0.1, s['seconds']), 1)
    if t['rc'] != s['rc'] or t['entries_restored'] != s['entries_restored'] or too_much(t['rss_mib'] * 1024, s['rss_mib'] * 1024):
        c.fail('M22 / F5, restore of %d directories: tree %s, stock %s' % (dirs, json.dumps(t), json.dumps(s)), evidence=c.res / 'summary.json',
               stock=json.dumps(s), repro='restic restore latest --target T on a snapshot with %d directories' % dirs)
    if t['seconds'] > s['seconds'] * 1.15 + 2:
        c.note('restore of %d directories: the tree needs %.1f s, stock %.1f s (%+.1f%%)' % (dirs, t['seconds'], s['seconds'], row['tree_seconds_over_stock_percent']))
    b_cases[str(dirs)] = row
    rmtree(repo)
    rmtree(src)
b_rows = dict(b_cases[str(dirs_largest)])
c.numbers['B_directories'] = b_cases
c.tick()

# ---------------------------------------------------------------- C. one 50 GiB sparse file
c.status('C sparse file')
src = c.work / 'c-src'
src.mkdir()
big = src / 'sparse.img'
r = random.Random(10)
with open(big, 'wb') as f:
    f.truncate(SPARSE_GIB << 30)
    for at in (0, (SPARSE_GIB << 30) // 5, (SPARSE_GIB << 30) // 2 + 12345, (SPARSE_GIB << 30) - (3 << 20)):
        f.seek(at)
        f.write(r.randbytes(3 << 20))


def sha(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        while True:
            block = f.read(8 << 20)
            if not block:
                break
            h.update(block)
    return h.hexdigest()


want = sha(big)
repo = c.work / 'c-repo'
must(restic(stock.bin, repo, 'init', work=ws))
res = run_rusage([str(stock.bin), '-r', str(repo), 'backup', '--no-cache', '--host', 'h', src.name], env=restic_env(ws), timeout=7200, cwd=src.parent)
c_rows = {'file_gib': SPARSE_GIB, 'backup_by_stock': {'rc': res['rc'], 'seconds': round(res['seconds']), 'rss_mib': res['rss_kib'] // 1024}}
c.log('C: stock backup of the %d GiB sparse file: %s' % (SPARSE_GIB, json.dumps(c_rows['backup_by_stock'])))
for mode, flags in (('--sparse', ['--sparse']), ('default', [])):
    for arm, binary in arms[:2]:
        target = c.work / 'c-restore'
        rmtree(target)
        res = run_rusage([str(binary), '-r', str(repo), 'restore', 'latest', '--no-cache', '--target', str(target)] + flags, env=restic_env(ws), timeout=7200)
        out = target / src.name / big.name
        ok = out.is_file() and out.stat().st_size == big.stat().st_size and sha(out) == want
        row = {'rc': res['rc'], 'seconds': round(res['seconds'], 1), 'rss_mib': res['rss_kib'] // 1024, 'content_equal': ok,
               'allocated_mib': (out.stat().st_blocks * 512) >> 20 if out.is_file() else None}
        c_rows['%s | %s' % (mode, arm)] = row
        c.log('C: restore %s by %s: %s' % (mode, arm, json.dumps(row)))
        rmtree(target)
    s, t = c_rows[mode + ' | stock'], c_rows[mode + ' | tree']
    if t['rc'] != s['rc'] or not t['content_equal'] or too_much(t['rss_mib'] * 1024, s['rss_mib'] * 1024) or (
            mode == '--sparse' and t['allocated_mib'] is not None and s['allocated_mib'] is not None and t['allocated_mib'] > s['allocated_mib'] * 1.2 + 64):
        c.fail('restore of one %d GiB sparse file (%s): tree %s, stock %s' % (SPARSE_GIB, mode, json.dumps(t), json.dumps(s)), evidence=c.res / 'summary.json',
               stock=json.dumps(s), repro='restic restore latest --target T %s' % ' '.join(flags))
c.numbers['C_sparse_file'] = c_rows
rmtree(repo)
rmtree(src)
c.tick()

# ---------------------------------------------------------------- D. synthetic index
c.status('D big index')
tool = chktool_bin()
tiny = c.work / 'd-tiny'
d_rows = {}
worst = 0
previous = 0
for blobs in FAKE:
    if previous and worst * (blobs / previous) > 11 * 1024 * 1024:
        d_rows[str(blobs)] = 'not reached: %d blobs peaked at %d MiB, which predicts more than 11 GiB here' % (previous, worst // 1024)
        c.note('index of %d blobs %s' % (blobs, d_rows[str(blobs)]))
        continue
    repo = c.work / 'd-repo'
    rmtree(repo)
    must(restic(stock.bin, repo, 'init', work=ws))
    made = restic(tool, repo, 'chk-fakeindex', '--blobs', blobs, '--no-cache', work=ws, timeout=7200)
    if made.rc != 0:
        c.note('chk-fakeindex for %d blobs failed: %s' % (blobs, made.brief(300)))
        d_rows[str(blobs)] = 'not reached: the index could not be written'
        continue
    row = {'index_files': len(repo_files(repo, 'index')), 'index_bytes': sum((repo / 'index' / n).stat().st_size for n in repo_files(repo, 'index')),
           'write_seconds': round(made.secs)}
    for arm, binary in arms[:2]:
        write_file(tiny / 'f', os.urandom(5000))
        res = run_rusage([str(binary), '-r', str(repo), 'backup', '--no-cache', '--host', 'h', str(tiny)], env=restic_env(ws), timeout=7200)
        row[arm] = {'rc': res['rc'], 'seconds': round(res['seconds'], 1), 'rss_mib': res['rss_kib'] // 1024}
        worst = max(worst, res['rss_kib']) if blobs != previous else res['rss_kib']
    worst = max(row['stock']['rss_mib'], row['tree']['rss_mib']) * 1024
    previous = blobs
    d_rows[str(blobs)] = row
    c.log('D: index of %d invented blobs: %s' % (blobs, json.dumps(row)))
    s, t = row['stock'], row['tree']
    if t['rc'] != s['rc'] or too_much(t['rss_mib'] * 1024, s['rss_mib'] * 1024):
        c.fail('backup against an index of %d blobs: tree %s, stock %s' % (blobs, json.dumps(t), json.dumps(s)), evidence=c.res / 'summary.json',
               stock=json.dumps(s), repro='chktool -r REPO chk-fakeindex --blobs %d; restic -r REPO backup <small dir>' % blobs)
    rmtree(repo)
c.numbers['D_big_index'] = d_rows
c.tick()
reached = [int(k) for k, v in d_rows.items() if isinstance(v, dict)]
extra_text = ''.join(', %s %s' % (name, b_rows[name]['rss_mib']) for name, _ in arms[2:] + extra_arms if name in b_rows)
extra_text += ' (wall s: stock %s, tree %s%s)' % (b_rows['stock']['seconds'], b_rows['tree']['seconds'],
                                                 ''.join(', %s %s' % (name, b_rows[name]['seconds']) for name, _ in extra_arms if name in b_rows))
for key, row in b_cases.items():
    if key != str(b_rows['directories']):
        extra_text += '; %s dirs: RSS stock %s / tree %s MiB, wall stock %s / tree %s s' % (key, row['stock']['rss_mib'], row['tree']['rss_mib'], row['stock']['seconds'], row['tree']['seconds'])
summary = 'A %d index files, 50 ms: 1 CPU stock %ss / tree %ss (in flight %s / %s), 2 CPUs stock %ss / tree %ss; B %d dirs restore RSS stock %s / tree %s MiB' + extra_text.replace('%', '%%') + '; C %d GiB file restore RSS stock %s / tree %s MiB; D largest index %s blobs: backup RSS stock %s / tree %s MiB'
summary = summary % (
    index_files, a_rows['1 CPU | stock']['seconds_median'], a_rows['1 CPU | tree']['seconds_median'],
    a_rows['1 CPU | stock']['index_downloads_in_flight_max'], a_rows['1 CPU | tree']['index_downloads_in_flight_max'],
    a_rows['2 CPUs | stock']['seconds_median'], a_rows['2 CPUs | tree']['seconds_median'], b_rows['directories'],
    b_rows['stock']['rss_mib'], b_rows['tree']['rss_mib'], SPARSE_GIB, c_rows['default | stock']['rss_mib'], c_rows['default | tree']['rss_mib'],
    max(reached) if reached else 'none', d_rows[str(max(reached))]['stock']['rss_mib'] if reached else '-', d_rows[str(max(reached))]['tree']['rss_mib'] if reached else '-')
sys.exit(c.finish(summary))
