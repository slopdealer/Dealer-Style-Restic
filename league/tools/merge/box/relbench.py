# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""relbench.py VERSION [PAIRS] : `restic ls --json latest` of a 90,000-file snapshot, the linux/amd64 release binary against the judged
build (the judge's flags, no -X), fixed pairs in alternating order on CPUs 26-29 with GOMAXPROCS=4. Prints one summary line."""
import os, subprocess, sys, time, statistics as st, shutil, resource
F = "/opt/league/restic-final"; V = sys.argv[1]; N = int(sys.argv[2]) if len(sys.argv) > 2 else 20
O = f"{F}/release/{V}"; REL = f"{O}/restic_{V}_linux_amd64"; JUD = sys.argv[3] if len(sys.argv) > 3 else f"{O}/.judged"
W = "/dev/shm/rfinal-relb"; shutil.rmtree(W, ignore_errors=True); os.makedirs(W + "/src")
for a in range(300):
    d = f"{W}/src/d{a:03d}"; os.mkdir(d)
    for b in range(300):
        with open(f"{d}/f{b:03d}.txt", "w") as f: f.write(f"file {a} {b}\n" * 4)
env = {"PATH": "/usr/bin:/bin", "HOME": W, "RESTIC_PASSWORD": "x", "RESTIC_CACHE_DIR": W + "/cache", "GOMAXPROCS": "4", "TZ": "UTC"}
pin = ["taskset", "-c", "26-29"]
subprocess.run(pin + [JUD, "init", "-r", W + "/repo", "-q"], env=env, check=True, stdout=subprocess.DEVNULL)
subprocess.run(pin + [JUD, "-r", W + "/repo", "backup", W + "/src", "-q"], env=env, check=True, stdout=subprocess.DEVNULL)
def run(b):
    r0 = resource.getrusage(resource.RUSAGE_CHILDREN); t0 = time.perf_counter()
    p = subprocess.run(pin + [b, "-r", W + "/repo", "ls", "--json", "latest"], env=env, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL)
    t = time.perf_counter() - t0; r1 = resource.getrusage(resource.RUSAGE_CHILDREN)
    assert p.returncode == 0
    return t, (r1.ru_utime - r0.ru_utime) + (r1.ru_stime - r0.ru_stime), len(p.stdout)
for b in (REL, JUD): run(b)
rel, jud, diff, cpu, sizes = [], [], [], [], set()
for i in range(N):
    order = (REL, JUD) if i % 2 == 0 else (JUD, REL)
    res = {b: run(b) for b in order}
    rel.append(res[REL][0]); jud.append(res[JUD][0]); diff.append(res[REL][0] - res[JUD][0]); cpu.append(res[REL][1] - res[JUD][1])
    sizes.add((res[REL][2], res[JUD][2]))
m = st.median(jud); d = st.median(diff)
line = (f"ls --json latest, 90,000 files, {N} fixed pairs, 4 CPUs: release median {st.median(rel)*1000:.1f} ms, judged build {m*1000:.1f} ms; "
        f"median paired difference {d*1000:+.2f} ms ({d/m*100:+.2f}%), release faster in {sum(1 for x in diff if x < 0)}/{N} pairs, "
        f"paired differences from {min(diff)*1000:+.1f} to {max(diff)*1000:+.1f} ms; CPU seconds median difference {st.median(cpu)*1000:+.1f} ms; "
        f"output bytes (release, judged) {sorted(sizes)}")
print(line)
open(f"{F}/PROGRESS.log", "a").write(time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()) + f" relbench {V} {line}\n")
shutil.rmtree(W, ignore_errors=True)
