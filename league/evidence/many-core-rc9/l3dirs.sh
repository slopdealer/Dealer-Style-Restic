# l3dirs.sh : does the many-directory restore lose against stock when its 8 cores are spread over L3 cache domains?
# 606,060 directories + 3 files, snapshot written by stock, repository on tmpfs, restore target on ext4, sync before each run,
# GOMAXPROCS=16 on 8 cores and their SMT siblings, three rotated rounds of stock and the rc-9 judge-flag binary per CPU set:
#   compact: 8 cores in as few L3 domains as the judge and research lanes allow; spread: 2 cores from each of four L3 domains.
F=/opt/league/restic-final
TOP=60; W=$F/tmp-l3dirs; S=/dev/shm/rfinal-l3dirs; LOG=$F/logs/l3dirs.log; : > $LOG
STOCK=$F/nm/bin/stock; CAND=$F/release/proof-rc-9/rc-9.judged
log() { echo "$(date -u +%FT%TZ) l3dirs $*" >> $F/PROGRESS.log; }
for c in 6 12 19 26 30 32 36 42; do echo "cpu $c: L3 shared with $(cat /sys/devices/system/cpu/cpu$c/cache/index3/shared_cpu_list), SMT sibling list $(cat /sys/devices/system/cpu/cpu$c/topology/thread_siblings_list)" >> $LOG; done
log "L3 domains: $(for c in 6 12 19 26 32 42; do printf '[%s] ' $(cat /sys/devices/system/cpu/cpu$c/cache/index3/shared_cpu_list); done)"
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
taskset -c 26-29 $STOCK init -r $S/repo -q >> $LOG 2>&1
GOMAXPROCS=4 taskset -c 26-29 $STOCK -r $S/repo backup $S/src -q >> $LOG 2>&1
rm -rf $S/src
cp -r $S/repo $W/repo-ext4
# compact: 4 + 4 cores of two L3 domains (24-29 and 30-35); spread: 2 cores from each of four L3 domains (12-17, 18-23, 24-29, 36-41),
# which is how the release check's CPUs lie; spread-nocache: the spread set with the release check's other two differences,
# the repository on ext4 and --no-cache.
for spec in "compact 26,27,28,29,30,31,32,33,74,75,76,77,78,79,80,81 cache" "spread 12,13,19,20,26,27,36,37,60,61,67,68,74,75,84,85 cache" "spread-nocache 12,13,19,20,26,27,36,37,60,61,67,68,74,75,84,85 nocache"; do
  set -- $spec; N=$1; CPUS=$2; MODE=$3; line=""
  REPO=$S/repo; OPT=""; [ $MODE = nocache ] && { REPO=$W/repo-ext4; OPT="--no-cache"; }
  for b in stock cand cand stock stock cand; do
    [ $b = stock ] && BIN=$STOCK || BIN=$CAND
    rm -rf $W/out; sync
    GOMAXPROCS=16 /usr/bin/time -v -o $S/time.txt taskset -c $CPUS $BIN -r $REPO restore latest $OPT --target $W/out -q > /dev/null 2>&1; rc=$?
    secs=$(awk '/Elapsed \(wall clock\)/ {print $8}' $S/time.txt)
    usr=$(awk '/User time/ {print $4}' $S/time.txt); sys=$(awk '/System time/ {print $4}' $S/time.txt)
    ctx=$(awk '/Voluntary context switches/ {print $4}' $S/time.txt); ictx=$(awk '/Involuntary context switches/ {print $4}' $S/time.txt)
    echo "set $N ($CPUS) $b rc=$rc wall=$secs user=$usr sys=$sys vcsw=$ctx icsw=$ictx" >> $LOG
    line="$line | $b $secs (user $usr, sys $sys, vcsw $ctx)"
  done
  log "$N ($CPUS): $line"
done
rm -rf $W $S
log DONE
