# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# mc.sh : stock (reference arm) against rc-7 (candidate) with 8 and with 16 whole physical cores, GOMAXPROCS = number of CPUs,
# 7 phases x 4 workloads x 5 fixed pairs, one bench at a time on an otherwise idle box. One PROGRESS line per workload and core count.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
PH=backup-initial,backup-unchanged,backup-mutated,restore,check-read-data,forget-prune,ls-json
for spec in "8 26,27,28,29,30,31,32,33" "16 26,27,28,29,30,31,32,33,36,37,38,39,42,43,44,45"; do
  set -- $spec; N=$1; CPUS=$2
  for w in main big-index large-files remote; do
    t0=$(date +%s)
    python3 $F/tools/mcbench.py bench --candidate $F/trees/rc-7 --incumbent $R/stock --cpus $CPUS --team FINALM --workload $w --phases $PH --reps 5 --fixed-reps --out $F/out/mc$N-$w.json > $F/logs/mc$N-$w.log 2>&1
    echo "$(date -u +%FT%TZ) mc $N cores $w rc=$? secs=$(( $(date +%s) - t0 )) $(tail -1 $F/logs/mc$N-$w.log | cut -c1-160)" >> $F/PROGRESS.log
  done
done
echo "$(date -u +%FT%TZ) mc DONE" >> $F/PROGRESS.log
