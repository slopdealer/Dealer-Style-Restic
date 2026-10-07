//go:build linux

package fs

import (
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// openFileNoPoller uses a direct open for callers that check the opened file's
// type before doing I/O. os.OpenFile registers descriptors with the runtime
// poller, which regular files cannot use. The path may refer to another file
// type by the time it is opened, so callers must Stat the descriptor and
// confirm it is regular before using it for I/O.
func openFileNoPoller(name string, flag int, perm os.FileMode, tryNoAtime bool) (*os.File, bool, error) {
	const supportedFlags = unix.O_RDONLY | unix.O_WRONLY | unix.O_CREAT | unix.O_EXCL | unix.O_TRUNC | unix.O_NOFOLLOW | unix.O_CLOEXEC
	if flag&^supportedFlags != 0 || flag&unix.O_ACCMODE == unix.O_RDWR || perm&^os.FileMode(0o777) != 0 {
		f, err := os.OpenFile(name, flag, perm)
		return f, false, err
	}

	flags := flag | unix.O_CLOEXEC
	if tryNoAtime && flag&unix.O_ACCMODE == unix.O_RDONLY {
		fd, err := openRetry(name, flags|unix.O_NOATIME, uint32(perm.Perm()))
		if err == nil {
			return fileFromFD(fd, name), true, nil
		}
	}

	fd, err := openRetry(name, flags, uint32(perm.Perm()))
	if err != nil {
		return nil, false, &os.PathError{Op: "open", Path: name, Err: err}
	}
	return fileFromFD(fd, name), false, nil
}

func openRetry(name string, flags int, mode uint32) (int, error) {
	for {
		fd, err := unix.Open(name, flags, mode)
		if err != syscall.EINTR {
			return fd, err
		}
	}
}

func fileFromFD(fd int, name string) *os.File {
	return os.NewFile(uintptr(fd), name)
}
