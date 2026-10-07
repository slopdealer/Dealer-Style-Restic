# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# h10.sh NAME : build trees/NAME and trees/final3 with the judge's flags, then the directory restore (606,060 directories, three files)
# with NAME against stock: tmpfs on 4, 8 and 16 whole cores, then ext4 on 4 CPUs and on 16 logical CPUs (8 cores with SMT siblings).
F=/opt/league/restic-final
N=$1
while ! grep -q "mcdirs2 DONE" $F/PROGRESS.log; do sleep 5; done
for t in final3 $N; do
  echo "$(date -u +%FT%TZ) h10 build $t: $(taskset -c 2-5 bash $F/tools/nm.sh $F/trees/$t $t 2>&1 | grep -E 'salsaXOR' | head -1)" >> $F/PROGRESS.log
done
sed -i 's/mcdirs2 DONE/mcdirs2 done (first run)/; s/mcdirs DONE/mcdirs done (first run)/' $F/PROGRESS.log
BINS="$N stock $N final3" NOPERF=1 bash $F/tools/mcdirs.sh
BINS="$N stock $N final3" NOPERF=1 bash $F/tools/mcdirs2.sh
echo "$(date -u +%FT%TZ) h10 $N DONE" >> $F/PROGRESS.log
