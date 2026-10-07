# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
F=/opt/league/restic-final; cd $F
for x in ls-retry ls-wil ls-repo; do echo "$x $(bash tools/nm.sh $F/trees/x-$x x$x 2>&1 | grep -E 'salsaXOR|main\.main$' | awk '{print $1, $3}' | tr '\n' ' ')"; done
for n in s020 s020nolw; do echo "$n $(env -i PATH=/usr/bin:/bin HOME=$F/nm GOCACHE=$F/nm/gocache GOROOT=/opt/league/restic-league-v2/toolchain/go /opt/league/restic-league-v2/toolchain/go/bin/go tool nm -n $F/nm/bin/$n | grep -E 'salsaXOR|main\.main$|sha256\.block$|runtime\.etext' | awk '{print $1, $3}' | tr '\n' ' ') size $(stat -c %s $F/nm/bin/$n)"; done
python3 tools/nmdiff.py s020nolw s020 2>/dev/null | head -12 | cut -c1-200
