# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""sidebyside.py NAME_A NAME_B [vs] : two finished official jobs with the same reference arm side by side, per workload and phase:
time saved % and wins of each, candidate seconds of each, candidate peak MiB of each. vs = incumbent (default) or stock."""
import json, sys
from pathlib import Path
E = Path(r"LEAGUES_HOME\evidence\restic-final-20261007")
jobs = json.loads((E / "jobs.json").read_text(encoding="utf-8"))
vs = sys.argv[3] if len(sys.argv) > 3 else "incumbent"
PH = ["backup-initial", "backup-unchanged", "backup-mutated", "restore", "check-read-data", "forget-prune", "ls-json"]
SH = dict(zip(PH, ["bi", "bu", "bm", "rs", "ck", "fp", "ls"]))
def load(name):
    return json.loads((E / jobs[name]["id"] / "result.json").read_text(encoding="utf-8"))
a, b = load(sys.argv[1]), load(sys.argv[2])
ca, cb = a["comparisons"]["candidate_vs_" + vs], b["comparisons"]["candidate_vs_" + vs]
ref = "incumbent" if vs == "incumbent" else "stock"
print("A = %s (%s), B = %s (%s), both vs %s" % (sys.argv[1], jobs[sys.argv[1]]["id"][:8], sys.argv[2], jobs[sys.argv[2]]["id"][:8], vs))
print("suite: A %+.3f%% (%s/%s) rss %+.2f%% | B %+.3f%% (%s/%s) rss %+.2f%%" % (
    ca["time_saved_pct"], ca["score_wins"], ca["rounds"], ca["rss_saved_pct"], cb["time_saved_pct"], cb["score_wins"], cb["rounds"], cb["rss_saved_pct"]))
for w in ca["per_workload"]:
    wa, wb = ca["per_workload"][w], cb["per_workload"][w]
    aa, ab = a["arms"]["candidate"]["per_workload"][w], b["arms"]["candidate"]["per_workload"][w]
    ra, rb = a["arms"][ref]["per_workload"][w], b["arms"][ref]["per_workload"][w]
    sa = sum(aa["per_phase"][p]["median_peak_rss_bytes"] for p in PH) / 2**20; sb = sum(ab["per_phase"][p]["median_peak_rss_bytes"] for p in PH) / 2**20
    qa = sum(ra["per_phase"][p]["median_peak_rss_bytes"] for p in PH) / 2**20; qb = sum(rb["per_phase"][p]["median_peak_rss_bytes"] for p in PH) / 2**20
    print("%-11s A %+.2f%% (%s/%s) %.3f s rss %+.2f%% peaks %.1f (ref %.1f, %+.1f) | B %+.2f%% (%s/%s) %.3f s rss %+.2f%% peaks %.1f (ref %.1f, %+.1f)" % (
        w, wa["time_saved_pct"], wa["score_wins"], wa["rounds"], aa["median_total_seconds"], wa["rss_saved_pct"], sa, qa, sa - qa,
        wb["time_saved_pct"], wb["score_wins"], wb["rounds"], ab["median_total_seconds"], wb["rss_saved_pct"], sb, qb, sb - qb))
    for p in PH:
        pa, pb = wa["per_phase"][p], wb["per_phase"][p]
        print("    %s  A %+6.2f%% (%s/%s) %.3f s %.1f MiB | B %+6.2f%% (%s/%s) %.3f s %.1f MiB | B-A %+.2f pts, %+.3f s, %+.1f MiB" % (
            SH[p], pa["time_saved_pct"], pa["time_wins"], pa["rounds"], aa["per_phase"][p]["median_seconds"], aa["per_phase"][p]["median_peak_rss_bytes"] / 2**20,
            pb["time_saved_pct"], pb["time_wins"], pb["rounds"], ab["per_phase"][p]["median_seconds"], ab["per_phase"][p]["median_peak_rss_bytes"] / 2**20,
            pb["time_saved_pct"] - pa["time_saved_pct"], ab["per_phase"][p]["median_seconds"] - aa["per_phase"][p]["median_seconds"],
            (ab["per_phase"][p]["median_peak_rss_bytes"] - aa["per_phase"][p]["median_peak_rss_bytes"]) / 2**20))
