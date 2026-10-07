# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# rel2.sh TREE VERSION JUDGED : release binaries of trees/TREE with exactly the judge's build flags and environment plus
# -X github.com/restic/restic/internal/global.Version=VERSION, for five targets, into $F/release/VERSION/ with SHA256SUMS.
# linux/amd64 checks against JUDGED (a binary built with the judge's flags from the judged tree): address of crypto.parallelSalsaXOR,
# .text section hashes, and every text symbol address of an unstripped twin against an unstripped build without -X.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
N=$1; V=$2; J=$3; T=$F/trees/$N; O=$F/release/$V; rm -rf $O; mkdir -p $O $F/gocache-rel $F/gotmp
log() { echo "$(date -u +%FT%TZ) rel2 $N $V $*" >> $F/PROGRESS.log; }
GO=$R/toolchain/go/bin/go
E="PATH=$R/toolchain/go/bin:/usr/bin:/bin HOME=$F/nm TMPDIR=$F/gotmp GOROOT=$R/toolchain/go GOTOOLCHAIN=local GOMODCACHE=$R/toolchain/gomodcache GOCACHE=$F/gocache-rel GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off GONOSUMCHECK=1 GOTELEMETRY=off CGO_ENABLED=0 TZ=UTC"
TAGS="selfupdate disable_grpc_modules"
X="-X github.com/restic/restic/internal/global.Version=$V"
sec() { o=$((0x$(readelf -S -W $1 | sed 's/^ *\[ *[0-9]*\] *//' | awk -v n=$2 '$1==n {print $4}'))); s=$((0x$(readelf -S -W $1 | sed 's/^ *\[ *[0-9]*\] *//' | awk -v n=$2 '$1==n {print $5}'))); echo "$s bytes sha256 $(dd if=$1 bs=1M iflag=skip_bytes,count_bytes skip=$o count=$s 2>/dev/null | sha256sum | cut -d' ' -f1)"; }
cd $T
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os=${target%/*}; arch=${target#*/}; ext=""; [ $os = windows ] && ext=.exe
  env -i $E GOOS=$os GOARCH=$arch GOAMD64=v1 $GO build -trimpath -buildvcs=false -tags "$TAGS" -ldflags "-s -w $X" -o $O/restic_${V}_${os}_${arch}$ext ./cmd/restic 2>> $O/.build.log
  log "built $target rc=$? $(stat -c %s $O/restic_${V}_${os}_${arch}$ext 2>/dev/null) bytes"
done
L=$O/restic_${V}_linux_amd64
env -i $E GOOS=linux GOARCH=amd64 GOAMD64=v1 $GO build -trimpath -buildvcs=false -tags "$TAGS" -ldflags "-s -w" -o $O/.published-judged-flags ./cmd/restic
env -i $E GOOS=linux GOARCH=amd64 GOAMD64=v1 $GO build -trimpath -buildvcs=false -tags "$TAGS" -ldflags "$X" -o $O/.twin ./cmd/restic
env -i $E GOOS=linux GOARCH=amd64 GOAMD64=v1 $GO build -trimpath -buildvcs=false -tags "$TAGS" -o $O/.plain ./cmd/restic
A=$(env -i $E $GO tool nm $O/.twin | grep '/crypto.parallelSalsaXOR$' | awk '{print $1}')
log "linux/amd64: crypto.parallelSalsaXOR at 0x$A (mod 64 = $(( 0x$A % 64 ))) in the unstripped twin of the release binary"
log ".text: release $(sec $L .text); its unstripped twin $(sec $O/.twin .text); the same tree with the judge's flags only $(sec $O/.published-judged-flags .text); judged binary $J $(sec $J .text)"
env -i $E $GO tool nm $O/.twin | awk '$2=="T"||$2=="t" {print $1, $3}' | sort > $O/.twin.nm
env -i $E $GO tool nm $O/.plain | awk '$2=="T"||$2=="t" {print $1, $3}' | sort > $O/.plain.nm
log "text symbols: $(wc -l < $O/.twin.nm) with -X, $(wc -l < $O/.plain.nm) without; lines that differ between the two tables (address or name): $(diff $O/.twin.nm $O/.plain.nm | grep -c '^[<>]')"
cmp -l $L $O/.published-judged-flags 2>/dev/null | awk '{print $1}' > $O/.xdiff
log "release binary against the same tree built with the judge's flags only: sizes $(stat -c %s $L) and $(stat -c %s $O/.published-judged-flags), $(wc -l < $O/.xdiff) bytes differ"
log "version: '$($L version)'; with the judge's flags only: '$($O/.published-judged-flags version)'"
( cd $O && sha256sum restic_* > SHA256SUMS )
log "SHA256SUMS: $(cat $O/SHA256SUMS | awk '{print $1 " " $2}' | tr '\n' ';')"
( cd $O && tar czf $F/release/rel-$V.tgz restic_* SHA256SUMS )
log "archive rel-$V.tgz $(stat -c %s $F/release/rel-$V.tgz) bytes sha256 $(sha256sum $F/release/rel-$V.tgz | cut -d' ' -f1)"
log DONE
