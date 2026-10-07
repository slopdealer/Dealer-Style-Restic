#!/usr/bin/env python3
"""X08 (the R2 step of C8 alone, repeated). `repair packs` with ONE backend connection must finish.

Repository: the one C8 builds (stock, two backups of the generated corpus), with no lock file in it (asserted).
Per binary (stock, the tree, the unrepaired tree) CHK_X08_RUNS times, each on a fresh copy and in its own working
directory: `restic -o local.connections=1 --pack-size 4 repair packs <the three largest data packs>` with a 60 s
limit (stock needs about 1.5 s); then stock `check --read-data`. Once per binary with two connections as the control.
The tree must finish every time with stock's exit code; the unrepaired tree is expected to hang (finding R2)."""
import json
import statistics
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import (QUICK, Check, copy_repo, envint, gen_corpus, index_packs, must, mutate_corpus, prepare, prepare_before,  # noqa: E402
                 prepare_stock, repo_files, restic, rmtree)

c = Check('x08', 'repair packs with one connection, repeated: stock, the tree, the unrepaired tree (R2)')
stock = prepare_stock()
arms = [('stock', stock.bin), ('tree', prepare(c.label, c.tree).bin)]
try:
    arms.append(('unrepaired', prepare_before().bin))
except Exception as exc:
    c.note('the unrepaired tree could not be built: %r' % (exc,))
N = envint('CHK_X08_RUNS', 6)
LIMIT = envint('CHK_X08_LIMIT_S', 60)
if QUICK:
    N = 2
ws = c.work / 'ws'
src = c.work / 'src'
gen_corpus(src, seed=8, small=250, text_files=3, text_mb=3, big=(7, 4), special=True)
repo = c.work / 'repo'
must(restic(stock.bin, repo, 'init', work=ws))
must(restic(stock.bin, repo, 'backup', '--no-cache', '--host', 'h', src, work=ws))
mutate_corpus(src, seed=81)
must(restic(stock.bin, repo, 'backup', '--no-cache', '--host', 'h', src, work=ws))
locks = repo_files(repo, 'locks')
if locks:
    raise RuntimeError('harness: the test repository holds lock files: %s' % locks)
packs = index_packs(stock.bin, repo, ws)
sizes = {p: sum(b['length'] for b in blobs) for p, blobs in packs.items() if all(b['type'] == 'data' for b in blobs)}
chosen = sorted(sizes, key=lambda p: -sizes[p])[:3]
c.numbers['pack_bytes'] = [sizes[p] for p in chosen]
c.log('three largest data packs: %s bytes' % c.numbers['pack_bytes'])
c.status('runs', 0, len(arms) * (N + 1))
rows = []
for arm, binary in arms:
    for n in range(N + 1):
        connections = 1 if n < N else 2
        copy = c.work / 'copy'
        copy_repo(repo, copy)
        cwd = c.work / 'cwd'             # repair packs saves copies of the packs into the current directory
        rmtree(cwd)
        cwd.mkdir()
        r = restic(binary, copy, '-o', 'local.connections=%d' % connections, '--pack-size', '4', 'repair', 'packs', *chosen, '--no-cache',
                   work=ws, timeout=LIMIT, cwd=cwd)
        hang = r.rc == -9
        check = None if hang else restic(stock.bin, copy, 'check', '--read-data', '--no-cache', work=ws)
        row = {'binary': arm, 'connections': connections, 'run': n + 1, 'rc': r.rc, 'seconds': round(r.secs, 1), 'hang': hang,
               'stock_check_rc': check.rc if check else None, 'last_line': (r.out + r.err).strip().splitlines()[-1][:160] if (r.out + r.err).strip() else ''}
        rows.append(row)
        (c.res / ('%s-conn%d-run%d.txt' % (arm, connections, n + 1))).write_text('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err))
        c.log(json.dumps(row))
        c.tick()
(c.res / 'runs.json').write_text(json.dumps(rows, indent=1) + '\n')
summary = {}
for arm, _ in arms:
    one = [r for r in rows if r['binary'] == arm and r['connections'] == 1]
    two = [r for r in rows if r['binary'] == arm and r['connections'] == 2]
    done = [r for r in one if not r['hang']]
    summary[arm] = {'runs': len(one), 'finished': len(done), 'hangs': len(one) - len(done), 'exit_codes': sorted({r['rc'] for r in one}),
                    'median_seconds_when_finished': round(statistics.median([r['seconds'] for r in done]), 1) if done else None,
                    'stock_check_failed': sum(1 for r in done if r['stock_check_rc'] != 0),
                    'with_two_connections': {'rc': two[0]['rc'], 'seconds': two[0]['seconds'], 'stock_check_rc': two[0]['stock_check_rc']} if two else None}
c.numbers['summary'] = summary
c.log(json.dumps(summary))
s, t = summary['stock'], summary['tree']
if s['exit_codes'] != [0]:
    c.fail('harness: stock `repair packs` with one connection does not exit 0: %s' % json.dumps(s), evidence=c.res / 'stock-conn1-run1.txt', stock=json.dumps(s))
if t['hangs'] or t['exit_codes'] != s['exit_codes'] or t['stock_check_failed']:
    c.fail('repair packs with -o local.connections=1: tree %s, stock %s' % (json.dumps(t), json.dumps(s)), evidence=c.res / 'tree-conn1-run1.txt',
           stock=json.dumps(s), repro='restic -r REPO -o local.connections=1 --pack-size 4 repair packs <three largest data packs>')
u = summary.get('unrepaired')
if u and not u['hangs']:
    c.note('the unrepaired tree did not hang in %d runs (finding R2 not reproduced here)' % u['runs'])
text = 'repair packs, one connection, %d runs each: stock finished %d (median %s s); tree finished %d, hangs %d (median %s s), stock check failed %d' % (
    N, s['finished'], s['median_seconds_when_finished'], t['finished'], t['hangs'], t['median_seconds_when_finished'], t['stock_check_failed'])
if u:
    text += '; unrepaired tree finished %d, hangs %d at the %d s limit (with two connections: rc %s in %s s)' % (
        u['finished'], u['hangs'], LIMIT, u['with_two_connections']['rc'], u['with_two_connections']['seconds'])
sys.exit(c.finish(text))
