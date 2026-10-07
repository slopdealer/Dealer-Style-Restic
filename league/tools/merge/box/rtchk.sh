# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# rtchk.sh : the red team's ORIGINAL test files, unchanged, on a copy of trees/final2 (the tree they reviewed): which of the tests of the
# nine findings fail there. Then the large check test (64 GiB + 1 byte) on trees/final3. One PROGRESS line each.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
log() { echo "$(date -u +%FT%TZ) rtchk $*" >> $F/PROGRESS.log; }
W=/dev/shm/rfinal-rt; rm -rf $W; mkdir -p $W $F/gocache $F/gotmp
cp -r $F/trees/final2 $W/tree
tar xzf $F/fetch/seat-tests.tgz -C $W
for s in seat1 seat2 seat3; do cp -r $W/$s/tree/. $W/tree/; done
G="env -i PATH=/usr/bin:/bin HOME=$F/nm TMPDIR=$F/gotmp GOROOT=$R/toolchain/go GOTOOLCHAIN=local GOMODCACHE=$R/toolchain/gomodcache GOCACHE=$F/gocache GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off CGO_ENABLED=0 GOMAXPROCS=4"
cd $W/tree
$G $R/toolchain/go/bin/go test -count=1 -timeout 20m -v -run 'TestReviewIndexCorruptionBeforeConfirmation|TestReviewCheckCommandAcceptsIndexDamagedDuringWait|TestReviewFailedLockLeaksSpeculativeError|TestReviewRestoreCommandIndexVisibilityDuringSnapshotRead|TestReviewRestoreIndexVisibilityDuringSnapshotRead|TestSeat2CheckRejectsIndexDamagedDuringLockWait|TestSeat2IndexNotesReleasedOnReload|TestSeat2RewriteRejectsDamagedKeptIndex|TestSeat2CheckDecoderRetainsCumulativeLimit|TestSeat2RepackReadErrorCancelsQueuedSave|TestReviewSeat3PrefetchHiddenAttributeBudget|TestReviewSeat3BatchErrorAtMessageBoundary' \
  ./cmd/restic ./internal/repository ./internal/repository/index ./internal/restorer ./internal/ui/termstatus > $F/logs/rtchk-final2.log 2>&1
log "reviewers' original tests on final2: $(grep -c '^--- FAIL' $F/logs/rtchk-final2.log) fail, $(grep -c '^--- PASS' $F/logs/rtchk-final2.log) pass: $(grep '^--- ' $F/logs/rtchk-final2.log | awk '{print $2 ":" $3}' | tr '\n' ' ' | cut -c1-900)"
cd $F/trees/final3
$G RESTIC_LARGE_CHECK_TEST=1 $R/toolchain/go/bin/go test -count=1 -timeout 60m -v -run 'TestLargeCheckRejectsUnrestorableBlob' ./internal/repository > $F/logs/rtchk-large.log 2>&1
log "large check test (64 GiB + 1 byte) on final3: $(grep '^--- \|^ok\|^FAIL' $F/logs/rtchk-large.log | tr '\n' ' ') $(grep -c 'restore control rejects\|eager checker rejects' $F/logs/rtchk-large.log) controls"
rm -rf $W
log DONE
