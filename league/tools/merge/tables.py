# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""tables.py NAME : markdown tables of a finished official job (candidate vs incumbent and vs stock) for the merge write-up."""
import json, sys
from pathlib import Path
E = Path(r"LEAGUES_HOME\evidence\restic-final-20261007")
jobs = json.loads((E / "jobs.json").read_text(encoding="utf-8"))
jid = jobs[sys.argv[1]]["id"]
r = json.loads((E / jid / "result.json").read_text(encoding="utf-8"))
PH = ["backup-initial", "backup-unchanged", "backup-mutated", "restore", "check-read-data", "forget-prune", "ls-json"]
W = ["main", "big-index", "large-files", "remote"]
ci, cs = r["comparisons"]["candidate_vs_incumbent"], r["comparisons"]["candidate_vs_stock"]
A = r["arms"]
def peaks(arm, w): return sum(A[arm]["per_workload"][w]["per_phase"][p]["median_peak_rss_bytes"] for p in PH) / 2**20
def iv(x): return "%+.2f..%+.2f" % (x[0], x[1]) if x else ""
print("job `%s`, %s, %d rounds, started %s, completed %s" % (jid, r["mode"], ci["rounds"], r["started_at"][:19], r["completed_at"][:19]))
corr = r["correctness"]
print("correctness: %s/%s units verified, compatibility tests %s, cross reader %s" % (corr.get("verified_units"), corr.get("total_units"), corr.get("candidate_compatibility_tests_passed"), corr.get("cross_reader_ok")))
print()
print("| | suite | main | big-index | large-files | remote |")
print("|---|---|---|---|---|---|")
def row(label, f, suite):
    print("| %s | %s | %s |" % (label, suite, " | ".join(f(w) for w in W)))
row("time vs S020", lambda w: "%+.2f%% (%s/%s)" % (ci["per_workload"][w]["time_saved_pct"], ci["per_workload"][w]["score_wins"], ci["per_workload"][w]["rounds"]),
    "**%+.3f%%** (%s/%s; 90%% %s)" % (ci["time_saved_pct"], ci["score_wins"], ci["rounds"], iv(ci.get("interval90"))))
row("90% interval vs S020", lambda w: iv(ci["per_workload"][w].get("interval90")), "")
row("memory saved vs S020", lambda w: "%+.2f%%" % ci["per_workload"][w]["rss_saved_pct"], "%+.2f%%" % ci["rss_saved_pct"])
row("summed phase peaks, final / S020 MiB", lambda w: "%.1f / %.1f (%+.1f)" % (peaks("candidate", w), peaks("incumbent", w), peaks("candidate", w) - peaks("incumbent", w)), "")
row("time vs stock", lambda w: "%+.2f%% (%s/%s)" % (cs["per_workload"][w]["time_saved_pct"], cs["per_workload"][w]["score_wins"], cs["per_workload"][w]["rounds"]),
    "**%+.3f%%** (%s/%s; 90%% %s)" % (cs["time_saved_pct"], cs["score_wins"], cs["rounds"], iv(cs.get("interval90"))))
row("memory saved vs stock", lambda w: "%+.2f%%" % cs["per_workload"][w]["rss_saved_pct"], "%+.2f%%" % cs["rss_saved_pct"])
row("summed phase peaks, final / stock MiB", lambda w: "%.1f / %.1f" % (peaks("candidate", w), peaks("stock", w)), "")
row("median total s, final / S020 / stock", lambda w: "%.3f / %.3f / %.3f" % tuple(A[a]["per_workload"][w]["median_total_seconds"] for a in ("candidate", "incumbent", "stock")),
    "%.2f / %.2f / %.2f" % tuple(sum(A[a]["per_workload"][w]["median_total_seconds"] for w in W) for a in ("candidate", "incumbent", "stock")))
print()
print("Per phase vs S020, time saved %% (wins of %d), final seconds, peak change MiB:" % ci["per_workload"]["main"]["rounds"])
print()
print("| phase | main | big-index | large-files | remote |")
print("|---|---|---|---|---|")
for p in PH:
    cells = []
    for w in W:
        v = ci["per_workload"][w]["per_phase"][p]
        c = A["candidate"]["per_workload"][w]["per_phase"][p]; i = A["incumbent"]["per_workload"][w]["per_phase"][p]
        cells.append("%+.2f (%s) %.3f s, %+.1f" % (v["time_saved_pct"], v["time_wins"], c["median_seconds"], (c["median_peak_rss_bytes"] - i["median_peak_rss_bytes"]) / 2**20))
    print("| %s | %s |" % (p, " | ".join(cells)))
print()
print("Per phase vs stock, time saved % (wins):")
print()
print("| phase | main | big-index | large-files | remote |")
print("|---|---|---|---|---|")
for p in PH:
    print("| %s | %s |" % (p, " | ".join("%+.2f (%s)" % (cs["per_workload"][w]["per_phase"][p]["time_saved_pct"], cs["per_workload"][w]["per_phase"][p]["time_wins"]) for w in W)))
print()
print("phase means vs S020:", {p: "%+.2f (%s/%s)" % (v["time_saved_pct"], v["time_wins"], v["rounds"]) for p, v in ci["per_phase"].items()})
print("eligibility:", json.dumps(r.get("eligibility", {}).get("reasons")))
