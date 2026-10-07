#!/usr/bin/env python3
"""C5. Corrupted-pack detection: `check` and `check --read-data` of the tree must report exactly what stock reports,
on every route of the tree's checker.

Damage (each on a fresh copy of one stock-written repository; the pack and the byte are picked at random per round):
  flip one byte inside a blob of: a tree pack, a data pack with more than 2,000 blobs (header above 64 KiB), a data
  pack whose blobs are all at most 1 MiB, one with a blob between 1 and 2 MiB, one with a blob above 2 MiB;
  flip one byte in the encrypted pack header (two pack classes) and in the 4-byte header length;
  cut the last 100 bytes (data pack, tree pack); cut a pack in half; empty a pack; remove a data pack; remove a tree pack.
Routes of the tree's binary: local with default settings, GOMAXPROCS 1 / 2 / 64, `-o local.connections=1` (also with
GOMAXPROCS 64), and through the REST server with default connections, `-o rest.connections=1` and `=2` (the latter
also with GOMAXPROCS 64). One undamaged copy goes through every route as the control.
Stock is run on the same damaged copy, local and through REST; each route is compared with stock on the same backend:
exit code, and stdout+stderr as a sorted list of lines without progress lines."""
import json
import os
import random
import sys
import threading
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import (QUICK, Check, Server, append, chkrest_bin, copy_repo, envint, index_packs, must, normalize_output,  # noqa: E402
                 pack_path, prepare, prepare_stock, restic, rmtree, same_output, write_file)

c = Check('c05', 'corrupted-pack detection on every checker route, against stock')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
chkrest_bin()
ROUNDS = envint('CHK_C05_ROUNDS', 4)
WORKERS = envint('CHK_C05_WORKERS', 4)
if QUICK:
    ROUNDS = 1
MIB = 1 << 20

src = c.work / 'src'
rng = random.Random(5)
for i in range(6000):
    write_file(src / 'tiny' / ('d%02d' % (i % 40)) / ('t%04d' % i), rng.randbytes(rng.randrange(200, 1500)))
for i in range(120):
    write_file(src / 'small' / ('s%03d' % i), rng.randbytes(rng.randrange(20_000, 300_000)))
for i in range(8):
    write_file(src / 'mid' / ('m%d' % i), rng.randbytes(rng.randrange(int(1.1 * MIB), int(1.9 * MIB))))
for i in range(2):
    write_file(src / 'big' / ('b%d' % i), rng.randbytes(28 * MIB))
write_file(src / 'text' / 't.txt', (b'restic check must see this damage. ' * 40000))
base = c.work / 'base'
w0 = c.work / 'w-setup'
must(restic(stock.bin, base, 'init', work=w0))
for part in ('tiny', 'small', 'mid', 'big', 'text'):
    must(restic(stock.bin, base, 'backup', '--no-cache', '--host', 'h', src / part, work=w0))
packs = index_packs(stock.bin, base, w0)
classes = {'tree': [], 'many': [], 'le1m': [], 'b1to2m': [], 'gt2m': []}
for pack_id, blobs in sorted(packs.items()):
    if all(b['type'] == 'tree' for b in blobs):
        classes['tree'].append(pack_id)
        continue
    biggest = max(b['length'] for b in blobs)
    if len(blobs) > 2000:
        classes['many'].append(pack_id)
    elif biggest > 2 * MIB:
        classes['gt2m'].append(pack_id)
    elif biggest > MIB:
        classes['b1to2m'].append(pack_id)
    else:
        classes['le1m'].append(pack_id)
c.numbers['pack_classes'] = {k: len(v) for k, v in classes.items()}
c.log('packs by class: %s' % json.dumps(c.numbers['pack_classes']))
for name, members in classes.items():
    if not members:
        c.note('no pack of class %s in the test repository: its scenarios are skipped' % name)

SCENARIOS = [('undamaged', 'le1m'), ('flip-blob', 'tree'), ('flip-blob', 'many'), ('flip-blob', 'le1m'), ('flip-blob', 'b1to2m'), ('flip-blob', 'gt2m'),
             ('flip-header', 'many'), ('flip-header', 'le1m'), ('flip-length', 'le1m'), ('cut-tail', 'le1m'), ('cut-tail', 'tree'),
             ('cut-half', 'gt2m'), ('empty', 'le1m'), ('remove', 'le1m'), ('remove', 'tree')]
ROUTES = [('local default', 'local', {}, []), ('local GOMAXPROCS=1', 'local', {'GOMAXPROCS': '1'}, []),
          ('local GOMAXPROCS=2', 'local', {'GOMAXPROCS': '2'}, []), ('local GOMAXPROCS=64', 'local', {'GOMAXPROCS': '64'}, []),
          ('local connections=1', 'local', {}, ['-o', 'local.connections=1']),
          ('local connections=1 GOMAXPROCS=64', 'local', {'GOMAXPROCS': '64'}, ['-o', 'local.connections=1']),
          ('rest default', 'rest', {}, []), ('rest connections=1', 'rest', {}, ['-o', 'rest.connections=1']),
          ('rest connections=2', 'rest', {}, ['-o', 'rest.connections=2']),
          ('rest connections=2 GOMAXPROCS=64', 'rest', {'GOMAXPROCS': '64'}, ['-o', 'rest.connections=2'])]
MODES = [('check', ['check', '--no-cache']), ('check --read-data', ['check', '--read-data', '--no-cache'])]


