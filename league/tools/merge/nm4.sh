# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2; cd $F
[ -x nm/bin/rc-2 ] || bash tools/nm.sh $F/trees/rc-2 rc-2 > /dev/null 2>&1
[ -x nm/bin/s-w0c ] || bash tools/nm.sh $F/trees/s-w0c s-w0c > /dev/null 2>&1
[ -x nm/bin/s-w1 ] || bash tools/nm.sh $F/trees/s-w1 s-w1 > /dev/null 2>&1
for n in stock s019merge s020 repaired r4b final pA16 pA5 s-w0c s-w1 rc-1 rc-2; do
  [ -x nm/bin/$n ] || continue
  echo "$n: $(env -i PATH=/usr/bin:/bin HOME=$F/nm GOCACHE=$F/nm/gocache GOROOT=$R/toolchain/go $R/toolchain/go/bin/go tool nm -n -size nm/bin/$n | grep -E 'crypto\.parallelSalsaXOR$|crypto\.parallelSmix$|crypto\.parallelBlockMix$|scrypt\.salsaXOR$' | awk '{n=split($4,a,"."); printf "%s=%s(mod64=%d,size=%s) ", a[n], $1, strtonum("0x"$1)%64, $2}')"
done
