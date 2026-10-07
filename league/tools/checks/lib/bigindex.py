#!/usr/bin/env python3
"""A repository with a large index that prune has real work on, for C11 and C12.

Start: a COPY of the judge's big-index fixture (restic-league-v2/workloads-big-index/pristine-repo, read only there):
1,500,000 unique 48-byte data blobs in 8 packs of 187,500 blobs each, referenced by one snapshot tagged `ballast`.
Added by stock with --pack-size 4: two backups of a generated corpus that also holds `small_files` files of 300
random bytes (one blob each); the second backup has every third of them deleted; the first snapshot is forgotten.
Added with chktool, when ballast_partial > 0: a snapshot whose files reference every third blob of that many ballast
packs. Then stock forgets the ballast snapshot.

What prune then finds: partly used packs of small blobs (to repack), ballast packs one third used (to repack) or
unused (to remove). The number of index entries in the packs to repack picks the way the final tree lists them
(review of S020, finding 10; constants in index/repack_routes.go):
  collector   up to 8,192 entries and 512 packs               small_files=3000,  ballast_partial=0
  routes      more, while the route table fits 65,536 slots    small_files=24000, ballast_partial=0
  fallback    above that                                       small_files=3000,  ballast_partial=1  (187,500 entries)"""
import json
import random
import subprocess
from pathlib import Path

from chk import LEAGUE, copy_repo, gen_corpus, index_packs, must, mutate_corpus, restic, rmtree, write_file

FIXTURE = LEAGUE / 'workloads-big-index'
VARIANTS = {'collector': (3000, 0, True), 'routes': (24000, 0, True), 'fallback': (3000, 1, True),
            # the ballast snapshot stays: its index files (full, no removed pack) are the ones prune keeps as they are
            'keepballast': (3000, 0, False)}
PACK = ['--pack-size', '4']


def available():
    return (FIXTURE / 'pristine-repo' / 'config').is_file() and (FIXTURE / 'password').is_file()


def password_env():
    return {'RESTIC_PASSWORD': (FIXTURE / 'password').read_text().strip()}


def build(work, stock_bin, tool_bin, variant, name):
    """Returns {'repo', 'env', 'snapshots', 'variant', 'small_files', 'ballast_partial', 'packs', 'blobs',
    'entries_in_partly_used_packs'}."""
    small_files, ballast_partial, forget_ballast = VARIANTS[variant]
    work = Path(work)
    env = password_env()
    ws = work / 'ws-build'
    repo = work / name
    copy_repo(FIXTURE / 'pristine-repo', repo)
    subprocess.check_call(['chmod', '-R', 'u+w', str(repo)])
    corpus = work / ('corpus-' + name)
    rmtree(corpus)
    gen_corpus(corpus, seed=11, small=200, text_files=3, text_mb=2, big=(5, 3), special=False)
    r = random.Random(13)
    for i in range(small_files):
        write_file(corpus / 'many' / ('d%03d' % (i % 200)) / ('m%05d' % i), r.randbytes(300))
    must(restic(stock_bin, repo, 'backup', '--no-cache', '--host', 'h', '--time', '2026-03-01 00:00:00', *PACK, corpus.name, work=ws, env=env, cwd=corpus.parent))
    mutate_corpus(corpus, seed=12)
    for i in range(0, small_files, 3):
        (corpus / 'many' / ('d%03d' % (i % 200)) / ('m%05d' % i)).unlink()
    must(restic(stock_bin, repo, 'backup', '--no-cache', '--host', 'h', '--time', '2026-03-02 00:00:00', *PACK, corpus.name, work=ws, env=env, cwd=corpus.parent))
    snaps = sorted(json.loads(must(restic(stock_bin, repo, 'snapshots', '--json', '--no-cache', work=ws, env=env)).out), key=lambda s: s['time'])
    ballast = [s for s in snaps if 'ballast' in (s.get('tags') or [])]
    corpus_snaps = [s for s in snaps if s not in ballast]
    packs = index_packs(stock_bin, repo, ws, env=env)
    ballast_packs = sorted(p for p, blobs in packs.items() if len(blobs) > 100000)
    chosen = ballast_packs[:ballast_partial]
    if chosen:
        # a file node of the real corpus as the template for the invented files
        top = json.loads(must(restic(stock_bin, repo, 'cat', 'blob', corpus_snaps[-1]['tree'], '--no-cache', work=ws, env=env)).out)
        template = None
        stack = [top]
        while stack and template is None:
            tree = stack.pop()
            for node in tree['nodes']:
                if node['type'] == 'file' and node.get('content'):
                    template = node
                    break
                if node['type'] == 'dir' and node.get('subtree'):
                    stack.append(json.loads(must(restic(stock_bin, repo, 'cat', 'blob', node['subtree'], '--no-cache', work=ws, env=env)).out))
        nodes = []
        for n, pack_id in enumerate(chosen):
            third = sorted(packs[pack_id], key=lambda b: b['offset'])[::3]
            for part in range(0, len(third), 5000):          # files of 5,000 blobs each
                piece = third[part:part + 5000]
                node = dict(template)
                node.update(name='p%02d-%04d' % (n, part // 5000), content=[b['id'] for b in piece],
                            size=sum(b.get('uncompressed_length') or (b['length'] - 32) for b in piece))
                for key in ('links', 'inode', 'device_id', 'extended_attributes'):
                    node.pop(key, None)
                nodes.append(node)
        blob = work / ('subset-%s.json' % name)
        blob.write_text(json.dumps({'nodes': nodes}, separators=(',', ':')) + '\n')
        must(restic(tool_bin, repo, 'chk-savetree', '--file', blob, '--name', '/subset', '--no-cache', work=ws, env=env), 'chktool chk-savetree')
    forget = ([s['id'] for s in ballast] if forget_ballast else []) + [corpus_snaps[0]['id']]
    must(restic(stock_bin, repo, 'forget', *forget, '--no-cache', work=ws, env=env), 'forget')
    left = sorted(s['id'] for s in json.loads(must(restic(stock_bin, repo, 'snapshots', '--json', '--no-cache', work=ws, env=env)).out))
    small_packs = [p for p, blobs in packs.items() if p not in ballast_packs]
    rmtree(corpus)
    return {'repo': repo, 'env': env, 'snapshots': left, 'variant': variant, 'small_files': small_files, 'ballast_partial': len(chosen),
            'packs': len(packs), 'blobs': sum(len(v) for v in packs.values()),
            'entries_in_partly_used_packs': sum(len(packs[p]) for p in small_packs) + sum(len(packs[p]) for p in chosen)}


def listings(stock_bin, repo, snapshot_ids, work, env):
    """`ls --json` of every snapshot, read by stock: what the snapshots contain."""
    out = {}
    for sid in snapshot_ids:
        r = restic(stock_bin, repo, 'ls', '--json', sid, '--no-cache', work=work, env=env)
        out[sid] = (r.rc, r.out)
    return out
