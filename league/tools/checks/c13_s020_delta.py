#!/usr/bin/env python3
"""C13. What the review of the last season (REVIEW-s020.md, section 6) asks of the tree that keeps that season's
prune and index work.

1. Which of the season's lock-wait mechanisms are in the tree (file list): backup compression preparation, check
   reading a pack during the wait, restore tree preparation. The final tree is meant to drop all three.
2. The tree's own Linux-only tests (every Test in a *_linux_test.go file or in a test file with a linux build
   constraint), run as root.
3. The S020 reviewers' witnesses (witness/s020), copied into a scratch copy; a witness whose mechanism is not in the
   tree does not compile and is left out (listed). On linux/amd64 and GOARCH=386.
4. `go test -race` over internal/repository/index, internal/data and the prune / repack / index-listing tests of
   internal/repository, with those witnesses.
   Then the tree's R1 regression tests with 3,000 randomized schedules (RESTIC_R1_SCHEDULES).
5. Real binaries under strace: a backup of a directory that holds a FIFO and a device node (major 1 minor 3, the
   null device; never a real watchdog). The tree must open neither (stock opens neither). The S020 incumbent, when
   it is on the box, is run as the positive control: the instrument must see its opens."""
import json
import os
import re
import shutil
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
import gotest  # noqa: E402
from chk import (CHECKS, LEAGUE, QUICK, Check, copy_tree, go_env, must, prepare, prepare_stock, restic, restic_env, rmtree,  # noqa: E402
                 run, short_tmp, write_file)

c = Check('c13', 'S020 delta: dropped mechanisms, Linux-only tests, S020 witnesses, -race on index/prune/data, strace')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
src = c.work / 'src'
copy_tree(c.tree, src)
c.status('files', 0, 7)

# ---------------------------------------------------------------- 1. mechanisms present
MECHANISMS = {
    'backup compression preparation (review: DROP)': ['cmd/restic/backup_compression_wait.go', 'cmd/restic/backup_compression_source_linux.go',
                                                       'cmd/restic/backup_compression_index.go', 'internal/repository/compression_preparation.go'],
    'check reads a pack during the lock wait (review: DROP)': ['cmd/restic/check_wait_pack.go', 'internal/repository/check_wait_pack.go',
                                                                 'internal/repository/check_wait_identity_linux.go', 'internal/repository/check_wait_watch_linux.go',
                                                                 'internal/backend/retry/prepared_range.go'],
    'restore tree preparation (review: DROP)': ['internal/repository/restore_wait_reader.go', 'internal/restorer/restore_wait_trees.go'],
    'restore snapshot preload (review: keep with the R1 port)': ['cmd/restic/restore_snapshot_wait.go'],
    'prune and index work (review: keep)': ['internal/repository/index/repack_routes.go', 'internal/repository/index/repack_fallback.go',
                                             'internal/repository/index/associated_count_validate.go', 'internal/repository/prune_pack_run.go'],
    'metadata tree decoder (review: keep with the R7 rule)': ['internal/data/tree_metadata.go'],
}
present = {name: [f for f in files if (src / f).is_file()] for name, files in MECHANISMS.items()}
c.numbers['mechanism_files_present'] = present
for name, files in present.items():
    c.log('%s: %s' % (name, ', '.join(files) if files else 'not in this tree'))
dropped_but_present = [name for name, files in present.items() if 'DROP' in name and files]
for name in dropped_but_present:
    c.note('%s is in this tree (%d files): its review items apply, see the witness results below' % (name, len(present[name])))
c.tick()

# ---------------------------------------------------------------- 2. the tree's own Linux-only tests
linux_tests = {}
for path in sorted(src.rglob('*_test.go')):
    if path.name.startswith('zzw_'):
        continue
    text = path.read_text(encoding='utf-8', errors='replace')
    head = text[:600]
    if path.name.endswith('_linux_test.go') or re.search(r'^//go:build .*\blinux\b', head, re.M):
        if re.search(r'^//go:build .*!linux', head, re.M):
            continue
        names = re.findall(r'^func (Test\w+)\(', text, re.M)
        if names:
            linux_tests.setdefault('./' + str(path.parent.relative_to(src)), []).extend(names)
