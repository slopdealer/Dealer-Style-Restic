#!/usr/bin/env python3
"""X06 (diagnostic for C6's repository-size comparison). Where do the bytes go when two binaries store the same input?

Inputs: the judge's main corpus with default options (one backup), and the generated corpus of C6 in the
configurations named in CHK_X06_CONFIGS (two backups with the edits of C6 in between).
For every input ONE repository is initialised and every run starts from a copy of it: same keys and, what matters,
the same chunker polynomial, so the same file content gives the same blob IDs in every run. (C6 lets every binary
initialise its own repository; two repositories then cut large files at different places.)
Runs: every binary twice (how much does a binary differ from itself?), stock once more on a second init (how much
does the polynomial alone move the size?).
Per run, from the index read by stock and the pack files on disk: pack files and their bytes; index entries, distinct
blob IDs, entries beyond the first of an ID (duplicates) and their bytes, for data and tree blobs; blobs stored raw;
bytes that are not blob content (pack headers). Against the first stock run: IDs only on one side, and for IDs on
both sides whether the stored length is equal. For duplicated data blobs: how many files reference them.
Binaries: stock, the tree, the unrepaired tree, and CHK_X06_EXTRA="name=/path/to/tree,..."."""
import json
import os
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import (LEAGUE, QUICK, Check, copy_repo, gen_corpus, index_packs, must, mutate_corpus, prepare, prepare_before,  # noqa: E402
                 prepare_stock, restic, rmtree)

c = Check('x06', 'repository size anatomy: duplicates, pack overhead, stored length per blob, noise of a binary against itself')
stock = prepare_stock()
arms = [('stock', stock.bin), ('tree', prepare(c.label, c.tree).bin)]
try:
    arms.append(('unrepaired', prepare_before().bin))
except Exception as exc:
    c.note('unrepaired tree not built: %r' % (exc,))
for item in [x for x in os.environ.get('CHK_X06_EXTRA', '').split(',') if '=' in x]:
    name, path = item.split('=', 1)
    try:
        arms.append((name, prepare('_x_' + name, path).bin))
    except Exception as exc:
        c.note('%s (%s) not built: %r' % (name, path, exc))
REPS = int(os.environ.get('CHK_X06_REPS', '2'))
ws = c.work / 'ws'
CONFIGS = [x for x in os.environ.get('CHK_X06_CONFIGS', 'off:128,auto:128,off:4').split(',') if x]
JUDGE = os.environ.get('CHK_X06_JUDGE', 'workloads').split(',') if not QUICK else []
if QUICK:
    CONFIGS, REPS = ['off:128'], 2


def anatomy(repo):
    packs = index_packs(stock.bin, repo, ws)
    files = {p.name: p.stat().st_size for p in (repo / 'data').glob('*/*') if p.is_file()}
    seen = {}
    out = {'pack_files': len(files), 'pack_bytes': sum(files.values()), 'packs_in_index': len(packs),
           'pack_files_not_in_index': len(set(files) - set(packs))}
    stats = {t: {'entries': 0, 'distinct': 0, 'duplicate_entries': 0, 'duplicate_bytes': 0, 'stored_bytes': 0, 'raw_entries': 0} for t in ('data', 'tree')}
    duplicated = defaultdict(int)
    for pack_id, blobs in packs.items():
        for b in blobs:
            s = stats[b['type']]
            s['entries'] += 1
            s['stored_bytes'] += b['length']
            if not b.get('uncompressed_length'):
                s['raw_entries'] += 1
            key = (b['type'], b['id'])
            if key in seen:
                s['duplicate_entries'] += 1
                s['duplicate_bytes'] += b['length']
                duplicated[key] += 1
            else:
                seen[key] = b['length']
                s['distinct'] += 1
    out.update(stats)
    out['bytes_that_are_not_blobs'] = sum(files[p] for p in packs if p in files) - stats['data']['stored_bytes'] - stats['tree']['stored_bytes']
    return out, seen, duplicated


def files_per_blob(repo, wanted):
    """How many file nodes of the latest snapshot reference each wanted data blob."""
    if not wanted:
        return {}
    count = defaultdict(int)
    out = must(restic(stock.bin, repo, 'ls', '--json', 'latest', '--no-cache', work=ws)).out
    names = defaultdict(list)
    for line in out.splitlines():
        if '"content"' not in line:
            continue
        node = json.loads(line)
        for blob in node.get('content') or []:
            if blob in wanted:
                count[blob] += 1
                if len(names[blob]) < 3:
                    names[blob].append(node.get('path', ''))
    return count, names


