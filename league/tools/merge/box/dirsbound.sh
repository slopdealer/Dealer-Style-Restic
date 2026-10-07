# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# dirsbound.sh [TOP] : wall time and peak RSS of `restore latest` for TOP*(1+100+100*100) directories plus three small files (the second-pass
# prefetch runs), for final2 (bound 65,536 tree IDs), the same tree with bounds 262,144 and 524,288, rc-5 (= final, no bound) and stock.
F=/opt/league/restic-final
TOP=${1:-60}; W=/dev/shm/rfinal-dirsb; LOG=$F/logs/dirsbound.log; : > $LOG
log() { echo "$(date -u +%FT%TZ) dirsbound $*" >> $F/PROGRESS.log; }
bash $F/tools/nm.sh $F/trees/x-f5-bound18 b18 > /dev/null 2>&1; bash $F/tools/nm.sh $F/trees/x-f5-bound19 b19 > /dev/null 2>&1
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
echo first > $W/src/00000/000/000/first.txt; echo root > $W/src/root.txt; echo last > $W/src/$(printf %05d $((TOP-1)))/099/099/last.txt
$F/nm/bin/stock init -r $W/repo -q >> $LOG 2>&1; $F/nm/bin/stock -r $W/repo backup $W/src -q >> $LOG 2>&1
line=""
for b in final2 b18 b19 rc-5 stock b18 final2 b19; do
  rm -rf $W/out; sync
  /usr/bin/time -v -o $W/time.txt $F/nm/bin/$b -r $W/repo restore latest --target $W/out -q > /dev/null 2>&1; rc=$?
  rss=$(awk '/Maximum resident set size/ {printf "%.1f", $6/1024}' $W/time.txt); secs=$(awk '/Elapsed \(wall clock\)/ {print $8}' $W/time.txt)
  echo "$b rc=$rc wall=$secs rss=$rss MiB" >> $LOG
  line="$line | $b $secs $rss MiB"
done
log "$DIRS directories, 3 files, order as listed: $line"
rm -rf $W
log "DONE"
