#!/usr/bin/env python3
"""C11. Prune differential on a large index: stock prune against the tree's prune from the same starting repository.

Starting repositories: lib/bigindex.py (a copy of the judge's big-index fixture, 1.5 million blobs, plus partly used
packs of small blobs). Three variants put about 3,500 / 24,500 / 190,000 index entries into the packs to repack: the
three ways the final tree lists them (bounded collector, route table, fallback scan).
For every option set both binaries prune their own copy. Compared:
  the dry-run output (plan statistics) and the output of the real prune, as sorted lines without progress lines
  the pack files kept (same IDs before and after), the pack files removed, the set of blobs that sit in new packs
    (new pack IDs differ between any two runs: blobs are re-encrypted), the set of all blobs in the index
  `ls --json` of every snapshot, read by stock, against the listing before the prune
  stock `stats --mode raw-data --json`; stock `check --read-data` of both results
Option sets: --max-unused 0, unlimited, 5%; --max-repack-size; --repack-small (deprecated spelling) and
--repack-smaller-than; --repack-uncompressed; --repack-cacheable-only.
A difference is settled by pruning a third copy with stock: what also differs between two stock runs is not ours."""
import hashlib
import json
import os
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
import bigindex  # noqa: E402
from chk import (QUICK, Check, Server, chkrest_bin, chktool_bin, copy_repo, index_packs, normalize_output, prepare,  # noqa: E402
                 prepare_stock, repo_files, restic, rmtree)

c = Check('c11', 'prune differential on a large index (1.5M blobs), stock against the tree, seven option sets')
if not bigindex.available():
    c.note('the big-index fixture is not readable at %s' % bigindex.FIXTURE)
    sys.exit(c.finish('big-index fixture not available', verdict='SKIPPED'))
stock = prepare_stock()
cand = prepare(c.label, c.tree)
tool = chktool_bin()
chkrest_bin()
ws = c.work / 'ws'
R = 'routes'
PLAN = [(R, ['--max-unused', '0']), (R, ['--max-unused', 'unlimited']), (R, ['--max-unused', '5%']),
        (R, ['--max-unused', '0', '--max-repack-size', '2M']), (R, ['--repack-small']), (R, ['--repack-smaller-than', '4M']),
        (R, ['--repack-uncompressed']), (R, ['--repack-cacheable-only']), (R, []),
        ('collector', ['--max-unused', '0']), ('fallback', ['--max-unused', '0']), ('fallback', ['--repack-uncompressed'])]
if QUICK:
    PLAN = [(R, ['--max-unused', '0']), ('fallback', ['--max-unused', '0'])]
bases = {}
for k in sorted({k for k, _ in PLAN}):
    bases[k] = bigindex.build(c.work, stock.bin, tool, k, 'base-' + k)
    info = {key: bases[k][key] for key in ('small_files', 'ballast_partial', 'entries_in_partly_used_packs', 'packs', 'blobs')}
    c.log('starting repository %s: %s, %d snapshots' % (k, json.dumps(info), len(bases[k]['snapshots'])))
    c.numbers['base_' + k] = info
c.status('prunes', 0, len(PLAN))


def blob_set(packs, ids):
    return {(b['type'], b['id']) for p in ids for b in packs[p]}


