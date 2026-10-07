#!/usr/bin/env python3
"""C12. kill -9 during a prune that has to list tens of thousands of blobs in the packs it repacks (in the final
tree: the repack route table; review of S020, finding 10 and NEEDS-RUN 9).

Starting repository: lib/bigindex.py, variant `routes`: a copy of the judge's 1.5M-blob fixture plus partly used packs
with about 24,500 index entries (more than the 8,192 of the bounded collector, inside the 65,536 slots of the route
table). CHK_C12_VARIANT=fallback uses a one-third-used ballast pack instead (187,500 entries).
Per trial, on a fresh copy: `prune --max-unused 0` is killed with SIGKILL at a random moment; then
  1. stock `unlock`, stock `check --read-data` must pass
  2. the same binary prunes again, completely: exit 0
  3. stock `check --read-data` must pass, and `ls --json` of every snapshot (read by stock) must equal the listing
     taken before the first prune
The same with stock as the killed binary is the control (a quarter of the trials). Same expectations as C4."""
import json
import os
import random
import signal
import statistics
import subprocess
import sys
import threading
import time
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
import bigindex  # noqa: E402
from chk import (QUICK, Check, append, chktool_bin, copy_repo, envint, prepare, prepare_stock, repo_files, restic,  # noqa: E402
                 restic_env)

c = Check('c12', 'kill -9 during prune on the large-index repack path, then stock check and a second prune')
if not bigindex.available():
    c.note('the big-index fixture is not readable at %s' % bigindex.FIXTURE)
    sys.exit(c.finish('big-index fixture not available', verdict='SKIPPED'))
stock = prepare_stock()
cand = prepare(c.label, c.tree)
tool = chktool_bin()
N = envint('CHK_C12_TRIALS', 40)
WORKERS = envint('CHK_C12_WORKERS', 3)
VARIANT = os.environ.get('CHK_C12_VARIANT', 'routes')
if QUICK:
    N = 3
base = bigindex.build(c.work, stock.bin, tool, VARIANT, 'base')
env = base['env']
c.numbers['base'] = {key: base[key] for key in ('variant', 'small_files', 'ballast_partial', 'entries_in_partly_used_packs', 'packs', 'blobs')}
c.log('starting repository: %s' % json.dumps(c.numbers['base']))
baseline = bigindex.listings(stock.bin, base['repo'], base['snapshots'], c.work / 'ws', env)
PRUNE = ['prune', '--max-unused', '0', '--no-cache', '--pack-size', '4']


def calibrate(arm, binary):
    wdir = c.work / ('cal-' + arm)
    wdir.mkdir(exist_ok=True)
    times, opens = [], []
    for i in range(3):
        copy_repo(base['repo'], wdir / 'repo')
        opens.append(restic(binary, wdir / 'repo', 'cat', 'config', '--no-lock', work=wdir, env=env).secs)
        r = restic(binary, wdir / 'repo', *PRUNE, work=wdir, env=env, timeout=3600)
        if r.rc != 0:
            raise RuntimeError('calibration prune by %s exits %d: %s' % (arm, r.rc, r.brief(400)))
        times.append(r.secs)
    cal = {'open': statistics.median(opens), 'prune': statistics.median(times)}
    c.log('calibration %s (seconds): %s' % (arm, json.dumps({k: round(v, 2) for k, v in cal.items()})))
    return cal


