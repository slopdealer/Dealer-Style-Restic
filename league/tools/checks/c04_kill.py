#!/usr/bin/env python3
"""C4. Kill tests and backend failures, real binaries.

Part 1: `kill -9` and SIGINT at a random moment during `backup`, `forget --keep-last 1 --prune --max-unused 0` and
`repair index --read-all-packs`, each many times, each on a fresh copy of the same repository. After every kill:
  1. the lock files left behind are counted, then STOCK `unlock`
  2. STOCK `check --read-data` must pass
  3. a complete backup by the same binary, `restore latest --verify`, and the restored tree must equal the source
     (content hashes and metadata); STOCK `check` once more
The same procedure with STOCK as the killed binary is the control (half as many trials).

Part 2: backend failures in the middle of a backup through chkrest: a burst of HTTP 500 answers, connections closed
without an answer, uploads and downloads cut half way (all transient: restic is expected to retry and succeed), and an
outage that does not end (SIGINT after 3 s: restic must exit, never hang). Then the same three steps as above."""
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
from chk import (QUICK, Check, Server, append, chkrest_bin, copy_repo, diff_manifests, envint, gen_corpus, manifest,  # noqa: E402
                 must, prepare, prepare_stock, repo_files, restic, restic_env, rmtree, write_file)

c = Check('c04', 'kill -9 / SIGINT during backup, prune, repair index; backend failures during backup')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
chkrest_bin()
N = envint('CHK_C04_TRIALS', 50)
N_FAULT = envint('CHK_C04_FAULT_TRIALS', 25)
WORKERS = envint('CHK_C04_WORKERS', 4)
if QUICK:
    N, N_FAULT = 3, 2
PACK = ['--pack-size', '4']