def prune_copy(arm, binary, base, options, tag):
    repo = c.work / ('run-' + tag)
    copy_repo(base['repo'], repo)
    env = base['env']
    hide = [(str(repo), '<repo>')]
    before_packs = set(repo_files(repo, 'data'))
    dry = restic(binary, repo, 'prune', *options, '--dry-run', '--no-cache', work=ws, env=env, timeout=3600)
    real = restic(binary, repo, 'prune', *options, '--no-cache', work=ws, env=env, timeout=3600)
    after_packs = set(repo_files(repo, 'data'))
    result = {'arm': arm, 'dry_rc': dry.rc, 'rc': real.rc, 'seconds': round(real.secs, 1),
              'dry_text': normalize_output(dry.out + '\n' + dry.err, hide), 'text': normalize_output(real.out + '\n' + real.err, hide),
              'kept': after_packs & before_packs, 'removed': before_packs - after_packs, 'new': after_packs - before_packs,
              'raw': '%s\nrc=%d\n%s\n%s\n\n%s\nrc=%d\n%s\n%s' % (dry.cmd, dry.rc, dry.out, dry.err, real.cmd, real.rc, real.out, real.err)}
    try:
        packs = index_packs(stock.bin, repo, ws, env=env)
        result['indexed_packs'] = set(packs)
        result['blobs_all'] = [(b['type'], b['id']) for blobs in packs.values() for b in blobs]
        result['blobs_new'] = blob_set(packs, [p for p in result['new'] if p in packs])
    except Exception as exc:
        result['index_error'] = repr(exc)[:300]
        result['indexed_packs'], result['blobs_all'], result['blobs_new'] = set(), [], set()
    result['listings'] = bigindex.listings(stock.bin, repo, base['snapshots'], ws, env)
    stats = restic(stock.bin, repo, 'stats', '--mode', 'raw-data', '--json', '--no-cache', work=ws, env=env)
    result['stats'] = stats.out.strip()
    check = restic(stock.bin, repo, 'check', '--read-data', '--no-cache', work=ws, env=env, timeout=3600)
    result['check_rc'] = check.rc
    result['check_text'] = check.out[-600:] + check.err[-600:]
    rmtree(repo)
    return result


def stats_parts(text):
    """(what is stored, how it is stored) from `stats --mode raw-data --json`."""
    try:
        doc = json.loads(text)
    except ValueError:
        return (text, ), ()
    what = {k: doc.get(k) for k in ('total_blob_count', 'total_uncompressed_size', 'snapshots_count')}
    how = {k: (round(doc[k], 2) if isinstance(doc.get(k), float) else doc.get(k)) for k in ('total_size', 'compression_progress', 'compression_ratio')}
    return what, how


def compare(a, b):
    """Differences between two prune results (a is the reference)."""
    out = []
    if (a['dry_rc'], a['rc']) != (b['dry_rc'], b['rc']):
        out.append('exit codes (dry-run, prune): %s vs %s' % ((a['dry_rc'], a['rc']), (b['dry_rc'], b['rc'])))
    for key, label in (('dry_text', 'dry-run output'), ('text', 'prune output')):
        if a[key] != b[key]:
            out.append('%s differs: only first %r; only second %r' % (label, [x for x in a[key] if x not in b[key]][:3], [x for x in b[key] if x not in a[key]][:3]))
    for key, label in (('kept', 'packs kept'), ('removed', 'packs removed'), ('blobs_new', 'blobs in new packs')):
        if a[key] != b[key]:
            out.append('%s differ: %d vs %d (only first %d, only second %d)' % (label, len(a[key]), len(b[key]), len(a[key] - b[key]), len(b[key] - a[key])))
    if sorted(a['blobs_all']) != sorted(b['blobs_all']):
        out.append('indexed blobs differ: %d vs %d entries' % (len(a['blobs_all']), len(b['blobs_all'])))
    if a['indexed_packs'] != (a['kept'] | a['new']) or b['indexed_packs'] != (b['kept'] | b['new']):
        out.append('index and pack files disagree after prune (first: %d indexed / %d files; second: %d / %d)' % (
            len(a['indexed_packs']), len(a['kept'] | a['new']), len(b['indexed_packs']), len(b['kept'] | b['new'])))
    if a['listings'] != b['listings']:
        out.append('snapshot contents (ls --json by stock) differ after prune')
    sa, sb = stats_parts(a['stats']), stats_parts(b['stats'])
    if sa[0] != sb[0]:
        out.append('stock stats --mode raw-data differ (blob count, uncompressed size, snapshots): %s vs %s' % (sa[0], sb[0]))
    elif sa[1] != sb[1]:
        out.append('stored form differs (same blobs; stock stats --mode raw-data: stored size and compression): %s vs %s' % (sa[1], sb[1]))
    if a['check_rc'] != b['check_rc']:
        out.append('stock check --read-data: rc %d vs %d' % (a['check_rc'], b['check_rc']))
    return out


