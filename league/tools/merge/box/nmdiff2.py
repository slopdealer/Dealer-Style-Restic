# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""nmdiff2.py A B [SYMBOL] : text symbols placed before SYMBOL (default the league's own key-derivation loop) that are new, gone or
resized between binaries nm/bin/A and nm/bin/B, with the net shift in 32-byte steps."""
import subprocess, sys
R = "/opt/league/restic-league-v2"; M = "/opt/league/restic-final"
SYM = sys.argv[3] if len(sys.argv) > 3 else "github.com/restic/restic/internal/repository/crypto.parallelSalsaXOR"
def syms(n):
    out = subprocess.run([R + "/toolchain/go/bin/go", "tool", "nm", "-n", "-size", M + "/nm/bin/" + n], capture_output=True, text=True,
                         env={"GOROOT": R + "/toolchain/go", "PATH": "/usr/bin:/bin", "HOME": M + "/nm", "GOCACHE": M + "/nm/gocache"}).stdout
    res = []
    for l in out.splitlines():
        p = l.split(None, 3)
        if len(p) < 4 or p[2] not in "Tt": continue
        res.append((int(p[0], 16), int(p[1]), p[3]))
    return res
a = syms(sys.argv[1]); b = syms(sys.argv[2])
la = [ad for ad, sz, nm in a if nm == SYM][0]; lb = [ad for ad, sz, nm in b if nm == SYM][0]
da = {nm: (ad, sz) for ad, sz, nm in a if ad < la}; db = {nm: (ad, sz) for ad, sz, nm in b if ad < lb}
print(SYM.rsplit("/", 1)[-1], hex(la), hex(lb), "delta", lb - la, "=", (lb - la) / 32, "steps; mod64", la % 64, lb % 64)
def al(x): return (x + 31) // 32 * 32
net = 0
for nm in db:
    if nm not in da: print("NEW  ", hex(db[nm][0]), db[nm][1], "(%d)" % al(db[nm][1]), nm[-90:]); net += al(db[nm][1])
    elif al(da[nm][1]) != al(db[nm][1]): print("SIZE ", hex(db[nm][0]), da[nm][1], "->", db[nm][1], "(%+d)" % (al(db[nm][1]) - al(da[nm][1])), nm[-90:]); net += al(db[nm][1]) - al(da[nm][1])
for nm in da:
    if nm not in db: print("GONE ", hex(da[nm][0]), da[nm][1], "(%d)" % -al(da[nm][1]), nm[-90:]); net -= al(da[nm][1])
print("sum of aligned changes:", net, "=", net / 32, "steps")
