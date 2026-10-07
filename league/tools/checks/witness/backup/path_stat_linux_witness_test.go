// PUBLISHED COPY (Dealer Style Restic). Paths under /opt/... are the league box's layout; change them, or use the flag or environment variable that overrides them, to run elsewhere. Workstation paths were replaced by LEAGUES_HOME, the box's host name by 'the league box' and the box's working prefix by /opt/league/.
//go:build linux

package fs

// WITNESS (review of S019-merge, area backup): Linux-only code that could NOT be run on
// the Windows review machine. It was only compile-checked (GOOS=linux go vet).
// MUST RUN ON LINUX (the league box), ideally once as root and once as an ordinary user:
//   go test -run 'TestWitnessLinux' -count=1 -p 2 -v ./internal/fs
//
// TestWitnessLinuxPathStatEqualsStock: internal/fs/path_stat_linux.go builds the
//   ExtendedFileInfo straight from syscall.Stat_t. Every field must equal what stock
//   produces with os.Lstat / os.Stat followed by extendedStat (node names, mode bits,
//   inode, device, link count, times). Walks /dev, /proc/self, /sys/class, /run, /tmp, /etc,
//   /usr/bin and a fixture directory holding a FIFO, a socket, symlinks (also dangling),
//   setuid / setgid / sticky files, a hard link pair and names with trailing slashes.
// TestWitnessLinuxMakeReadableEqualsStock: internal/fs/file_open_linux.go opens regular
//   files without the runtime poller and with O_NOATIME at open time. The content read and
//   the metadata recorded afterwards must equal stock's os.OpenFile path, and reading a
//   file must not change its atime when the caller owns it.

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func stockStat(name string, follow bool) (*ExtendedFileInfo, error) {
	var fi os.FileInfo
	var err error
	if follow {
		fi, err = os.Stat(name)
	} else {
		fi, err = os.Lstat(name)
	}
	if err != nil {
		return nil, err
	}
	return extendedStat(fi), nil
}

func TestWitnessLinuxPathStatEqualsStock(t *testing.T) {
	dir := t.TempDir()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(os.WriteFile(filepath.Join(dir, "plain"), []byte("x"), 0o640))
	must(os.Link(filepath.Join(dir, "plain"), filepath.Join(dir, "hardlink")))
	must(os.Symlink("plain", filepath.Join(dir, "symlink")))
	must(os.Symlink("missing", filepath.Join(dir, "dangling")))
	must(syscall.Mkfifo(filepath.Join(dir, "fifo"), 0o600))
	must(os.Mkdir(filepath.Join(dir, "sub"), 0o750))
	for name, mode := range map[string]os.FileMode{"suid": 0o755 | os.ModeSetuid, "sgid": 0o755 | os.ModeSetgid, "sticky": 0o755 | os.ModeSticky} {
		must(os.WriteFile(filepath.Join(dir, name), nil, 0o600))
		must(os.Chmod(filepath.Join(dir, name), mode))
	}
	sock, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	must(err)
	defer syscall.Close(sock)
	must(syscall.Bind(sock, &syscall.SockaddrUnix{Name: filepath.Join(dir, "socket")}))

	paths := []string{dir, dir + "/", dir + "//", filepath.Join(dir, "sub") + "/", "/", ".", "..", "./", "/dev/null", "/dev/zero", "/dev/tty", "/proc/self/exe", "/proc/self/fd/0", "", "does/not/exist", filepath.Join(dir, "plain", "notadir")}
	for _, root := range []string{dir, "/dev", "/proc/self", "/sys/class", "/run", "/tmp", "/etc", "/usr/bin"} {
		count := 0
		_ = filepath.WalkDir(root, func(p string, _ os.DirEntry, _ error) error {
			paths = append(paths, p)
			count++
			if count > 4000 {
				return filepath.SkipAll
			}
			return nil
		})
	}
	compared, mismatches := 0, 0
	for _, p := range paths {
		for _, follow := range []bool{false, true} {
			// two stock observations: skip entries that change by themselves (/proc, /dev/pts)
			w1, e1 := stockStat(p, follow)
			got, gotErr := localPathStat(p, follow)
			w2, e2 := stockStat(p, follow)
			if (e1 == nil) != (e2 == nil) || !reflect.DeepEqual(w1, w2) {
				continue
			}
			compared++
			if (e1 == nil) != (gotErr == nil) {
				mismatches++
				t.Errorf("%q follow=%v: error mismatch stock=%v candidate=%v", p, follow, e1, gotErr)
				continue
			}
			if e1 != nil {
				if e1.Error() != gotErr.Error() || !reflect.DeepEqual(reflect.TypeOf(e1), reflect.TypeOf(gotErr)) || os.IsNotExist(e1) != os.IsNotExist(gotErr) || os.IsPermission(e1) != os.IsPermission(gotErr) {
					mismatches++
					t.Errorf("%q follow=%v: error differs stock=%#v candidate=%#v", p, follow, e1, gotErr)
				}
				continue
			}
			if !reflect.DeepEqual(w1, got) {
				mismatches++
				t.Errorf("%q follow=%v: STAT DIFFERS\nstock:     %+v\ncandidate: %+v", p, follow, *w1, *got)
			}
			if mismatches > 20 {
				t.Fatal("too many mismatches")
			}
		}
	}
	t.Logf("%d path observations compared, %d mismatches", compared, mismatches)
	if compared < 500 {
		t.Errorf("only %d observations compared; the walk did not cover enough", compared)
	}
}

