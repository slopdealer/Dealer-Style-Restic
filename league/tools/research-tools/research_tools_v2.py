#!/usr/bin/env python3
# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""Restic League V2 - builders' fast research tools (box-side harness), version 2. CONTRACT.md (v2 section) is
the interface. Deployed beside v1 as research_tools_v2.py until the coordinator switches over.

    python3 research_tools_v2.py <op> --candidate DIR --incumbent DIR [--stock DIR] [--base DIR] --cpus 6-9 --team T1
            --workload main|big-index|large-files|remote [--workloads a,b] --out /path/result.json [op options]

ops: build | test | bench | profile | sweep | race | baseline | oracle | ablate | timeline | gobench

v2 (from the S017 M004 usage study): bench stops a phase as soon as its verdict is clear (sequential), uses a robust
noise band (pre-measured null + the call's own spread; it never measures a null inside a builder call), takes several
workloads in one call and says what the phase deltas are worth on the workload score; race is quick by default;
test can compile in the candidate's own new *_test.go files; four new diagnostic ops.

DIAGNOSTIC ONLY. This is not the judge and nothing here scores. It reuses the judge's sandbox (bwrap, no network),
its pinned offline Go toolchain, its build input rule (stock + the arm's non-test *.go under cmd/ and internal/, so
stock *_test.go are overlaid) and the judge's prepared repository states (read-only, under a shared lock; every run
works on a private copy). It never names or reads a workload outside the four above.

Workload `remote` (judge REMOTE.md; needs a judge with remote-backend support): every restic process of an arm runs
against the workload's frozen REST server, started before and stopped after the process, outside the sandbox and the
clock, exactly as the judge does. bench, timeline, profile, ablate and baseline then report REQUEST COUNTS AND BYTES
per phase and arm (the server's own statistics): on that workload they are what a builder aims with.

Exit 0 whenever the harness did its job (also when the candidate fails to build, fails tests or crashes).
Exit 2 = a bad argument a builder can fix; exit 1 = harness fault. Both write {"ok": false, "error": ...}.
The harness does NOT take the CPU lane lock: the caller holds it.
"""
import argparse
import collections
import fcntl
import hashlib
import json
import os
import re
import secrets
import shutil
import signal
import statistics
import subprocess
import sys
import threading
import time
import traceback
from pathlib import Path

sys.dont_write_bytecode = True
ROOT = Path(os.environ.get("RESTIC_LEAGUE_ROOT", "/opt/league/restic-league-v2"))
if os.environ.get("RESEARCH_TOOLS_JUDGE"):  # dev runs only: a scratch judge FILE instead of the deployed one
    import importlib.util
    _spec = importlib.util.spec_from_file_location("restic_judge_fastpath", os.environ["RESEARCH_TOOLS_JUDGE"])
    j = importlib.util.module_from_spec(_spec)
    sys.modules["restic_judge_fastpath"] = j
    _spec.loader.exec_module(j)
else:
    sys.path.insert(0, str(ROOT / "judge"))
    import restic_judge_fastpath as j  # noqa: E402  (read-only reuse: sandbox, toolchain, build input, phases, launcher)

# persistent harness state (Go caches, binaries, nulls, oracle references); RESEARCH_TOOLS_STATE = a dev run's own dir
RT = Path(os.environ.get("RESEARCH_TOOLS_STATE") or ROOT / "research-tools")
WORKLOAD_CHOICES = ("main", "big-index", "large-files", "remote")
MODULE = "github.com/restic/restic/"
NOWHERE = Path("/nonexistent-research-tools")
GO = "/toolchain/go/bin/go"
HARNESS_VERSION = "rt-2"
RUN_TIMEOUT = 600.0
NULL_PAIRS = 8
NULL_MAX_AGE_S = 14 * 86400
BAND_FLOOR_PCT = 0.3
NULL_DIR = "null2"          # v2 noise store (v1 keeps research-tools/null/, read here as a fallback only)
SEQ_MIN_PAIRS = 3           # sequential bench: first look after this many pairs
SEQ_TARGET_PAIRS = 5        # normal stopping point
SEQ_MAX_PAIRS = 9           # only an UNRESOLVED phase that could still resolve runs this far
SEQ_HARD_MAX = 15
SEQ_EARLY_MARGIN = 1.5      # an early stop (before the target) needs the median this many bands away from zero
SEQ_CONTINUE_RATIO = 0.7    # after the target, continue only if |median| is at least this share of the band
GOBENCH_KILL_S = 150        # per arm: a benchmark set with heavy setup is cut, not waited for
RACE_QUICK_RUNS = 40
RACE_FULL_RUNS = 200
D2 = {2: 1.128, 3: 1.693, 4: 2.059}   # range -> sigma for tiny samples
BINCACHE_KEEP = 80
TEST_TIMEOUT = "5m"   # go test -timeout: a hung test panics with its goroutine dump and is reported as a failure
TEST_KILL_S = 420     # the harness kills the go test processes after this
# phases that only read the repository (they add and remove a lock file): their runs share one private copy of the
# prepared state per invocation instead of a fresh copy per run; the copy is checked after every run
READONLY_PHASES = ("restore", "check-read-data", "ls-json")

PROFILE_GO = r'''//go:build profile

package main

// research-tools instrumentation only (never part of a candidate): with RT_PROF_DIR set, a sampler watches RSS
// and writes a heap profile + MemStats each time RSS has grown ~6% + 4 MiB past the last dump, so the last dump
// is the heap in use at (or just before) the peak.
import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"time"
)

func init() {
	dir := os.Getenv("RT_PROF_DIR")
	if dir == "" {
		return
	}
	_ = os.MkdirAll(dir, 0o755)
	go rtprofSampler(dir, time.Now())
}

func rtprofRSS(page int64) int64 {
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return 0
	}
	f := strings.Fields(string(b))
	if len(f) < 2 {
		return 0
	}
	v, _ := strconv.ParseInt(f[1], 10, 64)
	return v * page
}

func rtprofWrite(path string, write func(*os.File) error) {
	f, err := os.Create(path + ".tmp")
	if err != nil {
		return
	}
	err = write(f)
	_ = f.Close()
	if err == nil {
		_ = os.Rename(path+".tmp", path)
	}
}

func rtprofSampler(dir string, t0 time.Time) {
	page := int64(os.Getpagesize())
	var last int64
	tick := time.NewTicker(2 * time.Millisecond)
	for range tick.C {
		r := rtprofRSS(page)
		if r < (16<<20) || r < last+last/16+(4<<20) {
			continue
		}
		last = r
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		rtprofWrite(filepath.Join(dir, "peak-heap.pb.gz"), func(f *os.File) error {
			return pprof.Lookup("heap").WriteTo(f, 0)
		})
		rtprofWrite(filepath.Join(dir, "peak-memstats.json"), func(f *os.File) error {
			return json.NewEncoder(f).Encode(map[string]interface{}{
				"rss": r, "t": time.Since(t0).Seconds(), "HeapAlloc": ms.HeapAlloc, "HeapInuse": ms.HeapInuse,
				"HeapIdle": ms.HeapIdle, "HeapReleased": ms.HeapReleased, "StackInuse": ms.StackInuse, "Sys": ms.Sys,
				"NumGC": ms.NumGC, "PauseTotalNs": ms.PauseTotalNs, "HeapObjects": ms.HeapObjects,
			})
		})
	}
}
'''


class InputError(Exception):
    """A bad argument the builder can fix (exit 2)."""


# ============================================================ small helpers
def atomic_json(path: Path, value) -> None:
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    temporary = path.with_name(f"{path.name}.tmp-{os.getpid()}")
    temporary.write_text(json.dumps(value, indent=1, sort_keys=False, default=str) + "\n", encoding="utf-8")
    os.replace(temporary, path)


def sha_text(text: str) -> str:
    return hashlib.sha256(text.encode()).hexdigest()


def tail_text(path: Path, size: int = 3000) -> str:
    return j.tail(path, size)


def first_lines(text: str, count: int = 40) -> list:
    return [line for line in text.splitlines() if line.strip()][:count]


def median(values):
    return statistics.median(values) if values else None


def r2(value, digits=2):
    return None if value is None else round(value, digits)


class Ctx:
    """One invocation: arguments, scratch, bundle, progress."""

    def __init__(self, args):
        self.args = args
        self.op = args.op
        self.t0 = time.monotonic()
        self.cpus = j.parse_cpus(args.cpus)
        self.procs = len(self.cpus)
        self.team = args.team
        self.out = Path(args.out).resolve()
        self.status_path = self.out.with_name(self.out.name + ".status.json")
        self.started = j.now()
        self.units_done, self.units_total = 0, 1
        self.gocache = RT / "gocache" / self.team
        self.stock_dir = Path(args.stock).resolve()
        self.scratch = None
        self.workload = args.workload
        self.bundles = {}          # workload -> (bundle dir, shared lock handle)
        self.stock_arm = None
        self.arms = {}
        self.state_signature = {}

    # ---- progress (AGENTS.md s17): one line per finished unit on stdout + an atomically rewritten status file
    def progress(self, stage: str, done: bool = False, state: str = "running") -> None:
        if done:
            self.units_done += 1
        value = {"status": state, "op": self.op, "units_done": self.units_done, "units_total": self.units_total,
                 "started_at": self.started, "updated_at": j.now(), "stage": stage, "pid": os.getpid(),
                 "elapsed_s": round(time.monotonic() - self.t0, 1)}
        try:
            atomic_json(self.status_path, value)
        except OSError:
            pass
        print(json.dumps(value), flush=True)

    def setup(self) -> None:
        os.sched_setaffinity(0, self.cpus)
        base = Path(os.environ.get("RESEARCH_TOOLS_SCRATCH") or "/dev/shm/restic-research-tools")
        base.mkdir(parents=True, exist_ok=True)
        if not j.is_tmpfs(base):
            raise RuntimeError(f"scratch root {base} is not on tmpfs")
        if not os.environ.get("RESEARCH_TOOLS_SCRATCH"):
            sweep_stale(base)
        self.scratch = base / f"rt-{self.team}-{self.op}-{os.getpid()}-{secrets.token_hex(4)}"
        self.scratch.mkdir()
        self.gocache.mkdir(parents=True, exist_ok=True)
        for sub in ("bincache", NULL_DIR, "oracle-ref"):
            (RT / sub).mkdir(parents=True, exist_ok=True)
        j.set_workload(self.args.workload)
        stock_files = j.tree_files(self.stock_dir, j.SOURCE_EXCLUDED_DIRS)
        self.stock_arm = j.ArmTree(stock_files, NOWHERE, self.stock_dir)

    def cleanup(self) -> None:
        if self.scratch is not None:
            subprocess.run(["chmod", "-R", "u+rwX", str(self.scratch)], stderr=subprocess.DEVNULL)
            shutil.rmtree(self.scratch, ignore_errors=True)
        for _, lock in self.bundles.values():
            lock.close()

    def switch_workload(self, name: str) -> None:
        """One invocation may walk several workloads: the judge module keeps the active one in module globals."""
        if name not in WORKLOAD_CHOICES:
            raise InputError(f"unknown workload {name!r}; the workloads are " + ", ".join(WORKLOAD_CHOICES))
        if name != self.workload and self.scratch is not None:
            drop(self.scratch / "state")   # shared read-only state copies of the previous workload (1-2 GB of tmpfs)
            self.state_signature = {}
        self.workload = name
        j.set_workload(name)

    # ---- the judge's prepared repository states (read-only; shared lock keeps the judge from evicting them)
    def use_bundle(self) -> Path:
        if self.workload in self.bundles:
            return self.bundles[self.workload][0]
        broot = j.bundle_root()
        found = []
        for directory in broot.glob("bundle-*"):
            try:
                meta = j.load(directory / "bundle.json")
            except (OSError, ValueError):
                continue
            if meta.get("complete") is True:
                found.append(((directory / "bundle.json").stat().st_mtime, directory))
        if not found:
            raise RuntimeError(f"no prepared repository states for workload {self.workload!r} on this box; the judge "
                               "builds them on its first research job (run one benchmark_candidate or check_parity)")
        directory = max(found)[1]
        (j.CACHE_ROOT / "locks").mkdir(parents=True, exist_ok=True)
        lock = (j.CACHE_ROOT / "locks" / f"{directory.name}.lock").open("a+b")
        fcntl.flock(lock, fcntl.LOCK_SH)
        if not (directory / "bundle.json").is_file():
            raise RuntimeError("prepared repository states vanished while locking; retry")
        self.bundles[self.workload] = (directory, lock)
        return directory


def sweep_stale(base: Path) -> None:
    """Remove scratch dirs of dead invocations (a killed harness cannot clean up after itself)."""
    for directory in base.glob("rt-*"):
        parts = directory.name.split("-")
        try:
            pid = int(parts[-2])
        except (ValueError, IndexError):
            continue
        if not Path(f"/proc/{pid}").exists():
            subprocess.run(["chmod", "-R", "u+rwX", str(directory)], stderr=subprocess.DEVNULL)
            shutil.rmtree(directory, ignore_errors=True)


# ============================================================ arms: snapshot + build input + sandboxed go
class Arm:
    def __init__(self, name, tree, files, work, identity, policy):
        self.name, self.tree, self.files, self.work, self.identity, self.policy = name, tree, files, work, identity, policy
        self.src = work / "src"

    def promotable_changed_vs(self, other: "Arm") -> list:
        def promotable(rel):
            return rel.endswith(".go") and not rel.endswith("_test.go") and rel.split("/")[0] in ("cmd", "internal")
        keys = set(self.files) | set(other.files)
        return sorted(rel for rel in keys if promotable(rel) and self.files.get(rel) != other.files.get(rel))


def prepare_arm(ctx: Ctx, name: str, tree: Path) -> Arm:
    """Snapshot a live tree into the scratch as the judge's build input (stock + the arm's promotable files)."""
    if name in ctx.arms:
        return ctx.arms[name]
    tree = Path(tree).resolve()
    if not (tree / "cmd" / "restic").is_dir() or not (tree / "go.mod").is_file():
        raise InputError(f"{name} tree {tree} is not a restic source tree (cmd/restic or go.mod missing)")
    last = None
    for _ in range(3):  # a live team workspace may change under us: retry the snapshot
        files = j.tree_files(tree, j.SOURCE_EXCLUDED_DIRS)
        arm_tree = j.ArmTree(files, NOWHERE, tree)
        build_files, policy = j.build_input(arm_tree, ctx.stock_arm)
        work = ctx.scratch / "arms" / name
        shutil.rmtree(work, ignore_errors=True)
        for sub in ("src", "home", "tmp", "out", "gocache"):
            (work / sub).mkdir(parents=True)
        try:
            j.materialize(build_files, arm_tree, ctx.stock_arm, work / "src")
        except (RuntimeError, OSError) as exc:
            last = exc
            time.sleep(0.5)
            continue
        identity = j.canonical_sha({rel: digest for rel, (_, digest) in build_files.items()})
        arm = Arm(name, tree, files, work, identity, policy)
        arm.source_sha256 = j.canonical_sha(files)
        ctx.arms[name] = arm
        return arm
    raise InputError(f"{name} tree kept changing while it was being snapshotted: {last}")


def go_sandbox(ctx: Ctx, work: Path, unprivileged: bool = False, src: Path | None = None, env: dict | None = None) -> list:
    """The judge's build sandbox + the persistent per-team Go build cache (and optionally a shared source dir)."""
    argv = j.build_sandbox(work, ctx.procs, env, unprivileged=unprivileged)
    at = argv.index("--chdir")
    extra = ["--bind", str(ctx.gocache), "/build/gocache"]
    if src is not None:
        extra += ["--bind", str(src), "/build/src"]
    argv[at:at] = extra
    return argv


def run_plain(argv: list, stdout: Path, stderr: Path, timeout: float) -> dict:
    return j.measured_run(argv, stdout, stderr, timeout)


def bincache_prune() -> None:
    try:
        entries = sorted((RT / "bincache").iterdir(), key=lambda p: p.stat().st_mtime, reverse=True)
        for old in entries[BINCACHE_KEEP:]:
            shutil.rmtree(old, ignore_errors=True)
    except OSError:
        pass


