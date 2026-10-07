# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""phases.py NAME [vs] : per-workload and per-phase table of a finished official job from the local evidence folder.
vs = incumbent (default) or stock. Prints time saved %, wins/rounds, median seconds of both arms, peak MiB of both arms."""
import json, sys
from pathlib import Path
E = Path(r"LEAGUES_HOME\evidence\restic-final-20261007")
jobs = json.loads((E / "jobs.json").read_text(encoding="utf-8"))
name = sys.argv[1]
vs = sys.argv[2] if len(sys.argv) > 2 else "incumbent"
jid = jobs[name]["id"]
r = json.loads((E / jid / "result.json").read_text(encoding="utf-8"))
c = r["comparisons"]["candidate_vs_" + vs]
PH = ["backup-initial", "backup-unchanged", "backup-mutated", "restore", "check-read-data", "forget-prune", "ls-json"]
SH = {"backup-initial": "bi", "backup-unchanged": "bu", "backup-mutated": "bm", "restore": "rs", "check-read-data": "ck", "forget-prune": "fp", "ls-json": "ls"}
corr = r.get("correctness") or {}
print("job", jid, r.get("status"), r.get("mode"), "candidate vs", vs, "| started", r.get("started_at"), "completed", r.get("completed_at"))
print("correctness", {k: corr.get(k) for k in ("all_arms_matched_parent_stock_oracle", "verified_units", "total_units", "candidate_compatibility_tests_passed", "cross_reader_ok")})
print("suite: time %+.3f%%  score %+.3f%%  wins %s/%s  interval90 %s  rss saved %+.2f%%" % (c.get("time_saved_pct") or 0, c.get("score_pct") or 0, c.get("score_wins"), c.get("rounds"), c.get("interval90"), c.get("rss_saved_pct") or 0))
arms = r["arms"]
ref = "incumbent" if vs == "incumbent" else "stock"
for w, v in c["per_workload"].items():
    ca = arms["candidate"]["per_workload"][w]; ra = arms[ref]["per_workload"][w]
    csum = sum(ca["per_phase"][p]["median_peak_rss_bytes"] for p in PH) / 2**20
    rsum = sum(ra["per_phase"][p]["median_peak_rss_bytes"] for p in PH) / 2**20
    print("%-11s time %+.2f%% (%s/%s) int %s | total %.3f vs %.3f s | rss saved %+.2f%% | summed phase peaks %.1f vs %.1f MiB (%+.1f)" % (
        w, v.get("time_saved_pct") or 0, v.get("score_wins"), v.get("rounds"), v.get("interval90"),
        ca["median_total_seconds"], ra["median_total_seconds"], v.get("rss_saved_pct") or 0, csum, rsum, csum - rsum))
    for p in PH:
        pv = (v.get("per_phase") or {}).get(p) or {}
        cp = ca["per_phase"][p]; rp = ra["per_phase"][p]
        print("    %s %+6.2f%% (%s/%s)  %.3f vs %.3f s (%+.3f)   peak %.1f vs %.1f MiB (%+.1f)" % (
            SH[p], pv.get("time_saved_pct") or 0, pv.get("time_wins"), pv.get("rounds"),
            cp["median_seconds"], rp["median_seconds"], cp["median_seconds"] - rp["median_seconds"],
            cp["median_peak_rss_bytes"] / 2**20, rp["median_peak_rss_bytes"] / 2**20, (cp["median_peak_rss_bytes"] - rp["median_peak_rss_bytes"]) / 2**20))
print("phase means over workloads:", {SH[p]: (round(v.get("time_saved_pct") or 0, 2), "%s/%s" % (v.get("time_wins"), v.get("rounds"))) for p, v in (c.get("per_phase") or {}).items()})
print("eligibility", json.dumps(r.get("eligibility"))[:900])
