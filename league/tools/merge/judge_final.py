"""Official judge jobs for the S019 hand merge (box tree paths, no upload). Adapted from race-fix/judge_racefix.py.

    python judge_merge.py NAME MODE CANDIDATE INCUMBENT --workloads=main,big-index,large-files,remote

MODE reference (12 rounds) or promotion (44 rounds), official lanes. Evidence: Leagues/evidence/restic-final-20261007/<job id>/
(jobs.json maps NAME -> id; re-running NAME reconnects/resumes).
"""
import base64, json, sys, time, uuid
from pathlib import Path
sys.path.insert(0, str(Path(__file__).resolve().parents[4] / "tools"))
import remote_broker as rb  # noqa: E402

EVIDENCE = rb.ROOT / "evidence" / "restic-final-20261007"
STATE = EVIDENCE / "jobs.json"


def main() -> int:
    name, mode, candidate, incumbent = sys.argv[1:5]
    suite = next((a.split("=", 1)[1] for a in sys.argv if a.startswith("--workloads=")), None)
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    state = json.loads(STATE.read_text(encoding="utf-8")) if STATE.is_file() else {}
    job_id = state.get(name, {}).get("id") or uuid.uuid4().hex
    state[name] = {"id": job_id, "mode": mode, "candidate": candidate, "incumbent": incumbent}
    rb.atomic(STATE, state)
    out = EVIDENCE / job_id
    out.mkdir(parents=True, exist_ok=True)
    saved = out / "result.json"
    if saved.is_file() and json.loads(saved.read_text(encoding="utf-8")).get("status") == "complete":
        result = json.loads(saved.read_text(encoding="utf-8"))
    else:
        cpus = rb.RESTIC_OFFICIAL_CPUS
        work = rb.RESTIC_ROOT + "/supervisor-jobs/" + job_id
        labels = {"maintenance": "final-20261007-" + name}
        command = ["/usr/bin/python3", rb.RESTIC_JUDGE, "--candidate", candidate, "--incumbent", incumbent,
                   "--stock", rb.RESTIC_STOCK, "--mode", mode, "--out", work + "/evidence/result.json",
                   "--status", work + "/evidence/status.json", "--cpus", cpus,
                   "--labels", "b64:" + base64.urlsafe_b64encode(json.dumps(labels, sort_keys=True, separators=(",", ":")).encode()).decode()]
        extra = 0
        if suite:
            command += ["--workloads", suite]
            extra = rb.RESTIC_SUITE_EXTRA_TMPFS_GIB * (len(suite.split(",")) - 1)
        rb.atomic(out / "submission.json", {"name": name, "labels": labels, "command": command, "submitted_at": time.time()})
        result = rb._restic_managed(out, command, rb.RESTIC_SECONDS[mode] * (2 if suite else 1), cpus, rb.RESTIC_OFFICIAL_SLICE, extra_gib=extra)
    c = (result.get("comparisons") or {}).get("candidate_vs_incumbent") or {}
    corr = result.get("correctness") or {}
    print("SUMMARY " + json.dumps({
        "name": name, "id": job_id, "status": result.get("status"), "error": str(result.get("error") or "")[:400],
        "correctness": {k: corr.get(k) for k in ("all_arms_matched_parent_stock_oracle", "verified_units", "total_units",
                                                  "candidate_compatibility_tests_passed", "cross_reader_ok")},
        "vs_incumbent": {k: c.get(k) for k in ("score_pct", "time_saved_pct", "rss_saved_pct", "score_wins", "rounds", "interval90")},
        "per_workload": {w: {k: v.get(k) for k in ("score_pct", "time_saved_pct", "rss_saved_pct", "score_wins", "rounds")} for w, v in (c.get("per_workload") or {}).items()},
        "eligibility": (result.get("eligibility") or {}).get("reasons")}))
    return 0 if result.get("status") == "complete" else 1


if __name__ == "__main__":
    raise SystemExit(main())
