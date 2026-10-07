# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""Restic League V2 judge: paired, order-rotated, oracle-gated time + peak-RSS measurement on the league box.

    python3 restic_judge.py --candidate DIR --incumbent DIR --stock DIR
                            --mode {probe|correctness|match|reference|promotion}
                            --out RESULT.json [--status STATUS.json] [--cpus 26-29] [--labels JSON]
                            [--resume] [--calibration] [--scratch TMPFS_DIR] [--workload NAME]
    python3 restic_judge.py setup {pristine|compat-baseline|isolation}      (one-time, trusted)

Design (JUDGE.md has the full derivation):
* Arms are restic Go module roots. Each is built by a trusted, offline, sandboxed build of
  STOCK + the arm's non-test *.go files under cmd/ and internal/ (nothing else is taken from the arm).
* Seven phases, each ONE restic process with stock defaults, run in bwrap as namespace PID 1
  (no network, capabilities dropped to CHOWN/FOWNER/DAC_OVERRIDE/FSETID, read-only inputs, a
  per-phase tmpfs work dir). Every arm starts every phase from an identical cloned state that
  stock produced once per judge invocation from the frozen workloads.
* Time = parent-side monotonic wall clock around the process. Peak RSS = wait4() ru_maxrss of the
  bwrap monitor whose only child is restic: the kernel reports max(monitor ~11 MiB, restic VmHWM),
  i.e. restic's exact high-water mark at exit (no polling, no sampling gaps).
* Correctness oracle BEFORE timing counts: a full per-arm oracle in the warm-up round (cross-reader
  restores, check --read-data by stock, corruption detection) and a per-phase oracle every round.
* Research fast path (probe/correctness only; FASTPATH.md): builds, compatibility receipts, the stock-made
  reference states + stock references, and known-good arms' full-oracle passes are reused from a
  content-addressed cache keyed by exact identities. The candidate's build input, compat tests (correctness)
  and full oracle always run. Official modes (match/reference/promotion) never touch the cache.
"""
from __future__ import annotations

import argparse
from datetime import datetime, timezone
import fcntl
import hashlib
import json
import os
from pathlib import Path
import random
import shutil
import signal
import stat
import statistics
import subprocess
import sys
import threading
import time

SCHEMA = "restic-linux-judge-v1"
ROOT = Path(os.environ.get("RESTIC_LEAGUE_ROOT", "/opt/league/restic-league-v2"))
TOOLCHAIN = ROOT / "toolchain"


def workload_root(name: str) -> Path:
    """Frozen workload by name: "main" = ROOT/workloads (the scored workload); any other name = ROOT/workloads-<name>
    (SUITE.md). Default main, so nothing changes unless a workload is chosen."""
    if name == "main":
        return ROOT / "workloads"
    if not name or not all(c.islower() or c.isdigit() or c == "-" for c in name) or name[0] == "-":
        raise SystemExit(f"invalid workload name {name!r}")
    return ROOT / f"workloads-{name}"


WORKLOAD_NAME = os.environ.get("RESTIC_JUDGE_WORKLOAD", "main")  # or --workload (main() overrides)
WORKLOADS = workload_root(WORKLOAD_NAME)
MIB = 1024 * 1024
PHASES = ["backup-initial", "backup-unchanged", "backup-mutated", "restore", "check-read-data", "forget-prune", "ls-json"]

# ---- gate constants, derived from the measured null (calibration/null-result.json, 18 rounds x 3 identical arms,
# 54 identical-arm pairs, 2026-09-24; JUDGE.md "Gate derivation"). Per-round paired SD: time 1.110%, peak-RSS 2.224%,
# score 1.215%. SE of the median score: 6 rounds 0.62%, 12 rounds 0.44%, 42 rounds 0.235%.
# MATCH 6 = every orders() pattern of three arms once (each arm 2/2/2 across positions); a screen, not a gate.
MATCH_ROUNDS = int(os.environ.get("RESTIC_JUDGE_MATCH_ROUNDS", "6"))
# REFERENCE 12 = two full order cycles; reads incumbent-vs-stock to +/-0.44% (SE) for season calibration.
REFERENCE_ROUNDS = int(os.environ.get("RESTIC_JUDGE_REFERENCE_ROUNDS", "12"))
# PROMOTION 42 rounds (7 order cycles), >= 29 score wins, median score >= 0.25%. Empirical-null simulation:
# false positive 1.2%; power 65% / 83% / 89% / 100% at true score effects of 0.5 / 0.75 / 1.0 / 1.5%.
# The median floor sits ~1 SE above zero and well below the ~1% effect the rule is sized to catch.
PROMOTION_ROUNDS = int(os.environ.get("RESTIC_JUDGE_PROMOTION_ROUNDS", "42"))
PROMOTION_WINS = int(os.environ.get("RESTIC_JUDGE_PROMOTION_WINS", "29"))
MINIMUM_SCORE_PCT = float(os.environ.get("RESTIC_JUDGE_MIN_SCORE_PCT", "0.25"))
# Operator policy (INTERFACE.md): neither axis may be worse than -5% at the median. Null SE of a 42-round axis median is
# 0.22% (time) / 0.43% (RSS), so an unchanged tree cannot trip it (> 11 SE); it binds only real trades.
AXIS_REGRESSION_CAP_PCT = float(os.environ.get("RESTIC_JUDGE_AXIS_REGRESSION_CAP_PCT", "5.0"))
# Operator 2026-10-01 ("Return to Luna 6 Max and target speed ... scoring to match"): from S013 the score is the
# workload TIME saved (weight 1.0; S001-S012 used 0.5 = equal time + memory). Memory becomes a guard rail: a
# promotion may not grow peak memory more than RSS_GUARD_PCT vs the incumbent (null SE of the 42-round RSS median is
# 0.43%, so +2% is ~5 SE: an unchanged tree cannot trip it).
TIME_WEIGHT = float(os.environ.get("RESTIC_JUDGE_TIME_WEIGHT", "1.0"))
RSS_GUARD_PCT = float(os.environ.get("RESTIC_JUDGE_RSS_GUARD_PCT", "2.0"))
# Suite jobs only (--workloads a,b,c; SUITE.md): a promotion may not lose more than this much time on ANY one workload.
WORKLOAD_REGRESSION_PCT = float(os.environ.get("RESTIC_JUDGE_WORKLOAD_REGRESSION_PCT", "1.0"))
MODE_ROUNDS ={"probe": 1, "correctness": 0, "match": MATCH_ROUNDS, "reference": REFERENCE_ROUNDS,
               "promotion": PROMOTION_ROUNDS}

DEFAULT_CPUS = "26-29"          # CCD4 (L3 shared by 24-29 + SMT 72-77); 24 is a pylint lane, 25 is a buffer
OPERATION_TIMEOUT = float(os.environ.get("RESTIC_JUDGE_OPERATION_TIMEOUT", "900"))
BUILD_TAGS = "selfupdate disable_grpc_modules"
BUILD_LDFLAGS = "-s -w"
GO_VERSION = "go version go1.27.0 linux/amd64"
RSS_MEASUREMENT_METHOD = "bwrap-as-pid-1-monitor-wait4-ru_maxrss-v1"
SOURCE_EXCLUDED_DIRS = {".git", "reference-evidence", "reference-prototypes", "reference-continuity",
                        "experiments", "__pycache__", ".agents"}
# Stock *_test.go files of these packages (and the cmd/restic integration subset) are the compatibility
# subset; the internal APIs they call are therefore frozen for candidates.
COMPAT_PACKAGES = [
    "./internal/archiver", "./internal/repository", "./internal/repository/index", "./internal/repository/pack",
    "./internal/repository/hashing", "./internal/restorer", "./internal/checker", "./internal/data",
    "./internal/restic", "./internal/walker", "./internal/bloblru", "./internal/backend/local", "./internal/fs",
]
COMPAT_CMD_RUN = "."  # the whole cmd/restic integration suite
KEEP_CAPS = ["CAP_CHOWN", "CAP_FOWNER", "CAP_DAC_OVERRIDE", "CAP_FSETID"]
# ---- research fast path (probe/correctness only; see FASTPATH.md). RESTIC_JUDGE_FASTPATH=0 restores the full path.
RESEARCH_MODES = ("probe", "correctness")
FASTPATH = os.environ.get("RESTIC_JUDGE_FASTPATH", "1") != "0"
FASTPATH_VERSION = 1
# probe timing states: "shared" = probes use the bundle's states (default; FASTPATH.md: incumbent-vs-stock over 20 shared
# probes matched 72 full-path S002 probes within 1 SE, and per-job states did not change the paired readings);
# "fresh" = each probe builds its own S1a/Shist realization in its scratch exactly as the full path (+~110 s)
PROBE_STATES = os.environ.get("RESTIC_JUDGE_PROBE_STATES", "shared")
CACHE_ROOT = Path(os.environ.get("RESTIC_JUDGE_CACHE_ROOT", str(ROOT / "cache")))       # disk: builds, receipts, locks
BUNDLE_ROOT = Path(os.environ.get("RESTIC_JUDGE_BUNDLE_ROOT", "/dev/shm/restic-league-v2-cache"))  # tmpfs: states
BUNDLE_DATA = ("corpusA", "corpusB", "pristine", "states")


def bundle_root() -> Path:
    """Main keeps BUNDLE_ROOT; another workload gets its own subfolder, so neither one's bundle cleanup can evict the
    other's reference states."""
    return BUNDLE_ROOT if WORKLOAD_NAME == "main" else BUNDLE_ROOT / f"workload-{WORKLOAD_NAME}"


def set_workload(name: str) -> None:
    """Select the frozen workload every module-level helper reads (suite jobs switch per round)."""
    global WORKLOAD_NAME, WORKLOADS
    WORKLOAD_NAME, WORKLOADS = name, workload_root(name)


_WORKLOAD_SETTINGS: dict = {}


def workload_settings() -> dict:
    """Per-workload knobs frozen in workloads.json (absent for main, so its behaviour is unchanged):
    phase_args (per-phase argument overrides, e.g. big-index's forget --keep-tag ballast) and min_unmodified_v2."""
    key = str(WORKLOADS)
    if key not in _WORKLOAD_SETTINGS:
        try:
            frozen = load(WORKLOADS / "workloads.json")
        except (OSError, ValueError):
            frozen = {}
        _WORKLOAD_SETTINGS[key] = {"phase_args": frozen.get("phase_args") or {},
                                   "min_unmodified_v2": int(frozen.get("min_unmodified_v2", 50000)),
                                   "remote": frozen.get("remote") or None}
    return _WORKLOAD_SETTINGS[key]


# ---- remote-backend workloads (REMOTE.md). A workload whose frozen workloads.json has a "remote" block is measured
# through restic's REST backend: the judge starts the workload's own frozen server (WORKLOADS/bin/restserver, a
# simulated network: fixed latency per request, a rate cap per connection, a connection limit) on a unix socket inside
# the phase work dir, outside the sandbox, and the measured restic gets REMOTE_REPOSITORY instead of /work/repo. The
# server's storage is a local-layout repository OUTSIDE the sandbox's /work, moved to work/repo once the server has
# stopped, so the oracle (stock reading /work/repo) is unchanged. Workloads without the block never reach this code.
REMOTE_SOCKET = "rest.sock"
REMOTE_REPOSITORY = "rest:http+unix:///work/" + REMOTE_SOCKET + ":/"


def remote_server_argv(profile: dict, socket: Path, store: Path, stats: Path) -> list[str]:
    return [str(WORKLOADS / "bin" / "restserver"), "-listen", str(socket), "-root", str(store),
            "-latency-ms", repr(float(profile["latency_ms"])), "-rate", str(int(profile["rate_bytes_per_second"])),
            "-max-connections", str(int(profile["max_connections"])), "-stats", str(stats)]


def remote_summary(rounds: list[dict], measured: list[str]) -> dict:
    """Per arm and phase: median requests and bytes of the measured runs, and the first round's per-type table
    ({} unless the rounds hold remote-backend rows, so other workloads' results are unchanged)."""
    out = {}
    for arm in measured:
        phases = {}
        for phase in PHASES:
            rows = [r["remote"] for saved in rounds for r in saved.get("rows", [])
                    if r.get("arm") == arm and r.get("phase") == phase and r.get("remote")]
            if not rows:
                continue
            phases[phase] = {**{key: statistics.median(row[key] for row in rows)
                                for key in ("requests", "bytes_in", "bytes_out", "latency_seconds", "request_seconds",
                                            "queue_seconds", "server_cpu_seconds")},
                             "max_in_flight": max(row["max_in_flight"] for row in rows),
                             "rounds": len(rows), "by_first_round": rows[0]["by"]}
        if phases:
            out[arm] = phases
    return out


def parse_workload_list(text: str) -> list[str]:
    """--workloads a,b,c: distinct valid names, order kept."""
    names = [part.strip() for part in str(text).split(",") if part.strip()]
    if not names or len(set(names)) != len(names):
        raise SystemExit(f"--workloads needs distinct names: {text!r}")
    for name in names:
        workload_root(name)
    return names


