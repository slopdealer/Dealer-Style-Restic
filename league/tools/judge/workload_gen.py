# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""Deterministic, seeded generator for the Restic League V2 frozen workloads.

Run ONCE (per workload version) on the league box:
    python3 workload_gen.py --out /opt/league/restic-league-v2/workloads [--seed 20260924]
It writes corpus-v1/src (the backup source), mutation/ (a declarative mutation spec plus payload
files), expected v1/v2 manifests, and workloads.json (sizes, parameters, and hashes of all of it).
The judge re-derives every hash on every run and refuses to measure if anything differs.
Nothing here is ever regenerated during a season.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
from pathlib import Path
import random
import shutil
import sys
import time

sys.path.insert(0, str(Path(__file__).resolve().parent))
from restic_judge import (  # noqa: E402  (shared, trusted helpers)
    MIB, apply_mutations, atomic, canonical_sha, manifest, manifest_summary, now, tree_sha256_of_files,
)

WORKLOAD_VERSION = "restic-v2-workload-1"
BASE_MTIME = 1_700_000_000
PARAMS = {
    "small_files": 90_000,          # nested project-like tree of small files
    "top_dirs": 24,
    "max_depth": 5,
    "medium_files": 160,            # 256 KiB .. 8 MiB
    "large_random_mib": [192, 192, 128],   # incompressible, distinct
    "dedup_files": 2,               # built from shifted slices of large/big-0.bin plus insertions
    "dedup_mib": 128,
    "sparse_mib": 64,
    "mutation_modify_frac": 0.02,
    "mutation_append_frac": 0.01,
    "mutation_delete_frac": 0.01,
    "mutation_new_files": 600,
    "mutation_chmod": 60,
}


def shake(seed: str, size: int) -> bytes:
    return hashlib.shake_256(seed.encode()).digest(size) if size else b""


