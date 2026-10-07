#!/usr/bin/env python3
"""Rewrite the generated blocks of the documents from the files under league/.

    python league/tools/make_tables.py            rewrite the blocks
    python league/tools/make_tables.py --check    exit 1 if a block is not what would be generated

A block is the text between `<!-- BEGIN GENERATED:name -->` and `<!-- END GENERATED:name -->`.

Inputs:
  league/evidence/judge/*.result.json   the judge's result files
  league/evidence/judge/roles.json      which job is the headline, which jobs are listed, what is still pending
  league/evidence/source-identity.json  written when the repository was assembled
  league/evidence/many-core.json        the 8- and 16-core bench tables and the many-directory restore tables
  league/evidence/release-checks-final3-b8cf439/   the many-directory restores of the release-check harness, every
                                        run (x11-*/runs.json), and the scale check's summary (c10/summary.json)
  league/evidence/SHA256SUMS            checksums of the release binaries, when they exist
  league/series/*.patch                 the patch files

No number in a generated block is typed by hand. Percentages are printed with two decimals, seconds with two,
CPU seconds and MiB with one; a reading that its harness recorded to one decimal is printed as recorded. The result
files keep full precision. The 90% interval of a per-phase cell is computed here from the per-round values in the result file
with the judge's own method (bootstrap of the median, 4,000 resamples, seed 0); the script first recomputes the
judge's per-workload intervals the same way and stops if they do not reproduce.
"""
import json
import random
import re
import statistics
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
EVID = ROOT / "league" / "evidence"
JUDGE = EVID / "judge"
WORKLOADS = ["main", "big-index", "large-files", "remote"]
PHASES = ["backup-initial", "backup-unchanged", "backup-mutated", "restore", "check-read-data",
          "forget-prune", "ls-json"]
MIB = 1024 * 1024
PENDING = "PENDING("


def pct(x):
    return f"{x:+.2f}%"


def sec(x):
    return f"{x:.2f}"


def mib(b):
    return f"{b / MIB:.1f}"


def interval(iv):
    return f"{iv[0]:+.2f}..{iv[1]:+.2f}"


def load(name):
    return json.loads((JUDGE / f"{name}.result.json").read_text(encoding="utf-8"))


def table(header, rows):
    out = ["| " + " | ".join(header) + " |", "|" + "|".join(["---"] * len(header)) + "|"]
    out += ["| " + " | ".join(r) + " |" for r in rows]
    return "\n".join(out)


def bootstrap_interval90(values, resamples=4000, seed=0):
    """The judge's function, copied: 90% bootstrap interval of the median."""
    rng = random.Random(seed)
    medians = sorted(statistics.median(rng.choices(values, k=len(values))) for _ in range(resamples))
    return [medians[int(0.05 * resamples)], medians[int(0.95 * resamples) - 1]]


def paired(d, other, workload, phase=None, field="round_seconds"):
    """Per-round paired values in percent, candidate against `other`, for a workload (phase None = whole pass)."""
    ca = d["arms"]["candidate"]["per_workload"][workload]["per_phase"]
    ot = d["arms"][other]["per_workload"][workload]["per_phase"]
    n = len(ca[PHASES[0]][field])
    if phase is None:
        return [100 * (1 - sum(ca[p][field][i] for p in PHASES) / sum(ot[p][field][i] for p in PHASES))
                for i in range(n)]
    return [100 * (1 - a / b) for a, b in zip(ca[phase][field], ot[phase][field])]


def selfcheck(d):
    """The per-workload intervals in the file must come out of the per-round values with the method above."""
    for key, other in (("candidate_vs_stock", "stock"), ("candidate_vs_incumbent", "incumbent")):
        for w in WORKLOADS:
            mine = bootstrap_interval90(paired(d, other, w))
            theirs = d["comparisons"][key]["per_workload"][w]["interval90"]
            if any(abs(a - b) > 1e-9 for a, b in zip(mine, theirs)):
                raise SystemExit(f"interval of {key}/{w} does not reproduce: {mine} != {theirs}")


def pending_lines(roles):
    out = []
    if roles.get("provisional"):
        out.append(f"> **PROVISIONAL.** {roles['provisional_note']}")
    for p in roles.get("pending", []):
        out.append(f"> **{p}**")
    return ("\n>\n".join(out) + "\n\n") if out else ""


