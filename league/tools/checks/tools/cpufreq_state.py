#!/usr/bin/env python3
"""Frequency-governor state of the 16 check CPUs: save it, set a governor, restore, sample the clock, test for a quiet box.

  cpufreq_state.py save FILE            write the present state to FILE (never overwrites an existing FILE)
  cpufreq_state.py set GOVERNOR FILE    write scaling_governor on the check CPUs (FILE must hold the saved state)
  cpufreq_state.py restore FILE         write the saved values back, read them back, write FILE's .restored.json; exit 0 = equal
  cpufreq_state.py sample OUT SECONDS   append one line per interval: clock of each check CPU, load on the other CPUs
  cpufreq_state.py quiet                exit 0 when the CPUs outside the check set are idle (under 1.0 busy CPU over 20 s)
                                        and no restic bench or restic judge process of somebody else exists

Only the check CPUs are ever written, and `set` writes nothing but scaling_governor. A CPU whose frequency policy
reaches a CPU outside the check set makes `set` refuse (policy = affected_cpus and related_cpus; on this box every CPU
is its own policy). freqdomain_cpus, the firmware's table, pairs neighbouring CPU numbers and is recorded only."""
import json
import os
import re
import subprocess
import sys
import time

CPUS = [10, 11, 16, 17, 18, 23, 46, 47, 58, 59, 64, 65, 66, 71, 94, 95]
BASE = '/sys/devices/system/cpu/'
KEYS = ['scaling_governor', 'scaling_min_freq', 'scaling_max_freq', 'energy_performance_preference']


def path(cpu, key):
    return BASE + 'cpu' + str(cpu) + '/cpufreq/' + key


def rd(p):
    try:
        with open(p) as fh:
            return fh.read().strip()
    except OSError:
        return None


def state():
    out = {}
    for c in CPUS:
        out[str(c)] = {}
        for k in KEYS:
            v = rd(path(c, k))
            if v is not None:
                out[str(c)][k] = v
    return out


def stamp():
    return time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime())


def atomic(p, obj):
    tmp = p + '.tmp'
    with open(tmp, 'w') as fh:
        fh.write(json.dumps(obj, indent=1) + '\n')
    os.replace(tmp, p)


def policy_reach():
    for c in CPUS:
        for key in ('affected_cpus', 'related_cpus'):     # the kernel's policy; freqdomain_cpus (firmware table) is only recorded
            v = rd(path(c, key))
            if v and any(int(x) not in CPUS for x in v.split()):
                return 'cpu%d %s = %s reaches a CPU outside the check set' % (c, key, v)
    return ''


def proc_stat():
    d = {}
    with open('/proc/stat') as fh:
        for line in fh:
            if line.startswith('cpu') and line[3].isdigit():
                parts = line.split()
                v = [int(x) for x in parts[1:9]]
                d[int(parts[0][3:])] = (sum(v), v[3] + v[4])
    return d


def busy(a, b):
    out = {}
    for c in b:
        dt = b[c][0] - a[c][0]
        out[c] = 0.0 if dt <= 0 else 1.0 - (b[c][1] - a[c][1]) / dt
    return out