rows = []
stored_form = []
baseline = {k: bigindex.listings(stock.bin, bases[k]['repo'], bases[k]['snapshots'], ws, bases[k]['env']) for k in bases}
for n, (k, options) in enumerate(PLAN):
    name = '%s: prune %s' % (k, ' '.join(options) or '(defaults)')
    base = bases[k]
    s = prune_copy('stock', stock.bin, base, options, 'stock')
    t = prune_copy('tree', cand.bin, base, options, 'tree')
    problems = compare(s, t)
    if t['rc'] == 0 and t['check_rc'] != 0:
        problems.append('stock check --read-data rejects the repository the tree pruned: ' + t['check_text'][-300:])
    if t['rc'] == 0 and t['listings'] != baseline[k]:
        problems.append('snapshot contents changed by the tree prune')
    settled = []
    if problems:
        s2 = prune_copy('stock', stock.bin, base, options, 'stock2')
        own = compare(s, s2)
        settled = [p for p in problems if any(p.split(':')[0] == q.split(':')[0] for q in own)]
        problems = [p for p in problems if p not in settled]
    row = {'case': name, 'rc': {'stock': s['rc'], 'tree': t['rc']}, 'seconds': {'stock': s['seconds'], 'tree': t['seconds']},
           'packs': {'kept': len(s['kept']), 'removed': len(s['removed']), 'new_stock': len(s['new']), 'new_tree': len(t['new'])},
           'blobs_in_new_packs': len(s['blobs_new']), 'stock_check_rc': {'stock': s['check_rc'], 'tree': t['check_rc']},
           'problems': problems, 'also_between_two_stock_runs': settled}
    rows.append(row)
    c.log('%s: %s' % (name, json.dumps({key: row[key] for key in ('rc', 'seconds', 'packs', 'blobs_in_new_packs', 'stock_check_rc')})))
    for text in settled:
        c.note('%s: differs between two STOCK runs as well (not ours): %s' % (name, text[:200]))
    form = [p for p in problems if p.startswith('stored form differs')]
    if form:
        stored_form.append((name, form[0]))
        problems = [p for p in problems if p not in form]
        row['problems'] = problems
        row['stored_form_differs'] = form[0]
    if problems:
        ev = c.res / 'evidence' / ('case%02d' % n)
        ev.mkdir(parents=True, exist_ok=True)
        (ev / 'stock.txt').write_text(s['raw'])
        (ev / 'tree.txt').write_text(t['raw'])
        (ev / 'differences.txt').write_text(name + '\n' + '\n'.join(problems) + '\n')
        c.fail('prune %s: the tree and stock disagree: %s' % (name, problems[0][:500]), evidence=ev, stock='stock is the reference; a second stock run agreed with the first',
               repro='restic -r <copy of the starting repository> prune %s   (starting repository: lib/bigindex.py, variant %s)' % (' '.join(options), k))
    c.tick()
if stored_form:
    c.fail('after a prune by the tree the same blobs are stored in another form than after a prune by stock, in %d of %d prunes; first: %s: %s' % (
        len(stored_form), len(rows), stored_form[0][0], stored_form[0][1][:400]), evidence=c.res / 'prunes.json',
        stock='two stock runs agree with each other',
        repro='restic -r <copy> prune --max-unused 0 (both binaries), then restic-stock -r <copy> stats --mode raw-data --json')

# ---- does `prune --repack-uncompressed` come to rest?
# Each binary first prunes with --max-unused 0 (everything partly used is rewritten by that binary), then
# `prune --repack-uncompressed --dry-run` is asked what is left to do (expected: nothing, the repository was written
# compressed), then `prune --repack-uncompressed` runs for real and is asked again.
c.status('repack-uncompressed twice')
TO_REPACK = 'to repack:'


def blobs_to_repack(text):
    for line in text.splitlines():
        if line.strip().startswith(TO_REPACK):
            return int(line.split()[2])
    return None