def conditions(d, roles):
    m = d["measurement"]
    per = d["suite"]["rounds_per_workload"]
    c = d["correctness"]
    j = roles["judged"]
    return (f"Judge job `{d['job_id']}` ({d['mode']} mode), completed {d['completed_at'][:16].replace('T', ' ')} UTC: "
            f"{d['rounds']} rounds, {min(per.values())} per workload, three arms in every round "
            f"(this build, the S020 incumbent, upstream restic at `{roles['upstream_commit'][:8]}`). "
            f"**{len(m['cpus'])} CPUs pinned, GOMAXPROCS {m['gomaxprocs']}**; {m['storage']}; "
            f"{roles['machine']}. Correctness: {c['verified_units']:,} of {c['total_units']:,} units matched the "
            f"upstream oracle; compatibility tests passed: {c['candidate_compatibility_tests_passed']}. "
            f"**Judged: working commit `{j['working_commit']}` (the league's label `{j['label']}`); the source in this "
            f"repository is one commit later, {j['difference']}** (RESULTS.md, section 2).")


def compare_rows(d, key, other, other_label):
    c = d["comparisons"][key]
    arms = d["arms"]
    return [
        [f"time saved vs {other_label}", f"**{pct(c['time_saved_pct'])}**"]
        + [pct(c["per_workload"][w]["time_saved_pct"]) for w in WORKLOADS],
        ["rounds won (time)", f"{c['runtime_wins']} of {c['rounds']}"]
        + [f"{c['per_workload'][w]['runtime_wins']} of {c['per_workload'][w]['rounds']}" for w in WORKLOADS],
        ["90% interval of the time figure", interval(c["interval90"])]
        + [interval(c["per_workload"][w]["interval90"]) for w in WORKLOADS],
        [f"peak memory saved vs {other_label}", f"**{pct(c['rss_saved_pct'])}**"]
        + [pct(c["per_workload"][w]["rss_saved_pct"]) for w in WORKLOADS],
        ["rounds won (memory)", f"{c['rss_wins']} of {c['rounds']}"]
        + [f"{c['per_workload'][w]['rss_wins']} of {c['per_workload'][w]['rounds']}" for w in WORKLOADS],
        [f"pass medians in seconds (suite = sum), this build / {other_label}",
         f"{sec(arms['candidate']['median_total_seconds'])} / {sec(arms[other]['median_total_seconds'])}"]
        + [f"{sec(arms['candidate']['per_workload'][w]['median_total_seconds'])} / "
           f"{sec(arms[other]['per_workload'][w]['median_total_seconds'])}" for w in WORKLOADS],
        [f"median peak memory, MiB, this build / {other_label}",
         f"{mib(arms['candidate']['median_peak_rss_bytes'])} / {mib(arms[other]['median_peak_rss_bytes'])}"]
        + [f"{mib(arms['candidate']['per_workload'][w]['median_peak_rss_bytes'])} / "
           f"{mib(arms[other]['per_workload'][w]['median_peak_rss_bytes'])}" for w in WORKLOADS],
    ]


HEAD = ["", "suite"] + WORKLOADS
SUITE_NOTE = ("Suite percentages and memory are means of the four workload medians; suite seconds are the **sum** of "
              "the four workload pass medians. Memory of a workload is the median over rounds of the mean of the "
              "seven phases' peak resident sizes; it is not a single maximum.")


def positions(d):
    """How often each arm ran first, second and third in a round (from the per-round records)."""
    orders = [x["order"] for x in d["comparisons"]["candidate_vs_stock"]["pair_changes"]]
    return {arm: [sum(1 for o in orders if o.index(arm) == i) for i in range(3)]
            for arm in ("candidate", "incumbent", "stock")}


def block_headline(roles):
    d = load(roles["headline"])
    selfcheck(d)
    rows = compare_rows(d, "candidate_vs_stock", "stock", "upstream")
    return (pending_lines(roles) + table(HEAD, rows) + "\n\n" + SUITE_NOTE + "\n\n" + conditions(d, roles)
            + "\n\n"
            "Time is the sum of the seven phases of one pass; \"time saved\" is the median over rounds of the paired "
            "difference in that round, in percent of the other arm.")


def phase_table(d, key, other):
    c = d["comparisons"][key]
    rows = []
    for p in PHASES:
        row = [p]
        for w in WORKLOADS:
            x = c["per_workload"][w]["per_phase"][p]
            iv = bootstrap_interval90(paired(d, other, w, p))
            row.append(f"{pct(x['time_saved_pct'])} ({x['time_wins']}/{x['rounds']}; {interval(iv)})")
        s = c["per_phase"][p]
        row.append(f"{pct(s['time_saved_pct'])} ({s['time_wins']}/{s['rounds']})")
        rows.append(row)
    return table(["phase"] + WORKLOADS + ["mean of 4"], rows)


def phase_abs(d, field, fmt, other):
    rows = []
    for p in PHASES:
        row = [p]
        for w in WORKLOADS:
            a = d["arms"]["candidate"]["per_workload"][w]["per_phase"][p][field]
            b = d["arms"][other]["per_workload"][w]["per_phase"][p][field]
            row.append(f"{fmt(a)} / {fmt(b)}")
        rows.append(row)
    return table(["phase"] + WORKLOADS, rows)


