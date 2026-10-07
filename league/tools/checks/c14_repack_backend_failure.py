#!/usr/bin/env python3
"""C14. A backend that fails in the middle of a repack, then stock `check --read-data`.

Repository (stock-written, --pack-size 4): two snapshots, the first forgotten, so `prune --max-unused 0` has to
repack partly used packs into several new packs. Per trial, on a fresh copy, the prune is disturbed in one of
three ways:
  server-killed    the prune runs through the REST server; the server is killed (SIGKILL) at a random moment
  uploads-refused  through the REST server; from the K-th pack upload on (K random) every upload loses its connection
  data-read-only   local repository, prune as uid 65534; at a random moment every directory under data/ loses its
                   write permission (new packs cannot be created, old ones cannot be removed)
  read-error-while-uploads-stall   through the REST server, which holds every pack upload for 15 minutes; one blob
                   that the repack must read is damaged on disk (one byte, in a partly used pack). The prune must
                   come back with the read error within 90 s instead of waiting for its blocked saves (stock is
                   run too: what it does is recorded, a hang of stock is not counted against the tree). The byte
                   is put back before the checks that follow.
restic retries a failing backend for minutes; a prune that is still running 5 s after the failure gets SIGINT and
must exit within 90 s (no hang). Afterwards, with permissions restored and the server's own temporary files removed:
  1. stock `unlock`, stock `check --read-data` must pass
  2. `ls --json` of every snapshot (read by stock) must equal the listing taken before
  3. the same binary prunes again, undisturbed, on the local path: exit 0; stock `check --read-data` passes; listings equal
Stock as the disturbed binary is the control (half as many trials)."""
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
from chk import (NOBODY, QUICK, Check, Server, append, chkrest_bin, copy_repo, envint, gen_corpus, index_packs, must, prepare, prepare_stock,  # noqa: E402
                 repo_files, restic, restic_env, rmtree, write_file)

c = Check('c14', 'backend failure in the middle of a repack (server killed, uploads refused, data read-only), then stock check')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
chkrest_bin()
N = envint('CHK_C14_TRIALS', 14)
WORKERS = envint('CHK_C14_WORKERS', 3)
if QUICK:
    N = 2
