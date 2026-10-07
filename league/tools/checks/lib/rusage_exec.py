#!/usr/bin/env python3
"""rusage_exec.py REPORT CMD...   run CMD as a child of this small process and write its exit code and rusage as JSON.

Why a helper: on Linux a child's ru_maxrss starts at the resident size of the process that forked it (the kernel
keeps the high-water mark of the old address space across exec). A check script that holds a large manifest in
memory would otherwise report its own size as the peak RSS of every program it starts. This helper is about 10 MiB."""
import json
import os
import subprocess
import sys
import time

report = sys.argv[1]
began = time.monotonic()
p = subprocess.Popen(sys.argv[2:])
_, status, usage = os.wait4(p.pid, 0)
rc = os.waitstatus_to_exitcode(status)
with open(report + '.tmp', 'w') as f:
    json.dump({'rc': rc, 'seconds': time.monotonic() - began, 'rss_kib': usage.ru_maxrss, 'user': usage.ru_utime, 'sys': usage.ru_stime}, f)
os.replace(report + '.tmp', report)
sys.exit(rc if 0 <= rc < 256 else 1)