def phase_memory(d, key):
    c = d["comparisons"][key]
    rows = []
    for p in PHASES:
        row = [p]
        for w in WORKLOADS:
            x = c["per_workload"][w]["per_phase"][p]
            row.append(f"{pct(x['rss_saved_pct'])} ({x['rss_wins']}/{x['rounds']})")
        rows.append(row)
    return table(["phase"] + WORKLOADS, rows)


def block_final(roles):
    d = load(roles["headline"])
    selfcheck(d)
    e = d["eligibility"]
    out = [pending_lines(roles).rstrip("\n"), conditions(d, roles), "",
           "### 1.1 Against upstream restic (4 CPUs)", "",
           table(HEAD, compare_rows(d, "candidate_vs_stock", "stock", "upstream")), "", SUITE_NOTE, "",
           "### 1.2 Against the S020 incumbent, the league's last promoted tree (4 CPUs)", "",
           table(HEAD, compare_rows(d, "candidate_vs_incumbent", "incumbent", "S020")), "", SUITE_NOTE, "",
           "Order of the arms: it rotates through six patterns from round to round, which balances the positions "
           "over complete cycles of six. In this job of " + str(d["rounds"]) + " rounds the arms ran first / second / "
           "third: " + "; ".join(f"{n} {' / '.join(str(v) for v in positions(d)[k])}" for k, n in
                                 (("candidate", "this build"), ("incumbent", "S020"), ("stock", "upstream"))) + ".", "",
           "The judge also gives the league's own promotion verdict for this pair. It reads: eligible = "
           f"{str(e['eligible']).lower()}. Its reasons, verbatim:", ""]
    out += [f"- {r}" for r in e["reasons"]] or ["- (none)"]
    if not e["eligible"]:
        ci, pol = d["comparisons"]["candidate_vs_incumbent"], e["policy"]
        out += ["", f"**By the league's own rules this is a loss against S020: the tree would not have been promoted.** "
                f"It won {ci['score_wins']} of {ci['rounds']} rounds where {pol['score_wins']} are required; its suite "
                f"time is {pct(ci['time_saved_pct'])} where at least {pol['minimum_score_pct']:+.2f}% is required; its "
                f"peak memory is {pct(ci['rss_saved_pct'])} where the guard allows "
                f"-{pol['rss_guard_vs_incumbent_pct']:.2f}%."]
    return "\n".join(out).lstrip("\n")


def block_net(roles):
    d, bf = load(roles["headline"]), roles["before_fixes"]
    b = load(bf["name"])
    rows = []
    for key, label in (("candidate_vs_stock", "upstream"), ("candidate_vs_incumbent", "S020")):
        c, o = d["comparisons"][key], b["comparisons"][key]

        def cell(x):
            return f"{pct(x['time_saved_pct'])} ({x['runtime_wins']}/{x['rounds']}; {interval(x['interval90'])})"
        rows.append([f"suite time vs {label}", cell(o), cell(c), f"{c['time_saved_pct'] - o['time_saved_pct']:+.2f}"])
        for w in WORKLOADS:
            rows.append([f"{w}, time vs {label}", cell(o["per_workload"][w]), cell(c["per_workload"][w]),
                         f"{c['per_workload'][w]['time_saved_pct'] - o['per_workload'][w]['time_saved_pct']:+.2f}"])
        rows.append([f"suite memory vs {label}", f"{pct(o['rss_saved_pct'])} ({o['rss_wins']}/{o['rounds']})",
                     f"{pct(c['rss_saved_pct'])} ({c['rss_wins']}/{c['rounds']})",
                     f"{c['rss_saved_pct'] - o['rss_saved_pct']:+.2f}"])
    cs, os_ = d["comparisons"]["candidate_vs_stock"], b["comparisons"]["candidate_vs_stock"]
    ci, oi = d["comparisons"]["candidate_vs_incumbent"], b["comparisons"]["candidate_vs_incumbent"]
    head = ["", f"before: job `{b['job_id'][:8]}`, {b['rounds']} rounds", f"after: job `{d['job_id'][:8]}`, "
            f"{d['rounds']} rounds", "difference, points"]
    return (f"Before: {bf['what']}. After: the judged commit. Between them: {bf['between']}.\n\n"
            + table(head, rows) + "\n\n"
            f"The net price of those commits, read off two jobs: **{cs['time_saved_pct'] - os_['time_saved_pct']:+.2f} "
            f"suite points against upstream and {ci['time_saved_pct'] - oi['time_saved_pct']:+.2f} against S020.** "
            "This is a difference between two separate jobs, each with its own interval, not a paired measurement: "
            "the two trees never ran in the same round. Cells show rounds won and the 90% interval of each job.")


