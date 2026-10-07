#!/usr/bin/env python3
# PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
"""Shared library of the restic release checks (final-candidate/checks).

Every check is `python3 cNN_name.py <tree> <label>`. Results go to checks/results/<label>/<check>/ (progress.log,
status.json, summary.json, evidence files) and one line per finished check is appended to
checks/results/<label>/status.txt. Bulk data lives under /home/restic-final-checks/<label>/<check>/ and is removed
when the check passes.

Rules built in here: every process is pinned to the check CPUs (affinity is set at import and inherited), the trees
under test are never written to (they are copied to scratch first), builds use the league's pinned offline toolchain.
"""
import datetime
import fcntl
import hashlib
import json
import os
import random
import re
import shutil
import signal
import stat
import subprocess
import sys
import threading
import time
from pathlib import Path

ALLOWED_CPUS = [10, 11, 16, 17, 18, 23, 46, 47, 58, 59, 64, 65, 66, 71, 94, 95]
# CHK_CPUS narrows the set (run_all gives each lane half of the cores); it can never widen it.
CPUS = sorted(set(int(x) for x in os.environ.get('CHK_CPUS', '').replace(',', ' ').split()) & set(ALLOWED_CPUS)) or ALLOWED_CPUS
CPUSET = ','.join(str(x) for x in CPUS)
os.sched_setaffinity(0, CPUS)  # inherited by every child process and thread started from here

CHECKS = Path(__file__).resolve().parents[1]
LEAGUE = Path('/opt/league/restic-league-v2')
STOCK_TREE = LEAGUE / 'stock'
BEFORE_TREE = Path(os.environ.get('CHK_BEFORE_TREE', str(
    LEAGUE / 'league/space-bunny/restic-v2/workspace/incumbents/S019-merge')))
TOOLCHAIN = LEAGUE / 'toolchain'
SCRATCH = Path(os.environ.get('CHK_SCRATCH', '/home/restic-final-checks'))
BUILD = SCRATCH / '_build'
TAGS = 'selfupdate disable_grpc_modules'
PASSWORD = 'chk'
QUICK = os.environ.get('CHK_QUICK') == '1'
NOBODY = 65534


def now():
    return datetime.datetime.now(datetime.timezone.utc).strftime('%Y-%m-%dT%H:%M:%SZ')


def atomic_write(path, text):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(path.name + '.tmp%d' % os.getpid())
    tmp.write_text(text, encoding='utf-8')
    os.replace(tmp, path)


def append(path, line):
    path = Path(path)
    path.parent.mkdir(parents=True, exist_ok=True)
    with open(path, 'a', encoding='utf-8') as f:
        f.write(line.rstrip('\n') + '\n')


def envint(name, default):
    try:
        return int(os.environ.get(name, default))
    except ValueError:
        return default


# ---------------------------------------------------------------- toolchain and builds

def go_env(cgo=False, goarch='amd64', tmp=None, extra=None):
    tmp = Path(tmp or (SCRATCH / '_tmp'))
    tmp.mkdir(parents=True, exist_ok=True)
    (SCRATCH / '_home').mkdir(parents=True, exist_ok=True)
    shim = SCRATCH / '_shim'          # two stock tests call `python`; the box only has python3
    shim.mkdir(parents=True, exist_ok=True)
    if not (shim / 'python').exists():
        try:
            os.symlink('/usr/bin/python3', shim / 'python')
        except FileExistsError:
            pass
    env = {'PATH': '%s/go/bin:/usr/bin:/bin:%s' % (TOOLCHAIN, shim), 'GOROOT': '%s/go' % TOOLCHAIN, 'GOTOOLCHAIN': 'local',
           'GOMODCACHE': '%s/gomodcache' % TOOLCHAIN, 'GOCACHE': str(SCRATCH / '_gocache'), 'GOFLAGS': '-mod=readonly',
           'GOPROXY': 'off', 'GOSUMDB': 'off', 'GONOSUMCHECK': '1', 'GOTELEMETRY': 'off',
           'CGO_ENABLED': '1' if cgo else '0', 'GOOS': 'linux', 'GOARCH': goarch, 'HOME': str(SCRATCH / '_home'),
           'TMPDIR': str(tmp), 'TZ': 'UTC', 'USER': 'root', 'LC_ALL': 'C'}
    if goarch == 'amd64':
        env['GOAMD64'] = 'v1'
    env.update(extra or {})
    return env


_SHORT_TMPS = []