cals = {'tree': calibrate('tree', cand.bin), 'stock': calibrate('stock', stock.bin)}
c.numbers['calibration_seconds'] = cals
jobs = [('tree', cand.bin, 12000 + i) for i in range(N)] + [('stock', stock.bin, 13000 + i) for i in range(max(1, N // 4))]
random.Random(12).shuffle(jobs)
c.status('trials', 0, len(jobs))
lock = threading.Lock()
position = [0]
records = []
kept = {}


def trial(arm, binary, seed, wdir):
    r = random.Random(seed)
    repo = wdir / 'repo'
    copy_repo(base['repo'], repo)
    at = r.uniform(cals[arm]['open'] * 0.7, cals[arm]['prune'] * 1.05)
    record = {'arm': arm, 'seed': seed, 'kill_after_s': round(at, 3)}
    problems = []
    with open(wdir / 'killed-prune.txt', 'wb') as out:
        p = subprocess.Popen([str(binary), '-r', str(repo)] + PRUNE, env=restic_env(wdir, env), stdin=subprocess.DEVNULL, stdout=out, stderr=subprocess.STDOUT)
        try:
            p.wait(timeout=at)
            record['finished_before_kill'] = True
        except subprocess.TimeoutExpired:
            p.send_signal(signal.SIGKILL)
            p.wait()
        record['rc'] = p.returncode
    record['packs_after_kill'] = len(repo_files(repo, 'data'))
    record['index_files_after_kill'] = len(repo_files(repo, 'index'))
    restic(stock.bin, repo, 'unlock', work=wdir, env=env)
    check = restic(stock.bin, repo, 'check', '--read-data', '--no-cache', work=wdir, env=env, timeout=3600)
    (wdir / 'check-after-kill.txt').write_text(check.cmd + '\n' + check.out + check.err)
    record['check_rc'] = check.rc
    if check.rc != 0:
        problems.append('stock check --read-data after the kill: ' + check.brief(500))
    again = restic(binary, repo, *PRUNE, work=wdir, env=env, timeout=3600)
    (wdir / 'second-prune.txt').write_text(again.cmd + '\n' + again.out + again.err)
    record['second_prune_rc'] = again.rc
    if again.rc != 0:
        problems.append('second prune exits %d: %s' % (again.rc, again.brief(400)))
    check2 = restic(stock.bin, repo, 'check', '--read-data', '--no-cache', work=wdir, env=env, timeout=3600)
    record['check2_rc'] = check2.rc
    if check2.rc != 0:
        (wdir / 'check-after-second-prune.txt').write_text(check2.cmd + '\n' + check2.out + check2.err)
        problems.append('stock check --read-data after the second prune: ' + check2.brief(500))
    if bigindex.listings(stock.bin, repo, base['snapshots'], wdir, env) != baseline:
        problems.append('snapshot contents (ls --json by stock) differ from before the prune')
    record['packs_final'] = len(repo_files(repo, 'data'))
    record['problems'] = problems
    return record


def worker(index):
    wdir = c.work / ('w%d' % index)
    wdir.mkdir(exist_ok=True)
    while True:
        with lock:
            if position[0] >= len(jobs):
                return
            arm, binary, seed = jobs[position[0]]
            position[0] += 1
        try:
            record = trial(arm, binary, seed, wdir)
        except Exception as exc:
            record = {'arm': arm, 'seed': seed, 'problems': [], 'harness_error': repr(exc)[:400]}
        if record['problems']:
            with lock:
                n = kept.get(arm, 0)
                kept[arm] = n + 1
            if n < 3:
                ev = c.res / 'evidence' / ('%s-seed%d' % (arm, seed))
                ev.mkdir(parents=True, exist_ok=True)
                for path in wdir.glob('*.txt'):
                    (ev / path.name).write_bytes(path.read_bytes())
                (ev / 'record.json').write_text(json.dumps(record, indent=1) + '\n')
                record['evidence'] = str(ev)
        with lock:
            records.append(record)
            append(c.res / 'trials.jsonl', json.dumps(record))
        if record['problems'] or record.get('harness_error'):
            c.log('trial %s seed %d: %s' % (arm, seed, (record['problems'] or [record.get('harness_error')])[0][:300]))
        c.tick()


threads = [threading.Thread(target=worker, args=(i,)) for i in range(WORKERS)]
for t in threads:
    t.start()
for t in threads:
    t.join()
totals = {}
for rec in records:
    t = totals.setdefault(rec['arm'], {'trials': 0, 'killed_while_running': 0, 'problems': 0, 'harness_errors': 0, 'check_failed_after_kill': 0})
    t['trials'] += 1
    t['killed_while_running'] += 0 if rec.get('finished_before_kill') or rec.get('harness_error') else 1
    t['problems'] += 1 if rec['problems'] else 0
    t['harness_errors'] += 1 if rec.get('harness_error') else 0
    t['check_failed_after_kill'] += 1 if rec.get('check_rc') not in (0, None) else 0
c.numbers['totals'] = totals
c.log(json.dumps(totals))
tree = totals.get('tree', {})
if tree.get('problems'):
    first = next(r for r in records if r['arm'] == 'tree' and r['problems'])
    s = totals.get('stock', {})
    c.fail('kill -9 during prune (large index): %d of %d trials with a problem; first: %s' % (tree['problems'], tree['trials'], first['problems'][0][:400]),
           evidence=first.get('evidence', c.res / 'trials.jsonl'), stock='stock killed the same way: %d of %d trials with a problem' % (s.get('problems', 0), s.get('trials', 0)),
           repro='python3 c12_kill_prune_routes.py <tree> <label>   (trial seed %d, kill after %s s)' % (first['seed'], first['kill_after_s']))
if tree.get('harness_errors'):
    c.note('%d trials stopped with a harness error' % tree['harness_errors'])
s = totals.get('stock', {})
summary = '%d kill -9 trials on the tree (%d hit a running prune; %d index entries in the packs to repack): %d with a problem; stock control %d trials, %d with a problem' % (
    tree.get('trials', 0), tree.get('killed_while_running', 0), base['entries_in_partly_used_packs'], tree.get('problems', 0), s.get('trials', 0), s.get('problems', 0))
sys.exit(c.finish(summary))
