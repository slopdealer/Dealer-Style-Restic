#!/usr/bin/env python3
"""X11 (diagnostic for C10 part B). Wall time and peak RSS of restores of many directories, order effects removed.

C10 restores binary after binary (stock, then the tree, ...), so anything that changes while the check runs (what
the file system still has to do for the target that was just removed, cache state) is charged to the later binaries.
Here, per size, every binary restores the same snapshot in rotated order (round 1: S T X, round 2: T X S, round 3:
X S T), the old target is removed and the file system synced (`sync -f`) before every restore, once with all the
check CPUs and once pinned to four cores (the judge's lanes have four).
Sizes (CHK_X11_TOPS, default 60,100,200 top directories = 606,060 / 1,010,100 / 2,020,200 directories with 3 / 5 / 10
files) and rounds per size (CHK_X11_ROUNDS, default 3,3,2). Per restore: wall seconds, user and system CPU seconds,
peak RSS (read through the helper process). Binaries: stock, the tree, and the trees in CHK_EXTRA_TREES.
Run it alone on the check CPUs."""
import json
import os
import statistics
import subprocess
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import CPUS, QUICK, Check, must, prepare, prepare_stock, restic, restic_env, rmtree, run_rusage, write_file  # noqa: E402

c = Check('x11', 'restores of 0.6, 1.0 and 2.0 million directories: wall time and peak RSS in rotated order, all CPUs and four cores')
arms = [('stock', prepare_stock().bin), ('tree', prepare(c.label, c.tree).bin)]
for item in [x for x in os.environ.get('CHK_EXTRA_TREES', '').split(',') if '=' in x]:
    name, path = item.split('=', 1)
    try:
        arms.append((name, prepare('_x_' + name, path).bin))
    except Exception as exc:
        c.note('%s could not be built: %r' % (name, exc))
TOPS = [int(x) for x in os.environ.get('CHK_X11_TOPS', '60,100,200').split(',')]
ROUNDS = [int(x) for x in os.environ.get('CHK_X11_ROUNDS', '3,3,2').split(',')]
if QUICK:
    TOPS, ROUNDS = [2], [1]
ROUNDS = (ROUNDS + [ROUNDS[-1]] * len(TOPS))[:len(TOPS)]
ws = c.work / 'ws'
four = [x for x in CPUS if x < 48][:4]
SETS = os.environ.get('CHK_X11_SETS', 'all,4').split(',')   # CHK_X11_SETS=all: only the run with all the check CPUs
CPU_SETS = [s for key, s in (('all', ('all %d check CPUs' % len(CPUS), None)), ('4', ('4 cores', four))) if key in SETS]
c.status('restores', 0, sum(len(CPU_SETS) * r * len(arms) for r in ROUNDS))
rows = []
table = {}
lines = ['| directories | CPUs | binary | wall seconds per round | median s | against stock | CPU s (median) | peak RSS MiB (max) |', '|---|---|---|---|---|---|---|---|']
parts = []
for tops, rounds in zip(TOPS, ROUNDS):
    src = c.work / 'src'
    rmtree(src)
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
    repo = c.work / 'repo'
    rmtree(repo)
    must(restic(arms[0][1], repo, 'init', work=ws))
    must(restic(arms[0][1], repo, 'backup', '--no-cache', '--host', 'h', src.name, work=ws, cwd=src.parent, timeout=7200))
    rmtree(src)
    c.log('%d directories and %d files: snapshot written by stock' % (dirs, files))
    for title, cpus in CPU_SETS:
        for n in range(rounds):
            order = arms[n % len(arms):] + arms[:n % len(arms)]
            for arm, binary in order:
                target = c.work / 'target'
                rmtree(target)
                subprocess.call(['sync', '-f', str(c.work)])
                cmd = [str(binary), '-r', str(repo), 'restore', 'latest', '--no-cache', '--target', str(target)]
                if cpus:
                    cmd = ['taskset', '-c', ','.join(map(str, cpus))] + cmd
                res = run_rusage(cmd, env=restic_env(ws), timeout=7200)
                row = {'directories': dirs, 'cpus': title, 'round': n + 1, 'binary': arm, 'position_in_round': [a for a, _ in order].index(arm) + 1,
                       'rc': res['rc'], 'seconds': round(res['seconds'], 1), 'user_s': round(res['user'], 1), 'sys_s': round(res['sys'], 1),
                       'rss_mib': round(res['rss_kib'] / 1024, 1)}
                rows.append(row)
                c.log(json.dumps(row))
                c.tick()
        stock_median = None
        for arm, _b in arms:
            mine = [r for r in rows if r['directories'] == dirs and r['cpus'] == title and r['binary'] == arm]
            cell = {'seconds': [r['seconds'] for r in mine], 'median_seconds': statistics.median([r['seconds'] for r in mine]),
                    'cpu_seconds_median': round(statistics.median([r['user_s'] + r['sys_s'] for r in mine]), 1), 'rss_mib_max': max(r['rss_mib'] for r in mine),
                    'exit_codes': sorted({r['rc'] for r in mine})}
            if arm == 'stock':
                stock_median = cell['median_seconds']
            cell['against_stock_percent'] = round(100.0 * (cell['median_seconds'] - stock_median) / stock_median, 1)
            table['%d | %s | %s' % (dirs, title, arm)] = cell
            lines.append('| %d | %s | %s | %s | %s | %+.1f%% | %s | %s |' % (dirs, title, arm, cell['seconds'], cell['median_seconds'], cell['against_stock_percent'],
                                                                         cell['cpu_seconds_median'], cell['rss_mib_max']))
        s, t = table['%d | %s | stock' % (dirs, title)], table['%d | %s | tree' % (dirs, title)]
        parts.append('%d dirs, %s: stock %s s / %s MiB, tree %s s / %s MiB (%+.1f%%)' % (dirs, title, s['median_seconds'], s['rss_mib_max'], t['median_seconds'],
                                                                                     t['rss_mib_max'], t['against_stock_percent']))
        (c.res / 'table.md').write_text('\n'.join(lines) + '\n')
        (c.res / 'runs.json').write_text(json.dumps(rows, indent=1) + '\n')
    rmtree(c.work / 'target')
    rmtree(repo)
for line in lines:
    c.log(line)
c.numbers['table'] = table
sys.exit(c.finish('rotated order; median wall time and peak RSS: ' + '; '.join(parts), verdict='PASS'))