out = c.res / 'linux-only.json'
total = sum(len(v) for v in linux_tests.values())
c.log('Linux-only tests of the tree: %d in %d packages' % (total, len(linux_tests)))
state = {}
for package, names in sorted(linux_tests.items()):
    one = c.res / ('linux-only-%s.json' % gotest.safe(package))
    gotest.go_test(src, [package], one, run_filter='^(%s)$' % '|'.join(sorted(set(names))), tmp=short_tmp(c.label, 'c13', 'linux', package), extra=['-v'])
    parsed = gotest.parse(one)
    for p in parsed.values():
        for t, a in p['tests'].items():
            if '/' not in t:
                state['%s %s' % (package, t)] = a
    for pkg, test, kind in gotest.failures(parsed):
        ev = c.res / ('fail-linux-%s-%s.txt' % (gotest.safe(package), gotest.safe(test or 'PACKAGE')))
        ev.write_text(gotest.test_text(parsed, pkg, test, limit=60000))
        c.fail('Linux-only test %s %s [%s]' % (package, test or '(package)', kind), evidence=ev, stock='the test is the league\'s own (stock does not have it) unless C1 lists it under "also on stock"',
               repro='go test -count=1 -v -run "^%s$" %s' % (test or '.', package))
(c.res / 'linux-only-tests.json').write_text(json.dumps(state, indent=1, sort_keys=True) + '\n')
c.numbers['linux_only_tests'] = {'found': total, 'pass': sum(1 for v in state.values() if v == 'pass'), 'fail': sum(1 for v in state.values() if v == 'fail'),
                                 'skip': sum(1 for v in state.values() if v == 'skip')}
c.tick()

# ---------------------------------------------------------------- 3. the S020 witnesses
PACKAGE_OF = {'cmd': 'cmd/restic', 'data': 'internal/data', 'index': 'internal/repository/index', 'repository': 'internal/repository'}
witness_state = {}
by_pkg = {}
for path in sorted((CHECKS / 'witness' / 's020').glob('*_test.go')):
    package = PACKAGE_OF.get(path.name.split('_')[0])
    if not package or not (src / package).is_dir():
        witness_state[path.name] = 'no package for it in this tree'
        continue
    target = src / package / ('zzw_s020_' + path.name)
    shutil.copyfile(path, target)
    by_pkg.setdefault(package, []).append(target)
    witness_state[path.name] = 'in'
for package, targets in sorted(by_pkg.items()):
    for _ in range(len(targets) + 1):
        rc, o, e, _s = run(['go', 'test', '-count=1', '-run', '^$', '-tags', gotest.TAGS, './' + package], env=go_env(), cwd=src, timeout=1800)
        if rc == 0:
            break
        text = o + e
        culprit = next((t for t in targets if t.exists() and t.name in text), None)
        if culprit is None:
            c.note('package %s does not compile and no witness is to blame: %s' % (package, text.strip()[-300:]))
            break
        reason = [line.strip()[-220:] for line in text.splitlines() if culprit.name in line][:2]
        witness_state[culprit.name[len('zzw_s020_'):]] = 'left out (does not compile in this tree): ' + ' ; '.join(reason)
        culprit.unlink()
(c.res / 'witness-files.json').write_text(json.dumps(witness_state, indent=1) + '\n')
for name, value in witness_state.items():
    c.log('S020 witness %s: %s' % (name, value))