def suite_rounds(mode_rounds: int, count: int) -> int:
    """Rounds of a suite job: the mode's rounds rounded up to a multiple of the workload count (probe = one round per
    workload; 6/12/42 stay 6/12/42 for 2 or 3 workloads)."""
    return 0 if mode_rounds == 0 else -(-mode_rounds // count) * count


def suite_schedule(names: list[str], measured: list[str], rounds: int) -> list[tuple[str, list[str]]]:
    """Round r (1-based) measures workload names[(r-1) % N]; each workload has its own orders() rotation, so its k-th
    round uses the k-th order pattern (position balance holds within every workload)."""
    per = {name: orders(measured, -(-rounds // len(names))) for name in names}
    return [(names[(r - 1) % len(names)], per[names[(r - 1) % len(names)]][(r - 1) // len(names)])
            for r in range(1, rounds + 1)]


def workload_record() -> dict:
    """Which frozen workload a job used (recorded in every result)."""
    record = {"name": WORKLOAD_NAME, "root": str(WORKLOADS)}
    try:
        frozen = load(WORKLOADS / "workloads.json")
        record.update(version=frozen.get("version"), workloads_json_sha256=sha(WORKLOADS / "workloads.json"))
    except (OSError, ValueError):
        pass
    return record


# ============================================================ small trusted helpers
def now() -> str:
    return datetime.now(timezone.utc).isoformat()


def atomic(path: Path, value) -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(path.name + ".tmp")
    with temporary.open("w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=1, sort_keys=True, allow_nan=False)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def append(path: Path, value) -> None:
    with Path(path).open("a", encoding="utf-8") as stream:
        stream.write(json.dumps(value, sort_keys=True, allow_nan=False) + "\n")
        stream.flush()
        os.fsync(stream.fileno())


def sha_bytes(data: bytes) -> str:
    return hashlib.sha256(data).hexdigest()


def sha(path) -> str:
    fd = os.open(path, os.O_RDONLY | getattr(os, "O_NOATIME", 0))
    with os.fdopen(fd, "rb") as stream:
        return hashlib.file_digest(stream, "sha256").hexdigest()


def canonical_sha(value) -> str:
    return sha_bytes(json.dumps(value, sort_keys=True, separators=(",", ":")).encode())


def load(path):
    return json.loads(Path(path).read_text(encoding="utf-8"))


def tree_files(root: Path, excluded=frozenset()) -> dict[str, str]:
    """{relative posix path: sha256} over regular files (links and specials are not followed)."""
    root = Path(root)
    result = {}

    def visit(directory: Path, parts: tuple):
        with os.scandir(directory) as entries:
            for entry in sorted(entries, key=lambda e: e.name):
                if entry.name in excluded:
                    continue
                info = entry.stat(follow_symlinks=False)
                if stat.S_ISDIR(info.st_mode):
                    visit(Path(entry.path), parts + (entry.name,))
                elif stat.S_ISREG(info.st_mode):
                    result["/".join(parts + (entry.name,))] = sha(entry.path)
    visit(root, ())
    return result


def tree_sha256_of_files(root: Path, excluded=frozenset()) -> str:
    return canonical_sha(tree_files(root, excluded))


def source_identity(root: Path) -> dict:
    """Controller-shared rule: every regular file except the excluded directories."""
    files = tree_files(root, SOURCE_EXCLUDED_DIRS)
    return {"path": str(root), "sha256": canonical_sha(files), "files": len(files)}


# ============================================================ filesystem manifest (the restore oracle)
def manifest(root: Path) -> dict:
    """Exact metadata+content manifest: kind, mode, uid, gid, mtime_ns, size, sha256, link target,
    xattrs, hardlink groups. Paths are relative (root itself is '.')."""
    root = str(root)
    entries = {}
    inodes = {}

    def record(path: str, rel: str):
        info = os.lstat(path)
        mode = info.st_mode
        row = {"m": stat.S_IMODE(mode), "u": info.st_uid, "g": info.st_gid}
        if stat.S_ISLNK(mode):
            row.update(k="symlink", l=os.readlink(path), t=info.st_mtime_ns)
        elif stat.S_ISDIR(mode):
            row.update(k="dir", t=info.st_mtime_ns)
        elif stat.S_ISREG(mode):
            row.update(k="file", s=info.st_size, h=sha(path), t=info.st_mtime_ns)
            if info.st_nlink > 1:
                inodes.setdefault((info.st_dev, info.st_ino), []).append(rel)
        elif stat.S_ISFIFO(mode):
            row.update(k="fifo", t=info.st_mtime_ns)
        else:
            row.update(k="other:" + oct(stat.S_IFMT(mode)))
        if not stat.S_ISLNK(mode):
            names = sorted(os.listxattr(path, follow_symlinks=False))
            if names:
                row["x"] = {name: os.getxattr(path, name, follow_symlinks=False).hex() for name in names}
        entries[rel] = row
        if stat.S_ISDIR(mode):
            with os.scandir(path) as children:
                for child in sorted(children, key=lambda e: e.name):
                    record(child.path, child.name if rel == "." else rel + "/" + child.name)

    record(root, ".")
    for group in inodes.values():
        if len(group) < 2:
            continue  # nlink counts links outside this tree too (e.g. the judge's hardlink farm)
        label = min(group)
        for rel in group:
            entries[rel]["i"] = label
    return entries


def manifest_summary(entries: dict) -> dict:
    kinds = {}
    for row in entries.values():
        kinds[row["k"]] = kinds.get(row["k"], 0) + 1
    return {"entries": len(entries), "kinds": kinds,
            "bytes": sum(row.get("s", 0) for row in entries.values()),
            "hardlinked": sum("i" in row for row in entries.values())}


def manifest_diff(expected: dict, actual: dict, limit: int = 12) -> list:
    out = []
    for key in sorted(set(expected) | set(actual)):
        if expected.get(key) != actual.get(key):
            out.append({"path": key, "expected": expected.get(key), "actual": actual.get(key)})
            if len(out) >= limit:
                break
    return out


def apply_mutations(root: Path, mutation_dir: Path) -> None:
    """Apply the frozen, declarative mutation spec in place (deterministic; used by generator and judge)."""
    root = Path(root)
    spec = load(Path(mutation_dir) / "spec.json")
    payload = Path(mutation_dir) / "payload"
    touched = set()

    def parent(rel):
        touched.add((root / rel).parent)

    def detach(path: Path) -> None:
        """Break a hardlink before modifying content or inode metadata, preserving all metadata."""
        info = os.lstat(path)
        if not stat.S_ISREG(info.st_mode) or info.st_nlink < 2:
            return
        folder = os.lstat(path.parent)
        temporary = path.with_name(path.name + ".detach-tmp")
        if info.st_blocks * 512 < info.st_size:  # sparse (large-files VM image): keep the holes on tmpfs
            subprocess.run(["cp", "--sparse=always", "--preserve=all", "--reflink=never", str(path), str(temporary)],
                           check=True)
        else:
            shutil.copy2(path, temporary, follow_symlinks=False)
        os.chown(temporary, info.st_uid, info.st_gid)
        os.chmod(temporary, stat.S_IMODE(info.st_mode))
        os.utime(temporary, ns=(info.st_atime_ns, info.st_mtime_ns))
        os.replace(temporary, path)
        os.utime(path.parent, ns=(folder.st_atime_ns, folder.st_mtime_ns))  # detaching is invisible

    for op in spec["ops"]:
        kind = op["op"]
        target = root / op["path"] if "path" in op else None
        if target is not None and kind in ("write", "append", "patch", "truncate", "chmod", "setxattr") and os.path.lexists(target):
            detach(target)
        if kind == "write":
            parent(op["path"])
            target.write_bytes((payload / op["payload"]).read_bytes())
            if "mode" in op:
                os.chmod(target, op["mode"])
            os.utime(target, ns=(op["mtime_ns"], op["mtime_ns"]))
        elif kind == "append":
            with target.open("ab") as stream:
                stream.write((payload / op["payload"]).read_bytes())
            os.utime(target, ns=(op["mtime_ns"], op["mtime_ns"]))
        elif kind == "patch":
            with target.open("r+b") as stream:
                stream.seek(op["offset"])
                stream.write((payload / op["payload"]).read_bytes())
            os.utime(target, ns=(op["mtime_ns"], op["mtime_ns"]))
        elif kind == "truncate":
            os.truncate(target, op["size"])
            os.utime(target, ns=(op["mtime_ns"], op["mtime_ns"]))
        elif kind == "delete":
            parent(op["path"])
            target.unlink()
        elif kind == "rmtree":
            parent(op["path"])
            shutil.rmtree(target)
        elif kind == "rename":
            parent(op["path"])
            parent(op["to"])
            os.rename(target, root / op["to"])
            moved = {p for p in touched if p == target or target in p.parents}
            touched -= moved
            touched |= {root / op["to"] / p.relative_to(target) for p in moved}
        elif kind == "mkdir":
            parent(op["path"])
            target.mkdir()
            os.chmod(target, op["mode"])
            touched.add(target)
        elif kind == "chmod":
            os.chmod(target, op["mode"])
        elif kind == "symlink":
            parent(op["path"])
            target.unlink()
            os.symlink(op["target"], target)
            os.utime(target, ns=(op["mtime_ns"], op["mtime_ns"]), follow_symlinks=False)
        elif kind == "setxattr":
            os.setxattr(target, op["name"], bytes.fromhex(op["value_hex"]))
        elif kind == "dir_mtimes":
            for directory in sorted(touched, key=lambda p: len(p.parts), reverse=True):
                if directory.exists():
                    os.utime(directory, ns=(op["mtime_ns"], op["mtime_ns"]))
        else:
            raise ValueError(f"unknown mutation op {kind}")


def parse_labels(text: str | None) -> dict:
    """Labels are JSON, or "b64:" + urlsafe-base64 JSON (the broker's form: survives any shell/ssh quoting)."""
    if not text:
        return {}
    try:
        if text.startswith("b64:"):
            import base64
            text = base64.urlsafe_b64decode(text[4:].encode()).decode()
        value = json.loads(text)
        return value if isinstance(value, dict) else {"value": value}
    except (ValueError, UnicodeDecodeError):
        return {"unparsed_labels": text}


# ============================================================ statistics
def orders(arms: list[str], rounds: int) -> list[list[str]]:
    """Rotations plus their reverses balance position and predecessor effects (pylint judge)."""
    rotations = [arms[offset:] + arms[:offset] for offset in range(len(arms))]
    reversed_arms = list(reversed(arms))
    rotations += [reversed_arms[offset:] + reversed_arms[:offset] for offset in range(len(arms))]
    return [rotations[index % len(rotations)] for index in range(rounds)]


def bootstrap_interval90(values: list[float], resamples: int = 4000, seed: int = 0) -> list[float] | None:
    if len(values) < 2:
        return None
    rng = random.Random(seed)
    medians = sorted(statistics.median(rng.choices(values, k=len(values))) for _ in range(resamples))
    return [medians[int(0.05 * resamples)], medians[int(0.95 * resamples) - 1]]


def compare(rounds: list[dict], cand: str, ref: str) -> dict:
    pairs = []
    per_phase = {phase: [] for phase in PHASES}
    for row in rounds:
        values = row["values"]
        c, r = values[cand], values[ref]
        time_saved = 100 * (1 - c["seconds"] / r["seconds"])
        rss_saved = 100 * (1 - c["mean_peak_rss_bytes"] / r["mean_peak_rss_bytes"])
        pairs.append({"round": row["round"], "order": row["order"], "time_saved_pct": time_saved,
                      "rss_saved_pct": rss_saved,
                      "score_pct": TIME_WEIGHT * time_saved + (1 - TIME_WEIGHT) * rss_saved})
        for phase in PHASES:
            cp, rp = c["phases"][phase], r["phases"][phase]
            per_phase[phase].append((100 * (1 - cp["seconds"] / rp["seconds"]),
                                     100 * (1 - cp["peak_rss_bytes"] / rp["peak_rss_bytes"])))
    if not pairs:
        return {"rounds": 0}
    scores = [p["score_pct"] for p in pairs]
    return {
        "score_pct": statistics.median(scores),
        "score_wins": sum(s > 0 for s in scores),
        "time_saved_pct": statistics.median(p["time_saved_pct"] for p in pairs),
        "rss_saved_pct": statistics.median(p["rss_saved_pct"] for p in pairs),
        "runtime_wins": sum(p["time_saved_pct"] > 0 for p in pairs),
        "rss_wins": sum(p["rss_saved_pct"] > 0 for p in pairs),
        "rounds": len(pairs),
        "interval90": bootstrap_interval90(scores),
        "per_phase": {
            phase: {"time_saved_pct": statistics.median(t for t, _ in rows),
                    "rss_saved_pct": statistics.median(m for _, m in rows),
                    "wins": sum((TIME_WEIGHT * t + (1 - TIME_WEIGHT) * m) > 0 for t, m in rows),
                    "time_wins": sum(t > 0 for t, _ in rows), "rss_wins": sum(m > 0 for _, m in rows),
                    "rounds": len(rows)}
            for phase, rows in per_phase.items()
        },
        "pair_changes": pairs,
    }


def stratified_interval90(groups: list[list[float]], resamples: int = 4000, seed: int = 0) -> list[float] | None:
    """90% bootstrap interval of the mean over groups of each group's median (each group resampled on its own)."""
    if not groups or any(len(g) < 2 for g in groups):
        return None
    rng = random.Random(seed)
    stats = sorted(statistics.fmean(statistics.median(rng.choices(g, k=len(g))) for g in groups)
                   for _ in range(resamples))
    return [stats[int(0.05 * resamples)], stats[int(0.95 * resamples) - 1]]


def compare_suite(rounds_by_workload: dict[str, list[dict]], cand: str, ref: str) -> dict:
    """Suite comparison (SUITE.md). Every workload is compared on its own rounds exactly as a single-workload job
    (compare()); the aggregate score/time/rss = MEAN over workloads of each workload's MEDIAN paired value, so every
    workload weighs the same whatever its seconds; score_wins / runtime_wins / rss_wins = rounds whose OWN-workload
    paired value is > 0; per_phase = mean over workloads of the phase medians, wins summed."""
    per = {name: compare(rows, cand, ref) for name, rows in rounds_by_workload.items() if rows}
    if not per:
        return {"rounds": 0}
    mean = lambda key: statistics.fmean(c[key] for c in per.values())  # noqa: E731
    phases = {}
    for phase in PHASES:
        cells = [c["per_phase"][phase] for c in per.values()]
        phases[phase] = {"time_saved_pct": statistics.fmean(x["time_saved_pct"] for x in cells),
                         "rss_saved_pct": statistics.fmean(x["rss_saved_pct"] for x in cells),
                         **{k: sum(x[k] for x in cells) for k in ("wins", "time_wins", "rss_wins", "rounds")}}
    pairs = [dict(pair, workload=name) for name, c in per.items() for pair in c["pair_changes"]]
    return {
        "score_pct": mean("score_pct"), "score_wins": sum(c["score_wins"] for c in per.values()),
        "time_saved_pct": mean("time_saved_pct"), "rss_saved_pct": mean("rss_saved_pct"),
        "runtime_wins": sum(c["runtime_wins"] for c in per.values()),
        "rss_wins": sum(c["rss_wins"] for c in per.values()),
        "rounds": sum(c["rounds"] for c in per.values()),
        "interval90": stratified_interval90([[p["score_pct"] for p in c["pair_changes"]] for c in per.values()]),
        "per_phase": phases,
        "pair_changes": sorted(pairs, key=lambda p: p["round"]),
        "per_workload": {name: {k: v for k, v in c.items() if k != "pair_changes"} for name, c in per.items()},
        "workloads": list(per),
        "aggregate": "suite: mean over workloads of each workload's median paired value; wins = rounds whose own-workload "
                     "value is > 0",
    }


def suite_gate_reasons(inc: dict, workload_regression_pct: float, rss_guard_pct: float) -> list[str]:
    """Extra promotion gates of a suite job (on top of the single-workload gates applied to the aggregate): every
    workload's peak memory within the guard and no workload's time worse than -workload_regression_pct vs incumbent."""
    reasons = []
    for name, c in (inc.get("per_workload") or {}).items():
        if c["rss_saved_pct"] < -rss_guard_pct:
            reasons.append(f"workload {name}: peak memory versus incumbent is {c['rss_saved_pct']:.3f}%, beyond the "
                           f"-{rss_guard_pct}% memory guard")
        if c["time_saved_pct"] < -workload_regression_pct:
            reasons.append(f"workload {name}: time versus incumbent is {c['time_saved_pct']:.3f}%, worse than the "
                           f"-{workload_regression_pct}% per-workload regression tolerance")
    return reasons


# ============================================================ sandbox + measurement
def parse_cpus(text: str) -> list[int]:
    cpus = []
    for part in text.replace(" ", "").split(","):
        if not part:
            continue
        low, _, high = part.partition("-")
        cpus.extend(range(int(low), int(high or low) + 1))
    return sorted(set(cpus))


def is_tmpfs(path: Path) -> bool:
    path = str(Path(path).resolve())
    best, fstype = "", None
    with open("/proc/self/mounts") as stream:
        for line in stream:
            fields = line.split()
            mount = fields[1].replace("\\040", " ")
            if (path == mount or path.startswith(mount.rstrip("/") + "/")) and len(mount) >= len(best):
                best, fstype = mount, fields[2]
    return fstype == "tmpfs"


BWRAP_BASE = ["/usr/bin/bwrap", "--unshare-pid", "--unshare-net", "--unshare-ipc", "--unshare-uts",
              "--unshare-cgroup-try", "--hostname", "league", "--die-with-parent", "--new-session"]


def restic_sandbox(binary: Path, work: Path, data: Path | None, env_procs: int, repo: Path | None = None,
                   repository: str = "/work/repo") -> list[str]:
    """Exactly one process: restic as PID 1 of a fresh PID namespace, child of the bwrap monitor.
    repository: /work/repo (every local workload) or REMOTE_REPOSITORY (a remote-backend workload's measured run)."""
    argv = BWRAP_BASE + ["--as-pid-1", "--cap-drop", "ALL"]
    for cap in KEEP_CAPS:
        argv += ["--cap-add", cap]
    argv += ["--proc", "/proc", "--dev", "/dev",
             "--ro-bind", str(WORKLOADS / "etc" / "passwd"), "/etc/passwd",
             "--ro-bind", str(WORKLOADS / "etc" / "group"), "/etc/group",
             "--ro-bind", str(WORKLOADS / "password"), "/etc/restic-password",
             "--ro-bind", str(binary), "/restic",
             "--bind", str(work), "/work"]
    if repo is not None:
        argv += ["--bind", str(repo), "/work/repo"]
    if data is not None:
        argv += ["--ro-bind", str(data), "/data"]
    argv += ["--remount-ro", "/", "--chdir", "/work", "--clearenv",
             "--setenv", "HOME", "/work/home", "--setenv", "TMPDIR", "/work/tmp",
             "--setenv", "GOMAXPROCS", str(env_procs), "--setenv", "TZ", "UTC", "--setenv", "USER", "root",
             "--", "/restic", "-r", repository, "--password-file", "/etc/restic-password"]
    return argv


def build_sandbox(work: Path, procs: int, extra_env: dict | None = None, unprivileged: bool = False) -> list[str]:
    argv = BWRAP_BASE + (["--unshare-user", "--uid", "1000", "--gid", "1000"] if unprivileged else []) + ["--cap-drop", "ALL",
                         "--ro-bind", "/usr", "/usr", "--symlink", "usr/lib", "/lib", "--symlink", "usr/lib64", "/lib64",
                         "--symlink", "usr/bin", "/bin", "--symlink", "usr/sbin", "/sbin",
                         "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
                         "--ro-bind", str(WORKLOADS / "etc" / "passwd"), "/etc/passwd",
                         "--ro-bind", str(WORKLOADS / "etc" / "group"), "/etc/group",
                         "--ro-bind", str(TOOLCHAIN / "go"), "/toolchain/go",
                         "--ro-bind", str(TOOLCHAIN / "gomodcache"), "/toolchain/gomodcache",
                         "--bind", str(work), "/build", "--chdir", "/build/src", "--clearenv"]
    env = {"PATH": "/toolchain/go/bin:/usr/bin:/bin", "HOME": "/build/home", "TMPDIR": "/build/tmp",
           "GOROOT": "/toolchain/go", "GOTOOLCHAIN": "local", "GOMODCACHE": "/toolchain/gomodcache",
           "GOCACHE": "/build/gocache", "GOFLAGS": "-mod=readonly", "GOPROXY": "off", "GOSUMDB": "off",
           "GONOSUMCHECK": "1", "GOTELEMETRY": "off", "CGO_ENABLED": "0", "GOOS": "linux", "GOARCH": "amd64",
           "GOAMD64": "v1", "GOMAXPROCS": str(procs), "TZ": "UTC"}
    env.update(extra_env or {})
    for key, value in sorted(env.items()):
        argv += ["--setenv", key, value]
    return argv + ["--"]


def measured_run(argv: list[str], stdout_path: Path, stderr_path: Path, timeout: float) -> dict:
    """Launch, block in wait4 (no polling on the measured cores), return wall seconds + exact ru_maxrss."""
    with open(stdout_path, "wb") as out, open(stderr_path, "wb") as err:
        began = time.monotonic()
        process = subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=out, stderr=err, start_new_session=True)
        timed_out = threading.Event()

        def kill():
            timed_out.set()
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
        timer = threading.Timer(timeout, kill)
        timer.start()
        try:
            _, status, usage = os.wait4(process.pid, 0)
        finally:
            timer.cancel()
        seconds = time.monotonic() - began
    process.returncode = os.waitstatus_to_exitcode(status)
    return {"seconds": seconds, "peak_rss_bytes": usage.ru_maxrss * 1024, "exit_code": process.returncode,
            "timed_out": timed_out.is_set(), "user_seconds": usage.ru_utime, "system_seconds": usage.ru_stime,
            "minor_faults": usage.ru_minflt, "voluntary_switches": usage.ru_nvcsw,
            "involuntary_switches": usage.ru_nivcsw}


def copy_tree(source: Path, target: Path) -> None:
    if target.exists():
        shutil.rmtree(target)
    subprocess.run(["cp", "-a", "--reflink=never", str(source), str(target)], check=True)


def tail(path: Path, size: int = 3000) -> str:
    try:
        data = Path(path).read_bytes()
    except OSError:
        return ""
    return data[-size:].decode("utf-8", "replace")


# ============================================================ arm snapshots + trusted build
class ArmTree:
    """An immutable per-run snapshot of an arm: its full file map plus copies of the files that differ
    from stock (candidate trees are live team workspaces; nothing is read from them after the snapshot)."""

    def __init__(self, files: dict, root: Path, stock: Path):
        self.files, self.root, self.stock = files, root, stock

    def path(self, rel: str) -> Path:
        copied = self.root / "tree" / rel
        return copied if copied.is_file() else self.stock / rel

    @property
    def identity(self) -> dict:
        return {"sha256": canonical_sha(self.files), "files": len(self.files)}


def snapshot_arm(tree: Path, stock: Path, stock_files: dict, dest: Path) -> ArmTree:
    if (dest / "files.json").exists():
        files = load(dest / "files.json")
        arm = ArmTree(files, dest, stock)
        for rel, digest in files.items():
            if sha(arm.path(rel)) != digest:
                raise RuntimeError(f"saved arm snapshot changed: {rel}")
        return arm
    temporary = dest.with_name(dest.name + ".partial")
    shutil.rmtree(temporary, ignore_errors=True)
    files = {}
    for rel in sorted(tree_files(tree, SOURCE_EXCLUDED_DIRS)):
        data = (tree / rel).read_bytes()
        files[rel] = sha_bytes(data)
        if stock_files.get(rel) != files[rel]:
            target = temporary / "tree" / rel
            target.parent.mkdir(parents=True, exist_ok=True)
            target.write_bytes(data)
    atomic(temporary / "files.json", files)
    shutil.rmtree(dest, ignore_errors=True)
    os.replace(temporary, dest)
    return ArmTree(files, dest, stock)


def stock_tree(stock: Path) -> ArmTree:
    return ArmTree(tree_files(stock, SOURCE_EXCLUDED_DIRS), stock / ".no-copies", stock)


def build_input(arm: ArmTree, stock: ArmTree) -> tuple[dict, dict]:
    """STOCK + the arm's non-test *.go under cmd/ and internal/. Returns (file map, policy report)."""
    stock_files = stock.files
    arm_files = arm.files

    def promotable(rel: str) -> bool:
        return rel.endswith(".go") and not rel.endswith("_test.go") and rel.split("/")[0] in ("cmd", "internal")

    files = {rel: ("stock", digest) for rel, digest in stock_files.items() if not promotable(rel)}
    for rel, digest in arm_files.items():
        if promotable(rel):
            files[rel] = ("arm", digest)
    ignored = sorted(rel for rel, digest in arm_files.items()
                     if not promotable(rel) and stock_files.get(rel) != digest)
    removed = sorted(rel for rel in stock_files if promotable(rel) and rel not in arm_files)
    changed = sorted(rel for rel, digest in arm_files.items() if promotable(rel) and stock_files.get(rel) != digest)
    violations = []
    for name in ("go.mod", "go.sum"):
        if arm_files.get(name) != stock_files.get(name):
            violations.append(f"{name} differs from stock (no dependency changes are allowed)")
    report = {"changed_promotable": changed, "removed_promotable": removed,
              "ignored_non_promotable_differences": ignored[:200], "ignored_count": len(ignored),
              "violations": violations}
    return files, report


def code_sha256(binary: Path) -> str:
    """sha256 of an ELF binary with its .note.go.buildid and .note.gnu.build-id contents zeroed: the build IDs
    hash the source bytes (so e.g. CRLF vs LF sources give different IDs), everything else is the machine code."""
    import struct
    data = bytearray(Path(binary).read_bytes())
    if data[:4] != b"ELF" or data[4] != 2:
        raise ValueError("not an ELF64 binary")
    shoff, = struct.unpack_from("<Q", data, 0x28)
    shentsize, shnum, shstrndx = struct.unpack_from("<HHH", data, 0x3A)
    headers = [struct.unpack_from("<IIQQQQIIQQ", data, shoff + index * shentsize) for index in range(shnum)]
    strtab = headers[shstrndx]
    for header in headers:
        name_offset = strtab[4] + header[0]
        name = bytes(data[name_offset:data.index(0, name_offset)]).decode()
        if name in (".note.go.buildid", ".note.gnu.build-id"):
            data[header[4]:header[4] + header[5]] = bytes(header[5])
    return sha_bytes(bytes(data))


def materialize(files: dict, arm_tree: ArmTree, stock: ArmTree, target: Path) -> None:
    for rel, (origin, digest) in sorted(files.items()):
        source = (arm_tree if origin == "arm" else stock).path(rel)
        destination = target / rel
        destination.parent.mkdir(parents=True, exist_ok=True)
        data = source.read_bytes()
        if sha_bytes(data) != digest:
            raise RuntimeError(f"source changed while materializing: {source}")
        destination.write_bytes(data)
        os.chmod(destination, 0o644)


def trusted_build(name: str, arm_tree: ArmTree, stock: ArmTree, scratch: Path, evidence: Path, procs: int) -> dict:
    files, policy = build_input(arm_tree, stock)
    identity = canonical_sha({rel: digest for rel, (_, digest) in files.items()})
    receipt_path = evidence / "builds" / f"{name}.json"
    binary_path = evidence / "builds" / f"{name}.restic"
    if receipt_path.exists() and binary_path.exists():
        saved = load(receipt_path)
        if saved.get("build_input_sha256") == identity and saved.get("binary_sha256") == sha(binary_path) and saved.get("ok"):
            return saved
    work = scratch / "build" / name
    if work.exists():
        shutil.rmtree(work)
    for sub in ("src", "gocache", "home", "tmp", "out"):
        (work / sub).mkdir(parents=True)
    materialize(files, arm_tree, stock, work / "src")
    argv = build_sandbox(work, procs) + ["/toolchain/go/bin/go", "build", "-trimpath", "-buildvcs=false",
                                         "-tags", BUILD_TAGS, "-ldflags", BUILD_LDFLAGS, "-o", "/build/out/restic", "./cmd/restic"]
    (evidence / "builds").mkdir(parents=True, exist_ok=True)
    run = measured_run(argv, evidence / "builds" / f"{name}.stdout", evidence / "builds" / f"{name}.stderr", 1200)
    receipt = {"arm": name, "build_input_sha256": identity, "build_input_files": len(files), "policy": policy,
               "argv": argv, "exit_code": run["exit_code"], "seconds": run["seconds"],
               "ok": run["exit_code"] == 0 and (work / "out" / "restic").is_file() and not policy["violations"],
               "stderr_tail": tail(evidence / "builds" / f"{name}.stderr")}
    if run["exit_code"] == 0 and (work / "out" / "restic").is_file():
        shutil.copyfile(work / "out" / "restic", binary_path)
        os.chmod(binary_path, 0o555)
        receipt["binary_sha256"] = sha(binary_path)
        receipt["code_sha256"] = code_sha256(binary_path)
        receipt["binary_bytes"] = binary_path.stat().st_size
        version = subprocess.run([str(binary_path), "version"], capture_output=True, text=True, timeout=30)
        receipt["version"] = version.stdout.strip()
    shutil.rmtree(work, ignore_errors=True)
    atomic(receipt_path, receipt)
    return receipt


def compatibility_tests(name: str, arm_tree: ArmTree, stock: ArmTree, scratch: Path, evidence: Path, procs: int,
                        baseline: dict | None) -> dict:
    """Stock *_test.go of the pinned packages against the arm's non-test code; 0 failures, and every
    test that passes on stock in this sandbox must pass."""
    files, _ = build_input(arm_tree, stock)
    identity = canonical_sha({rel: digest for rel, (_, digest) in files.items()})
    receipt_path = evidence / "compat" / f"{name}.json"
    if receipt_path.exists():
        saved = load(receipt_path)
        if saved.get("build_input_sha256") == identity and saved.get("status") == "complete":
            return saved
    work = scratch / "compat" / name
    if work.exists():
        shutil.rmtree(work)
    for sub in ("src", "gocache", "home", "tmp"):
        (work / sub).mkdir(parents=True)
    materialize(files, arm_tree, stock, work / "src")
    (evidence / "compat").mkdir(parents=True, exist_ok=True)
    results = {}
    runs = []
    for label, args in (("packages", COMPAT_PACKAGES), ("cmd", ["-run", COMPAT_CMD_RUN, "./cmd/restic"])):
        argv = build_sandbox(work, procs, unprivileged=True) + ["/toolchain/go/bin/go", "test", "-count=1", "-json", "-p", str(procs),
                                             "-tags", BUILD_TAGS, "-timeout", "20m", *args]
        out = evidence / "compat" / f"{name}-{label}.jsonl"
        run = measured_run(argv, out, evidence / "compat" / f"{name}-{label}.stderr", 2400)
        runs.append({"label": label, "exit_code": run["exit_code"], "seconds": run["seconds"], "timed_out": run["timed_out"]})
        output_tail = []
        for line in out.read_text(errors="replace").splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                continue
            if event.get("Action") in ("pass", "fail", "skip") and event.get("Test"):
                results[f"{event['Package']}::{event['Test']}"] = event["Action"]
            elif event.get("Action") == "fail" and not event.get("Test"):
                results[f"{event['Package']}::<package>"] = "fail"
            if event.get("Action") == "output" and len(output_tail) < 4000:
                output_tail.append(event.get("Output", ""))
    shutil.rmtree(work, ignore_errors=True)
    failed = sorted(k for k, v in results.items() if v == "fail")
    missing = []
    if baseline is not None:
        # stock's own environmental failures in this sandbox (no FUSE, no `python`, user-namespace ownership)
        # are tolerated; any other failure, and any stock-passing test not passing, is a compatibility failure
        stock_failed = {k for k, v in baseline["results"].items() if v == "fail"}
        failed = [k for k in failed if k not in stock_failed]
        missing = sorted(k for k, v in baseline["results"].items() if v == "pass" and results.get(k) != "pass")
    receipt = {"status": "complete", "arm": name, "build_input_sha256": identity, "runs": runs,
               "counts": {a: sum(v == a for v in results.values()) for a in ("pass", "fail", "skip")},
               "failed": failed[:100], "baseline_pass_not_passing": missing[:100],
               "passed": baseline is not None and not failed and not missing and not any(r["timed_out"] for r in runs),
               "results": results,
               "output_tail": "".join(output_tail)[-6000:] if (failed or missing) else "",
               "scope": "stock *_test.go of pinned packages + cmd/restic integration subset " + COMPAT_CMD_RUN}
    atomic(receipt_path, receipt)
    return receipt


class ExpectedManifest:
    """A stock-produced expected restore manifest: digest in memory, full manifest on disk for diffs."""

    def __init__(self, sha256: str, path: Path):
        self.sha256, self.path = sha256, path

    def matches(self, actual: dict) -> bool:
        return canonical_sha(actual) == self.sha256

    def diff(self, actual: dict) -> str:
        return json.dumps(manifest_diff(load(self.path), actual, 5))[:1500]


# ============================================================ research cache helpers (probe/correctness only)
def build_template(procs: int) -> list[str]:
    """The trusted build command with the per-job work path abstracted (part of the build cache key)."""
    return build_sandbox(Path("/WORK"), procs) + ["/toolchain/go/bin/go", "build", "-trimpath", "-buildvcs=false",
                                                  "-tags", BUILD_TAGS, "-ldflags", BUILD_LDFLAGS, "-o", "/build/out/restic",
                                                  "./cmd/restic"]


def compat_template(procs: int) -> list[list[str]]:
    base = build_sandbox(Path("/WORK"), procs, unprivileged=True)
    return [base + ["/toolchain/go/bin/go", "test", "-count=1", "-json", "-p", str(procs), "-tags", BUILD_TAGS,
                    "-timeout", "20m", *args]
            for args in (COMPAT_PACKAGES, ["-run", COMPAT_CMD_RUN, "./cmd/restic"])]


def cache_write(path: Path, value) -> None:
    """Atomic JSON write with a writer-unique temporary name (concurrent jobs may publish the same key)."""
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f"{path.name}.tmp-{os.getpid()}-{threading.get_ident()}")
    with temporary.open("w", encoding="utf-8") as stream:
        json.dump(value, stream, indent=1, sort_keys=True, allow_nan=False)
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def bundle_signature(root: Path) -> dict:
    """lstat signature of every entry of a reference-state bundle (kind/mode, inode, size, mtime, ctime, nlink,
    owner). Any write, chmod, chown, xattr change, link, unlink or rename changes a ctime or a directory row, and
    ctime cannot be set from userspace, so an unchanged signature means the bundle is exactly as stock left it."""
    rows = []

    def visit(path: str, rel: str) -> None:
        info = os.lstat(path)
        rows.append([rel, info.st_mode, info.st_ino, info.st_size, info.st_mtime_ns, info.st_ctime_ns, info.st_nlink,
                     info.st_uid, info.st_gid])
        if stat.S_ISDIR(info.st_mode):
            with os.scandir(path) as entries:
                names = sorted(entry.name for entry in entries)
            for name in names:
                visit(os.path.join(path, name), rel + "/" + name)

    for name in BUNDLE_DATA:
        visit(os.path.join(str(root), name), name)
    return {"sha256": canonical_sha(rows), "entries": len(rows)}


# ============================================================ the judge
class Judge:
    def __init__(self, args):
        self.args = args
        self.mode = args.mode
        self.out = args.out.resolve()
        self.evidence = (Path(str(self.out)[:-5] + "-evidence") if str(self.out).endswith(".json")
                         else Path(str(self.out) + "-evidence"))
        self.status_path = (args.status or self.out.with_name("status.json")).resolve()
        self.lanes = [lane for lane in args.cpus.split("/") if lane]  # "6-9/12-15": take whichever lane is free
        if len({len(parse_cpus(lane)) for lane in self.lanes}) != 1:
            raise SystemExit("alternative CPU lanes must have the same core count")
        self.cpus = parse_cpus(self.lanes[0])
        self.procs = len(self.cpus)
        self.labels = parse_labels(args.labels)
        self.started_at = now()
        self.units_done = 0
        self.units_total = 1
        self.stage = "starting"
        self.t0 = time.monotonic()

    # ---- observability (AGENTS.md section 17)
    def progress(self, stage: str, state: str = "running", **extra) -> None:
        self.stage = stage
        value = {"status": state, "units_done": self.units_done, "units_total": self.units_total,
                 "started_at": self.started_at, "updated_at": now(), "stage": stage, "pid": os.getpid(),
                 "mode": self.mode, "elapsed_seconds_this_invocation": round(time.monotonic() - self.t0, 1), **extra}
        atomic(self.status_path, value)
        append(self.evidence / "events.jsonl", {"timestamp": now(), "event": "progress", **value})
        print(json.dumps(value, sort_keys=True), flush=True)

    def event(self, **value) -> None:
        append(self.evidence / "events.jsonl", {"timestamp": now(), **value})

    # ---- identities
    def toolchain_identity(self) -> dict:
        go = TOOLCHAIN / "go" / "bin" / "go"
        version = subprocess.run([str(go), "version"], capture_output=True, text=True, timeout=60,
                                 env={"GOTOOLCHAIN": "local", "PATH": "/usr/bin:/bin"}).stdout.strip()
        if version != GO_VERSION:
            raise RuntimeError(f"toolchain is {version!r}, expected {GO_VERSION!r}")
        return {"go_version": version, "go_binary_sha256": sha(go),
                "goroot_sha256": tree_sha256_of_files(TOOLCHAIN / "go"),
                "gomodcache_sha256": tree_sha256_of_files(TOOLCHAIN / "gomodcache")}

    def workload_identity(self) -> dict:
        frozen = load(WORKLOADS / "workloads.json")
        v1 = manifest(WORKLOADS / "corpus-v1")
        if canonical_sha(v1) != frozen["v1"]["sha256"]:
            raise RuntimeError("frozen corpus-v1 on disk differs from its manifest")
        if canonical_sha(load(WORKLOADS / "manifest-v1.json")) != frozen["v1"]["sha256"]:
            raise RuntimeError("manifest-v1.json differs from workloads.json")
        if canonical_sha(load(WORKLOADS / "manifest-v2.json")) != frozen["v2"]["sha256"]:
            raise RuntimeError("manifest-v2.json differs from workloads.json")
        if tree_sha256_of_files(WORKLOADS / "mutation") != frozen["mutation"]["tree_sha256"]:
            raise RuntimeError("frozen mutation set differs")
        pristine = load(WORKLOADS / "pristine.json")
        if tree_sha256_of_files(WORKLOADS / "pristine-repo") != pristine["tree_sha256"]:
            raise RuntimeError("frozen pristine repository differs")
        identity = {"name": WORKLOAD_NAME, "root": str(WORKLOADS),
                    "workloads_json_sha256": sha(WORKLOADS / "workloads.json"), "version": frozen["version"],
                    "v1_sha256": frozen["v1"]["sha256"], "v2_sha256": frozen["v2"]["sha256"],
                    "mutation_sha256": frozen["mutation"]["tree_sha256"], "pristine_repo_sha256": pristine["tree_sha256"],
                    "etc_sha256": tree_sha256_of_files(WORKLOADS / "etc"), "password_sha256": sha(WORKLOADS / "password"),
                    "v1_summary": {k: frozen["v1"][k] for k in ("entries", "bytes", "kinds")}}
        if frozen.get("remote"):  # remote-backend workload: the frozen network profile and server are part of it
            if sha(WORKLOADS / "bin" / "restserver") != frozen["remote"]["server_sha256"]:
                raise RuntimeError("frozen REST server differs from workloads.json")
            identity["remote"] = frozen["remote"]
        return identity

    # ---- restic invocation inside the sandbox
    def restic(self, arm: str, args: list[str], work: Path, data: Path | None, label: str,
               stdout_keep: bool = False, repo: Path | None = None, remote_store: Path | None = None) -> dict:
        for sub in ("home", "tmp"):
            (work / sub).mkdir(exist_ok=True)
        if repo is not None:
            (work / "repo").mkdir(exist_ok=True)
        stdout_path, stderr_path = work / f"{label}.stdout", work / f"{label}.stderr"
        if remote_store is None:
            argv = restic_sandbox(self.binaries[arm], work, data, self.procs, repo=repo) + args
            run = launcher_run(argv, stdout_path, stderr_path, OPERATION_TIMEOUT)
        else:  # remote-backend workload (REMOTE.md): the frozen REST server runs outside the sandbox and the clock
            argv = restic_sandbox(self.binaries[arm], work, data, self.procs, repository=REMOTE_REPOSITORY) + args
            stats_path = work / "rest-stats.json"
            # The server binds the socket by its RELATIVE name from cwd = work: a unix socket path may hold 107
            # bytes, and <supervisor-jobs>/<job id>/scratch/<key>/work/<arm>/rest.sock is 130 or more (2026-10-05).
            with open(work / "rest-server.stderr", "wb") as server_err:
                server = subprocess.Popen(remote_server_argv(workload_settings()["remote"], Path(REMOTE_SOCKET),
                                                             Path(os.path.abspath(remote_store)),
                                                             Path(os.path.abspath(stats_path))), cwd=work,
                                          stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=server_err)
            try:
                deadline = time.monotonic() + 20
                while not (work / REMOTE_SOCKET).exists():
                    if server.poll() is not None or time.monotonic() > deadline:
                        raise RuntimeError("REST server did not start: " + tail(work / "rest-server.stderr"))
                    time.sleep(0.005)
                run = launcher_run(argv, stdout_path, stderr_path, OPERATION_TIMEOUT)
            finally:
                if server.poll() is None:
                    server.send_signal(signal.SIGTERM)
                try:
                    server.wait(timeout=60)
                except subprocess.TimeoutExpired:
                    server.kill()
                    server.wait()
            run["remote"] = load(stats_path)
        run.update(arm=arm, label=label, args=args, stdout_sha256=sha(stdout_path),
                   stdout_tail=tail(stdout_path, 1500), stderr_tail=tail(stderr_path, 1500))
        if stdout_keep:
            run["stdout_path"] = str(stdout_path)
        return run

    def fresh_work(self, name: str) -> Path:
        work = self.scratch / "work" / name
        if work.exists():
            shutil.rmtree(work)
        work.mkdir(parents=True)
        return work

    def stock_snapshots(self, work: Path, repo: Path | None = None) -> list:
        run = self.restic("stock", ["snapshots", "--json"], work, None, "oracle-snapshots", stdout_keep=True, repo=repo)
        if run["exit_code"] != 0:
            raise RuntimeError("stock snapshots failed: " + run["stderr_tail"])
        return json.loads(Path(run["stdout_path"]).read_text())

    @staticmethod
    def normalize_snapshot(snapshot: dict) -> dict:
        value = {k: v for k, v in snapshot.items() if k not in ("id", "short_id", "time", "program_version")}
        summary = dict(value.get("summary") or {})
        for key in ("backup_start", "backup_end", "data_added_packed"):
            summary.pop(key, None)
        value["summary"] = summary
        return value

    def stock_restore_manifest(self, work: Path, snapshot: str, label: str, repo: Path | None = None) -> dict:
        target = work / "oracle-restore"
        if target.exists():
            shutil.rmtree(target)
        run = self.restic("stock", ["restore", snapshot, "--target", "/work/oracle-restore"], work, None, label, repo=repo)
        if run["exit_code"] != 0:
            raise RuntimeError(f"{label}: stock restore failed: {run['stderr_tail'][-800:]}")
        actual = manifest(target / "data")
        shutil.rmtree(target)
        return actual

    # ---- states (stock-made, per invocation, identical input for every arm)
    def build_states(self) -> None:
        """corpusA = cp -a of frozen v1; corpusB = cp -al hardlink farm of A, then the frozen mutation set
        (which detaches every file it touches), so unchanged files in B share inode+ctime with A and
        S1a (stock's backup of A) is the natural parent snapshot for backing up B. Shist = S1a + v2 + v3."""
        s = self.state_root
        self.progress("copy frozen corpus onto tmpfs (A: v1 copy, B: hardlink farm of A)")
        copy_tree(WORKLOADS / "corpus-v1", s / "corpusA")
        subprocess.run(["cp", "-al", str(s / "corpusA"), str(s / "corpusB")], check=True)
        v1 = load(WORKLOADS / "manifest-v1.json")
        for name in ("corpusA", "corpusB"):
            if manifest(s / name) != v1:
                raise RuntimeError(f"tmpfs {name} does not reproduce the frozen v1 manifest")
        copy_tree(WORKLOADS / "pristine-repo", s / "pristine")
        (s / "states").mkdir(exist_ok=True)
        states = {}

        def backup(work: Path, data: Path, label: str, extra=()) -> None:
            run = self.restic("stock", ["backup", *extra, "/data/src"], work, data, "state-" + label)
            if run["exit_code"] != 0:
                raise RuntimeError(f"stock state backup {label} failed: {run['stderr_tail']}")
            states[label] = {"seconds": run["seconds"], "peak_rss_bytes": run["peak_rss_bytes"],
                             "stdout_tail": run["stdout_tail"][-700:]}

        self.progress("stock builds state S1a (v1 from corpus A)")
        work = self.fresh_work("state-S1a")
        copy_tree(s / "pristine", work / "repo")
        backup(work, s / "corpusA", "S1a")
        os.rename(work / "repo", s / "states" / "S1a")
        apply_mutations(s / "corpusB", WORKLOADS / "mutation")
        if canonical_sha(manifest(s / "corpusB")) != load(WORKLOADS / "workloads.json")["v2"]["sha256"]:
            raise RuntimeError("mutated corpus B does not reproduce the frozen v2 manifest")
        if manifest(s / "corpusA") != v1:
            raise RuntimeError("mutating the hardlink farm B changed corpus A")
        spec = load(WORKLOADS / "mutation" / "spec.json")
        # history after S1a: main = v2, then v3 = v2 minus excludes; a workload may list its own N snapshots of corpus B
        # (spec "history": [{"label", "exclude"}], first = full v2, last = the one forget --keep-last 1 keeps = "v3")
        plan = ([(row["label"], row["exclude"]) for row in spec["history"]] if "history" in spec
                else [("v2", []), ("v3", spec["exclude_for_history_v3"])])
        if plan[0] != ("v2", []) or plan[-1][0] != "v3" or len(plan) < 2:
            raise RuntimeError(f"history plan must start with a full v2 and end with v3: {plan}")
        self.progress("stock builds history state Shist (v1, v2, v3 = v2 minus excludes)" if "history" not in spec
                      else f"stock builds history state Shist (v1 + {len(plan)} snapshots of corpus B)")
        work = self.fresh_work("state-Shist")
        copy_tree(s / "states" / "S1a", work / "repo")
        for label, exclude in plan:
            backup(work, s / "corpusB", "Shist-" + label, [arg for path in exclude for arg in ("--exclude", path)])
        os.rename(work / "repo", s / "states" / "Shist")
        oracle = self.fresh_work("oracle-states")
        for label in ("S1a", "Shist"):
            snaps = self.stock_snapshots(oracle, repo=s / "states" / label)
            states[label + "-snapshots"] = [{"id": x["id"], "tree": x["tree"], "parent": x.get("parent"),
                                             "summary": x.get("summary")} for x in snaps]
        history = states["Shist-snapshots"]
        base = len(states["S1a-snapshots"]) - 1  # snapshots already in the pristine repository (big-index ballast: 1)
        if (len(history) != base + 1 + len(plan)
                or history[base + 1]["summary"].get("files_unmodified", 0) < workload_settings()["min_unmodified_v2"]):
            raise RuntimeError(f"history state is not v1->v2->...->v3 with v1 as v2's parent: {history}")
        self.progress("stock restores v1, v2, v3 to fix the expected restore manifests")
        self.expected_restore = {}
        differences = {}
        for version, snapshot, repo in (("v1", "latest", "S1a"), ("v2", history[base + 1]["id"], "Shist"),
                                        ("v3", history[-1]["id"], "Shist")):
            entries = self.stock_restore_manifest(oracle, snapshot, "expect-" + version, repo=s / "states" / repo)
            path = self.evidence / f"{getattr(self, 'evidence_tag', '')}expected-restore-{version}.json"
            atomic(path, entries)
            self.expected_restore[version] = ExpectedManifest(canonical_sha(entries), path)
            if version in ("v1", "v2"):
                differences[version] = manifest_diff(load(WORKLOADS / f"manifest-{version}.json"), entries, 50)
            del entries
        shutil.rmtree(oracle)
        states["stock_restore_vs_source_differences"] = differences
        for label in ("S1a", "Shist"):
            states[label + "-tree_sha256"] = tree_sha256_of_files(s / "states" / label)
        states["expected_restore_sha256"] = {k: v.sha256 for k, v in self.expected_restore.items()}
        self.states = states

    # ---- one phase for one arm (measured or not), with its per-phase oracle
    PHASE_STATE = {"backup-initial": ("pristine", "corpusA"), "backup-unchanged": ("S1a", "corpusA"),
                   "backup-mutated": ("S1a", "corpusB"), "restore": ("S1a", None),
                   "check-read-data": ("S1a", None), "forget-prune": ("Shist", None), "ls-json": ("S1a", None)}
    PHASE_ARGS = {"backup-initial": ["backup", "/data/src"], "backup-unchanged": ["backup", "/data/src"],
                  "backup-mutated": ["backup", "/data/src"], "restore": ["restore", "latest", "--target", "/work/restore"],
                  "check-read-data": ["check", "--read-data"], "forget-prune": ["forget", "--keep-last", "1", "--prune"],
                  "ls-json": ["ls", "--json", "latest"]}
    PHASE_EXPECTED = {"backup-initial": "v1", "backup-unchanged": "v1", "backup-mutated": "v2", "forget-prune": "v3"}

    def phase_args(self, phase: str) -> list[str]:
        """The phase's restic arguments; a workload may override a phase in its frozen workloads.json (big-index:
        forget keeps the ballast snapshot by tag). Main has no overrides."""
        return list(workload_settings()["phase_args"].get(phase, self.PHASE_ARGS[phase]))

    def run_phase(self, arm: str, phase: str, round_index: int, full_oracle: bool) -> dict:
        state, data = self.PHASE_STATE[phase]
        work = self.fresh_work(f"{arm}")
        repo_source = self.state_root / ("pristine" if state == "pristine" else f"states/{state}")
        data_path = self.state_root / data if data else None
        if workload_settings()["remote"] is None:
            copy_tree(repo_source, work / "repo")
            run = self.restic(arm, self.phase_args(phase), work, data_path, phase, stdout_keep=(phase == "ls-json"))
        else:  # the repository is the REST server's storage, out of the sandbox's sight until the server has stopped
            store = work.with_name(work.name + ".remote-store")
            copy_tree(repo_source, store)
            try:
                run = self.restic(arm, self.phase_args(phase), work, data_path, phase, stdout_keep=(phase == "ls-json"),
                                  remote_store=store)
                os.rename(store, work / "repo")
            finally:
                shutil.rmtree(store, ignore_errors=True)
        row = {"phase": phase, "arm": arm, "round": round_index, "binary_sha256": self.binary_sha[arm], **run}
        row.pop("stdout_path", None)
        failures = []
        if run["timed_out"] or run["exit_code"] != 0:
            failures.append(f"exit {run['exit_code']} timeout={run['timed_out']}: {run['stderr_tail'][-800:]}")
        else:
            try:
                failures += self.phase_oracle(arm, phase, work, round_index, full_oracle, row)
            except Exception as exc:  # an oracle that cannot read the arm's output is a failed oracle
                failures.append(f"{phase}: oracle error: {exc}")
        row["verified"] = not failures
        row["oracle_failures"] = failures
        row["full_oracle"] = full_oracle
        shutil.rmtree(work, ignore_errors=True)
        return row

    def phase_oracle(self, arm, phase, work, round_index, full_oracle, row) -> list[str]:
        failures = []
        ref = self.references
        if phase.startswith("backup-"):
            snaps = self.stock_snapshots(work)
            latest = snaps[-1]
            row["snapshot_tree"] = latest["tree"]
            normalized = self.normalize_snapshot(latest)
            if phase not in ref:
                if arm != "stock":
                    raise RuntimeError("references must be established by stock first")
                ref[phase] = {"normalized_snapshot": normalized, "count": len(snaps), "tree": latest["tree"]}
            expected = ref[phase]
            if len(snaps) != expected["count"]:
                failures.append(f"{phase}: {len(snaps)} snapshots, expected {expected['count']}")
            other = {k: v for k, v in normalized.items() if k != "tree"}
            want = {k: v for k, v in expected["normalized_snapshot"].items() if k != "tree"}
            if other != want:
                failures.append(f"{phase}: snapshot metadata/summary differs from stock: "
                                + json.dumps({k: (other.get(k), want.get(k)) for k in set(other) | set(want)
                                              if other.get(k) != want.get(k)})[:1500])
            row["tree_fast_path"] = latest["tree"] == expected["tree"]
            if not row["tree_fast_path"] or full_oracle:
                # slow path (different but possibly equivalent tree) and the warm-up full oracle
                failures += self.stock_check(work, f"{phase}-full", read_data=True)
                wanted = self.expected_restore[self.PHASE_EXPECTED[phase]]
                actual = self.stock_restore_manifest(work, latest["id"], f"stock-restores-{arm}-{phase}")
                if not wanted.matches(actual):
                    failures.append(f"{phase}: stock restore of the arm's snapshot differs: " + wanted.diff(actual))
        elif phase == "restore":
            actual = manifest(work / "restore" / "data")
            if not self.expected_restore["v1"].matches(actual):
                failures.append("restore: restored tree differs from stock's restore: " + self.expected_restore["v1"].diff(actual))
        elif phase == "check-read-data":
            if full_oracle:
                failures += self.corruption_detected(arm)
        elif phase == "forget-prune":
            snaps = self.stock_snapshots(work)
            ids = [s["id"] for s in snaps]
            if "forget-prune" not in ref:
                if arm != "stock":
                    raise RuntimeError("references must be established by stock first")
                ref["forget-prune"] = {"ids": ids}
            if ids != ref["forget-prune"]["ids"]:
                failures.append(f"forget-prune: kept {ids}, stock kept {ref['forget-prune']['ids']}")
            if full_oracle:
                failures += self.stock_check(work, "prune-full", read_data=True)
                if not failures:
                    actual = self.stock_restore_manifest(work, "latest", f"stock-restores-{arm}-pruned")
                    if not self.expected_restore["v3"].matches(actual):
                        failures.append("forget-prune: stock restore of the pruned repo differs: " + self.expected_restore["v3"].diff(actual))
            else:
                failures += self.stock_check(work, "prune-subset", read_data=True, subset=f"{round_index % 5 + 1}/5")
        elif phase == "ls-json":
            parsed = [json.loads(line) for line in (work / "ls-json.stdout").read_text().splitlines() if line.strip()]
            digest = canonical_sha(parsed)
            if "ls-json" not in ref:
                if arm != "stock":
                    raise RuntimeError("references must be established by stock first")
                ref["ls-json"] = {"sha256": digest, "lines": len(parsed)}
            if digest != ref["ls-json"]["sha256"]:
                failures.append(f"ls-json: {len(parsed)} parsed lines differ from stock's ({ref['ls-json']['lines']})")
        return failures

    def stock_check(self, work: Path, label: str, read_data: bool, subset: str | None = None) -> list[str]:
        args = ["check"] + ([f"--read-data-subset={subset}"] if subset else (["--read-data"] if read_data else []))
        run = self.restic("stock", args, work, None, "oracle-" + label)
        return [] if run["exit_code"] == 0 else [f"stock check {' '.join(args)} failed: {run['stdout_tail'][-600:]} {run['stderr_tail'][-600:]}"]

    def corruption_detected(self, arm: str) -> list[str]:
        work = self.fresh_work(f"corrupt-{arm}")
        remote = workload_settings()["remote"] is not None
        # a remote-backend workload: the corrupted repository is the REST server's storage, so the arm reads the bad
        # pack through the backend it is measured on (REMOTE.md); every other workload: work/repo, as before
        repo = work.with_name(work.name + ".remote-store") if remote else work / "repo"
        if remote:
            shutil.rmtree(repo, ignore_errors=True)
        copy_tree(self.state_root / "states" / "S1a", repo)
        packs = sorted(p for p in (repo / "data").rglob("*") if p.is_file() and p.stat().st_size > 4096)
        victim = packs[len(packs) // 2]
        with victim.open("r+b") as stream:
            stream.seek(victim.stat().st_size // 2)
            byte = stream.read(1)
            stream.seek(victim.stat().st_size // 2)
            stream.write(bytes([byte[0] ^ 0x5A]))
        if remote:
            try:
                run = self.restic(arm, ["check", "--read-data"], work, None, "corrupt-check", remote_store=repo)
            finally:
                shutil.rmtree(repo, ignore_errors=True)
        else:
            run = self.restic(arm, ["check", "--read-data"], work, None, "corrupt-check")
        shutil.rmtree(work)
        return [] if run["exit_code"] != 0 else [f"{arm} check --read-data did not detect a corrupted pack"]


    # ---- main flow
    def run(self) -> int:
        args = self.args
        self.evidence.mkdir(parents=True, exist_ok=True)
        if (self.evidence / "config.json").exists() and not args.resume:
            raise SystemExit(f"{self.evidence} holds a previous run; pass --resume or use a new --out")
        (ROOT / "shared").mkdir(parents=True, exist_ok=True)
        self.progress(f"waiting for a CPU lane lock ({args.cpus})")
        lock = None
        while lock is None:
            for lane in self.lanes:
                candidate = (ROOT / "shared" / f"cpus-{lane}.lock").open("a+b")
                try:
                    fcntl.flock(candidate, fcntl.LOCK_EX | (fcntl.LOCK_NB if len(self.lanes) > 1 else 0))
                except BlockingIOError:
                    candidate.close()
                    continue
                lock, self.cpus = candidate, parse_cpus(lane)
                break
            if lock is None:
                time.sleep(5)
        os.sched_setaffinity(0, set(self.cpus))
        self.progress(f"CPU lane {self.cpus[0]}-{self.cpus[-1]} acquired")
        scratch_root = Path(args.scratch or os.environ.get("RESTIC_JUDGE_SCRATCH") or "/dev/shm/restic-league-v2")
        self.scratch = scratch_root / sha_bytes(str(self.evidence).encode())[:16]
        self.state_root = self.scratch  # official path: stock-made states live in this job's scratch
        try:
            if getattr(args, "suite", None):
                return self._run_suite()  # every mode on the full path (no research cache); SUITE.md
            if self.mode in RESEARCH_MODES and FASTPATH and not args.calibration:
                return self._run_research()
            return self._run()
        except Exception as exc:  # a failed candidate is a measured failure; the controller sees status
            self.fail(str(exc))
            print(f"ERROR: {exc}", file=sys.stderr)
            return 1
        finally:
            shutil.rmtree(self.scratch, ignore_errors=True)
            fcntl.flock(lock, fcntl.LOCK_UN)

    def workload_info(self) -> dict:
        """Single workload: workload_record() exactly as before. Suite: the list, each with its own record."""
        names = getattr(self.args, "suite", None)
        if not names:
            return workload_record()
        current = WORKLOAD_NAME
        records = []
        for name in names:
            set_workload(name)
            records.append(workload_record())
        set_workload(current)
        return {"name": "suite:" + ",".join(names), "suite": names, "workloads": records}

    def fail(self, message: str) -> None:
        atomic(self.evidence / "error.json", {"error": message, "timestamp": now()})
        builds = {}
        for path in sorted((self.evidence / "builds").glob("*.json")) if (self.evidence / "builds").exists() else []:
            row = load(path)
            builds[row["arm"]] = {"ok": row.get("ok"), "exit_code": row.get("exit_code"), "stderr_tail": row.get("stderr_tail"),
                                  "policy_violations": row.get("policy", {}).get("violations")}
        compat = {}
        for path in sorted((self.evidence / "compat").glob("*.json")) if (self.evidence / "compat").exists() else []:
            row = load(path)
            compat[row["arm"]] = {k: row.get(k) for k in ("passed", "counts", "failed", "baseline_pass_not_passing", "output_tail")}
        result = {"schema": SCHEMA, "status": "failed", "mode": self.mode, "measurement_valid": False,
                  "error": message, "labels": self.labels, "started_at": self.started_at, "completed_at": now(),
                  "rss_measurement_method": RSS_MEASUREMENT_METHOD, "arms": getattr(self, "arm_sources", {}),
                  "correctness": {"all_arms_matched_parent_stock_oracle": False, "build": builds,
                                  "compatibility": compat,
                                  "candidate_compatibility_tests_passed": bool(compat.get("candidate", {}).get("passed")),
                                  "cross_reader_ok": False},
                  "comparisons": {}, "eligibility": {"eligible": False, "reasons": [message], "why": [message]},
                  "evidence": str(self.evidence), "workload": self.workload_info()}
        if getattr(self, "research_cache", None) is not None:  # research fast path only; official results unchanged
            result["research_cache"] = self.research_cache
        atomic(self.out, result)
        self.progress(message[:500], "failed")

    def _run(self) -> int:
        args = self.args
        arms_requested = {"candidate": args.candidate.resolve(), "incumbent": args.incumbent.resolve(),
                          "stock": args.stock.resolve()}
        for name, tree in arms_requested.items():
            if not (tree / "go.mod").is_file():
                raise RuntimeError(f"{name} is not a restic Go module root: {tree}")
        self.progress("snapshot arms; identify toolchain and frozen workloads")
        judge_sha = sha(Path(__file__).resolve())
        pinned_stock = load(ROOT / "stock.json")
        trusted_stock = stock_tree(ROOT / "stock")
        if trusted_stock.identity["sha256"] != pinned_stock["source_sha256"]:
            raise RuntimeError("the judge's own pinned stock tree differs from stock.json")
        trees = {name: snapshot_arm(tree, ROOT / "stock", trusted_stock.files,
                                    self.evidence / "sources" / name)
                 for name, tree in arms_requested.items()}
        sources = {name: {"path": str(arms_requested[name]), **trees[name].identity} for name in trees}
        stock_matches_pin = sources["stock"]["sha256"] == pinned_stock["source_sha256"]
        trees["stock"] = trusted_stock
        inputs = {}
        for name, tree in trees.items():
            files, policy = build_input(tree, trusted_stock)
            inputs[name] = {"sha256": canonical_sha({rel: d for rel, (_, d) in files.items()}), "policy": policy}
        self.arm_sources = {name: {"source": sources[name], "build_input_sha256": inputs[name]["sha256"]}
                            for name in arms_requested}
        toolchain = self.toolchain_identity()
        workloads = self.workload_identity()
        launcher_record = load(ROOT / "judge" / "launcher.json")
        if sha(LAUNCHER) != launcher_record["sha256"]:
            raise RuntimeError("rusage launcher differs from launcher.json")
        toolchain["rusage_launcher_sha256"] = launcher_record["sha256"]

        # skip / alias rules (pylint_judge semantics)
        equivalent = [name for name in ("incumbent", "stock") if inputs["candidate"]["sha256"] == inputs[name]["sha256"]]
        if self.mode == "match" and equivalent and not args.calibration:
            reason = ("Unchanged candidate: its build input (stock + cmd/internal non-test *.go) is identical to "
                      + "/".join(equivalent) + "; it cannot become a promotion nominee")
            result = {"schema": SCHEMA, "status": "skipped", "mode": self.mode, "measurement_valid": False,
                      "labels": self.labels, "skip": {"reason": reason, "equivalent_to": equivalent},
                      "arms": self.arm_sources, "comparisons": {}, "rss_measurement_method": RSS_MEASUREMENT_METHOD,
                      "correctness": {"all_arms_matched_parent_stock_oracle": False,
                                      "candidate_compatibility_tests_passed": None, "cross_reader_ok": None},
                      "eligibility": {"eligible": False, "reasons": [reason], "why": [reason]},
                      "started_at": self.started_at, "completed_at": now(), "workload": workload_record()}
            atomic(self.out, result)
            self.progress(reason, "complete")
            return 0
        aliases = {}
        measured = ["candidate", "incumbent", "stock"]
        if inputs["incumbent"]["sha256"] == inputs["stock"]["sha256"] and not args.calibration:
            aliases["incumbent"] = "stock"
            measured = ["candidate", "stock"]
        rounds_required = MODE_ROUNDS[self.mode]
        run_compat = self.mode in ("correctness", "match", "reference", "promotion")
        self.units_total = (len(measured) + (len(measured) if run_compat else 0) + 1
                            + len(measured) * len(PHASES) + rounds_required * len(measured) * len(PHASES) + 1)
        config = {"schema": SCHEMA, "mode": self.mode, "rounds": rounds_required, "phases": PHASES,
                  "arms": self.arm_sources, "measured_arms": measured, "aliases": aliases,
                  "calibration": bool(args.calibration), "cpus_request": args.cpus, "gomaxprocs": self.procs,
                  "toolchain": toolchain, "workloads": workloads, "judge_sha256": judge_sha,
                  "build": {"tags": BUILD_TAGS, "ldflags": BUILD_LDFLAGS, "flags": ["-trimpath", "-buildvcs=false"],
                            "env": "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOFLAGS=-mod=readonly GOPROXY=off"},
                  "stock_pin": pinned_stock, "stock_matches_pin": stock_matches_pin,
                  "rss_measurement_method": RSS_MEASUREMENT_METHOD, "labels": self.labels}
        config_path = self.evidence / "config.json"
        if config_path.exists():
            if load(config_path) != config:
                raise RuntimeError("--resume configuration differs from the saved run")
        else:
            atomic(config_path, config)
        if not stock_matches_pin:
            raise RuntimeError("the --stock tree is not the pinned stock source " + pinned_stock["commit"])
        if not is_tmpfs(self.scratch.parent if self.scratch.parent.exists() else Path("/dev/shm")):
            raise RuntimeError(f"scratch {self.scratch} is not on tmpfs")
        if self.scratch.exists():
            shutil.rmtree(self.scratch)
        self.scratch.mkdir(parents=True)
        if not is_tmpfs(self.scratch):
            raise RuntimeError(f"scratch {self.scratch} is not on tmpfs")
        self.invocation = len(list(self.evidence.glob("invocation-*.json"))) + 1
        atomic(self.evidence / f"invocation-{self.invocation:02d}.json", {"started_at": now(), "pid": os.getpid(), "cpus": self.cpus})

        # ---- builds
        self.binaries, self.binary_sha, builds = {}, {}, {}
        for name in measured:
            self.progress(f"trusted offline build: {name}")
            receipt = trusted_build(name, trees[name], trusted_stock, self.scratch, self.evidence, self.procs)
            builds[name] = receipt
            self.units_done += 1
            if not receipt["ok"]:
                raise RuntimeError(f"build of {name} failed (exit {receipt['exit_code']}): "
                                   f"{'; '.join(receipt['policy']['violations'])} {receipt['stderr_tail'][-2500:]}")
            self.binaries[name] = self.evidence / "builds" / f"{name}.restic"
            self.binary_sha[name] = receipt["binary_sha256"]
        codes = {name: builds[name].get("code_sha256") for name in measured}
        for name in aliases:
            codes[name] = codes[aliases[name]]
        same_code = [name for name in ("incumbent", "stock") if codes["candidate"] == codes[name]]
        if self.mode == "match" and same_code and not args.calibration:
            reason = ("Unchanged candidate: its binary machine code (build IDs zeroed) is identical to "
                      + "/".join(same_code) + "; it cannot become a promotion nominee")
            atomic(self.out, {"schema": SCHEMA, "status": "skipped", "mode": self.mode, "measurement_valid": False,
                              "labels": self.labels, "skip": {"reason": reason, "equivalent_to": same_code},
                              "arms": self.arm_sources, "code_sha256": codes, "comparisons": {},
                              "rss_measurement_method": RSS_MEASUREMENT_METHOD,
                              "correctness": {"all_arms_matched_parent_stock_oracle": False,
                                              "candidate_compatibility_tests_passed": None, "cross_reader_ok": None},
                              "eligibility": {"eligible": False, "reasons": [reason], "why": [reason]},
                              "started_at": self.started_at, "completed_at": now(), "workload": workload_record()})
            self.progress(reason, "complete")
            return 0
        stock_build = load(ROOT / "stock-build.json")
        if self.binary_sha["stock"] != stock_build["binary_sha256"]:
            raise RuntimeError("stock rebuild is not bit-identical to the recorded reproducible stock build")
        # ---- compatibility subset
        compat = {}
        if run_compat:
            baseline = load(ROOT / "compat-baseline.json")
            for name in [n for n in measured if n != "stock"] + ["stock"]:
                if name == "stock":
                    continue  # stock's own result is the frozen baseline
                self.progress(f"compatibility tests (stock *_test.go subset) for {name}")
                compat[name] = compatibility_tests(name, trees[name], trusted_stock, self.scratch,
                                                   self.evidence, self.procs, baseline)
                self.units_done += 1
                if not compat[name]["passed"]:
                    raise RuntimeError(f"{name} failed the compatibility subset: failed={compat[name]['failed'][:10]} "
                                       f"missing={compat[name]['baseline_pass_not_passing'][:10]}\n{compat[name]['output_tail'][-2500:]}")
            self.units_done += 1  # stock = baseline
        # ---- states + warm-up round with the FULL oracle for every arm (unmeasured)
        self.build_states()
        self.units_done += 1
        self.references = {}
        full_path = self.evidence / "full-oracle.json"
        full_saved = load(full_path) if full_path.exists() else {}
        warm = {"invocation": self.invocation, "states": self.states, "rows": []}
        order = ["stock"] + [a for a in measured if a != "stock"]
        for phase in PHASES:
            for arm in order:
                full = not full_saved.get(arm, {}).get("passed") or arm == "stock"
                self.progress(f"warm-up (unmeasured){' + full oracle' if full else ''}: {arm}/{phase}")
                row = self.run_phase(arm, phase, 0, full)
                warm["rows"].append(row)
                self.units_done += 1
                if not row["verified"]:
                    atomic(self.evidence / f"warmup-{self.invocation:02d}.json", warm)
                    raise RuntimeError(f"{arm}/{phase} failed the oracle: {row['oracle_failures']}")
        warm["references"] = self.references
        atomic(self.evidence / f"warmup-{self.invocation:02d}.json", warm)
        for arm in measured:
            if not full_saved.get(arm, {}).get("passed"):
                full_saved[arm] = {"passed": True, "invocation": self.invocation, "binary_sha256": self.binary_sha[arm]}
        atomic(full_path, full_saved)
        cross_reader_ok = all(full_saved[a]["passed"] for a in measured)

        # ---- measured rounds
        completed = []
        round_orders = orders(measured, rounds_required)
        for round_index in range(1, rounds_required + 1):
            path = self.evidence / f"round-{round_index:02d}.json"
            if path.exists():
                saved = load(path)
                if saved.get("status") == "complete" and saved.get("binary_sha256") == self.binary_sha:
                    completed.append(saved)
                    self.units_done += len(measured) * len(PHASES)
                    self.progress(f"reused checkpointed round {round_index}")
                    continue
                raise RuntimeError(f"saved round {round_index} is incomplete or from other binaries")
            order = round_orders[round_index - 1]
            rows = []
            for phase in PHASES:
                for arm in order:
                    self.progress(f"round {round_index}/{rounds_required}: {arm}/{phase}")
                    row = self.run_phase(arm, phase, round_index, False)
                    rows.append(row)
                    self.units_done += 1
                    if not row["verified"]:
                        atomic(self.evidence / f"round-{round_index:02d}.failed.json", {"rows": rows})
                        raise RuntimeError(f"round {round_index}: {arm}/{phase} failed the oracle: {row['oracle_failures']}")
            values = {}
            for arm in measured:
                phases = {r["phase"]: {"seconds": r["seconds"], "peak_rss_bytes": r["peak_rss_bytes"]} for r in rows if r["arm"] == arm}
                values[arm] = {"seconds": sum(p["seconds"] for p in phases.values()),
                               "mean_peak_rss_bytes": statistics.fmean(p["peak_rss_bytes"] for p in phases.values()),
                               "phases": phases}
            saved = {"status": "complete", "round": round_index, "order": order, "invocation": self.invocation,
                     "binary_sha256": self.binary_sha, "values": values, "rows": rows, "completed_at": now()}
            atomic(path, saved)
            completed.append(saved)
            quick = compare([saved], "candidate", "stock" if "stock" in measured else measured[-1])
            self.event(event="round_complete", round=round_index, candidate_vs_stock={k: quick[k] for k in ("score_pct", "time_saved_pct", "rss_saved_pct")})

        # ---- tamper checks (re-verified at the end)
        self.progress("tamper checks: sources, binaries, toolchain, workloads, judge")
        for name in ("candidate", "incumbent"):
            if snapshot_arm(arms_requested[name], ROOT / "stock", trusted_stock.files,
                            self.evidence / "sources" / name).identity["sha256"] != sources[name]["sha256"]:
                raise RuntimeError(f"{name} source snapshot changed during the run")
        if stock_tree(ROOT / "stock").identity["sha256"] != pinned_stock["source_sha256"]:
            raise RuntimeError("pinned stock tree changed during the run")
        for name in measured:
            if sha(self.binaries[name]) != self.binary_sha[name]:
                raise RuntimeError(f"{name} binary changed during the run")
        if {**self.toolchain_identity(), "rusage_launcher_sha256": sha(LAUNCHER)} != toolchain:
            raise RuntimeError("toolchain or rusage launcher changed during the run")
        if self.workload_identity() != workloads:
            raise RuntimeError("frozen workloads changed during the run")
        if sha(Path(__file__).resolve()) != judge_sha:
            raise RuntimeError("judge script changed during the run")
        self.units_done += 1
        return self.finish(completed, measured, aliases, builds, compat, cross_reader_ok, config)

    # ---- multi-workload suite (--workloads a,b,c; SUITE.md) ------------------------------------------------------
    def activate(self, name: str) -> None:
        """Point every helper at one workload of the suite: its frozen inputs, states, expected manifests, refs."""
        set_workload(name)
        slot = self.suite_slots[name]
        self.state_root, self.evidence_tag = slot["state_root"], slot["evidence_tag"]
        self.states = slot.get("states")
        self.expected_restore = slot.get("expected_restore")
        self.references = slot.get("references")

    def _run_suite(self) -> int:
        """Same arms, builds, compatibility subset, states, warm-up full oracle, per-round oracle and tamper checks as
        _run, once per workload; rounds rotate over the workloads (round r measures workload (r-1) mod N with that
        workload's own orders() rotation). Every mode runs this full path; the research cache is never used."""
        args = self.args
        names = list(args.suite)
        arms_requested = {"candidate": args.candidate.resolve(), "incumbent": args.incumbent.resolve(),
                          "stock": args.stock.resolve()}
        for name, tree in arms_requested.items():
            if not (tree / "go.mod").is_file():
                raise RuntimeError(f"{name} is not a restic Go module root: {tree}")
        self.progress("snapshot arms; identify toolchain and frozen workloads (suite: " + ",".join(names) + ")")
        judge_sha = sha(Path(__file__).resolve())
        pinned_stock = load(ROOT / "stock.json")
        trusted_stock = stock_tree(ROOT / "stock")
        if trusted_stock.identity["sha256"] != pinned_stock["source_sha256"]:
            raise RuntimeError("the judge's own pinned stock tree differs from stock.json")
        trees = {name: snapshot_arm(tree, ROOT / "stock", trusted_stock.files, self.evidence / "sources" / name)
                 for name, tree in arms_requested.items()}
        sources = {name: {"path": str(arms_requested[name]), **trees[name].identity} for name in trees}
        stock_matches_pin = sources["stock"]["sha256"] == pinned_stock["source_sha256"]
        trees["stock"] = trusted_stock
        inputs = {}
        for name, tree in trees.items():
            files, policy = build_input(tree, trusted_stock)
            inputs[name] = {"sha256": canonical_sha({rel: d for rel, (_, d) in files.items()}), "policy": policy}
        self.arm_sources = {name: {"source": sources[name], "build_input_sha256": inputs[name]["sha256"]}
                            for name in arms_requested}
        toolchain = self.toolchain_identity()
        workloads = {}
        for name in names:
            set_workload(name)
            workloads[name] = self.workload_identity()
        launcher_record = load(ROOT / "judge" / "launcher.json")
        if sha(LAUNCHER) != launcher_record["sha256"]:
            raise RuntimeError("rusage launcher differs from launcher.json")
        toolchain["rusage_launcher_sha256"] = launcher_record["sha256"]

        def skipped(reason: str, equivalent: list, **extra) -> int:
            atomic(self.out, {"schema": SCHEMA, "status": "skipped", "mode": self.mode, "measurement_valid": False,
                              "labels": self.labels, "skip": {"reason": reason, "equivalent_to": equivalent},
                              "arms": self.arm_sources, "comparisons": {}, "rss_measurement_method": RSS_MEASUREMENT_METHOD,
                              "correctness": {"all_arms_matched_parent_stock_oracle": False,
                                              "candidate_compatibility_tests_passed": None, "cross_reader_ok": None},
                              "eligibility": {"eligible": False, "reasons": [reason], "why": [reason]},
                              "started_at": self.started_at, "completed_at": now(), "workload": self.workload_info(),
                              **extra})
            self.progress(reason, "complete")
            return 0

        equivalent = [name for name in ("incumbent", "stock") if inputs["candidate"]["sha256"] == inputs[name]["sha256"]]
        if self.mode == "match" and equivalent and not args.calibration:
            return skipped("Unchanged candidate: its build input (stock + cmd/internal non-test *.go) is identical to "
                           + "/".join(equivalent) + "; it cannot become a promotion nominee", equivalent)
        aliases = {}
        measured = ["candidate", "incumbent", "stock"]
        if inputs["incumbent"]["sha256"] == inputs["stock"]["sha256"] and not args.calibration:
            aliases["incumbent"] = "stock"
            measured = ["candidate", "stock"]
        rounds_required = suite_rounds(MODE_ROUNDS[self.mode], len(names))
        schedule = suite_schedule(names, measured, rounds_required)
        run_compat = self.mode in ("correctness", "match", "reference", "promotion")
        self.units_total = (len(measured) + (len(measured) if run_compat else 0) + len(names)
                            + len(names) * len(measured) * len(PHASES) + rounds_required * len(measured) * len(PHASES) + 1)
        config = {"schema": SCHEMA, "mode": self.mode, "rounds": rounds_required, "phases": PHASES,
                  "arms": self.arm_sources, "measured_arms": measured, "aliases": aliases,
                  "calibration": bool(args.calibration), "cpus_request": args.cpus, "gomaxprocs": self.procs,
                  "toolchain": toolchain, "workloads": workloads, "suite": names,
                  "schedule": [{"round": r + 1, "workload": w, "order": o} for r, (w, o) in enumerate(schedule)],
                  "judge_sha256": judge_sha,
                  "build": {"tags": BUILD_TAGS, "ldflags": BUILD_LDFLAGS, "flags": ["-trimpath", "-buildvcs=false"],
                            "env": "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOFLAGS=-mod=readonly GOPROXY=off"},
                  "stock_pin": pinned_stock, "stock_matches_pin": stock_matches_pin,
                  "rss_measurement_method": RSS_MEASUREMENT_METHOD, "labels": self.labels}
        config_path = self.evidence / "config.json"
        if config_path.exists():
            if load(config_path) != config:
                raise RuntimeError("--resume configuration differs from the saved run")
        else:
            atomic(config_path, config)
        if not stock_matches_pin:
            raise RuntimeError("the --stock tree is not the pinned stock source " + pinned_stock["commit"])
        if not is_tmpfs(self.scratch.parent if self.scratch.parent.exists() else Path("/dev/shm")):
            raise RuntimeError(f"scratch {self.scratch} is not on tmpfs")
        if self.scratch.exists():
            shutil.rmtree(self.scratch)
        self.scratch.mkdir(parents=True)
        if not is_tmpfs(self.scratch):
            raise RuntimeError(f"scratch {self.scratch} is not on tmpfs")
        self.invocation = len(list(self.evidence.glob("invocation-*.json"))) + 1
        atomic(self.evidence / f"invocation-{self.invocation:02d}.json", {"started_at": now(), "pid": os.getpid(), "cpus": self.cpus})

        # ---- builds (once for the suite)
        self.binaries, self.binary_sha, builds = {}, {}, {}
        for name in measured:
            self.progress(f"trusted offline build: {name}")
            receipt = trusted_build(name, trees[name], trusted_stock, self.scratch, self.evidence, self.procs)
            builds[name] = receipt
            self.units_done += 1
            if not receipt["ok"]:
                raise RuntimeError(f"build of {name} failed (exit {receipt['exit_code']}): "
                                   f"{'; '.join(receipt['policy']['violations'])} {receipt['stderr_tail'][-2500:]}")
            self.binaries[name] = self.evidence / "builds" / f"{name}.restic"
            self.binary_sha[name] = receipt["binary_sha256"]
        codes = {name: builds[name].get("code_sha256") for name in measured}
        for name in aliases:
            codes[name] = codes[aliases[name]]
        same_code = [name for name in ("incumbent", "stock") if codes["candidate"] == codes[name]]
        if self.mode == "match" and same_code and not args.calibration:
            return skipped("Unchanged candidate: its binary machine code (build IDs zeroed) is identical to "
                           + "/".join(same_code) + "; it cannot become a promotion nominee", same_code, code_sha256=codes)
        if self.binary_sha["stock"] != load(ROOT / "stock-build.json")["binary_sha256"]:
            raise RuntimeError("stock rebuild is not bit-identical to the recorded reproducible stock build")
        # ---- compatibility subset (workload-independent; once)
        compat = {}
        if run_compat:
            baseline = load(ROOT / "compat-baseline.json")
            for name in [n for n in measured if n != "stock"]:
                self.progress(f"compatibility tests (stock *_test.go subset) for {name}")
                compat[name] = compatibility_tests(name, trees[name], trusted_stock, self.scratch, self.evidence,
                                                   self.procs, baseline)
                self.units_done += 1
                if not compat[name]["passed"]:
                    raise RuntimeError(f"{name} failed the compatibility subset: failed={compat[name]['failed'][:10]} "
                                       f"missing={compat[name]['baseline_pass_not_passing'][:10]}\n{compat[name]['output_tail'][-2500:]}")
            self.units_done += 1  # stock = baseline
        # ---- per workload: stock-made states + warm-up round with the FULL oracle for every arm (unmeasured)
        self.suite_slots = {name: {"state_root": self.scratch / f"wl-{name}", "evidence_tag": f"wl-{name}-"}
                            for name in names}
        cross_reader = {}
        for name in names:
            self.activate(name)
            self.state_root.mkdir(parents=True)
            self.progress(f"[{name}] stock builds the reference states")
            self.build_states()
            self.units_done += 1
            self.references = {}
            full_path = self.evidence / f"wl-{name}-full-oracle.json"
            full_saved = load(full_path) if full_path.exists() else {}
            warm = {"invocation": self.invocation, "workload": name, "states": self.states, "rows": []}
            for phase in PHASES:
                for arm in ["stock"] + [a for a in measured if a != "stock"]:
                    full = not full_saved.get(arm, {}).get("passed") or arm == "stock"
                    self.progress(f"[{name}] warm-up (unmeasured){' + full oracle' if full else ''}: {arm}/{phase}")
                    row = self.run_phase(arm, phase, 0, full)
                    warm["rows"].append(row)
                    self.units_done += 1
                    if not row["verified"]:
                        atomic(self.evidence / f"wl-{name}-warmup-{self.invocation:02d}.json", warm)
                        raise RuntimeError(f"[{name}] {arm}/{phase} failed the oracle: {row['oracle_failures']}")
            warm["references"] = self.references
            atomic(self.evidence / f"wl-{name}-warmup-{self.invocation:02d}.json", warm)
            for arm in measured:
                if not full_saved.get(arm, {}).get("passed"):
                    full_saved[arm] = {"passed": True, "invocation": self.invocation, "binary_sha256": self.binary_sha[arm]}
            atomic(full_path, full_saved)
            cross_reader[name] = all(full_saved[a]["passed"] for a in measured)
            self.suite_slots[name].update(states=self.states, expected_restore=self.expected_restore,
                                          references=self.references)

        # ---- measured rounds (rotating workloads)
        completed = {name: [] for name in names}
        for round_index, (name, order) in enumerate(schedule, 1):
            path = self.evidence / f"round-{round_index:02d}.json"
            if path.exists():
                saved = load(path)
                if (saved.get("status") == "complete" and saved.get("binary_sha256") == self.binary_sha
                        and saved.get("workload") == name):
                    completed[name].append(saved)
                    self.units_done += len(measured) * len(PHASES)
                    self.progress(f"reused checkpointed round {round_index} ({name})")
                    continue
                raise RuntimeError(f"saved round {round_index} is incomplete, from other binaries or another workload")
            self.activate(name)
            rows = []
            for phase in PHASES:
                for arm in order:
                    self.progress(f"round {round_index}/{rounds_required} [{name}]: {arm}/{phase}")
                    row = self.run_phase(arm, phase, round_index, False)
                    rows.append(row)
                    self.units_done += 1
                    if not row["verified"]:
                        atomic(self.evidence / f"round-{round_index:02d}.failed.json", {"workload": name, "rows": rows})
                        raise RuntimeError(f"round {round_index} [{name}]: {arm}/{phase} failed the oracle: {row['oracle_failures']}")
            values = {}
            for arm in measured:
                phases = {r["phase"]: {"seconds": r["seconds"], "peak_rss_bytes": r["peak_rss_bytes"]} for r in rows if r["arm"] == arm}
                values[arm] = {"seconds": sum(p["seconds"] for p in phases.values()),
                               "mean_peak_rss_bytes": statistics.fmean(p["peak_rss_bytes"] for p in phases.values()),
                               "phases": phases}
            saved = {"status": "complete", "round": round_index, "workload": name, "order": order,
                     "invocation": self.invocation, "binary_sha256": self.binary_sha, "values": values, "rows": rows,
                     "completed_at": now()}
            atomic(path, saved)
            completed[name].append(saved)
            quick = compare([saved], "candidate", "stock" if "stock" in measured else measured[-1])
            self.event(event="round_complete", round=round_index, workload=name,
                       candidate_vs_stock={k: quick[k] for k in ("score_pct", "time_saved_pct", "rss_saved_pct")})

        # ---- tamper checks
        self.progress("tamper checks: sources, binaries, toolchain, workloads, judge")
        for name in ("candidate", "incumbent"):
            if snapshot_arm(arms_requested[name], ROOT / "stock", trusted_stock.files,
                            self.evidence / "sources" / name).identity["sha256"] != sources[name]["sha256"]:
                raise RuntimeError(f"{name} source snapshot changed during the run")
        if stock_tree(ROOT / "stock").identity["sha256"] != pinned_stock["source_sha256"]:
            raise RuntimeError("pinned stock tree changed during the run")
        for name in measured:
            if sha(self.binaries[name]) != self.binary_sha[name]:
                raise RuntimeError(f"{name} binary changed during the run")
        if {**self.toolchain_identity(), "rusage_launcher_sha256": sha(LAUNCHER)} != toolchain:
            raise RuntimeError("toolchain or rusage launcher changed during the run")
        for name in names:
            set_workload(name)
            if self.workload_identity() != workloads[name]:
                raise RuntimeError(f"frozen workload {name} changed during the run")
        if sha(Path(__file__).resolve()) != judge_sha:
            raise RuntimeError("judge script changed during the run")
        self.units_done += 1
        return self.finish_suite(completed, measured, aliases, builds, compat, cross_reader, config)

    def finish_suite(self, completed, measured, aliases, builds, compat, cross_reader, config) -> int:
        """Result in the single-workload shape (every key the controller reads), aggregates across the suite, plus
        per-workload detail: arms.<arm>.per_workload, comparisons.<C>.per_workload, states/references per workload."""
        names = list(self.args.suite)
        resolve = lambda name: aliases.get(name, name)  # noqa: E731
        done = {name: rows for name, rows in completed.items() if rows}
        arms = {}
        for name in ("candidate", "incumbent", "stock"):
            measured_as = resolve(name)
            row = {**self.arm_sources[name], "measured_as": measured_as,
                   "binary_sha256": self.binary_sha[measured_as], "code_sha256": builds[measured_as].get("code_sha256"),
                   "build_seconds": builds[measured_as]["seconds"],
                   "policy": builds[measured_as]["policy"] if name != "stock" else None}
            if done:
                per_workload = {}
                for wl, rounds in done.items():
                    seconds = [r["values"][measured_as]["seconds"] for r in rounds]
                    rss = [r["values"][measured_as]["mean_peak_rss_bytes"] for r in rounds]
                    per_workload[wl] = {
                        "median_total_seconds": statistics.median(seconds), "median_peak_rss_bytes": statistics.median(rss),
                        "round_seconds": seconds, "round_mean_peak_rss_bytes": rss,
                        "per_phase": {phase: {
                            "median_seconds": statistics.median(r["values"][measured_as]["phases"][phase]["seconds"] for r in rounds),
                            "median_peak_rss_bytes": statistics.median(r["values"][measured_as]["phases"][phase]["peak_rss_bytes"] for r in rounds),
                            "round_seconds": [r["values"][measured_as]["phases"][phase]["seconds"] for r in rounds],
                            "round_peak_rss_bytes": [r["values"][measured_as]["phases"][phase]["peak_rss_bytes"] for r in rounds]}
                            for phase in PHASES}}
                cells = list(per_workload.values())
                # suite totals: seconds summed over workloads (one pass of every workload), memory averaged
                row.update(median_total_seconds=sum(c["median_total_seconds"] for c in cells),
                           median_peak_rss_bytes=statistics.fmean(c["median_peak_rss_bytes"] for c in cells),
                           per_phase={phase: {
                               "median_seconds": sum(c["per_phase"][phase]["median_seconds"] for c in cells),
                               "median_peak_rss_bytes": statistics.fmean(c["per_phase"][phase]["median_peak_rss_bytes"] for c in cells)}
                               for phase in PHASES},
                           per_workload=per_workload,
                           suite_totals="median_total_seconds and per_phase.median_seconds = sum over workloads of the "
                                        "workload medians; peak RSS = mean over workloads")
            arms[name] = row
        comparisons = {}
        if done:
            comparisons["candidate_vs_incumbent"] = compare_suite(done, "candidate", resolve("incumbent"))
            comparisons["candidate_vs_stock"] = compare_suite(done, "candidate", "stock")
            if self.args.calibration:
                comparisons["incumbent_vs_stock"] = compare_suite(done, "incumbent", "stock")
        rounds_done = sum(len(rows) for rows in completed.values())
        reasons = []
        if self.mode != "promotion":
            reasons.append(f"{self.mode} evidence cannot promote; promotion requires {config['rounds']} fresh rounds")
        else:
            inc, stk = comparisons["candidate_vs_incumbent"], comparisons["candidate_vs_stock"]
            if rounds_done != config["rounds"]:
                reasons.append(f"promotion requires {config['rounds']} rounds; got {rounds_done}")
            if self.arm_sources["candidate"]["build_input_sha256"] in (self.arm_sources["incumbent"]["build_input_sha256"],
                                                                      self.arm_sources["stock"]["build_input_sha256"]):
                reasons.append("candidate build input is identical to the incumbent or stock")
            if builds["candidate"]["code_sha256"] in (builds[resolve("incumbent")]["code_sha256"], builds["stock"]["code_sha256"]):
                reasons.append("candidate binary machine code is identical to the incumbent or stock")
            if inc["score_wins"] < PROMOTION_WINS:
                reasons.append(f"candidate won {inc['score_wins']}/{rounds_done} score pairs; {PROMOTION_WINS} required")
            if inc["score_pct"] < MINIMUM_SCORE_PCT:
                reasons.append(f"suite score {inc['score_pct']:.4f}% is below {MINIMUM_SCORE_PCT}%")
            for axis in ("time_saved_pct", "rss_saved_pct"):
                for label, comparison in (("incumbent", inc), ("stock", stk)):
                    if comparison[axis] < -AXIS_REGRESSION_CAP_PCT:
                        reasons.append(f"{axis} versus {label} is {comparison[axis]:.3f}%, beyond the -{AXIS_REGRESSION_CAP_PCT}% axis cap")
            if stk["score_pct"] <= 0:
                reasons.append(f"candidate does not beat stock on score ({stk['score_pct']:.4f}%)")
            if inc["rss_saved_pct"] < -RSS_GUARD_PCT:
                reasons.append(f"peak memory versus incumbent is {inc['rss_saved_pct']:.3f}%, beyond the -{RSS_GUARD_PCT}% memory guard")
            reasons += suite_gate_reasons(inc, WORKLOAD_REGRESSION_PCT, RSS_GUARD_PCT)
        if self.args.calibration:
            reasons.append("calibration run (identical-arm null or sensitivity check); never promotable")
        eligible = self.mode == "promotion" and not reasons
        slots = getattr(self, "suite_slots", {})
        result = {
            "schema": SCHEMA, "status": "complete", "mode": self.mode,
            "measurement_valid": bool(done) or self.mode == "correctness",
            "rounds": rounds_done, "labels": self.labels, "calibration": bool(self.args.calibration),
            "started_at": self.started_at, "completed_at": now(), "config_sha256": canonical_sha(config),
            "evidence": str(self.evidence), "arms": arms, "aliases": aliases, "phases": PHASES,
            "suite": {"workloads": names, "schedule": config["schedule"],
                      "rounds_per_workload": {name: len(rows) for name, rows in completed.items()},
                      "score": "mean over workloads of each workload's median paired score (time-weighted as "
                               "score_pct); wins = rounds whose own-workload score > 0"},
            "correctness": {
                "all_arms_matched_parent_stock_oracle": True, "verified_units": self.units_done, "total_units": self.units_total,
                "candidate_compatibility_tests_passed": (compat["candidate"]["passed"] if "candidate" in compat else None),
                "compatibility": {k: {x: v.get(x) for x in ("passed", "counts", "failed", "baseline_pass_not_passing", "runs")}
                                  for k, v in compat.items()},
                "cross_reader_ok": all(cross_reader.values()), "cross_reader_by_workload": cross_reader,
                "build": {k: {"ok": v["ok"], "exit_code": v["exit_code"], "stderr_tail": v["stderr_tail"][-1500:],
                              "binary_sha256": v.get("binary_sha256")} for k, v in builds.items()},
                "oracle": "per workload, as a single-workload job: per round stock-read snapshot equality, restore "
                          "manifest == stock restore, rotating 1/5 check --read-data-subset of the pruned repo, ls --json "
                          "parsed equality; warm-up full oracle of every arm on every workload",
                "stock_restore_vs_source_differences": {name: (slot.get("states") or {}).get("stock_restore_vs_source_differences")
                                                        for name, slot in slots.items()},
            },
            "comparisons": comparisons,
            "eligibility": {"eligible": eligible, "reasons": reasons, "why": reasons, "policy": {
                "rounds": config["rounds"], "score_wins": PROMOTION_WINS, "minimum_score_pct": MINIMUM_SCORE_PCT,
                "axis_regression_cap_pct": AXIS_REGRESSION_CAP_PCT, "must_beat_stock_on_score": True,
                "score_time_weight": TIME_WEIGHT, "rss_guard_vs_incumbent_pct": RSS_GUARD_PCT,
                "rss_guard_applies_per_workload": True, "workload_regression_pct": WORKLOAD_REGRESSION_PCT,
                "match_rounds": suite_rounds(MATCH_ROUNDS, len(names)),
                "reference_rounds": suite_rounds(REFERENCE_ROUNDS, len(names))}},
            "rss_measurement_method": RSS_MEASUREMENT_METHOD,
            "measurement": {"time": "parent-side time.monotonic() around the sandboxed process (includes ~8 ms bwrap setup, identical for all arms)",
                            "rss": "wait4() ru_maxrss of the bwrap monitor whose only child is restic (namespace PID 1): "
                                   "max(monitor ~11 MiB, restic VmHWM at exit)",
                            "T": "sum of phase wall seconds (per workload round)", "M": "mean over phases of phase peak RSS",
                            "cpus": self.cpus, "gomaxprocs": self.procs, "gc_env": "GOGC/GOMEMLIMIT unset (restic defaults)",
                            "storage": "tmpfs for corpus, repositories, restore targets and restic temp/cache"},
            "states": {name: slot.get("states") for name, slot in slots.items()},
            "references": {name: slot.get("references") for name, slot in slots.items()},
            "workload": self.workload_info(),
        }
        backend = {name: remote_summary(rows, measured) for name, rows in done.items()}
        if any(backend.values()):  # suites with a remote-backend workload only (REMOTE.md)
            result["remote_backend"] = {name: {"arms": arms_summary} for name, arms_summary in backend.items() if arms_summary}
        atomic(self.out, result)
        self.progress("complete: all arms matched the stock oracle on every workload", "complete")
        print(json.dumps({k: {x: v.get(x) for x in ("score_pct", "time_saved_pct", "rss_saved_pct", "score_wins", "interval90")}
                          for k, v in comparisons.items()}, sort_keys=True))
        return 0

    # ---- research fast path (probe/correctness only; FASTPATH.md) ----------------------------------------------
    def cache_key(self, kind: str, **inputs) -> str:
        """Every cache key carries the judge file's own sha256 and the fast-path version: any judge edit is a miss."""
        return canonical_sha({"kind": kind, **self.cache_base, **inputs})

    def cached_build(self, name: str, arm_tree: ArmTree, stock: ArmTree, toolchain: dict) -> tuple[dict, str]:
        """trusted_build, reused from a content-addressed cache keyed by the exact build input, toolchain identity,
        build command and judge. The binary is copied into this job's evidence and re-hashed."""
        files, policy = build_input(arm_tree, stock)
        identity = canonical_sha({rel: digest for rel, (_, digest) in files.items()})
        receipt_path = self.evidence / "builds" / f"{name}.json"
        binary_path = self.evidence / "builds" / f"{name}.restic"
        if receipt_path.exists() and binary_path.exists():  # --resume: this job's own receipt
            saved = load(receipt_path)
            if saved.get("build_input_sha256") == identity and saved.get("binary_sha256") == sha(binary_path) and saved.get("ok"):
                return saved, "resume"
        key = self.cache_key("build", build_input_sha256=identity, toolchain=toolchain, argv=build_template(self.procs))
        entry = CACHE_ROOT / "builds" / key
        try:
            saved = load(entry / "receipt.json")
            usable = (saved.get("build_input_sha256") == identity and saved.get("exit_code") == 0
                      and bool(saved.get("binary_sha256")) and sha(entry / "restic") == saved["binary_sha256"])
        except (OSError, ValueError):
            usable = False
        if usable:
            (self.evidence / "builds").mkdir(parents=True, exist_ok=True)
            temporary = binary_path.with_name(binary_path.name + ".tmp")
            shutil.copyfile(entry / "restic", temporary)
            os.chmod(temporary, 0o555)
            os.replace(temporary, binary_path)
            if sha(binary_path) != saved["binary_sha256"]:
                raise RuntimeError(f"cached build of {name} changed while copying")
            receipt = {**saved, "arm": name, "policy": policy, "ok": not policy["violations"],
                       "research_cache": {"hit": True, "key": key, "built_by": saved.get("built_by")}}
            atomic(receipt_path, receipt)
            return receipt, "hit"
        receipt = trusted_build(name, arm_tree, stock, self.scratch, self.evidence, self.procs)
        if receipt.get("exit_code") == 0 and receipt.get("binary_sha256") and binary_path.exists() and not entry.exists():
            temporary = entry.with_name(f"{entry.name}.partial-{os.getpid()}")
            shutil.rmtree(temporary, ignore_errors=True)
            temporary.mkdir(parents=True)
            shutil.copyfile(binary_path, temporary / "restic")
            os.chmod(temporary / "restic", 0o555)
            cache_write(temporary / "receipt.json", {**receipt, "built_by": str(self.evidence), "cached_at": now()})
            try:
                os.replace(temporary, entry)  # atomic publish; a concurrent identical publish may win instead
            except OSError:
                shutil.rmtree(temporary, ignore_errors=True)
        return receipt, "miss"

    def cached_compat(self, name: str, arm_tree: ArmTree, stock: ArmTree, toolchain: dict, baseline: dict) -> tuple[dict, str]:
        """compatibility_tests; a PASSING receipt is reused for known-good (non-candidate) arms only, keyed by the exact
        build input, toolchain, compat baseline, package list, test command and judge. The candidate always runs."""
        files, _ = build_input(arm_tree, stock)
        identity = canonical_sha({rel: digest for rel, (_, digest) in files.items()})
        receipt_path = self.evidence / "compat" / f"{name}.json"
        if receipt_path.exists():  # --resume
            saved = load(receipt_path)
            if saved.get("build_input_sha256") == identity and saved.get("status") == "complete":
                return saved, "resume"
        key = self.cache_key("compat", build_input_sha256=identity, toolchain=toolchain,
                             baseline_sha256=sha(ROOT / "compat-baseline.json"), packages=COMPAT_PACKAGES,
                             cmd_run=COMPAT_CMD_RUN, argv=compat_template(self.procs))
        path = CACHE_ROOT / "compat" / f"{key}.json"
        if name != "candidate" and path.is_file():
            try:
                saved = load(path)
            except (OSError, ValueError):
                saved = {}
            if saved.get("build_input_sha256") == identity and saved.get("status") == "complete" and saved.get("passed") is True:
                receipt = {**saved, "arm": name, "research_cache": {"hit": True, "key": key, "tested_by": saved.get("tested_by")}}
                atomic(receipt_path, receipt)
                return receipt, "hit"
        receipt = compatibility_tests(name, arm_tree, stock, self.scratch, self.evidence, self.procs, baseline)
        if receipt.get("passed") is True:
            cache_write(path, {**receipt, "tested_by": str(self.evidence), "cached_at": now()})
        return receipt, ("fresh" if name == "candidate" else "miss")

    def oracle_cache_path(self, binary_sha256: str) -> Path:
        return CACHE_ROOT / "oracle" / f"{self.cache_key('full-oracle', binary_sha256=binary_sha256, bundle_key=self.bundle_key)}.json"

    def validate_bundle(self, bdir: Path, key: str) -> dict | None:
        meta_path = bdir / "bundle.json"
        if not meta_path.is_file():
            return None
        try:
            meta = load(meta_path)
            if meta.get("key") != key or meta.get("complete") is not True:
                return None
            for version, row in meta["expected_restore"].items():
                if sha(bdir / f"expected-restore-{version}.json") != row["file_sha256"]:
                    return None
            if bundle_signature(bdir) != meta["signature"]:
                return None
        except (OSError, ValueError, KeyError):
            return None
        return meta

    def build_bundle(self, bdir: Path, key: str, key_inputs: dict) -> dict:
        """Exactly the full path's stock work, once per identity: build_states (corpus A/B, S1a, Shist, expected
        restore manifests) into a tmpfs bundle, then stock's warm-up round with the full oracle, which establishes
        the references every arm is compared against."""
        if bdir.exists():
            shutil.rmtree(bdir)
        bdir.mkdir(parents=True)
        try:
            job_scratch = self.scratch
            self.state_root = bdir
            self.scratch = bdir  # build_states renames repositories out of its work dirs: same filesystem as states/
            try:
                self.build_states()
            finally:
                self.scratch = job_scratch
            shutil.rmtree(bdir / "work", ignore_errors=True)
            expected = {}
            for version, wanted in list(self.expected_restore.items()):
                target = bdir / f"expected-restore-{version}.json"
                shutil.copyfile(wanted.path, target)
                expected[version] = {"sha256": wanted.sha256, "file_sha256": sha(target)}
                self.expected_restore[version] = ExpectedManifest(wanted.sha256, target)
            self.references = {}
            rows = []
            for phase in PHASES:
                self.progress(f"research cache: stock warm-up + full oracle establishes the references: stock/{phase}")
                row = self.run_phase("stock", phase, 0, True)
                rows.append(row)
                self.units_done += 1
                if not row["verified"]:
                    raise RuntimeError(f"stock/{phase} failed the oracle while building the reference states: {row['oracle_failures']}")
            meta = {"schema": "restic-research-bundle-v1", "key": key, "key_inputs": key_inputs, "states": self.states,
                    "references": self.references, "expected_restore": expected, "stock_warmup": rows,
                    "signature": bundle_signature(bdir), "created_at": now(), "created_by": str(self.evidence),
                    "complete": True}
            cache_write(CACHE_ROOT / "bundles" / f"{key}.json", meta)  # durable record (the bundle is on tmpfs)
            cache_write(bdir / "bundle.json", meta)  # written last: its presence marks the bundle complete
            return meta
        except BaseException:
            shutil.rmtree(bdir, ignore_errors=True)
            raise

    def bundle_identity(self, workloads: dict) -> tuple[str, dict]:
        """Every input the stock-made states and references depend on: the stock binary, the frozen workloads, the
        sandbox, the phase definitions, the lane width and (via cache_key) the judge itself."""
        key_inputs = {"stock_binary_sha256": self.binary_sha["stock"], "workloads": workloads,
                      "sandbox": restic_sandbox(Path("/BINARY"), Path("/WORK"), Path("/DATA"), self.procs),
                      "launcher_sha256": sha(LAUNCHER), "phases": PHASES,
                      "phase_args": {phase: self.phase_args(phase) for phase in self.PHASE_ARGS},
                      "phase_state": self.PHASE_STATE, "phase_expected": self.PHASE_EXPECTED}
        return self.cache_key("bundle", **key_inputs), key_inputs

    def fresh_states(self) -> None:
        """Probe: this job's own stock-made states (a fresh S1a/Shist realization, exactly as _run builds them) and
        stock's references from a warm-up round of stock. Stock's full oracle is not repeated: the shared bundle
        recorded it for this exact stock binary and identity (ensure_bundle ran first)."""
        self.state_root = self.scratch
        self.build_states()
        self.references = {}
        self.units_total += len(PHASES)
        for phase in PHASES:
            self.progress(f"warm-up (unmeasured), establishes the references: stock/{phase}")
            row = self.run_phase("stock", phase, 0, False)
            self.units_done += 1
            if not row["verified"]:
                raise RuntimeError(f"stock/{phase} failed the oracle: {row['oracle_failures']}")

    def ensure_bundle(self, workloads: dict) -> tuple[dict, bool]:
        """Reference states shared by research jobs: a shared flock while in use, an exclusive one to build."""
        key, key_inputs = self.bundle_identity(workloads)
        name = "bundle-" + key[:40]
        broot = bundle_root()
        bdir = broot / name
        broot.mkdir(parents=True, exist_ok=True)
        if not is_tmpfs(broot):
            raise RuntimeError(f"research bundle root {broot} is not on tmpfs")
        (CACHE_ROOT / "locks").mkdir(parents=True, exist_ok=True)
        lock = (CACHE_ROOT / "locks" / f"{name}.lock").open("a+b")
        self.bundle_lock = lock
        self.progress("research cache: reference states lookup")
        fcntl.flock(lock, fcntl.LOCK_SH)
        meta = self.validate_bundle(bdir, key)
        built = False
        if meta is None:
            fcntl.flock(lock, fcntl.LOCK_EX)
            meta = self.validate_bundle(bdir, key)
            if meta is None:
                self.units_total += len(PHASES)
                self.progress("research cache miss: stock builds the reference states and references (once per identity)")
                meta = self.build_bundle(bdir, key, key_inputs)
                built = True
            fcntl.flock(lock, fcntl.LOCK_SH)
            if built:
                for other in broot.glob("bundle-*"):  # drop bundles of other identities nobody is using
                    if other.name == name:
                        continue
                    with (CACHE_ROOT / "locks" / f"{other.name}.lock").open("a+b") as other_lock:
                        try:
                            fcntl.flock(other_lock, fcntl.LOCK_EX | fcntl.LOCK_NB)
                        except BlockingIOError:
                            continue
                        shutil.rmtree(other, ignore_errors=True)
        self.bundle_key = key
        self.bundle_dir = bdir
        self.state_root = bdir
        self.states = meta["states"]
        self.expected_restore = {version: ExpectedManifest(row["sha256"], bdir / f"expected-restore-{version}.json")
                                 for version, row in meta["expected_restore"].items()}
        self.references = json.loads(json.dumps(meta["references"]))
        return meta, built

    def _run_research(self) -> int:
        try:
            return self._run_research_inner()
        finally:
            lock = getattr(self, "bundle_lock", None)
            if lock is not None:
                fcntl.flock(lock, fcntl.LOCK_UN)
                lock.close()

    def _run_research_inner(self) -> int:
        """probe/correctness through the research cache. Identical inputs, trusted build rules, oracle and result
        schema as _run (plus result["research_cache"]); what is reused is only ever reused on an exact identity match,
        and every miss falls back to exactly the work _run does."""
        args = self.args
        arms_requested = {"candidate": args.candidate.resolve(), "incumbent": args.incumbent.resolve(),
                          "stock": args.stock.resolve()}
        for name, tree in arms_requested.items():
            if not (tree / "go.mod").is_file():
                raise RuntimeError(f"{name} is not a restic Go module root: {tree}")
        self.progress("snapshot arms; identify toolchain and frozen workloads")
        judge_sha = sha(Path(__file__).resolve())
        self.cache_base = {"judge_sha256": judge_sha, "fastpath_version": FASTPATH_VERSION,
                           "workload": {"name": WORKLOAD_NAME, "root": str(WORKLOADS)}}
        pinned_stock = load(ROOT / "stock.json")
        trusted_stock = stock_tree(ROOT / "stock")
        if trusted_stock.identity["sha256"] != pinned_stock["source_sha256"]:
            raise RuntimeError("the judge's own pinned stock tree differs from stock.json")
        trees = {name: snapshot_arm(tree, ROOT / "stock", trusted_stock.files,
                                    self.evidence / "sources" / name)
                 for name, tree in arms_requested.items()}
        sources = {name: {"path": str(arms_requested[name]), **trees[name].identity} for name in trees}
        stock_matches_pin = sources["stock"]["sha256"] == pinned_stock["source_sha256"]
        trees["stock"] = trusted_stock
        inputs = {}
        for name, tree in trees.items():
            files, policy = build_input(tree, trusted_stock)
            inputs[name] = {"sha256": canonical_sha({rel: d for rel, (_, d) in files.items()}), "policy": policy}
        self.arm_sources = {name: {"source": sources[name], "build_input_sha256": inputs[name]["sha256"]}
                            for name in arms_requested}
        toolchain = self.toolchain_identity()
        workloads = self.workload_identity()
        launcher_record = load(ROOT / "judge" / "launcher.json")
        if sha(LAUNCHER) != launcher_record["sha256"]:
            raise RuntimeError("rusage launcher differs from launcher.json")
        toolchain["rusage_launcher_sha256"] = launcher_record["sha256"]
        aliases = {}
        measured = ["candidate", "incumbent", "stock"]
        if inputs["incumbent"]["sha256"] == inputs["stock"]["sha256"]:
            aliases["incumbent"] = "stock"
            measured = ["candidate", "stock"]
        rounds_required = MODE_ROUNDS[self.mode]
        run_compat = self.mode == "correctness"
        compat_arms = [n for n in measured if n != "stock"] if run_compat else []
        self.units_total = (len(measured) + (len(compat_arms) + 1 if run_compat else 0) + 1
                            + len(PHASES) + rounds_required * len(measured) * len(PHASES) + 1)
        config = {"schema": SCHEMA, "mode": self.mode, "rounds": rounds_required, "phases": PHASES,
                  "arms": self.arm_sources, "measured_arms": measured, "aliases": aliases,
                  "calibration": bool(args.calibration), "cpus_request": args.cpus, "gomaxprocs": self.procs,
                  "toolchain": toolchain, "workloads": workloads, "judge_sha256": judge_sha,
                  "build": {"tags": BUILD_TAGS, "ldflags": BUILD_LDFLAGS, "flags": ["-trimpath", "-buildvcs=false"],
                            "env": "CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOAMD64=v1 GOFLAGS=-mod=readonly GOPROXY=off"},
                  "stock_pin": pinned_stock, "stock_matches_pin": stock_matches_pin,
                  "rss_measurement_method": RSS_MEASUREMENT_METHOD, "labels": self.labels,
                  "research_fastpath": {"version": FASTPATH_VERSION, "cache_root": str(CACHE_ROOT),
                                        "bundle_root": str(bundle_root()), "probe_states": PROBE_STATES}}
        config_path = self.evidence / "config.json"
        if config_path.exists():
            if load(config_path) != config:
                raise RuntimeError("--resume configuration differs from the saved run")
        else:
            atomic(config_path, config)
        if not stock_matches_pin:
            raise RuntimeError("the --stock tree is not the pinned stock source " + pinned_stock["commit"])
        if not is_tmpfs(self.scratch.parent if self.scratch.parent.exists() else Path("/dev/shm")):
            raise RuntimeError(f"scratch {self.scratch} is not on tmpfs")
        if self.scratch.exists():
            shutil.rmtree(self.scratch)
        self.scratch.mkdir(parents=True)
        if not is_tmpfs(self.scratch):
            raise RuntimeError(f"scratch {self.scratch} is not on tmpfs")
        self.invocation = len(list(self.evidence.glob("invocation-*.json"))) + 1
        atomic(self.evidence / f"invocation-{self.invocation:02d}.json", {"started_at": now(), "pid": os.getpid(), "cpus": self.cpus})
        cache = self.research_cache = {
            "version": FASTPATH_VERSION, "cache_root": str(CACHE_ROOT), "builds": {}, "compatibility": {},
            "states": None, "full_oracle": {}, "warmup_arms": [],
            "note": ("research fast path: stock/incumbent builds, the incumbent's passing compat receipt, the stock-made "
                     "reference states + stock references, and known-good arms' full-oracle passes are reused on exact "
                     "identity matches; the candidate's compat tests (correctness) and full oracle always run")}

        # ---- builds (content-addressed cache; the stock binary must still equal the reproducible stock build)
        self.binaries, self.binary_sha, builds = {}, {}, {}
        for name in measured:
            self.progress(f"trusted offline build: {name}")
            receipt, how = self.cached_build(name, trees[name], trusted_stock, toolchain)
            cache["builds"][name] = how
            builds[name] = receipt
            self.units_done += 1
            if not receipt["ok"]:
                raise RuntimeError(f"build of {name} failed (exit {receipt['exit_code']}): "
                                   f"{'; '.join(receipt['policy']['violations'])} {receipt['stderr_tail'][-2500:]}")
            self.binaries[name] = self.evidence / "builds" / f"{name}.restic"
            self.binary_sha[name] = receipt["binary_sha256"]
        stock_build = load(ROOT / "stock-build.json")
        if self.binary_sha["stock"] != stock_build["binary_sha256"]:
            raise RuntimeError("stock rebuild is not bit-identical to the recorded reproducible stock build")
        # ---- compatibility subset (correctness only, as in _run); the candidate always runs, the incumbent's pass is reused
        compat = {}
        if run_compat:
            baseline = load(ROOT / "compat-baseline.json")
            for name in compat_arms:
                self.progress(f"compatibility tests (stock *_test.go subset) for {name}")
                compat[name], how = self.cached_compat(name, trees[name], trusted_stock, toolchain, baseline)
                cache["compatibility"][name] = how
                self.units_done += 1
                if not compat[name]["passed"]:
                    raise RuntimeError(f"{name} failed the compatibility subset: failed={compat[name]['failed'][:10]} "
                                       f"missing={compat[name]['baseline_pass_not_passing'][:10]}\n{compat[name]['output_tail'][-2500:]}")
            self.units_done += 1  # stock = baseline
        # ---- states + stock references from the bundle (built here on a miss)
        meta, built = self.ensure_bundle(workloads)
        cache["states"] = {"hit": not built, "key": self.bundle_key, "bundle": str(self.state_root),
                           "created_at": meta["created_at"], "created_by": meta["created_by"],
                           "signature_sha256": meta["signature"]["sha256"], "entries": meta["signature"]["entries"],
                           "references": "stock warm-up + full oracle recorded when the bundle was built"}
        self.units_done += 1
        if self.mode == "probe" and PROBE_STATES == "fresh":
            # timing: every probe draws its own S1a/Shist realization (pack layout), as the full path does
            self.fresh_states()
            cache["states"].update(probe_states="fresh per job (built by stock in this job's scratch)",
                                   references="stock warm-up (per-phase oracle) on this job's states; stock's full "
                                              "oracle recorded with the shared bundle")
        # ---- warm-up with the FULL oracle: the candidate always; a known-good arm only without a cached pass
        full_path = self.evidence / "full-oracle.json"
        full_saved = load(full_path) if full_path.exists() else {}
        warm = {"invocation": self.invocation, "states": self.states, "rows": [], "research_cache": True}
        need = ["candidate"]
        cache["full_oracle"]["candidate"] = "fresh"
        cache["full_oracle"]["stock"] = "bundle" if not built else "fresh (bundle built by this job)"
        for arm in measured:
            if arm in ("candidate", "stock"):
                continue
            saved_pass = self.oracle_cache_path(self.binary_sha[arm])
            if saved_pass.is_file() and load(saved_pass).get("passed") is True:
                cache["full_oracle"][arm] = "hit"
            else:
                cache["full_oracle"][arm] = "miss"
                need.append(arm)
        cache["warmup_arms"] = need
        self.units_total += (len(need) - 1) * len(PHASES)
        for phase in PHASES:
            for arm in need:
                full = not full_saved.get(arm, {}).get("passed")
                self.progress(f"warm-up (unmeasured){' + full oracle' if full else ''}: {arm}/{phase}")
                row = self.run_phase(arm, phase, 0, full)
                warm["rows"].append(row)
                self.units_done += 1
                if not row["verified"]:
                    atomic(self.evidence / f"warmup-{self.invocation:02d}.json", warm)
                    raise RuntimeError(f"{arm}/{phase} failed the oracle: {row['oracle_failures']}")
        warm["references"] = self.references
        atomic(self.evidence / f"warmup-{self.invocation:02d}.json", warm)
        for arm in measured:
            if not full_saved.get(arm, {}).get("passed"):
                full_saved[arm] = {"passed": True, "invocation": self.invocation, "binary_sha256": self.binary_sha[arm],
                                   **({} if arm in need else {"research_cache": cache["full_oracle"][arm]})}
        atomic(full_path, full_saved)
        for arm in need:  # this binary passed the full oracle on these states: reusable once it is the incumbent
            cache_write(self.oracle_cache_path(self.binary_sha[arm]),
                        {"passed": True, "arm": arm, "binary_sha256": self.binary_sha[arm], "bundle_key": self.bundle_key,
                         "verified_by": str(self.evidence), "verified_at": now()})
        cross_reader_ok = all(full_saved[a]["passed"] for a in measured)

        # ---- measured rounds (probe: one round of every measured arm, exactly as _run)
        for name in measured:  # arms whose warm-up was skipped must not start round 1 with a cold binary page cache
            self.binaries[name].read_bytes()
        completed = []
        round_orders = orders(measured, rounds_required)
        for round_index in range(1, rounds_required + 1):
            path = self.evidence / f"round-{round_index:02d}.json"
            if path.exists():
                saved = load(path)
                if saved.get("status") == "complete" and saved.get("binary_sha256") == self.binary_sha:
                    completed.append(saved)
                    self.units_done += len(measured) * len(PHASES)
                    self.progress(f"reused checkpointed round {round_index}")
                    continue
                raise RuntimeError(f"saved round {round_index} is incomplete or from other binaries")
            order = round_orders[round_index - 1]
            rows = []
            for phase in PHASES:
                for arm in order:
                    self.progress(f"round {round_index}/{rounds_required}: {arm}/{phase}")
                    row = self.run_phase(arm, phase, round_index, False)
                    rows.append(row)
                    self.units_done += 1
                    if not row["verified"]:
                        atomic(self.evidence / f"round-{round_index:02d}.failed.json", {"rows": rows})
                        raise RuntimeError(f"round {round_index}: {arm}/{phase} failed the oracle: {row['oracle_failures']}")
            values = {}
            for arm in measured:
                phases = {r["phase"]: {"seconds": r["seconds"], "peak_rss_bytes": r["peak_rss_bytes"]} for r in rows if r["arm"] == arm}
                values[arm] = {"seconds": sum(p["seconds"] for p in phases.values()),
                               "mean_peak_rss_bytes": statistics.fmean(p["peak_rss_bytes"] for p in phases.values()),
                               "phases": phases}
            saved = {"status": "complete", "round": round_index, "order": order, "invocation": self.invocation,
                     "binary_sha256": self.binary_sha, "values": values, "rows": rows, "completed_at": now()}
            atomic(path, saved)
            completed.append(saved)
            quick = compare([saved], "candidate", "stock" if "stock" in measured else measured[-1])
            self.event(event="round_complete", round=round_index, candidate_vs_stock={k: quick[k] for k in ("score_pct", "time_saved_pct", "rss_saved_pct")})

        # ---- tamper checks (as _run, plus the shared reference states)
        self.progress("tamper checks: sources, binaries, toolchain, workloads, judge, research states")
        for name in ("candidate", "incumbent"):
            if snapshot_arm(arms_requested[name], ROOT / "stock", trusted_stock.files,
                            self.evidence / "sources" / name).identity["sha256"] != sources[name]["sha256"]:
                raise RuntimeError(f"{name} source snapshot changed during the run")
        if stock_tree(ROOT / "stock").identity["sha256"] != pinned_stock["source_sha256"]:
            raise RuntimeError("pinned stock tree changed during the run")
        for name in measured:
            if sha(self.binaries[name]) != self.binary_sha[name]:
                raise RuntimeError(f"{name} binary changed during the run")
        if {**self.toolchain_identity(), "rusage_launcher_sha256": sha(LAUNCHER)} != toolchain:
            raise RuntimeError("toolchain or rusage launcher changed during the run")
        if self.workload_identity() != workloads:
            raise RuntimeError("frozen workloads changed during the run")
        if bundle_signature(self.bundle_dir) != meta["signature"]:
            raise RuntimeError("research reference states changed during the run")
        if sha(Path(__file__).resolve()) != judge_sha:
            raise RuntimeError("judge script changed during the run")
        self.units_done += 1
        return self.finish(completed, measured, aliases, builds, compat, cross_reader_ok, config)

    def finish(self, completed, measured, aliases, builds, compat, cross_reader_ok, config) -> int:
        resolve = lambda name: aliases.get(name, name)  # noqa: E731
        arms = {}
        for name in ("candidate", "incumbent", "stock"):
            measured_as = resolve(name)
            row = {**self.arm_sources[name], "measured_as": measured_as,
                   "binary_sha256": self.binary_sha[measured_as], "code_sha256": builds[measured_as].get("code_sha256"),
                   "build_seconds": builds[measured_as]["seconds"],
                   "policy": builds[measured_as]["policy"] if name != "stock" else None}
            if completed:
                seconds = [r["values"][measured_as]["seconds"] for r in completed]
                rss = [r["values"][measured_as]["mean_peak_rss_bytes"] for r in completed]
                row.update(median_total_seconds=statistics.median(seconds), median_peak_rss_bytes=statistics.median(rss),
                           round_seconds=seconds, round_mean_peak_rss_bytes=rss,
                           per_phase={phase: {
                               "median_seconds": statistics.median(r["values"][measured_as]["phases"][phase]["seconds"] for r in completed),
                               "median_peak_rss_bytes": statistics.median(r["values"][measured_as]["phases"][phase]["peak_rss_bytes"] for r in completed),
                               "round_seconds": [r["values"][measured_as]["phases"][phase]["seconds"] for r in completed],
                               "round_peak_rss_bytes": [r["values"][measured_as]["phases"][phase]["peak_rss_bytes"] for r in completed]}
                               for phase in PHASES})
            arms[name] = row
        comparisons = {}
        if completed:
            comparisons["candidate_vs_incumbent"] = compare(completed, "candidate", resolve("incumbent"))
            comparisons["candidate_vs_stock"] = compare(completed, "candidate", "stock")
            if self.args.calibration:
                comparisons["incumbent_vs_stock"] = compare(completed, "incumbent", "stock")
        reasons = []
        if self.mode != "promotion":
            reasons.append(f"{self.mode} evidence cannot promote; promotion requires {PROMOTION_ROUNDS} fresh rounds")
        else:
            inc, stk = comparisons["candidate_vs_incumbent"], comparisons["candidate_vs_stock"]
            if len(completed) != PROMOTION_ROUNDS:
                reasons.append(f"promotion requires {PROMOTION_ROUNDS} rounds; got {len(completed)}")
            if self.arm_sources["candidate"]["build_input_sha256"] in (self.arm_sources["incumbent"]["build_input_sha256"],
                                                                      self.arm_sources["stock"]["build_input_sha256"]):
                reasons.append("candidate build input is identical to the incumbent or stock")
            if builds["candidate"]["code_sha256"] in (builds[resolve("incumbent")]["code_sha256"], builds["stock"]["code_sha256"]):
                reasons.append("candidate binary machine code is identical to the incumbent or stock")
            if inc["score_wins"] < PROMOTION_WINS:
                reasons.append(f"candidate won {inc['score_wins']}/{len(completed)} score pairs; {PROMOTION_WINS} required")
            if inc["score_pct"] < MINIMUM_SCORE_PCT:
                reasons.append(f"median paired score {inc['score_pct']:.4f}% is below {MINIMUM_SCORE_PCT}%")
            for axis in ("time_saved_pct", "rss_saved_pct"):
                for label, comparison in (("incumbent", inc), ("stock", stk)):
                    if comparison[axis] < -AXIS_REGRESSION_CAP_PCT:
                        reasons.append(f"{axis} versus {label} is {comparison[axis]:.3f}%, beyond the -{AXIS_REGRESSION_CAP_PCT}% axis cap")
            if stk["score_pct"] <= 0:
                reasons.append(f"candidate does not beat stock on score ({stk['score_pct']:.4f}%)")
            if inc["rss_saved_pct"] < -RSS_GUARD_PCT:
                reasons.append(f"peak memory versus incumbent is {inc['rss_saved_pct']:.3f}%, beyond the -{RSS_GUARD_PCT}% memory guard")
        if self.args.calibration:
            reasons.append("calibration run (identical-arm null or sensitivity check); never promotable")
        eligible = self.mode == "promotion" and not reasons
        result = {
            "schema": SCHEMA, "status": "complete", "mode": self.mode, "measurement_valid": bool(completed) or self.mode == "correctness",
            "rounds": len(completed), "labels": self.labels, "calibration": bool(self.args.calibration),
            "started_at": self.started_at, "completed_at": now(), "config_sha256": canonical_sha(config),
            "evidence": str(self.evidence), "arms": arms, "aliases": aliases, "phases": PHASES,
            "correctness": {
                "all_arms_matched_parent_stock_oracle": True, "verified_units": self.units_done, "total_units": self.units_total,
                "candidate_compatibility_tests_passed": (compat["candidate"]["passed"] if "candidate" in compat else None),
                "compatibility": {k: {x: v.get(x) for x in ("passed", "counts", "failed", "baseline_pass_not_passing", "runs")}
                                  for k, v in compat.items()},
                "cross_reader_ok": cross_reader_ok,
                "build": {k: {"ok": v["ok"], "exit_code": v["exit_code"], "stderr_tail": v["stderr_tail"][-1500:],
                              "binary_sha256": v.get("binary_sha256")} for k, v in builds.items()},
                "oracle": "per round: stock-read snapshot equality (tree id fast path, else stock ls+restore+check), "
                          "restore manifest == stock restore, rotating 1/5 check --read-data-subset of pruned repo, "
                          "ls --json parsed equality; warm-up: stock check --read-data of every arm-written repo, "
                          "stock restores arm-written v1 and pruned repos, arm detects a corrupted pack",
                "stock_restore_vs_source_differences": self.states.get("stock_restore_vs_source_differences"),
            },
            "comparisons": comparisons,
            "eligibility": {"eligible": eligible, "reasons": reasons, "why": reasons, "policy": {
                "rounds": PROMOTION_ROUNDS, "score_wins": PROMOTION_WINS, "minimum_score_pct": MINIMUM_SCORE_PCT,
                "axis_regression_cap_pct": AXIS_REGRESSION_CAP_PCT, "must_beat_stock_on_score": True,
                "score_time_weight": TIME_WEIGHT, "rss_guard_vs_incumbent_pct": RSS_GUARD_PCT,
                "match_rounds": MATCH_ROUNDS, "reference_rounds": REFERENCE_ROUNDS}},
            "rss_measurement_method": RSS_MEASUREMENT_METHOD,
            "measurement": {"time": "parent-side time.monotonic() around the sandboxed process (includes ~8 ms bwrap setup, identical for all arms)",
                            "rss": "wait4() ru_maxrss of the bwrap monitor whose only child is restic (namespace PID 1): "
                                   "max(monitor ~11 MiB, restic VmHWM at exit)",
                            "T": "sum of phase wall seconds", "M": "mean over phases of phase peak RSS",
                            "cpus": self.cpus, "gomaxprocs": self.procs, "gc_env": "GOGC/GOMEMLIMIT unset (restic defaults)",
                            "storage": "tmpfs for corpus, repositories, restore targets and restic temp/cache"},
            "states": self.states, "references": self.references, "workload": workload_record(),
        }
        if getattr(self, "research_cache", None) is not None:  # research fast path only; official results unchanged
            result["research_cache"] = self.research_cache
        backend = remote_summary(completed, measured)
        if backend:  # remote-backend workloads only (REMOTE.md): requests and bytes per arm and phase
            result["remote_backend"] = {"profile": workload_settings()["remote"], "arms": backend}
        atomic(self.out, result)
        self.progress("complete: all arms matched the stock oracle", "complete")
        print(json.dumps({k: {x: v.get(x) for x in ("score_pct", "time_saved_pct", "rss_saved_pct", "score_wins", "interval90")}
                          for k, v in comparisons.items()}, sort_keys=True))
        return 0


# ============================================================ one-time setup (trusted)
ISOLATION_PROBE_GO = r'''package main

import (
	"encoding/json"
	"net"
	"os"
	"strings"
	"syscall"
	"time"
)

func main() {
	r := map[string]interface{}{}
	_, err := net.DialTimeout("tcp", "1.1.1.1:443", 2*time.Second)
	r["network_denied"] = err != nil
	_, err = net.DialTimeout("tcp", "127.0.0.1:22", 2*time.Second)
	r["loopback_ssh_denied"] = err != nil
	ents, _ := os.ReadDir("/")
	names := []string{}
	for _, e := range ents { names = append(names, e.Name()) }
	r["root_entries"] = names
	_, err = os.Stat("/opt")
	r["host_opt_invisible"] = os.IsNotExist(err)
	err = os.WriteFile("/data/x", []byte("x"), 0644)
	r["data_readonly"] = err != nil
	err = os.WriteFile("/restic-overwrite", []byte("x"), 0644)
	r["root_not_writable"] = err != nil
	err = os.WriteFile("/work/ok", []byte("x"), 0644)
	r["work_writable"] = err == nil
	err = syscall.Mknod("/work/dev", syscall.S_IFBLK|0600, 0x0902)
	r["mknod_denied"] = err != nil
	err = syscall.Mount("tmpfs", "/work", "tmpfs", 0, "")
	r["mount_denied"] = err != nil
	procs, _ := os.ReadDir("/proc")
	n := 0
	for _, p := range procs { if strings.Trim(p.Name(), "0123456789") == "" { n++ } }
	r["visible_pids"] = n
	r["pid"] = os.Getpid()
	st, _ := os.ReadFile("/proc/self/status")
	for _, line := range strings.Split(string(st), "\n") { if strings.HasPrefix(line, "CapEff") || strings.HasPrefix(line, "CapBnd") { r[strings.Fields(line)[0]] = strings.Fields(line)[1] } }
	h, _ := os.Hostname()
	r["hostname"] = h
	err = syscall.Kill(1, 0)
	r["kill_self_pid1_ok"] = err == nil
	json.NewEncoder(os.Stdout).Encode(r)
}
'''


LAUNCHER_GO = r'''package main

// rusage-launch: the only process the judge forks for a measured run. It is tiny (a few MiB), so the
// kernel's exec-time seeding of ru_maxrss with the parent image's high-water mark is negligible; it
// starts the sandbox, blocks in wait4, and reports wall time (monotonic) and the exact ru_maxrss.
import (
	"encoding/json"
	"os"
	"strconv"
	"syscall"
	"time"
)

func main() {
	timeout, _ := strconv.ParseFloat(os.Args[1], 64)
	stdout, err := os.Create(os.Args[2])
	if err != nil { panic(err) }
	stderr, err := os.Create(os.Args[3])
	if err != nil { panic(err) }
	argv := os.Args[5:]
	devnull, _ := os.Open("/dev/null")
	attr := &os.ProcAttr{Files: []*os.File{devnull, stdout, stderr}, Sys: &syscall.SysProcAttr{Setsid: true}}
	start := time.Now()
	proc, err := os.StartProcess(argv[0], argv, attr)
	if err != nil { panic(err) }
	timedOut := false
	timer := time.AfterFunc(time.Duration(timeout*float64(time.Second)), func() {
		timedOut = true
		syscall.Kill(-proc.Pid, syscall.SIGKILL)
	})
	var status syscall.WaitStatus
	var usage syscall.Rusage
	for {
		_, err = syscall.Wait4(proc.Pid, &status, 0, &usage)
		if err != syscall.EINTR { break }
	}
	wall := time.Since(start)
	timer.Stop()
	var self syscall.Rusage
	syscall.Getrusage(syscall.RUSAGE_SELF, &self)
	code := status.ExitStatus()
	if status.Signaled() { code = -int(status.Signal()) }
	json.NewEncoder(os.Stdout).Encode(map[string]interface{}{
		"wall_ns": wall.Nanoseconds(), "exit_code": code, "timed_out": timedOut,
		"maxrss_kib": usage.Maxrss, "utime_us": usage.Utime.Sec*1000000 + usage.Utime.Usec,
		"stime_us": usage.Stime.Sec*1000000 + usage.Stime.Usec, "minflt": usage.Minflt,
		"nvcsw": usage.Nvcsw, "nivcsw": usage.Nivcsw, "launcher_maxrss_kib": self.Maxrss,
	})
}
'''
LAUNCHER = ROOT / "judge" / "bin" / "rusage-launch"


def launcher_run(argv: list[str], stdout_path: Path, stderr_path: Path, timeout: float) -> dict:
    """Measured run through the trusted tiny launcher (exact ru_maxrss, launcher-side monotonic wall)."""
    began = time.monotonic()
    completed = subprocess.run([str(LAUNCHER), repr(float(timeout)), str(stdout_path), str(stderr_path), "--", *argv],
                               stdin=subprocess.DEVNULL, capture_output=True, timeout=timeout + 120)
    parent_seconds = time.monotonic() - began
    if completed.returncode != 0:
        raise RuntimeError("rusage launcher failed: " + completed.stderr.decode("utf-8", "replace")[-1000:])
    report = json.loads(completed.stdout)
    return {"seconds": report["wall_ns"] / 1e9, "peak_rss_bytes": report["maxrss_kib"] * 1024,
            "exit_code": report["exit_code"], "timed_out": report["timed_out"],
            "user_seconds": report["utime_us"] / 1e6, "system_seconds": report["stime_us"] / 1e6,
            "minor_faults": report["minflt"], "voluntary_switches": report["nvcsw"],
            "involuntary_switches": report["nivcsw"], "launcher_peak_rss_bytes": report["launcher_maxrss_kib"] * 1024,
            "parent_seconds": parent_seconds}


def setup(argv: list[str]) -> int:
    parser = argparse.ArgumentParser(prog="restic_judge.py setup")
    parser.add_argument("what", choices=["pristine", "compat-baseline", "isolation", "stock-build", "launcher"])
    parser.add_argument("--cpus", default=DEFAULT_CPUS)
    args = parser.parse_args(argv)
    cpus = parse_cpus(args.cpus)
    os.sched_setaffinity(0, set(cpus))
    stock = stock_tree(ROOT / "stock")
    scratch = Path("/dev/shm/restic-league-v2/setup-" + args.what)
    shutil.rmtree(scratch, ignore_errors=True)
    scratch.mkdir(parents=True)
    evidence = ROOT / "setup-evidence" / args.what
    evidence.mkdir(parents=True, exist_ok=True)
    try:
        if args.what == "launcher":
            work = scratch / "b"
            for sub in ("src", "gocache", "home", "tmp", "out"):
                (work / sub).mkdir(parents=True)
            (work / "src" / "go.mod").write_text("module rusagelaunch\n\ngo 1.25\n")
            (work / "src" / "main.go").write_text(LAUNCHER_GO)
            run = measured_run(build_sandbox(work, len(cpus)) + ["/toolchain/go/bin/go", "build", "-trimpath", "-buildvcs=false",
                                                                 "-ldflags", "-s -w", "-o", "/build/out/rusage-launch", "."],
                               evidence / "build.stdout", evidence / "build.stderr", 600)
            if run["exit_code"]:
                raise SystemExit(tail(evidence / "build.stderr"))
            LAUNCHER.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(work / "out" / "rusage-launch", LAUNCHER)
            os.chmod(LAUNCHER, 0o555)
            probe = []
            for megabytes in (0, 64, 300):
                code = f"b=bytearray({megabytes}*1024*1024)\nfor i in range(0,len(b),4096): b[i]=1"
                out = launcher_run(["/usr/bin/python3", "-c", code], evidence / "p.out", evidence / "p.err", 60)
                probe.append({"allocated_mib": megabytes, "peak_rss_mib": out["peak_rss_bytes"] / MIB,
                              "launcher_peak_rss_mib": out["launcher_peak_rss_bytes"] / MIB})
            record = {"sha256": sha(LAUNCHER), "source_sha256": sha_bytes(LAUNCHER_GO.encode()), "probe": probe,
                      "created_at": now()}
            atomic(ROOT / "judge" / "launcher.json", record)
            print(json.dumps(record, indent=1))
        elif args.what == "stock-build":
            receipts = []
            for attempt in (1, 2):
                folder = evidence / f"attempt-{attempt}"
                shutil.rmtree(folder, ignore_errors=True)
                receipts.append(trusted_build("stock", stock, stock, scratch, folder, len(cpus)))
            if not receipts[0]["ok"] or receipts[0]["binary_sha256"] != receipts[1]["binary_sha256"]:
                raise SystemExit(f"stock build is not reproducible: {receipts}")
            record = {"binary_sha256": receipts[0]["binary_sha256"], "code_sha256": receipts[0]["code_sha256"],
                      "build_input_sha256": receipts[0]["build_input_sha256"],
                      "version": receipts[0]["version"], "seconds": [r["seconds"] for r in receipts],
                      "reproduced": True, "argv": receipts[0]["argv"], "created_at": now()}
            atomic(ROOT / "stock-build.json", record)
            print(json.dumps(record, indent=1))
        elif args.what == "pristine":
            binary = ROOT / "setup-evidence" / "stock-build" / "attempt-1" / "builds" / "stock.restic"
            if sha(binary) != load(ROOT / "stock-build.json")["binary_sha256"]:
                raise SystemExit("stock binary differs from stock-build.json")
            target = WORKLOADS / "pristine-repo"
            if target.exists():
                raise SystemExit("pristine repository already frozen; never regenerated")
            work = scratch / "work"
            (work / "home").mkdir(parents=True)
            (work / "tmp").mkdir()
            argv = restic_sandbox(binary, work, None, len(cpus)) + ["init"]
            run = measured_run(argv, evidence / "init.stdout", evidence / "init.stderr", 120)
            if run["exit_code"]:
                raise SystemExit(tail(evidence / "init.stderr"))
            subprocess.run(["cp", "-a", str(work / "repo"), str(target)], check=True)
            record = {"tree_sha256": tree_sha256_of_files(target), "files": tree_files(target),
                      "created_by_binary_sha256": sha(binary), "stdout": tail(evidence / "init.stdout"), "created_at": now()}
            atomic(WORKLOADS / "pristine.json", record)
            print(json.dumps(record, indent=1))
        elif args.what == "compat-baseline":
            receipt = compatibility_tests("stock", stock, stock, scratch, evidence, len(cpus), None)
            atomic(ROOT / "compat-baseline.json", {"results": receipt["results"], "counts": receipt["counts"],
                                                  "runs": receipt["runs"], "failed_on_stock": receipt["failed"],
                                                  "build_input_sha256": receipt["build_input_sha256"], "created_at": now()})
            print(json.dumps({k: receipt[k] for k in ("counts", "runs", "failed")}, indent=1))
        elif args.what == "isolation":
            work = scratch / "b"
            for sub in ("src", "gocache", "home", "tmp", "out"):
                (work / sub).mkdir(parents=True)
            (work / "src" / "go.mod").write_text("module probe\n\ngo 1.25\n")
            (work / "src" / "main.go").write_text(ISOLATION_PROBE_GO)
            run = measured_run(build_sandbox(work, len(cpus)) + ["/toolchain/go/bin/go", "build", "-o", "/build/out/probe", "."],
                               evidence / "build.stdout", evidence / "build.stderr", 600)
            if run["exit_code"]:
                raise SystemExit(tail(evidence / "build.stderr"))
            runw = scratch / "run"
            (runw / "home").mkdir(parents=True)
            (runw / "tmp").mkdir()
            data = scratch / "data"
            data.mkdir()
            argv = restic_sandbox(work / "out" / "probe", runw, data, len(cpus))
            argv = argv[:argv.index("--") + 1] + ["/restic"]
            result = measured_run(argv, evidence / "probe.stdout", evidence / "probe.stderr", 60)
            observed = json.loads((evidence / "probe.stdout").read_text())
            checks = {k: observed.get(k) is True for k in ("network_denied", "loopback_ssh_denied", "host_opt_invisible",
                                                           "data_readonly", "root_not_writable", "work_writable",
                                                           "mknod_denied", "mount_denied")}
            checks["pid_namespace_pid1"] = observed.get("pid") == 1
            checks["few_visible_pids"] = observed.get("visible_pids", 99) <= 2
            checks["hostname_fixed"] = observed.get("hostname") == "league"
            record = {"verified": all(checks.values()), "checks": checks, "observed": observed,
                      "exit_code": result["exit_code"], "sandbox_argv_template": restic_sandbox(Path("/BINARY"), Path("/WORK"), Path("/DATA"), len(cpus)),
                      "build_sandbox_template": build_sandbox(Path("/WORK"), len(cpus)),
                      "judge_sha256": sha(Path(__file__).resolve()), "created_at": now(),
                      "scope": "candidate builds, go tests and every restic invocation run in these bwrap sandboxes"}
            atomic(ROOT / "judge" / "isolation.json", record)
            print(json.dumps(record, indent=1))
    finally:
        shutil.rmtree(scratch, ignore_errors=True)
    return 0


def main(argv=None) -> int:
    argv = list(sys.argv[1:] if argv is None else argv)
    if argv[:1] == ["setup"]:
        return setup(argv[1:])
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--candidate", type=Path, required=True)
    parser.add_argument("--incumbent", type=Path, required=True)
    parser.add_argument("--stock", type=Path, required=True)
    parser.add_argument("--mode", choices=list(MODE_ROUNDS), required=True)
    parser.add_argument("--out", type=Path, required=True, help="result JSON path; evidence goes to <out minus .json>-evidence/")
    parser.add_argument("--status", type=Path)
    parser.add_argument("--cpus", default=DEFAULT_CPUS)
    parser.add_argument("--labels")
    parser.add_argument("--resume", action="store_true")
    parser.add_argument("--calibration", action="store_true", help="measure identical arms separately; never promotable")
    parser.add_argument("--scratch", help="tmpfs directory (default $RESTIC_JUDGE_SCRATCH or /dev/shm/restic-league-v2)")
    parser.add_argument("--workload", help="frozen workload name: main (default; $RESTIC_JUDGE_WORKLOAD)")
    parser.add_argument("--workloads", help="suite: comma-separated workload names; rounds rotate over them (SUITE.md). "
                                            "One name = --workload NAME")
    args = parser.parse_args(argv)
    if sys.platform != "linux" or os.geteuid() != 0:
        parser.error("the judge runs as root on Linux (bwrap sandbox, restore of ownership)")
    args.suite = None
    if args.workloads:
        if args.workload:
            parser.error("--workload and --workloads are exclusive")
        names = parse_workload_list(args.workloads)
        if len(names) == 1:
            args.workload = names[0]
        else:
            args.suite = names
            for name in names:
                if not (workload_root(name) / "workloads.json").is_file():
                    parser.error(f"workload {name!r}: {workload_root(name)}/workloads.json not found")
            set_workload(names[0])
    if args.workload:
        set_workload(args.workload)
    if not (WORKLOADS / "workloads.json").is_file():
        parser.error(f"workload {WORKLOAD_NAME!r}: {WORKLOADS}/workloads.json not found")
    return Judge(args).run()


if __name__ == "__main__":
    raise SystemExit(main())
