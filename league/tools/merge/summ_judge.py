# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""summ_judge.py NAME : print suite and per-workload numbers of a finished judge job from the local evidence folder."""
import json, sys
from pathlib import Path
E = Path(r"LEAGUES_HOME\evidence\restic-final-20261007")
jobs = json.loads((E / "jobs.json").read_text(encoding="utf-8"))
jid = jobs[sys.argv[1]]["id"]
r = json.loads((E / jid / "result.json").read_text(encoding="utf-8"))
print("job", jid, "status", r.get("status"), "mode", r.get("mode"), "error", str(r.get("error") or "")[:300])
corr = r.get("correctness") or {}
print("correctness", {k: corr.get(k) for k in ("all_arms_matched_parent_stock_oracle", "verified_units", "total_units", "candidate_compatibility_tests_passed", "cross_reader_ok")})
for cname, c in (r.get("comparisons") or {}).items():
    print("==", cname, {k: c.get(k) for k in ("score_pct", "time_saved_pct", "rss_saved_pct", "score_wins", "rounds", "interval90")})
    for w, v in (c.get("per_workload") or {}).items():
        print("   ", w, {k: v.get(k) for k in ("score_pct", "time_saved_pct", "rss_saved_pct", "score_wins", "rounds")})
        if "-v" in sys.argv:
            for p, pv in (v.get("per_phase") or {}).items():
                print("        ", p, {k: (round(pv.get(k), 2) if isinstance(pv.get(k), float) else pv.get(k)) for k in ("time_saved_pct", "rss_saved_pct", "wins", "rounds")})
    if "-v" in sys.argv:
        for p, pv in (c.get("per_phase") or {}).items():
            print("    phase", p, {k: (round(pv.get(k), 2) if isinstance(pv.get(k), float) else pv.get(k)) for k in ("time_saved_pct", "rss_saved_pct", "wins", "rounds")})
print("eligibility", json.dumps(r.get("eligibility"))[:1500])
for a, v in (r.get("arms") or {}).items():
    print("arm", a, {k: v.get(k) for k in ("median_total_seconds", "median_peak_rss_mib", "median_peak_rss_kb")}, {w: {k: x.get(k) for k in ("median_total_seconds", "median_peak_rss_mib")} for w, x in (v.get("per_workload") or {}).items()})
