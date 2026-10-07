#!/usr/bin/env python3
"""C1. The tree's OWN tests: `go test ./...` on linux/amd64 and `GOARCH=386 go test` for the touched packages,
compared with the same runs on stock (the league's judge only ever ran stock's test files against the tree).

Four runs: everything as root; everything as uid 65534; cmd/restic and internal/repository with the debug log
switched on (DEBUG_LOG: repair R10 removed the switches that made a debug run take other code paths); the touched
packages for 386.
A failure counts against the tree unless the same test (or the same package-level failure) also fails on stock.
FUSE mount tests are switched off in both arms (RESTIC_TEST_FUSE=0): nothing is mounted on the host."""
import hashlib
import json
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
import gotest  # noqa: E402
from chk import CHECKS, NOBODY, QUICK, STOCK_TREE, Check, copy_tree, envint, short_tmp  # noqa: E402

c = Check('c01', "the tree's own tests, linux/amd64 and 386, against stock")
src = c.work / 'src'
stock_src = c.work / 'stock-src'
copy_tree(c.tree, src)
copy_tree(STOCK_TREE, stock_src)
touched = gotest.touched_packages(src, stock_src)
(c.res / 'touched-packages.txt').write_text('\n'.join(touched) + '\n')
c.log('touched packages: %d' % len(touched))
stock_cache = CHECKS / 'results' / '_stock' / 'c01'
stock_cache.mkdir(parents=True, exist_ok=True)
timeout = envint('CHK_C01_TIMEOUT_MIN', 40)
# (name, GOARCH, packages, uid): everything as root, everything again as uid 65534 (tests about files that cannot
# be read only mean something there), and the touched packages on 386
plan = [('amd64', 'amd64', ['./...'], None), ('amd64-uid65534', 'amd64', ['./...'], NOBODY),
        ('amd64-debuglog', 'amd64', ['./cmd/restic', './internal/repository/...', './internal/checker/...', './internal/restorer/...'], None)]
plan.append(('386', '386', touched if not QUICK else touched[:3], None))
if QUICK:
    plan = plan[3:]
c.status('go test', 0, len(plan) * 2)
results = {}


def extra_env(arch, arm):
    if 'debuglog' not in arch:
        return None
    log = c.work / ('debug-%s.log' % arm)
    return {'DEBUG_LOG': str(log)}


for arch, goarch, packages, uid in plan:
    key = hashlib.sha256(('%s %s' % (arch, ' '.join(packages))).encode()).hexdigest()[:12]
    stock_out = stock_cache / ('%s-%s.json' % (arch, key))
    if not (stock_cache / ('%s-%s.done' % (arch, key))).is_file():
        present = [p for p in packages if p == './...' or (stock_src / p).is_dir()]
        c.log('stock %s: go test %d package patterns' % (arch, len(present)))
        rc, seconds = gotest.go_test(stock_src, present, stock_out, goarch=goarch, timeout_min=timeout,
                                     tmp=short_tmp(c.label, 'c01', 'stock', arch), user=uid, env=extra_env(arch, 'stock'))
        (stock_cache / ('%s-%s.done' % (arch, key))).write_text('rc=%d seconds=%d\n' % (rc, seconds))
        c.log('stock %s done rc=%d in %d s' % (arch, rc, seconds))
    c.tick()
    cand_out = c.res / ('cand-%s.json' % arch)
    c.log('candidate %s: go test %d package patterns' % (arch, len(packages)))
    rc, seconds = gotest.go_test(src, packages, cand_out, goarch=goarch, timeout_min=timeout,
                                 tmp=short_tmp(c.label, 'c01', 'cand', arch), user=uid, env=extra_env(arch, 'cand'))
    c.log('candidate %s done rc=%d in %d s' % (arch, rc, seconds))
    c.tick()
    results[arch] = (gotest.parse(cand_out), gotest.parse(stock_out), str(stock_out))

ours = 0
shared = 0
for arch, (cand, stock, stock_out) in results.items():
    cc, sc = gotest.counts(cand), gotest.counts(stock)
    c.numbers[arch] = {'candidate': cc, 'stock': sc}
    c.log('%s candidate %s' % (arch, json.dumps(cc)))
    c.log('%s stock     %s' % (arch, json.dumps(sc)))
    if cc['packages'] == 0:
        c.fail('%s: go test produced no package result' % arch, evidence=c.res / ('cand-%s.json.stderr' % arch))
    stock_fail = {(p, t): k for p, t, k in gotest.failures(stock)}
    lines = []
    for package, test, kind in gotest.failures(cand):
        evidence = c.res / ('fail-%s-%s-%s.txt' % (arch, gotest.safe(package), gotest.safe(test or 'PACKAGE')))
        evidence.write_text(gotest.test_text(cand, package, test))
        in_stock = stock.get(package, {}).get('tests', {}).get(test) if test else stock.get(package, {}).get('result')
        if (package, test) in stock_fail:
            shared += 1
            lines.append('NOT OURS %s %s %s [%s]: fails on stock too (%s)' % (arch, package, test, kind, stock_fail[(package, test)]))
            continue
        ours += 1
        verdict = 'stock passes this test' if in_stock == 'pass' else ('stock skips it' if in_stock == 'skip' else 'own test (not in stock)')
        lines.append('FAIL %s %s %s [%s]: %s' % (arch, package, test, kind, verdict))
        c.fail('%s go test: %s %s [%s]' % (arch, package, test or '(package)', kind), stock=verdict, evidence=evidence,
               repro='cd <tree copy> && %sgo test -count=1 -tags "selfupdate disable_grpc_modules" -run "^%s$" %s' % (
                   'GOARCH=386 ' if arch == '386' else ('(as uid 65534) ' if 'uid' in arch else ''), test or '.',
                   package.replace('github.com/restic/restic', '.')))
    (c.res / ('compare-%s.txt' % arch)).write_text('\n'.join(lines) + '\n')
    for line in lines:
        c.log(line)
a, b = c.numbers.get('amd64', {}).get('candidate', {}), c.numbers.get('386', {}).get('candidate', {})
u = c.numbers.get('amd64-uid65534', {}).get('candidate', {})
d = c.numbers.get('amd64-debuglog', {}).get('candidate', {})
summary = 'amd64 as root %d tests in %d packages: %d pass, %d fail, %d skip; as uid 65534 %d tests: %d pass, %d fail; with DEBUG_LOG %d tests: %d pass, %d fail; 386 (%d touched packages) %d tests: %d pass, %d fail; failures ours=%d, also on stock=%d' % (
    a.get('tests', 0), a.get('packages', 0), a.get('passed', 0), a.get('failed', 0), a.get('skipped', 0),
    u.get('tests', 0), u.get('passed', 0), u.get('failed', 0), d.get('tests', 0), d.get('passed', 0), d.get('failed', 0), len(touched),
    b.get('tests', 0), b.get('passed', 0), b.get('failed', 0), ours, shared)
sys.exit(c.finish(summary))