def damage(repo, kind, cls, r):
    pack_id = r.choice(classes[cls])
    path = pack_path(repo, pack_id)
    size = path.stat().st_size
    blobs = packs[pack_id]
    header = 4 + len(blobs) * 41 + 32          # upper bound of the encrypted header plus the length field
    detail = {'pack': pack_id, 'size': size, 'blobs': len(blobs)}
    if kind == 'undamaged':          # the control: a clean repository through every route
        return {'pack': None}
    if kind == 'remove':
        path.unlink()
        return detail
    os.chmod(path, 0o600)
    with open(path, 'r+b') as f:
        if kind == 'flip-blob':
            blob = r.choice(blobs)
            offset = blob['offset'] + r.randrange(blob['length'])
            detail.update(offset=offset, blob=blob['id'], blob_length=blob['length'])
        elif kind == 'flip-header':
            offset = size - 4 - 1 - r.randrange(min(header - 4, len(blobs) * 37))
            detail.update(offset=offset)
        elif kind == 'flip-length':
            offset = size - 1 - r.randrange(4)
            detail.update(offset=offset)
        elif kind == 'cut-tail':
            f.truncate(size - 100)
            return detail
        elif kind == 'cut-half':
            f.truncate(size // 2)
            return detail
        elif kind == 'empty':
            f.truncate(0)
            return detail
        f.seek(offset)
        old = f.read(1)
        f.seek(offset)
        f.write(bytes([old[0] ^ (1 << r.randrange(8))]))
    return detail


jobs = [(kind, cls, n) for kind, cls in SCENARIOS if classes[cls] for n in range(ROUNDS)]
c.status('scenarios', 0, len(jobs))
c.log('%d damaged repositories x %d routes x %d modes' % (len(jobs), len(ROUTES), len(MODES)))
lock = threading.Lock()
position = [0]
results = []
mismatches = []
undetected = []


def worker(index):
    wdir = c.work / ('w%d' % index)
    wdir.mkdir(exist_ok=True)
    while True:
        with lock:
            if position[0] >= len(jobs):
                return
            kind, cls, n = jobs[position[0]]
            job_no = position[0]
            position[0] += 1
        repo = wdir / 'repo'
        copy_repo(base, repo)
        detail = damage(repo, kind, cls, random.Random(500 + job_no))
        name = '%s/%s/round%d' % (kind, cls, n)
        srv = Server(repo)
        row = {'scenario': name, 'detail': detail, 'routes': {}}
        try:
            for mode, args in MODES:
                ref = {'local': restic(stock.bin, repo, *args, work=wdir), 'rest': restic(stock.bin, srv.url, *args, work=wdir)}
                row['stock ' + mode] = ref['local'].rc
                if ref['local'].rc == 0 and mode == 'check --read-data' and kind != 'undamaged':
                    with lock:
                        undetected.append(name)
                for route, backend, env, options in ROUTES:
                    target = repo if backend == 'local' else srv.url
                    got = restic(cand.bin, target, *options, *args, work=wdir, env=env)
                    hide = [(str(repo), '<repo>'), (srv.url, '<repo>'), (str(srv.sock), '<sock>')]
                    diff = same_output(ref[backend], got, hide, hide)
                    row['routes']['%s | %s' % (mode, route)] = 'same' if not diff else diff
                    if diff:
                        ev = c.res / 'evidence' / ('%s-%s-%s' % (name.replace('/', '_'), mode.replace(' ', ''), route.replace(' ', '_').replace('=', '')))
                        ev.mkdir(parents=True, exist_ok=True)
                        (ev / 'stock.txt').write_text('%s\nrc=%d\n%s\n%s' % (ref[backend].cmd, ref[backend].rc, ref[backend].out, ref[backend].err))
                        (ev / 'tree.txt').write_text('%s\nenv=%s\nrc=%d\n%s\n%s' % (got.cmd, env, got.rc, got.out, got.err))
                        (ev / 'damage.json').write_text(json.dumps({'scenario': name, 'detail': detail}, indent=1) + '\n')
                        with lock:
                            mismatches.append((name, mode, route, diff, str(ev), got.cmd, env))
        finally:
            srv.stop()
        with lock:
            results.append(row)
            append(c.res / 'scenarios.jsonl', json.dumps(row))
        c.tick()


threads = [threading.Thread(target=worker, args=(i,)) for i in range(WORKERS)]
for t in threads:
    t.start()
for t in threads:
    t.join()
comparisons = sum(len(r['routes']) for r in results)
c.numbers.update(scenarios=len(results), comparisons=comparisons, mismatches=len(mismatches), undetected_by_stock=len(undetected))
seen = set()
for name, mode, route, diff, ev, cmd, env in mismatches:
    key = (name.rsplit('/', 1)[0], mode, route)
    if key in seen:
        continue
    seen.add(key)
    c.fail('%s, %s, route %s: the tree does not report what stock reports: %s' % (key[0], mode, route, '; '.join(diff)[:500]),
           evidence=ev, stock='stock output in the evidence folder (stock.txt)',
           repro='%s %s   (on the damaged repository described in damage.json)' % (' '.join('%s=%s' % kv for kv in env.items()), cmd))
if undetected:
    c.note('stock check --read-data exits 0 for: %s (the damage is not visible to stock either)' % ', '.join(sorted(set(undetected))[:10]))
summary = '%d damaged repositories (%d kinds x %d rounds), %d route comparisons with stock: %d differ' % (
    len(results), len(SCENARIOS), ROUNDS, comparisons, len(mismatches))
sys.exit(c.finish(summary))