def build_binary(ctx: Ctx, arm: Arm, profile: bool = False, use_cache: bool = True) -> dict:
    """go build ./cmd/restic exactly as the judge (tags, ldflags, trimpath), in the sandbox. Returns
    {ok, binary, seconds, stderr, cache}. Binaries are kept in a content-addressed cache (build input + tags)."""
    tags = j.BUILD_TAGS + (" profile" if profile else "")
    key = sha_text(json.dumps([HARNESS_VERSION, arm.identity, tags, j.GO_VERSION, j.BUILD_LDFLAGS,
                               sha_text(PROFILE_GO) if profile else ""]))
    out_name = "restic-profile" if profile else "restic"
    binary = arm.work / "out" / out_name
    entry = RT / "bincache" / key
    began = time.monotonic()
    if use_cache and (entry / "restic").is_file():
        shutil.copyfile(entry / "restic", binary)
        os.chmod(binary, 0o555)
        os.utime(entry)
        return {"ok": True, "binary": binary, "seconds": time.monotonic() - began, "stderr": "", "cache": "hit"}
    helper = arm.src / "cmd" / "restic" / "zz_rtprof.go"
    if profile:
        helper.write_text(PROFILE_GO)
    argv = go_sandbox(ctx, arm.work) + [GO, "build", "-trimpath", "-buildvcs=false", "-tags", tags,
                                        "-ldflags", j.BUILD_LDFLAGS, "-o", f"/build/out/{out_name}", "./cmd/restic"]
    log = arm.work / f"build-{out_name}"
    try:
        run = run_plain(argv, Path(str(log) + ".stdout"), Path(str(log) + ".stderr"), 900)
    finally:
        if profile:
            helper.unlink(missing_ok=True)
    stderr = tail_text(Path(str(log) + ".stderr"), 20000)
    ok = run["exit_code"] == 0 and binary.is_file()
    if ok:
        os.chmod(binary, 0o555)
        temporary = entry.with_name(f"{entry.name}.partial-{os.getpid()}")
        try:
            shutil.rmtree(temporary, ignore_errors=True)
            temporary.mkdir(parents=True)
            shutil.copyfile(binary, temporary / "restic")
            os.chmod(temporary / "restic", 0o555)
            atomic_json(temporary / "meta.json", {"arm": arm.name, "tree": str(arm.tree), "build_input_sha256": arm.identity,
                                                  "tags": tags, "built_at": j.now(), "team": ctx.team})
            if entry.exists():
                shutil.rmtree(temporary, ignore_errors=True)
            else:
                os.replace(temporary, entry)
        except OSError:
            shutil.rmtree(temporary, ignore_errors=True)
        bincache_prune()
    return {"ok": ok, "binary": binary, "seconds": time.monotonic() - began, "stderr": stderr, "cache": "miss",
            "exit_code": run["exit_code"], "timed_out": run["timed_out"]}


def need_binary(ctx: Ctx, arm: Arm, profile: bool = False) -> Path:
    """Binary of a trusted arm (incumbent, stock): a build failure there is a harness fault."""
    built = build_binary(ctx, arm, profile)
    if not built["ok"]:
        raise RuntimeError(f"{arm.name} does not build: {built['stderr'][-1500:]}")
    return built["binary"]


def candidate_failed_build(result: dict, built: dict) -> dict:
    lines = first_lines(built["stderr"])
    result.update(build_ok=False, errors=lines,
                  summary="The candidate does not build: " + (" | ".join(lines[:3]) or "no compiler output")[:400])
    return result


# ============================================================ restic runs on prepared states
def phase_args(phase: str) -> list:
    return list(j.workload_settings()["phase_args"].get(phase, j.Judge.PHASE_ARGS[phase]))


# ---- remote-backend workloads (judge REMOTE.md). A workload whose frozen workloads.json has a "remote" block is
# reached through restic's REST backend: the workload's own frozen server (simulated latency, rate and connection
# limit) is started before every restic process and stopped after it, outside the sandbox and outside the timed
# region, as Judge.restic does. Its statistics file is where the request and byte counts come from.
def remote_profile() -> dict | None:
    profile = j.workload_settings().get("remote")
    if profile and not hasattr(j, "remote_server_argv"):
        raise RuntimeError("this workload is reached through the REST backend and the judge on this box has no "
                           "remote-backend support yet")
    return profile or None


def remote_launch(argv: list, work: Path, store: Path, stdout: Path, stderr: Path, timeout: float, runner=None,
                  log: Path | None = None) -> dict:
    """One sandboxed restic process against the workload's REST server serving `store` (a local-layout repository
    outside the sandbox's /work). The server is stopped afterwards and its statistics become run['remote']."""
    stats, socket = work / "rest-stats.json", work / j.REMOTE_SOCKET
    for stale in (stats, socket):
        stale.unlink(missing_ok=True)
    # As Judge.restic: the server binds the socket by its relative name from cwd = work (a unix socket path may hold
    # 107 bytes; a tool unit's work dir alone can be longer). Every other path it is given is made absolute.
    server_argv = (j.remote_server_argv(remote_profile(), Path(j.REMOTE_SOCKET), Path(os.path.abspath(store)),
                                        Path(os.path.abspath(stats)))
                   + (["-log", os.path.abspath(log)] if log else []))
    with open(work / "rest-server.stderr", "wb") as server_err:
        server = subprocess.Popen(server_argv, cwd=work, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL,
                                  stderr=server_err)
    server_started = time.monotonic()
    try:
        deadline = time.monotonic() + 20
        while not socket.exists():
            if server.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError("REST server did not start: " + tail_text(work / "rest-server.stderr"))
            time.sleep(0.005)
        launched = time.monotonic()
        run = (runner or j.launcher_run)(argv, stdout, stderr, timeout)
    finally:
        if server.poll() is None:
            server.send_signal(signal.SIGTERM)
        try:
            server.wait(timeout=60)
        except subprocess.TimeoutExpired:
            server.kill()
            server.wait()
    try:
        run["remote"] = j.load(stats)
    except (OSError, ValueError):
        run["remote"] = None
    run["remote_log_offset_s"] = launched - server_started  # server log times - this = seconds since the launch
    return run


def remote_counts(stats: dict) -> dict:
    """The server statistics of one run, in the units a builder reads."""
    return {"requests": stats["requests"], "mib_up": round(stats["bytes_in"] / j.MIB, 1),
            "mib_down": round(stats["bytes_out"] / j.MIB, 1), "latency_s": round(stats["latency_seconds"], 2),
            "queue_s": round(stats.get("queue_seconds", 0.0), 2), "max_in_flight": stats["max_in_flight"],
            "by": {kind: {"requests": c["requests"], "mib_up": round(c["bytes_in"] / j.MIB, 1),
                          "mib_down": round(c["bytes_out"] / j.MIB, 1)} for kind, c in sorted(stats["by"].items())}}


def remote_summary(runs: list, profile: dict | None = None) -> dict | None:
    """Medians over the runs of one arm on one phase (slim() rows): requests, MiB up and down, the latency the
    requests cost (requests x latency, summed over connections), the transfer floor of the bytes moved, and the
    request table by method and file type. None when the runs carry no REST statistics (every local workload)."""
    rows = [run["remote"] for run in runs if run.get("remote")]
    if not rows:
        return None
    out = {key: median([row[key] for row in rows]) for key in ("requests", "mib_up", "mib_down", "latency_s", "queue_s")}
    out["max_in_flight"] = max(row["max_in_flight"] for row in rows)
    out["runs"] = len(rows)
    if profile:
        rate, connections = float(profile["rate_bytes_per_second"]), int(profile["max_connections"])
        out["transfer_floor_s"] = round((out["mib_up"] + out["mib_down"]) * j.MIB / rate / connections, 2) if rate > 0 else 0.0
    kinds = sorted({kind for row in rows for kind in row["by"]})
    out["by_kind"] = {kind: {key: median([(row["by"].get(kind) or {}).get(key, 0) for row in rows])
                             for key in ("requests", "mib_up", "mib_down")} for kind in kinds}
    return out


def remote_note(profile: dict) -> str:
    return (f"REST backend: {profile['latency_ms']:g} ms per request, {profile['rate_bytes_per_second'] / j.MIB:g} MiB/s per "
            f"connection, {profile['max_connections']} connections. latency_s = requests x latency (connection-seconds); "
            "transfer_floor_s = bytes moved / (rate x connections): no code change goes below it without moving fewer "
            "bytes. Request count and how many connections stay busy are the terms a tree can move.")


def remote_sentence(cand: dict | None, ref: dict | None) -> str:
    if not cand or not ref:
        return ""
    return (f"; requests {cand['requests']:g} vs {ref['requests']:g}, MiB up {cand['mib_up']:g} vs {ref['mib_up']:g}, "
            f"down {cand['mib_down']:g} vs {ref['mib_down']:g}")


def restic_run(ctx: Ctx, binary: Path, phase: str, env: dict | None = None, extra: list | None = None,
               wrap: list | None = None, request_log: bool = False) -> tuple:
    """One run of one phase on a private copy of the judge's prepared state. Returns (run row, work dir);
    the caller removes the work dir."""
    bundle = ctx.use_bundle()
    remote = remote_profile()
    state, data = j.Judge.PHASE_STATE[phase]
    work = ctx.scratch / "run"
    if work.exists():
        subprocess.run(["chmod", "-R", "u+rwX", str(work)], stderr=subprocess.DEVNULL)
        shutil.rmtree(work)
    work.mkdir(parents=True)
    for sub in ("home", "tmp"):
        (work / sub).mkdir()
    source = bundle / ("pristine" if state == "pristine" else f"states/{state}")
    shared = store = None
    if phase in READONLY_PHASES:
        shared = ctx.scratch / "state" / state
        if not shared.exists():
            shared.parent.mkdir(exist_ok=True)
            j.copy_tree(source, shared)
            ctx.state_signature[state] = repo_signature(shared)
        if remote is None:
            (work / "repo").mkdir()
    elif remote is None:
        j.copy_tree(source, work / "repo")
    else:  # the repository is the REST server's storage: outside the sandbox's /work until the server has stopped
        store = ctx.scratch / "run.remote-store"
        drop(store)
        j.copy_tree(source, store)
    if remote is None:
        argv = j.restic_sandbox(binary, work, bundle / data if data else None, ctx.procs, repo=shared)
    else:
        argv = j.restic_sandbox(binary, work, bundle / data if data else None, ctx.procs, repository=j.REMOTE_REPOSITORY)
    if env:
        at = argv.index("--")
        for key, value in sorted(env.items()):
            argv[at:at] = ["--setenv", key, value]
    argv = (wrap or []) + argv + (extra or []) + phase_args(phase)
    if remote is None:
        run = j.launcher_run(argv, work / "stdout", work / "stderr", RUN_TIMEOUT)
    else:
        run = remote_launch(argv, work, shared if shared is not None else store, work / "stdout", work / "stderr", RUN_TIMEOUT,
                            log=(work / "rest-requests.jsonl") if request_log else None)
        if store is not None:
            os.rename(store, work / "repo")  # stock reads what the arm wrote as a LOCAL repository (the oracle)
    run["stderr_tail"] = tail_text(work / "stderr", 1200)
    if shared is not None and (run["exit_code"] != 0 or repo_signature(shared) != ctx.state_signature[state]):
        drop(shared)  # the run left the repository changed (stale lock, new file): the next run gets a fresh copy
    return run, work


def repo_signature(repo: Path) -> tuple:
    count = total = 0
    for directory, _, names in os.walk(repo):
        for name in names:
            count += 1
            total += os.lstat(os.path.join(directory, name)).st_size
    return count, total


def drop(work: Path) -> None:
    subprocess.run(["chmod", "-R", "u+rwX", str(work)], stderr=subprocess.DEVNULL)
    shutil.rmtree(work, ignore_errors=True)


def slim(run: dict) -> dict:
    cpu = run["user_seconds"] + run["system_seconds"]
    out = {"seconds": round(run["seconds"], 4), "rss_mib": round(run["peak_rss_bytes"] / j.MIB, 1),
           "exit_code": run["exit_code"], "cores": round(cpu / run["seconds"], 2) if run["seconds"] else None}
    if run.get("remote"):  # remote-backend workload only
        out["remote"] = remote_counts(run["remote"])
    return out


def governor(ctx: Ctx) -> str:
    try:
        return Path(f"/sys/devices/system/cpu/cpu{ctx.cpus[0]}/cpufreq/scaling_governor").read_text().strip()
    except OSError:
        return "unknown"


def pct_saved(reference: float, value: float) -> float:
    return (reference - value) / reference * 100.0 if reference else 0.0


def paired_runs(ctx: Ctx, phase: str, binaries: dict, reps: int, label: str) -> tuple:
    """reps rounds; each round runs every arm once, order rotated per round. Returns (rounds, failure or None)."""
    names = list(binaries)
    rounds = []
    for rep in range(reps):
        order = names[rep % len(names):] + names[:rep % len(names)]
        row = {"rep": rep + 1, "order": order}
        for name in order:
            run, work = restic_run(ctx, binaries[name], phase)
            drop(work)
            row[name] = slim(run)
            if run["exit_code"] != 0 or run["timed_out"]:
                rounds.append(row)
                return rounds, {"arm": name, "exit_code": run["exit_code"], "timed_out": run["timed_out"],
                                "stderr_tail": run["stderr_tail"][-600:]}
        rounds.append(row)
        ctx.progress(f"{label} {phase} rep {rep + 1}/{reps}", done=True)
    return rounds, None


def robust_sigma(values: list, center: float | None = None) -> float:
    """Outlier-proof spread of paired differences: 1.4826 x the median absolute deviation (about `center`, default the
    sample median); tiny samples use the range instead (a MAD of 3 values can be zero by chance)."""
    n = len(values)
    if n < 2:
        return 0.0
    mid = median(values) if center is None else center
    if n in D2 and center is None:
        return (max(values) - min(values)) / D2[n]
    return 1.4826 * median([abs(v - mid) for v in values])


def null_path(ctx: Ctx, phase: str) -> Path:
    return RT / NULL_DIR / f"{ctx.workload}__{phase}__{governor(ctx)}.json"


def read_null(ctx: Ctx, phase: str) -> dict | None:
    """The pre-measured null of this workload + phase + governor (v2 store, then v1's file re-read robustly).
    The noise of a phase belongs to the lane and the workload, not to one binary, so any incumbent's null serves."""
    for path, origin in ((null_path(ctx, phase), "v2"),
                         (RT / "null" / f"{ctx.workload}__{phase}__{governor(ctx)}.json", "v1 pairs, robust re-read")):
        try:
            saved = j.load(path)
        except (OSError, ValueError):
            continue
        if time.time() - saved.get("measured_unix", 0) > NULL_MAX_AGE_S or not saved.get("time_pcts"):
            continue
        return {"time_pair_sigma_pct": round(robust_sigma(saved["time_pcts"], 0.0), 4),
                "rss_pair_sigma_pct": round(robust_sigma(saved.get("rss_pcts") or [0.0, 0.0], 0.0), 4),
                "pairs": len(saved["time_pcts"]), "governor": saved.get("governor"), "measured_at": saved.get("measured_at"),
                "incumbent_median_s": saved.get("incumbent_median_s"), "cache": "hit", "origin": origin}
    return None


def measure_null(ctx: Ctx, phase: str, inc_binary: Path, inc_identity: str) -> dict:
    """Incumbent vs incumbent, NULL_PAIRS pairs, stored for every later bench on this governor. Only `baseline`
    calls this (season start, on a research lane): a builder call never pays for a null."""
    ctx.units_total += NULL_PAIRS
    rounds, failure = paired_runs(ctx, phase, {"a": inc_binary, "b": inc_binary}, NULL_PAIRS, "null (incumbent vs incumbent)")
    if failure:
        raise RuntimeError(f"incumbent failed {phase} while measuring the null: {failure}")
    time_pcts = [pct_saved(row["b"]["seconds"], row["a"]["seconds"]) for row in rounds]
    rss_pcts = [pct_saved(row["b"]["rss_mib"], row["a"]["rss_mib"]) for row in rounds]
    value = {"version": HARNESS_VERSION, "workload": ctx.workload, "phase": phase, "governor": governor(ctx),
             "cpus": ctx.args.cpus, "pairs": NULL_PAIRS, "time_pair_sigma_pct": round(robust_sigma(time_pcts, 0.0), 4),
             "rss_pair_sigma_pct": round(robust_sigma(rss_pcts, 0.0), 4), "time_pcts": [round(v, 3) for v in time_pcts],
             "rss_pcts": [round(v, 3) for v in rss_pcts], "incumbent_build_input_sha256": inc_identity,
             "incumbent_median_s": round(median([row["a"]["seconds"] for row in rounds]), 4),
             "measured_at": j.now(), "measured_unix": time.time()}
    atomic_json(null_path(ctx, phase), value)
    return read_null(ctx, phase)


def band_of(sigma: float, n: int) -> float:
    """Noise band of a median of n pairs: 2 standard errors of the median for a per-pair spread sigma."""
    return round(max(BAND_FLOOR_PCT, 2.0 * 1.2533 * sigma / max(1, n) ** 0.5), 2)