twice = {}
for arm, binary in (('stock', stock.bin), ('tree', cand.bin)):
    repo = c.work / ('twice-' + arm)
    copy_repo(bases[R]['repo'], repo)
    env = bases[R]['env']
    zero = restic(binary, repo, 'prune', '--max-unused', '0', '--no-cache', work=ws, env=env, timeout=3600)
    ask1 = restic(binary, repo, 'prune', '--repack-uncompressed', '--max-unused', '0', '--dry-run', '--no-cache', work=ws, env=env, timeout=3600)
    first = restic(binary, repo, 'prune', '--repack-uncompressed', '--max-unused', '0', '--no-cache', work=ws, env=env, timeout=3600)
    second = restic(binary, repo, 'prune', '--repack-uncompressed', '--max-unused', '0', '--dry-run', '--no-cache', work=ws, env=env, timeout=3600)
    by_stock = restic(stock.bin, repo, 'prune', '--repack-uncompressed', '--max-unused', '0', '--dry-run', '--no-cache', work=ws, env=env, timeout=3600)
    twice[arm] = {'prune_rc': zero.rc, 'to_repack_after_own_prune': blobs_to_repack(ask1.out), 'repack_uncompressed_rc': first.rc,
                  'second_to_repack': blobs_to_repack(second.out), 'second_to_repack_as_stock_sees_it': blobs_to_repack(by_stock.out)}
    (c.res / ('repack-uncompressed-twice-%s.txt' % arm)).write_text('\n\n'.join('%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err) for r in (zero, ask1, first, second, by_stock)))
    rmtree(repo)
c.log('prune --repack-uncompressed, then again: %s' % json.dumps(twice))
c.numbers['repack_uncompressed_twice'] = twice
if (twice['tree']['second_to_repack'] or 0) > (twice['stock']['second_to_repack'] or 0) or (
        twice['tree']['second_to_repack_as_stock_sees_it'] or 0) > (twice['stock']['second_to_repack_as_stock_sees_it'] or 0):
    c.fail('`prune --repack-uncompressed` does not come to rest with the tree: right after one such prune a second one still wants to repack %s blobs (stock binary on the same repository: %s; stock after its own prune: %s)' % (
        twice['tree']['second_to_repack'], twice['tree']['second_to_repack_as_stock_sees_it'], twice['stock']['second_to_repack']),
        evidence=c.res / 'repack-uncompressed-twice-tree.txt', stock='stock: second run repacks %s blobs' % twice['stock']['second_to_repack'],
        repro='restic -r REPO prune --repack-uncompressed --max-unused 0 && restic -r REPO prune --repack-uncompressed --max-unused 0 --dry-run   (REPO holds incompressible data blobs of 512 KiB or more)')
elif (twice['tree']['to_repack_after_own_prune'] or 0) > (twice['stock']['to_repack_after_own_prune'] or 0):
    c.note('after `prune --max-unused 0` by the tree, `prune --repack-uncompressed` finds %s blobs to repack (after stock\'s prune: %s): the tree wrote blobs uncompressed that stock writes compressed; one --repack-uncompressed run settles it' % (
        twice['tree']['to_repack_after_own_prune'], twice['stock']['to_repack_after_own_prune']))
# ---- a KEPT index file is damaged after prune has loaded the index and before it rewrites it
# Stock reloads every index file in the rewrite step and aborts when one does not hash to its name. The tree keeps
# full index files without removed packs as they are; since the red-team fixes it reads them too and compares the
# hash. Here the largest index file (ballast: full, all its packs stay) is damaged by the REST server at the moment
# the first repacked pack is uploaded, i.e. after the load and before the rewrite. Both binaries must abort, and
# stock `check` must say the same about both repositories.
c.status('kept index file damaged')
MASK64 = re.compile(r'[0-9a-f]{64}')
MASK8 = re.compile(r'\b[0-9a-f]{8}\b')
kb = bigindex.build(c.work, stock.bin, tool, 'keepballast', 'base-keepballast')
victim = max(repo_files(kb['repo'], 'index'), key=lambda n: (kb['repo'] / 'index' / n).stat().st_size)
kept_case = {'victim_index_bytes': (kb['repo'] / 'index' / victim).stat().st_size, 'index_files': len(repo_files(kb['repo'], 'index'))}
for arm, binary in (('stock', stock.bin), ('tree', cand.bin)):
    repo = c.work / ('kept-' + arm)
    copy_repo(kb['repo'], repo)
    path = repo / 'index' / victim
    srv = Server(repo, damage_file=path, damage_trigger='POST data')
    r = restic(binary, srv.url, 'prune', '--max-unused', '0', '--pack-size', '4', '--no-cache', work=ws, env=kb['env'], timeout=1800)
    stats = srv.stop()
    for stray in (repo / 'data').glob('*/*rest-tmp*'):
        stray.unlink()
    damaged = hashlib.sha256(path.read_bytes()).hexdigest() != victim if path.is_file() else None
    restic(stock.bin, repo, 'unlock', work=ws, env=kb['env'])
    chk = restic(stock.bin, repo, 'check', '--no-cache', work=ws, env=kb['env'], timeout=1800)
    lines = sorted({MASK8.sub('<id>', MASK64.sub('<id>', line)) for line in normalize_output(chk.out + '\n' + chk.err, [(str(repo), '<repo>')])})
    kept_case[arm] = {'prune_rc': r.rc, 'prune_seconds': round(r.secs, 1), 'file_damaged_while_prune_ran': damaged, 'trigger_fired': stats.get('damage_done'),
                      'victim_still_there': path.is_file(), 'prune_names_the_file': victim[:8] in (r.out + r.err),
                      'index_files_after': len(repo_files(repo, 'index')), 'stock_check_rc': chk.rc, 'stock_check_distinct_lines': lines,
                      'prune_last_lines': [x[:200] for x in (r.out + r.err).strip().splitlines()[-3:]]}
    (c.res / ('kept-index-damaged-%s.txt' % arm)).write_text('%s\nrc=%d\n%s\n%s\n\n%s\nrc=%d\n%s\n%s' % (r.cmd, r.rc, r.out, r.err, chk.cmd, chk.rc, chk.out, chk.err))
    c.log('kept index file damaged before the rewrite, %s: %s' % (arm, json.dumps({k: v for k, v in kept_case[arm].items() if k != 'stock_check_distinct_lines'})))
    rmtree(repo)
c.numbers['kept_index_damaged'] = kept_case
ks, kt = kept_case['stock'], kept_case['tree']
if not ks['file_damaged_while_prune_ran'] or not kt['file_damaged_while_prune_ran']:
    c.note('kept-index case: the index file was not damaged during the prune of %s (no pack upload happened): the case says nothing' % (
        'stock' if not ks['file_damaged_while_prune_ran'] else 'the tree'))
elif (ks['prune_rc'] != 0) != (kt['prune_rc'] != 0) or ks['stock_check_rc'] != kt['stock_check_rc'] or ks['stock_check_distinct_lines'] != kt['stock_check_distinct_lines']:
    only_s = [x for x in ks['stock_check_distinct_lines'] if x not in kt['stock_check_distinct_lines']][:3]
    only_t = [x for x in kt['stock_check_distinct_lines'] if x not in ks['stock_check_distinct_lines']][:3]
    c.fail('a kept index file damaged between load and rewrite: stock prune exits %d, the tree exits %d; stock check afterwards exits %d / %d; check lines only after stock %r, only after the tree %r' % (
        ks['prune_rc'], kt['prune_rc'], ks['stock_check_rc'], kt['stock_check_rc'], only_s, only_t), evidence=c.res / 'kept-index-damaged-tree.txt',
        stock='stock aborts with exit %d: %s' % (ks['prune_rc'], ks['prune_last_lines'][-1:] ),
        repro='chkrest -damage-file REPO/index/<largest> -damage-trigger "POST data"; restic -r rest:... prune --max-unused 0; restic-stock -r REPO check')
c.tick()
(c.res / 'prunes.json').write_text(json.dumps(rows, indent=1) + '\n')
c.numbers['prunes'] = len(rows)
c.numbers['prunes_with_differences'] = sum(1 for r in rows if r['problems'])
c.numbers['rows'] = rows
summary = '%d prune pairs on the 1.5M-blob repository (variants %s; %s index entries in the packs to repack): %d differ from stock; stock check --read-data of every tree result: %s; kept index file damaged before the rewrite: prune exit stock %s / tree %s, stock check exit %s / %s' % (
    len(rows), sorted(bases), sorted(b['entries_in_partly_used_packs'] for b in bases.values()), c.numbers['prunes_with_differences'],
    'pass' if all(r['stock_check_rc']['tree'] == r['stock_check_rc']['stock'] for r in rows) else 'DIFFERS',
    ks['prune_rc'], kt['prune_rc'], ks['stock_check_rc'], kt['stock_check_rc'])
sys.exit(c.finish(summary))