def block_phases(roles):
    d = load(roles["headline"])
    n = min(d["suite"]["rounds_per_workload"].values())
    out = [f"From job `{d['job_id'][:8]}`, {d['rounds']} rounds, {n} per workload, 4 CPUs. A cell reads: time saved "
           "(rounds won of rounds; 90% interval of the median, bootstrap as the judge computes it)."
           + (f" With {n} rounds per cell an interval is little more than the range of the values; the 44-round job "
              "gives the cells that count." if n < 8 else "")
           + " The noise between identical arms, per phase, is in METHODS.md, section 3: a cell smaller than its "
           "band is not established.", "",
           "### 3.1 All 28 cells against upstream: time saved", "",
           phase_table(d, "candidate_vs_stock", "stock"), "",
           "### 3.2 Median seconds per phase, this build / upstream", "",
           phase_abs(d, "median_seconds", sec, "stock"), "",
           "### 3.3 All 28 cells against the S020 incumbent: time saved", "",
           phase_table(d, "candidate_vs_incumbent", "incumbent")]
    return "\n".join(out)


def block_memory(roles):
    d = load(roles["headline"])
    cs, ci = d["comparisons"]["candidate_vs_stock"], d["comparisons"]["candidate_vs_incumbent"]
    arms = d["arms"]

    def miv(key, w):
        vals = [x["rss_saved_pct"] for x in d["comparisons"][key]["pair_changes"] if x["workload"] == w]
        return interval(bootstrap_interval90(vals))
    rows = []
    for w in WORKLOADS:
        a = arms["candidate"]["per_workload"][w]["median_peak_rss_bytes"]
        rows.append([w,
                     f"{mib(a)} / {mib(arms['stock']['per_workload'][w]['median_peak_rss_bytes'])} / "
                     f"{mib(arms['incumbent']['per_workload'][w]['median_peak_rss_bytes'])}",
                     f"{pct(cs['per_workload'][w]['rss_saved_pct'])} ({cs['per_workload'][w]['rss_wins']}/"
                     f"{cs['per_workload'][w]['rounds']}; {miv('candidate_vs_stock', w)})",
                     f"{pct(ci['per_workload'][w]['rss_saved_pct'])} ({ci['per_workload'][w]['rss_wins']}/"
                     f"{ci['per_workload'][w]['rounds']}; {miv('candidate_vs_incumbent', w)})"])
    out = [f"From job `{d['job_id'][:8]}`, 4 CPUs. Memory of a pass is the mean over the seven phases of each phase's "
           "peak resident size; the figure per workload is the median over rounds. The noise between identical arms "
           "is larger for memory than for time (METHODS.md, section 3).",
           "",
           table(["workload", "median MiB: this build / upstream / S020", "saved vs upstream (rounds won; 90% interval)",
                  "saved vs S020 (rounds won; 90% interval)"], rows), "",
           f"Suite: {pct(cs['rss_saved_pct'])} against upstream ({cs['rss_wins']} of {cs['rounds']} rounds), "
           f"{pct(ci['rss_saved_pct'])} against S020 ({ci['rss_wins']} of {ci['rounds']}).", "",
           "### 4.1 Peak memory per phase in MiB, this build / upstream", "",
           phase_abs(d, "median_peak_rss_bytes", mib, "stock"), "",
           "### 4.2 Peak memory per phase against upstream: saved, with rounds won", "",
           phase_memory(d, "candidate_vs_stock")]
    return "\n".join(out)


def block_arms(roles):
    rows = []
    for arm in roles["arms"]:
        d = load(arm["name"])
        ci, cs = d["comparisons"]["candidate_vs_incumbent"], d["comparisons"]["candidate_vs_stock"]
        rows.append([
            arm["what"], f"`{d['job_id'][:8]}`", str(d["rounds"]), arm["versus"],
            f"{pct(ci['time_saved_pct'])} ({interval(ci['interval90'])}; {ci['runtime_wins']}/{ci['rounds']})",
            " / ".join(f"{ci['per_workload'][w]['time_saved_pct']:+.2f}" for w in WORKLOADS),
            pct(ci["rss_saved_pct"]),
            f"{pct(cs['time_saved_pct'])} ({interval(cs['interval90'])}; {cs['runtime_wins']}/{cs['rounds']})",
            pct(cs["rss_saved_pct"]),
        ])
    head = ["job", "id", "rounds", "candidate vs reference", "suite time vs reference (90% interval; wins)",
            "main / big-index / large-files / remote", "memory vs reference",
            "suite time vs upstream (90% interval; wins)", "memory vs upstream"]
    return (table(head, rows) + "\n\nAll jobs: official judge lanes, 4 CPUs pinned, the same four workloads. "
            "Positive = the candidate needs less than the tree it is compared with. "
            "The result files are in `league/evidence/judge/` under the names in `index.json`.")