def main():
    cmd = sys.argv[1]
    if cmd == 'save':
        f = sys.argv[2]
        if os.path.exists(f):
            print('saved state exists already, kept: ' + f)
            return 0
        atomic(f, {'saved_at': stamp(), 'cpus': state(), 'driver': rd(path(10, 'scaling_driver')),
                   'available_governors': rd(path(10, 'scaling_available_governors')),
                   'cpuinfo_min_freq': rd(path(10, 'cpuinfo_min_freq')), 'cpuinfo_max_freq': rd(path(10, 'cpuinfo_max_freq')),
                   'boost_switch_read_only': rd(BASE + 'cpufreq/boost'),
                   'policy_members': {str(c): rd(path(c, 'related_cpus')) for c in CPUS}})
        print('saved ' + f)
        return 0
    if cmd == 'set':
        gov, f = sys.argv[2], sys.argv[3]
        saved = json.load(open(f))['cpus']
        why = policy_reach()
        if why:
            print('REFUSED, nothing written: ' + why)
            return 2
        errors = []
        for c in CPUS:
            try:
                with open(path(c, 'scaling_governor'), 'w') as fh:
                    fh.write(gov)
            except OSError as exc:
                errors.append('cpu%d: %r' % (c, exc))
        now = state()
        others_same = all(now[c][k] == saved[c][k] for c in now for k in now[c] if k != 'scaling_governor')
        outside = sorted({int(x) for c in CPUS for x in (rd(path(c, 'freqdomain_cpus')) or '').split() if int(x) not in CPUS})
        print(json.dumps({'set_at': stamp(), 'governor_asked': gov, 'errors': errors, 'state_after': now,
                          'kernel_policy_members': {str(c): rd(path(c, 'related_cpus')) for c in CPUS},
                          'firmware_frequency_domains_recorded_only': {str(c): rd(path(c, 'freqdomain_cpus')) for c in CPUS},
                          'firmware_domain_partners_outside_the_check_set_not_written': {str(c): rd(path(c, 'scaling_governor')) for c in outside},
                          'all_on_asked_governor': all(now[c].get('scaling_governor') == gov for c in now),
                          'every_other_setting_as_saved': others_same}, indent=1))
        return 0 if not errors else 1
    if cmd == 'restore':
        f = sys.argv[2]
        saved = json.load(open(f))['cpus']
        errors = []
        for k in KEYS:
            for c in CPUS:
                want = saved[str(c)].get(k)
                if want is not None and rd(path(c, k)) != want:
                    try:
                        with open(path(c, k), 'w') as fh:
                            fh.write(want)
                    except OSError as exc:
                        errors.append('cpu%d %s: %r' % (c, k, exc))
        now = state()
        diff = ['cpu%s %s: now %s, saved %s' % (c, k, now[c].get(k), saved[c][k]) for c in saved for k in saved[c] if now[c].get(k) != saved[c][k]]
        report = {'restored_at': stamp(), 'equal_to_saved_state': now == saved, 'differences': diff, 'errors': errors, 'state_now': now}
        atomic(f[:-5] + '.restored.json', report)
        print(json.dumps({k: report[k] for k in ('restored_at', 'equal_to_saved_state', 'differences', 'errors')}))
        return 0 if now == saved else 1
    if cmd == 'sample':
        out, every = sys.argv[2], float(sys.argv[3])
        prev = None
        n = 0
        while True:
            cur = proc_stat()
            row = {'t': stamp(), 'epoch': round(time.time(), 1), 'governor': rd(path(10, 'scaling_governor')),
                   'khz': {str(c): int(rd(path(c, 'scaling_cur_freq')) or 0) for c in CPUS}}
            if prev:
                b = busy(prev, cur)
                row['others_busy_cpus'] = round(sum(v for c, v in b.items() if c not in CPUS), 1)
                row['others_over_half'] = sorted(c for c, v in b.items() if c not in CPUS and v > 0.5)
                row['check_busy_cpus'] = round(sum(v for c, v in b.items() if c in CPUS), 1)
            if n % 12 == 0:
                ps = subprocess.run('ps -eo psr,pcpu,comm,args --sort=-pcpu | head -n 9 | cut -c1-160', shell=True, capture_output=True, text=True)
                row['top'] = ps.stdout.splitlines()[1:]
            prev = cur
            n += 1
            with open(out, 'a') as fh:
                fh.write(json.dumps(row) + '\n')
            time.sleep(every)
    if cmd == 'quiet':
        a = proc_stat()
        time.sleep(20)
        b = busy(a, proc_stat())
        others = round(sum(v for c, v in b.items() if c not in CPUS), 1)
        ps = subprocess.run('ps -eo pid,pcpu,comm,cgroup,args', shell=True, capture_output=True, text=True).stdout.splitlines()[1:]
        foreign = []
        for line in ps:
            if 'cpufreq_state.py' in line or 'resticchecks.slice' in line:
                continue
            parts = line.split(None, 4)
            comm = parts[2] if len(parts) > 2 else ''
            if comm.startswith('restic') or re.search('mcbench|restic-league|restic-final/tools|restic-research|restic-password', line):
                foreign.append(line[:200])
        print(json.dumps({'t': stamp(), 'others_busy_cpus': others, 'foreign_processes': foreign[:8]}))
        return 0 if others < 1.0 and not foreign else 1
    print(__doc__)
    return 2


if __name__ == '__main__':
    sys.exit(main())
