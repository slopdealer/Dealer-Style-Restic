# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# rel.sh TREE VERSION : release binaries of trees/TREE with exactly the judge's build flags and environment plus
# -X github.com/restic/restic/internal/global.Version=VERSION, for five targets, into $F/release/VERSION/. Also: the judged build
# itself (no -X) to compare its hash with the harness's build of the tree; an unstripped twin of the linux/amd64 release binary to
# read the address of crypto.parallelSalsaXOR, with the .text of twin and release compared. One PROGRESS line each.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
N=$1; V=$2; T=$F/trees/$N; O=$F/release/$V; rm -rf $O; mkdir -p $O $F/gocache-rel $F/gotmp
log() { echo "$(date -u +%FT%TZ) rel $N $V $*" >> $F/PROGRESS.log; }
GO=$R/toolchain/go/bin/go
E="PATH=$R/toolchain/go/bin:/usr/bin:/bin HOME=$F/nm TMPDIR=$F/gotmp GOROOT=$R/toolchain/go GOTOOLCHAIN=local GOMODCACHE=$R/toolchain/gomodcache GOCACHE=$F/gocache-rel GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off GONOSUMCHECK=1 GOTELEMETRY=off CGO_ENABLED=0 TZ=UTC"
TAGS="selfupdate disable_grpc_modules"
X="-X github.com/restic/restic/internal/global.Version=$V"
cd $T
# the judged build: the judge's flags and nothing else
env -i $E GOOS=linux GOARCH=amd64 GOAMD64=v1 $GO build -trimpath -buildvcs=false -tags "$TAGS" -ldflags "-s -w" -o $O/.judged ./cmd/restic
H=$(python3 -c "import json;print(json.load(open('$F/out/build-$N.json')).get('candidate_sha256'))" 2>/dev/null)
log "judged build (no -X): sha256 $(sha256sum $O/.judged | cut -d' ' -f1); the harness's build of the tree: $H; go $($GO version | awk '{print $3}')"
for target in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64; do
  os=${target%/*}; arch=${target#*/}; ext=""; [ $os = windows ] && ext=.exe
  env -i $E GOOS=$os GOARCH=$arch GOAMD64=v1 $GO build -trimpath -buildvcs=false -tags "$TAGS" -ldflags "-s -w $X" -o $O/restic_${V}_${os}_${arch}$ext ./cmd/restic 2>> $O/.build.log
  log "built $target rc=$? $(stat -c %s $O/restic_${V}_${os}_${arch}$ext 2>/dev/null) bytes"
done
env -i $E GOOS=linux GOARCH=amd64 GOAMD64=v1 $GO build -trimpath -buildvcs=false -tags "$TAGS" -ldflags "$X" -o $O/.twin ./cmd/restic
A=$(env -i $E $GO tool nm $O/.twin | grep '/crypto.parallelSalsaXOR$' | awk '{print $1}')
for b in .twin restic_${V}_linux_amd64 .judged; do objcopy -O binary --only-section=.text $O/$b $O/$b.text; done
log "linux/amd64: crypto.parallelSalsaXOR at 0x$A (mod 64 = $(( 0x$A % 64 ))) in the unstripped twin; .text sha256 twin $(sha256sum $O/.twin.text | cut -c1-16), release $(sha256sum $O/restic_${V}_linux_amd64.text | cut -c1-16), judged $(sha256sum $O/.judged.text | cut -c1-16); .text bytes $(stat -c %s $O/.twin.text) $(stat -c %s $O/restic_${V}_linux_amd64.text) $(stat -c %s $O/.judged.text)"
log "version strings: release '$($O/restic_${V}_linux_amd64 version)' judged '$($O/.judged version)'"
cmp -l $O/.judged $O/restic_${V}_linux_amd64 2>/dev/null | wc -l > $O/.diffbytes; log "bytes that differ between the judged build and the linux/amd64 release binary: $(cat $O/.diffbytes) (sizes $(stat -c %s $O/.judged) and $(stat -c %s $O/restic_${V}_linux_amd64))"
rm -f $O/*.text
( cd $O && sha256sum restic_* > SHA256SUMS )
log "SHA256SUMS: $(cat $O/SHA256SUMS | awk '{print substr($1,1,12) " " $2}' | tr '\n' ';')"
log DONE
