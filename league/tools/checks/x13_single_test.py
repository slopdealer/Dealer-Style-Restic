#!/usr/bin/env python3
"""X13 (diagnostic). One named test of a tree under C13's set-up, plain and under -race; and which files of the tree
differ from another export.

CHK_X13_TEST     name of the test (a top-level Test function; every package that defines it is run)
CHK_X13_COMPARE  optional: another tree; the files that differ between the two are listed (diff -rq)
The test runs from a scratch copy of the tree with the league's pinned toolchain: `go test -count=1 -v -run '^NAME$'`,
then the same with -race (cgo), three times."""
import json
import os
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
import gotest  # noqa: E402
from chk import Check, copy_tree, run, short_tmp  # noqa: E402

c = Check('x13', 'one named test of a tree, plain and under -race; files that differ from another export')
name = os.environ.get('CHK_X13_TEST', '')
other = os.environ.get('CHK_X13_COMPARE', '')
if other:
    rc, out, err, _ = run(['diff', '-rq', other, str(c.tree)])
    lines = [line.replace(other, '<other>').replace(str(c.tree), '<tree>') for line in (out + err).splitlines()]
    (c.res / 'diff-against-other.txt').write_text('other = %s\ntree = %s\n' % (other, c.tree) + '\n'.join(lines) + '\n')
    c.numbers['files_that_differ'] = lines
    c.log('against %s: %d differences: %s' % (other, len(lines), lines[:10]))
src = c.work / 'src'
copy_tree(c.tree, src)
packages = sorted({'./' + str(p.parent.relative_to(src)) for p in src.rglob('*_test.go') if ('func %s(' % name) in p.read_text(errors='replace')}) if name else []
c.numbers['packages'] = packages
results = {}
if not packages:
    c.fail('no test named %r in this tree' % name, evidence=c.res / 'progress.log', stock='n/a')
for mode, race, count in (('plain', False, 1), ('race', True, 3)):
    if not packages:
        break
    out = c.res / (mode + '.json')
    gotest.go_test(src, packages, out, race=race, count=count, run_filter='^%s$' % name, tmp=short_tmp(c.label, 'x13', mode), extra=['-v'], timeout_min=30)
    parsed = gotest.parse(out)
    got = {t: a for p in parsed.values() for t, a in p['tests'].items() if '/' not in t}
    results[mode] = {'result': got.get(name, 'did not run'), 'count': count, 'data_race': any(p.get('race') for p in parsed.values())}
    c.log('%s: %s' % (mode, json.dumps(results[mode])))
    if got.get(name) != 'pass' or results[mode]['data_race']:
        ev = c.res / ('fail-%s.txt' % mode)
        ev.write_text(''.join(gotest.test_text(parsed, p, name, limit=60000) for p in parsed) or (Path(str(out) + '.stderr').read_text() if Path(str(out) + '.stderr').is_file() else ''))
        c.fail('%s (%s): %s' % (name, mode, json.dumps(results[mode])), evidence=ev, stock='the tree\'s own test',
               repro='%sgo test -count=%d -v %s-run "^%s$" %s' % ('CGO_ENABLED=1 ' if race else '', count, '-race ' if race else '', name, ' '.join(packages)))
c.numbers['results'] = results
sys.exit(c.finish('%s in %s: plain %s, -race x3 %s; files that differ from the other export: %s' % (
    name, packages, results.get('plain', {}).get('result'), results.get('race', {}).get('result'), len(c.numbers.get('files_that_differ', [])) if other else 'not compared')))