def block_identity(roles):
    i = json.loads((EVID / "source-identity.json").read_text(encoding="utf-8"))
    p, j = i["published"], i["source_equals"]
    u = i.get("against_upstream_subset", {})
    lines = [
        f"- The source here: every file of the last source commit (`HEAD~1`; the commit after it adds this README, "
        f"`.gitattributes` and `league/` and renames upstream's README) equals the league's working commit "
        f"`{j['working_commit'][:7]}` (branch `{j['working_label']}`, git tree id `{j['git_tree_id']}`). Checked twice "
        f"when the repository was assembled: every blob id and path equal, and SHA-256 of all {i['files']} files of "
        f"that commit against an export of the working commit: all equal = {str(i['all_equal']).lower()} "
        f"(`league/evidence/source-identity.json`).",
        f"- In this repository: commit `{p['commit_upstream_subset'][:10]}` is the upstream subset, "
        f"`{p['commit_league_tree_after_S019'][:10]}` the league tree after S019, then {p['replayed_commits']} "
        f"replayed commits up to `{p['commit_last_replayed'][:10]}`, whose git tree id is "
        f"`{p['git_tree_id_of_last_replayed_commit']}`. That id differs from the working commit's for one reason: "
        f"`{i['mode_difference']['file']}` carries its executable bit here, as it does upstream.",
        f"- Against the upstream subset of commit 1: {u.get('A', 0)} files added, {u.get('M', 0)} upstream files "
        f"changed, {u.get('D', 0)} deleted; {i['files']} files in all.",
    ]
    jd = i.get("judged_commit")
    if jd and jd["working_commit"] != j["working_commit"]:
        lines.append(
            f"- **What the judge measured is one commit earlier**: working commit `{jd['working_commit'][:7]}` "
            f"(label `{roles['judged']['label']}`). From it to the published source {len(jd['files_changed'])} files "
            f"change: " + ", ".join(f"`{f}`" for f in jd["files_changed"]) + ". "
            + ("In the non-test file every changed line is a comment line. " if jd.get("non_test_changes_are_comments")
               else "") + "RESULTS.md, section 2, says how the two builds were compared.")
    return "\n".join(lines)


def block_binaries(roles):
    f = EVID / "SHA256SUMS"
    if not f.exists():
        return ("**PENDING(binaries): no binaries are published yet.** The release binaries are built from the "
                "published source after the last measurement; their names and SHA-256 checksums are listed here "
                "then. Until then, build from source.")
    rows = []
    for line in f.read_text(encoding="utf-8").splitlines():
        if line.strip():
            h, name = line.split(None, 1)
            rows.append([f"`{name.strip().lstrip('*')}`", f"`{h}`"])
    i = json.loads((EVID / "source-identity.json").read_text(encoding="utf-8"))
    pub, jud = i["source_equals"]["working_commit"][:7], i["judged_commit"]["working_commit"][:7]
    return (table(["file", "SHA-256"], rows) + "\n\nThe same list is `league/evidence/SHA256SUMS`. The files "
            "themselves are not part of the source tree.\n\n"
            f"**What these files are.** They were built from the published commit (`{pub}`) with the judge's build "
            "flags plus one linker flag that sets the version string. **They are not the binary the judge measured, "
            f"and they were not themselves judged.** The judged binary is a build of the commit before (`{jud}`) "
            "without that flag. A build of the published commit without the flag has the judged machine code "
            "byte for byte. With the flag, the linux/amd64 file carries the same machine instructions at the same "
            "addresses as the judged build, with different address operands; no difference was resolved in the "
            "one timing of it against the judged binary, which covered one phase with 20 pairs. The other four "
            "files were built the same way and were not compared or timed. An independent reviewer rebuilt all five "
            "files offline from the published source with the documented command and got these five hashes "
            "(`league/evidence/independent-rebuild/`). RESULTS.md, section 2, and METHODS.md, section 12.2, give "
            "the detail and the command to rebuild a file and compare its checksum.")


def manycore():
    return json.loads((EVID / "many-core.json").read_text(encoding="utf-8"))


def up(text):
    """The league's records call upstream restic `stock`."""
    return re.sub(r"\bstock\b", "upstream", text)


def workload_table(part):
    rows = []
    for r in part["workloads"]:
        name = "**pass**" if r["phase"] == "pass" else r["phase"]
        rows.append([r["workload"], name, f"{r['cores_4']:+.2f}", f"{r['cores_8']:+.2f}", f"{r['cores_16']:+.2f}"])
    low = min((r for r in part["workloads"] if r["phase"] != "pass"), key=lambda r: min(r["cores_8"], r["cores_16"]))
    return (table(["workload", "phase", "4 cores (judge)", "8 cores (bench, 5 pairs)", "16 cores (bench, 5 pairs)"],
                  rows)
            + f"\n\nSmallest cell at 8 or 16 cores: {low['workload']} {low['phase']}, "
              f"{min(low['cores_8'], low['cores_16']):+.2f}%.")


