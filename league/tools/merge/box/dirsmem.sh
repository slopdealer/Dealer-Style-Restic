# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# dirsmem.sh NAME [TOP] : peak RSS (GNU time, ru_maxrss) of `restore latest` into an empty target for a snapshot of
# TOP*(1+100+100*100) directories three levels deep (names of 5 and 3 bytes), restored by stock, rc-5 (= final) and NAME.
# Variant A: empty directories only (the release check's snapshot). Variant B: the same plus three small files (first directory,
# the root, the last directory), so that the second-pass prefetch runs; B's restores are also compared entry by entry with stock's.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
N=$1; TOP=${2:-200}; W=/dev/shm/rfinal-dirs; LOG=$F/logs/dirsmem-$N.log; : > $LOG
log() { echo "$(date -u +%FT%TZ) dirsmem $N $*" >> $F/PROGRESS.log; }
[ -x $F/nm/bin/stock ] || bash $F/tools/nm.sh $R/stock stock > /dev/null 2>&1
[ -x $F/nm/bin/rc-5 ] || bash $F/tools/nm.sh $F/trees/rc-5 rc-5 > /dev/null 2>&1
bash $F/tools/nm.sh $F/trees/$N $N > /dev/null 2>&1
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
manifest() { ( cd $1 && find . -printf '%P %y %m %U %G %s %T@\n' | sort | sha256sum | cut -c1-16 ); }
for V in A B; do
  if [ $V = B ]; then
    echo first > $W/src/00000/000/000/first.txt; echo root > $W/src/root.txt; echo last > $W/src/$(printf %05d $((TOP-1)))/099/099/last.txt
  fi
  rm -rf $W/repo $W/cache; $F/nm/bin/stock init -r $W/repo -q >> $LOG 2>&1
  $F/nm/bin/stock -r $W/repo backup $W/src -q >> $LOG 2>&1 || { log "variant $V: backup failed"; continue; }
  line=""
  for b in stock rc-5 $N; do
    rm -rf $W/out
    /usr/bin/time -v -o $W/time.txt $F/nm/bin/$b -r $W/repo restore latest --target $W/out -q >> $LOG 2>&1; rc=$?
    rss=$(awk '/Maximum resident set size/ {printf "%.1f", $6/1024}' $W/time.txt); secs=$(awk '/Elapsed \(wall clock\)/ {print $8}' $W/time.txt)
    m=""; if [ $V = B ]; then m=" manifest $(manifest $W/out$W/src)"; fi
    line="$line | $b: rc $rc, peak RSS $rss MiB, wall $secs, $(find $W/out -type d | wc -l) dirs$m"
    echo "variant $V $b rc=$rc rss=$rss MiB wall=$secs" >> $LOG
  done
  log "variant $V ($DIRS directories$( [ $V = B ] && echo ', 3 files' )): $line"
done
rm -rf $W
log "DONE"