def write_big_random(path: Path, size: int, seed: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    with path.open("wb") as stream:
        for offset in range(0, size, MIB):
            stream.write(shake(f"{seed}/{offset}", min(MIB, size - offset)))


class Text:
    """A deterministic 4 MiB word pool; text files are unique-header slices of it (compressible)."""

    def __init__(self, rng: random.Random):
        syllables = ["ka", "to", "ri", "men", "sa", "lo", "vi", "dor", "an", "te", "qua", "ple", "st", "ion", "er"]
        words = ["".join(rng.choice(syllables) for _ in range(rng.randint(1, 4))) for _ in range(3000)]
        words += ["func", "return", "if", "else", "for", "struct", "import", "package", "error", "nil", "{", "}", "(", ")"]
        out = []
        size = 0
        while size < 4 * MIB:
            line = " ".join(rng.choice(words) for _ in range(rng.randint(3, 14))) + "\n"
            out.append(line)
            size += len(line)
        self.pool = "".join(out).encode()

    def make(self, rng: random.Random, size: int, header: str) -> bytes:
        head = (header + "\n").encode()
        if size <= len(head):
            return head[:size]
        body = bytearray(head)
        while len(body) < size:
            start = rng.randrange(0, len(self.pool) - 4096)
            take = min(size - len(body), rng.randint(512, 64 * 1024), len(self.pool) - start)
            body += self.pool[start:start + take]
        return bytes(body[:size])


def small_size(rng: random.Random) -> int:
    # log-normal around 2 KiB, capped at 256 KiB, 1% empty
    if rng.random() < 0.01:
        return 0
    return max(1, min(256 * 1024, int(rng.lognormvariate(7.6, 1.5))))


ODD_NAMES = [
    "Unicode café 空 名前.txt", "HTML-<>&.txt", "quotes-\"-apostrophe-'.txt", "newline-\n-tab-\t-return-\r.txt",
    "separators- - .txt", "backslash-\\-name.txt", "space at end ", " leading space", "-dash-first",
    "emoji-\U0001F600.bin", "x" * 255, "UPPER.lower.MiXeD", "dots...name", ".hidden-file", "#hash#", "semi;colon",
]


def generate(out: Path, seed: int) -> dict:
    rng = random.Random(seed)
    text = Text(random.Random(seed + 1))
    corpus_root = out / "corpus-v1"
    if corpus_root.exists():
        raise SystemExit(f"{corpus_root} exists; workloads are generated once and never regenerated")
    src = corpus_root / "src"
    src.mkdir(parents=True)
    started = time.monotonic()

    def log(stage: str) -> None:
        print(json.dumps({"timestamp": now(), "stage": stage, "elapsed_s": round(time.monotonic() - started, 1)}), flush=True)

    # ---- directory tree
    dirs = [Path("tree")]
    frontier = [(Path("tree") / f"pkg{index:02d}", 1) for index in range(PARAMS["top_dirs"])]
    while frontier:
        path, depth = frontier.pop(0)
        dirs.append(path)
        if depth < PARAMS["max_depth"]:
            for index in range(rng.choice([0, 0, 1, 2, 3, 4, 6])):
                frontier.append((path / f"{rng.choice(['sub', 'mod', 'lib', 'data', 'test'])}{index}", depth + 1))
    for d in dirs:
        (src / d).mkdir(parents=True, exist_ok=True)
    weights = [rng.paretovariate(1.2) for _ in dirs]
    log(f"{len(dirs)} directories")

    # ---- small files
    contents = []
    written = []
    for index in range(PARAMS["small_files"]):
        d = rng.choices(dirs, weights)[0]
        ext = rng.choice([".go", ".txt", ".json", ".bin", ".md", ".dat", ""])
        path = src / d / f"f{index:06d}{ext}"
        kind = rng.random()
        size = small_size(rng)
        if kind < 0.15 and contents:
            data = rng.choice(contents)            # exact duplicate -> blob dedup
        elif kind < 0.55:
            data = text.make(rng, size, f"// file {index}")
        else:
            data = shake(f"small/{seed}/{index}", size)
        if len(contents) < 5000 and len(data) < 64 * 1024:
            contents.append(data)
        path.write_bytes(data)
        mode = rng.choice([0o644, 0o644, 0o644, 0o600, 0o755, 0o444])
        os.chmod(path, mode)
        mtime = BASE_MTIME + rng.randrange(0, 86400 * 900)
        os.utime(path, ns=(mtime * 10**9 + rng.randrange(10**9), mtime * 10**9 + rng.randrange(10**9)))
        written.append(path)
        if (index + 1) % 10000 == 0:
            log(f"small files {index + 1}")

    # ---- medium files
    medium = src / "medium"
    medium.mkdir()
    for index in range(PARAMS["medium_files"]):
        size = int(256 * 1024 * (32 ** rng.random()))
        path = medium / f"m{index:04d}.{'txt' if index % 2 else 'bin'}"
        path.write_bytes(text.make(rng, size, f"medium {index}") if index % 2 else shake(f"medium/{seed}/{index}", size))
        os.utime(path, (BASE_MTIME + index, BASE_MTIME + index))
    log("medium files")

    # ---- large incompressible, dedup-heavy, sparse
    large = src / "large"
    large.mkdir()
    for index, mib in enumerate(PARAMS["large_random_mib"]):
        write_big_random(large / f"big-{index}.bin", mib * MIB, f"big/{seed}/{index}")
    base = (large / "big-0.bin").read_bytes()
    for index in range(PARAMS["dedup_files"]):
        body = bytearray()
        while len(body) < PARAMS["dedup_mib"] * MIB:
            start = rng.randrange(0, len(base) - 4 * MIB)
            body += base[start:start + rng.randint(MIB, 4 * MIB)]
            body += shake(f"ins/{seed}/{index}/{len(body)}", rng.randint(4096, 65536))
        (large / f"dedup-{index}.bin").write_bytes(bytes(body[:PARAMS["dedup_mib"] * MIB]))
    del base
    with (large / "sparse.bin").open("wb") as stream:
        for island in range(3):
            stream.seek(island * 20 * MIB + 7 * MIB)
            stream.write(shake(f"sparse/{seed}/{island}", 512 * 1024))
        stream.truncate(PARAMS["sparse_mib"] * MIB)
    for path in large.iterdir():
        os.utime(path, (BASE_MTIME, BASE_MTIME))
    log("large files")

    write_special(src, medium, seed)
    log("special entries")
    return finish(out, corpus_root, written, rng, seed, log)


def write_special(src: Path, medium: Path, seed: int) -> None:
    # ---- special: names, links, xattrs, ownership, modes, fifo, empty dirs (needs medium/m0001..m0003)
    special = src / "special"
    special.mkdir()
    for index, name in enumerate(ODD_NAMES):
        (special / name).write_bytes(f"odd name {index}\n".encode())
    raw = os.fsdecode(b"raw-invalid-\xff-\xfe.bin")
    (special / raw).write_bytes(b"raw linux filename bytes\n")
    os.symlink(raw, special / "symlink-to-raw-name")
    os.symlink("../medium/m0001.txt", special / "symlink-relative")
    os.symlink("/data/src/medium/m0002.bin", special / "symlink-absolute")
    os.symlink("does/not/exist", special / "symlink-dangling")
    os.symlink("../tree", special / "symlink-to-dir")
    (special / "hardlink-a.bin").write_bytes(shake(f"hl/{seed}", 20000))
    os.link(special / "hardlink-a.bin", special / "hardlink-b.bin")
    os.link(special / "hardlink-a.bin", special / "hardlink-c.bin")
    (special / "hardlink-pair-1.txt").write_bytes(b"pair\n")
    os.link(special / "hardlink-pair-1.txt", medium / "hardlink-pair-2.txt")
    xfile = special / "xattr.bin"
    xfile.write_bytes(b"xattr roundtrip\n")
    os.setxattr(xfile, "user.restic-binary", b"\x00\xff<>&\"\n")
    os.setxattr(xfile, "user.restic-text", b"hello")
    os.setxattr(special, "user.dir-attr", b"on a directory")
    owned = special / "owned-1000.txt"
    owned.write_bytes(b"uid 1000 gid 1000\n")
    os.chown(owned, 1000, 1000)
    os.chmod(owned, 0o640)
    for mode in (0o000, 0o4755, 0o2755, 0o1777, 0o700):
        path = special / f"mode-{mode:04o}"
        path.write_bytes(b"mode\n")
        os.chmod(path, mode)
    os.mkfifo(special / "named-pipe")
    for name, mode in (("empty-dir", 0o755), ("empty-dir-0700", 0o700), ("sticky-dir", 0o1777), ("setgid-dir", 0o2775)):
        (special / name).mkdir()
        os.chmod(special / name, mode)
    deep = special
    for level in range(30):
        deep = deep / f"deep{level:02d}"
    deep.mkdir(parents=True)
    (deep / "bottom.txt").write_bytes(b"deep path\n")
    (special / "zero-length").write_bytes(b"")
    for root, dirnames, filenames in os.walk(special):
        for name in filenames:  # regular files, fifo and symlinks under special/ get fixed mtimes
            os.utime(Path(root) / name, (BASE_MTIME + 777, BASE_MTIME + 777), follow_symlinks=False)


def fix_mtimes(corpus_root: Path) -> dict:
    """Fix every directory and link mtime (post-order), so the manifest is fully deterministic; return manifest v1."""
    for root, dirnames, filenames in os.walk(corpus_root / "src", topdown=False):
        for name in filenames + dirnames:
            path = Path(root) / name
            if path.is_symlink():
                os.utime(path, (BASE_MTIME, BASE_MTIME), follow_symlinks=False)
        os.utime(root, (BASE_MTIME + 12345, BASE_MTIME + 12345))
    os.utime(corpus_root, (BASE_MTIME + 12345, BASE_MTIME + 12345))
    return manifest(corpus_root)


def finish(out: Path, corpus_root: Path, written: list, rng: random.Random, seed: int, log) -> dict:
    v1 = fix_mtimes(corpus_root)
    log("v1 manifest")

    # ---- mutation spec (applied in place to a tmpfs copy of v1; deterministic)
    mutation = out / "mutation"
    payload = mutation / "payload"
    payload.mkdir(parents=True)
    ops = []
    files = [p for p in sorted(written) if p.stat().st_size > 0]
    rel = lambda p: p.relative_to(corpus_root).as_posix()  # noqa: E731
    pick = rng.sample(files, int(len(files) * (PARAMS["mutation_modify_frac"] + PARAMS["mutation_append_frac"] + PARAMS["mutation_delete_frac"])))
    n_mod = int(len(files) * PARAMS["mutation_modify_frac"])
    n_app = int(len(files) * PARAMS["mutation_append_frac"])
    mtime2 = (BASE_MTIME + 86400 * 1000) * 10**9

    def store(name: str, data: bytes) -> str:
        (payload / name).write_bytes(data)
        return name

    for index, path in enumerate(pick):
        if index < n_mod:
            size = path.stat().st_size
            ops.append({"op": "write", "path": rel(path), "payload": store(f"mod-{index}", shake(f"mod/{seed}/{index}", size)), "mtime_ns": mtime2 + index})
        elif index < n_mod + n_app:
            ops.append({"op": "append", "path": rel(path), "payload": store(f"app-{index}", shake(f"app/{seed}/{index}", rng.randint(1, 8192))), "mtime_ns": mtime2 + index})
        else:
            ops.append({"op": "delete", "path": rel(path)})
    ops.append({"op": "rmtree", "path": "src/tree/pkg03"})
    ops.append({"op": "rename", "path": "src/tree/pkg05", "to": "src/tree/pkg05-renamed"})
    ops.append({"op": "mkdir", "path": "src/new-in-v2", "mode": 0o755})
    for index in range(PARAMS["mutation_new_files"]):
        size = small_size(rng)
        ops.append({"op": "write", "path": f"src/new-in-v2/n{index:05d}.dat", "payload": store(f"new-{index}", shake(f"new/{seed}/{index}", size)), "mtime_ns": mtime2 + index, "mode": 0o644})
    ops.append({"op": "write", "path": "src/large/new-64MiB.bin", "payload": store("new-large", shake(f"newlarge/{seed}", 64 * MIB)), "mtime_ns": mtime2, "mode": 0o644})
    ops.append({"op": "patch", "path": "src/large/big-1.bin", "offset": 77 * MIB + 12345, "payload": store("patch-big1", shake(f"patch/{seed}", MIB)), "mtime_ns": mtime2})
    ops.append({"op": "truncate", "path": "src/large/dedup-1.bin", "size": 96 * MIB, "mtime_ns": mtime2})
    remaining = [p for p in files if p not in set(pick)]
    for index, path in enumerate(rng.sample(remaining, PARAMS["mutation_chmod"])):
        if "tree/pkg03" in rel(path) or "tree/pkg05" in rel(path):
            continue
        ops.append({"op": "chmod", "path": rel(path), "mode": 0o640 if index % 2 else 0o755})
    ops.append({"op": "symlink", "path": "src/special/symlink-relative", "target": "../medium/m0003.txt", "mtime_ns": mtime2})
    ops.append({"op": "setxattr", "path": "src/special/xattr.bin", "name": "user.added-in-v2", "value_hex": b"v2".hex()})
    ops.append({"op": "dir_mtimes", "mtime_ns": mtime2 + 777})
    spec = {"version": WORKLOAD_VERSION, "ops": ops,
            "exclude_for_history_v3": ["/data/src/large/big-2.bin", "/data/src/tree/pkg07", "/data/src/medium"]}
    return freeze(out, corpus_root, v1, spec, WORKLOAD_VERSION, seed, PARAMS, log)


def freeze(out: Path, corpus_root: Path, v1: dict, spec: dict, version: str, seed: int, params: dict, log) -> dict:
    """Write the mutation spec, dry-run it on a tmpfs copy to freeze the v2 manifest, write manifests + workloads.json."""
    mutation = out / "mutation"
    atomic(mutation / "spec.json", spec)
    trial = Path("/dev/shm") / f"restic-v2-gen-{os.getpid()}"
    shutil.rmtree(trial, ignore_errors=True)
    try:
        import subprocess
        subprocess.run(["cp", "-a", "--reflink=never", str(corpus_root), str(trial)], check=True)
        if manifest(trial) != v1:
            raise RuntimeError("cp -a onto tmpfs does not reproduce the v1 manifest")
        apply_mutations(trial, mutation)
        v2 = manifest(trial)
    finally:
        shutil.rmtree(trial, ignore_errors=True)
    log("v2 manifest")
    atomic(out / "manifest-v1.json", v1)
    atomic(out / "manifest-v2.json", v2)
    description = {
        "version": version,
        "seed": seed,
        "params": params,
        "generated_at": now(),
        "generator_sha256": hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        "v1": {"sha256": canonical_sha(v1), **manifest_summary(v1)},
        "v2": {"sha256": canonical_sha(v2), **manifest_summary(v2)},
        "mutation": {"ops": len(spec["ops"]), "tree_sha256": tree_sha256_of_files(mutation)},
        "note": "corpus-v1 is copied (cp -a) onto tmpfs per judge run and must reproduce manifest v1 exactly",
    }
    atomic(out / "workloads.json", description)
    log("done")
    return description


# ============================================================ visible suite workloads (SUITE.md)
# big-index: the main workload's data unchanged, plus a "ballast" already in the pristine repository: ~1.5M tiny unique
# data blobs referenced by one old snapshot tagged "ballast" (written by judge/ballastgen, compiled inside a copy of the
# pinned stock tree, using restic's own repository/packer code). Every phase that loads the index carries all of it;
# forget-prune keeps it by tag. The phases, oracle and corpus are main's; only the starting repository differs.
BIG_INDEX_VERSION = "restic-v2-workload-big-index-1"
BIG_INDEX_PARAMS = {"ballast_blobs": 1_500_000, "ballast_blob_bytes": 48, "ballast_per_file": 2000,
                    "ballast_files_per_dir": 25, "ballast_seed": "restic-v2-ballast-1"}
BIG_INDEX_PHASE_ARGS = {"forget-prune": ["forget", "--keep-last", "1", "--keep-tag", "ballast", "--prune"]}


def generate_big_index(out: Path, base: Path, ballast_bin: Path, params: dict) -> dict:
    import subprocess
    if (out / "corpus-v1").exists():
        raise SystemExit(f"{out}/corpus-v1 exists; workloads are generated once and never regenerated")
    out.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()

    def log(stage: str) -> None:
        print(json.dumps({"timestamp": now(), "stage": stage, "elapsed_s": round(time.monotonic() - started, 1)}), flush=True)

    for name in ("corpus-v1", "mutation", "manifest-v1.json", "manifest-v2.json", "etc", "password", "pristine-repo"):
        subprocess.run(["cp", "-a", "--reflink=never", str(base / name), str(out / name)], check=True)
    log("copied main workload")
    run = subprocess.run([str(ballast_bin), "-repo", str(out / "pristine-repo"), "-password-file", str(out / "password"),
                          "-blobs", str(params["ballast_blobs"]), "-blob-bytes", str(params["ballast_blob_bytes"]),
                          "-per-file", str(params["ballast_per_file"]), "-files-per-dir", str(params["ballast_files_per_dir"]),
                          "-seed", params["ballast_seed"]],
                         capture_output=True, text=True, check=True, env={"GOMAXPROCS": "4", "PATH": "/usr/bin:/bin"})
    ballast = json.loads(run.stdout.strip().splitlines()[-1])
    log(f"ballast written: {ballast}")
    from restic_judge import load, sha, tree_files  # noqa: E402
    pristine = {"tree_sha256": tree_sha256_of_files(out / "pristine-repo"), "files": tree_files(out / "pristine-repo"),
                "base_pristine_tree_sha256": load(base / "pristine.json")["tree_sha256"], "ballast": ballast,
                "ballastgen_sha256": sha(ballast_bin), "created_at": now()}
    atomic(out / "pristine.json", pristine)
    description = load(base / "workloads.json")
    description.update(
        version=BIG_INDEX_VERSION, params=params, generated_at=now(),
        generator_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        base_workload={"root": str(base), "version": description.get("version"),
                       "workloads_json_sha256": sha(base / "workloads.json")},
        phase_args=BIG_INDEX_PHASE_ARGS, ballast=ballast,
        note="main corpus and mutation set; the pristine repository additionally holds the ballast snapshot "
             "(tag ballast, path /ballast, time 2020-01-01) and its blobs")
    atomic(out / "workloads.json", description)
    log("done")
    return description


# remote: the main workload unchanged (byte copies of its corpus, mutation set, manifests and pristine repository), but
# the measured restic reaches the repository through restic's REST backend, served by judge/restserver (compiled once
# with the judge toolchain, frozen into <out>/bin/) with a simulated network. The profile below is part of the workload
# identity (workloads.json "remote"); the judge reads it from there (REMOTE.md).
# Profile v2 (coordinator 2026-10-04, scored from S019): 30 ms per request, 48 MiB/s per connection, 5 connections.
# v1 was 12 MiB/s: 73% of a pass was a transfer floor no code change can touch. At 48 MiB/s the floor is a quarter of
# that, so latency x request count, connection use and CPU are the terms a team can move. A profile change is a new
# workload version: results of different versions are never pooled.
REMOTE_VERSION = "restic-v2-workload-remote-2"
REMOTE_PROFILE = {"latency_ms": 30.0, "rate_bytes_per_second": 48 * 1024 * 1024, "max_connections": 5}


def generate_remote(out: Path, base: Path, server_bin: Path, server_source: Path | None, profile: dict) -> dict:
    import subprocess
    if (out / "corpus-v1").exists():
        raise SystemExit(f"{out}/corpus-v1 exists; workloads are generated once and never regenerated")
    out.mkdir(parents=True, exist_ok=True)
    for name in ("corpus-v1", "mutation", "manifest-v1.json", "manifest-v2.json", "etc", "password", "pristine-repo",
                 "pristine.json"):
        subprocess.run(["cp", "-a", "--reflink=never", str(base / name), str(out / name)], check=True)
    (out / "bin").mkdir(exist_ok=True)
    subprocess.run(["cp", "--reflink=never", str(server_bin), str(out / "bin" / "restserver")], check=True)
    os.chmod(out / "bin" / "restserver", 0o555)
    from restic_judge import load, sha  # noqa: E402
    description = load(base / "workloads.json")
    description.update(
        version=REMOTE_VERSION, generated_at=now(),
        generator_sha256=hashlib.sha256(Path(__file__).read_bytes()).hexdigest(),
        base_workload={"root": str(base), "version": description.get("version"),
                       "workloads_json_sha256": sha(base / "workloads.json")},
        remote={**profile, "backend": "rest", "transport": "unix socket bound into the sandbox work dir",
                "server_sha256": sha(out / "bin" / "restserver"),
                "server_source_sha256": sha(server_source) if server_source else None},
        note="main corpus, mutation set and pristine repository (byte copies); the measured restic reaches the "
             "repository through the REST backend served by bin/restserver with the frozen network profile")
    atomic(out / "workloads.json", description)
    return description


# large-files: a few big files (sparse VM image with scattered rewrites, page-structured DB file with page rewrites and
# appended pages, compressible append-only logs, incompressible media files) plus a small tree and the special block.
# v1 -> v2 exercises chunk reuse: in-place rewrites and appends inside big files, a truncated media file, a renamed one.
LARGE_FILES_VERSION = "restic-v2-workload-large-files-1"
LARGE_FILES_PARAMS = {
    "vm_image_mib": 1536, "vm_gap_mib": [4, 40], "vm_extent_mib": [1, 8], "vm_rewrites": 300, "vm_rewrite_kib": 64,
    "vm_new_extent_mib": 32,
    "db_mib": 256, "db_page_bytes": 8192, "db_page_rewrites": 2000, "db_append_mib": 16,
    "log_files": 4, "log_mib": 48, "log_append_mib": [2, 6],
    "media_files": 3, "media_mib": 256, "media_truncate_mib": 200,
    "small_files": 3000, "small_dirs": 60, "medium_files": 8,
    "mutation_modify_frac": 0.05, "mutation_append_frac": 0.02, "mutation_delete_frac": 0.01,
    "mutation_new_files": 200, "mutation_chmod": 30,
    "history": [{"label": "v2", "exclude": []},
                {"label": "v3", "exclude": ["/data/src/media/media-0.mkv", "/data/src/logs"]}],
    "min_unmodified_v2": 1000,
}


def generate_large_files(out: Path, seed: int, base: Path) -> dict:
    import subprocess
    p = LARGE_FILES_PARAMS
    rng = random.Random(seed)
    text = Text(random.Random(seed + 1))
    corpus_root = out / "corpus-v1"
    if corpus_root.exists():
        raise SystemExit(f"{corpus_root} exists; workloads are generated once and never regenerated")
    out.mkdir(parents=True, exist_ok=True)
    for name in ("etc", "password", "pristine-repo", "pristine.json"):  # same restic init + sandbox files as main
        subprocess.run(["cp", "-a", "--reflink=never", str(base / name), str(out / name)], check=True)
    src = corpus_root / "src"
    src.mkdir(parents=True)
    started = time.monotonic()

    def log(stage: str) -> None:
        print(json.dumps({"timestamp": now(), "stage": stage, "elapsed_s": round(time.monotonic() - started, 1)}), flush=True)

    # ---- small tree
    dirs = [Path("tree") / f"d{n:02d}" / rng.choice(["src", "lib", "doc", "cfg"]) for n in range(p["small_dirs"])]
    for d in dirs:
        (src / d).mkdir(parents=True, exist_ok=True)
    written = []
    for n in range(p["small_files"]):
        path = src / rng.choice(dirs) / f"f{n:05d}{rng.choice(['.c', '.h', '.txt', '.json', '.bin', ''])}"
        size = small_size(rng)
        path.write_bytes(text.make(rng, size, f"// file {n}") if rng.random() < 0.6 else shake(f"lf-small/{seed}/{n}", size))
        os.chmod(path, rng.choice([0o644, 0o644, 0o600, 0o755]))
        mtime = BASE_MTIME + rng.randrange(0, 86400 * 900)
        os.utime(path, ns=(mtime * 10**9 + rng.randrange(10**9), mtime * 10**9 + rng.randrange(10**9)))
        written.append(path)
    medium = src / "medium"
    medium.mkdir()
    for n in range(p["medium_files"]):
        size = int(256 * 1024 * (8 ** rng.random()))
        path = medium / f"m{n:04d}.{'txt' if n % 2 else 'bin'}"
        path.write_bytes(text.make(rng, size, f"medium {n}") if n % 2 else shake(f"lf-medium/{seed}/{n}", size))
        os.utime(path, (BASE_MTIME + n, BASE_MTIME + n))
    log("small tree + medium files")
    # ---- sparse VM image: holes + random extents
    (src / "vm").mkdir()
    image = src / "vm" / "disk.img"
    size = p["vm_image_mib"] * MIB
    holes = []
    with image.open("wb") as stream:
        position = 0
        while position < size:
            gap = rng.randint(*p["vm_gap_mib"]) * MIB
            holes.append((position, gap))
            position += gap
            run = min(rng.randint(*p["vm_extent_mib"]) * MIB, max(0, size - position))
            stream.seek(position)
            for offset in range(0, run, MIB):
                stream.write(shake(f"lf-vm/{seed}/{position + offset}", MIB))
            position += run
        stream.truncate(size)
    os.utime(image, (BASE_MTIME, BASE_MTIME))
    # ---- page-structured DB file
    (src / "db").mkdir()
    page = p["db_page_bytes"]

    def db_page(number: int, generation: int) -> bytes:
        head = number.to_bytes(4, "little") + generation.to_bytes(8, "little")
        body = text.make(rng, int(page * rng.uniform(0.55, 0.9)), f"page {number} gen {generation}")
        return (head + body).ljust(page, b"\0")[:page]

    database = src / "db" / "main.db"
    with database.open("wb") as stream:
        for number in range(p["db_mib"] * MIB // page):
            stream.write(db_page(number, 1))
    os.utime(database, (BASE_MTIME, BASE_MTIME))
    # ---- compressible logs, incompressible media
    (src / "logs").mkdir()
    logs = []
    for n in range(p["log_files"]):
        path = src / "logs" / f"app-{n}.log"
        path.write_bytes(text.make(rng, p["log_mib"] * MIB, f"log {n} start"))
        os.utime(path, (BASE_MTIME + n, BASE_MTIME + n))
        logs.append(path)
    (src / "media").mkdir()
    for n in range(p["media_files"]):
        write_big_random(src / "media" / f"media-{n}.mkv", p["media_mib"] * MIB, f"lf-media/{seed}/{n}")
        os.utime(src / "media" / f"media-{n}.mkv", (BASE_MTIME, BASE_MTIME))
    log("vm image, db, logs, media")
    write_special(src, medium, seed)
    log("special entries")
    v1 = fix_mtimes(corpus_root)
    log("v1 manifest")

    # ---- mutation spec (v2)
    payload = out / "mutation" / "payload"
    payload.mkdir(parents=True)
    ops = []
    rel = lambda path: path.relative_to(corpus_root).as_posix()  # noqa: E731
    mtime2 = (BASE_MTIME + 86400 * 1000) * 10**9

    def store(name: str, data: bytes) -> str:
        (payload / name).write_bytes(data)
        return name

    files = [path for path in written if path.stat().st_size > 0]
    n_mod, n_app = int(len(files) * p["mutation_modify_frac"]), int(len(files) * p["mutation_append_frac"])
    n_del = int(len(files) * p["mutation_delete_frac"])
    pick = rng.sample(files, n_mod + n_app + n_del)
    for k, path in enumerate(pick):
        if k < n_mod:
            ops.append({"op": "write", "path": rel(path), "mtime_ns": mtime2 + k,
                        "payload": store(f"mod-{k}", shake(f"lf-mod/{seed}/{k}", path.stat().st_size))})
        elif k < n_mod + n_app:
            ops.append({"op": "append", "path": rel(path), "mtime_ns": mtime2 + k,
                        "payload": store(f"app-{k}", shake(f"lf-app/{seed}/{k}", rng.randint(1, 8192)))})
        else:
            ops.append({"op": "delete", "path": rel(path)})
    chunk = p["vm_rewrite_kib"] * 1024
    for n in range(p["vm_rewrites"]):  # scattered in-place writes, some into holes
        offset = rng.randrange(0, size - chunk) // 4096 * 4096
        ops.append({"op": "patch", "path": rel(image), "offset": offset, "mtime_ns": mtime2,
                    "payload": store(f"vm-{n}", shake(f"lf-vmpatch/{seed}/{n}", chunk))})
    big_holes = [h for h in holes if h[1] > p["vm_new_extent_mib"] * MIB and h[0] + h[1] <= size]
    hole = big_holes[len(big_holes) // 2]
    ops.append({"op": "patch", "path": rel(image), "offset": hole[0] + MIB, "mtime_ns": mtime2,
                "payload": store("vm-extent", shake(f"lf-vmextent/{seed}", p["vm_new_extent_mib"] * MIB))})
    pages = p["db_mib"] * MIB // page
    for n, number in enumerate(sorted(rng.sample(range(pages), p["db_page_rewrites"]))):
        ops.append({"op": "patch", "path": rel(database), "offset": number * page, "mtime_ns": mtime2,
                    "payload": store(f"db-{n}", db_page(number, 2))})
    ops.append({"op": "append", "path": rel(database), "mtime_ns": mtime2,
                "payload": store("db-append", b"".join(db_page(pages + n, 2) for n in range(p["db_append_mib"] * MIB // page)))})
    for n, path in enumerate(logs):
        ops.append({"op": "append", "path": rel(path), "mtime_ns": mtime2 + n,
                    "payload": store(f"log-{n}", text.make(rng, rng.randint(*p["log_append_mib"]) * MIB, f"log {n} v2"))})
    ops.append({"op": "truncate", "path": "src/media/media-1.mkv", "size": p["media_truncate_mib"] * MIB, "mtime_ns": mtime2})
    ops.append({"op": "mkdir", "path": "src/archive", "mode": 0o755})
    ops.append({"op": "rename", "path": "src/media/media-2.mkv", "to": "src/archive/media-2-2025.mkv"})
    ops.append({"op": "mkdir", "path": "src/new-in-v2", "mode": 0o755})
    for n in range(p["mutation_new_files"]):
        ops.append({"op": "write", "path": f"src/new-in-v2/n{n:05d}.dat", "mtime_ns": mtime2 + n, "mode": 0o644,
                    "payload": store(f"new-{n}", shake(f"lf-new/{seed}/{n}", small_size(rng)))})
    chosen = set(pick)
    for n, path in enumerate(rng.sample([f for f in files if f not in chosen], p["mutation_chmod"])):
        ops.append({"op": "chmod", "path": rel(path), "mode": 0o640 if n % 2 else 0o755})
    ops.append({"op": "symlink", "path": "src/special/symlink-relative", "target": "../medium/m0003.txt", "mtime_ns": mtime2})
    ops.append({"op": "setxattr", "path": "src/special/xattr.bin", "name": "user.added-in-v2", "value_hex": b"v2".hex()})
    ops.append({"op": "dir_mtimes", "mtime_ns": mtime2 + 777})
    log(f"mutation spec: {len(ops)} ops")
    spec = {"version": LARGE_FILES_VERSION, "ops": ops, "history": p["history"]}
    description = freeze(out, corpus_root, v1, spec, LARGE_FILES_VERSION, seed, p, log)
    description["min_unmodified_v2"] = p["min_unmodified_v2"]
    atomic(out / "workloads.json", description)
    return description


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--out", type=Path, required=True)
    parser.add_argument("--profile", choices=["v1", "big-index", "large-files", "remote"], default="v1")
    parser.add_argument("--server-bin", type=Path, help="remote: the compiled judge/restserver binary")
    parser.add_argument("--server-source", type=Path, help="remote: restserver/main.go (its sha256 is recorded)")
    parser.add_argument("--latency-ms", type=float, help="remote: first-byte delay per request (default 30)")
    parser.add_argument("--rate-mib", type=float, help="remote: MiB/s per connection, both directions (default 48)")
    parser.add_argument("--max-connections", type=int, help="remote: requests in flight at once (default 5)")
    parser.add_argument("--ballast-bin", type=Path, help="big-index: the compiled judge/ballastgen binary")
    parser.add_argument("--ballast-blobs", type=int, help="big-index: override the ballast blob count")
    parser.add_argument("--seed", type=int, help="v1 default 20260924")
    parser.add_argument("--base", type=Path, default=Path("/opt/league/restic-league-v2/workloads"),
                        help="main workload dir to copy etc/, password, pristine-repo, pristine.json from")
    args = parser.parse_args()
    if os.geteuid() != 0:
        parser.error("run as root (chown/xattr fixtures)")
    if args.profile == "big-index":
        if args.ballast_bin is None:
            parser.error("--profile big-index needs --ballast-bin")
        params = dict(BIG_INDEX_PARAMS, **({"ballast_blobs": args.ballast_blobs} if args.ballast_blobs else {}))
        print(json.dumps(generate_big_index(args.out, args.base, args.ballast_bin, params), indent=2)[:3000])
        return 0
    if args.profile == "remote":
        if args.server_bin is None:
            parser.error("--profile remote needs --server-bin")
        profile = dict(REMOTE_PROFILE)
        if args.latency_ms is not None:
            profile["latency_ms"] = float(args.latency_ms)
        if args.rate_mib is not None:
            profile["rate_bytes_per_second"] = int(args.rate_mib * 1024 * 1024)
        if args.max_connections is not None:
            profile["max_connections"] = int(args.max_connections)
        print(json.dumps(generate_remote(args.out, args.base, args.server_bin, args.server_source, profile), indent=2)[:3000])
        return 0
    if args.profile == "large-files":
        print(json.dumps(generate_large_files(args.out, 20261003 if args.seed is None else args.seed, args.base), indent=2))
        return 0
    print(json.dumps(generate(args.out, 20260924 if args.seed is None else args.seed), indent=2))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
