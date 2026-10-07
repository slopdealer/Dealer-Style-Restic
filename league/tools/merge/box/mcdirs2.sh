# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# mcdirs2.sh : the release check's case on ext4 (not tmpfs): restore of 606,060 directories plus three small files, stock and rc-7,
# on 4 CPUs (26-29) and on 16 logical CPUs made of 8 cores and their SMT siblings (26-33,74-81), GOMAXPROCS following; two runs each,
# alternating. Then flat perf profiles of both binaries on the 16 logical CPUs. Per-run lines in logs/mcdirs2.log.
F=/opt/league/restic-final
TOP=60; W=$F/tmp-mcdirs2; S=/dev/shm/rfinal-mcdirs2; LOG=$F/logs/mcdirs2.log; : > $LOG
BINS=${BINS:-rc-7 stock stock rc-7}
log() { echo "$(date -u +%FT%TZ) mcdirs2 $*" >> $F/PROGRESS.log; }
rm -rf $W $S; mkdir -p $W $S/src
export RESTIC_PASSWORD=x RESTIC_CACHE_DIR=$S/cache
python3 - $S/src $TOP <<'PY'
import os, sys
root, top = sys.argv[1], int(sys.argv[2])
for a in range(top):
    da = "%s/%05d" % (root, a)
    os.mkdir(da)
    for b in range(100):
        db = "%s/%03d" % (da, b)
        os.mkdir(db)
        for c in range(100):
            os.mkdir("%s/%03d" % (db, c))
PY
echo first > $S/src/00000/000/000/first.txt; echo root > $S/src/root.txt; echo last > $S/src/$(printf %05d $((TOP-1)))/099/099/last.txt
taskset -c 26-29 $F/nm/bin/stock init -r $S/repo -q >> $LOG 2>&1
GOMAXPROCS=4 taskset -c 26-29 $F/nm/bin/stock -r $S/repo backup $S/src -q >> $LOG 2>&1
rm -rf $S/src
log "setup done (repository on tmpfs, restore target on ext4)"
for spec in "4 26-29" "16smt 26-33,74-81" ${EXTRA:+"$EXTRA"}; do
  set -- $spec; N=$1; CPUS=$2; P=${N%smt}; line=""
  for b in $BINS; do
    rm -rf $W/out; sync
    GOMAXPROCS=$P /usr/bin/time -v -o $S/time.txt taskset -c $CPUS $F/nm/bin/$b -r $S/repo restore latest --target $W/out -q > /dev/null 2>&1; rc=$?
    secs=$(awk '/Elapsed \(wall clock\)/ {print $8}' $S/time.txt)
    usr=$(awk '/User time/ {print $4}' $S/time.txt); sys=$(awk '/System time/ {print $4}' $S/time.txt)
    ctx=$(awk '/Voluntary context switches/ {print $4}' $S/time.txt)
    echo "cpus $N $b rc=$rc wall=$secs user=$usr sys=$sys vcsw=$ctx" >> $LOG
    line="$line | $b $secs (user $usr, sys $sys, vcsw $ctx)"
  done
  log "$N: $line"
done
if [ -z "$NOPERF" ]; then
for b in rc-7 stock; do
  rm -rf $W/out; sync
  GOMAXPROCS=16 perf record -F 199 -o $S/perf-$b.data taskset -c 26-33,74-81 $F/nm/bin/$b -r $S/repo restore latest --target $W/out -q > /dev/null 2>> $LOG
  perf report -i $S/perf-$b.data --sort symbol --stdio 2> /dev/null | grep -E '^ +[0-9.]+%' | head -45 > $F/logs/mcdirs2-perf-$b.txt
  perf report -i $S/perf-$b.data --sort dso --stdio 2> /dev/null | grep -E '^ +[0-9.]+%' | head -6 > $F/logs/mcdirs2-dso-$b.txt
  log "perf $b by object: $(cat $F/logs/mcdirs2-dso-$b.txt | awk '{printf "%s %s; ", $1, $2}')"
done
fi
rm -rf $W $S
log DONE
