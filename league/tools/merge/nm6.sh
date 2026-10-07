# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2; cd $F
bash tools/nm.sh $F/trees/rc-5 rc-5 > /dev/null 2>&1
for n in s019merge s020 rc-5; do
  echo "$n: $(env -i PATH=/usr/bin:/bin HOME=$F/nm GOCACHE=$F/nm/gocache GOROOT=$R/toolchain/go $R/toolchain/go/bin/go tool nm -n -size nm/bin/$n | grep -E 'crypto\.parallelSalsaXOR$|crypto\.parallelSmix$|crypto\.parallelBlockMix$|scrypt\.salsaXOR$' | awk '{n=split($4,a,"."); printf "%s=%s(%d) ", a[n], $1, strtonum("0x"$1)%64}')"
done
python3 tools/show.py 'bn-Faq-*.json'
diff <(cd trees/rc-5 && find . -type f | sort | xargs sha256sum | sha256sum) <(cd final && find . -type f -not -path './.git' | sort | xargs sha256sum | sha256sum) && echo "worktree final == trees/rc-5 (all files)"
git -C final log --oneline -1
ls queueq runningq queue running | tr '\n' ' '; date -u +%T; free -g | sed -n 2p