def judge_pairs(time_pcts: list, rss_pcts: list, null: dict | None) -> dict:
    """Verdict of n paired differences. The per-pair spread is the LARGER of the pre-measured null and this call's
    own robust spread, so one wild pair cannot widen the band and a quiet null cannot hide a noisy call."""
    n = len(time_pcts)
    med = median(time_pcts)
    wins = sum(v > 0 for v in time_pcts)
    own = robust_sigma(time_pcts)
    sigma = max(own, null["time_pair_sigma_pct"]) if null else own
    band = band_of(sigma, n)
    need = max(1, -(-n * 4 // 5))  # 80% of the pairs must agree with the sign
    agree = wins if med > 0 else n - wins
    if abs(med) > band and agree >= need:
        verdict = "gain beyond noise" if med > 0 else "loss beyond noise"
        reason = f"median {med:+.2f}% is outside the +/-{band}% band and {agree}/{n} pairs agree"
    elif abs(med) > band:
        verdict = "UNRESOLVED"
        reason = f"median {med:+.2f}% is outside the +/-{band}% band but only {agree}/{n} pairs agree (need {need})"
    else:
        verdict = "UNRESOLVED"
        reason = f"median {med:+.2f}% is inside the +/-{band}% band"
    rss_own = robust_sigma(rss_pcts)
    rss_band = band_of(max(rss_own, null["rss_pair_sigma_pct"]) if null else rss_own, n)
    return {"time_saved_pct": round(med, 2), "rss_saved_pct": round(median(rss_pcts), 2), "wins": wins, "reps": n,
            "noise_band_pct": band, "rss_noise_band_pct": rss_band, "verdict": verdict, "reason": reason,
            "pair_sigma_pct": {"own": round(own, 3), "null": null["time_pair_sigma_pct"] if null else None}}


def pair_row(rep: int, order: list, c: dict, i: dict) -> dict:
    row = {"rep": rep, "first": order[0], "cand_s": c["seconds"], "inc_s": i["seconds"],
           "cand_rss_mib": c["rss_mib"], "inc_rss_mib": i["rss_mib"],
           "time_saved_pct": round(pct_saved(i["seconds"], c["seconds"]), 3),
           "rss_saved_pct": round(pct_saved(i["rss_mib"], c["rss_mib"]), 3)}
    if c.get("remote") and i.get("remote"):
        for prefix, arm in (("cand", c["remote"]), ("inc", i["remote"])):
            row.update({f"{prefix}_requests": arm["requests"], f"{prefix}_mib_up": arm["mib_up"], f"{prefix}_mib_down": arm["mib_down"]})
    return row


def seq_decision(out: dict, n: int, target: int, limit: int, fixed: bool = False) -> str:
    """'' = run another pair, else why the phase stops (the sequential rule, kept apart so it can be tested)."""
    resolved = out["verdict"] != "UNRESOLVED"
    if n >= limit:
        return f"at the limit of {limit} pairs"
    if fixed:
        return ""
    if n < target:
        if resolved and abs(out["time_saved_pct"]) > SEQ_EARLY_MARGIN * out["noise_band_pct"]:
            return f"early: clear after {n} pairs"
        return ""
    if resolved:
        return f"clear after {n} pairs"
    if abs(out["time_saved_pct"]) < SEQ_CONTINUE_RATIO * out["noise_band_pct"]:
        return f"after {n} pairs: too small to resolve here, even at the limit of {limit} pairs"
    return ""


def bench_phase(ctx: Ctx, phase: str, cand: Path, inc: Path, target: int, limit: int, fixed: bool = False,
                names: tuple = ("candidate", "incumbent")) -> dict:
    """Sequential paired bench of one phase: look after SEQ_MIN_PAIRS pairs and after every pair from then on; stop
    as soon as the verdict is clear, at `target` pairs when nothing more can be learned, at `limit` otherwise."""
    began = time.monotonic()
    run, work = restic_run(ctx, cand, phase)  # one unmeasured warm-up (state copy, page cache) = early failure check
    drop(work)
    if run["exit_code"] != 0:
        return {"verdict": "CANDIDATE FAILED", "reps": 0, "exit_code": run["exit_code"],
                "stderr_tail": run["stderr_tail"][-1200:], "seconds": round(time.monotonic() - began, 1)}
    null = read_null(ctx, phase)
    binaries = {names[0]: cand, names[1]: inc}
    pairs, out, stopped = [], None, ""
    runs = {name: [] for name in binaries}
    while not stopped:
        rep = len(pairs)
        order = list(binaries)[rep % 2:] + list(binaries)[:rep % 2]
        row = {}
        for name in order:
            run, work = restic_run(ctx, binaries[name], phase)
            drop(work)
            row[name] = slim(run)
            runs[name].append(row[name])
            if run["exit_code"] != 0 or run["timed_out"]:
                return {"verdict": "CANDIDATE FAILED" if name == names[0] else "INCUMBENT FAILED", "reps": rep,
                        "arm": name, "exit_code": run["exit_code"], "timed_out": run["timed_out"],
                        "stderr_tail": run["stderr_tail"][-1200:], "seconds": round(time.monotonic() - began, 1)}
        pairs.append(pair_row(rep + 1, order, row[names[0]], row[names[1]]))
        n = len(pairs)
        ctx.progress(f"bench {ctx.workload} {phase} pair {n}", done=True)
        if n < min(SEQ_MIN_PAIRS, limit):
            continue
        out = judge_pairs([p["time_saved_pct"] for p in pairs], [p["rss_saved_pct"] for p in pairs], null)
        stopped = seq_decision(out, n, target, limit, fixed)
    out.update(stopped=stopped,
               cand_median_s=round(median([p["cand_s"] for p in pairs]), 3),
               inc_median_s=round(median([p["inc_s"] for p in pairs]), 3),
               cand_median_rss_mib=round(median([p["cand_rss_mib"] for p in pairs]), 1),
               inc_median_rss_mib=round(median([p["inc_rss_mib"] for p in pairs]), 1),
               null=({k: null[k] for k in ("time_pair_sigma_pct", "rss_pair_sigma_pct", "pairs", "governor", "measured_at",
                                           "origin")} if null else
                     {"origin": "none pre-measured for this governor: the band comes from this call's own pairs"}),
               pairs=pairs, seconds=round(time.monotonic() - began, 1))
    profile = remote_profile()
    if profile:  # remote-backend workload: what each arm asked of the link in this phase (medians over its runs)
        out["remote"] = {"cand": remote_summary(runs[names[0]], profile), "ref": remote_summary(runs[names[1]], profile)}
    return out


def phase_sentence(phase: str, row: dict) -> str:
    if row["verdict"].endswith("FAILED"):
        lines = [" ".join(line.split()) for line in (row.get("stderr_tail") or "").splitlines() if line.strip()]
        return f"{phase}: {row['verdict']} (exit {row.get('exit_code')}): {' | '.join(lines[-2:])[:300]}"
    why = f" ({row['reason'].split(' but ')[1]})" if " but only " in row.get("reason", "") else ""
    link = remote_sentence((row.get("remote") or {}).get("cand"), (row.get("remote") or {}).get("ref"))
    return (f"{phase}: {row['time_saved_pct']:+.2f}% time, {row['rss_saved_pct']:+.2f}% memory, {row['wins']}/{row['reps']} wins, "
            f"band +/-{row['noise_band_pct']}% -> {row['verdict']}{why}{link}")


def phase_shares(ctx: Ctx, measured: dict) -> dict | None:
    """Each phase's share of the reference's time on the active workload: this call's own reference medians where it
    has them, the pre-measured nulls (same seconds as the baseline card) for the rest. None if a phase is unknown."""
    seconds = {}
    for phase in j.PHASES:
        row = measured.get(phase) or {}
        if row.get("inc_median_s"):
            seconds[phase] = row["inc_median_s"]
            continue
        null = read_null(ctx, phase)
        if not null or not null.get("incumbent_median_s"):
            return None
        seconds[phase] = null["incumbent_median_s"]
    total = sum(seconds.values())
    return {phase: value / total for phase, value in seconds.items()} if total else None


def worth_line(ctx: Ctx, rows: dict) -> dict:
    """What the measured phase deltas are worth on this workload's time score (the judge's workload score is the
    time saved over the SUM of the seven phases; the suite score is the mean over its workloads)."""
    shares = phase_shares(ctx, rows)
    if shares is None:
        return {"note": "phase time shares are not known yet (no baseline for this workload on this governor)"}
    resolved = point = 0.0
    parts = []
    for phase, row in rows.items():
        if row.get("time_saved_pct") is None:
            continue
        value = row["time_saved_pct"] * shares[phase]
        point += value
        row["share_of_workload_time_pct"] = round(shares[phase] * 100, 1)
        row["worth_on_workload_score"] = round(value, 2)
        if row["verdict"] != "UNRESOLVED":
            resolved += value
        parts.append(f"{phase} {row['time_saved_pct']:+.2f}% x {shares[phase] * 100:.0f}% of the time = {value:+.2f}")
    return {"resolved_points": round(resolved, 2), "point_estimate": round(point, 2), "parts": parts,
            "text": (f"worth on the {ctx.workload} score: {resolved:+.2f} points from the resolved phases "
                     f"({point:+.2f} if every reading is taken at face value; phases not benched count as 0): "
                     + "; ".join(parts))}


# ============================================================ op: build
def op_build(ctx: Ctx, result: dict) -> dict:
    ctx.units_total = 2
    cand = prepare_arm(ctx, "candidate", ctx.args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    inc_files = j.tree_files(Path(ctx.args.incumbent).resolve(), j.SOURCE_EXCLUDED_DIRS)
    changed = cand.promotable_changed_vs(Arm("incumbent", None, inc_files, ctx.scratch, "", {}))
    result["changed_files_vs_incumbent"] = changed[:60]
    built = build_binary(ctx, cand, use_cache=False)
    ctx.progress("go build ./cmd/restic", done=True)
    result.update(build_ok=built["ok"], build_seconds=round(built["seconds"], 1), vet_ok=None, errors=[],
                  policy_violations=cand.policy["violations"])
    if not built["ok"]:
        return candidate_failed_build(result, built)
    packages = sorted({"./" + rel.rsplit("/", 1)[0] for rel in changed if (cand.src / rel).is_file()})
    vet_note = ""
    if not packages:
        result.update(vet_ok=True, vet_packages=[])
        vet_note = "no promotable file differs from the incumbent, so nothing was vetted"
    else:
        if len(packages) > 30:
            packages = ["./cmd/...", "./internal/..."]
        began = time.monotonic()
        argv = go_sandbox(ctx, cand.work) + [GO, "vet", "-tags", j.BUILD_TAGS, *packages]
        run = run_plain(argv, cand.work / "vet.stdout", cand.work / "vet.stderr", 900)
        vet_err = tail_text(cand.work / "vet.stderr", 20000)
        result.update(vet_ok=run["exit_code"] == 0, vet_packages=packages, vet_seconds=round(time.monotonic() - began, 1))
        if run["exit_code"] != 0:
            result["errors"] = first_lines(vet_err)
        vet_note = f"go vet (with the stock tests overlaid) on {len(packages)} changed package(s)"
    ctx.progress("go vet", done=True)
    problems = []
    if cand.policy["violations"]:
        problems.append("POLICY: " + "; ".join(cand.policy["violations"]))
    if result["vet_ok"] is False:
        problems.append("go vet FAILED: " + " | ".join(result["errors"][:3])[:400])
    if problems:
        result["summary"] = "Build OK, but " + " ".join(problems)
    else:
        result["summary"] = f"Build OK in {result['build_seconds']} s; {vet_note}: clean."
    return result


# ============================================================ op: test
def parse_go_test(paths: list) -> dict:
    counts = collections.defaultdict(lambda: collections.Counter())
    outputs = collections.defaultdict(lambda: collections.deque(maxlen=80))
    package_fail, build_output, garbage = set(), [], []
    for path in paths:
        for line in Path(path).read_text(errors="replace").splitlines():
            try:
                event = json.loads(line)
            except ValueError:
                if line.strip():
                    garbage.append(line)
                continue
            action, package, test = event.get("Action"), event.get("Package") or event.get("ImportPath") or "", event.get("Test")
            if action in ("pass", "fail", "skip") and test:
                counts[f"{package}::{test}"][action] += 1
            elif action == "fail" and not test:
                package_fail.add(package)
            elif action == "output":
                outputs[f"{package}::{test}" if test else f"{package}::<package>"].append(event.get("Output", ""))
            elif action == "build-output":
                build_output.append(event.get("Output", ""))
    return {"counts": counts, "outputs": outputs, "package_fail": package_fail, "build_output": "".join(build_output),
            "garbage": garbage}


def short(name: str) -> str:
    return name[len(MODULE):] if name.startswith(MODULE) else name


def parse_packages(text: str | None) -> list:
    if not text:
        raise InputError("--packages is required (comma separated, e.g. internal/restorer,internal/repository)")
    packages = []
    for item in text.split(","):
        item = item.strip()
        if item.startswith("./"):
            item = item[2:]
        item = item.strip("/")
        if not item:
            continue
        if not re.fullmatch(r"[A-Za-z0-9_./-]+", item) or ".." in item or item.split("/")[0] not in ("cmd", "internal"):
            raise InputError(f"invalid package {item!r}: name packages under cmd/ or internal/, e.g. internal/restorer")
        if item not in packages:
            packages.append(item)
    return packages


def overlay_own_tests(ctx: Ctx, arm: Arm) -> list:
    """Compile in the arm's OWN new *_test.go files (names stock does not have) under cmd/ and internal/. Stock test
    files stay stock: a candidate cannot replace one."""
    added = []
    for rel in sorted(arm.files):
        if not rel.endswith("_test.go") or rel.split("/")[0] not in ("cmd", "internal") or rel in ctx.stock_arm.files:
            continue
        target = arm.src / rel
        if not target.parent.is_dir():
            continue
        try:
            shutil.copyfile(arm.tree / rel, target)
            added.append(rel)
        except OSError:
            continue
    return added


def op_test(ctx: Ctx, result: dict) -> dict:
    args = ctx.args
    packages = parse_packages(args.packages)
    if args.run:
        try:
            re.compile(args.run)
        except re.error as exc:
            raise InputError(f"--run is not a valid regular expression: {exc}")
    count = min(max(1, args.count), 20)
    copies = min(max(1, args.parallel_copies), 4)
    ctx.units_total = 1 + copies
    cand = prepare_arm(ctx, "candidate", args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    missing = [p for p in packages if not (cand.src / p).is_dir()]
    if missing:
        raise InputError(f"package(s) not in the tree: {', '.join(missing)}")
    own = overlay_own_tests(ctx, cand) if args.own_tests else []
    ctx.progress("snapshot + stock test overlay" + (f" + {len(own)} own test file(s)" if own else ""), done=True)
    go_args = [GO, "test", f"-count={count}", "-json", "-p", str(ctx.procs), "-tags", j.BUILD_TAGS, "-timeout", TEST_TIMEOUT]
    if args.run:
        go_args += ["-run", args.run]
    go_args += ["./" + p for p in packages]
    processes, outs = [], []
    for copy in range(copies):
        work = ctx.scratch / "test" / f"copy{copy}"
        for sub in ("home", "tmp", "gocache", "src"):
            (work / sub).mkdir(parents=True)
        out = work / "test.jsonl"
        argv = go_sandbox(ctx, work, unprivileged=True, src=cand.src) + go_args
        handle = open(out, "wb")
        err = open(work / "test.stderr", "wb")
        processes.append((subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=handle, stderr=err, start_new_session=True),
                          handle, err, work))
        outs.append(out)
    deadline = time.monotonic() + TEST_KILL_S
    timed_out = False
    stderr_text = ""
    for process, handle, err, work in processes:
        try:
            process.wait(timeout=max(1, deadline - time.monotonic()))
        except subprocess.TimeoutExpired:
            timed_out = True
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()
        handle.close()
        err.close()
        stderr_text += tail_text(work / "test.stderr", 4000)
        ctx.progress("go test copy finished", done=True)
    parsed = parse_go_test(outs)
    try:
        baseline = j.load(ROOT / "compat-baseline.json")["results"]
    except (OSError, ValueError, KeyError):
        baseline = {}
    stock_failed = {k for k, v in baseline.items() if v == "fail"}
    counts = parsed["counts"]
    failing = sorted(k for k, c in counts.items() if c["fail"])
    tolerated = [k for k in failing if k in stock_failed]
    failed = [k for k in failing if k not in stock_failed]
    packages_with_failed_tests = {k.split("::")[0] for k in failing}
    for package in sorted(parsed["package_fail"]):
        if package not in packages_with_failed_tests:  # build failure, panic, timeout or os.Exit in a package
            failed.append(f"{package}::<package did not complete>")
    passed = sum(1 for k, c in counts.items() if c["pass"] and not c["fail"])
    skipped = sum(1 for k, c in counts.items() if c["skip"] and not c["pass"] and not c["fail"])
    chunks = []
    if parsed["build_output"]:
        chunks.append(parsed["build_output"])
    for key in failed:
        lookup = key if key in parsed["outputs"] else key.split("::")[0] + "::<package>"
        chunks.append(f"=== {short(key)}\n" + "".join(parsed["outputs"].get(lookup, [])))
    if not counts and not parsed["package_fail"]:
        chunks.append(stderr_text + "\n".join(parsed["garbage"][-40:]))
    if args.verbose:
        for key in sorted(counts):
            if key in failed:
                continue
            lines = [line for line in parsed["outputs"].get(key, [])
                     if not line.lstrip().startswith(("=== RUN", "=== PAUSE", "=== CONT", "=== NAME"))]
            if lines:
                chunks.append(f"=== {short(key)}\n" + "".join(lines))
    output_tail = "\n".join(chunks)[-6000:]
    exit_codes = [process.returncode for process, _, _, _ in processes]
    no_match = (not counts and not parsed["package_fail"] and not parsed["build_output"] and not timed_out
                and all(code == 0 for code in exit_codes))
    build_failed = bool(parsed["build_output"]) or (not counts and not parsed["package_fail"] and not no_match)
    result.update(passed=passed, failed=[short(k) for k in failed], skipped=skipped,
                  failed_counts={short(k): f"{counts[k]['fail']} of {sum(counts[k].values())} runs" for k in failing if k in failed},
                  tolerated_stock_failures=[short(k) for k in tolerated], packages=packages, run=args.run, count=count,
                  parallel_copies=copies, timed_out=timed_out, build_failed=build_failed, output_tail=output_tail,
                  no_test_matched=no_match, own_test_files=own)
    scope = ", ".join(packages) + (f" -run {args.run}" if args.run else "") + (f", count {count}" if count > 1 else "") + \
        (f", {copies} parallel copies" if copies > 1 else "")
    which = "stock tests" + (f" + your {len(own)} own test file(s)" if own else "")
    if no_match:
        result["summary"] = (f"No test matched ({scope}); nothing failed to compile. run_tests runs the STOCK tests of the "
                             "named packages" + (f" plus your own new *_test.go files ({len(own)} compiled in)" if args.own_tests
                                                 else "; your own new *_test.go files are compiled in only with own_tests=true")
                             + ". Check the -run regex and the package list.")
    elif timed_out:
        result["summary"] = f"Tests TIMED OUT ({scope}); {passed} passed, {len(failed)} failed before the kill."
    elif build_failed and not passed:
        result["summary"] = (f"The {which} do not compile against the candidate ({scope}): "
                             + (" | ".join(first_lines(parsed["build_output"] or stderr_text, 3))[:400]
                                or "go test wrote no compiler output; see output_tail"))
    elif failed:
        result["summary"] = f"{len(failed)} test(s) FAILED ({scope}): " + ", ".join(result["failed"][:6]) + \
            (" ..." if len(failed) > 6 else "") + f"; {passed} passed."
    elif not passed:
        result["summary"] = f"No test ran ({scope}): check --run and --packages."
    else:
        result["summary"] = f"All {passed} tests passed ({which}; {scope})" + \
            (f"; {len(tolerated)} known sandbox failure(s) of stock ignored." if tolerated else ".")
    return result


# ============================================================ op: bench
def parse_phases(text: str | None, default: list | None = None) -> list:
    if not text:
        if default is None:
            raise InputError("--phases is required: 1-7 of " + ",".join(j.PHASES))
        return default
    phases = [p.strip() for p in text.split(",") if p.strip()]
    bad = [p for p in phases if p not in j.PHASES]
    if bad or not phases or len(set(phases)) != len(phases):
        raise InputError(f"unknown or repeated phase {bad or phases}; the judge's phases are " + ",".join(j.PHASES))
    return phases


def parse_workloads(ctx: Ctx) -> list:
    text = ctx.args.workloads
    if not text:
        return [ctx.args.workload]
    names = [w.strip() for w in text.split(",") if w.strip()]
    bad = [w for w in names if w not in WORKLOAD_CHOICES]
    if bad or not names or len(set(names)) != len(names):
        raise InputError(f"unknown or repeated workload {bad or names}; the workloads are " + ", ".join(WORKLOAD_CHOICES))
    return names


def reference_arm(ctx: Ctx, result: dict) -> tuple:
    """The tree the candidate is compared with: the season incumbent, or (--versus base) the tree this workspace
    started the match from, which isolates what the builder added on top of a retained base."""
    if ctx.args.versus == "base":
        if not ctx.args.base:
            raise InputError("versus=base needs the match's starting source and none is recorded for this workspace; "
                             "use versus=incumbent")
        arm, label = prepare_arm(ctx, "base", ctx.args.base), "base (the source this match started from)"
    else:
        arm, label = prepare_arm(ctx, "incumbent", ctx.args.incumbent), "incumbent"
    result["versus"] = label
    return arm, label


def op_bench(ctx: Ctx, result: dict) -> dict:
    phases = parse_phases(ctx.args.phases)
    workloads = parse_workloads(ctx)
    asked = ctx.args.reps
    limit = min(max(1, asked), SEQ_HARD_MAX) if asked else SEQ_MAX_PAIRS
    target = min(SEQ_TARGET_PAIRS, limit)
    notes = []
    if asked and asked > SEQ_HARD_MAX:
        notes.append(f"reps {asked} is above the limit; {SEQ_HARD_MAX} used")
    ctx.units_total = 2 + len(workloads) * len(phases) * target
    cand = prepare_arm(ctx, "candidate", ctx.args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    ref, label = reference_arm(ctx, result)
    built = build_binary(ctx, cand)
    ctx.progress(f"candidate build ({built['cache']})", done=True)
    if not built["ok"]:
        result["phases"] = {}
        return candidate_failed_build(result, built)
    ref_binary = need_binary(ctx, ref)
    ctx.progress(f"{ref.name} build", done=True)
    result.update(reps=limit, reps_target=target, reps_limit=limit, governor=governor(ctx),
                  identical_build_input=cand.identity == ref.identity, phases={}, workloads={}, notes=notes)
    sentences = []
    for workload in workloads:
        ctx.switch_workload(workload)
        rows = {}
        for phase in phases:
            rows[phase] = bench_phase(ctx, phase, built["binary"], ref_binary, target, limit, fixed=ctx.args.fixed_reps)
        worth = worth_line(ctx, rows)
        result["workloads"][workload] = {"phases": rows, "worth": worth}
        if remote_profile():
            result["workloads"][workload]["remote_profile"] = dict(remote_profile(), note=remote_note(remote_profile()))
            result["remote_profile"] = result["workloads"][workload]["remote_profile"]
            notes.append(f"{workload} is reached through a simulated link; requests and MiB are candidate vs {ref.name}. "
                         + remote_note(remote_profile()).rstrip("."))
        line = (f"[{workload}] " if len(workloads) > 1 else "") + "; ".join(phase_sentence(p, row) for p, row in rows.items())
        if worth.get("text"):
            line += ". W" + worth["text"][1:]
        sentences.append(line)
    if len(workloads) == 1:
        result["phases"] = result["workloads"][workloads[0]]["phases"]
        result["worth"] = result["workloads"][workloads[0]]["worth"]
    else:
        result["phases"] = {f"{w}/{p}": row for w, entry in result["workloads"].items() for p, row in entry["phases"].items()}
        result["workload"] = ",".join(workloads)
    pairs = sum(row.get("reps") or 0 for row in result["phases"].values())
    if cand.identity == ref.identity:
        notes.append(f"the candidate's build input is identical to the {ref.name}'s: every delta here is noise")
    result["summary"] = (" | ".join(sentences) + f" (candidate vs {label}; paired, each phase stops when its verdict is clear: "
                         f"{pairs} pairs in all, limit {limit} per phase; {governor(ctx)} governor; diagnostic, the judge "
                         "alone scores)." + ("".join(" Note: " + note + "." for note in notes)))
    return result


# ============================================================ op: profile
UNIT_S = {"s": 1.0, "ms": 1e-3, "us": 1e-6, "µs": 1e-6, "ns": 1e-9, "m": 60.0, "min": 60.0, "mins": 60.0, "h": 3600.0, "hrs": 3600.0}
UNIT_MIB = {"B": 1 / 2**20, "kB": 1 / 1024, "MB": 1.0, "GB": 1024.0, "TB": 1024.0 * 1024}


def parse_quantity(text: str, units: dict) -> float:
    match = re.fullmatch(r"(-?[0-9.]+)([A-Za-zµ]*)", text)
    if not match:
        return 0.0
    return float(match.group(1)) * units.get(match.group(2), 1.0 if not match.group(2) else 0.0)


def pprof_top(ctx: Ctx, profile: Path, units: dict, sample_index: str | None = None, nodes: int = 400) -> dict:
    """go tool pprof -top in the sandbox (the profile is data written by the arm). {function: (flat, cum)}, total."""
    work = ctx.scratch / "pprof"
    shutil.rmtree(work, ignore_errors=True)
    for sub in ("src", "home", "tmp", "gocache", "in"):
        (work / sub).mkdir(parents=True)
    shutil.copyfile(profile, work / "in" / "profile")
    argv = go_sandbox(ctx, work) + [GO, "tool", "pprof", "-top", f"-nodecount={nodes}"]
    if sample_index:
        argv.append(f"-sample_index={sample_index}")
    argv.append("/build/in/profile")
    run = run_plain(argv, work / "top.txt", work / "top.err", 300)
    text = (work / "top.txt").read_text(errors="replace")
    if run["exit_code"] != 0:
        raise RuntimeError("go tool pprof failed: " + tail_text(work / "top.err", 800))
    rows, total, started = {}, None, False
    for line in text.splitlines():
        if not started:
            match = re.search(r"of ([0-9.]+[A-Za-zµ]*) total", line)
            if match:
                total = parse_quantity(match.group(1), units)
            started = line.strip().startswith("flat") and "cum" in line
            continue
        fields = line.split(None, 5)
        if len(fields) == 6:
            rows[fields[5].strip()] = (parse_quantity(fields[0], units), parse_quantity(fields[3], units))
    return {"rows": rows, "total": total}


def parse_gctrace(stderr: Path) -> dict:
    count, pause_ms, last_pct = 0, 0.0, None
    for line in stderr.read_text(errors="replace").splitlines():
        match = re.match(r"gc (\d+) @[0-9.]+s (\d+)%: ([0-9.]+)\+([0-9.]+)\+([0-9.]+) ms clock", line)
        if match:
            count += 1
            pause_ms += float(match.group(3)) + float(match.group(5))
            last_pct = int(match.group(2))
    return {"count": count, "pause_total_ms": round(pause_ms, 2), "gc_cpu_pct_at_end": last_pct}


def parse_strace(path: Path) -> dict:
    calls = {}
    for line in path.read_text(errors="replace").splitlines():
        fields = line.split()
        if len(fields) in (5, 6) and fields[0].replace(".", "").isdigit() and fields[-1] != "total":
            try:
                calls[fields[-1]] = int(fields[3])
            except ValueError:
                pass
    return calls


def instrumented(ctx: Ctx, phase: str, plain: Path, prof: Path, label: str, syscalls: bool = True) -> dict:
    """Three instrumented runs of one arm on one phase: CPU profile + GC trace, heap-at-peak sampler, strace -c."""
    out = {}
    run, work = restic_run(ctx, prof, phase, env={"GODEBUG": "gctrace=1"}, extra=["--cpu-profile", "/work/prof"])
    try:
        if run["exit_code"] != 0:
            return {"failed": {"run": "cpu-profile", "exit_code": run["exit_code"], "stderr_tail": run["stderr_tail"][-600:]}}
        out["wall_s"] = round(run["seconds"], 3)
        out["cpu_s"] = round(run["user_seconds"] + run["system_seconds"], 3)
        out["cores_used"] = round(out["cpu_s"] / run["seconds"], 2)
        out["rss_mib"] = round(run["peak_rss_bytes"] / j.MIB, 1)
        if run.get("remote"):
            out["remote"] = remote_summary([slim(run)], remote_profile())
        out["gc"] = parse_gctrace(work / "stderr")
        profiles = sorted((work / "prof").glob("*.pprof")) if (work / "prof").is_dir() else []
        out["cpu"] = pprof_top(ctx, profiles[0], UNIT_S) if profiles else {"rows": {}, "total": None}
    finally:
        drop(work)
    ctx.progress(f"{label} {phase}: CPU profile + GC trace", done=True)
    run, work = restic_run(ctx, prof, phase, env={"RT_PROF_DIR": "/work/rtprof"})
    try:
        heap = work / "rtprof" / "peak-heap.pb.gz"
        if run["exit_code"] == 0 and heap.is_file():
            out["heap"] = pprof_top(ctx, heap, UNIT_MIB, "inuse_space", 60)
            try:
                stats = j.load(work / "rtprof" / "peak-memstats.json")
                out["heap_stats"] = {"rss_mib_at_dump": round(stats["rss"] / j.MIB, 1), "at_s": round(stats["t"], 2),
                                     "heap_inuse_mib": round(stats["HeapInuse"] / j.MIB, 1),
                                     "heap_idle_not_released_mib": round((stats["HeapIdle"] - stats["HeapReleased"]) / j.MIB, 1),
                                     "stack_mib": round(stats["StackInuse"] / j.MIB, 1), "go_sys_mib": round(stats["Sys"] / j.MIB, 1)}
            except (OSError, ValueError, KeyError):
                pass
        else:
            out["heap"] = {"rows": {}, "total": None}
    finally:
        drop(work)
    ctx.progress(f"{label} {phase}: heap at peak", done=True)
    if syscalls:
        trace = ctx.scratch / "strace.txt"
        run, work = restic_run(ctx, plain, phase, wrap=["/usr/bin/strace", "-f", "-c", "-S", "calls", "-o", str(trace)])
        drop(work)
        out["syscalls"] = parse_strace(trace) if trace.is_file() else {}
        trace.unlink(missing_ok=True)
        ctx.progress(f"{label} {phase}: syscall counts", done=True)
    return out


def diff_rows(cand: dict, inc: dict, top: int, key: str, digits: int, index: int = 0, only: str | None = None) -> list:
    names = set(cand) | set(inc)
    if only:
        names = {n for n in names if only in n}
    value = lambda table, name: (table.get(name) or (0.0, 0.0))[index]  # noqa: E731
    rows = sorted(names, key=lambda n: max(value(cand, n), value(inc, n)), reverse=True)[:top]
    return [{"function": n, f"cand_{key}": round(value(cand, n), digits), f"inc_{key}": round(value(inc, n), digits),
             f"delta_{key}": round(value(cand, n) - value(inc, n), digits)} for n in rows]


def op_profile(ctx: Ctx, result: dict) -> dict:
    phase = ctx.args.phase
    if phase not in j.PHASES:
        raise InputError("--phase must be one of " + ",".join(j.PHASES))
    top = min(max(3, ctx.args.top), 60)
    ctx.units_total = 2 + 6
    cand = prepare_arm(ctx, "candidate", ctx.args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    inc = prepare_arm(ctx, "incumbent", ctx.args.incumbent)
    built = build_binary(ctx, cand)
    if not built["ok"]:
        return candidate_failed_build(result, built)
    built_prof = build_binary(ctx, cand, profile=True)
    if not built_prof["ok"]:
        return candidate_failed_build(result, built_prof)
    ctx.progress("candidate builds (plain + profile)", done=True)
    inc_plain, inc_prof = need_binary(ctx, inc), need_binary(ctx, inc, profile=True)
    ctx.progress("incumbent builds (plain + profile)", done=True)
    c = instrumented(ctx, phase, built["binary"], built_prof["binary"], "candidate")
    if "failed" in c:
        result.update(phase=phase, failed=c["failed"],
                      summary=f"The candidate FAILED {phase} (exit {c['failed']['exit_code']}): {' '.join(c['failed']['stderr_tail'].split())[-300:]}")
        return result
    i = instrumented(ctx, phase, inc_plain, inc_prof, "incumbent")
    if "failed" in i:
        raise RuntimeError(f"incumbent failed the instrumented {phase}: {i['failed']}")
    cpu_top = diff_rows(c["cpu"]["rows"], i["cpu"]["rows"], top, "s", 3)
    movers = sorted(diff_rows(c["cpu"]["rows"], i["cpu"]["rows"], 400, "s", 3), key=lambda r: abs(r["delta_s"]), reverse=True)[:8]
    own = diff_rows(c["cpu"]["rows"], i["cpu"]["rows"], top, "cum_s", 3, index=1, only=MODULE)
    heap = [{"owner": r["function"], "cand_mib": r["cand_mib"], "inc_mib": r["inc_mib"], "delta_mib": r["delta_mib"]}
            for r in diff_rows(c["heap"]["rows"], i["heap"]["rows"], 12, "mib", 2)]
    names = sorted(set(c["syscalls"]) | set(i["syscalls"]), key=lambda n: max(c["syscalls"].get(n, 0), i["syscalls"].get(n, 0)),
                   reverse=True)[:14]
    syscalls = [{"syscall": n, "cand": c["syscalls"].get(n, 0), "inc": i["syscalls"].get(n, 0),
                 "delta": c["syscalls"].get(n, 0) - i["syscalls"].get(n, 0)} for n in names]
    result.update(
        phase=phase, cpu_top=cpu_top, cpu_top_movers=movers, cpu_top_restic_cumulative=own,
        cpu_total_s={"cand": r2(c["cpu"]["total"], 3), "inc": r2(i["cpu"]["total"], 3)},
        heap_at_peak={"owners": heap, "cand_total_mib": r2(c["heap"]["total"]), "inc_total_mib": r2(i["heap"]["total"]),
                      "cand": c.get("heap_stats"), "inc": i.get("heap_stats")},
        gc={"cand": c["gc"], "inc": i["gc"]}, syscalls=syscalls,
        syscalls_total={"cand": sum(c["syscalls"].values()), "inc": sum(i["syscalls"].values())},
        cores_used={"cand": c["cores_used"], "inc": i["cores_used"]},
        wall_s={"cand": c["wall_s"], "inc": i["wall_s"]}, process_cpu_s={"cand": c["cpu_s"], "inc": i["cpu_s"]},
        rss_mib={"cand": c["rss_mib"], "inc": i["rss_mib"]},
        note="one instrumented run per arm: timings include profiling overhead, use quick_bench for deltas")
    if c.get("remote"):
        result["remote"] = {"cand": c["remote"], "inc": i.get("remote")}
        result["remote_profile"] = dict(remote_profile(), note=remote_note(remote_profile()))
    lead = cpu_top[0] if cpu_top else None
    mover = movers[0] if movers else None
    result["summary"] = (
        f"{phase}: CPU {c['cpu_s']} s vs incumbent {i['cpu_s']} s on {c['cores_used']} vs {i['cores_used']} cores"
        + (f"; biggest cost {lead['function']} ({lead['cand_s']} s vs {lead['inc_s']} s)" if lead else "")
        + (f"; biggest change {mover['function']} ({mover['delta_s']:+.3f} s)" if mover and abs(mover["delta_s"]) > 0 else "")
        + f"; GC {c['gc']['count']} vs {i['gc']['count']} cycles; syscalls {result['syscalls_total']['cand']} vs "
          f"{result['syscalls_total']['inc']}; heap at peak {r2(c['heap']['total'])} vs {r2(i['heap']['total'])} MiB"
        + remote_sentence(c.get("remote"), i.get("remote")) + ".")
    return result


# ============================================================ op: sweep
def op_sweep(ctx: Ctx, result: dict) -> dict:
    args = ctx.args
    phases = parse_phases(args.phases)
    reps = min(max(1, args.reps or 3), 9)
    if not args.file or not args.pattern or not args.values:
        raise InputError("sweep needs --file, --pattern (one capture group) and --values a,b,c")
    rel = args.file.strip().lstrip("./")
    if ".." in rel or not rel.endswith(".go") or rel.endswith("_test.go") or rel.split("/")[0] not in ("cmd", "internal"):
        raise InputError("--file must be a non-test .go file under cmd/ or internal/")
    try:
        pattern = re.compile(args.pattern)
    except re.error as exc:
        raise InputError(f"--pattern is not a valid regular expression: {exc}")
    if pattern.groups != 1:
        raise InputError("--pattern needs exactly one capture group around the text to replace")
    values = [v.strip() for v in args.values.split(",") if v.strip()]
    if not 1 <= len(values) <= 6 or len(set(values)) != len(values):
        raise InputError("--values takes 1 to 6 distinct values")
    cand = prepare_arm(ctx, "candidate", args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    target = cand.src / rel
    if not target.is_file():
        raise InputError(f"{rel} is not in the candidate tree")
    text = target.read_text()
    matches = list(pattern.finditer(text))
    if len(matches) != 1:
        raise InputError(f"--pattern matched {len(matches)} times in {rel}; it must match exactly once")
    current = matches[0].group(1)
    start, end = matches[0].span(1)
    inc = prepare_arm(ctx, "incumbent", args.incumbent)
    inc_binary = need_binary(ctx, inc)
    ctx.units_total = 1 + len(values) + len(phases) * reps * 2
    ctx.progress("incumbent build", done=True)
    binaries, rows = {}, {}
    for value in values:
        target.write_text(text[:start] + value + text[end:])
        variant = Arm(f"v{len(binaries)}", cand.tree, cand.files, cand.work,
                      sha_text(json.dumps([cand.identity, rel, start, end, value])), cand.policy)
        built = build_binary(ctx, variant)
        if built["ok"]:
            kept = cand.work / "out" / f"restic-v{len(binaries)}"
            os.replace(built["binary"], kept)
            binaries[value] = kept
            rows[value] = {"value": value}
        else:
            rows[value] = {"value": value, "verdict": "DOES NOT BUILD", "errors": first_lines(built["stderr"], 6)}
        ctx.progress(f"variant {value} built ({'ok' if built['ok'] else 'FAILED'})", done=True)
    target.write_text(text)
    result.update(file=rel, pattern=args.pattern, current_value=current, phases=phases, reps_per_round=reps,
                  governor=governor(ctx))
    if not binaries:
        result.update(values=list(rows.values()), recommended=None,
                      summary="No value builds: " + " | ".join(next(iter(rows.values())).get("errors", [])[:2])[:300])
        return result
    nulls = {phase: read_null(ctx, phase) for phase in phases}
    rounds = {value: [] for value in binaries}  # per value: list of {phase: (time_pct, rss_pct, cand_s, inc_s)}

    def measure(active: list, round_name: str) -> None:
        arms = {"incumbent": inc_binary, **{f"value {v}": binaries[v] for v in active}}
        per_phase = {}
        for phase in phases:
            got, failure = paired_runs(ctx, phase, arms, reps, f"sweep {round_name}")
            if failure:
                value = failure["arm"].removeprefix("value ")
                if failure["arm"] == "incumbent":
                    raise RuntimeError(f"incumbent failed {phase} during the sweep: {failure}")
                rows[value].update(verdict="FAILED", failure=failure)
                binaries.pop(value, None)
                got = [row for row in got if all(name in row for name in arms)]
            per_phase[phase] = got
        for value in active:
            if value not in binaries:
                continue
            for index in range(reps):
                entry = {}
                for phase in phases:
                    if index < len(per_phase[phase]):
                        row = per_phase[phase][index]
                        entry[phase] = (row[f"value {value}"], row["incumbent"])
                if len(entry) == len(phases):
                    rounds[value].append(entry)

    def score(value: str) -> dict:
        time_pcts, rss_pcts = [], []
        for entry in rounds[value]:
            cand_s = sum(c["seconds"] for c, _ in entry.values())
            inc_s = sum(i["seconds"] for _, i in entry.values())
            time_pcts.append(pct_saved(inc_s, cand_s))
            rss_pcts.append(pct_saved(max(i["rss_mib"] for _, i in entry.values()), max(c["rss_mib"] for c, _ in entry.values())))
        # spread of the summed time: phase spreads weighted by each phase's share of the incumbent's time
        null = None
        if all(nulls[p] and nulls[p].get("incumbent_median_s") for p in phases):
            total = sum(nulls[p]["incumbent_median_s"] for p in phases)
            sigma = sum((nulls[p]["time_pair_sigma_pct"] * nulls[p]["incumbent_median_s"] / total) ** 2 for p in phases) ** 0.5
            null = {"time_pair_sigma_pct": round(sigma, 4), "rss_pair_sigma_pct": max(nulls[p]["rss_pair_sigma_pct"] for p in phases)}
        out = judge_pairs(time_pcts, rss_pcts, null)
        out.pop("pair_sigma_pct", None)
        out["per_phase_time_saved_pct"] = {
            p: round(median([pct_saved(e[p][1]["seconds"], e[p][0]["seconds"]) for e in rounds[value]]), 2) for p in phases}
        return out

    measure(list(binaries), "round 1")
    scored = {value: score(value) for value in binaries if rounds[value]}
    if scored:
        best = max(v["time_saved_pct"] for v in scored.values())
        survivors = [value for value, s in scored.items() if s["time_saved_pct"] >= best - s["noise_band_pct"]]
        dropped = [value for value in scored if value not in survivors]
        if len(scored) > 1 and survivors:
            measure(survivors, "round 2 (survivors)")
            scored.update({value: score(value) for value in survivors if value in binaries})
        for value, s in scored.items():
            rows[value].update(s, dropped_after_round_1=value in dropped)
    alive = {value: s for value, s in scored.items() if value in binaries}
    recommended, note = None, ""
    if alive:
        best_value = max(alive, key=lambda v: alive[v]["time_saved_pct"])
        best = alive[best_value]
        near = [v for v, s in alive.items() if s["time_saved_pct"] >= best["time_saved_pct"] - best["noise_band_pct"]]

        def numeric(v):
            try:
                return (0, float(v))
            except ValueError:
                return (1, values.index(v))
        recommended = min(near, key=numeric)
        note = (f"best measured {best_value} ({best['time_saved_pct']:+.2f}% vs incumbent, band +/-{best['noise_band_pct']}%); "
                f"{len(near)} value(s) within noise of it: {', '.join(near)}; current value in the tree is {current}. "
                "time_saved_pct is on the summed time of the named phases; rss on the largest phase peak.")
    result.update(values=[rows[v] for v in values], recommended=recommended, note=note)
    if recommended is None:
        result["summary"] = f"No value of {rel} survived: " + ", ".join(f"{v}: {rows[v].get('verdict')}" for v in values)
    else:
        result["summary"] = (f"{rel}: recommended {recommended} (the smallest value within noise of the best; current {current}). "
                             + "; ".join(f"{v}: {rows[v]['time_saved_pct']:+.2f}% {rows[v]['verdict']}" if "time_saved_pct" in rows[v]
                                         else f"{v}: {rows[v].get('verdict')}" for v in values)
                             + f" (vs incumbent on {'+'.join(phases)}).")
    return result


# ============================================================ op: race
RACE_MARKS = ("snapshots after index", "concurrent map", "not found in repository", "panic:", "--- FAIL", "fatal error")


def race_lines(text: str) -> str:
    text = re.sub(chr(27) + r"\[[0-9;]*m", "", text)
    hits = [line.strip() for line in text.splitlines() if any(mark in line for mark in RACE_MARKS)]
    return " | ".join(dict.fromkeys(hits))[:400] or text.strip()[-300:]


def op_race(ctx: Ctx, result: dict) -> dict:
    quick = not ctx.args.runs and not ctx.args.full
    runs = min(max(4, ctx.args.runs or (RACE_FULL_RUNS if ctx.args.full else RACE_QUICK_RUNS)), 1000)
    copies = 4
    per = -(-runs // copies)
    loops = min(200, max(20, runs // 2))
    ctx.units_total = 3 + copies + loops // 25
    cand = prepare_arm(ctx, "candidate", ctx.args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    built = build_binary(ctx, cand)
    if not built["ok"]:
        return candidate_failed_build(result, built)
    stock = prepare_arm(ctx, "stock", ctx.stock_dir)
    stock_binary = need_binary(ctx, stock)
    ctx.progress("candidate + stock binaries", done=True)
    # ---- part 1: stock cmd/restic TestRestore, 4 parallel copies, one process per run (the S015 reproduction)
    argv = go_sandbox(ctx, cand.work, unprivileged=True) + [GO, "test", "-c", "-tags", j.BUILD_TAGS, "-o", "/build/out/restic.test",
                                                            "./cmd/restic"]
    run = run_plain(argv, cand.work / "testc.stdout", cand.work / "testc.stderr", 900)
    if run["exit_code"] != 0:
        lines = first_lines(tail_text(cand.work / "testc.stderr", 20000))
        result.update(testrestore_failures=None, concurrent_restore_failures=None, concurrent_ls_failures=None, examples=lines[:10],
                      summary="The stock cmd/restic tests do not compile against the candidate: " + " | ".join(lines[:3])[:400])
        return result
    ctx.progress("stock cmd/restic test binary compiled against the candidate", done=True)
    began = time.monotonic()
    script = ("cd /build/src/cmd/restic; w=$1; n=$2; f=0; mkdir -p /build/tmp/w$w /build/out/race; "
              "for i in $(seq 1 $n); do TMPDIR=/build/tmp/w$w /build/out/restic.test -test.run '^TestRestore$' -test.count=1 "
              "-test.timeout 5m > /build/out/race/w$w.txt 2>&1; rc=$?; echo \"$i rc=$rc\" >> /build/out/race/w$w.log; "
              "if [ $rc -ne 0 ] && [ $f -lt 5 ]; then cp /build/out/race/w$w.txt /build/out/race/fail-w$w-$i.txt; f=$((f+1)); fi; "
              "rm -rf /build/tmp/w$w/*; done")
    workers = []
    for worker in range(copies):
        argv = go_sandbox(ctx, cand.work, unprivileged=True) + ["/bin/bash", "-c", script, "race", str(worker), str(per)]
        workers.append(subprocess.Popen(argv, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL,
                                        start_new_session=True))
    deadline = time.monotonic() + 1500
    for worker, process in enumerate(workers):
        try:
            process.wait(timeout=max(1, deadline - time.monotonic()))
        except subprocess.TimeoutExpired:
            try:
                os.killpg(process.pid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait()
        ctx.progress(f"TestRestore worker {worker + 1}/{copies} finished", done=True)
    race_dir = cand.work / "out" / "race"
    done_runs, test_failures = 0, 0
    for log in race_dir.glob("w*.log"):
        for line in log.read_text().splitlines():
            done_runs += 1
            test_failures += not line.endswith("rc=0")
    examples = []
    for path in sorted(race_dir.glob("fail-*.txt"))[:4]:
        examples.append("TestRestore: " + race_lines(path.read_text(errors="replace")))
    test_seconds = time.monotonic() - began
    # ---- part 2: the user-level race: restore/ls latest while a stock backup loop keeps committing snapshots
    began = time.monotonic()
    work = ctx.scratch / "race-repo"
    for sub in ("home", "tmp", "repo"):
        (work / sub).mkdir(parents=True)
    data = ctx.scratch / "race-data"
    (data / "src").mkdir(parents=True)
    devnull = subprocess.DEVNULL

    def restic(binary, *args, capture=False):
        argv = j.restic_sandbox(binary, work, data, ctx.procs) + list(args)
        return subprocess.run(argv, stdin=devnull, stdout=devnull, stderr=subprocess.PIPE if capture else devnull, timeout=300)

    (data / "src" / "f0").write_bytes(os.urandom(200000))
    if restic(stock_binary, "init", "-q", capture=True).returncode != 0 or restic(stock_binary, "backup", "-q", "/data/src").returncode != 0:
        raise RuntimeError("stock could not initialise the race repository")
    stop = threading.Event()
    backups = [0]

    def backup_loop():
        index = 0
        while not stop.is_set():
            index += 1
            (data / "src" / f"f{index % 50}").write_bytes(os.urandom(50000))
            restic(stock_binary, "backup", "-q", "--no-cache", "/data/src")
            backups[0] += 1

    thread = threading.Thread(target=backup_loop, daemon=True)
    thread.start()
    restore_failures = ls_failures = 0
    try:
        for index in range(1, loops + 1):
            shutil.rmtree(work / "rt", ignore_errors=True)
            got = restic(built["binary"], "restore", "latest", "--no-cache", "-q", "--target", "/work/rt", capture=True)
            if got.returncode != 0:
                restore_failures += 1
                if restore_failures <= 2:
                    examples.append("restore latest during backups: " + race_lines(got.stderr.decode("utf-8", "replace")))
            got = restic(built["binary"], "ls", "latest", "--no-cache", "-q", capture=True)
            if got.returncode != 0:
                ls_failures += 1
                if ls_failures <= 2:
                    examples.append("ls latest during backups: " + race_lines(got.stderr.decode("utf-8", "replace")))
            if index % 25 == 0:
                ctx.progress(f"concurrent restore/ls {index}/{loops}: restore_fail={restore_failures} ls_fail={ls_failures}", done=True)
    finally:
        stop.set()
        thread.join(120)
    result.update(testrestore_failures=test_failures, testrestore_runs=done_runs, testrestore_parallel_copies=copies,
                  concurrent_restore_failures=restore_failures, concurrent_ls_failures=ls_failures, concurrent_runs=loops,
                  concurrent_stock_backups=backups[0], examples=examples[:8],
                  testrestore_seconds=round(test_seconds, 1), concurrent_seconds=round(time.monotonic() - began, 1),
                  quick=quick)
    total = test_failures + restore_failures + ls_failures
    if total:
        result["summary"] = (f"RACE FAILURES: TestRestore {test_failures}/{done_runs} (4 parallel copies), restore latest "
                             f"{restore_failures}/{loops} and ls latest {ls_failures}/{loops} during stock backups; first: "
                             + (examples[0] if examples else "")[:300])
    else:
        result["summary"] = (f"No race failure: TestRestore 0/{done_runs} with 4 parallel copies; restore latest 0/{loops} and "
                             f"ls latest 0/{loops} during {backups[0]} concurrent stock backups."
                             + (" Quick check; full=true runs 200 before a handoff that touched locking." if quick else ""))
    return result


# ============================================================ op: baseline
def op_baseline(ctx: Ctx, result: dict) -> dict:
    out_dir = Path(ctx.args.out_dir).resolve() if ctx.args.out_dir else ctx.out.parent
    phases = parse_phases(ctx.args.phases, default=list(j.PHASES))
    reps = min(max(1, ctx.args.reps or 3), 9)
    ctx.units_total = 1 + len(phases) * (reps + 3)
    inc = prepare_arm(ctx, "incumbent", ctx.args.incumbent)
    result["candidate_sha256"] = inc.source_sha256
    plain, prof = need_binary(ctx, inc), need_binary(ctx, inc, profile=True)
    ctx.progress("incumbent builds (plain + profile)", done=True)
    data = {}
    for phase in phases:
        run, work = restic_run(ctx, plain, phase)  # warm-up
        drop(work)
        rounds, failure = paired_runs(ctx, phase, {"incumbent": plain}, reps, "baseline")
        if failure:
            raise RuntimeError(f"incumbent failed {phase}: {failure}")
        runs = [row["incumbent"] for row in rounds]
        null = measure_null(ctx, phase, plain, inc.identity)  # the noise band every later bench of this phase uses
        ins = instrumented(ctx, phase, plain, prof, "baseline")
        if "failed" in ins:
            raise RuntimeError(f"incumbent failed the instrumented {phase}: {ins['failed']}")
        cpu = sorted(ins["cpu"]["rows"].items(), key=lambda kv: kv[1][0], reverse=True)[:10]
        own = sorted(((n, v) for n, v in ins["cpu"]["rows"].items() if MODULE in n), key=lambda kv: kv[1][1], reverse=True)[:10]
        heap = sorted(ins["heap"]["rows"].items(), key=lambda kv: kv[1][0], reverse=True)[:8]
        calls = sorted(ins["syscalls"].items(), key=lambda kv: kv[1], reverse=True)[:10]
        data[phase] = {
            "seconds": round(median([r["seconds"] for r in runs]), 3), "rss_mib": round(median([r["rss_mib"] for r in runs]), 1),
            "cores": round(median([r["cores"] for r in runs]), 2), "runs": runs,
            "cpu_profile_total_s": r2(ins["cpu"]["total"], 3),
            "cpu_top": [{"function": n, "flat_s": round(v[0], 3), "cum_s": round(v[1], 3)} for n, v in cpu],
            "cpu_top_restic_cumulative": [{"function": n, "cum_s": round(v[1], 3)} for n, v in own],
            "heap_at_peak": {"total_mib": r2(ins["heap"]["total"]), "stats": ins.get("heap_stats"),
                             "owners": [{"owner": n, "mib": round(v[0], 2)} for n, v in heap]},
            "gc": ins["gc"], "syscalls": [{"syscall": n, "calls": c} for n, c in calls],
            "syscalls_total": sum(ins["syscalls"].values()),
            "noise_band_pct_at_5_reps": band_of(null["time_pair_sigma_pct"], 5),
            "null_time_pair_sigma_pct": null["time_pair_sigma_pct"]}
        link = remote_summary(runs, remote_profile())
        if link:
            data[phase]["remote"] = link
    total = sum(row["seconds"] for row in data.values())
    lines = [f"# Baseline card: incumbent on workload `{ctx.args.workload}`", "",
             f"Incumbent tree `{inc.tree.name}` (source sha256 `{inc.source_sha256[:16]}`), CPUs {ctx.args.cpus} "
             f"({governor(ctx)} governor), {j.now()[:19]}Z. Median of {reps} runs per phase. Diagnostic only: the judge alone scores.",
             "", "| phase | time s | share | peak RSS MiB | cores used | GC cycles | GC pause ms | syscalls |", "|---|---|---|---|---|---|---|---|"]
    for phase, row in data.items():
        lines.append(f"| {phase} | {row['seconds']:.3f} | {row['seconds'] / total * 100:.0f}% | {row['rss_mib']:.0f} | {row['cores']:.2f} | "
                     f"{row['gc']['count']} | {row['gc']['pause_total_ms']:.1f} | {row['syscalls_total']} |")
    lines += [f"| **total** | **{total:.3f}** | | | | | | |", ""]
    if any(row.get("remote") for row in data.values()):
        lines += ["## The link (REST backend)", "", remote_note(remote_profile()), "",
                  "| phase | requests | MiB up | MiB down | latency s (requests x latency) | transfer floor s | most connections busy |",
                  "|---|---|---|---|---|---|---|"]
        for phase, row in data.items():
            link = row.get("remote") or {}
            lines.append(f"| {phase} | {link.get('requests', 0):g} | {link.get('mib_up', 0):g} | {link.get('mib_down', 0):g} | "
                         f"{link.get('latency_s', 0):g} | {link.get('transfer_floor_s', 0):g} | {link.get('max_in_flight', 0)} |")
        lines += ["", "Requests by method and file type (median per run): " + "; ".join(
            f"{phase}: " + ", ".join(f"{kind} {cell['requests']:g}" for kind, cell in (row.get('remote') or {}).get('by_kind', {}).items())
            for phase, row in data.items()), ""]
    for phase, row in data.items():
        lines += [f"## {phase}", "", f"CPU profile total {row['cpu_profile_total_s']} s over {row['seconds']:.3f} s wall "
                  f"({row['cores']:.2f} cores of {ctx.procs}).", "", "CPU, flat (where the cycles are burned):", ""]
        lines += [f"- {r['flat_s']:.3f} s  `{r['function']}`" for r in row["cpu_top"]]
        lines += ["", "CPU, cumulative, restic's own functions (who owns the time):", ""]
        lines += [f"- {r['cum_s']:.3f} s  `{r['function']}`" for r in row["cpu_top_restic_cumulative"]]
        lines += ["", f"Heap in use at the RSS peak ({row['heap_at_peak']['total_mib']} MiB in the profile), by allocating function:", ""]
        lines += [f"- {r['mib']:.2f} MiB  `{r['owner']}`" for r in row["heap_at_peak"]["owners"]] or ["- (the process stayed under 16 MiB)"]
        lines += ["", "Syscalls: " + ", ".join(f"{r['syscall']} {r['calls']}" for r in row["syscalls"]), ""]
    card = "\n".join(lines) + "\n"
    out_dir.mkdir(parents=True, exist_ok=True)
    card_path = out_dir / f"baseline-{ctx.args.workload}.md"
    temporary = card_path.with_name(card_path.name + f".tmp-{os.getpid()}")
    temporary.write_text(card, encoding="utf-8")
    os.replace(temporary, card_path)
    atomic_json(out_dir / f"baseline-{ctx.args.workload}.json", {"workload": ctx.args.workload, "incumbent": str(inc.tree),
                                                                  "incumbent_sha256": inc.source_sha256, "phases": data})
    slowest = max(data, key=lambda p: data[p]["seconds"])
    result.update(card=card, card_path=str(card_path), phases=data, total_seconds_all_phases=round(total, 3),
                  summary=f"Baseline card for {ctx.args.workload}: {total:.2f} s over {len(data)} phases; slowest {slowest} "
                          f"({data[slowest]['seconds']:.2f} s, {data[slowest]['cores']:.2f} cores); card at {card_path}.")
    return result


# ============================================================ op: oracle (output correctness of named phases)
def restic_cmd(ctx: Ctx, binary: Path, work: Path, args: list, label: str, data: Path | None = None,
               remote_store: Path | None = None) -> dict:
    """One unmeasured restic command on an existing work dir (the judge's run sandbox). remote_store: reach that
    repository through the workload's REST server instead of /work/repo (remote-backend workloads only)."""
    for sub in ("home", "tmp"):
        (work / sub).mkdir(exist_ok=True)
    out, err = work / f"{label}.stdout", work / f"{label}.stderr"
    if remote_store is None:
        argv = j.restic_sandbox(binary, work, data, ctx.procs) + list(args)
        run = run_plain(argv, out, err, RUN_TIMEOUT)
    else:
        argv = j.restic_sandbox(binary, work, data, ctx.procs, repository=j.REMOTE_REPOSITORY) + list(args)
        run = remote_launch(argv, work, remote_store, out, err, RUN_TIMEOUT, runner=run_plain)
    run.update(stdout_path=out, stdout_tail=tail_text(out, 600), stderr_tail=tail_text(err, 600))
    return run


def oracle_facts(ctx: Ctx, phase: str, binary: Path, stock_binary: Path, deep: bool = False) -> dict:
    """Run one phase with `binary` on a private copy of the prepared state and collect what the judge's per-phase
    oracle compares. '_manifest' (kept in memory only) is the full restore manifest for a diff."""
    run, work = restic_run(ctx, binary, phase)
    facts = {"exit_code": run["exit_code"], "seconds": round(run["seconds"], 3)}
    try:
        if run["exit_code"] != 0 or run["timed_out"]:
            facts["failed"] = f"exit {run['exit_code']}: " + " ".join(run["stderr_tail"].split())[-400:]
            return facts
        if phase.startswith("backup-") or phase == "forget-prune":
            got = restic_cmd(ctx, stock_binary, work, ["snapshots", "--json"], "oracle-snapshots")
            if got["exit_code"] != 0:
                facts["failed"] = "stock cannot list the snapshots of the repository this arm wrote: " + got["stderr_tail"][-300:]
                return facts
            snaps = json.loads(Path(got["stdout_path"]).read_text())
            if phase == "forget-prune":
                facts["kept_ids"] = [snap["id"] for snap in snaps]
            else:
                latest = snaps[-1]
                facts.update(count=len(snaps), tree=latest["tree"],
                             normalized={k: v for k, v in j.Judge.normalize_snapshot(latest).items() if k != "tree"})
            if deep:
                check = restic_cmd(ctx, stock_binary, work, ["check", "--read-data"], "oracle-check")
                facts["stock_check_ok"] = check["exit_code"] == 0
                if check["exit_code"] != 0:
                    facts["stock_check_output"] = (check["stdout_tail"] + " " + check["stderr_tail"])[-500:]
                restored = restic_cmd(ctx, stock_binary, work, ["restore", "latest", "--target", "/work/oracle-restore"],
                                      "oracle-restore")
                if restored["exit_code"] == 0:
                    entries = j.manifest(work / "oracle-restore" / "data")
                    facts.update(restore_manifest_sha256=j.canonical_sha(entries), entries=len(entries), _manifest=entries)
                else:
                    facts["stock_restore_error"] = restored["stderr_tail"][-300:]
        elif phase == "restore":
            entries = j.manifest(work / "restore" / "data")
            facts.update(restore_manifest_sha256=j.canonical_sha(entries), entries=len(entries), _manifest=entries)
        elif phase == "ls-json":
            parsed = [json.loads(line) for line in (work / "stdout").read_text().splitlines() if line.strip()]
            facts.update(ls_sha256=j.canonical_sha(parsed), ls_lines=len(parsed))
    finally:
        drop(work)
    return facts


def corruption_detected(ctx: Ctx, binary: Path) -> bool:
    """The judge's corruption check: flip one byte in the middle pack; check --read-data must exit non-zero."""
    work = ctx.scratch / "corrupt"
    drop(work)
    work.mkdir(parents=True)
    remote = remote_profile() is not None
    repo = ctx.scratch / "corrupt.remote-store" if remote else work / "repo"  # remote: the REST server's storage
    drop(repo)
    j.copy_tree(ctx.use_bundle() / "states" / "S1a", repo)
    packs = sorted(path for path in (repo / "data").rglob("*") if path.is_file() and path.stat().st_size > 4096)
    victim = packs[len(packs) // 2]
    os.chmod(victim, 0o600)
    with victim.open("r+b") as stream:
        stream.seek(victim.stat().st_size // 2)
        byte = stream.read(1)
        stream.seek(victim.stat().st_size // 2)
        stream.write(bytes([byte[0] ^ 0x5A]))
    run = restic_cmd(ctx, binary, work, ["check", "--read-data"], "corrupt-check", remote_store=repo if remote else None)
    drop(work)
    drop(repo)
    return run["exit_code"] != 0


def stock_reference(ctx: Ctx, phase: str, stock_binary: Path, deep: bool, need_manifest: bool = False) -> dict:
    """Stock's facts for one phase of the active bundle, kept on disk (digests only) so a later call skips stock."""
    bundle = ctx.use_bundle()
    path = RT / "oracle-ref" / f"{bundle.name}__{phase}{'__deep' if deep else ''}.json"
    if not need_manifest:
        try:
            saved = j.load(path)
            if saved.get("stock_identity") == ctx.arms["stock"].identity and saved.get("version") == HARNESS_VERSION:
                return dict(saved["facts"], cached=True)
        except (OSError, ValueError, KeyError):
            pass
    facts = oracle_facts(ctx, phase, stock_binary, stock_binary, deep)
    if "failed" in facts:
        raise RuntimeError(f"stock failed {phase} while building the oracle reference: {facts['failed']}")
    atomic_json(path, {"version": HARNESS_VERSION, "stock_identity": ctx.arms["stock"].identity, "bundle": bundle.name,
                       "phase": phase, "facts": {k: v for k, v in facts.items() if k != "_manifest"}, "made_at": j.now()})
    return facts


def oracle_phase(ctx: Ctx, phase: str, cand_binary: Path, stock_binary: Path) -> dict:
    began = time.monotonic()
    deep = phase == "forget-prune"
    failures, checks = [], []
    try:
        cand = oracle_facts(ctx, phase, cand_binary, stock_binary, deep)
    except Exception as exc:  # an oracle that cannot read the candidate's output is a failed oracle (as in the judge)
        return {"ok": False, "failures": [f"{phase}: the oracle could not read the candidate's output: {exc}"[:500]],
                "checks": [], "seconds": round(time.monotonic() - began, 1)}
    if "failed" in cand:
        return {"ok": False, "failures": [f"{phase}: the candidate failed the phase ({cand['failed']})"], "checks": [],
                "seconds": round(time.monotonic() - began, 1)}
    ref = stock_reference(ctx, phase, stock_binary, deep)

    def manifests_equal(what: str) -> None:
        nonlocal ref
        if cand.get("restore_manifest_sha256") == ref.get("restore_manifest_sha256") and cand.get("restore_manifest_sha256"):
            checks.append(f"{what}: {cand['entries']} entries equal stock's (content, modes, owners, file and directory mtimes, xattrs)")
            return
        if "_manifest" not in ref:
            ref = stock_reference(ctx, phase, stock_binary, True if phase != "restore" else deep, need_manifest=True)
        diff = j.manifest_diff(ref.get("_manifest") or {}, cand.get("_manifest") or {}, 5)
        failures.append(f"{what} differs from stock's: " + json.dumps(diff)[:900])

    if phase.startswith("backup-"):
        if cand["count"] != ref["count"]:
            failures.append(f"{phase}: {cand['count']} snapshots, stock has {ref['count']}")
        if cand["normalized"] != ref["normalized"]:
            keys = [k for k in set(cand["normalized"]) | set(ref["normalized"]) if cand["normalized"].get(k) != ref["normalized"].get(k)]
            failures.append(f"{phase}: snapshot metadata/summary differs from stock: "
                            + json.dumps({k: (cand["normalized"].get(k), ref["normalized"].get(k)) for k in keys})[:900])
        else:
            checks.append("snapshot metadata and summary equal stock's (ids, times and added-bytes normalised)")
        if cand["tree"] == ref["tree"]:
            checks.append("snapshot tree id equals stock's")
        else:  # the judge's slow path: a different tree may still be equivalent
            cand = oracle_facts(ctx, phase, cand_binary, stock_binary, deep=True)
            ref = stock_reference(ctx, phase, stock_binary, True)
            if not cand.get("stock_check_ok"):
                failures.append(f"{phase}: stock check --read-data fails on the candidate's repository: {cand.get('stock_check_output', '')}")
            manifests_equal(f"{phase}: stock's restore of the candidate's snapshot (tree id differs from stock's)")
    elif phase == "restore":
        manifests_equal("restore: the restored tree")
    elif phase == "check-read-data":
        checks.append("check --read-data exits 0 on the intact repository")
        if corruption_detected(ctx, cand_binary):
            checks.append("check --read-data detects a pack with one flipped byte")
        else:
            failures.append("check-read-data: the candidate did not detect a corrupted pack")
    elif phase == "forget-prune":
        if cand["kept_ids"] != ref["kept_ids"]:
            failures.append(f"forget-prune: kept {cand['kept_ids']}, stock kept {ref['kept_ids']}")
        else:
            checks.append(f"kept the same {len(cand['kept_ids'])} snapshot(s) as stock")
        if cand.get("stock_check_ok"):
            checks.append("stock check --read-data passes on the pruned repository")
        else:
            failures.append(f"forget-prune: stock check --read-data fails on the pruned repository: {cand.get('stock_check_output', '')}")
        manifests_equal("forget-prune: stock's restore of the pruned repository")
    elif phase == "ls-json":
        if cand["ls_sha256"] == ref["ls_sha256"]:
            checks.append(f"ls --json: {cand['ls_lines']} parsed lines equal stock's")
        else:
            failures.append(f"ls-json: {cand['ls_lines']} parsed lines differ from stock's ({ref['ls_lines']})")
    return {"ok": not failures, "failures": failures, "checks": checks, "stock_reference": "cached" if ref.get("cached") else "run now",
            "seconds": round(time.monotonic() - began, 1)}


def op_oracle(ctx: Ctx, result: dict) -> dict:
    phases = parse_phases(ctx.args.phases)
    workloads = parse_workloads(ctx)
    ctx.units_total = 1 + len(workloads) * len(phases)
    cand = prepare_arm(ctx, "candidate", ctx.args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    built = build_binary(ctx, cand)
    if not built["ok"]:
        result["phases"] = {}
        return candidate_failed_build(result, built)
    stock_binary = need_binary(ctx, prepare_arm(ctx, "stock", ctx.stock_dir))
    ctx.progress("candidate + stock binaries", done=True)
    result.update(workloads={}, phases={})
    sentences, bad = [], 0
    for workload in workloads:
        ctx.switch_workload(workload)
        rows = {}
        for phase in phases:
            rows[phase] = oracle_phase(ctx, phase, built["binary"], stock_binary)
            ctx.progress(f"oracle {workload} {phase}: {'ok' if rows[phase]['ok'] else 'FAILED'}", done=True)
        result["workloads"][workload] = {"phases": rows}
        bad += sum(not row["ok"] for row in rows.values())
        sentences.append((f"[{workload}] " if len(workloads) > 1 else "") + "; ".join(
            f"{phase}: " + ("OK (" + "; ".join(row["checks"]) + ")" if row["ok"] else "FAILED: " + " | ".join(row["failures"])[:500])
            for phase, row in rows.items()))
    result["phases"] = (result["workloads"][workloads[0]]["phases"] if len(workloads) == 1 else
                        {f"{w}/{p}": row for w, entry in result["workloads"].items() for p, row in entry["phases"].items()})
    if len(workloads) > 1:
        result["workload"] = ",".join(workloads)
    result["oracle_ok"] = bad == 0
    result["summary"] = (("Output oracle PASSED for the named phases. " if bad == 0 else f"Output oracle FAILED in {bad} phase(s). ")
                         + " | ".join(sentences) + " (the judge's per-phase oracle against stock; check_parity still runs the "
                         "whole oracle, the cross-reader and the compatibility tests before a handoff).")
    return result


# ============================================================ op: ablate (which change carries the gain)
def changed_line_count(a: bytes | None, b: bytes | None) -> int:
    import difflib
    left = (a or b"").decode("utf-8", "replace").splitlines()
    right = (b or b"").decode("utf-8", "replace").splitlines()
    return sum(1 for line in difflib.unified_diff(left, right, lineterm="", n=0)
               if line[:1] in "+-" and not line.startswith(("+++", "---")))


def op_ablate(ctx: Ctx, result: dict) -> dict:
    args = ctx.args
    phases = parse_phases(args.phases)
    reps = min(max(2, args.reps or 3), 7)
    limit = min(max(1, args.max_variants or 6), 8)
    cand = prepare_arm(ctx, "candidate", args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    against_base = bool(args.base) and args.versus != "incumbent"
    base = prepare_arm(ctx, "base" if against_base else "incumbent", args.base if against_base else args.incumbent)
    label = "base (the source this match started from)" if against_base else "incumbent"
    changed = cand.promotable_changed_vs(base)
    sizes = {rel: changed_line_count((base.src / rel).read_bytes() if (base.src / rel).is_file() else None,
                                     (cand.src / rel).read_bytes() if (cand.src / rel).is_file() else None) for rel in changed}
    result.update(versus=label, changed_files=[{"file": rel, "changed_lines": sizes[rel]} for rel in changed][:80], phases=phases,
                  reps=reps, governor=governor(ctx))
    if not changed:
        result.update(groups=[], summary=f"No promotable file differs from the {label}: nothing to ablate.")
        return result
    groups, notes = [], []
    if args.groups:
        for index, item in enumerate(part for part in args.groups.split(",") if part.strip()):
            files = [f.strip().lstrip("./") for f in item.split("+") if f.strip()]
            unknown = [f for f in files if f not in changed]
            if unknown:
                raise InputError(f"group file(s) not among the changed files: {', '.join(unknown)}; changed: {', '.join(changed[:40])}")
            groups.append({"group": files[0] if len(files) == 1 else f"group {index + 1} ({len(files)} files)", "files": files})
    elif len(changed) <= limit:
        groups = [{"group": rel, "files": [rel]} for rel in changed]
    else:
        by_dir = collections.defaultdict(list)
        for rel in changed:
            by_dir[rel.rsplit("/", 1)[0]].append(rel)
        groups = [{"group": f"{directory}/ ({len(files)} files)", "files": files} for directory, files in sorted(by_dir.items())]
        notes.append(f"{len(changed)} changed files were grouped by package directory; name groups to split a package")
    if len(groups) > limit:
        groups.sort(key=lambda g: sum(sizes[f] for f in g["files"]), reverse=True)
        notes.append(f"only the {limit} largest of {len(groups)} groups were measured: "
                     + ", ".join(g["group"] for g in groups[limit:])[:300] + " left out")
        groups = groups[:limit]
    ctx.units_total = 2 + len(groups) + len(phases) * reps
    built = build_binary(ctx, cand)
    if not built["ok"]:
        return candidate_failed_build(result, built)
    arms = {"candidate": cand.work / "out" / "restic-candidate"}
    os.replace(built["binary"], arms["candidate"])
    base_built = build_binary(ctx, base)
    if base_built["ok"]:
        arms["reference"] = base_built["binary"]
    else:
        notes.append(f"the {label} tree does not build here, so there is no all-changes row")
    ctx.progress("candidate + reference builds", done=True)
    for index, group in enumerate(groups):
        originals = {}
        try:
            for rel in group["files"]:
                target, source = cand.src / rel, base.src / rel
                originals[rel] = target.read_bytes() if target.is_file() else None
                if source.is_file():
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(source.read_bytes())
                else:
                    target.unlink(missing_ok=True)
            variant = Arm(f"without-{index}", cand.tree, cand.files, cand.work,
                          sha_text(json.dumps([cand.identity, base.identity, "ablate", group["files"]])), cand.policy)
            got = build_binary(ctx, variant)
        finally:
            for rel, data in originals.items():
                target = cand.src / rel
                if data is None:
                    target.unlink(missing_ok=True)
                else:
                    target.parent.mkdir(parents=True, exist_ok=True)
                    target.write_bytes(data)
        group["changed_lines"] = sum(sizes[f] for f in group["files"])
        if got["ok"]:
            kept = cand.work / "out" / f"restic-without-{index}"
            os.replace(got["binary"], kept)
            arms[f"without {index}"] = kept
            group["builds_without_it"] = True
        else:
            group.update(builds_without_it=False, verdict="DOES NOT BUILD WITHOUT IT (other changes depend on it)",
                         errors=first_lines(got["stderr"], 4))
        ctx.progress(f"variant without {group['group']} ({'built' if got['ok'] else 'does not build'})", done=True)
    rows = {}
    for phase in phases:
        rounds, failure = paired_runs(ctx, phase, arms, reps, "ablate")
        if failure and failure["arm"] in ("candidate", "reference"):
            result.update(groups=groups, notes=notes, failure=failure,
                          summary=f"The {failure['arm']} FAILED {phase} (exit {failure['exit_code']}): "
                                  + " ".join(failure["stderr_tail"].split())[-300:])
            return result
        if failure:
            index = int(failure["arm"].split()[1])
            groups[index].setdefault("per_phase", {})[phase] = {"verdict": "FAILS THE PHASE WITHOUT IT", "exit_code": failure["exit_code"]}
            arms.pop(failure["arm"])
            rounds = [row for row in rounds if all(name in row for name in arms)]
        rows[phase] = rounds
    null = {phase: read_null(ctx, phase) for phase in phases}

    def carried(phase: str, other: str) -> dict | None:
        got = [row for row in rows[phase] if other in row and "candidate" in row]
        if not got:
            return None
        out = judge_pairs([pct_saved(row[other]["seconds"], row["candidate"]["seconds"]) for row in got],
                          [pct_saved(row[other]["rss_mib"], row["candidate"]["rss_mib"]) for row in got], null[phase])
        out.pop("pair_sigma_pct", None)
        link = {"with_it": remote_summary([row["candidate"] for row in got]), "without_it": remote_summary([row[other] for row in got])}
        if link["with_it"] and link["without_it"]:  # remote-backend workload: what the group does to the link
            out["remote"] = {side: {key: cell[key] for key in ("requests", "mib_up", "mib_down", "latency_s")}
                             for side, cell in link.items()}
        return out

    result["all_changes"] = {phase: carried(phase, "reference") for phase in phases} if "reference" in arms else None
    for index, group in enumerate(groups):
        if f"without {index}" in arms:
            for phase in phases:
                cell = carried(phase, f"without {index}")
                if cell:
                    group.setdefault("per_phase", {})[phase] = cell
    lines = []
    for phase in phases:
        parts = []
        whole = (result["all_changes"] or {}).get(phase)
        if whole:
            parts.append(f"all changes {whole['time_saved_pct']:+.2f}% time, {whole['rss_saved_pct']:+.2f}% memory ({whole['verdict']})")
        for group in groups:
            cell = (group.get("per_phase") or {}).get(phase)
            if cell and "time_saved_pct" in cell:
                parts.append(f"{group['group']} carries {cell['time_saved_pct']:+.2f}% time, {cell['rss_saved_pct']:+.2f}% memory ({cell['verdict']})")
            elif cell:
                parts.append(f"{group['group']}: {cell['verdict']}")
            elif not group.get("builds_without_it"):
                parts.append(f"{group['group']}: does not build without it")
        lines.append(f"{phase} vs {label}: " + "; ".join(parts))
    result.update(groups=groups, notes=notes,
                  summary=" | ".join(lines) + f" (a group's number is the candidate against the candidate with that group "
                  f"reverted to the {label.split(' ')[0]}; {reps} rotated runs per arm; positive = the change helps; groups "
                  "that depend on each other each carry the shared gain, so the numbers need not add up; diagnostic)." + "".join(" Note: " + note + "." for note in notes))
    return result


# ============================================================ op: timeline (stage marks: wall, CPU, cores, idle)
MARKS_GO = r"""// Package rtmarks is research-tools instrumentation only (never part of a candidate).
// With RT_MARKS set, Point appends one line per mark: name, seconds since start, process CPU seconds.
package rtmarks

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	path = os.Getenv("RT_MARKS")
	t0   = time.Now()
	mu   sync.Mutex
)

func init() { Point("init") }

func cpu() float64 {
	b, err := os.ReadFile("/proc/self/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 13 {
		return 0
	}
	u, _ := strconv.ParseFloat(f[11], 64)
	k, _ := strconv.ParseFloat(f[12], 64)
	return (u + k) / 100.0
}

// Point records a named mark.
func Point(name string) {
	if path == "" {
		return
	}
	line := fmt.Sprintf("%s\t%.6f\t%.2f\n", name, time.Since(t0).Seconds(), cpu())
	mu.Lock()
	defer mu.Unlock()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line)
	_ = f.Close()
}
"""
MARKS_IMPORT = '\t"github.com/restic/restic/internal/rtmarks"\n'
# (file, anchor text, where, mark): soft anchors - one that is not in the tree is skipped and reported.
PHASE_MARKS = {"backup-initial": ("work:snapshot",), "backup-unchanged": ("work:snapshot",), "backup-mutated": ("work:snapshot",),
               "restore": ("work:restore", "restore:"), "check-read-data": ("check:",), "forget-prune": ("prune:",),
               "ls-json": ("ls:",)}
COMMON_MARKS = ("main", "open:", "lock:", "index")
MARK_ANCHORS = [
    ("cmd/restic/main.go", "func main() {\n", "after", "main"),
    ("internal/global/global.go", "\terr = hasRepositoryConfig(ctx, be, repo, gopts)\n", "before", "open:backend-opened"),
    ("internal/global/global.go", "\terr = decryptRepository(ctx, s, &gopts, printer)\n", "before", "open:key-begin"),
    ("internal/global/global.go", "\tprintRepositoryInfo(s, gopts, printer)\n", "before", "open:key-done"),
    ("cmd/restic/lock.go", "\t} else {\n\t\trepo.SetDryRun()\n\t}\n", "after", "lock:acquired"),
    ("internal/repository/repository.go",
     "func (r *Repository) LoadIndex(ctx context.Context, p restic.TerminalCounterFactory) error {\n", "defer", "index"),
    ("cmd/restic/cmd_backup.go", "\t_, id, summary, err := arch.Snapshot(ctx, targets, snapshotOpts)\n", "around", "work:snapshot"),
    ("cmd/restic/cmd_restore.go", "\tcountRestoredFiles, err := res.RestoreTo(ctx, opts.Target)\n", "around", "work:restore"),
    ("internal/restorer/restorer.go", "\t\terr = filerestorer.restoreFiles(ctx)\n", "around", "restore:files"),
    ("internal/restorer/restorer.go", '\tdebug.Log("second pass for %q", dst)\n', "before", "restore:second-pass"),
    ("cmd/restic/cmd_check.go", "\tgo chkr.Packs(ctx, errChan)\n", "before", "check:packs"),
    ("cmd/restic/cmd_check.go", "\t\tchkr.Structure(ctx, bar, errChan)\n", "before", "check:structure"),
    ("cmd/restic/cmd_check.go", "\t\tunused, err := chkr.UnusedBlobs(ctx)\n", "before", "check:unused"),
    ("cmd/restic/cmd_check.go", "\t\tgo chkr.ReadPacks(ctx, readDataFilter, printer, errChan)\n", "before", "check:readdata"),
    ("cmd/restic/cmd_prune.go", "\tplan, err := repository.PlanPrune(", "before", "prune:plan"),
    ("cmd/restic/cmd_prune.go", "\treturn plan.Execute(ctx, printer)\n", "before", "prune:execute"),
    ("cmd/restic/cmd_ls.go", "\terr = walker.Walk(ctx, repo, *sn.Tree, walker.WalkVisitor{", "before", "ls:walk"),
]


def apply_marks(src: Path) -> tuple:
    """Insert the stage marks into a scratch copy of a tree. Returns (originals for undo, applied, missing)."""
    originals, applied, missing = {}, [], []
    point = lambda name: f'\trtmarks.Point("{name}")\n'  # noqa: E731
    by_file = collections.defaultdict(list)
    for rel, anchor, where, mark in MARK_ANCHORS:
        by_file[rel].append((anchor, where, mark))
    for rel, edits in by_file.items():
        path = src / rel
        try:
            text = path.read_text()
        except OSError:
            missing += [mark for _, _, mark in edits]
            continue
        new, done = text, []
        for anchor, where, mark in edits:
            if anchor not in new or "import (\n" not in new:
                missing.append(mark)
                continue
            if where == "before":
                new = new.replace(anchor, point(mark) + anchor, 1)
            elif where == "after":
                new = new.replace(anchor, anchor + point(mark), 1)
            elif where == "defer":
                new = new.replace(anchor, anchor + point(mark + ":begin") + f'\tdefer rtmarks.Point("{mark}:end")\n', 1)
            else:  # around
                new = new.replace(anchor, point(mark + "-begin") + anchor + point(mark + "-end"), 1)
            done.append(mark)
        if done:
            originals[rel] = text
            path.write_text(new.replace("import (\n", "import (\n" + MARKS_IMPORT, 1))
            applied += done
    return originals, applied, missing


def build_marked(ctx: Ctx, arm: Arm) -> dict:
    """The arm's binary with stage marks (scratch copy only; a tree whose anchors moved gets fewer marks, a tree the
    marks do not compile in falls back to the plain binary)."""
    package = arm.src / "internal" / "rtmarks"
    originals, applied, missing = {}, [], []
    try:
        package.mkdir(exist_ok=True)
        (package / "rtmarks.go").write_text(MARKS_GO)
        originals, applied, missing = apply_marks(arm.src)
        variant = Arm(arm.name + "-marks", arm.tree, arm.files, arm.work,
                      sha_text(json.dumps([arm.identity, "marks", sha_text(MARKS_GO), sorted(applied)])), arm.policy)
        built = build_binary(ctx, variant)
    finally:
        for rel, text in originals.items():
            (arm.src / rel).write_text(text)
        shutil.rmtree(package, ignore_errors=True)
    if built["ok"]:
        kept = arm.work / "out" / "restic-marks"
        os.replace(built["binary"], kept)
        return {"ok": True, "binary": kept, "marks": applied, "missing": missing}
    plain = build_binary(ctx, arm)
    return {"ok": plain["ok"], "binary": plain.get("binary"), "marks": [], "missing": [m for _, _, _, m in MARK_ANCHORS],
            "stderr": plain.get("stderr", ""), "marks_build_error": first_lines(built["stderr"], 4)}


def stage_rows(marks_text: str, run: dict) -> list:
    """[(stage label, wall s, cpu s)] between consecutive marks, the last one ending at process exit."""
    return [(label, end - begin, cpu) for label, begin, end, cpu in stage_spans(marks_text, run)]


def stage_spans(marks_text: str, run: dict) -> list:
    """[(stage label, start s, end s, cpu s)] between consecutive marks, the last one ending at process exit."""
    points = [("start", 0.0, 0.0)]
    for line in marks_text.splitlines():
        fields = line.split("\t")
        if len(fields) == 3:
            try:
                points.append((fields[0], float(fields[1]), float(fields[2])))
            except ValueError:
                pass
    points = [points[0]] + sorted(points[1:], key=lambda item: item[1])
    points.append(("exit", max(run["seconds"], points[-1][1]), max(run["user_seconds"] + run["system_seconds"], points[-1][2])))
    rows, seen = [], collections.Counter()
    for (a, at, acpu), (b, bt, bcpu) in zip(points, points[1:]):
        if a == "start" and b == "init":
            continue
        label = f"{'start' if a == 'init' else a} -> {b}"
        seen[label] += 1
        if seen[label] > 1:
            label += f" #{seen[label]}"
        rows.append((label, at, bt, max(0.0, bcpu - acpu)))
    return rows


def stage_requests(spans: list, log_path: Path, offset_s: float) -> dict:
    """{stage label: {requests, mib_up, mib_down}} from the REST server's request log (remote-backend workloads): a
    request belongs to the stage in which it ARRIVED. The log clock starts with the server, offset_s before the
    restic launch, so a request within ~0.03 s of a stage edge may land on either side."""
    out = {label: {"requests": 0, "mib_up": 0.0, "mib_down": 0.0} for label, _, _, _ in spans}
    try:
        lines = log_path.read_text().splitlines()
    except OSError:
        return {}
    for line in lines:
        try:
            row = json.loads(line)
            at = row["t_ms"] / 1000.0 - offset_s
        except (ValueError, KeyError, TypeError):
            continue
        label = next((name for name, begin, end, _ in spans if at < end), spans[-1][0] if spans else None)
        if label is None:
            continue
        out[label]["requests"] += 1
        out[label]["mib_up"] += row.get("in", 0) / j.MIB
        out[label]["mib_down"] += row.get("out", 0) / j.MIB
    return out


def timeline_arm(ctx: Ctx, phase: str, binary: Path, runs: int) -> dict:
    samples, order, stderr, failed = collections.defaultdict(list), [], "", None
    totals, link_runs, link_stages = [], [], collections.defaultdict(list)
    for _ in range(runs):
        run, work = restic_run(ctx, binary, phase, env={"RT_MARKS": "/work/rtmarks.tsv"}, request_log=True)
        try:
            stderr = run["stderr_tail"]
            if run["exit_code"] != 0 or run["timed_out"]:
                failed = {"exit_code": run["exit_code"], "timed_out": run["timed_out"]}
            marks = (work / "rtmarks.tsv").read_text() if (work / "rtmarks.tsv").is_file() else ""
            spans = stage_spans(marks, run)
            if run.get("remote"):  # remote-backend workload: the link use of the run, and of each stage
                link_runs.append(slim(run))
                for label, cell in stage_requests(spans, work / "rest-requests.jsonl", run.get("remote_log_offset_s", 0.0)).items():
                    link_stages[label].append(cell)
        finally:
            drop(work)
        rows = [(label, end - begin, cpu) for label, begin, end, cpu in spans]
        if not order:
            order = [label for label, _, _ in rows]
        for label, wall, cpu in rows:
            samples[label].append((wall, cpu))
        totals.append((run["seconds"], run["user_seconds"] + run["system_seconds"], run["peak_rss_bytes"] / j.MIB))
        if failed:
            break
    stages = {}
    for label in order:
        wall = median([w for w, _ in samples[label]])
        cpu = median([c for _, c in samples[label]])
        stages[label] = {"wall_s": round(wall, 3), "cpu_s": round(cpu, 3), "cores": round(cpu / wall, 2) if wall > 0.005 else None,
                         "idle_core_s": round(max(0.0, wall * ctx.procs - cpu), 3)}
        if link_stages.get(label):
            stages[label].update({key: round(median([cell[key] for cell in link_stages[label]]), 1)
                                  for key in ("requests", "mib_up", "mib_down")})
    return {"stages": stages, "wall_s": round(median([t[0] for t in totals]), 3), "cpu_s": round(median([t[1] for t in totals]), 3),
            "rss_mib": round(median([t[2] for t in totals]), 1), "runs": len(totals), "failed": failed, "stderr_tail": stderr,
            "remote": remote_summary(link_runs, remote_profile()) if link_runs else None}


def op_timeline(ctx: Ctx, result: dict) -> dict:
    phase = ctx.args.phase
    if phase not in j.PHASES:
        raise InputError("--phase must be one of " + ",".join(j.PHASES))
    runs = min(max(1, ctx.args.reps or 3), 5)
    ctx.units_total = 4
    cand = prepare_arm(ctx, "candidate", ctx.args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    ref, label = reference_arm(ctx, result)
    cand_built = build_marked(ctx, cand)
    if not cand_built["ok"]:
        return candidate_failed_build(result, cand_built)
    ctx.progress(f"candidate build with {len(cand_built['marks'])} stage marks", done=True)
    ref_built = build_marked(ctx, ref)
    if not ref_built["ok"]:
        raise RuntimeError(f"{ref.name} does not build: {ref_built.get('stderr', '')[-800:]}")
    ctx.progress(f"{ref.name} build with {len(ref_built['marks'])} stage marks", done=True)
    c = timeline_arm(ctx, phase, cand_built["binary"], runs)
    ctx.progress("candidate runs", done=True)
    i = timeline_arm(ctx, phase, ref_built["binary"], runs)
    ctx.progress(f"{ref.name} runs", done=True)
    labels = list(c["stages"]) + [name for name in i["stages"] if name not in c["stages"]]
    stages, tiny = [], 0
    for name in labels:
        cs, rs = c["stages"].get(name), i["stages"].get(name)
        if max((cs or {}).get("wall_s", 0), (rs or {}).get("wall_s", 0)) < 0.002:
            tiny += 1   # two marks next to each other: nothing happens between them
            continue
        stages.append({"stage": name, "cand": cs, "ref": rs,
                       "wall_delta_s": round(cs["wall_s"] - rs["wall_s"], 3) if cs and rs else None})
    relevant = COMMON_MARKS + PHASE_MARKS[phase]
    cand_built["missing"] = [m for m in cand_built["missing"] if m.startswith(relevant)]
    ref_built["missing"] = [m for m in ref_built["missing"] if m.startswith(relevant)]
    own_lines = [line for line in c["stderr_tail"].splitlines() if line.strip()][-20:]
    result.update(phase=phase, stages=stages, runs=runs, cores_available=ctx.procs,
                  wall_s={"cand": c["wall_s"], "ref": i["wall_s"]}, cpu_s={"cand": c["cpu_s"], "ref": i["cpu_s"]},
                  rss_mib={"cand": c["rss_mib"], "ref": i["rss_mib"]},
                  marks_missing={"cand": cand_built["missing"], "ref": ref_built["missing"]},
                  candidate_stderr_lines=own_lines, candidate_exit=c["failed"], empty_stages_left_out=tiny,
                  note="medians of %d instrumented runs per arm; marks cost microseconds; use quick_bench for deltas. "
                       "idle_core_s = wall x cores available - CPU used: time the lane's cores sat idle in that stage." % runs)
    if cand_built.get("marks_build_error"):
        result["marks_build_error"] = cand_built["marks_build_error"]
    if c.get("remote"):  # remote-backend workload: requests and bytes of the phase per arm (per stage: in `stages`)
        result["remote"] = {"cand": c["remote"], "ref": i.get("remote")}
        result["remote_profile"] = dict(remote_profile(), note=remote_note(remote_profile()))
        result["note"] += (" On this workload each stage also carries the requests that ARRIVED in it and their MiB "
                           "up/down (from the REST server's log; a request within ~0.03 s of a stage edge may land on "
                           "either side).")
    biggest = max((row for row in stages if row["cand"]), key=lambda row: row["cand"]["wall_s"], default=None)
    moved = max((row for row in stages if row["wall_delta_s"] is not None), key=lambda row: abs(row["wall_delta_s"]), default=None)
    idle = max((row for row in stages if row["cand"]), key=lambda row: row["cand"]["idle_core_s"], default=None)
    text = f"{phase}: {c['wall_s']} s wall, {c['cpu_s']} s CPU vs {label} {i['wall_s']} s, {i['cpu_s']} s on {ctx.procs} cores"
    text += remote_sentence(c.get("remote"), i.get("remote"))
    if biggest:
        text += (f"; longest stage '{biggest['stage']}' {biggest['cand']['wall_s']} s at {biggest['cand']['cores']} cores"
                 + (f" ({label.split(' ')[0]} {biggest['ref']['wall_s']} s)" if biggest["ref"] else ""))
    if moved and abs(moved["wall_delta_s"]) > 0:
        text += f"; biggest change '{moved['stage']}' {moved['wall_delta_s']:+.3f} s"
    if idle:
        text += f"; most idle core time in '{idle['stage']}' ({idle['cand']['idle_core_s']} core-s)"
    if c["failed"]:
        text += f"; the candidate EXITED {c['failed']['exit_code']} (its last stderr lines are in candidate_stderr_lines)"
    if cand_built["missing"]:
        text += f"; {len(cand_built['missing'])} mark(s) not placed in the candidate (code moved): " + ", ".join(cand_built["missing"])[:160]
    result["summary"] = text + "."
    return result


# ============================================================ op: gobench (restic's own Go benchmarks)
BENCH_LINE = re.compile(r"^(Benchmark\S+?)(?:-\d+)?\s+(\d+)\s+([0-9.]+) ns/op(.*)$")


def parse_bench(text: str) -> dict:
    rows, package = collections.defaultdict(lambda: {"ns": [], "bytes": [], "allocs": []}), ""
    for line in text.splitlines():
        if line.startswith("pkg: "):
            package = short(line[5:].strip())
            continue
        match = BENCH_LINE.match(line.strip())
        if not match:
            continue
        row = rows[f"{package}::{match.group(1)}"]
        row["ns"].append(float(match.group(3)))
        extra = match.group(4)
        for key, pattern in (("bytes", r"(\d+) B/op"), ("allocs", r"(\d+) allocs/op")):
            found = re.search(pattern, extra)
            if found:
                row[key].append(int(found.group(1)))
    return rows


def op_gobench(ctx: Ctx, result: dict) -> dict:
    args = ctx.args
    packages = parse_packages(args.packages)
    if not args.bench:
        raise InputError("--bench is required: a regex of benchmark names, e.g. 'BenchmarkMasterIndex'")
    try:
        re.compile(args.bench)
    except re.error as exc:
        raise InputError(f"--bench is not a valid regular expression: {exc}")
    benchtime = args.benchtime or "1s"
    timed, counted = re.fullmatch(r"(\d+(?:\.\d+)?)s", benchtime), re.fullmatch(r"(\d+)x", benchtime)
    if not ((timed and 0 < float(timed.group(1)) <= 5) or (counted and 0 < int(counted.group(1)) <= 1000000)):
        raise InputError("--benchtime must be seconds up to 5s (e.g. 1s) or iterations up to 1000000x (e.g. 200x)")
    count = min(max(1, args.count if args.count and args.count > 1 else 3), 6)
    ctx.units_total = 3
    cand = prepare_arm(ctx, "candidate", args.candidate)
    result["candidate_sha256"] = cand.source_sha256
    ref, label = reference_arm(ctx, result)
    missing = [p for p in packages if not (cand.src / p).is_dir()]
    if missing:
        raise InputError(f"package(s) not in the tree: {', '.join(missing)}")
    own = overlay_own_tests(ctx, cand) if args.own_tests else []
    ctx.progress("snapshots + stock test overlay", done=True)
    go_args = [GO, "test", "-run", "^$", "-bench", args.bench, "-benchtime", benchtime, f"-count={count}", "-benchmem",
               "-p", "1", "-tags", j.BUILD_TAGS, "-timeout", "2m"] + ["./" + p for p in packages]
    outputs = {}
    for arm in (cand, ref):
        work = ctx.scratch / "gobench" / arm.name
        for sub in ("home", "tmp", "gocache", "src"):
            (work / sub).mkdir(parents=True)
        run = run_plain(go_sandbox(ctx, work, unprivileged=True, src=arm.src) + go_args, work / "bench.txt", work / "bench.err",
                        GOBENCH_KILL_S)
        outputs[arm.name] = {"rows": parse_bench((work / "bench.txt").read_text(errors="replace")), "exit_code": run["exit_code"],
                             "timed_out": run["timed_out"] or "test timed out" in (work / "bench.txt").read_text(errors="replace"),
                             "tail": (tail_text(work / "bench.txt", 1500) + "\n" + tail_text(work / "bench.err", 1500)).strip()}
        ctx.progress(f"{arm.name} benchmarks", done=True)
    c, i = outputs["candidate"], outputs[ref.name]
    rows = []
    for name in sorted(set(c["rows"]) | set(i["rows"])):
        a, b = c["rows"].get(name), i["rows"].get(name)
        row = {"benchmark": name.split("::")[1] if len(packages) == 1 else name,
               "cand_ns_op": median(a["ns"]) if a else None, "ref_ns_op": median(b["ns"]) if b else None}
        if a and b:
            row["time_saved_pct"] = round(pct_saved(row["ref_ns_op"], row["cand_ns_op"]), 2)
            spread = max(robust_sigma([pct_saved(median(b["ns"]), v) for v in a["ns"]]),
                         robust_sigma([pct_saved(median(b["ns"]), v) for v in b["ns"]])) if count > 1 else None
            row["spread_pct"] = round(spread, 2) if spread is not None else None
        for key in ("bytes", "allocs"):
            if a and a[key]:
                row[f"cand_{key}_op"] = median(a[key])
            if b and b[key]:
                row[f"ref_{key}_op"] = median(b[key])
        rows.append(row)
    rows.sort(key=lambda row: abs(row.get("time_saved_pct") or 0), reverse=True)
    result.update(packages=packages, bench=args.bench, benchtime=benchtime, count=count, benchmarks=rows, own_test_files=own,
                  exit_codes={"candidate": c["exit_code"], ref.name: i["exit_code"]})
    scope = f"{', '.join(packages)} -bench {args.bench} -benchtime {benchtime} x{count}"
    cut = [name for name, got in outputs.items() if got["timed_out"]]
    if cut:
        result["timed_out"] = cut
    if cut and not [row for row in rows if row.get("time_saved_pct") is not None]:
        result["output_tail"] = c["tail"][-2000:]
        result["summary"] = (f"The benchmarks did not finish inside the 2-minute cap per tree ({scope}): the regex selects "
                             "benchmarks with heavy setup. Name one benchmark exactly (e.g. '^BenchmarkX$') or lower "
                             "benchtime (e.g. 20x).")
        return result
    if not rows:
        result["output_tail"] = c["tail"][-3000:]
        result["summary"] = (f"No benchmark ran ({scope}): " + ("the test binary did not build or failed: " + " | ".join(
            first_lines(c["tail"], 3))[:300] if c["exit_code"] != 0 else "no benchmark name matched the regex in the stock tests"
            + ("" if args.own_tests else " (your own *_test.go files are compiled in only with own_tests=true)")) + ".")
        return result
    both = [row for row in rows if row.get("time_saved_pct") is not None]
    parts = [f"{row['benchmark']} {row['cand_ns_op']:.0f} vs {row['ref_ns_op']:.0f} ns/op ({row['time_saved_pct']:+.1f}%"
             + (", inside the run-to-run spread" if row.get("spread_pct") and abs(row["time_saved_pct"]) <= 2 * row["spread_pct"] else "")
             + (f", {row.get('cand_allocs_op')} vs {row.get('ref_allocs_op')} allocs" if row.get("cand_allocs_op") is not None
                and row.get("cand_allocs_op") != row.get("ref_allocs_op") else "") + ")" for row in both[:6]]
    only = [row["benchmark"] for row in rows if row.get("time_saved_pct") is None]
    result["summary"] = (f"{len(both)} benchmark(s) on both trees ({scope}), candidate vs {label}, positive = candidate faster: "
                         + "; ".join(parts) + (f"; {len(both) - 6} more in the result" if len(both) > 6 else "")
                         + (f"; on one tree only: {', '.join(only[:5])}" if only else "")
                         + ". Micro-benchmarks say where a function moved, not what the suite score does.")
    return result


OPS = {"build": op_build, "test": op_test, "bench": op_bench, "profile": op_profile, "sweep": op_sweep, "race": op_race,
       "baseline": op_baseline, "oracle": op_oracle, "ablate": op_ablate, "timeline": op_timeline, "gobench": op_gobench}


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(prog="research_tools_v2.py", description=__doc__.split("\n\n")[0])
    parser.add_argument("op", choices=sorted(OPS))
    parser.add_argument("--candidate")
    parser.add_argument("--incumbent", required=True)
    parser.add_argument("--stock", default=str(ROOT / "stock"))
    parser.add_argument("--cpus", required=True)
    parser.add_argument("--team", required=True)
    parser.add_argument("--workload", choices=WORKLOAD_CHOICES, default="main")
    parser.add_argument("--out", required=True)
    parser.add_argument("--packages")
    parser.add_argument("--run")
    parser.add_argument("--count", type=int, default=1)
    parser.add_argument("--parallel-copies", type=int, default=1)
    parser.add_argument("--phases")
    parser.add_argument("--reps", type=int)
    parser.add_argument("--phase", default="restore")
    parser.add_argument("--top", type=int, default=15)
    parser.add_argument("--file")
    parser.add_argument("--pattern")
    parser.add_argument("--values")
    parser.add_argument("--runs", type=int)
    parser.add_argument("--full", action="store_true")
    parser.add_argument("--out-dir")
    parser.add_argument("--workloads")
    parser.add_argument("--base")
    parser.add_argument("--versus", choices=("incumbent", "base"))
    parser.add_argument("--fixed-reps", action="store_true")
    parser.add_argument("--own-tests", action="store_true")
    parser.add_argument("--verbose", action="store_true")
    parser.add_argument("--groups")
    parser.add_argument("--max-variants", type=int)
    parser.add_argument("--bench")
    parser.add_argument("--benchtime")
    args = parser.parse_args(argv)
    out = Path(args.out).resolve()
    result = {"ok": False, "op": args.op, "seconds": None, "workload": args.workload, "candidate_sha256": None,
              "summary": "", "diagnostic_only": True, "harness_version": HARNESS_VERSION, "cpus": args.cpus, "team": args.team}
    began = time.monotonic()
    ctx = None
    code = 0

    def terminate(signum, frame):
        raise KeyboardInterrupt(f"signal {signum}")
    signal.signal(signal.SIGTERM, terminate)
    try:
        if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_.-]{0,40}", args.team):
            raise InputError("--team must be a short plain name")
        if args.op == "baseline":
            args.candidate = args.incumbent
        if not args.candidate:
            raise InputError("--candidate is required")
        if len(j.parse_cpus(args.cpus)) < 1:
            raise InputError("--cpus must name at least one CPU, e.g. 6-9")
        ctx = Ctx(args)
        ctx.setup()
        ctx.progress("started")
        result = OPS[args.op](ctx, result)
        result["ok"] = True
    except InputError as exc:
        result.update(ok=False, error=str(exc), error_kind="bad-argument", summary="Bad argument: " + str(exc))
        code = 2
    except KeyboardInterrupt as exc:
        result.update(ok=False, error=f"interrupted: {exc}", error_kind="interrupted", summary="The harness was stopped before it finished.")
        code = 1
    except Exception as exc:  # harness fault
        result.update(ok=False, error=f"{type(exc).__name__}: {exc}"[:3000], error_kind="harness",
                      traceback=traceback.format_exc()[-3000:], summary="Harness error (not the candidate's fault): " + str(exc)[:300])
        code = 1
    finally:
        result["seconds"] = round(time.monotonic() - began, 2)
        try:
            atomic_json(out, result)
        finally:
            if ctx is not None:
                try:
                    ctx.progress("complete" if code == 0 else "failed", state="complete" if code == 0 else "failed")
                except Exception:
                    pass
                ctx.cleanup()
    print(json.dumps({"ok": result["ok"], "op": args.op, "seconds": result["seconds"], "summary": result["summary"]}), flush=True)
    return code


if __name__ == "__main__":
    sys.exit(main())
