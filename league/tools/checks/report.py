#!/usr/bin/env python3
"""report.py <label>     one table of a run, from the summary.json every check writes.

Prints markdown to stdout and saves it as results/<label>/table.md (run_all calls it when a run ends):
the table of checks (verdict, key numbers, seconds, evidence path), every FAIL with its repro, evidence and what
stock does, and the notes of the checks (not ours / not reached / left out)."""
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
label = sys.argv[1]
root = HERE / 'results' / label
lines = ['# Release checks, run `%s`' % label, '']
rows = []
for path in sorted(root.glob('c*/summary.json')):
    try:
        rows.append(json.loads(path.read_text()))
    except ValueError:
        continue
if rows:
    lines += ['Tree: `%s`' % rows[0].get('tree', '?'), '']
lines += ['| check | what it ran | result | numbers | seconds | evidence |', '|---|---|---|---|---|---|']
for s in rows:
    lines.append('| %s | %s | **%s** | %s | %s | `%s` |' % (
        s['check'].upper().replace('C0', 'C'), s.get('title', '').replace('|', '/'), s['verdict'], s.get('summary', '').replace('|', '/'),
        s.get('seconds', ''), root / s['check']))
missing = [c for c in ('c01', 'c02', 'c03', 'c04', 'c05', 'c06', 'c07', 'c08', 'c09', 'c10', 'c11', 'c12', 'c13', 'c14', 'c15', 'c16')
           if c not in {s['check'] for s in rows}]
if missing:
    lines += ['', 'No verdict yet for: %s' % ', '.join(missing)]
fails = [(s['check'], f) for s in rows for f in s.get('fails', [])]
lines += ['', '## FAILs (%d)' % len(fails), '']
for check, f in fails:
    lines += ['- **%s** %s' % (check.upper(), f.get('what', '')), '  - stock: %s' % f.get('stock', ''),
              '  - repro: `%s`' % f.get('repro', ''), '  - evidence: `%s`' % f.get('evidence', '')]
if not fails:
    lines.append('none')
lines += ['', '## Notes of the checks', '']
for s in rows:
    for note in s.get('notes', []):
        lines.append('- %s: %s' % (s['check'].upper(), note))
text = '\n'.join(lines) + '\n'
(root / 'table.md').write_text(text)
sys.stdout.write(text)
