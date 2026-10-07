# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# killtest.sh NAME N : kill -9 a `prune --max-unused 0` of binary nm/bin/NAME at a random moment, N times, on a private repository
# in /dev/shm/rfinal-kill-NAME. After every kill the STOCK binary must pass `check --read-data` and restore the kept snapshot
# byte-identical; then NAME completes the prune and stock checks and restores again. One PROGRESS line at the end, one log line per run.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
N=$1; RUNS=${2:-20}; W=/dev/shm/rfinal-kill-$N; BIN=$F/nm/bin/$N; STOCK=$F/nm/bin/stock
LOG=$F/logs/kill-$N.log; : > $LOG
[ -x $STOCK ] || bash $F/tools/nm.sh $R/stock stock > /dev/null 2>&1
[ -x $BIN ] || bash $F/tools/nm.sh $F/trees/$N $N > /dev/null 2>&1
rm -rf $W; mkdir -p $W/src/big $W/src/small
export RESTIC_PASSWORD=x RESTIC_CACHE_DIR=$W/cache GOMAXPROCS=4
python3 - $W/src <<'PY'
import os, sys, random
root = sys.argv[1]; rnd = random.Random(20261007)
for i in range(160):
    with open(f"{root}/big/f{i:03d}.bin", "wb") as f: f.write(rnd.randbytes(rnd.randint(600_000, 3_500_000)))
words = [b"backup", b"snapshot", b"repository", b"restore", b"index", b"pack", b"blob", b"tree"]
for i in range(4000):
    with open(f"{root}/small/t{i:04d}.txt", "wb") as f: f.write(b" ".join(rnd.choice(words) for _ in range(rnd.randint(20, 3000))))
PY
# state 2: every second file gone
cp -al $W/src $W/src2
ls $W/src2/big | awk 'NR%2==0' | while read f; do rm $W/src2/big/$f; done
ls $W/src2/small | awk 'NR%2==0' | while read f; do rm $W/src2/small/$f; done
fail=0; killed=0; early=0
for i in $(seq 1 $RUNS); do
  rm -rf $W/repo $W/out $W/cache
  $BIN init -r $W/repo -q > /dev/null 2>&1
  $BIN -r $W/repo backup $W/src -q > /dev/null 2>&1
  $BIN -r $W/repo backup $W/src2 -q > /dev/null 2>&1
  first=$($STOCK -r $W/repo snapshots --json 2>/dev/null | python3 -c "import json,sys;s=json.load(sys.stdin);print(sorted(s,key=lambda x:x['time'])[0]['id'])")
  $BIN -r $W/repo forget $first -q > /dev/null 2>&1
  packs_before=$(find $W/repo/data -type f | wc -l)
  $BIN -r $W/repo prune --max-unused 0 > $W/prune.log 2>&1 &
  pid=$!
  delay=$(python3 -c "import random;print('%.3f' % (0.22 + random.random()*1.3))")
  sleep $delay
  if kill -9 $pid 2>/dev/null; then killed=$((killed+1)); state=killed; else early=$((early+1)); state=finished; fi
  wait $pid 2>/dev/null
  stage=$(tr '\r' '\n' < $W/prune.log | grep -v '^$' | tail -1 | cut -c1-60)
  $STOCK -r $W/repo unlock --remove-all -q > /dev/null 2>&1
  $STOCK -r $W/repo check --read-data > $W/check1.log 2>&1; c1=$?
  $STOCK -r $W/repo restore latest --target $W/out -q > /dev/null 2>&1; r1=$?
  diff -r $W/src2 $W/out$W/src2 > /dev/null 2>&1; d1=$?
  rm -rf $W/out
  $BIN -r $W/repo prune --max-unused 0 > $W/prune2.log 2>&1; p2=$?
  $STOCK -r $W/repo check --read-data > $W/check2.log 2>&1; c2=$?
  $STOCK -r $W/repo restore latest --target $W/out -q > /dev/null 2>&1; r2=$?
  diff -r $W/src2 $W/out$W/src2 > /dev/null 2>&1; d2=$?
  ok=ok; if [ "$c1$r1$d1$p2$c2$r2$d2" != "0000000" ]; then ok=FAIL; fail=$((fail+1)); cp $W/check1.log $F/logs/kill-$N-check1-$i.log; cp $W/check2.log $F/logs/kill-$N-check2-$i.log; cp $W/prune.log $F/logs/kill-$N-prune-$i.log; fi
  echo "run $i delay $delay $state at [$stage] packs $packs_before -> $(find $W/repo/data -type f | wc -l): check $c1 restore $r1 diff $d1 | prune $p2 check $c2 restore $r2 diff $d2 $ok" >> $LOG
done
rm -rf $W
echo "$(date -u +%FT%TZ) killtest $N: $RUNS runs, $killed killed during prune ($early had finished), failures $fail; stages hit: $(grep -o 'at \[[^]]*' $LOG | cut -c5-30 | sort | uniq -c | sort -rn | head -6 | tr '\n' ';' | tr -s ' ')" >> $F/PROGRESS.log