def short_tmp(*parts):
    """A fresh directory with a short path: unix sockets created by tests must fit in 107 bytes."""
    name = hashlib.sha256('/'.join(str(p) for p in parts).encode()).hexdigest()[:10]
    path = SCRATCH / 't' / name
    rmtree(path)
    path.mkdir(parents=True)
    _SHORT_TMPS.append(path)
    return path


def run(cmd, env=None, cwd=None, timeout=None, stdout=None, stderr=None, stdin=None, user=None):
    """Run to completion; returns (rc, out, err, seconds). rc is -9 with err 'TIMEOUT' when the limit was hit.
    user: numeric uid to run as (its gid is the same number, no supplementary groups)."""
    began = time.monotonic()
    identity = {} if user is None else {'user': user, 'group': user, 'extra_groups': []}
    p = subprocess.Popen([str(c) for c in cmd], env=env, cwd=str(cwd) if cwd else None,
                         stdin=stdin if stdin is not None else subprocess.DEVNULL,
                         stdout=stdout if stdout is not None else subprocess.PIPE,
                         stderr=stderr if stderr is not None else subprocess.PIPE, start_new_session=True, **identity)
    try:
        out, err = p.communicate(timeout=timeout)
        rc = p.returncode
    except subprocess.TimeoutExpired:
        try:
            os.killpg(p.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        out, err = p.communicate()
        rc = -9
        err = (err or b'') + b'\nTIMEOUT after %d s' % int(timeout)
    dec = lambda b: b.decode('utf-8', 'surrogateescape') if b is not None else ''
    return rc, dec(out), dec(err), time.monotonic() - began


def tree_identity(tree):
    tree = Path(tree)
    h = hashlib.sha256()
    names = []
    for top in ('cmd', 'internal', 'helpers'):
        for dirpath, dirnames, filenames in os.walk(tree / top):
            dirnames.sort()
            for name in sorted(filenames):
                names.append(os.path.join(dirpath, name))
    for name in ('go.mod', 'go.sum', 'build.go', 'doc.go', 'VERSION'):
        if (tree / name).is_file():
            names.append(str(tree / name))
    for name in sorted(names):
        with open(name, 'rb') as f:
            digest = hashlib.sha256(f.read()).hexdigest()
        h.update(('%s %s\n' % (os.path.relpath(name, tree), digest)).encode())
    return h.hexdigest()


class Built:
    def __init__(self, name, tree, src, binary, identity):
        self.name, self.tree, self.src, self.bin, self.identity = name, Path(tree), Path(src), Path(binary), identity


def copy_tree(tree, dst):
    dst = Path(dst)
    dst.mkdir(parents=True, exist_ok=True)
    rc, out, err, _ = run(['rsync', '-a', '--delete', '--exclude', '.git', '--exclude', '*-evidence*',
                           '--exclude', '*.orig', str(tree) + '/', str(dst) + '/'])
    if rc:
        raise RuntimeError('copy of tree failed: ' + err[-500:])


def prepare(name, tree):
    """Copy a tree to scratch and build its restic binary (cached by content identity). Never writes to `tree`."""
    tree = Path(tree).resolve()
    base = BUILD / name
    base.mkdir(parents=True, exist_ok=True)
    with open(base / 'lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        identity = tree_identity(tree)
        marker = base / 'identity'
        binary = base / 'restic'
        if marker.is_file() and marker.read_text().strip() == identity and binary.is_file():
            return Built(name, tree, base / 'src', binary, identity)
        copy_tree(tree, base / 'src')
        rc, out, err, secs = run(['go', 'build', '-trimpath', '-buildvcs=false', '-tags', TAGS, '-ldflags', '-s -w',
                                  '-o', str(binary) + '.new', './cmd/restic'], env=go_env(), cwd=base / 'src',
                                 timeout=1800)
        (base / 'build.log').write_text(out + err)
        if rc:
            raise RuntimeError('build of %s failed: %s' % (name, err[-1500:]))
        os.replace(str(binary) + '.new', binary)
        marker.write_text(identity + '\n')
        return Built(name, tree, base / 'src', binary, identity)


def prepare_386(built):
    """The same tree as a 32-bit binary (GOARCH=386), built in the scratch copy `prepare` made."""
    base = BUILD / built.name
    with open(base / 'lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        binary = base / 'restic386'
        marker = base / 'identity386'
        if marker.is_file() and marker.read_text().strip() == built.identity and binary.is_file():
            return binary
        rc, out, err, _ = run(['go', 'build', '-trimpath', '-buildvcs=false', '-tags', TAGS, '-ldflags', '-s -w', '-o', str(binary), './cmd/restic'],
                              env=go_env(goarch='386'), cwd=built.src, timeout=1800)
        if rc:
            raise RuntimeError('386 build of %s failed: %s' % (built.name, err[-1500:]))
        marker.write_text(built.identity + '\n')
        return binary


def prepare_stock():
    return prepare('_stock', STOCK_TREE)


def prepare_before():
    return prepare('_before', BEFORE_TREE)


def _build_tool(name, source_dir, in_tree=None):
    base = BUILD / name
    base.mkdir(parents=True, exist_ok=True)
    with open(base / 'lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        h = hashlib.sha256()
        for path in sorted(Path(source_dir).glob('*')):
            if path.is_file():
                h.update(path.name.encode() + path.read_bytes())
        identity = h.hexdigest()
        binary = base / name.strip('_')
        marker = base / 'identity'
        if marker.is_file() and marker.read_text().strip() == identity and binary.is_file():
            return binary
        if in_tree is None:
            src = base / 'src'
            shutil.rmtree(src, ignore_errors=True)
            shutil.copytree(source_dir, src)
            cmd, cwd = ['go', 'build', '-trimpath', '-o', str(binary), '.'], src
        else:
            copy_tree(in_tree, base / 'src')
            target = base / 'src' / 'cmd' / name.strip('_')
            shutil.rmtree(target, ignore_errors=True)
            shutil.copytree(source_dir, target)
            cmd, cwd = ['go', 'build', '-trimpath', '-tags', TAGS, '-o', str(binary), './cmd/' + name.strip('_')], base / 'src'
        rc, out, err, _ = run(cmd, env=go_env(), cwd=cwd, timeout=1800)
        (base / 'build.log').write_text(out + err)
        if rc:
            raise RuntimeError('build of %s failed: %s' % (name, err[-2500:]))
        marker.write_text(identity + '\n')
        return binary


def chkrest_bin():
    return _build_tool('_chkrest', CHECKS / 'server' / 'chkrest')


def chktool_bin():
    """Stock restic plus two helper commands (tools/chktool), built in a scratch copy of the STOCK tree."""
    base = BUILD / '_chktool'
    base.mkdir(parents=True, exist_ok=True)
    source_dir = CHECKS / 'tools' / 'chktool'
    with open(base / 'lock', 'w') as lock:
        fcntl.flock(lock, fcntl.LOCK_EX)
        h = hashlib.sha256(tree_identity(STOCK_TREE).encode())
        for path in sorted(source_dir.glob('*.go')):
            h.update(path.name.encode() + path.read_bytes())
        identity = h.hexdigest()
        binary = base / 'chktool'
        marker = base / 'identity'
        if marker.is_file() and marker.read_text().strip() == identity and binary.is_file():
            return binary
        copy_tree(STOCK_TREE, base / 'src')
        for path in sorted(source_dir.glob('*.go')):
            package = 'internal/repository' if path.name.startswith('repository_') else 'cmd/restic'
            shutil.copyfile(path, base / 'src' / package / ('zz_' + path.name))
        main = base / 'src' / 'cmd' / 'restic' / 'main.go'
        text = main.read_text()
        anchor = '\t\tnewRecoverCommand(globalOptions),\n'
        if anchor not in text:
            raise RuntimeError('chktool: cannot find the command list in stock main.go')
        main.write_text(text.replace(anchor, anchor + '\t\tnewChkSaveTreeCommand(globalOptions),\n\t\tnewChkFakeIndexCommand(globalOptions),\n'))
        rc, out, err, _ = run(['go', 'build', '-trimpath', '-buildvcs=false', '-tags', TAGS, '-o', str(binary), './cmd/restic'],
                              env=go_env(), cwd=base / 'src', timeout=1800)
        (base / 'build.log').write_text(out + err)
        if rc:
            raise RuntimeError('build of chktool failed: %s' % err[-2500:])
        marker.write_text(identity + '\n')
        return binary


# ---------------------------------------------------------------- check bookkeeping

class Check:
    def __init__(self, cid, title=''):
        if len(sys.argv) < 3:
            print('usage: %s <tree> <label>' % sys.argv[0])
            sys.exit(2)
        self.id = cid
        self.title = title
        self.tree = Path(sys.argv[1]).resolve()
        self.label = sys.argv[2]
        self.root = CHECKS / 'results' / self.label
        self.res = self.root / cid
        self.res.mkdir(parents=True, exist_ok=True)
        self.work = SCRATCH / self.label / cid
        if self.work.exists():
            rmtree(self.work)
        self.work.mkdir(parents=True)
        self.started = time.time()
        self.fails = []
        self.notes = []
        self.numbers = {}
        self.lock = threading.Lock()
        self.done = 0
        self.total = 0
        self.stage = 'start'
        self.log('START %s tree=%s label=%s quick=%s' % (cid, self.tree, self.label, QUICK))
        self.status('start')

    def log(self, msg):
        line = '%s %s' % (now(), msg)
        with self.lock:
            append(self.res / 'progress.log', line)
        print(line, flush=True)

    def status(self, stage=None, done=None, total=None, state='running'):
        with self.lock:
            if stage is not None:
                self.stage = stage
            if done is not None:
                self.done = done
            if total is not None:
                self.total = total
            atomic_write(self.res / 'status.json', json.dumps({
                'check': self.id, 'status': state, 'stage': self.stage, 'units_done': self.done,
                'units_total': self.total, 'started_at': self.started, 'updated_at': time.time(),
                'fails': len(self.fails), 'pid': os.getpid()}, indent=1) + '\n')

    def tick(self, n=1):
        with self.lock:
            self.done += n
        self.status()

    def fail(self, what, tree=None, repro='', evidence='', stock=''):
        record = {'check': self.id, 'what': what, 'tree': tree or str(self.tree), 'repro': repro,
                  'evidence': str(evidence), 'stock': stock, 'at': now()}
        with self.lock:
            self.fails.append(record)
            append(self.root / 'FAILURES.jsonl', json.dumps(record))
        self.log('FAIL: %s | stock: %s | evidence: %s | repro: %s' % (what, stock, evidence, repro))

    def note(self, text):
        with self.lock:
            self.notes.append(text)
        self.log('NOTE: ' + text)

    def finish(self, summary='', verdict=None, keep=False):
        if verdict is None:
            verdict = 'FAIL' if self.fails else 'PASS'
        seconds = int(time.time() - self.started)
        atomic_write(self.res / 'summary.json', json.dumps({
            'check': self.id, 'title': self.title, 'verdict': verdict, 'summary': summary, 'tree': str(self.tree),
            'label': self.label, 'seconds': seconds, 'numbers': self.numbers, 'fails': self.fails,
            'notes': self.notes, 'finished_at': now()}, indent=1, default=str) + '\n')
        append(self.root / 'status.txt', '%s %s %s | %s | %d s | evidence=%s' % (
            now(), self.id, verdict, summary, seconds, self.res))
        self.status('finished', state=verdict)
        self.log('END %s %s %s' % (self.id, verdict, summary))
        if not keep and not self.fails and os.environ.get('CHK_KEEP') != '1':
            rmtree(self.work)
        for path in _SHORT_TMPS:
            rmtree(path)
        return 0 if verdict in ('PASS', 'SKIPPED') else 1


def rmtree(path):
    path = Path(path)
    if not path.exists() and not path.is_symlink():
        return
    def onerror(func, p, exc):
        try:
            os.chmod(os.path.dirname(p), 0o700)
            os.chmod(p, 0o700)
            func(p)
        except OSError:
            pass
    shutil.rmtree(path, onerror=onerror)


# ---------------------------------------------------------------- running restic

class R:
    def __init__(self, rc, out, err, secs, cmd):
        self.rc, self.out, self.err, self.secs, self.cmd = rc, out, err, secs, cmd

    @property
    def text(self):
        return self.out + self.err

    def brief(self, n=400):
        return 'rc=%s %s' % (self.rc, (self.err.strip() or self.out.strip())[-n:].replace('\n', ' | '))


def restic_env(work, extra=None):
    work = Path(work)
    for sub in ('home', 'tmp'):
        (work / sub).mkdir(parents=True, exist_ok=True)
    env = {'PATH': '/usr/bin:/bin', 'HOME': str(work / 'home'), 'TMPDIR': str(work / 'tmp'), 'TZ': 'UTC',
           'USER': 'root', 'RESTIC_PASSWORD': PASSWORD, 'RESTIC_CACHE_DIR': str(work / 'cache'), 'LC_ALL': 'C'}
    env.update(extra or {})
    return env


def restic(binary, repo, *args, work=None, env=None, timeout=900, cwd=None, user=None, stdout=None):
    cmd = [str(binary), '-r', str(repo)] + [str(a) for a in args]
    rc, out, err, secs = run(cmd, env=restic_env(work or SCRATCH / '_tmp', env), timeout=timeout, cwd=cwd, user=user,
                             stdout=stdout)
    return R(rc, out, err, secs, ' '.join(cmd))


def must(result, what=''):
    if result.rc != 0:
        raise RuntimeError('%s failed: %s\n%s' % (what or result.cmd, result.brief(1500), result.cmd))
    return result


_RUSAGE_COUNTER = [0]


def run_rusage(cmd, env=None, timeout=3600, stdout=None, stderr=None, cwd=None):
    """Run one process and return rc, wall seconds, peak RSS in KiB, stdout and stderr text.

    The process is started by lib/rusage_exec.py, a small helper, and the helper reads its rusage: the peak RSS of a
    child starts at the resident size of whatever forked it, and a check script can be gigabytes large (a manifest of
    two million directories). Until 2026-10-07T09:40Z this function forked directly; readings taken by a large
    harness process before that are too high (C10 parts B "unrepaired", C and D up to 10 million blobs)."""
    out_f = stdout or subprocess.PIPE
    err_f = stderr or subprocess.PIPE
    with Server.lock:
        _RUSAGE_COUNTER[0] += 1
        n = _RUSAGE_COUNTER[0]
    (SCRATCH / '_tmp').mkdir(parents=True, exist_ok=True)
    report = SCRATCH / '_tmp' / ('rusage-%d-%d.json' % (os.getpid(), n))
    began = time.monotonic()
    p = subprocess.Popen([sys.executable, str(CHECKS / 'lib' / 'rusage_exec.py'), str(report)] + [str(c) for c in cmd], env=env,
                         stdin=subprocess.DEVNULL, stdout=out_f, stderr=err_f, start_new_session=True, cwd=str(cwd) if cwd else None)
    box = {}

    def reader(name, stream):
        box[name] = stream.read() if stream else b''
    threads = [threading.Thread(target=reader, args=('out', p.stdout)), threading.Thread(target=reader, args=('err', p.stderr))]
    for t in threads:
        t.start()

    def kill():
        try:
            os.killpg(p.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
    timer = threading.Timer(timeout, kill)
    timer.start()
    try:
        p.wait()
    finally:
        timer.cancel()
    for t in threads:
        t.join()
    dec = lambda b: (b or b'').decode('utf-8', 'surrogateescape')
    result = {'rc': -9, 'seconds': time.monotonic() - began, 'rss_kib': 0, 'user': 0.0, 'sys': 0.0}
    try:
        result.update(json.loads(report.read_text()))
        report.unlink()
    except (OSError, ValueError):
        pass
    result.update(out=dec(box.get('out')), err=dec(box.get('err')))
    return result


class Server:
    """One chkrest process serving one repository directory over a unix socket."""
    counter = 0
    lock = threading.Lock()

    def __init__(self, root, **options):
        with Server.lock:
            Server.counter += 1
            n = Server.counter
        sockdir = SCRATCH / '_s'
        sockdir.mkdir(parents=True, exist_ok=True)
        self.sock = sockdir / ('%d-%d.sock' % (os.getpid(), n))
        self.stats_path = sockdir / ('%d-%d.stats.json' % (os.getpid(), n))
        self.err_path = sockdir / ('%d-%d.err' % (os.getpid(), n))
        cmd = [str(chkrest_bin()), '-listen', str(self.sock), '-root', str(root), '-stats', str(self.stats_path)]
        for key, value in options.items():
            flag = '-' + key.replace('_', '-')
            if value is True:
                cmd.append(flag)
            elif value is not False and value is not None:
                cmd += [flag, str(value)]
        if self.sock.exists():
            self.sock.unlink()
        self.err = open(self.err_path, 'wb')
        self.proc = subprocess.Popen(cmd, stdin=subprocess.DEVNULL, stdout=subprocess.DEVNULL, stderr=self.err,
                                     start_new_session=True)
        deadline = time.monotonic() + 10
        while not self.sock.exists():
            if self.proc.poll() is not None or time.monotonic() > deadline:
                raise RuntimeError('chkrest did not start: ' + self.err_path.read_text()[-500:])
            time.sleep(0.005)
        self.url = 'rest:http+unix://%s:/' % self.sock

    def outage(self, on=True):
        self.proc.send_signal(signal.SIGUSR1 if on else signal.SIGUSR2)

    def stop(self):
        stats = {}
        if self.proc.poll() is None:
            self.proc.send_signal(signal.SIGTERM)
            try:
                self.proc.wait(timeout=20)
            except subprocess.TimeoutExpired:
                self.proc.kill()
                self.proc.wait()
        self.err.close()
        try:
            stats = json.loads(self.stats_path.read_text())
        except (OSError, ValueError):
            pass
        for path in (self.sock, self.stats_path, self.err_path):
            try:
                path.unlink()
            except OSError:
                pass
        return stats

    def __enter__(self):
        return self

    def __exit__(self, *exc):
        self.stop()


# ---------------------------------------------------------------- manifests of file trees

def _sha256(path):
    h = hashlib.sha256()
    with open(path, 'rb') as f:
        while True:
            block = f.read(4 << 20)
            if not block:
                break
            h.update(block)
    return h.hexdigest()


def manifest(root, content=True, blocks=False):
    """Everything restic is expected to reproduce, keyed by path (bytes decoded with surrogateescape)."""
    root = os.fsencode(str(root))
    entries = {}
    inodes = {}
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames.sort()
        for name in sorted(dirnames) + sorted(filenames):
            path = os.path.join(dirpath, name)
            rel = os.path.relpath(path, root).decode('utf-8', 'surrogateescape')
            st = os.lstat(path)
            e = {'mode': st.st_mode, 'uid': st.st_uid, 'gid': st.st_gid, 'mtime': st.st_mtime_ns}
            if stat.S_ISLNK(st.st_mode):
                e['target'] = os.readlink(path).hex()
            elif stat.S_ISREG(st.st_mode):
                e['size'] = st.st_size
                e['nlink'] = st.st_nlink
                if st.st_nlink > 1:
                    inodes.setdefault((st.st_dev, st.st_ino), []).append(rel)
                if content:
                    e['sha256'] = _sha256(path)
                if blocks:
                    e['blocks'] = st.st_blocks
            elif stat.S_ISCHR(st.st_mode) or stat.S_ISBLK(st.st_mode):
                e['rdev'] = st.st_rdev
            try:
                names = os.listxattr(path, follow_symlinks=False)
                xattrs = {}
                for x in sorted(names):
                    try:
                        xattrs[x] = os.getxattr(path, x, follow_symlinks=False).hex()
                    except OSError as exc:
                        xattrs[x] = 'ERR %d' % exc.errno
                if xattrs:
                    e['xattrs'] = xattrs
            except OSError:
                pass
            entries[rel] = e
    for group in inodes.values():
        first = sorted(group)[0]
        for rel in group:
            entries[rel]['hardlink_group'] = first
    return entries


def diff_manifests(a, b, ignore=(), limit=40):
    """Human-readable differences between two manifests (empty list: equal)."""
    out = []
    for path in sorted(set(a) | set(b), key=lambda s: s.encode('utf-8', 'surrogateescape')):
        if path not in a:
            out.append('only in B: %r' % path)
        elif path not in b:
            out.append('only in A: %r' % path)
        else:
            for field in sorted(set(a[path]) | set(b[path])):
                if field in ignore:
                    continue
                if a[path].get(field) != b[path].get(field):
                    out.append('%r %s: A=%r B=%r' % (path, field, a[path].get(field), b[path].get(field)))
        if len(out) >= limit:
            out.append('... (more)')
            break
    return out


# ---------------------------------------------------------------- test data

def _rand_bytes(rng, n):
    return rng.randbytes(n)


WORDS = [b'backup', b'restore', b'snapshot', b'repository', b'index', b'pack', b'tree', b'blob', b'chunk', b'lock',
         b'prune', b'check', b'forget', b'the', b'of', b'and', b'a', b'to', b'in', b'is', b'\n']


def _text_bytes(rng, n):
    parts = []
    size = 0
    while size < n:
        w = rng.choice(WORDS)
        parts.append(w)
        size += len(w) + 1
    return b' '.join(parts)[:n]


def write_file(path, data, mtime=None):
    if not isinstance(path, bytes):       # bytes: a name that is not valid UTF-8
        path = str(path)
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, 'wb') as f:
        f.write(data)
    if mtime is not None:
        os.utime(path, ns=(mtime, mtime))


def gen_corpus(root, seed=1, small=300, small_max=48 * 1024, text_files=6, text_mb=4, big=(6, 3, 11), special=True):
    """A deterministic source tree: many small files, compressible text, incompressible big files, duplicates and
    (with special=True) links, sparse data, extended attributes, odd names and modes."""
    root = Path(root)
    rng = random.Random(seed)
    base = 1_600_000_000 * 10**9
    for i in range(small):
        size = rng.choice([0, 1, 100, 4096, 4097]) if i % 11 == 0 else rng.randrange(1, small_max)
        data = _text_bytes(rng, size) if i % 3 == 0 else _rand_bytes(rng, size)
        write_file(root / 'small' / ('d%02d' % (i % 17)) / ('f%04d.dat' % i), data, base + i * 10**9 + i * 1001)
    for i in range(text_files):
        write_file(root / 'text' / ('t%d.txt' % i), _text_bytes(rng, text_mb << 20), base + 5 * 10**12 + i)
    for i, mb in enumerate(big):
        write_file(root / 'big' / ('b%d.bin' % i), _rand_bytes(rng, mb << 20), base + 6 * 10**12 + i)
    block = _rand_bytes(rng, 64 * 1024)
    write_file(root / 'big' / 'repeat64k.bin', block * 48, base + 7 * 10**12)
    (root / 'dup').mkdir(exist_ok=True)
    shutil.copyfile(root / 'big' / 'b0.bin', root / 'dup' / 'copy-of-b0.bin')
    if special:
        sp = root / 'special'
        sp.mkdir(exist_ok=True)
        write_file(sp / 'empty', b'')
        (sp / 'emptydir').mkdir(exist_ok=True)
        write_file(sp / 'linked-a', _rand_bytes(rng, 300_000), base + 8 * 10**12)
        os.link(sp / 'linked-a', sp / 'linked-b')
        (sp / 'sub').mkdir(exist_ok=True)
        os.link(sp / 'linked-a', sp / 'sub' / 'linked-c')
        os.symlink('linked-a', sp / 'sym-rel')
        os.symlink('/nonexistent/target', sp / 'sym-dangling')
        os.symlink(b'bad\xfftarget', os.fsencode(str(sp)) + b'/sym-rawbytes')
        os.mkfifo(sp / 'fifo')
        with open(sp / 'sparse', 'wb') as f:
            f.truncate(96 << 20)
            f.seek(5 << 20)
            f.write(_rand_bytes(rng, 70_000))
            f.seek(60 << 20)
            f.write(_rand_bytes(rng, 200_000))
        os.utime(sp / 'sparse', ns=(base + 9 * 10**12, base + 9 * 10**12))
        write_file(os.fsencode(str(sp)) + b'/name\xff\xfe-not-utf8', b'raw name')
        write_file(sp / ('long-' + 'n' * 200), b'long name')
        deep = sp / ('L' * 200) / ('M' * 200) / ('N' * 200)
        write_file(deep / 'deep.txt', b'deep file')
        write_file(sp / 'xattr-file', b'has attributes')
        os.setxattr(sp / 'xattr-file', 'user.chk.one', b'value one')
        os.setxattr(sp / 'xattr-file', 'user.chk.bin', bytes(range(256)))
        (sp / 'xattr-dir').mkdir(exist_ok=True)
        os.setxattr(sp / 'xattr-dir', 'user.chk.dir', b'd')
        write_file(sp / 'mode-setuid', b'x')
        os.chmod(sp / 'mode-setuid', 0o4755)
        write_file(sp / 'mode-000', b'no permissions')
        os.chmod(sp / 'mode-000', 0)
        write_file(sp / 'owned', b'other owner')
        os.chown(sp / 'owned', 1234, 4321)
        (sp / 'sticky').mkdir(exist_ok=True)
        os.chmod(sp / 'sticky', 0o1777)
        os.utime(sp / 'emptydir', ns=(base, base))
    return root


def mutate_corpus(root, seed=2):
    """Change a source tree the way a second backup would find it: edits, appends, deletions, new files, type swaps."""
    root = Path(root)
    rng = random.Random(seed)
    files = sorted((root / 'small').rglob('*.dat'))
    for path in rng.sample(files, max(1, len(files) // 6)):
        data = path.read_bytes()
        choice = rng.randrange(4)
        if choice == 0:
            path.unlink()
        elif choice == 1:
            path.write_bytes(data + _rand_bytes(rng, 1 + rng.randrange(5000)))
        elif choice == 2 and data:
            cut = rng.randrange(len(data))
            path.write_bytes(data[:cut] + _rand_bytes(rng, 16) + data[cut + 16:])
        else:
            os.utime(path, ns=(1_700_000_000 * 10**9, 1_700_000_000 * 10**9))
    for i in range(20):
        write_file(root / 'small' / 'new' / ('n%03d.dat' % i), _rand_bytes(rng, rng.randrange(1, 90_000)))
    big = sorted((root / 'big').glob('b*.bin'))
    if big:
        with open(big[0], 'r+b') as f:
            f.seek(rng.randrange(1 << 20, 3 << 20))
            f.write(_rand_bytes(rng, 4096))
        with open(big[-1], 'ab') as f:
            f.write(_rand_bytes(rng, 700_000))
    write_file(root / 'big' / ('added-%d.bin' % seed), _rand_bytes(rng, 3 << 20))
    texts = sorted((root / 'text').glob('*.txt'))
    if texts:
        texts[0].unlink()
        with open(texts[-1], 'ab') as f:
            f.write(_text_bytes(rng, 300_000))
    sp = root / 'special'
    if sp.is_dir():
        if (sp / 'emptydir').is_dir():            # directory becomes a file
            (sp / 'emptydir').rmdir()
            write_file(sp / 'emptydir', b'now a file')
        if (sp / 'empty').is_file():              # file becomes a directory
            (sp / 'empty').unlink()
            (sp / 'empty').mkdir()
            write_file(sp / 'empty' / 'inside', b'inside')
        if (sp / 'sym-rel').is_symlink():         # symlink becomes a file
            (sp / 'sym-rel').unlink()
            write_file(sp / 'sym-rel', b'was a symlink')
        if (sp / 'linked-b').exists():            # one hard link goes, one comes
            (sp / 'linked-b').unlink()
            os.link(sp / 'linked-a', sp / 'linked-d')
        if (sp / 'mode-setuid').exists():
            os.chmod(sp / 'mode-setuid', 0o640)
        if (sp / 'xattr-file').exists():
            os.setxattr(sp / 'xattr-file', 'user.chk.one', b'changed value')
            try:
                os.removexattr(sp / 'xattr-file', 'user.chk.bin')
            except OSError:
                pass
            os.setxattr(sp / 'xattr-file', 'user.chk.new', b'new %d' % seed)
    return root


def repo_files(repo, kind):
    """IDs of the files of one type in a local repository directory."""
    repo = Path(repo)
    if kind == 'data':
        return sorted(p.name for p in (repo / 'data').glob('*/*') if p.is_file())
    return sorted(p.name for p in (repo / kind).glob('*') if p.is_file())


def pack_path(repo, pack_id):
    return Path(repo) / 'data' / pack_id[:2] / pack_id


def copy_repo(src, dst):
    if Path(dst).exists():
        rmtree(dst)
    rc, out, err, _ = run(['cp', '-a', str(src), str(dst)])
    if rc:
        raise RuntimeError('cp failed: ' + err)


def index_packs(binary, repo, work, env=None):
    """pack id -> list of blobs {id,type,offset,length,uncompressed_length}, read with `cat index`."""
    packs = {}
    listing = must(restic(binary, repo, 'list', 'index', '--no-lock', '--no-cache', work=work, env=env))
    for index_id in listing.out.split():
        doc = json.loads(must(restic(binary, repo, 'cat', 'index', index_id, '--no-lock', '--no-cache', work=work, env=env)).out)
        for pack in doc.get('packs', []):
            packs.setdefault(pack['id'], []).extend(pack['blobs'])
    return packs


def snapshots(binary, repo, work):
    return json.loads(must(restic(binary, repo, 'snapshots', '--json', '--no-lock', work=work)).out)


PROGRESS_LINE = re.compile(r'^\[\d+:\d+(:\d+)?\]')


def normalize_output(text, replacements=()):
    """Sorted non-empty lines without progress lines; `replacements` hide paths that legitimately differ."""
    lines = []
    for line in text.replace('\r', '\n').splitlines():
        line = line.rstrip()
        if not line.strip() or PROGRESS_LINE.match(line.strip()):
            continue
        for old, new in replacements:
            line = line.replace(str(old), new)
        if line.startswith('restic repair packs '):
            # stock prints these pack IDs in map order, which changes from run to run of the same binary
            line = 'restic repair packs ' + ' '.join(sorted(line.split()[3:]))
        lines.append(line)
    return sorted(lines)


def blur_recent_mtimes(entries, since_ns):
    """A timestamp newer than the start of the check was not restored (it is 'now'): make such values comparable."""
    for e in entries.values():
        if e.get('mtime', 0) >= since_ns:
            e['mtime'] = 'not restored (time of the run)'
    return entries


def same_output(a, b, replacements_a=(), replacements_b=()):
    """Compare two restic results: exit code and normalized stdout+stderr. Returns a list of differences."""
    out = []
    if a.rc != b.rc:
        out.append('exit code %s vs %s' % (a.rc, b.rc))
    la = normalize_output(a.out + '\n' + a.err, replacements_a)
    lb = normalize_output(b.out + '\n' + b.err, replacements_b)
    if la != lb:
        only_a = [x for x in la if x not in lb][:6]
        only_b = [x for x in lb if x not in la][:6]
        out.append('output differs; only A: %r; only B: %r' % (only_a, only_b))
    return out
