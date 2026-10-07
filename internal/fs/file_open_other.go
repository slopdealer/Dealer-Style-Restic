//go:build !linux

package fs

import "os"

func openFileNoPoller(name string, flag int, perm os.FileMode, _ bool) (*os.File, bool, error) {
	f, err := os.OpenFile(name, flag, perm)
	return f, false, err
}
