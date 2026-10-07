//go:build linux

package fs

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// localPathStat retains the path observation and error contract of os.Stat and
// os.Lstat, but builds the final representation without a fileStat wrapper.
// Descriptor observations deliberately continue through os.File.Stat, which
// coordinates with concurrent descriptor close.
func localPathStat(name string, follow bool) (*ExtendedFileInfo, error) {
	var s syscall.Stat_t
	var err error
	op := "lstat"
	if follow {
		op = "stat"
	}
	for {
		if follow {
			err = syscall.Stat(name, &s)
		} else {
			err = syscall.Lstat(name, &s)
		}
		if err != syscall.EINTR {
			break
		}
	}
	if err != nil {
		return nil, &os.PathError{Op: op, Path: name, Err: err}
	}
	mode := os.FileMode(s.Mode & 0777)
	switch s.Mode & syscall.S_IFMT {
	case syscall.S_IFBLK:
		mode |= os.ModeDevice
	case syscall.S_IFCHR:
		mode |= os.ModeDevice | os.ModeCharDevice
	case syscall.S_IFDIR:
		mode |= os.ModeDir
	case syscall.S_IFIFO:
		mode |= os.ModeNamedPipe
	case syscall.S_IFLNK:
		mode |= os.ModeSymlink
	case syscall.S_IFSOCK:
		mode |= os.ModeSocket
	}
	if s.Mode&syscall.S_ISGID != 0 {
		mode |= os.ModeSetgid
	}
	if s.Mode&syscall.S_ISUID != 0 {
		mode |= os.ModeSetuid
	}
	if s.Mode&syscall.S_ISVTX != 0 {
		mode |= os.ModeSticky
	}
	return &ExtendedFileInfo{
		Name:       filepath.Base(name),
		Mode:       mode,
		DeviceID:   uint64(s.Dev),
		Inode:      s.Ino,
		Links:      uint64(s.Nlink),
		UID:        s.Uid,
		GID:        s.Gid,
		Device:     uint64(s.Rdev),
		BlockSize:  int64(s.Blksize),
		Blocks:     s.Blocks,
		Size:       s.Size,
		AccessTime: time.Unix(s.Atim.Unix()),
		ModTime:    time.Unix(s.Mtim.Unix()),
		ChangeTime: time.Unix(s.Ctim.Unix()),
	}, nil
}