def run_input(title, init_flags, backups, extra_flags):
    """backups: list of functions that put the source in place and return (cwd, path argument)."""
    base = c.work / 'init-a'
    other = c.work / 'init-b'
    for repo in (base, other):
        rmtree(repo)
        must(restic(stock.bin, repo, 'init', *init_flags, work=ws))
    plan = [(arm, binary, rep, base) for arm, binary in arms for rep in range(REPS)] + [('stock, second init', stock.bin, 0, other)]
    rows = []
    reference = None
    for arm, binary, rep, start in plan:
        repo = c.work / 'run'
        copy_repo(start, repo)
        seconds = 0.0
        for prepare_source in backups:
            cwd, arg = prepare_source()
            r = restic(binary, repo, 'backup', '--no-cache', '--host', 'h', *extra_flags, arg, work=ws, cwd=cwd, timeout=3600)
            seconds += r.secs
            if r.rc != 0:
                c.note('%s, %s: backup exits %d: %s' % (title, arm, r.rc, r.brief(200)))
        a, seen, duplicated = anatomy(repo)
        row = {'input': title, 'binary': arm, 'run': rep + 1, 'backup_seconds': round(seconds, 1), **a}
        if arm == 'stock' and rep == 0:
            reference = seen
        elif start == base and reference is not None:
            both = set(seen) & set(reference)
            differ = [k for k in both if seen[k] != reference[k]]
            row['against_first_stock_run'] = {'ids_only_here': len(set(seen) - set(reference)), 'ids_only_in_stock': len(set(reference) - set(seen)),
                                              'ids_in_both': len(both), 'stored_length_differs': len(differ),
                                              'stored_bytes_here_minus_stock_for_those': sum(seen[k] - reference[k] for k in differ)}
        dup_data = {k[1] for k in duplicated if k[0] == 'data'}
        if dup_data:
            got = files_per_blob(repo, dup_data)
            count, names = got
            row['duplicated_data_blobs'] = {'ids': len(dup_data), 'referenced_by_two_or_more_files': sum(1 for b in dup_data if count.get(b, 0) >= 2),
                                            'referenced_by_one_file': sum(1 for b in dup_data if count.get(b, 0) == 1),
                                            'examples': [names[b] for b in sorted(dup_data)[:3]]}
        rows.append(row)
        c.log(json.dumps(row))
        rmtree(repo)
        c.tick()
    return rows


all_rows = []
c.status('runs', 0, (len(JUDGE) + len(CONFIGS)) * (len(arms) * REPS + 1))
for folder in JUDGE:
    corpus_dir = LEAGUE / folder / 'corpus-v1'
    if not corpus_dir.is_dir():
        c.note('judge corpus %s not readable' % folder)
        continue
    all_rows += run_input('judge corpus %s, default options' % folder, [], [lambda d=corpus_dir: (d, '.')], [])
generated = c.work / 'generated'
for config in CONFIGS:
    compression, pack = config.split(':')
    state = {'n': 0}

    def first():
        rmtree(generated)
        gen_corpus(generated, seed=6, small=300, text_files=4, text_mb=4, big=(9, 6, 13), special=True)
        return generated.parent, generated.name

    def second():
        mutate_corpus(generated, seed=61)
        return generated.parent, generated.name
    all_rows += run_input('generated corpus, v2, compression %s, pack-size %s, two backups' % (compression, pack), ['--repository-version', '2'],
                          [first, second], ['--compression', compression, '--pack-size', pack])
(c.res / 'runs.json').write_text(json.dumps(all_rows, indent=1) + '\n')
lines = ['| input | binary | run | pack bytes | packs | data entries | distinct | duplicate entries | duplicate bytes | tree entries | tree dup | raw data | not blob bytes |',
         '|---|---|---|---|---|---|---|---|---|---|---|---|---|']
for r in all_rows:
    lines.append('| %s | %s | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d | %d |' % (
        r['input'], r['binary'], r['run'], r['pack_bytes'], r['pack_files'], r['data']['entries'], r['data']['distinct'], r['data']['duplicate_entries'],
        r['data']['duplicate_bytes'], r['tree']['entries'], r['tree']['duplicate_entries'], r['data']['raw_entries'], r['bytes_that_are_not_blobs']))
(c.res / 'anatomy.md').write_text('\n'.join(lines) + '\n')
for line in lines:
    c.log(line)
c.numbers['rows'] = all_rows
sys.exit(c.finish('%d runs; see anatomy.md and runs.json' % len(all_rows), verdict='PASS'))
