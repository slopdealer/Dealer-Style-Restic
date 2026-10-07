#!/usr/bin/env python3
"""C2. `go test -race` (cgo, gcc) over the packages the reviews named, with the reviewers' witness tests copied
into a scratch copy of the tree; `cmd/restic` a second time with RESTIC_TEST_LOCK_WAIT=200ms (the production lock
wait). The same packages of stock run under -race as the control.

A witness that cannot be compiled in this tree (its names already exist there as regression tests, or an API it
used changed) is left out and listed. A failing test counts against the tree unless stock fails the same way."""
import json
import os
import shutil
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
import gotest  # noqa: E402
from chk import CHECKS, QUICK, STOCK_TREE, Check, copy_tree, envint, go_env, run, short_tmp  # noqa: E402

WITNESS = {
    'storage': {'chunker_streampack_witness_test.go': 'internal/repository', 'compression_witness_test.go': 'internal/repository',
                'index_witness_test.go': 'internal/repository/index', 'kdf_witness_test.go': 'internal/repository/crypto',
                'lock_dwell_witness_test.go': 'internal/repository', 'repair_packs_conn1_witness_test.go': 'internal/repository'},
    'backup': {'chunker_witness_test.go': 'internal/repository', 'file_saver_witness_test.go': 'internal/archiver',
               'kdf_witness_test.go': 'internal/repository/crypto', 'lock_dwell_stale_index_witness_test.go': 'internal/repository',
               'node_tree_witness_test.go': 'internal/data', 'open_key_witness_test.go': 'internal/global',
               'path_stat_linux_witness_test.go': 'internal/fs', 'status_witness_test.go': 'internal/ui/termstatus'},
    'readside': {'cmd_lock_wait_stale_witness_test.go': 'cmd/restic', 'data_tree_name_witness_test.go': 'internal/data',
                 'repository_stream_coalesced_witness_test.go': 'internal/repository',
                 'restorer_hardlink_linkcount_witness_test.go': 'internal/restorer'},
}
# Witnesses that carry the pre-repair caller INSIDE the test: the test itself preloads the index as wait work at
# repository level and keeps it after confirmation. They show that the repository API permits the hazard, not that a
# command still has it; the commands are tested by C3 (real binaries) and by the R1 regression tests in cmd/restic.
MODELS_OLD_CALLER = {
    'TestWitnessLockDwellPreloadedIndexIsStale': 'storage witness, repository level: the test plays the old cmd_backup (it keeps the preloaded index without the comparison that repair R1 added in cmd/restic)',
    'TestWitnessLockDwellStaleIndex': 'backup witness, repository level: same construction',
}
GROUP_INTERNAL = ['./internal/repository/...', './internal/checker/...', './internal/restic/...', './internal/backend/sema/...',
                  './internal/backend/retry/...', './internal/archiver/...', './internal/data/...', './internal/global/...',
                  './internal/restorer/...', './internal/walker/...', './internal/ui/termstatus/...', './internal/ui/backup/...',
                  './internal/fs/...']


def install_witnesses(c, src):
    """Copy the witnesses in; drop the ones the tree cannot compile. Returns {file: 'in' | reason}."""
    state = {}
    by_pkg = {}
    for area, files in WITNESS.items():
        for name, pkg in files.items():
            source = CHECKS / 'witness' / area / name
            if not source.is_file() or not (src / pkg).is_dir():
                state['%s/%s' % (area, name)] = 'missing source or package'
                continue
            target = src / pkg / ('zzw_%s_%s' % (area, name))
            shutil.copyfile(source, target)
            by_pkg.setdefault(pkg, []).append(target)
            state['%s/%s' % (area, name)] = 'in'
    env = go_env(cgo=False)
    for pkg, targets in sorted(by_pkg.items()):
        for _ in range(len(targets) + 1):
            rc, out, err, _s = run(['go', 'test', '-count=1', '-run', '^$', '-tags', gotest.TAGS, './' + pkg], env=env, cwd=src, timeout=1800)
            if rc == 0:
                break
            text = out + err
            culprit = None
            for target in targets:
                if target.exists() and target.name in text:
                    culprit = target
                    break
            if culprit is None:
                c.note('package %s does not compile even without a witness to blame: %s' % (pkg, text.strip()[-300:]))
                break
            reason = [line for line in text.splitlines() if culprit.name in line][:2]
            key = culprit.name[len('zzw_'):].replace('_', '/', 1)
            state[key] = 'left out: ' + ' ; '.join(r.strip()[-200:] for r in reason)
            culprit.unlink()
            c.log('witness %s left out of %s: %s' % (culprit.name, pkg, state[key]))
    return state


def race_run(c, src, name, packages, env=None, count=1, timeout_min=60):
    out = c.res / (name + '.json')
    c.log('%s: go test -race -count=%d %s' % (name, count, ' '.join(packages)))
    rc, seconds = gotest.go_test(src, packages, out, race=True, count=count, timeout_min=timeout_min,
                                 tmp=short_tmp(c.label, 'c02', name), env=env)
    c.log('%s done rc=%d in %d s' % (name, rc, seconds))
    return gotest.parse(out)


c = Check('c02', 'go test -race with the reviewers witnesses, against stock')
rc, out, err, _ = run(['gcc', '--version'])
if rc != 0:
    c.note('gcc is missing: the race detector cannot run')
    sys.exit(c.finish('gcc missing, race detector not run', verdict='SKIPPED'))
