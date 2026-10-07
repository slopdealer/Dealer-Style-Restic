# mc2.sh TREE TAG : stock (reference arm) against trees/TREE (candidate) with 8 and with 16 whole physical cores, GOMAXPROCS = number
# of CPUs, 7 phases x 4 workloads x 5 fixed pairs, one bench at a time on an otherwise idle box. Results out/mc<N>-TAG-<workload>.json.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
T=$1; TAG=$2
PH=backup-initial,backup-unchanged,backup-mutated,restore,check-read-data,forget-prune,ls-json
for spec in "8 26,27,28,29,30,31,32,33" "16 26,27,28,29,30,31,32,33,36,37,38,39,42,43,44,45"; do
  set -- $spec; N=$1; CPUS=$2
  for w in main big-index large-files remote; do
    t0=$(date +%s)
    python3 $F/tools/mcbench.py bench --candidate $F/trees/$T --incumbent $R/stock --cpus $CPUS --team FINALM --workload $w --phases $PH --reps 5 --fixed-reps --out $F/out/mc$N-$TAG-$w.json > $F/logs/mc$N-$TAG-$w.log 2>&1
    echo "$(date -u +%FT%TZ) mc2 $TAG $N cores $w rc=$? secs=$(( $(date +%s) - t0 )) $(tail -1 $F/logs/mc$N-$TAG-$w.log | cut -c1-160)" >> $F/PROGRESS.log
  done
done
echo "$(date -u +%FT%TZ) mc2 $TAG DONE" >> $F/PROGRESS.log
