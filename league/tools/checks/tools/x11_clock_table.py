#!/usr/bin/env python3
# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""Table of the governor test of x11: per restore the wall time, CPU seconds, the clock of the CPUs in use (from the
sampler of tools/cpufreq_state.py) and the load on the CPUs outside the check set; then the read-back of the restored state.
usage: x11_clock_table.py   (reads results/final3-b8cf439/x11-606060-performance and -schedutil-quiet)"""
import calendar, json, os, statistics, time
os.sched_setaffinity(0, [47])
R = '/opt/league/restic-final/checks/results/final3-b8cf439/'
print(time.strftime('%Y-%m-%dT%H:%M:%SZ', time.gmtime()))
TABLE = {1500000, 2100000, 2750000}
try:
    TABLE = {int(x) for x in open('/sys/devices/system/cpu/cpu10/cpufreq/scaling_available_frequencies').read().split()}
except Exception as exc:
    print('no frequency table', exc)
print('P-state table kHz', sorted(TABLE))
FOUR = ['10', '11', '16', '17']


def epoch(ts):
    return calendar.timegm(time.strptime(ts, '%Y-%m-%dT%H:%M:%SZ'))


def rows_of(folder):
    lines = open(R + folder + '/progress.log', errors='replace').read().splitlines()
    start = max(i for i, line in enumerate(lines) if ' START x11 ' in line)
    out = []
    for line in lines[start:]:
        i = line.find('{"directories"')
        if i > 0:
            row = json.loads(line[i:])
            row['end'] = epoch(line[:20])
            out.append(row)
    return lines[start][:20], out


for folder, clock in (('x11-606060-performance', 'clock-performance.jsonl'), ('x11-606060-schedutil-quiet', 'clock-schedutil-quiet.jsonl')):
    if not os.path.isfile(R + folder + '/table.md'):
        print('==', folder, 'not there yet')
        continue
    started, rows = rows_of(folder)
    samples = [json.loads(x) for x in open(R + 'cpufreq/' + clock).read().splitlines() if x.strip()]
    print('==', folder, 'started', started, 'restores', len(rows), 'clock samples', len(samples), 'governors seen', sorted({s['governor'] for s in samples}))
    for row in rows:
        use = FOUR if row['cpus'].startswith('4') else list(samples[0]['khz'])
        win = [s for s in samples if row['end'] - row['seconds'] <= s['epoch'] <= row['end']]
        vals = [s['khz'][c] for s in win for c in use]
        measured = [v for v in vals if v not in TABLE]
        row['clock_mean_mhz'] = round(statistics.mean(vals) / 1000) if vals else None
        row['clock_measured_mean_mhz'] = round(statistics.mean(measured) / 1000) if measured else None
        row['clock_measured_share'] = round(len(measured) / len(vals), 2) if vals else None
        row['clock_at_min_share'] = round(sum(1 for v in vals if v == min(TABLE)) / len(vals), 2) if vals else None
        ob = [s['others_busy_cpus'] for s in win if 'others_busy_cpus' in s]
        row['others_busy_cpus'] = round(statistics.mean(ob), 1) if ob else None
        row['check_busy_cpus'] = round(statistics.mean([s['check_busy_cpus'] for s in win if 'check_busy_cpus' in s]), 2) if ob else None
        print(json.dumps({k: row[k] for k in ('cpus', 'round', 'binary', 'position_in_round', 'rc', 'seconds', 'user_s', 'sys_s', 'rss_mib', 'clock_mean_mhz',
                                              'clock_measured_mean_mhz', 'clock_measured_share', 'clock_at_min_share', 'others_busy_cpus', 'check_busy_cpus')}))
    for cpus in sorted({r['cpus'] for r in rows}):
        base = None
        for arm in ('stock', 'tree'):
            mine = [r for r in rows if r['cpus'] == cpus and r['binary'] == arm]
            if not mine:
                continue
            med = statistics.median([r['seconds'] for r in mine])
            if arm == 'stock':
                base = med
            print('  CELL', cpus, '|', arm, '| wall', [r['seconds'] for r in mine], '| median', med, '| vs stock', ('%+.1f%%' % (100.0 * (med - base) / base)) if base else '',
                  '| user', statistics.median([r['user_s'] for r in mine]), '+ sys', statistics.median([r['sys_s'] for r in mine]),
                  '| rss max', max(r['rss_mib'] for r in mine),
                  '| clock mean MHz', round(statistics.mean([r['clock_mean_mhz'] for r in mine if r['clock_mean_mhz']])),
                  '| measured MHz', round(statistics.mean([r['clock_measured_mean_mhz'] for r in mine if r['clock_measured_mean_mhz']])),
                  '| at-min share', round(statistics.mean([r['clock_at_min_share'] for r in mine]), 2),
                  '| others busy CPUs', round(statistics.mean([r['others_busy_cpus'] for r in mine if r['others_busy_cpus'] is not None]), 1),
                  '| check busy CPUs', round(statistics.mean([r['check_busy_cpus'] for r in mine if r['check_busy_cpus'] is not None]), 2))
    tops = {}
    first, last = rows[0]['end'] - rows[0]['seconds'], rows[-1]['end']
    for s in samples:
        if first <= s['epoch'] <= last:
            for line in s.get('top', []):
                parts = line.split(None, 3)
                if len(parts) > 3 and float(parts[1]) >= 20 and 'restic-final-checks' not in line and 'cpufreq_state' not in line:
                    key = parts[3][:110]
                    tops[key] = tops.get(key, 0) + 1
    print('  busy foreign processes seen (count of 60 s snapshots):', json.dumps(sorted(tops.items(), key=lambda kv: -kv[1])[:8]))
    span = [s for s in samples if first <= s['epoch'] <= last and 'others_busy_cpus' in s]
    print('  others busy CPUs over the run: mean', round(statistics.mean([s['others_busy_cpus'] for s in span]), 1), 'min', min(s['others_busy_cpus'] for s in span),
          'max', max(s['others_busy_cpus'] for s in span), '| per 5 minutes:', [round(statistics.mean([s['others_busy_cpus'] for s in span[i:i + 60]]), 1) for i in range(0, len(span), 60)])
S = R + 'cpufreq/'
for n in ('restore.log', 'saved-state.restored.json'):
    if os.path.isfile(S + n):
        text = open(S + n).read()
        print('==', n, text[:600] if n.endswith('.log') else json.dumps({k: v for k, v in json.loads(text).items() if k != 'state_now'}))
saved = json.load(open(S + 'saved-state.json'))['cpus']
now = {c: {k: open('/sys/devices/system/cpu/cpu' + c + '/cpufreq/' + k).read().strip() for k in saved[c]} for c in saved}
print('live read-back equals saved state:', now == saved, '| governors now:', sorted({v['scaling_governor'] for v in now.values()}))
if os.path.isfile(S + 'set-performance.json'):
    d = json.load(open(S + 'set-performance.json'))
    print('set:', json.dumps({k: d[k] for k in d if k not in ('state_after', 'kernel_policy_members', 'firmware_frequency_domains_recorded_only')}))
if os.path.isfile(S + 'quiet-wait.jsonl'):
    q = open(S + 'quiet-wait.jsonl').read().splitlines()
    print('quiet wait readings', len(q), '| first', q[0][:260], '| last', q[-1][:260])
print(open('/opt/league/restic-final/checks/results/orch.log').read()[-700:])