def cpu_table(part):
    def total(u, s):
        return " / ".join(f"{float(x) + float(y):.1f}" for x, y in zip(u.split("/"), s.split("/")))
    rows = [[r["cell"], r["user_8"], r["system_8"], total(r["user_8"], r["system_8"]),
             r["user_16"], r["system_16"], total(r["user_16"], r["system_16"])] for r in part["cpu_time"]]
    return table(["cell", "user, 8 cores", "system, 8 cores", "user + system, 8 cores",
                  "user, 16 cores", "system, 16 cores", "user + system, 16 cores"], rows)


def manycore_part(m, part):
    mo = part["measured_on"]
    return (f"Measured on `{mo['label']}` (working commit `{mo['working_commit']}`: {mo['what']}). "
            f"4 cores: {part['four_cores']}. 8 and 16 cores: {part['eight_and_sixteen_cores']}. "
            f"**{m['pass_rows'][0].upper()}{m['pass_rows'][1:]}.**")


CPU_NOTE = ("User and system CPU in the five cells the bench record lists because this build's **user** CPU is higher "
            "there than upstream's (CPU seconds, mean of the five pairs, this build / upstream; the user + system "
            "columns add the two displayed figures). Higher user CPU does not always mean higher total CPU: in "
            "several of these cells the build's system CPU is lower by as much.")


def block_manycore(_roles):
    m = manycore()
    c = m["current"]
    return "\n".join([manycore_part(m, c), "", workload_table(c), "", CPU_NOTE, "", cpu_table(c)])


def block_manycore_earlier(_roles):
    m = manycore()
    e = m["earlier"]
    return "\n".join(["**An earlier reading, not a measurement of the published build.** " + manycore_part(m, e), "",
                      workload_table(e), "", "User and system CPU on that earlier tree, the same five cells:", "",
                      cpu_table(e)])


def block_dirs(_roles):
    d = manycore()["directory_restore"]
    t = d["before_after"]
    rows = []
    for name, before, after, upstream in t["rows"]:
        a, u = float(after.split("/")[0]), float(upstream.split("/")[0])
        rows.append([name, before, after, upstream, f"{100 * (1 - a / u):+.1f}%"])
    return (f"Restore of {d['shape']}; two runs each on the bench lanes, governor: performance. Cells: wall seconds "
            "/ user CPU seconds / voluntary context switches (M = million). \"Before\" is the tree with the nine "
            "fixes and without the hand-over commit, except where a cell says `rc-7`, which has the same restore "
            "code; \"after\" adds that commit.\n\n"
            + table(["set-up", "before", "after", "upstream", "after against upstream, wall time"], rows))


X11 = EVID / "release-checks-final3-b8cf439"
X11_NAMES = {"stock": "upstream", "tree": "judged commit (`rc-8`)", "rc7": "before the hand-over commit (`rc-7`)"}


def x11_runs(folder, name="runs.json"):
    return json.loads((X11 / folder / name).read_text(encoding="utf-8"))


def x11_cell(runs, cpus, binary, directories=None):
    """The three rotated rounds of one binary on one CPU set: rounds, medians, largest peak RSS."""
    rs = sorted((r for r in runs if r["cpus"] == cpus and r["binary"] == binary
                 and (directories is None or r["directories"] == directories)), key=lambda r: r["round"])
    if len(rs) != 3 or any(r["rc"] != 0 for r in rs):
        raise SystemExit(f"x11 runs: unexpected records for {cpus} {binary}")
    return {"rounds": " / ".join(f"{r['seconds']:.1f}" for r in rs),
            "wall": statistics.median(r["seconds"] for r in rs),
            "user": statistics.median(r["user_s"] for r in rs),
            "sys": statistics.median(r["sys_s"] for r in rs),
            "cpu": statistics.median(r["user_s"] + r["sys_s"] for r in rs),
            "rss": max(r["rss_mib"] for r in rs)}


def slower(wall, base):
    return f"{abs(100 * (wall / base - 1)):.1f}% " + ("slower" if wall > base else "faster")


def x11_binary_rows(runs, cpus_list, binaries, directories=None, first=None):
    rows = []
    for cpus in cpus_list:
        base = x11_cell(runs, cpus, "stock", directories)["wall"]
        for b in binaries:
            c = x11_cell(runs, cpus, b, directories)
            rows.append(([first] if first else []) + [
                cpus, X11_NAMES[b], c["rounds"], f"{c['wall']:.1f}", "" if b == "stock" else slower(c["wall"], base),
                f"{c['user']:.1f} + {c['sys']:.1f}", f"{c['cpu']:.1f}", f"{c['rss']:.1f}"])
    return rows


