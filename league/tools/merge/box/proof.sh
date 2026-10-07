#!/bin/bash
# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
# proof.sh JUDGED PUBLISHED : build both trees exactly as the league's judge builds its candidate and compare the two binaries byte by
# byte and section by section. Writes PROOF.txt next to the binaries. Used to show that a commit which changes only a comment and a
# test file (PUBLISHED) leaves the judged machine code (JUDGED) as it was.
F=/opt/league/restic-final; R=/opt/league/restic-league-v2
A=$1; B=$2; O=$F/release/proof-$B; mkdir -p $O $F/gocache-rel $F/gotmp
GO=$R/toolchain/go/bin/go
ENV="PATH=$R/toolchain/go/bin:/usr/bin:/bin HOME=$F/nm TMPDIR=$F/gotmp GOROOT=$R/toolchain/go GOTOOLCHAIN=local GOMODCACHE=$R/toolchain/gomodcache GOCACHE=$F/gocache-rel GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off GONOSUMCHECK=1 GOTELEMETRY=off CGO_ENABLED=0 TZ=UTC GOOS=linux GOARCH=amd64 GOAMD64=v1"
for t in $A $B; do
  ( cd $F/trees/$t && env -i $ENV $GO build -trimpath -buildvcs=false -tags "selfupdate disable_grpc_modules" -ldflags "-s -w" -o $O/$t.judged ./cmd/restic )
done
cd $O
P=PROOF.txt
{
echo "Proof that the published commit carries the judged machine code"
echo "written $(date -u +%FT%TZ) by tools/proof.sh $A $B"
echo
echo "judged tree:    trees/$A  commit $(git -C $F/src/repaired rev-parse refs/remotes/ws/$(echo $A | sed 's/-/\//'))"
echo "published tree: trees/$B  commit $(git -C $F/src/repaired rev-parse refs/remotes/ws/$(echo $B | sed 's/-/\//'))"
echo "source files that differ between the two trees:"
( cd $F/trees && diff -rq $A $B | sed 's/^/  /' )
echo
echo "build, in each tree (the judge's command and environment; toolchain $($GO version | awk '{print $3}')):"
echo "  env -i CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOFLAGS=-mod=readonly GOPROXY=off GOSUMDB=off GOTOOLCHAIN=local GONOSUMCHECK=1 GOTELEMETRY=off TZ=UTC \\"
echo "    go build -trimpath -buildvcs=false -tags \"selfupdate disable_grpc_modules\" -ldflags \"-s -w\" -o restic ./cmd/restic"
echo
for t in $A $B; do
  echo "$t: $(stat -c %s $t.judged) bytes, sha256 $(sha256sum $t.judged | cut -d' ' -f1)"
  echo "  Go build ID  $(env -i $ENV $GO tool buildid $t.judged)"
  echo "  GNU build ID $(readelf -n $t.judged 2>/dev/null | grep 'Build ID' | awk '{print $3}')"
done
echo
echo "The Go build ID has four parts: inputs/inputs/output/output. The first two are hashes of what went INTO the build"
echo "(the source text, comments included); the last two are hashes of what came OUT. The GNU build ID is derived from it."
cmp -l $A.judged $B.judged | awk '{print $1}' > diff.offsets
echo
echo "bytes that differ: $(wc -l < diff.offsets), at file offsets (first byte = 1) $(head -1 diff.offsets) to $(tail -1 diff.offsets)"
echo "  inside .note.go.buildid:   $(awk -v a=$((0x$(readelf -S -W $A.judged | awk '/\.note\.go\.buildid/ {print $6}'))) -v n=$((0x$(readelf -S -W $A.judged | awk '/\.note\.go\.buildid/ {print $7}'))) '$1>a && $1<=a+n' diff.offsets | wc -l)"
echo "  inside .note.gnu.build-id: $(awk -v a=$((0x$(readelf -S -W $A.judged | awk '/\.note\.gnu\.build-id/ {print $6}'))) -v n=$((0x$(readelf -S -W $A.judged | awk '/\.note\.gnu\.build-id/ {print $7}'))) '$1>a && $1<=a+n' diff.offsets | wc -l)"
echo
printf '%-22s %10s %10s  %-64s  %-64s  %s\n' section offset size "sha256 in $A" "sha256 in $B" result
readelf -S -W $A.judged | sed 's/^ *\[ *[0-9]*\] *//' | awk '$1 ~ /^\./ {print $1, $4, $5}' | while read name off size; do
  for t in $A $B; do
    o=$((0x$(readelf -S -W $t.judged | sed 's/^ *\[ *[0-9]*\] *//' | awk -v n=$name '$1==n {print $4}'))); s=$((0x$(readelf -S -W $t.judged | sed 's/^ *\[ *[0-9]*\] *//' | awk -v n=$name '$1==n {print $5}')))
    typ=$(readelf -S -W $t.judged | sed 's/^ *\[ *[0-9]*\] *//' | awk -v n=$name '$1==n {print $2}')
    if [ "$typ" = NOBITS ] || [ $s -eq 0 ]; then echo "(no bytes in the file)" > sec.$t; else dd if=$t.judged bs=1M iflag=skip_bytes,count_bytes skip=$o count=$s 2>/dev/null | sha256sum | cut -d' ' -f1 > sec.$t; fi
  done
  a=$(cat sec.$A); b=$(cat sec.$B); [ "$a" = "$b" ] && r=equal || r=DIFFERENT
  printf '%-22s %10d %10d  %-64s  %-64s  %s\n' $name $((0x$off)) $((0x$size)) "$a" "$b" $r
done
rm -f sec.$A sec.$B
echo
echo "The differing offsets are in diff.offsets, one per line."
} > $P
echo "$(date -u +%FT%TZ) proof $A $B: $(grep -c ' equal$' $P) sections equal, $(grep -c ' DIFFERENT$' $P) different ($(grep ' DIFFERENT$' $P | awk '{print $1}' | tr '\n' ' ')); $(grep 'bytes that differ' $P)" >> $F/PROGRESS.log
