# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# fullchk.sh NAME : release checks of trees/NAME on the tools lane: the tree's OWN tests over every package (as root),
# compared by failing test name with the stock tree in the same environment; go vet; linux/386 build; race detector on the new code.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
N=$1; T=$F/trees/$N
log() { echo "$(date -u +%FT%TZ) fullchk $N $*" >> $F/PROGRESS.log; }
mkdir -p $F/gocache $F/gotmp
E="PATH=/usr/bin:/bin HOME=$F/nm TMPDIR=$F/gotmp GOROOT=$R/toolchain/go GOTOOLCHAIN=local GOMODCACHE=$R/toolchain/gomodcache GOCACHE=$F/gocache GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off GOMAXPROCS=4"
GO=$R/toolchain/go/bin/go
if [ ! -s $F/logs/all-stock.fail ]; then
  ( cd $R/stock && env -i $E CGO_ENABLED=0 $GO test -count=1 -p 2 ./... > $F/logs/all-stock.log 2>&1 )
  grep '^--- FAIL\|^FAIL' $F/logs/all-stock.log | sed 's/ (.*//; s/\t[0-9.]*s$//' | sort -u > $F/logs/all-stock.fail
  log "stock tree, all packages, same environment: $(grep -c '^ok' $F/logs/all-stock.log) packages ok; failing: $(cat $F/logs/all-stock.fail | tr '\n' ';' | cut -c1-600)"
fi
( cd $T && env -i $E CGO_ENABLED=0 $GO test -count=1 -p 2 ./... > $F/logs/all-$N.log 2>&1 )
grep '^--- FAIL\|^FAIL' $F/logs/all-$N.log | sed 's/ (.*//; s/\t[0-9.]*s$//' | sort -u > $F/logs/all-$N.fail
log "own tests, all packages: $(grep -c '^ok' $F/logs/all-$N.log) packages ok, $(grep -c 'no test files' $F/logs/all-$N.log) without tests; failing: $(cat $F/logs/all-$N.fail | tr '\n' ';' | cut -c1-600); NOT failing in stock too: [$(comm -23 $F/logs/all-$N.fail $F/logs/all-stock.fail | tr '\n' ';' | cut -c1-600)]"
( cd $T && env -i $E CGO_ENABLED=0 $GO vet ./... > $F/logs/vet-$N.log 2>&1 ); log "go vet ./...: rc=$? $(grep -v '^#' $F/logs/vet-$N.log | head -3 | tr '\n' ';' | cut -c1-300)"
( cd $T && env -i $E CGO_ENABLED=0 GOARCH=386 $GO build -o /dev/null ./cmd/restic > $F/logs/b386-$N.log 2>&1 ); log "linux/386 build: rc=$? $(head -2 $F/logs/b386-$N.log | tr '\n' ';')"
( cd $T && env -i $E CGO_ENABLED=0 GOARCH=386 $GO test -count=1 -run 'TestPreparedKey|TestEncodeBlob|TestParallelScrypt|TestRepackCompletionOverlap' ./internal/repository/crypto ./internal/repository > $F/logs/t386-$N.log 2>&1 ); log "linux/386 tests of the new code: rc=$? $(grep '^ok\|^FAIL\|^---' $F/logs/t386-$N.log | tr '\n' ';' | cut -c1-300)"
if command -v gcc >/dev/null 2>&1; then
  ( cd $T && env -i $E CGO_ENABLED=1 $GO test -race -count=1 -run 'Repack|PreparedKey|CheckWorkers|CheckExtra|EncodeBlob|Prune|ListPacks|PackType' ./internal/repository/... > $F/logs/race-$N.log 2>&1 )
  log "race detector, repository packages (repack, prepared key, check workers, prune, listing): rc=$? $(grep '^ok\|^FAIL\|DATA RACE' $F/logs/race-$N.log | sort | uniq -c | tr '\n' ';' | cut -c1-400)"
  ( cd $T && env -i $E CGO_ENABLED=1 $GO test -race -count=1 -run 'TestLockWait|TestWitnessLock|Wait|Prepared|Confirmation' ./cmd/restic > $F/logs/race-cmd-$N.log 2>&1 )
  log "race detector, cmd/restic lock-wait tests: rc=$? $(grep '^ok\|^FAIL\|DATA RACE' $F/logs/race-cmd-$N.log | sort | uniq -c | tr '\n' ';' | cut -c1-300)"
else
  log "race detector: no gcc on the box, not run"
fi
log "DONE"
