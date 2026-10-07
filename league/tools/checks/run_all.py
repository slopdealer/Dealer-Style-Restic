#!/usr/bin/env python3
"""Scheduler behind run_all.sh: runs the check scripts in two lanes on disjoint cores, then the scale check alone.

Lane A: the go test checks c01, c02, c13, then c04, c06, c08, c12, c16.   Lane B: c03 (timing sensitive, so it
never shares cores with go test), c05, c07, c09, c11, c14, c15.   Then c10 (scale, the only check whose timings are reported) alone
on all the check CPUs.
Each check writes its own status line; this file only starts them, keeps their output and reports crashes."""
import os
import subprocess
import sys
import threading
import time
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE / 'lib'))
from chk import append, now  # noqa: E402

SCRIPTS = {'c01': 'c01_own_tests.py', 'c02': 'c02_race.py', 'c03': 'c03_lock_race.py', 'c04': 'c04_kill.py',
           'c05': 'c05_corrupt.py', 'c06': 'c06_crossread.py', 'c07': 'c07_tree_ids.py', 'c08': 'c08_restore_matrix.py',
           'c09': 'c09_linux_only.py', 'c10': 'c10_scale.py', 'c11': 'c11_prune_differential.py',
           'c12': 'c12_kill_prune_routes.py', 'c13': 'c13_s020_delta.py', 'c14': 'c14_repack_backend_failure.py',
           'c15': 'c15_backup_output.py', 'c16': 'c16_copy.py',
           # diagnostics, only when named: size anatomy for C6, the repair-packs step of C8 alone, directory memory for C10
           'x06': 'x06_size_anatomy.py', 'x08': 'x08_repair_packs.py', 'x10': 'x10_dirs_memory.py', 'x11': 'x11_dirs_time.py',
           'x13': 'x13_single_test.py'}
# measured on the box (repaired tree): A is about 4+17+5+21+8+6+6 min, B about 30+5+8+5+8 min, c10 about 30 min
LANE_A = ['c01', 'c02', 'c13', 'c04', 'c06', 'c08', 'c12', 'c16']
LANE_B = ['c03', 'c05', 'c07', 'c09', 'c11', 'c14', 'c15']
LAST = ['c10']
CPUS_A = os.environ.get('CHK_LANE_A', '10,11,16,17,58,59,64,65')      # four cores and their siblings
CPUS_B = os.environ.get('CHK_LANE_B', '18,23,46,47,66,71,94,95')      # the other four
tree, label = sys.argv[1], sys.argv[2]
only = [x for x in (sys.argv[3] if len(sys.argv) > 3 else '').split(',') if x]
root = HERE / 'results' / label
root.mkdir(parents=True, exist_ok=True)


def say(text):
    append(root / 'run_all.progress.log', '%s %s' % (now(), text))
    print('%s %s' % (now(), text), flush=True)


def lane(checks, cpus):
    for check in checks:
        if only and check not in only:
            continue
        script = HERE / SCRIPTS[check]
        if not script.is_file():
            append(root / 'status.txt', '%s %s SKIPPED | script %s is missing | 0 s | evidence=' % (now(), check, script.name))
            continue
        (root / check).mkdir(parents=True, exist_ok=True)
        env = dict(os.environ)
        if cpus:
            env['CHK_CPUS'] = cpus
        say('start %s on cpus %s' % (check, cpus or 'all'))
        began = time.time()
        with open(root / check / 'stdout.log', 'ab') as out:
            rc = subprocess.call([sys.executable, str(script), tree, label], stdout=out, stderr=subprocess.STDOUT, env=env)
        say('end %s rc=%d after %d s' % (check, rc, time.time() - began))
        summary = root / check / 'summary.json'
        if not summary.is_file() or summary.stat().st_mtime < began:
            append(root / 'status.txt', '%s %s ERROR | the check script stopped without a verdict (rc=%d), see stdout.log | %d s | evidence=%s' % (
                now(), check, rc, time.time() - began, root / check))


say('run_all tree=%s label=%s only=%s' % (tree, label, only or 'all'))
say('affinity of this run: %s' % sorted(os.sched_getaffinity(0)))
extras = [x for x in only if x in SCRIPTS and x not in LANE_A + LANE_B + LAST]
threads = [threading.Thread(target=lane, args=(LANE_A + extras, CPUS_A)), threading.Thread(target=lane, args=(LANE_B, CPUS_B))]
for t in threads:
    t.start()
for t in threads:
    t.join()
lane(LAST, '')
say('run_all finished')
subprocess.call([sys.executable, str(HERE / 'report.py'), label], stdout=subprocess.DEVNULL)     # results/<label>/table.md
append(root / 'status.txt', '%s ALL DONE' % now())
