# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# mcdirs.sh : restore of 606,060 directories plus three small files (the release check's case) with stock and rc-7 on 4, 8 and 16 cores
# (taskset, GOMAXPROCS following), two runs each in alternating order: wall, user and system seconds, peak RSS. Then one perf profile of
# each binary at 16 cores. One PROGRESS line per core count; per-run lines in logs/mcdirs.log.
F=/opt/league/restic-final
TOP=60; W=/dev/shm/rfinal-mcdirs; LOG=$F/logs/mcdirs.log; : > $LOG
log() { echo "$(date -u +%FT%TZ) mcdirs $*" >> $F/PROGRESS.log; }
rm -rf $W; mkdir -p $W/src
export RESTIC_PASSWORD=x RESTIC_CACHE_DIR=$W/cache
DIRS=$(python3 - $W/src $TOP <<'PY'
import os, sys
root, top = sys.argv[1], int(sys.argv[2])
n = 0
for a in range(top):
    da = "%s/%05d" % (root, a)
    os.mkdir(da); n += 1
    for b in range(100):
        db = "%s/%03d" % (da, b)
        os.mkdir(db); n += 1
        for c in range(100):
            os.mkdir("%s/%03d" % (db, c)); n += 1
print(n)
PY
)
echo first > $W/src/00000/000/000/first.txt; echo root > $W/src/root.txt; echo last > $W/src/$(printf %05d $((TOP-1)))/099/099/last.txt
taskset -c 26-29 $F/nm/bin/stock init -r $W/repo -q >> $LOG 2>&1
GOMAXPROCS=4 taskset -c 26-29 $F/nm/bin/stock -r $W/repo backup $W/src -q >> $LOG 2>&1
log "setup done: $DIRS directories"
for spec in "4 26-29" "8 26-33" "16 26-33,36-39,42-45"; do
  set -- $spec; N=$1; CPUS=$2; line=""
  for b in ${BINS:-rc-7 stock stock rc-7}; do
    rm -rf $W/out; sync
    GOMAXPROCS=$N /usr/bin/time -v -o $W/time.txt taskset -c $CPUS $F/nm/bin/$b -r $W/repo restore latest --target $W/out -q > /dev/null 2>&1; rc=$?
    rss=$(awk '/Maximum resident set size/ {printf "%.1f", $6/1024}' $W/time.txt); secs=$(awk '/Elapsed \(wall clock\)/ {print $8}' $W/time.txt)
    usr=$(awk '/User time/ {print $4}' $W/time.txt); sys=$(awk '/System time/ {print $4}' $W/time.txt)
    ctx=$(awk '/Voluntary context switches/ {print $4}' $W/time.txt)
    echo "cores $N $b rc=$rc wall=$secs user=$usr sys=$sys rss=$rss vcsw=$ctx" >> $LOG
    line="$line | $b $secs (user $usr, sys $sys, vcsw $ctx)"
  done
  log "$N cores: $line"
done
if [ -z "$NOPERF" ] && command -v perf > /dev/null 2>&1; then
  for b in rc-7 stock; do
    rm -rf $W/out; sync
    GOMAXPROCS=16 perf record -F 199 -g -o $W/perf-$b.data taskset -c 26-33,36-39,42-45 $F/nm/bin/$b -r $W/repo restore latest --target $W/out -q > /dev/null 2>> $LOG
    perf report -i $W/perf-$b.data --no-children --sort symbol --stdio 2> /dev/null | grep -v "^#" | grep -v "^$" | grep "%" | head -40 > $F/logs/mcdirs-perf-$b.txt
    log "perf $b: $(head -12 $F/logs/mcdirs-perf-$b.txt | awk '{printf "%s %s %s; ", $1, $3, $4}')"
  done
else
  log "perf not installed"
fi
rm -rf $W
log DONE
