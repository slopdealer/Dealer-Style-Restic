#!/bin/bash
# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# worker2.sh LANE : as worker.sh, plus QUIET jobs (queueq/): they run one at a time on lane 6-9 only while nothing else of
# this merge runs (running/ empty) and no restic judge unit is active; while a quiet job waits or runs, no worker claims a normal job.
C=/opt/league/restic-final; R=/opt/league/restic-league-v2
LANE=$1
mkdir -p $C/queueq $C/runningq
while :; do
  [ -e $C/STOP ] && exit 0
  if [ -n "$(ls $C/queueq $C/runningq 2>/dev/null | grep -v : | grep . | head -1)" ]; then
    if [ "$LANE" != "6-9" ] || [ -n "$(ls $C/runningq | head -1)" ]; then sleep 4; continue; fi
    if [ -n "$(ls $C/running | head -1)" ] || systemctl list-units 'restic-v2-*' --no-legend 2>/dev/null | grep -q ' running '; then sleep 4; continue; fi
    j=$(ls $C/queueq | sort | head -1)
    [ -z "$j" ] && continue
    (
      flock -x 9
      mv $C/queueq/$j $C/runningq/$j 2>/dev/null || exit 0
      t0=$(date +%s)
      bash $C/runningq/$j $LANE > $C/logs/$j.log 2>&1
      rc=$?
      mv $C/runningq/$j $C/done/$j
      echo "$(date -u +%FT%TZ) QUIET lane=$LANE job=$j rc=$rc secs=$(( $(date +%s) - t0 )) $(tail -1 $C/logs/$j.log | cut -c1-200)" >> $C/PROGRESS.log
    ) 9>>$R/shared/cpus-$LANE.lock
    continue
  fi
  if [ -z "$(ls $C/queue 2>/dev/null | head -1)" ]; then sleep 3; continue; fi
  (
    flock -x 9
    [ -n "$(ls $C/queueq $C/runningq 2>/dev/null | grep -v : | grep . | head -1)" ] && exit 0
    j=$(ls $C/queue 2>/dev/null | sort | head -1)
    [ -z "$j" ] && exit 0
    mv $C/queue/$j $C/running/$j.$LANE 2>/dev/null || exit 0
    t0=$(date +%s)
    bash $C/running/$j.$LANE $LANE > $C/logs/$j.log 2>&1
    rc=$?
    mv $C/running/$j.$LANE $C/done/$j
    echo "$(date -u +%FT%TZ) lane=$LANE job=$j rc=$rc secs=$(( $(date +%s) - t0 )) $(tail -1 $C/logs/$j.log | cut -c1-200)" >> $C/PROGRESS.log
  ) 9>>$R/shared/cpus-$LANE.lock
  sleep 1
done
