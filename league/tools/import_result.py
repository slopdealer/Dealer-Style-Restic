#!/usr/bin/env python3
"""Copy one judge result into league/evidence/judge/ and record it in the index.

    python league/tools/import_result.py SOURCE_RESULT_JSON NAME [BOX_PREFIX]

The file is the judge's result.json for one job. The copy differs from the source in three ways only:
the key `local_evidence` (a path on the workstation that fetched the result) is removed; the JSON is
written with LF line ends and two-space indentation; and, when BOX_PREFIX is given, that prefix of the
league box's working directory is replaced by /opt/league/ in every path (the rule of every published
copy, see league/evidence/README.md). The SHA-256 of the SOURCE file and the number of replaced
prefixes are recorded in league/evidence/judge/index.json so that the copy can be matched with the
original.

Paths that start with /opt/ inside the result are the league box's layout.
"""
import hashlib
import json
import sys
from pathlib import Path

HERE = Path(__file__).resolve().parent
JUDGE = HERE.parent / "evidence" / "judge"
REMOVED_KEYS = ["local_evidence"]
NEW_PREFIX = "/opt/league/"


def label(path):
    return path.rstrip("/").split("/")[-1] if path else None


def main():
    if len(sys.argv) not in (3, 4):
        print(__doc__)
        return 2
    src, name = Path(sys.argv[1]), sys.argv[2]
    prefix = sys.argv[3] if len(sys.argv) == 4 else None
    raw = src.read_bytes()
    text = raw.decode("utf-8")
    replaced = text.count(prefix) if prefix else 0
    if prefix:
        text = text.replace(prefix, NEW_PREFIX)
    data = json.loads(text)
    removed = [k for k in REMOVED_KEYS if data.pop(k, None) is not None]
    JUDGE.mkdir(parents=True, exist_ok=True)
    out = JUDGE / f"{name}.result.json"
    out.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n", encoding="utf-8", newline="\n")
    index_path = JUDGE / "index.json"
    index = json.loads(index_path.read_text(encoding="utf-8")) if index_path.exists() else {"jobs": {}}
    arms = data.get("arms") or {}
    comp = data.get("comparisons") or {}
    index["jobs"][name] = {
        "file": out.name,
        "job_id": data.get("job_id"),
        "mode": data.get("mode"),
        "rounds": data.get("rounds"),
        "status": data.get("status"),
        "started_at": data.get("started_at"),
        "completed_at": data.get("completed_at"),
        "candidate": label(((arms.get("candidate") or {}).get("source") or {}).get("path")),
        "candidate_source_sha256": ((arms.get("candidate") or {}).get("source") or {}).get("sha256"),
        "incumbent": label(((arms.get("incumbent") or {}).get("source") or {}).get("path")),
        "has_comparisons": bool(comp),
        "source_file_sha256": hashlib.sha256(raw).hexdigest(),
        "keys_removed": removed,
        "box_prefix_replaced": replaced,
    }
    index["jobs"] = dict(sorted(index["jobs"].items()))
    index_path.write_text(json.dumps(index, indent=1) + "\n", encoding="utf-8", newline="\n")
    print(f"imported {name}: job {data.get('job_id')}, {data.get('rounds')} rounds, status {data.get('status')}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
