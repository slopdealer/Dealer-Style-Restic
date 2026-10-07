#!/bin/bash
# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# run_all.sh <tree> <label> [checks]     all release checks on one restic source tree, detached.
#
#   <tree>    restic source tree to test (never written to; it is copied to /home/restic-final-checks)
#   <label>   name of this run; results go to checks/results/<label>/ (status.txt: one line per finished check)
#   [checks]  optional comma list, e.g. c03,c05 (default: all of c01..c16; diagnostics x06, x08, x10 only when named)
#
# The run is one systemd unit: pinned to the check CPUs, 16 GiB memory cap, the league root and the tree mounted
# read-only for it. It returns at once; follow results/<label>/status.txt and results/<label>/run_all.log.
# Stop it with: systemctl stop <unit printed below>
# Environment passed through: CHK_QUICK=1 (small smoke run), CHK_KEEP=1 (keep scratch data), CHK_* tuning knobs.
set -u
if [ $# -lt 2 ]; then sed -n '2,12p' "$0"; exit 2; fi
TREE=$(readlink -f "$1"); LABEL=$2; ONLY=${3:-}
HERE=$(dirname "$(readlink -f "$0")")
[ -f "$TREE/go.mod" ] || { echo "not a restic tree: $TREE"; exit 2; }
case "$LABEL" in *[!A-Za-z0-9._-]*|_*|"") echo "label: letters, digits, . _ - only, not starting with _"; exit 2;; esac
RES=$HERE/results/$LABEL; mkdir -p "$RES" /home/restic-final-checks
UNIT=restic-checks-$LABEL-$(date +%s)-$$      # $$: two runs started in the same second must not share a unit name
CPUS=10,11,16-18,23,46,47,58,59,64-66,71,94,95
PASS=()
while IFS='=' read -r name value; do
  case "$name" in CHK_*) PASS+=("--setenv=$name=$value");; esac
done < <(env)
# system.slice is confined to CPUs 0-1,48-49 on this box, so the unit gets its own top-level transient slice.
systemd-run --quiet --collect --unit="$UNIT" --slice=resticchecks.slice \
  -p AllowedCPUs=$CPUS -p MemoryMax=${CHK_MEM:-16G} -p MemorySwapMax=0 -p TasksMax=16384 -p LimitNOFILE=65536 -p OOMPolicy=continue \
  -p ReadOnlyPaths=/opt/league/restic-league-v2 -p ReadOnlyPaths="$TREE" \
  -p WorkingDirectory="$HERE" -p StandardOutput=append:"$RES/run_all.log" -p StandardError=append:"$RES/run_all.log" \
  "${PASS[@]}" \
  /usr/bin/taskset -c $CPUS /usr/bin/python3 "$HERE/run_all.py" "$TREE" "$LABEL" "$ONLY" || { echo "systemd-run failed"; exit 1; }
echo "$UNIT" > "$RES/unit.txt"
echo "unit=$UNIT"
echo "status=$RES/status.txt"
echo "log=$RES/run_all.log"