packages = ['./' + p for p in sorted(by_pkg) if any(t.exists() for t in by_pkg[p])]
witness_results = {}
EXPECTED_FAIL = {  # witnesses written to FAIL where the reviewed finding is present
    'TestWitnessS020CheckWaitPackBlindProbes': 'finding 3 (check trusts a pack read before the lock): present in this tree',
    'TestWitnessS020MetadataTreeNameLeniency': 'finding 4 (R7 leniency in the metadata tree decoder): present in this tree',
    'TestWitnessLockWaitStalePruneVsBackup': 'finding 1 (R1): present in this tree', 'TestWitnessLockWaitStaleRestoreVsPrune': 'finding 1 (R1): present in this tree',
    'TestWitnessLockWaitStaleBackupVsPrune': 'finding 1 (R1, data loss): present in this tree',
    'TestWitnessS020BackupWait': 'finding 2 (source walk opens a FIFO): present in this tree',
}
for arch in ('amd64', '386'):
    if not packages:
        break
    one = c.res / ('witness-%s.json' % arch)
    gotest.go_test(src, packages, one, goarch=arch, run_filter='TestWitnessS020|TestWitnessLockWaitStale', tmp=short_tmp(c.label, 'c13', 'wit', arch), extra=['-v'])
    parsed = gotest.parse(one)
    for p_name, p in parsed.items():
        for t, a in p['tests'].items():
            if '/' not in t:
                witness_results['%s %s %s' % (arch, p_name.replace('github.com/restic/restic/', ''), t)] = a
    for pkg, test, kind in gotest.failures(parsed):
        ev = c.res / ('fail-witness-%s-%s-%s.txt' % (arch, gotest.safe(pkg), gotest.safe(test or 'PACKAGE')))
        ev.write_text(gotest.test_text(parsed, pkg, test, limit=60000))
        short = pkg.replace('github.com/restic/restic', '.')
        if kind == 'build' and arch == '386':
            c.note('386: %s does not build its tests (see C1; F1 for internal/repository/index): %s' % (short, gotest.test_text(parsed, pkg, '')[-200:].strip()))
            continue
        why = next((v for k, v in EXPECTED_FAIL.items() if test.startswith(k)), '')
        c.fail('S020 witness %s %s (%s) fails%s' % (short, test or '(package)', arch, ': ' + why if why else ''), evidence=ev,
               stock='witness: passes where the mechanism behaves as stock', repro='%sgo test -count=1 -v -run "^%s$" %s' % ('GOARCH=386 ' if arch == '386' else '', test or '.', short))
(c.res / 'witness-results.json').write_text(json.dumps(witness_results, indent=1, sort_keys=True) + '\n')
c.numbers['s020_witness_tests'] = {'pass': sum(1 for v in witness_results.values() if v == 'pass'), 'fail': sum(1 for v in witness_results.values() if v == 'fail'),
                                   'files_in': sum(1 for v in witness_state.values() if v == 'in'), 'files_left_out': sum(1 for v in witness_state.values() if v != 'in')}
c.tick()

# ---------------------------------------------------------------- 4. race detector
races = {}
rc, o, e, _ = run(['gcc', '--version'])
if rc != 0:
    c.note('gcc missing: the race part is skipped')
elif not QUICK:
    for name, pkgs, flt in (('race-index-data', ['./internal/repository/index/...', './internal/data/...'], None),
                            ('race-repository-prune', ['./internal/repository'], 'Prune|Repack|Route|ListPacks|Associated|PackInfo|PackTypes|S020|Index')):
        one = c.res / (name + '.json')
        gotest.go_test(src, pkgs, one, race=True, run_filter=flt, timeout_min=60, tmp=short_tmp(c.label, 'c13', name))
        parsed = gotest.parse(one)
        races[name] = gotest.counts(parsed)
        c.log('%s: %s' % (name, json.dumps(races[name])))
        for pkg, test, kind in gotest.failures(parsed):
            short = pkg.replace('github.com/restic/restic', '.')
            ev = c.res / ('fail-%s-%s-%s.txt' % (name, gotest.safe(pkg), gotest.safe(test or 'PACKAGE')))
            ev.write_text(gotest.test_text(parsed, pkg, test, limit=60000))
            if any(test.startswith(k) for k in EXPECTED_FAIL) and 'race' not in kind:
                continue            # already reported in part 3
            c.fail('%s: %s %s [%s]' % (name, short, test or '(package)', kind), evidence=ev, stock='see C2 for the stock -race run of these packages',
                   repro='CGO_ENABLED=1 go test -race -count=1 -run "^%s$" %s' % (test or '.', short))
c.numbers['race'] = races
c.tick()

# ---------------------------------------------------------------- 4b. the R1 regression tests, 3,000 schedules
# (review of S020, NEEDS-RUN 5: after the R1 repair was ported by hand to the season's new wait-work constructors)
c.status('R1 schedules')
R1_TESTS = 'TestLockWaitSchedules|TestWitnessLockWaitStale|TestWitnessLock|TestLockWaitValidation|TestLockWaitIsNeverShortened|TestLockWaitRequestCounts|TestNewLockGate'
schedules = '300' if QUICK else '3000'
one = c.res / 'r1-schedules.json'
gotest.go_test(src, ['./cmd/restic', './internal/repository'], one, run_filter=R1_TESTS, env={'RESTIC_R1_SCHEDULES': schedules},
               tmp=short_tmp(c.label, 'c13', 'r1'), timeout_min=90, extra=['-v'])
