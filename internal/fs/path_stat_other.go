//go:build !linux

package fs

import "os"

func localPathStat(name string, follow bool) (*ExtendedFileInfo, error) {
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