func TestWitnessLinuxMakeReadableEqualsStock(t *testing.T) {
	dir := t.TempDir()
	content := bytes.Repeat([]byte("restic"), 200000)
	name := filepath.Join(dir, "file")
	if err := os.WriteFile(name, content, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(name, old, old); err != nil {
		t.Fatal(err)
	}

	// stock: newLocalFile(name, flag, false) = os.OpenFile + setFlags
	stockFile, err := newLocalFile(name, O_NOFOLLOW, false)
	if err != nil {
		t.Fatal(err)
	}
	stockFI, err := stockFile.Stat()
	if err != nil {
		t.Fatal(err)
	}
	stockData, err := io.ReadAll(stockFile)
	if err != nil {
		t.Fatal(err)
	}
	_ = stockFile.Close()

	// candidate: metadata-only handle, Stat, MakeReadable (takes the no-poller path)
	f, err := NewLocal().OpenFile(name, O_NOFOLLOW, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Stat(); err != nil {
		t.Fatal(err)
	}
	if err := f.MakeReadable(); err != nil {
		t.Fatal(err)
	}
	candFI, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	candData, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(stockData, candData) || !bytes.Equal(candData, content) {
		t.Errorf("content read through the no-poller descriptor differs (%d vs %d bytes)", len(candData), len(stockData))
	}
	if !reflect.DeepEqual(stockFI, candFI) {
		t.Errorf("metadata after open differs\nstock:     %+v\ncandidate: %+v", *stockFI, *candFI)
	}
	after, err := os.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	if at := extendedStat(after).AccessTime; !at.Equal(old) {
		t.Logf("atime changed from %v to %v (expected to stay only if the file system honours O_NOATIME and is not mounted noatime/relatime-updated)", old, at)
	}

	// a path that stops being a regular file between Stat and MakeReadable must fail, not hang
	swap := filepath.Join(dir, "swap")
	if err := os.WriteFile(swap, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	g, err := NewLocal().OpenFile(swap, O_NOFOLLOW, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Stat(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(swap); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("file", swap); err != nil {
		t.Fatal(err)
	}
	err = g.MakeReadable()
	t.Logf("regular file replaced by a symlink before MakeReadable: err = %v (stock: O_NOFOLLOW open fails with ELOOP)", err)
	if err == nil {
		t.Errorf("MakeReadable followed a symlink that replaced a regular file")
		_ = g.Close()
	}
}
