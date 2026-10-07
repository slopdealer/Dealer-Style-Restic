#!/usr/bin/env python3
"""X10 (diagnostic for C10 part B, finding M22). How much memory does a restore keep per directory, and where?

Source trees of about 1.01 and 2.02 million directories (t0000/m00/d00: three levels, names of 5 and 3 bytes, a
file in every twentieth top directory), backed up by stock. Per size and binary (stock, the tree, the unrepaired
tree): `restore latest` into an empty target, peak RSS read through lib/rusage_exec.py (a child's ru_maxrss
otherwise starts at the size of the harness process).
  bytes per directory = (peak RSS at 2.02M - peak RSS at 1.01M) / 1,010,100, per binary, and the same less stock's
Heap profile: the tree (and stock, as the baseline) built with the `profile` build tag, `restore --mem-profile DIR`
at the larger size: the profile restic writes when the command ends; `go tool pprof -top` for in-use and for
allocated space. In-use space is what the last garbage collection before the end found alive."""
import json
import os
import re
import subprocess
import sys
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import (BUILD, QUICK, TAGS, Check, go_env, must, prepare, prepare_before, prepare_stock, restic, restic_env, rmtree,  # noqa: E402
                 run, run_rusage, write_file)

c = Check('x10', 'restore memory per directory (M22): RSS at two sizes, heap profile at the end of the restore')
stock_built = prepare_stock()
tree_built = prepare(c.label, c.tree)
arms = [('stock', stock_built), ('tree', tree_built)]
try:
    arms.append(('unrepaired', prepare_before()))
except Exception as exc:
    c.note('the unrepaired tree could not be built: %r' % (exc,))
TOPS = [100, 200] if not QUICK else [2, 4]
ws = c.work / 'ws'


def profile_binary(built):
    out = BUILD / built.name / 'restic-profile'
    rc, o, e, _ = run(['go', 'build', '-trimpath', '-buildvcs=false', '-tags', TAGS + ' profile', '-o', str(out), './cmd/restic'],
                      env=go_env(), cwd=built.src, timeout=1800)
    if rc != 0:
        raise RuntimeError('profile build of %s failed: %s' % (built.name, e[-800:]))
    return out


def make_tree(root, tops):
    made = 0
    for i in range(tops):
        for j in range(100):
            base = root / ('t%04d' % i) / ('m%02d' % j)
            os.makedirs(base)
            for k in range(100):
                os.mkdir(base / ('d%02d' % k))
            made += 101
        if i % 20 == 0:
            write_file(root / ('t%04d' % i) / 'file.txt', b'a file among the directories\n')
    return made + tops


c.status('sizes', 0, len(TOPS) * len(arms) + 3)
rss = {}
dirs = {}
repo = None
for tops in TOPS:
    src = c.work / 'src'
    rmtree(src)
    dirs[tops] = make_tree(src, tops)
    repo = c.work / 'repo'
    rmtree(repo)
    must(restic(stock_built.bin, repo, 'init', work=ws))
    must(restic(stock_built.bin, repo, 'backup', '--no-cache', '--host', 'h', src.name, work=ws, cwd=src.parent, timeout=7200))
    for arm, built in arms:
        target = c.work / 'target'
        rmtree(target)
        res = run_rusage([str(built.bin), '-r', str(repo), 'restore', 'latest', '--no-cache', '--target', str(target)], env=restic_env(ws), timeout=7200)
        rss[(arm, tops)] = {'rc': res['rc'], 'rss_mib': round(res['rss_kib'] / 1024, 1), 'seconds': round(res['seconds'], 1)}
        c.log('%d directories, restore by %s: %s' % (dirs[tops], arm, json.dumps(rss[(arm, tops)])))
        rmtree(target)
        c.tick()
small, large = TOPS
table = {}
for arm, _ in arms:
    a, b = rss[(arm, small)], rss[(arm, large)]
    table[arm] = {'rss_mib_at_%d' % dirs[small]: a['rss_mib'], 'rss_mib_at_%d' % dirs[large]: b['rss_mib'],
                  'bytes_per_directory': round((b['rss_mib'] - a['rss_mib']) * 1048576 / (dirs[large] - dirs[small]), 1),
                  'seconds_at_%d' % dirs[large]: b['seconds']}
for arm in table:
    table[arm]['bytes_per_directory_above_stock'] = round(table[arm]['bytes_per_directory'] - table['stock']['bytes_per_directory'], 1)
c.numbers['rss'] = table
c.log(json.dumps(table))

# ---- heap profiles at the larger size (the repository of the last loop is still there)
profiles = {}
for arm, built in arms[:2][::-1]:          # the tree first, then stock as the baseline
    try:
        binary = profile_binary(built)
    except Exception as exc:
        c.note('no heap profile for %s: %r' % (arm, exc))
        continue
    pdir = c.res / ('profile-' + arm)
    rmtree(pdir)
    pdir.mkdir(parents=True)
    target = c.work / 'target'
    rmtree(target)
    res = run_rusage([str(binary), '-r', str(repo), 'restore', 'latest', '--no-cache', '--target', str(target), '--mem-profile', str(pdir)],
                     env=restic_env(ws), timeout=7200)
    rmtree(target)
    prof = pdir / 'mem.pprof'
    info = {'rc': res['rc'], 'rss_mib': round(res['rss_kib'] / 1024, 1), 'profile': str(prof) if prof.is_file() else None}
    for index in ('inuse_space', 'alloc_space'):
        if not prof.is_file():
            break
        rc, o, e, _ = run(['go', 'tool', 'pprof', '-top', '-nodecount=25', '-sample_index=' + index, str(binary), str(prof)], env=go_env(), timeout=600)
        (pdir / ('top-%s.txt' % index)).write_text(o + e)
        rc, o2, e2, _ = run(['go', 'tool', 'pprof', '-top', '-cum', '-nodecount=30', '-sample_index=' + index, str(binary), str(prof)], env=go_env(), timeout=600)
        (pdir / ('top-cum-%s.txt' % index)).write_text(o2 + e2)
        total = re.search(r'Showing nodes accounting for .*?, .*? of ([0-9.]+)([kMG]?B) total', o)
        info[index + '_total'] = (total.group(1) + ' ' + total.group(2)) if total else None
        info[index + '_top'] = [' '.join(line.split()) for line in o.splitlines() if re.match(r'^\s*[0-9.]+[kMG]?B', line)][:8]
    profiles[arm] = info
    c.log('heap profile of %s at %d directories: %s' % (arm, dirs[large], json.dumps(info)[:1500]))
    c.tick()
c.numbers['profiles'] = profiles
c.numbers['directories'] = {str(k): v for k, v in dirs.items()}
t = table['tree']
summary = 'restore peak RSS at %d / %d directories: stock %s / %s MiB, tree %s / %s MiB%s; per directory: stock %s bytes, tree %s bytes (%s above stock)' % (
    dirs[small], dirs[large], table['stock']['rss_mib_at_%d' % dirs[small]], table['stock']['rss_mib_at_%d' % dirs[large]],
    t['rss_mib_at_%d' % dirs[small]], t['rss_mib_at_%d' % dirs[large]],
    (', unrepaired %s / %s MiB' % (table['unrepaired']['rss_mib_at_%d' % dirs[small]], table['unrepaired']['rss_mib_at_%d' % dirs[large]])) if 'unrepaired' in table else '',
    table['stock']['bytes_per_directory'], t['bytes_per_directory'], t['bytes_per_directory_above_stock'])
sys.exit(c.finish(summary, verdict='PASS'))
