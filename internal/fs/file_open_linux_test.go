//go:build linux

package fs

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestOpenFileNoFollowCreateSemantics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "created")
	f, err := OpenFile(path, O_CREATE|O_EXCL|O_WRONLY|O_NOFOLLOW, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	fdFlags, err := unix.FcntlInt(f.Fd(), unix.F_GETFD, 0)
	if err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	if fdFlags&unix.FD_CLOEXEC == 0 {
		_ = f.Close()
		t.Fatal("opened descriptor is not close-on-exec")
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("created file mode is %04o, want 0600", got)
	}

	_, err = OpenFile(path, O_CREATE|O_EXCL|O_WRONLY|O_NOFOLLOW, 0o600)
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "open" || !errors.Is(err, os.ErrExist) {
		t.Fatalf("exclusive create error = %#v, want an open PathError wrapping os.ErrExist", err)
	}
}

func TestOpenFileNoFollowRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "target")
	link := filepath.Join(dir, "link")
	if err := os.WriteFile(target, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	_, err := OpenFile(link, O_RDONLY|O_NOFOLLOW, 0)
	var pathErr *os.PathError
	if !errors.As(err, &pathErr) || pathErr.Op != "open" || !errors.Is(err, syscall.ELOOP) {
		t.Fatalf("no-follow symlink error = %#v, want open PathError wrapping ELOOP", err)
	}
}

func TestOpenFileFIFOKeepsDeadlineSupport(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fifo")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}

	// Keep a writer present so opening the read end does not block. Opening
	// with O_RDWR is supported for FIFOs on Linux and provides both ends.
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	holder := os.NewFile(uintptr(fd), path)
	defer holder.Close()

	f, err := OpenFile(path, O_RDONLY|O_NOFOLLOW, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	if err := f.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatalf("SetReadDeadline on FIFO = %v, want poller-backed FIFO support", err)
	}
}
