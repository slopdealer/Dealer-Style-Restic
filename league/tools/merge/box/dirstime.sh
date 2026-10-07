# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# dirstime.sh NAME [TOP] : wall time and peak RSS of `restore latest` for TOP*(1+100+100*100) directories, restored in ALTERNATING order by
# NAME, rc-5 (= final) and stock, twice each, for variant A (empty directories only: no second-pass prefetch) and variant B (three small
# files: the prefetch runs). Waits for the f5chk unit to finish first. One log line per restore, one PROGRESS line per variant.
F=/opt/league/restic-final
N=$1; TOP=${2:-50}; W=/dev/shm/rfinal-dirst; LOG=$F/logs/dirstime-$N.log; : > $LOG
while ! grep -q "f5chk $N DONE" $F/PROGRESS.log; do sleep 5; done
log() { echo "$(date -u +%FT%TZ) dirstime $N $*" >> $F/PROGRESS.log; }
rm -rf $W; mkdir -p $W/src
export RESTIC_PASSWORD=x RESTIC_CACHE_DIR=$W/cache GOMAXPROCS=4
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
for V in A B; do
  if [ $V = B ]; then
    echo first > $W/src/00000/000/000/first.txt; echo root > $W/src/root.txt; echo last > $W/src/$(printf %05d $((TOP-1)))/099/099/last.txt
  fi
  rm -rf $W/repo $W/cache; $F/nm/bin/stock init -r $W/repo -q >> $LOG 2>&1
  $F/nm/bin/stock -r $W/repo backup $W/src -q >> $LOG 2>&1
  line=""
  for b in $N rc-5 stock rc-5 $N stock; do
    rm -rf $W/out; sync
    /usr/bin/time -v -o $W/time.txt $F/nm/bin/$b -r $W/repo restore latest --target $W/out -q > /dev/null 2>&1; rc=$?
    rss=$(awk '/Maximum resident set size/ {printf "%.1f", $6/1024}' $W/time.txt); secs=$(awk '/Elapsed \(wall clock\)/ {print $8}' $W/time.txt)
    usr=$(awk '/User time/ {print $4}' $W/time.txt); sys=$(awk '/System time/ {print $4}' $W/time.txt)
    echo "variant $V $b rc=$rc wall=$secs user=$usr sys=$sys rss=$rss MiB" >> $LOG
    line="$line | $b $secs (user $usr, sys $sys) $rss MiB"
  done
  log "variant $V ($DIRS directories, order as listed): $line"
done
rm -rf $W
log "DONE"
