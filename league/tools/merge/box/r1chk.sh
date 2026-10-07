# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# r1chk.sh NAME : in trees/NAME run the R1 witnesses, the randomized schedules (3000), the settle-delay, verdict and request tests,
# and the whole cmd/restic package with its own tests at lock wait 0 and at the production 200 ms. One PROGRESS line each.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
N=$1; T=$F/trees/$N
log() { echo "$(date -u +%FT%TZ) r1chk $N $*" >> $F/PROGRESS.log; }
mkdir -p $F/gocache $F/gotmp
G="env -i PATH=/usr/bin:/bin HOME=$F/nm TMPDIR=$F/gotmp GOROOT=$R/toolchain/go GOTOOLCHAIN=local GOMODCACHE=$R/toolchain/gomodcache GOCACHE=$F/gocache GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOMAXPROCS=4"
cd $T
$G $R/toolchain/go/bin/go test -count=1 -run 'TestWitnessLock' -v ./cmd/restic > $F/logs/r1w-$N.log 2>&1
log "R1 witnesses: $(grep -c '^--- PASS' $F/logs/r1w-$N.log) pass, $(grep -c '^--- FAIL' $F/logs/r1w-$N.log) fail: $(grep '^--- ' $F/logs/r1w-$N.log | awk '{print $2 $3}' | tr '\n' ' ' | cut -c1-400) $(tail -1 $F/logs/r1w-$N.log)"
$G RESTIC_R1_SCHEDULES=3000 $R/toolchain/go/bin/go test -count=1 -timeout 30m -run 'TestLockWaitSchedules' -v ./cmd/restic > $F/logs/r1s-$N.log 2>&1
log "R1 schedules 3000: $(grep -E '^--- |schedules|failing|confirmed' $F/logs/r1s-$N.log | tail -4 | tr '\n' ' ' | cut -c1-500) $(tail -1 $F/logs/r1s-$N.log)"
$G $R/toolchain/go/bin/go test -count=1 -run 'TestLockWaitIsNeverShortened|TestLockWaitValidation|TestLockWaitRequestCounts' -v ./cmd/restic > $F/logs/r1d-$N.log 2>&1
log "R1 settle delay, verdicts, requests: $(grep -c '^--- PASS' $F/logs/r1d-$N.log) pass, $(grep -c '^--- FAIL' $F/logs/r1d-$N.log) fail $(tail -1 $F/logs/r1d-$N.log)"
$G $R/toolchain/go/bin/go test -count=1 -timeout 30m ./cmd/restic > $F/logs/cmd0-$N.log 2>&1
log "cmd/restic own tests, wait 0: $(tail -1 $F/logs/cmd0-$N.log) $(grep '^--- FAIL' $F/logs/cmd0-$N.log | head -6 | tr '\n' ' ')"
$G RESTIC_TEST_LOCK_WAIT=200ms $R/toolchain/go/bin/go test -count=1 -timeout 30m ./cmd/restic > $F/logs/cmd200-$N.log 2>&1
log "cmd/restic own tests, wait 200ms: $(tail -1 $F/logs/cmd200-$N.log) $(grep '^--- FAIL' $F/logs/cmd200-$N.log | head -6 | tr '\n' ' ')"
log "DONE"
