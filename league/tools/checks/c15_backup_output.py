#!/usr/bin/env python3
"""C15. What `backup` prints, at verbosity 0 to 3 and with --json, against stock (A14: a completed item's line is
only formatted when it will be printed).

For every verbosity (--quiet, default, -v, -vv) and for text and --json output: the same three backups by stock and
by the tree into two copies of one initialised repository, same --host and --time: a first backup (everything new),
a second one with nothing changed, a third after edits (new, changed and removed files, type changes).
Compared per backup: exit code; stdout and stderr as sorted lines after removing what differs between any two runs
(snapshot IDs, durations, the "saved in ..." time of an item). For --json: every `verbose_status` and `error`
message, and the `summary` without its times and snapshot ID (`status` messages are progress ticks: their number
depends on timing, they are counted but not compared).
The source has no two files with the same content and no hard links: which of two identical files is reported as
"added" depends on which worker saves first, in stock as well."""
import json
import os
import re
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent / 'lib'))
from chk import QUICK, Check, copy_repo, gen_corpus, must, mutate_corpus, prepare, prepare_stock, restic, rmtree, write_file  # noqa: E402

c = Check('c15', 'backup output at verbosity 0 to 3, text and --json, against stock (A14)')
stock = prepare_stock()
cand = prepare(c.label, c.tree)
BIN = {'stock': stock.bin, 'tree': cand.bin}
ws = c.work / 'ws'
r0 = c.work / 'r0'
must(restic(stock.bin, r0, 'init', work=ws))
LEVELS = [('0 (--quiet)', ['--quiet']), ('1 (default)', []), ('2 (-v)', ['-v']), ('3 (-vv)', ['-vv'])]
FORMATS = [('text', []), ('json', ['--json'])]
if QUICK:
    LEVELS = [LEVELS[1], LEVELS[3]]
SNAPSHOT = re.compile(r'\b[0-9a-f]{8}\b')
DURATION = re.compile(r'( in |saved in |finished in )\d+[:.]\d+(:\d+)?s?')
SECONDS = re.compile(r'\b\d+\.\d+s\b')


def make_source(path):
    rmtree(path)
    gen_corpus(path, seed=15, small=120, text_files=2, text_mb=1, big=(3,), special=False)
    rmtree(path / 'dup')
    sp = path / 'special'
    write_file(sp / 'empty', b'')
    (sp / 'emptydir').mkdir()
    os.symlink('empty', sp / 'sym-rel')
    os.mkfifo(sp / 'fifo')
    write_file(os.fsencode(str(sp)) + b'/name\xff\xfe-not-utf8', b'raw name')
    write_file(sp / 'mode-setuid', b'x')
    os.chmod(sp / 'mode-setuid', 0o4755)
    write_file(sp / 'xattr-file', b'has attributes')
    os.setxattr(sp / 'xattr-file', 'user.chk.one', b'value one')
    os.setxattr(sp / 'xattr-file', 'user.chk.bin', b'two')


def text_lines(text, hide):
    out = []
    for line in text.replace('\r', '\n').splitlines():
        line = line.rstrip()
        if not line.strip():
            continue
        for old, new in hide:
            line = line.replace(old, new)
        line = DURATION.sub(r'\1<time>', line)
        line = SECONDS.sub('<time>', line)
        if line.startswith(('snapshot ', 'using parent snapshot ')):
            line = SNAPSHOT.sub('<id>', line)
        out.append(line)
    return sorted(out)


def json_lines(text, hide):
    out = []
    ticks = 0
    for line in text.splitlines():
        if not line.strip():
            continue
        try:
            doc = json.loads(line)
        except ValueError:
            out.append('NOT JSON: ' + line[:200])
            continue
        kind = doc.get('message_type')
        if kind == 'status':
            ticks += 1
            continue
        for key in ('duration', 'total_duration', 'snapshot_id', 'backup_start', 'backup_end'):
            doc.pop(key, None)
        canon = json.dumps(doc, sort_keys=True)
        for old, new in hide:
            canon = canon.replace(old, new)
        out.append(canon)
    return sorted(out), ticks


rows = []
c.status('combinations', 0, len(LEVELS) * len(FORMATS))
src = c.work / 'src'
for level, level_flags in LEVELS:
    for fmt, fmt_flags in FORMATS:
        repos = {arm: c.work / ('repo-' + arm) for arm in BIN}
        for arm in BIN:
            copy_repo(r0, repos[arm])
        make_source(src)
        for step, when in ((1, '2026-06-01 00:00:00'), (2, '2026-06-02 00:00:00'), (3, '2026-06-03 00:00:00')):
            if step == 3:
                mutate_corpus(src, seed=151)
            got = {}
            for arm, binary in BIN.items():
                r = restic(binary, repos[arm], 'backup', '--no-cache', '--host', 'h', '--time', when, *level_flags, *fmt_flags, src.name, work=ws, cwd=src.parent)
                hide = [(str(repos[arm]), '<repo>')]
                if fmt == 'json':
                    out, ticks = json_lines(r.out, hide)
                else:
                    out, ticks = text_lines(r.out, hide), 0
                got[arm] = {'rc': r.rc, 'out': out, 'err': text_lines(r.err, hide), 'ticks': ticks, 'raw': r}
            s, t = got['stock'], got['tree']
            problems = []
            if s['rc'] != t['rc']:
                problems.append('exit code stock %d, tree %d' % (s['rc'], t['rc']))
            for stream in ('out', 'err'):
                if s[stream] != t[stream]:
                    only_s = [x for x in s[stream] if x not in t[stream]][:3]
                    only_t = [x for x in t[stream] if x not in s[stream]][:3]
                    problems.append('std%s differs (%d / %d lines): only stock %r; only tree %r' % (stream, len(s[stream]), len(t[stream]), only_s, only_t))
            name = 'verbosity %s, %s, backup %d' % (level, fmt, step)
            row = {'case': name, 'rc': s['rc'], 'stdout_lines': {'stock': len(s['out']), 'tree': len(t['out'])},
                   'status_ticks': {'stock': s['ticks'], 'tree': t['ticks']}, 'problems': problems}
            rows.append(row)
            if problems:
                ev = c.res / 'evidence' / ('v%s-%s-backup%d' % (level[0], fmt, step))
                ev.mkdir(parents=True, exist_ok=True)
                for arm in got:
                    r = got[arm]['raw']
                    (ev / (arm + '.stdout.txt')).write_text(r.out)
                    (ev / (arm + '.stderr.txt')).write_text(r.cmd + '\nrc=%d\n' % r.rc + r.err)
                (ev / 'differences.txt').write_text(name + '\n' + '\n'.join(problems) + '\n')
                c.log('%s: %s' % (name, ' | '.join(problems)[:700]))
                c.fail('backup output, %s: %s' % (name, problems[0][:500]), evidence=ev, stock='stock is the reference (stock.stdout.txt)',
                       repro='restic -r REPO backup --host h --time "%s" %s SRC   (both binaries, copies of one init; backup %d of 3)' % (when, ' '.join(level_flags + fmt_flags), step))
        c.tick()
(c.res / 'cases.json').write_text(json.dumps(rows, indent=1) + '\n')
c.numbers['cases'] = len(rows)
c.numbers['cases_with_differences'] = sum(1 for r in rows if r['problems'])
c.numbers['lines_compared'] = sum(r['stdout_lines']['stock'] for r in rows)
sys.exit(c.finish('%d backups compared (4 verbosities x text/json x 3 backups), %d stdout lines of stock: %d differ' % (
    len(rows), c.numbers['lines_compared'], c.numbers['cases_with_differences'])))