# ---- template: a repository with two snapshots; the first one holds data the second does not reference
src0 = c.work / 'src0'
gen_corpus(src0, seed=4, small=500, small_max=40_000, text_files=3, text_mb=3, big=(5, 7, 4), special=True)
templates = {}
for arm, binary in (('cand', cand.bin), ('stock', stock.bin)):
    w = c.work / ('setup-' + arm)
    repo = c.work / ('template-' + arm)
    src = c.work / ('src-' + arm)
    subprocess.check_call(['cp', '-a', str(src0), str(src)])
    must(restic(binary, repo, 'init', work=w))
    must(restic(binary, repo, 'backup', '--no-cache', '--host', 'h', *PACK, src, work=w))
    r = random.Random(41)
    doomed = sorted((src / 'small').rglob('*.dat'))
    for path in r.sample(doomed, len(doomed) // 2):
        path.unlink()
    (src / 'big' / 'b1.bin').unlink()
    write_file(src / 'big' / 'second.bin', r.randbytes(3 << 20))
    must(restic(binary, repo, 'backup', '--no-cache', '--host', 'h', *PACK, src, work=w))
    templates[arm] = (repo, src)
c.log('template repositories ready: %d packs' % len(repo_files(templates['cand'][0], 'data')))

OPS = {
    'backup': lambda repo, src: ['backup', '--no-cache', '--host', 'h', *PACK, str(src)],
    'prune': lambda repo, src: ['forget', '--keep-last', '1', '--prune', '--max-unused', '0', '--no-cache', *PACK],
    'repair-index': lambda repo, src: ['repair', 'index', '--read-all-packs', '--no-cache'],
}


def grow(src, seed):
    """New data for the backup that will be killed or disturbed."""
    r = random.Random(seed)
    for i in range(3):
        write_file(src / 'grow' / ('g%d.bin' % i), r.randbytes(14 << 20))
    for i in range(150):
        write_file(src / 'grow' / 'small' / ('s%03d' % i), r.randbytes(r.randrange(1, 30000)))


def fresh(arm, wdir, op, seed):
    repo, src = wdir / 'repo', wdir / 'src'
    wdir.mkdir(parents=True, exist_ok=True)
    copy_repo(templates[arm][0], repo)
    rmtree(src)
    subprocess.check_call(['cp', '-a', str(templates[arm][1]), str(src)])
    if op == 'backup':
        grow(src, seed)
    return repo, src


def calibrate(arm, binary):
    wdir = c.work / ('cal-' + arm)
    env = restic_env(wdir)
    cal = {}
    opens = []
    for i in range(3):
        repo, src = fresh(arm, wdir, 'backup', 1)
        opens.append(restic(binary, repo, 'cat', 'config', '--no-lock', work=wdir).secs)
    cal['open'] = statistics.median(opens)
    for op, make in OPS.items():
        times = []
        for i in range(3):
            repo, src = fresh(arm, wdir, op, 7 + i)
            began = time.monotonic()
            rc = subprocess.call([str(binary), '-r', str(repo)] + make(repo, src), env=env, stdin=subprocess.DEVNULL,
                                 stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
            times.append(time.monotonic() - began)
            if rc != 0:
                raise RuntimeError('calibration: %s %s exits %d' % (arm, op, rc))
        cal[op] = statistics.median(times)
    c.log('calibration %s (seconds): %s' % (arm, json.dumps({k: round(v, 3) for k, v in cal.items()})))
    return cal


def after_checks(arm, binary, repo, src, wdir, record):
    """Steps 1 to 3 of the docstring. Fills record; returns the list of problems found."""
    problems = []
    record['locks_left'] = len(repo_files(repo, 'locks'))
    unlock = restic(stock.bin, repo, 'unlock', work=wdir)
    if unlock.rc != 0:
        problems.append('stock unlock: ' + unlock.brief())
    check = restic(stock.bin, repo, 'check', '--read-data', '--no-cache', work=wdir, timeout=900)
    record['check_rc'] = check.rc
    (wdir / 'check1.txt').write_text(check.cmd + '\n' + check.out + check.err)
    if check.rc != 0:
        problems.append('stock check --read-data after the event: ' + check.brief(600))
    backup = restic(binary, repo, 'backup', '--no-cache', '--host', 'h', *PACK, src, work=wdir, timeout=900)
    record['followup_backup_rc'] = backup.rc
    (wdir / 'followup-backup.txt').write_text(backup.cmd + '\n' + backup.out + backup.err)
    if backup.rc != 0:
        problems.append('follow-up backup: ' + backup.brief(600))
    target = wdir / 'restore'
    rmtree(target)
    restore = restic(binary, repo, 'restore', 'latest', '--no-cache', '--verify', '--target', target, work=wdir, timeout=900)
    record['followup_restore_rc'] = restore.rc
    (wdir / 'followup-restore.txt').write_text(restore.cmd + '\n' + restore.out + restore.err)
    if restore.rc != 0:
        problems.append('follow-up restore: ' + restore.brief(600))
    else:
        diff = diff_manifests(manifest(src), manifest(Path(str(target) + str(src))))
        record['restore_differences'] = len(diff)
        if diff:
            (wdir / 'restore-diff.txt').write_text('\n'.join(diff) + '\n')
            problems.append('restored tree differs from the source: ' + '; '.join(diff[:3]))
    check2 = restic(stock.bin, repo, 'check', '--no-cache', work=wdir, timeout=900)
    record['check2_rc'] = check2.rc
    if check2.rc != 0:
        (wdir / 'check2.txt').write_text(check2.cmd + '\n' + check2.out + check2.err)
        problems.append('stock check after the follow-up backup: ' + check2.brief(600))
    rmtree(target)
    return problems


def kill_trial(arm, binary, op, sig, cal, wdir, seed):
    r = random.Random(seed)
    repo, src = fresh(arm, wdir, op, seed)
    env = restic_env(wdir)
    cmd = [str(binary), '-r', str(repo)] + OPS[op](repo, src)
    at = r.uniform(cal['open'] * 0.7, cal[op] * 1.05)
    record = {'part': 'kill', 'arm': arm, 'op': op, 'signal': sig, 'kill_after_s': round(at, 3), 'seed': seed}
    with open(wdir / 'op.txt', 'wb') as out:
        began = time.monotonic()
        p = subprocess.Popen(cmd, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=subprocess.STDOUT)
        try:
            p.wait(timeout=at)
            record['finished_before_signal'] = True
        except subprocess.TimeoutExpired:
            p.send_signal(signal.SIGKILL if sig == 'KILL' else signal.SIGINT)
            try:
                p.wait(timeout=90)
            except subprocess.TimeoutExpired:
                record['hang'] = True
                p.kill()
                p.wait()
        record['op_rc'] = p.returncode
        record['op_seconds'] = round(time.monotonic() - began, 3)
    problems = ['the process did not exit within 90 s of SIGINT'] if record.get('hang') else []
    problems += after_checks(arm, binary, repo, src, wdir, record)
    return record, problems, ' '.join(cmd)


FAULTS = ['err500', 'drop', 'half', 'outage']


def fault_trial(arm, binary, mode, cal, wdir, seed, requests):
    r = random.Random(seed)
    repo, src = fresh(arm, wdir, 'backup', seed)
    env = restic_env(wdir)
    record = {'part': 'fault', 'arm': arm, 'op': 'backup', 'signal': mode, 'seed': seed}
    options = {}
    if mode != 'outage':
        options = {'fail_after': r.randrange(3, max(4, requests)), 'fail_count': r.randrange(1, 5),
                   'fail_mode': {'err500': '500', 'drop': 'drop', 'half': 'half'}[mode]}
        record.update(options)
    srv = Server(repo, **options)
    cmd = [str(binary), '-r', srv.url] + OPS['backup'](repo, src)
    problems = []
    with open(wdir / 'op.txt', 'wb') as out:
        began = time.monotonic()
        p = subprocess.Popen(cmd, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=subprocess.STDOUT)
        if mode == 'outage':
            at = r.uniform(cal['open'] * 0.7, cal['backup'] * 1.0)
            record['outage_after_s'] = round(at, 3)
            try:
                p.wait(timeout=at)
                record['finished_before_signal'] = True
            except subprocess.TimeoutExpired:
                srv.outage(True)
                try:
                    p.wait(timeout=3)
                except subprocess.TimeoutExpired:
                    p.send_signal(signal.SIGINT)
                    try:
                        p.wait(timeout=90)
                    except subprocess.TimeoutExpired:
                        record['hang'] = True
                        problems.append('backup did not exit within 90 s of SIGINT during a backend outage')
                        p.kill()
                        p.wait()
        else:
            try:
                p.wait(timeout=600)
            except subprocess.TimeoutExpired:
                record['hang'] = True
                problems.append('backup still running 600 s after a transient backend failure')
                p.kill()
                p.wait()
        record['op_rc'] = p.returncode
        record['op_seconds'] = round(time.monotonic() - began, 3)
    stats = srv.stop()
    record['injected'] = stats.get('injected_failures')
    record['aborted'] = stats.get('aborted')
    if mode != 'outage' and record['op_rc'] != 0 and not record.get('hang'):
        record['backup_failed_on_transient_error'] = True
    leftovers = [p for p in (repo / 'data').glob('*/*rest-tmp*')]
    for path in leftovers:
        path.unlink()          # the server's own temporary file of an upload cut by the outage, not restic's
    problems += after_checks(arm, binary, repo, src, wdir, record)
    return record, problems, ' '.join(cmd)


cals = {'cand': calibrate('cand', cand.bin), 'stock': calibrate('stock', stock.bin)}
c.numbers['calibration_seconds'] = cals
# how many requests an undisturbed backup makes through the server (to place the injected failures inside it)
wd = c.work / 'cal-requests'
REQUESTS = 40
try:
    repo, src = fresh('cand', wd, 'backup', 3)
    srv0 = Server(repo)
    must(restic(cand.bin, srv0.url, *OPS['backup'](repo, src), work=wd))
    REQUESTS = max(8, int(srv0.stop().get('requests', 40)))
except Exception as exc:
    c.note('request count calibration failed: %r' % exc)
c.log('an undisturbed backup makes about %d requests' % REQUESTS)

jobs = []
seed = 4000
for arm, binary, share in (('cand', cand.bin, 1.0), ('stock', stock.bin, 0.5)):
    for op in OPS:
        for sig in ('KILL', 'INT'):
            for i in range(max(1, int(N * share))):
                seed += 1
                jobs.append(('kill', arm, binary, op, sig, seed))
    for mode in FAULTS:
        for i in range(max(1, int(N_FAULT * share))):
            seed += 1
            jobs.append(('fault', arm, binary, 'backup', mode, seed))
random.Random(8).shuffle(jobs)
c.status('trials', 0, len(jobs))
c.log('%d trials (%d kill per op and signal, %d per backend failure mode; stock control half), %d workers' % (len(jobs), N, N_FAULT, WORKERS))
records = []
lock = threading.Lock()
position = [0]
kept = {}


def worker(index):
    wdir = c.work / ('w%d' % index)
    wdir.mkdir(exist_ok=True)
    while True:
        with lock:
            if position[0] >= len(jobs):
                return
            part, arm, binary, op, sig, job_seed = jobs[position[0]]
            position[0] += 1
        try:
            if part == 'kill':
                record, problems, cmd = kill_trial(arm, binary, op, sig, cals[arm], wdir, job_seed)
            else:
                record, problems, cmd = fault_trial(arm, binary, sig, cals[arm], wdir, job_seed, REQUESTS)
        except Exception as exc:
            record, problems, cmd = {'part': part, 'arm': arm, 'op': op, 'signal': sig, 'seed': job_seed, 'harness_error': repr(exc)[:400]}, [], ''
        record['problems'] = problems
        if problems:
            with lock:
                n = kept.get((arm, op, sig), 0)
                kept[(arm, op, sig)] = n + 1
            if n < 3:
                ev = c.res / 'evidence' / ('%s-%s-%s-seed%d' % (arm, op, sig, job_seed))
                ev.mkdir(parents=True, exist_ok=True)
                for path in wdir.glob('*.txt'):
                    (ev / path.name).write_bytes(path.read_bytes())
                (ev / 'record.json').write_text(json.dumps(record, indent=1) + '\n')
                (ev / 'command.txt').write_text(cmd + '\n')
                record['evidence'] = str(ev)
                if n == 0:
                    subprocess.call(['cp', '-a', str(wdir / 'repo'), str(ev / 'repo')])
        with lock:
            records.append(record)
            append(c.res / 'trials.jsonl', json.dumps(record))
            if problems or record.get('harness_error'):
                c.log('trial %s %s %s seed %d: %s' % (arm, op, sig, job_seed, (problems or [record.get('harness_error')])[0][:300]))
        c.tick()


threads = [threading.Thread(target=worker, args=(i,)) for i in range(WORKERS)]
for t in threads:
    t.start()
for t in threads:
    t.join()

cells = {}
for rec in records:
    cell = cells.setdefault((rec['arm'], rec.get('part'), rec['op'], rec['signal']), {
        'trials': 0, 'problems': 0, 'signalled': 0, 'locks_left': 0, 'exit_codes': {}, 'harness_errors': 0, 'check_failed': 0, 'hang': 0})
    cell['trials'] += 1
    cell['harness_errors'] += 1 if rec.get('harness_error') else 0
    cell['problems'] += 1 if rec.get('problems') else 0
    cell['signalled'] += 0 if rec.get('finished_before_signal') else 1
    cell['locks_left'] += 1 if rec.get('locks_left') else 0
    cell['check_failed'] += 1 if rec.get('check_rc') not in (0, None) else 0
    cell['hang'] += 1 if rec.get('hang') else 0
    code = str(rec.get('op_rc'))
    cell['exit_codes'][code] = cell['exit_codes'].get(code, 0) + 1
lines = ['| binary | part | operation | event | trials | hit while running | trials with a problem | stock check failed | hangs | lock left behind | exit codes |',
         '|---|---|---|---|---|---|---|---|---|---|---|']
for (arm, part, op, sig), cell in sorted(cells.items()):
    lines.append('| %s | %s | %s | %s | %d | %d | %d | %d | %d | %d | %s |' % (
        arm, part, op, sig, cell['trials'], cell['signalled'], cell['problems'], cell['check_failed'], cell['hang'],
        cell['locks_left'], json.dumps(cell['exit_codes'], sort_keys=True)))
(c.res / 'kill-table.md').write_text('\n'.join(lines) + '\n')
for line in lines:
    c.log(line)
c.numbers['cells'] = {'%s/%s/%s/%s' % k: v for k, v in sorted(cells.items())}
total_problems = 0
for (arm, part, op, sig), cell in sorted(cells.items()):
    if arm != 'cand':
        continue
    control = cells.get(('stock', part, op, sig), {'trials': 0, 'problems': 0, 'locks_left': 0, 'exit_codes': {}})
    if cell['problems']:
        total_problems += cell['problems']
        first = next(r for r in records if r['arm'] == 'cand' and r['op'] == op and r['signal'] == sig and r.get('problems'))
        c.fail('%s, %s: %d of %d trials with a problem; first: %s' % (op, sig, cell['problems'], cell['trials'], first['problems'][0][:400]),
               evidence=first.get('evidence', c.res / 'trials.jsonl'),
               stock='stock as the disturbed binary: %d of %d trials with a problem' % (control['problems'], control['trials']),
               repro='python3 c04_kill.py <tree> <label>   (trial seed %d)' % first['seed'])
    if sig == 'INT' and cell['locks_left'] and not control['locks_left'] and control['trials'] >= 10:
        c.note('%s, SIGINT: the tree left a lock file behind in %d of %d trials, stock in 0 of %d' % (op, cell['locks_left'], cell['trials'], control['trials']))
    extra = set(cell['exit_codes']) - set(control['exit_codes'])
    if extra and control['trials'] >= 10:
        c.note('%s, %s: exit codes %s seen on the tree but not on stock (%s)' % (op, sig, sorted(extra), json.dumps(control['exit_codes'], sort_keys=True)))
    if cell['harness_errors']:
        c.note('%s, %s: %d trials stopped with a harness error' % (op, sig, cell['harness_errors']))
cand_trials = sum(v['trials'] for k, v in cells.items() if k[0] == 'cand')
hit = sum(v['signalled'] for k, v in cells.items() if k[0] == 'cand')
stock_problems = sum(v['problems'] for k, v in cells.items() if k[0] == 'stock')
summary = '%d trials on the tree (%d hit the process while it ran): %d with a problem; stock control %d trials, %d with a problem' % (
    cand_trials, hit, total_problems, sum(v['trials'] for k, v in cells.items() if k[0] == 'stock'), stock_problems)
sys.exit(c.finish(summary))