X11_HEAD = ["CPUs", "binary", "wall s, rounds 1 to 3", "median wall s", "against upstream",
            "user + system CPU s (medians)", "CPU s of a run (median)", "peak RSS MiB (largest)"]
X11_NOTE = ("The harness recorded seconds to one decimal. \"CPU s of a run\" is the median over the three rounds of "
            "user plus system of one run, so it can differ by 0.1 from the sum of the two medians beside it.")


def block_dirs_recheck(_roles):
    runs = x11_runs("x11-606060")
    cpus = list(dict.fromkeys(r["cpus"] for r in runs))
    return (table(X11_HEAD, x11_binary_rows(runs, cpus, ("stock", "tree", "rc7")))
            + "\n\nFrom `league/evidence/release-checks-final3-b8cf439/x11-606060/runs.json`, every run. " + X11_NOTE)


def block_dirs_large(_roles):
    runs = x11_runs("x11-large-partial")
    rows = x11_binary_rows(runs, ["all 16 check CPUs"], ("stock", "tree", "rc7"), 1010100)
    done = x11_runs("x11-large-partial", "rows-completed-before-stop.json")
    four = {r["binary"]: r for r in done if r["directories"] == 1010100 and r["cpus"] == "4 cores"}
    if set(four) != {"stock", "tree"} or any(r["rc"] != 0 or r["round"] != 1 for r in four.values()):
        raise SystemExit("x11 large partial: the 4-core records are not the two expected")
    c = json.loads((X11 / "c10" / "summary.json").read_text(encoding="utf-8"))["numbers"]["B_directories"]["2020200"]
    if any(len(c[b]["seconds_all"]) != 1 or c[b]["rc"] != 0 for b in ("stock", "tree", "rc7")):
        raise SystemExit("c10 part B: one run per binary expected")
    s, t, r7 = c["stock"]["seconds"], c["tree"]["seconds"], c["rc7"]["seconds"]
    return ("1,010,100 directories, three rotated rounds with all 16 check CPUs, from the run of the diagnostic "
            "that was stopped before its 4-core part was complete (`x11-large-partial/runs.json`):\n\n"
            + table(X11_HEAD, rows) + "\n\n"
            f"Its 4-core part finished one round for two binaries: upstream {four['stock']['seconds']:.1f} s, judged "
            f"commit {four['tree']['seconds']:.1f} s ({slower(four['tree']['seconds'], four['stock']['seconds'])}) "
            "(`x11-large-partial/rows-completed-before-stop.json`).\n\n"
            f"{c['directories']:,} directories and {c['files']} files, all 16 check CPUs, **one run per binary**, not "
            f"rotated (release check C10, part B; `c10/summary.json`): upstream {s:.1f} s, "
            f"judged commit {t:.1f} s ({slower(t, s)}), `rc-7` {r7:.1f} s ({slower(r7, s)}); peak RSS "
            f"{c['stock']['rss_mib']:.1f} / {c['tree']['rss_mib']:.1f} / {c['rc7']['rss_mib']:.1f} MiB.")


def block_dirs_governor(_roles):
    sets = [("x11-606060", "`schedutil`", {"all 16 check CPUs": "the official judge job", "4 cores": "the official judge job"}),
            ("x11-606060-schedutil-quiet", "`schedutil`", {"all 16 check CPUs": "nothing"}),
            ("x11-606060-performance", "`performance`", {"all 16 check CPUs": "a bench of the merge agent, light",
                                                         "4 cores": "nothing"})]
    rows = []
    for cpus in ("all 16 check CPUs", "4 cores"):
        for folder, governor, beside in sets:
            if cpus not in beside:
                continue
            runs = x11_runs(folder)
            u, t = x11_cell(runs, cpus, "stock"), x11_cell(runs, cpus, "tree")
            rows.append([cpus, governor, beside[cpus], f"{u['rounds']}; {u['wall']:.1f}", f"{t['rounds']}; {t['wall']:.1f}",
                         f"**{slower(t['wall'], u['wall'])}**", f"{u['user']:.1f} + {u['sys']:.1f} / {t['user']:.1f} + {t['sys']:.1f}",
                         f"{u['cpu']:.1f} / {t['cpu']:.1f}", f"{u['rss']:.1f} / {t['rss']:.1f}"])
    return (table(["check CPUs", "governor", "beside it", "upstream: wall s of rounds 1 to 3; median",
                   "judged commit: wall s of rounds 1 to 3; median", "judged commit against upstream",
                   "user + system CPU s (medians), upstream / judged commit",
                   "CPU s of a run (median), upstream / judged commit",
                   "peak RSS MiB (largest), upstream / judged commit"], rows)
            + "\n\nFrom `runs.json` in `x11-606060/`, `x11-606060-schedutil-quiet/` and `x11-606060-performance/` under "
              "`league/evidence/release-checks-final3-b8cf439/`, every run. " + X11_NOTE)