src = c.work / 'src'
copy_tree(c.tree, src)
witness_state = install_witnesses(c, src)
(c.res / 'witness-files.json').write_text(json.dumps(witness_state, indent=1) + '\n')
count = envint('CHK_RACE_COUNT', 1)
runs = [('cand-internal', GROUP_INTERNAL, None, count), ('cand-cmd-wait0', ['./cmd/restic'], None, 1),
        ('cand-cmd-wait200ms', ['./cmd/restic'], {'RESTIC_TEST_LOCK_WAIT': '200ms'}, 1)]
if QUICK:
    runs = [('cand-internal', ['./internal/backend/sema/...', './internal/walker/...'], None, 1)]
stock_cache = CHECKS / 'results' / '_stock' / 'c02'
stock_runs = [('stock-internal', GROUP_INTERNAL), ('stock-cmd-wait0', ['./cmd/restic'])]
c.status('race', 0, len(runs) + (0 if QUICK else len(stock_runs)))
stock = {}
if not QUICK:
    stock_src = c.work / 'stock-src'
    copy_tree(STOCK_TREE, stock_src)
    for name, packages in stock_runs:
        out = stock_cache / (name + '.json')
        if not (stock_cache / (name + '.done')).is_file():
            c.log('%s: go test -race (control)' % name)
            rc, seconds = gotest.go_test(stock_src, packages, out, race=True, timeout_min=60, tmp=short_tmp(c.label, 'c02', name))
            (stock_cache / (name + '.done')).write_text('rc=%d seconds=%d\n' % (rc, seconds))
            c.log('%s done rc=%d in %d s' % (name, rc, seconds))
        stock[name] = gotest.parse(out)
        c.numbers[name] = gotest.counts(stock[name])
        c.tick()
ours = shared = witness_fail = 0
witness_results = {}
for name, packages, env, n in runs:
    parsed = race_run(c, src, name, packages, env=env, count=n, timeout_min=envint('CHK_RACE_TIMEOUT_MIN', 90))
    c.numbers[name] = gotest.counts(parsed)
    c.log('%s %s' % (name, json.dumps(c.numbers[name])))
    if not parsed:
        c.fail('%s: no result at all' % name, evidence=c.res / (name + '.json.stderr'))
    control = stock.get('stock-internal' if 'internal' in name else 'stock-cmd-wait0', {})
    control_fail = {(p, t): k for p, t, k in gotest.failures(control)}
    for package, pkg in parsed.items():
        for test, action in pkg['tests'].items():
            if test.startswith('TestWitness') and '/' not in test:
                witness_results['%s %s %s' % (name, package.replace('github.com/restic/restic/', ''), test)] = action
    lines = []
    for package, test, kind in gotest.failures(parsed):
        evidence = c.res / ('fail-%s-%s-%s.txt' % (name, gotest.safe(package), gotest.safe(test or 'PACKAGE')))
        evidence.write_text(gotest.test_text(parsed, package, test, limit=60000))
        short = package.replace('github.com/restic/restic', '.')
        repro = 'cd <tree copy with witnesses> && %sCGO_ENABLED=1 go test -race -count=1 -tags "selfupdate disable_grpc_modules" -run "^%s$" %s' % (
            ''.join('%s=%s ' % kv for kv in (env or {}).items()), test or '.', short)
        if (package, test) in control_fail:
            shared += 1
            lines.append('NOT OURS %s %s %s [%s]: stock fails it too under -race (%s)' % (name, short, test, kind, control_fail[(package, test)]))
            continue
        if test in MODELS_OLD_CALLER and 'race' not in kind:
            lines.append('WITNESS (models the caller itself) %s %s %s [%s]' % (name, short, test, kind))
            c.note('%s fails as written: %s' % (test, MODELS_OLD_CALLER[test]))
            continue
        if test.startswith('TestWitness') and 'race' not in kind:
            witness_fail += 1
            lines.append('WITNESS FAILS %s %s %s [%s]' % (name, short, test, kind))
            c.fail('reviewer witness still fails: %s %s (%s)' % (short, test, name), evidence=evidence, repro=repro,
                   stock='witness: by design it passes on stock order / fails where the finding is present')
            continue
        ours += 1
        lines.append('FAIL %s %s %s [%s]' % (name, short, test, kind))
        in_stock = control.get(package, {}).get('tests', {}).get(test) if test else control.get(package, {}).get('result')
        c.fail('%s: %s %s [%s]' % (name, short, test or '(package)', kind), evidence=evidence, repro=repro,
               stock='stock -race: %s' % (in_stock or 'test not in stock'))
    (c.res / ('compare-%s.txt' % name)).write_text('\n'.join(lines) + '\n')
    for line in lines:
        c.log(line)
    c.tick()
(c.res / 'witness-results.json').write_text(json.dumps(witness_results, indent=1, sort_keys=True) + '\n')
races = sum(v.get('races', 0) for k, v in c.numbers.items() if k.startswith('cand'))
tests = sum(v.get('tests', 0) for k, v in c.numbers.items() if k.startswith('cand'))
left_out = sum(1 for v in witness_state.values() if v != 'in')
summary = '%d tests under -race in %d runs; packages with DATA RACE: %d; failures ours=%d, witness=%d, also on stock=%d; witness files in=%d left out=%d; witness tests pass=%d fail=%d' % (
    tests, len(runs), races, ours, witness_fail, shared, len(witness_state) - left_out, left_out,
    sum(1 for v in witness_results.values() if v == 'pass'), sum(1 for v in witness_results.values() if v == 'fail'))
sys.exit(c.finish(summary))
