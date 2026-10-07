# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2; cd $F
bash tools/nm.sh $F/trees/rc-3 rc-3 > /dev/null 2>&1; bash tools/nm.sh $F/trees/x-R4c r4c > /dev/null 2>&1
for n in s019merge s020 repaired rc-2 rc-3 r4c; do
  echo "$n: $(env -i PATH=/usr/bin:/bin HOME=$F/nm GOCACHE=$F/nm/gocache GOROOT=$R/toolchain/go $R/toolchain/go/bin/go tool nm -n -size nm/bin/$n | grep -E 'crypto\.parallelSalsaXOR$|crypto\.parallelSmix$|crypto\.parallelBlockMix$|scrypt\.salsaXOR$|crypto\.KDF$|crypto\.parallelScrypt(Key|Lanes)$' | awk '{n=split($4,a,"."); printf "%s=%s(%d,%s) ", a[n], $1, strtonum("0x"$1)%64, $2}')"
done
python3 tools/nmdiff2.py s019merge rc-3 | head -30
echo; python3 tools/nmdiff2.py s019merge r4c | head -8