ws = c.work / 'ws'
PACK = ['--pack-size', '4']
PRUNE = ['prune', '--max-unused', '0', '--no-cache'] + PACK
src = c.work / 'src'
gen_corpus(src, seed=14, small=600, small_max=40_000, text_files=3, text_mb=3, big=(5, 7, 4), special=False)
template = c.work / 'template'
must(restic(stock.bin, template, 'init', work=ws))
must(restic(stock.bin, template, 'backup', '--no-cache', '--host', 'h', '--time', '2026-05-01 00:00:00', *PACK, src, work=ws))
r0 = random.Random(141)
small = sorted((src / 'small').rglob('*.dat'))
for path in r0.sample(small, len(small) // 2):
    path.unlink()
(src / 'big' / 'b1.bin').unlink()
write_file(src / 'big' / 'second.bin', r0.randbytes(3 << 20))
must(restic(stock.bin, template, 'backup', '--no-cache', '--host', 'h', '--time', '2026-05-02 00:00:00', *PACK, src, work=ws))
snaps = sorted(json.loads(must(restic(stock.bin, template, 'snapshots', '--json', '--no-cache', work=ws)).out), key=lambda s: s['time'])
must(restic(stock.bin, template, 'forget', snaps[0]['id'], '--no-cache', work=ws))
KEPT = [snaps[1]['id']]


def listings(repo, wdir):
    return {sid: (lambda r: (r.rc, r.out))(restic(stock.bin, repo, 'ls', '--json', sid, '--no-cache', work=wdir)) for sid in KEPT}


baseline = listings(template, ws)
# blobs the kept snapshot uses, and the partly used packs (those a prune with --max-unused 0 has to repack)
used = set()
stack = [snaps[1]['tree']]
while stack:           # `ls --json` does not print content IDs: walk the trees themselves
    tree = json.loads(must(restic(stock.bin, template, 'cat', 'blob', stack.pop(), '--no-cache', work=ws)).out)
    for node in tree['nodes']:
        if node['type'] == 'file':
            used.update(node.get('content') or [])
        elif node['type'] == 'dir' and node.get('subtree'):
            stack.append(node['subtree'])
PACKS = index_packs(stock.bin, template, ws)
READ_TARGETS = []
for pack_id, blobs in sorted(PACKS.items()):
    mine = [b for b in blobs if b['type'] == 'data' and b['id'] in used]
    if mine and len(mine) < len(blobs):
        READ_TARGETS += [(pack_id, b['offset'] + b['length'] // 2) for b in mine if b['length'] > 64]
STALL_LIMIT = 90
c.log('template: %d packs, %d snapshot kept' % (len(repo_files(template, 'data')), len(KEPT)))


def calibrate(arm, binary):
    wdir = c.work / ('cal-' + arm)
    wdir.mkdir(exist_ok=True)
    times, posts, opens = [], [], []
    for i in range(3):
        copy_repo(template, wdir / 'repo')
        opens.append(restic(binary, wdir / 'repo', 'cat', 'config', '--no-lock', work=wdir).secs)
        srv = Server(wdir / 'repo')
        r = restic(binary, srv.url, *PRUNE, work=wdir, timeout=600)
        stats = srv.stop()
        if r.rc != 0:
            raise RuntimeError('calibration prune by %s exits %d: %s' % (arm, r.rc, r.brief(300)))
        times.append(r.secs)
        posts.append(stats.get('by', {}).get('POST data', {}).get('requests', 0))
    cal = {'open': statistics.median(opens), 'prune': statistics.median(times), 'pack_uploads': int(statistics.median(posts))}
    c.log('calibration %s: %s' % (arm, json.dumps({k: round(v, 2) for k, v in cal.items()})))
    return cal


cals = {'tree': calibrate('tree', cand.bin), 'stock': calibrate('stock', stock.bin)}
c.numbers['calibration'] = cals
MODES = ['server-killed', 'uploads-refused', 'data-read-only', 'read-error-while-uploads-stall']
c.log('blobs used by the kept snapshot: %d; damage targets in partly used packs: %d' % (len(used), len(READ_TARGETS)))
if not READ_TARGETS:
    c.note('no used blob in a partly used pack was found: the read-error case is not run')
    MODES = MODES[:3]


def wait_or_interrupt(p, record, grace):
    """Give the prune `grace` seconds to end by itself, then SIGINT; a prune that ignores SIGINT for 90 s hangs."""
    try:
        p.wait(timeout=grace)
        record['ended'] = 'by itself'
    except subprocess.TimeoutExpired:
        p.send_signal(signal.SIGINT)
        try:
            p.wait(timeout=90)
            record['ended'] = 'after SIGINT'
        except subprocess.TimeoutExpired:
            p.kill()
            p.wait()
            record['ended'] = 'HANG'
    record['rc'] = p.returncode


def trial(arm, binary, mode, seed, wdir):
    r = random.Random(seed)
    repo = wdir / 'repo'
    copy_repo(template, repo)
    env = restic_env(wdir)
    cal = cals[arm]
    record = {'arm': arm, 'mode': mode, 'seed': seed}
    problems = []
    out = open(wdir / 'disturbed-prune.txt', 'wb')
    undo = None
    if mode == 'read-error-while-uploads-stall':
        pack_id, at = r.choice(READ_TARGETS)
        path = repo / 'data' / pack_id[:2] / pack_id
        os.chmod(path, 0o600)
        with open(path, 'r+b') as f:
            f.seek(at)
            old = f.read(1)
            f.seek(at)
            f.write(bytes([old[0] ^ 0x10]))
        undo = (path, at, old)
        record['damaged_pack'] = pack_id[:8]
        srv = Server(repo, fail_after=1, fail_count=10**6, fail_mode='stall', fail_method='POST', fail_kind='data', stall_ms=900000)
        p = subprocess.Popen([str(binary), '-r', srv.url] + PRUNE, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=subprocess.STDOUT)
        began = time.monotonic()
        try:
            p.wait(timeout=STALL_LIMIT)
            record['ended'] = 'by itself'
        except subprocess.TimeoutExpired:
            p.kill()
            p.wait()
            record['ended'] = 'HANG'
        record['rc'] = p.returncode
        record['seconds_to_return'] = round(time.monotonic() - began, 1)
        stats = srv.stop()
        record['uploads_stalled'] = stats.get('injected_failures')
        with open(path, 'r+b') as f:
            f.seek(at)
            f.write(old)
        os.chmod(path, 0o400)
    elif mode == 'data-read-only':
        subprocess.check_call(['chown', '-R', '%d:%d' % (NOBODY, NOBODY), str(repo), str(wdir / 'home'), str(wdir / 'tmp')])
        at = r.uniform(cal['open'] * 0.7, cal['prune'] * 0.95)
        record['failure_after_s'] = round(at, 3)
        p = subprocess.Popen([str(binary), '-r', str(repo)] + PRUNE, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=subprocess.STDOUT,
                             user=NOBODY, group=NOBODY, extra_groups=[])
        try:
            p.wait(timeout=at)
            record['finished_before_failure'] = True
            record['rc'] = p.returncode
            record['ended'] = 'before the failure'
        except subprocess.TimeoutExpired:
            for d in [repo / 'data'] + [x for x in (repo / 'data').iterdir() if x.is_dir()]:
                os.chmod(d, 0o555)
            wait_or_interrupt(p, record, 5)
        for d in [repo / 'data'] + [x for x in (repo / 'data').iterdir() if x.is_dir()]:
            os.chmod(d, 0o700)
        subprocess.check_call(['chown', '-R', '0:0', str(repo), str(wdir / 'home'), str(wdir / 'tmp')])
    else:
        options = {}
        if mode == 'uploads-refused':
            options = {'fail_after': r.randrange(1, max(2, cal['pack_uploads'] + 1)), 'fail_count': 10**6, 'fail_mode': 'drop',
                       'fail_method': 'POST', 'fail_kind': 'data'}
            record['first_refused_upload'] = options['fail_after']
        srv = Server(repo, **options)
        p = subprocess.Popen([str(binary), '-r', srv.url] + PRUNE, env=env, stdin=subprocess.DEVNULL, stdout=out, stderr=subprocess.STDOUT)
        if mode == 'server-killed':
            at = r.uniform(cal['open'] * 0.7, cal['prune'] * 0.95)
            record['failure_after_s'] = round(at, 3)
            try:
                p.wait(timeout=at)
                record['finished_before_failure'] = True
                record['rc'] = p.returncode
                record['ended'] = 'before the failure'
            except subprocess.TimeoutExpired:
                srv.proc.kill()
                wait_or_interrupt(p, record, 5)
        else:
            wait_or_interrupt(p, record, cal['prune'] + 5)
        stats = srv.stop()
        record['uploads_refused'] = stats.get('injected_failures')
    out.close()
    if record.get('ended') == 'HANG' and mode == 'read-error-while-uploads-stall':
        problems.append('the prune did not return the read error within %d s (its uploads were stalled): it hangs' % STALL_LIMIT)
    elif record.get('ended') == 'HANG':
        problems.append('the prune did not exit within 90 s of SIGINT')
    if mode == 'read-error-while-uploads-stall' and record.get('rc') == 0:
        problems.append('the prune exits 0 although a blob it had to repack was damaged')
    for stray in (repo / 'data').glob('*/*rest-tmp*'):       # the killed server's own temporary file, not restic's
        stray.unlink()
    record['packs_after'] = len(repo_files(repo, 'data'))
    restic(stock.bin, repo, 'unlock', work=wdir)
    check = restic(stock.bin, repo, 'check', '--read-data', '--no-cache', work=wdir, timeout=900)
    (wdir / 'check-after-failure.txt').write_text(check.cmd + '\n' + check.out + check.err)
    record['check_rc'] = check.rc
    if check.rc != 0:
        problems.append('stock check --read-data after the failure: ' + check.brief(500))
    if listings(repo, wdir) != baseline:
        problems.append('snapshot contents differ after the disturbed prune')
    again = restic(binary, repo, *PRUNE, work=wdir, timeout=900)
    (wdir / 'second-prune.txt').write_text(again.cmd + '\n' + again.out + again.err)
    record['second_prune_rc'] = again.rc
    if again.rc != 0:
        problems.append('second prune exits %d: %s' % (again.rc, again.brief(400)))
    check2 = restic(stock.bin, repo, 'check', '--read-data', '--no-cache', work=wdir, timeout=900)
    record['check2_rc'] = check2.rc
    if check2.rc != 0:
        (wdir / 'check-after-second-prune.txt').write_text(check2.cmd + '\n' + check2.out + check2.err)
        problems.append('stock check --read-data after the second prune: ' + check2.brief(500))
    if listings(repo, wdir) != baseline:
        problems.append('snapshot contents differ after the second prune')
    record['problems'] = problems
    return record


def trials_for(mode, n):
    return max(1, n // 2) if mode == 'read-error-while-uploads-stall' else n      # each of these can cost the full limit


jobs = [('tree', cand.bin, mode, 14000 + i * 10 + k) for k, mode in enumerate(MODES) for i in range(trials_for(mode, N))]
jobs += [('stock', stock.bin, mode, 15000 + i * 10 + k) for k, mode in enumerate(MODES) for i in range(trials_for(mode, max(1, N // 2)))]
random.Random(14).shuffle(jobs)
c.status('trials', 0, len(jobs))
lock = threading.Lock()
position = [0]
records = []
kept = {}


def worker(index):
    wdir = c.work / ('w%d' % index)
    wdir.mkdir(exist_ok=True)
    restic_env(wdir)
    while True:
        with lock:
            if position[0] >= len(jobs):
                return
            arm, binary, mode, seed = jobs[position[0]]
            position[0] += 1
        try:
            record = trial(arm, binary, mode, seed, wdir)
        except Exception as exc:
            record = {'arm': arm, 'mode': mode, 'seed': seed, 'problems': [], 'harness_error': repr(exc)[:400]}
            subprocess.call(['chown', '-R', '0:0', str(wdir)])
        if record['problems']:
            with lock:
                n = kept.get((arm, mode), 0)
                kept[(arm, mode)] = n + 1
            if n < 3:
                ev = c.res / 'evidence' / ('%s-%s-seed%d' % (arm, mode, seed))
                ev.mkdir(parents=True, exist_ok=True)
                for path in wdir.glob('*.txt'):
                    (ev / path.name).write_bytes(path.read_bytes())
                (ev / 'record.json').write_text(json.dumps(record, indent=1) + '\n')
                record['evidence'] = str(ev)
        with lock:
            records.append(record)
            append(c.res / 'trials.jsonl', json.dumps(record))
        if record['problems'] or record.get('harness_error'):
            c.log('trial %s %s seed %d: %s' % (arm, mode, seed, (record['problems'] or [record.get('harness_error')])[0][:300]))
        c.tick()


threads = [threading.Thread(target=worker, args=(i,)) for i in range(WORKERS)]
for t in threads:
    t.start()
for t in threads:
    t.join()
cells = {}
for rec in records:
    cell = cells.setdefault((rec['arm'], rec['mode']), {'trials': 0, 'disturbed_while_running': 0, 'problems': 0, 'harness_errors': 0, 'ended': {}, 'exit_codes': {}})
    cell['trials'] += 1
    cell['harness_errors'] += 1 if rec.get('harness_error') else 0
    cell['problems'] += 1 if rec['problems'] else 0
    cell['disturbed_while_running'] += 0 if rec.get('finished_before_failure') or rec.get('harness_error') else 1
    cell['ended'][rec.get('ended', '?')] = cell['ended'].get(rec.get('ended', '?'), 0) + 1
    cell['exit_codes'][str(rec.get('rc'))] = cell['exit_codes'].get(str(rec.get('rc')), 0) + 1
lines = ['| binary | failure | trials | disturbed while running | with a problem | how the prune ended | exit codes |', '|---|---|---|---|---|---|---|']
for (arm, mode), cell in sorted(cells.items()):
    lines.append('| %s | %s | %d | %d | %d | %s | %s |' % (arm, mode, cell['trials'], cell['disturbed_while_running'], cell['problems'],
                                                        json.dumps(cell['ended'], sort_keys=True), json.dumps(cell['exit_codes'], sort_keys=True)))
(c.res / 'table.md').write_text('\n'.join(lines) + '\n')
for line in lines:
    c.log(line)
c.numbers['cells'] = {'%s/%s' % k: v for k, v in sorted(cells.items())}
total = 0
for (arm, mode), cell in sorted(cells.items()):
    if arm != 'tree':
        continue
    control = cells.get(('stock', mode), {'trials': 0, 'problems': 0})
    if cell['problems']:
        total += cell['problems']
        first = next(r for r in records if r['arm'] == 'tree' and r['mode'] == mode and r['problems'])
        c.fail('repack with a failing backend (%s): %d of %d trials with a problem; first: %s' % (mode, cell['problems'], cell['trials'], first['problems'][0][:400]),
               evidence=first.get('evidence', c.res / 'trials.jsonl'), stock='stock disturbed the same way: %d of %d trials with a problem' % (control['problems'], control['trials']),
               repro='python3 c14_repack_backend_failure.py <tree> <label>   (trial seed %d)' % first['seed'])
    if cell['harness_errors']:
        c.note('%s: %d trials stopped with a harness error' % (mode, cell['harness_errors']))
    if mode == 'read-error-while-uploads-stall':
        c.note('read error while the uploads are stalled: the tree ended %s with exit codes %s; stock ended %s with exit codes %s' % (
            json.dumps(cell['ended'], sort_keys=True), json.dumps(cell['exit_codes'], sort_keys=True),
            json.dumps(control.get('ended', {}), sort_keys=True), json.dumps(control.get('exit_codes', {}), sort_keys=True)))
tree_trials = sum(v['trials'] for k, v in cells.items() if k[0] == 'tree')
hit = sum(v['disturbed_while_running'] for k, v in cells.items() if k[0] == 'tree')
sys.exit(c.finish('%d disturbed prunes on the tree (%d hit a running prune; 4 kinds of backend failure): %d with a problem; stock control %d trials, %d with a problem' % (
    tree_trials, hit, total, sum(v['trials'] for k, v in cells.items() if k[0] == 'stock'), sum(v['problems'] for k, v in cells.items() if k[0] == 'stock'))))
