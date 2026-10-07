#!/usr/bin/env python3
"""Run `go test -json` with the pinned toolchain and reduce its event stream to per-test results."""
import json
import os
import re
from pathlib import Path

import subprocess

from chk import SCRATCH, TAGS, go_env, run


def go_test(src, packages, out_path, goarch='amd64', race=False, count=1, parallel=None, timeout_min=45,
            run_filter=None, tmp=None, env=None, extra=None, user=None):
    """Returns (rc, seconds). The raw event stream is written to out_path, stderr to out_path + '.stderr'.
    user: run the whole `go test` (build and tests) as this uid, with its own build cache."""
    parallel = parallel or int(os.environ.get('CHK_GO_P', '4'))
    cmd = ['go', 'test', '-count=%d' % count, '-json', '-p', str(parallel), '-timeout', '%dm' % timeout_min,
           '-tags', TAGS]
    if race:
        cmd.append('-race')
    if run_filter:
        cmd += ['-run', run_filter]
    cmd += list(extra or []) + list(packages)
    environment = go_env(cgo=race, goarch=goarch, tmp=tmp, extra={'RESTIC_TEST_FUSE': '0'})
    environment.update(env or {})
    if user is not None:
        cache = SCRATCH / ('_gocache_u%d' % user)
        home = SCRATCH / ('_home_u%d' % user)
        for path in (cache, home, Path(environment['TMPDIR'])):
            path.mkdir(parents=True, exist_ok=True)
            subprocess.check_call(['chown', '-R', '%d:%d' % (user, user), str(path)])
        environment.update(GOCACHE=str(cache), HOME=str(home), USER='nobody')
        # tests run in their package directory and some write there: the scratch copy must belong to that user
        subprocess.check_call(['chown', '-R', '%d:%d' % (user, user), str(src)])
    out_path = Path(out_path)
    out_path.parent.mkdir(parents=True, exist_ok=True)
    with open(out_path, 'wb') as out, open(str(out_path) + '.stderr', 'wb') as err:
        wall = int(os.environ.get('CHK_GO_WALL_MIN', '240')) * 60
        rc, _, _, seconds = run(cmd, env=environment, cwd=src, timeout=wall, stdout=out, stderr=err, user=user)
    (Path(str(out_path) + '.cmd')).write_text(' '.join(cmd) + '\n')
    return rc, seconds


def parse(out_path, keep_output=True):
    """{'packages': {pkg: {'result': pass|fail|skip|none, 'tests': {name: pass|fail|skip|run}, 'output': {...}}}}"""
    packages = {}
    out_path = Path(out_path)
    if not out_path.is_file():
        return packages
    with open(out_path, 'r', encoding='utf-8', errors='replace') as stream:
        for line in stream:
            line = line.strip()
            if not line.startswith('{'):
                continue
            try:
                event = json.loads(line)
            except ValueError:
                continue
            name = event.get('Package') or event.get('ImportPath') or '?'
            name = name.split(' ')[0]
            pkg = packages.setdefault(name, {'result': 'none', 'tests': {}, 'output': {}, 'pkg_output': [], 'elapsed': 0})
            action = event.get('Action')
            test = event.get('Test')
            if action in ('build-output', 'build-fail'):
                if action == 'build-fail':
                    pkg['result'] = 'fail'
                    pkg['build_failed'] = True
                if event.get('Output'):
                    pkg['pkg_output'].append(event['Output'])
                continue
            if test:
                if action in ('pass', 'fail', 'skip'):
                    pkg['tests'][test] = action
                elif action == 'run':
                    pkg['tests'].setdefault(test, 'run')
                elif action == 'output' and keep_output:
                    lines = pkg['output'].setdefault(test, [])
                    if len(lines) < 4000:
                        lines.append(event.get('Output', ''))
            else:
                if action in ('pass', 'fail', 'skip'):
                    pkg['result'] = action
                    pkg['elapsed'] = event.get('Elapsed', 0)
                elif action == 'output':
                    if len(pkg['pkg_output']) < 4000:
                        pkg['pkg_output'].append(event.get('Output', ''))
    for pkg in packages.values():
        text = ''.join(pkg['pkg_output'])
        pkg['race'] = 'DATA RACE' in text or any('DATA RACE' in ''.join(v) for v in pkg['output'].values())
        if '[build failed]' in text or '[setup failed]' in text:
            pkg['build_failed'] = True
        if 'panic: test timed out' in text:
            pkg['timed_out'] = True
    return packages


def counts(packages):
    tests = [a for p in packages.values() for a in p['tests'].values()]
    return {'packages': len(packages), 'packages_failed': sum(1 for p in packages.values() if p['result'] == 'fail'),
            'packages_passed': sum(1 for p in packages.values() if p['result'] == 'pass'),
            'tests': len(tests), 'passed': tests.count('pass'), 'failed': tests.count('fail'),
            'skipped': tests.count('skip'), 'unfinished': tests.count('run'),
            'races': sum(1 for p in packages.values() if p.get('race'))}


def failures(packages):
    """[(package, test or '' for a package-level failure, kind)]; subtests are folded into their top-level test."""
    out = []
    for name, pkg in sorted(packages.items()):
        failed = sorted(t for t, a in pkg['tests'].items() if a in ('fail', 'run'))
        tops = sorted({t.split('/')[0] for t in failed})
        for test in tops:
            kind = 'fail' if pkg['tests'].get(test) == 'fail' else 'unfinished'
            text = ''.join(pkg['output'].get(test, []))
            for sub in failed:
                if sub.startswith(test + '/'):
                    text += ''.join(pkg['output'].get(sub, []))
            if 'DATA RACE' in text:
                kind += '+race'
            out.append((name, test, kind))
        if pkg['result'] == 'fail' and not tops:
            kind = 'build' if pkg.get('build_failed') else ('timeout' if pkg.get('timed_out') else 'package')
            if pkg.get('race'):
                kind += '+race'
            out.append((name, '', kind))
        elif pkg['result'] == 'none' and not tops:
            out.append((name, '', 'no-result'))
    return out


def test_text(packages, package, test, limit=20000):
    pkg = packages.get(package)
    if not pkg:
        return ''
    if not test:
        return ''.join(pkg['pkg_output'])[-limit:]
    text = ''.join(pkg['output'].get(test, []))
    for name, lines in sorted(pkg['output'].items()):
        if name.startswith(test + '/'):
            text += ''.join(lines)
    if not text.strip():
        text = ''.join(pkg['pkg_output'])
    return text[-limit:]


def safe(name):
    return re.sub(r'[^A-Za-z0-9_.-]+', '_', name)[-150:]


def touched_packages(tree, stock):
    """Package directories (relative) of `tree` whose .go files differ from stock's, or that stock does not have."""
    tree, stock = Path(tree), Path(stock)
    touched = []
    for top in ('cmd', 'internal', 'helpers'):
        for dirpath, dirnames, filenames in os.walk(tree / top):
            dirnames.sort()
            gofiles = sorted(f for f in filenames if f.endswith('.go'))
            if not gofiles:
                continue
            rel = os.path.relpath(dirpath, tree)
            other = stock / rel
            theirs = sorted(f for f in os.listdir(other) if f.endswith('.go')) if other.is_dir() else []
            differs = gofiles != theirs
            if not differs:
                for f in gofiles:
                    if (Path(dirpath) / f).read_bytes() != (other / f).read_bytes():
                        differs = True
                        break
            if differs:
                touched.append('./' + rel.replace(os.sep, '/'))
    return touched
