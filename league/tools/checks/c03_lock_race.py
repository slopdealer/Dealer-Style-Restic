#!/usr/bin/env python3
"""C3. Repair R1 end to end with real binaries, BEFORE (unrepaired tree) and AFTER (the tree under test).

P1 = `restic backup` through chkrest, which holds back only P1's lock-file upload (POST /locks/<id>) for D ms.
P2 = `restic forget <snapshot> --prune` on the same repository through the local path (fast, next to the storage).
P1 backs up the data of the snapshot P2 removes, so a P1 that keeps an index (and snapshot list) it read before P2's
prune deduplicates against deleted packs. Afterwards STOCK runs `check --read-data`.

  BROKEN   P1 exited 0 and stock check rejects the repository          (the silent data loss of finding R1)
  DAMAGED  P1 exited non-zero and stock check rejects the repository   (should never happen either)
  ok       stock check passes; who was refused (backup, prune, nobody) is counted separately

The bug needs P2 to pass its own lock checks before P1's lock file becomes visible and to finish inside P1's 200 ms
settle wait. Two start-time distributions are run for every delay D:
  uniform  P2 starts at a uniformly random moment between 1 s before P1 and 0.5 s after P1's lock lands
  aimed    P2 starts so that its second lock check falls within about +-100 ms of the moment P1's lock lands
           (start times are calibrated per binary from five solo runs of each role)
Same schedules for three arms: before (S019-merge), after (the tree), stock (control, fewer trials).

Second part: the S015 race repro (race-fix/REPORT.md): a stock backup loop beside N x `restore latest` and
N x `ls latest` by the arm; expected 0 failures."""
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
from chk import (QUICK, Check, Server, append, chkrest_bin, copy_repo, envint, must, prepare, prepare_before,  # noqa: E402
                 prepare_stock, restic, restic_env, rmtree, write_file)

c = Check('c03', 'R1 end to end: delayed lock upload against forget --prune, before and after; S015 repro')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
before = prepare_before()
chkrest_bin()
DELAYS = [int(x) for x in os.environ.get('CHK_C03_DELAYS', '300,700,1500,3000').split(',')]
N_UNIFORM = envint('CHK_C03_UNIFORM', 60)
N_AIMED = envint('CHK_C03_AIMED', 60)
WORKERS = envint('CHK_C03_WORKERS', 3)
KEEP_FILES = envint('CHK_C03_KEEP_FILES', 1500)
S015_N = envint('CHK_C03_S015_N', 100)
if QUICK:
    DELAYS, N_UNIFORM, N_AIMED, S015_N = [300, 1500], 3, 5, 8

template = c.work / 'template'
keep = c.work / 'keep'
rng0 = random.Random(3)
for i in range(KEEP_FILES):
    write_file(keep / ('d%02d' % (i % 20)) / ('k%04d.dat' % i), rng0.randbytes(rng0.randrange(500, 12000)))
must(restic(stock.bin, template, 'init', work=c.work / 'w-setup'))
must(restic(stock.bin, template, 'backup', '--no-cache', '--host', 'h', keep, work=c.work / 'w-setup'))


def new_trial_repo(wdir, seed):
    repo = wdir / 'repo'
    copy_repo(template, repo)
    src = wdir / 'src'
    rmtree(src)
    r = random.Random(seed)
    write_file(src / 'g', r.randbytes(2 << 20))
    for i in range(30):
        write_file(src / 'few' / ('s%02d' % i), r.randbytes(r.randrange(100, 9000)))
    out = must(restic(stock.bin, repo, 'backup', '--no-cache', '--host', 'h', '--json', src, work=wdir), 'setup backup')
    old = None
    for line in out.out.splitlines():
        if '"snapshot_id"' in line:
            old = json.loads(line)['snapshot_id']
    if not old:
        raise RuntimeError('no snapshot id in: ' + out.out[-300:])
    return repo, src, old