def generic_table(t, header=None):
    return table([up(h) for h in (header or t["header"])], [[up(c) for c in r] for r in t["rows"]])


def block_dirs_l3(_roles):
    d = manycore()["directory_restore"]
    return (generic_table(d["cache_layout"]) + "\n\nCells: wall seconds of the three rounds (user CPU s / system CPU "
            "s / voluntary context switches of the median round, as the record gives them).")


def block_harness_timings(_roles):
    return generic_table(manycore()["check_harness_timings"])


def patches():
    out = []
    files = sorted(p for p in (ROOT / "league" / "series").glob("*.patch") if not p.name.startswith("0000"))
    for n, p in enumerate(files, 1):
        text = p.read_text(encoding="utf-8", errors="replace")
        m = re.search(r"^Subject: (.*?)\n(?=\S|\n)", text, re.S | re.M)
        subject = re.sub(r"\s*\n\s+", " ", m.group(1)) if m else "?"
        subject = re.sub(r"^\[PATCH[^\]]*\]\s*", "", subject)
        files_changed = re.findall(r"^diff --git a/(\S+) b/", text, re.M)
        removed = set(re.findall(r"^diff --git a/(\S+) b/\S+\ndeleted file mode", text, re.M))
        out.append((n, p.name, subject, files_changed, removed))
    return out


def block_later_commits(_roles):
    rows = []
    for n, name, subject, files, _removed in patches():
        nontest = [f for f in files if not f.endswith("_test.go")]
        rows.append([str(n + 2), f"`{name[:4]}`", subject.replace("|", "\\|"), str(len(files)),
                     "test only" if not nontest else str(len(nontest))])
    return (table(["commit", "patch", "subject", "files", "non-test files"], rows) +
            f"\n\n{len(rows)} commits. Patch numbers are the file names in `league/series/`.")


def block_redteam_commits(_roles):
    rows = []
    for n, name, subject, files, removed in patches():
        if re.match(r"(Red team fix|Many directories|Release:|Review of final3)", subject):
            nontest = [f for f in files if not f.endswith("_test.go")]
            tests = [f for f in files if f.endswith("_test.go")]
            shown = [f"`{f}`" if f not in removed else f"{f} (removed)" for f in nontest]
            rows.append([str(n + 2), f"`{name[:4]}`", subject.replace("|", "\\|"),
                         ", ".join(shown) or "(none)", str(len(tests))])
    return table(["commit", "patch", "subject", "non-test files", "test files"], rows)


BLOCKS = {
    "README.md": {"headline": block_headline, "identity": block_identity, "binaries": block_binaries},
    "league/docs/RESULTS.md": {"final": block_final, "net": block_net, "phases": block_phases, "memory": block_memory,
                               "arms": block_arms, "many-core": block_manycore,
                               "many-core-earlier": block_manycore_earlier, "dirs": block_dirs,
                               "dirs-recheck": block_dirs_recheck, "dirs-large": block_dirs_large,
                               "dirs-governor": block_dirs_governor, "dirs-l3": block_dirs_l3,
                               "harness-timings": block_harness_timings},
    "league/docs/CHANGES.md": {"later-commits": block_later_commits},
    "league/docs/RED-TEAM.md": {"redteam-commits": block_redteam_commits},
}


def main():
    check = "--check" in sys.argv
    roles = json.loads((JUDGE / "roles.json").read_text(encoding="utf-8"))
    bad = []
    for rel, blocks in BLOCKS.items():
        path = ROOT / rel
        text = path.read_text(encoding="utf-8")
        new = text
        for name, fn in blocks.items():
            rx = re.compile(r"(<!-- BEGIN GENERATED:" + re.escape(name) + r" -->\n).*?(<!-- END GENERATED:"
                            + re.escape(name) + r" -->)", re.S)
            if not rx.search(new):
                raise SystemExit(f"{rel}: block {name} not found")
            body = fn(roles).strip("\n")
            new = rx.sub(lambda m, body=body: m.group(1) + body + "\n" + m.group(2), new)
        if new != text:
            if check:
                bad.append(rel)
            else:
                path.write_text(new, encoding="utf-8", newline="\n")
                print(f"rewrote {rel}")
    if check:
        if bad:
            print("not up to date: " + ", ".join(bad))
            return 1
        print("generated blocks are up to date")
    return 0


if __name__ == "__main__":
    sys.exit(main())