parsed = gotest.parse(one)
r1 = {}
for p_name, p in parsed.items():
    for t, a in p['tests'].items():
        if '/' not in t:
            r1['%s %s' % (p_name.replace('github.com/restic/restic/', ''), t)] = a
c.numbers['r1_regression_tests'] = {'schedules': int(schedules), 'tests': r1}
c.log('R1 regression tests with %s schedules: %s' % (schedules, json.dumps(r1)))
if not r1:
    c.note('this tree has none of the R1 regression tests (TestLockWaitSchedules, TestWitnessLock...)')
for pkg, test, kind in gotest.failures(parsed):
    short = pkg.replace('github.com/restic/restic', '.')
    ev = c.res / ('fail-r1-%s-%s.txt' % (gotest.safe(pkg), gotest.safe(test or 'PACKAGE')))
    ev.write_text(gotest.test_text(parsed, pkg, test, limit=60000))
    if any(test.startswith(k) for k in EXPECTED_FAIL) and (c.res / ('fail-witness-amd64-%s-%s.txt' % (gotest.safe(pkg), gotest.safe(test)))).is_file():
        continue                # the same witness already reported in part 3
    c.fail('R1 regression test %s %s [%s] with %s schedules' % (short, test or '(package)', kind, schedules), evidence=ev,
           stock='stock has no lock-wait reads; these are the tree\'s own tests of repair R1',
           repro='RESTIC_R1_SCHEDULES=%s go test -count=1 -v -run "^%s$" %s' % (schedules, test or '.', short))
c.tick()

# ---------------------------------------------------------------- 4c. opt-in tests of the tree (skipped by a plain `go test`)
c.status('opt-in tests')
optin = {}
if not QUICK:
    plan = []
    if any('RESTIC_LARGE_CHECK_TEST' in p.read_text(errors='replace') for p in (src / 'internal' / 'repository').glob('*_test.go')):
        plan.append(('./internal/repository', '^TestLargeCheck', {'RESTIC_LARGE_CHECK_TEST': '1'}))
    for path in sorted((src / 'cmd' / 'restic').glob('*_test.go')):
        text = path.read_text(errors='replace')
        at = text.find('RESTIC_TEST_STOCK_BINARY')
        if at >= 0:
            names = re.findall(r'^func (Test\w+)\(', text[:at], re.M)
            if names:
                plan.append(('./cmd/restic', '^%s$' % names[-1], {'RESTIC_TEST_STOCK_BINARY': str(stock.bin)}))
    for n, (package, flt, extra_env) in enumerate(plan):
        one = c.res / ('optin-%d.json' % n)
        gotest.go_test(src, [package], one, run_filter=flt, env=extra_env, tmp=short_tmp(c.label, 'c13', 'optin', n), timeout_min=30, extra=['-v'])
        parsed = gotest.parse(one)
        for p_name, p in parsed.items():
            for t, a in p['tests'].items():
                if '/' not in t:
                    optin['%s %s (%s)' % (p_name.replace('github.com/restic/restic/', ''), t, ' '.join('%s=%s' % (k, '<stock binary>' if 'STOCK' in k else v) for k, v in extra_env.items()))] = a
        for pkg, test, kind in gotest.failures(parsed):
            ev = c.res / ('fail-optin-%s-%s.txt' % (gotest.safe(pkg), gotest.safe(test or 'PACKAGE')))
            ev.write_text(gotest.test_text(parsed, pkg, test, limit=60000))
            c.fail('opt-in test %s %s [%s] with %s' % (pkg.replace('github.com/restic/restic', '.'), test or '(package)', kind, ' '.join(extra_env)), evidence=ev,
                   stock='the tree\'s own test', repro='%s go test -count=1 -v -run "%s" %s' % (' '.join('%s=%s' % kv for kv in extra_env.items()), flt, package))