def calibrate(arm, binary):
    """Solo timings of both roles for one binary: when P1's lock POST reaches the server, when P2 creates and
    removes its lock file."""
    wdir = c.work / ('cal-' + arm)
    env = restic_env(wdir)
    created, removed, post = [], [], []
    for i in range(5):
        repo, src, old = new_trial_repo(wdir, 9000 + i)
        locks = repo / 'locks'
        began = time.monotonic()
        p = subprocess.Popen([str(binary), '-r', str(repo), 'forget', old, '--prune', '--no-cache'], env=env,
                             stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        t_created = t_removed = None
        while p.poll() is None:
            n = len(os.listdir(locks))
            if t_created is None and n:
                t_created = time.monotonic() - began
            elif t_created is not None and t_removed is None and n == 0:
                t_removed = time.monotonic() - began
            time.sleep(0.0005)
        total = time.monotonic() - began
        if p.returncode != 0 or t_created is None:
            raise RuntimeError('calibration: solo forget --prune failed for ' + arm)
        created.append(t_created)
        removed.append(t_removed or total)
        repo, src, old = new_trial_repo(wdir, 9100 + i)
        log = wdir / 'cal.log'
        with Server(repo, log=log) as srv:
            wall = time.time()
            r = restic(binary, srv.url, 'backup', '--no-cache', '--host', 'h', src, work=wdir)
        if r.rc != 0:
            raise RuntimeError('calibration: solo backup failed for %s: %s' % (arm, r.brief()))
        for line in log.read_text().splitlines():
            event = json.loads(line)
            if event['method'] == 'POST' and event['path'].startswith('/locks/'):
                post.append(event['abs_us'] / 1e6 - wall)
                break
    cal = {'p2_lock_created': statistics.median(created), 'p2_lock_removed': statistics.median(removed),
           'p1_lock_post': statistics.median(post)}
    cal['p2_work_after_confirmation'] = cal['p2_lock_removed'] - cal['p2_lock_created'] - 0.2
    c.log('calibration %s: %s' % (arm, json.dumps({k: round(v, 3) for k, v in cal.items()})))
    return cal


def launch_at(cmd, env, at, out_path):
    while True:
        left = at - time.monotonic()
        if left <= 0:
            break
        time.sleep(min(left, 0.01) if left > 0.002 else 0)
    f = open(out_path, 'wb')
    return subprocess.Popen(cmd, env=env, stdin=subprocess.DEVNULL, stdout=f, stderr=subprocess.STDOUT), f


def trial(arm, binary, delay, mode, cal, wdir, seed):
    r = random.Random(seed)
    repo, src, old = new_trial_repo(wdir, seed)
    lands = cal['p1_lock_post'] + delay / 1000.0          # P1's lock becomes visible, seconds after P1 starts
    if mode == 'aimed':
        offset = lands - cal['p2_lock_created'] - 0.2 - r.uniform(-0.10, 0.15)
    else:
        offset = r.uniform(-1.0, lands + 0.5)
    env = restic_env(wdir)
    srv = Server(repo, hold_lock_post_ms=delay, log=wdir / 'server.log')
    cmd1 = [str(binary), '-r', srv.url, 'backup', '--no-cache', '--host', 'h', str(src)]
    cmd2 = [str(binary), '-r', str(repo), 'forget', old, '--prune', '--no-cache']
    zero = time.monotonic() + 0.03 + max(0.0, -offset)
    order = [(cmd1, zero, wdir / 'p1.txt'), (cmd2, zero + offset, wdir / 'p2.txt')]
    if offset < 0:
        order.reverse()
    procs = [launch_at(cmd, env, at, out) for cmd, at, out in order]
    if offset < 0:
        procs.reverse()
    rcs = []
    for p, f in procs:
        try:
            rcs.append(p.wait(timeout=240))
        except subprocess.TimeoutExpired:
            p.kill()
            p.wait()
            rcs.append(-9)
        f.close()
    srv.stop()
    check = restic(stock.bin, repo, 'check', '--read-data', '--no-cache', work=wdir, timeout=600)
    if -9 in rcs:
        category = 'HANG'
    elif check.rc != 0:
        category = 'BROKEN' if rcs[0] == 0 else 'DAMAGED'
    else:
        category = {(True, True): 'ok_both', (False, True): 'ok_backup_refused', (True, False): 'ok_prune_refused',
                    (False, False): 'ok_both_refused'}[(rcs[0] == 0, rcs[1] == 0)]
    record = {'arm': arm, 'delay_ms': delay, 'mode': mode, 'offset_s': round(offset, 3), 'backup_rc': rcs[0],
              'prune_rc': rcs[1], 'check_rc': check.rc, 'category': category, 'seed': seed}
    if category in ('BROKEN', 'DAMAGED', 'HANG'):
        with keep_lock:
            n = kept.get((arm, category), 0)
            kept[(arm, category)] = n + 1
        if n < 4:
            ev = c.res / 'evidence' / ('%s-%s-%dms-%s-seed%d' % (arm, category, delay, mode, seed))
            ev.mkdir(parents=True, exist_ok=True)
            for name in ('p1.txt', 'p2.txt', 'server.log'):
                if (wdir / name).is_file():
                    (ev / name).write_bytes((wdir / name).read_bytes())
            (ev / 'stock-check.txt').write_text(check.cmd + '\n' + check.out + check.err)
            (ev / 'record.json').write_text(json.dumps(record, indent=1) + '\n')
            record['evidence'] = str(ev)
    return record


keep_lock = threading.Lock()
kept = {}
arms = [('before', before.bin, 1.0), ('after', cand.bin, 1.0), ('stock', stock.bin, 0.25)]
cals = {arm: calibrate(arm, binary) for arm, binary, _ in arms}
c.numbers['calibration_seconds'] = cals
jobs = []
seed = 1000
for arm, binary, share in arms:
    for delay in DELAYS:
        for mode, n in (('uniform', N_UNIFORM), ('aimed', N_AIMED)):
            for i in range(max(1, int(n * share))):
                seed += 1
                jobs.append((arm, binary, delay, mode, seed))
random.Random(7).shuffle(jobs)       # the arms take turns, so box conditions are shared
c.status('lock race trials', 0, len(jobs))
c.log('%d trials: delays %s ms, uniform %d + aimed %d per delay and arm (stock a quarter), %d workers' % (
    len(jobs), DELAYS, N_UNIFORM, N_AIMED, WORKERS))
records = []
queue_lock = threading.Lock()
position = [0]
errors = []


def worker(index):
    wdir = c.work / ('w%d' % index)
    wdir.mkdir(exist_ok=True)
    while True:
        with queue_lock:
            if position[0] >= len(jobs):
                return
            job = jobs[position[0]]
            position[0] += 1
        arm, binary, delay, mode, job_seed = job
        try:
            record = trial(arm, binary, delay, mode, cals[arm], wdir, job_seed)
        except Exception as exc:  # a broken trial must not stop the series
            record = {'arm': arm, 'delay_ms': delay, 'mode': mode, 'category': 'HARNESS_ERROR', 'error': repr(exc)[:300], 'seed': job_seed}
            errors.append(record)
        with queue_lock:
            records.append(record)
            append(c.res / 'trials.jsonl', json.dumps(record))
            if record['category'] in ('BROKEN', 'DAMAGED', 'HANG', 'HARNESS_ERROR'):
                c.log('trial %s %d ms %s offset=%s -> %s' % (arm, delay, mode, record.get('offset_s'), record['category']))
        c.tick()


threads = [threading.Thread(target=worker, args=(i,)) for i in range(WORKERS)]
for t in threads:
    t.start()
for t in threads:
    t.join()

table = {}
for record in records:
    key = (record['arm'], record['delay_ms'], record['mode'])
    table.setdefault(key, {}).setdefault(record['category'], 0)
    table[key][record['category']] += 1
lines = ['| arm | lock delay ms | P2 start | trials | BROKEN | DAMAGED | HANG | ok, both ran | ok, backup refused | ok, prune refused | other |',
         '|---|---|---|---|---|---|---|---|---|---|---|']
totals = {}
for (arm, delay, mode), cats in sorted(table.items()):
    n = sum(cats.values())
    other = n - sum(cats.get(k, 0) for k in ('BROKEN', 'DAMAGED', 'HANG', 'ok_both', 'ok_backup_refused', 'ok_prune_refused'))
    lines.append('| %s | %d | %s | %d | %d | %d | %d | %d | %d | %d | %d |' % (
        arm, delay, mode, n, cats.get('BROKEN', 0), cats.get('DAMAGED', 0), cats.get('HANG', 0), cats.get('ok_both', 0),
        cats.get('ok_backup_refused', 0), cats.get('ok_prune_refused', 0), other))
    t = totals.setdefault(arm, {'trials': 0, 'BROKEN': 0, 'DAMAGED': 0, 'HANG': 0, 'ok_both': 0, 'uniform_trials': 0,
                                'uniform_BROKEN': 0, 'aimed_trials': 0, 'aimed_BROKEN': 0})
    t['trials'] += n
    for k in ('BROKEN', 'DAMAGED', 'HANG', 'ok_both'):
        t[k] += cats.get(k, 0)
    t[mode + '_trials'] += n
    t[mode + '_BROKEN'] += cats.get('BROKEN', 0)
(c.res / 'r1-table.md').write_text('\n'.join(lines) + '\n')
c.numbers['r1'] = totals
c.numbers['r1_by_cell'] = {'%s/%d/%s' % k: v for k, v in sorted(table.items())}
for line in lines:
    c.log(line)
if errors:
    c.note('%d trials stopped with a harness error (first: %s)' % (len(errors), errors[0].get('error')))
after = totals.get('after', {})
for category in ('BROKEN', 'DAMAGED', 'HANG'):
    if after.get(category):
        first = next(r for r in records if r['arm'] == 'after' and r['category'] == category)
        st = totals.get('stock', {}).get(category, 0)
        c.fail('R1 race: %d of %d trials on the tree under test ended %s' % (after[category], after['trials'], category),
               evidence=first.get('evidence', c.res / 'trials.jsonl'),
               stock='stock control: %d of %d' % (st, totals.get('stock', {}).get('trials', 0)),
               repro='python3 c03_lock_race.py <tree> <label>   (trial seed %d, delay %d ms, %s)' % (first['seed'], first['delay_ms'], first['mode']))
if totals.get('before', {}).get('BROKEN', 0) == 0:
    c.note('the unrepaired tree produced no BROKEN snapshot in %d trials: the schedule did not reach the window, the before/after comparison is not conclusive' % totals.get('before', {}).get('trials', 0))
if totals.get('stock', {}).get('BROKEN', 0):
    c.note('stock itself produced BROKEN snapshots: %d' % totals['stock']['BROKEN'])

# ---------------------------------------------------------------- S015 race repro (race-fix/repro.sh)

def s015(arm, binary, n):
    w = c.work / ('s015-' + arm)
    repo, data = w / 'repo', w / 'data'
    env = {'GOMAXPROCS': '4'}
    must(restic(stock.bin, repo, 'init', work=w / 'bg', env=env))
    write_file(data / 'f0', os.urandom(200000))
    must(restic(stock.bin, repo, 'backup', '-q', data, work=w / 'bg', env=env))
    stop = threading.Event()

    def loop():
        i = 0
        while not stop.is_set():
            i += 1
            write_file(data / ('f%d' % (i % 50)), os.urandom(50000))
            restic(stock.bin, repo, 'backup', '-q', '--no-cache', data, work=w / 'bg', env=env)
    th = threading.Thread(target=loop)
    th.start()
    restore_fail = ls_fail = 0
    for i in range(n):
        rmtree(w / 'rt')
        r = restic(binary, repo, 'restore', 'latest', '--no-cache', '-q', '--target', w / 'rt', work=w / 'fg', env=env)
        if r.rc:
            restore_fail += 1
            append(c.res / 's015-errors.log', '%s restore %d: %s' % (arm, i, r.brief(300)))
        r = restic(binary, repo, 'ls', 'latest', '--no-cache', '-q', work=w / 'fg', env=env)
        if r.rc:
            ls_fail += 1
            append(c.res / 's015-errors.log', '%s ls %d: %s' % (arm, i, r.brief(300)))
    stop.set()
    th.join()
    snaps = len(json.loads(must(restic(stock.bin, repo, 'snapshots', '--no-cache', '--json', work=w / 'bg')).out))
    result = {'n': n, 'restore_failed': restore_fail, 'ls_failed': ls_fail, 'concurrent_snapshots': snaps}
    c.log('S015 repro %s: %s' % (arm, json.dumps(result)))
    return result


c.status('S015 repro', 0, 3)
s015_results = {}
for arm, binary in (('after', cand.bin), ('before', before.bin), ('stock', stock.bin)):
    s015_results[arm] = s015(arm, binary, S015_N)
    c.tick()
c.numbers['s015_repro'] = s015_results
a = s015_results['after']
if a['restore_failed'] or a['ls_failed']:
    s = s015_results['stock']
    c.fail('S015 race repro: restore latest failed %d/%d, ls latest failed %d/%d beside a stock backup loop' % (
        a['restore_failed'], a['n'], a['ls_failed'], a['n']), evidence=c.res / 's015-errors.log',
        stock='stock: restore %d/%d, ls %d/%d' % (s['restore_failed'], s['n'], s['ls_failed'], s['n']),
        repro='python3 c03_lock_race.py <tree> <label>   (second part)')
b = totals.get('before', {})
summary = 'R1 BROKEN snapshots: before %d/%d (uniform %d/%d, aimed %d/%d), after %d/%d, stock %d/%d; after DAMAGED %d HANG %d; S015 repro after: restore %d/%d ls %d/%d failed' % (
    b.get('BROKEN', 0), b.get('trials', 0), b.get('uniform_BROKEN', 0), b.get('uniform_trials', 0), b.get('aimed_BROKEN', 0),
    b.get('aimed_trials', 0), after.get('BROKEN', 0), after.get('trials', 0), totals.get('stock', {}).get('BROKEN', 0),
    totals.get('stock', {}).get('trials', 0), after.get('DAMAGED', 0), after.get('HANG', 0), a['restore_failed'], a['n'],
    a['ls_failed'], a['n'])
sys.exit(c.finish(summary))
