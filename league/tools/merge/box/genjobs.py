# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""genjobs.py KIND ... : write job files into queue/. Each job is a shell script taking the lane as $1."""
import sys, os
C = "/opt/league/restic-final"; R = "/opt/league/restic-league-v2"
INC = R + "/league/space-bunny/restic-v2/workspace/incumbents/S019-merge"
RT = f"python3 {R}/research-tools/research_tools_v2.py"
PH = ["backup-initial", "backup-unchanged", "backup-mutated", "restore", "check-read-data", "forget-prune", "ls-json"]
AB = {"bi": PH[0], "bu": PH[1], "bm": PH[2], "rs": PH[3], "ck": PH[4], "fp": PH[5], "ls": PH[6]}
WA = {"m": "main", "b": "big-index", "l": "large-files", "r": "remote"}
def job(name, cmd):
    d = "queueq" if name.startswith("q") else "queue"   # priority q.. = quiet job (worker2.sh)
    os.makedirs(f"{C}/{d}", exist_ok=True)
    with open(f"{C}/{d}/{name}.sh", "w") as f: f.write(cmd + "\n")
kind = sys.argv[1]
n = 0
if kind == "bench":  # genjobs.py bench PRIO NAME TREE 'mblr=rs,ck;r=fp' [reps] [reference tree]
    prio, name, tree, spec = sys.argv[2:6]
    reps = sys.argv[6] if len(sys.argv) > 6 else "5"
    ref = sys.argv[7] if len(sys.argv) > 7 else INC
    for s in spec.split(";"):
        ws, phs = s.split("=")
        phases = ",".join(AB[x] for x in phs.split(","))
        for wc in ws:
            w = WA[wc]
            job(f"{prio}-bn-{name}-{w}", f"{RT} bench --candidate {tree} --incumbent {ref} --cpus $1 --team FINALM --workload {w} --phases {phases} --reps {reps} --fixed-reps --out {C}/out/bn-{name}-{w}.json"); n += 1
elif kind == "one":  # genjobs.py one PRIO NAME OP TREE WORKLOAD PHASE [ref|-] [extra...]
    prio, name, op, tree, w, p = sys.argv[2:8]
    ref = sys.argv[8] if len(sys.argv) > 8 and sys.argv[8] != "-" else INC
    extra = " ".join(sys.argv[9:])
    job(f"{prio}-{name}", f"{RT} {op} --candidate {tree} --incumbent {ref} --cpus $1 --team FINALM --workload {WA.get(w, w)} --phase {AB.get(p, p)} {extra} --out {C}/out/{name}.json"); n += 1
print(n, "jobs queued")