c.numbers['opt_in_tests'] = optin
c.log('opt-in tests: %s' % json.dumps(optin))
c.tick()

# ---------------------------------------------------------------- 5. strace: no open() of a FIFO or a device node
c.status('strace')
rc, o, e, _ = run(['strace', '-V'])
strace = {}
if rc != 0:
    c.note('strace is not installed: part 5 skipped')
else:
    fx = c.work / 'st-src'
    write_file(fx / 'z.bin', b'\0' * 700_000)
    write_file(fx / 'sub' / 'text.txt', b'compressible text line\n' * 60000)
    os.mkfifo(fx / 'a.fifo')
    os.mkfifo(fx / 'sub' / 'c.fifo')
    os.mknod(fx / 'b.null', 0o644 | 0o020000, os.makedev(1, 3))
    arms = [('stock', stock.bin), ('tree', cand.bin)]
    s020 = LEAGUE / 'league/space-bunny/restic-v2/workspace/incumbents/S020'
    if (s020 / 'go.mod').is_file():
        try:
            arms.append(('S020 incumbent (positive control)', prepare('_s020', s020).bin))
        except Exception as exc:
            c.note('the S020 incumbent could not be built for the positive control: %r' % (exc,))
    ws = c.work / 'ws'
    for arm, binary in arms:
        counts = []
        sample = []
        for i in range(3 if QUICK else 6):
            repo = c.work / 'st-repo'
            rmtree(repo)
            must(restic(stock.bin, repo, 'init', work=ws))
            if i % 2:
                must(restic(stock.bin, repo, 'backup', '--no-cache', '--host', 'h', fx, work=ws))      # with a parent snapshot
            trace = c.work / 'trace.txt'
            rc, o, e, _ = run(['strace', '-f', '-e', 'trace=open,openat', '-o', str(trace), str(binary), '-r', str(repo), 'backup', '--no-cache', '--host', 'h', str(fx)],
                              env=restic_env(ws), timeout=600)
            lines = [line for line in trace.read_text(errors='replace').splitlines() if re.search(r'a\.fifo|c\.fifo|b\.null', line)]
            counts.append(len(lines))
            sample += lines[:3]
            if rc != 0:
                c.note('strace run of %s exits %d: %s' % (arm, rc, e[-200:]))
        strace[arm] = {'opens_of_fifo_or_device_per_run': counts, 'sample': sample[:6]}
        c.log('strace, %s: opens of the FIFOs or the device node per backup: %s' % (arm, counts))
    (c.res / 'strace.json').write_text(json.dumps(strace, indent=1) + '\n')
    if sum(strace['tree']['opens_of_fifo_or_device_per_run']) > sum(strace['stock']['opens_of_fifo_or_device_per_run']):
        c.fail('backup opens a FIFO or a device node of the source: %s (stock: %s)' % (strace['tree']['opens_of_fifo_or_device_per_run'], strace['stock']['opens_of_fifo_or_device_per_run']),
               evidence=c.res / 'strace.json', stock='stock: %s' % strace['stock']['opens_of_fifo_or_device_per_run'],
               repro='mkfifo SRC/a.fifo; mknod SRC/b.null c 1 3; strace -f -e trace=open,openat -o T restic -r REPO backup SRC; grep -E "a.fifo|b.null" T')
c.numbers['strace'] = {k: v['opens_of_fifo_or_device_per_run'] for k, v in strace.items()}
c.tick()
summary = 'dropped-mechanism files present: %d groups; Linux-only tests %s; S020 witnesses %s; race %s; R1 tests with %s schedules: %d pass, %d fail; opt-in tests: ' + json.dumps(optin).replace('%', '%%') + '; strace opens of FIFO/device per backup: %s'
summary = summary % (
    len(dropped_but_present), json.dumps(c.numbers['linux_only_tests']), json.dumps(c.numbers['s020_witness_tests']),
    json.dumps({k: {'tests': v['tests'], 'failed': v['failed'], 'races': v['races']} for k, v in races.items()}) if races else 'not run',
    schedules, sum(1 for v in r1.values() if v == 'pass'), sum(1 for v in r1.values() if v == 'fail'), json.dumps(c.numbers['strace']))
sys.exit(c.finish(summary))
