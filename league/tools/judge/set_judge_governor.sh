#!/bin/sh
# Operator 2026-10-01/02: fixed 'performance' governor on the restic judge lanes (CPUs 26-39) and the research lanes
# (6-9, 12-15, 19-22, 42-45), plus their SMT siblings (+48: same physical cores), so team probes match the judge.
# Not persistent across reboot: rerun after a box restart.
for c in $(seq 26 39) $(seq 6 9) $(seq 12 15) $(seq 19 22) $(seq 42 45); do
  for t in $c $((c + 48)); do echo performance > /sys/devices/system/cpu/cpu$t/cpufreq/scaling_governor; done
done
for c in 2 6 12 19 26 42 54 90 40; do echo "cpu$c $(cat /sys/devices/system/cpu/cpu$c/cpufreq/scaling_governor) siblings=$(cat /sys/devices/system/cpu/cpu$c/topology/thread_siblings_list)"; done
