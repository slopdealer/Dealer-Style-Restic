//go:build linux

package fs

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDirectPathStatMetadata(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("metadata"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(file, 0751|os.ModeSetuid|os.ModeSetgid|os.ModeSticky); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(file, time.Unix(100000, 123456789), time.Unix(200000, 987654321)); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "symlink")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	hard := filepath.Join(dir, "hardlink")
	if err := os.Link(file, hard); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(dir, "socket")
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	paths := []string{file, hard, dir, dir + "/", dir + "//", link, fifo, "/", "//", ".", dir + "/./file"}
	if err == syscall.EPERM {
		t.Log("socket metadata case unavailable: sandbox denies socket creation")
	} else {
		if err != nil {
			t.Fatal(err)
		}
		defer syscall.Close(fd)
		if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: socket}); err == syscall.EPERM {
			t.Log("socket metadata case unavailable: sandbox denies socket bind")
		} else {
			if err != nil {
				t.Fatal(err)
			}
			paths = append(paths, socket)
		}
	}
	for _, path := range paths {
		for _, follow := range []bool{false, true} {
			var old os.FileInfo
			if follow {
				old, err = os.Stat(path)
			} else {
				old, err = os.Lstat(path)
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := localPathStat(path, follow)
			if err != nil {
				t.Fatal(err)
			}
			want := extendedStat(old)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%q follow=%v: metadata differs\nwant %#v\ngot %#v", path, follow, want, got)
			}
		}
	}
}

func TestDirectPathStatErrors(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"", "bad\x00path", filepath.Join(dir, "absent"), file + "/child", loop, dir + "/" + strings.Repeat("x", 256)} {
		for _, follow := range []bool{false, true} {
			var want error
			if follow {
				_, want = os.Stat(path)
			} else {
				_, want = os.Lstat(path)
			}
			_, got := localPathStat(path, follow)
			if !reflect.DeepEqual(want, got) {
				t.Errorf("%q follow=%v: want %#v, got %#v", path, follow, want, got)
			}
		}
	}
}

func TestDirectPathStatReplacementAndCache(t *testing.T) {
	for _, replacement := range []string{"regular", "directory", "symlink", "absent"} {
		t.Run(replacement, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "item")
			if err := os.WriteFile(path, []byte("old"), 0600); err != nil {
				t.Fatal(err)
			}
			f, err := newLocalFile(path, O_NOFOLLOW, true)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			old, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path, path+"old"); err != nil {
				t.Fatal(err)
			}
			switch replacement {
			case "regular":
				err = os.WriteFile(path, []byte("new longer data"), 0640)
			case "directory":
				err = os.Mkdir(path, 0700)
			case "symlink":
				err = os.Symlink(path+"old", path)
			}
			if err != nil {
				t.Fatal(err)
			}
			cached, err := f.Stat()
			if err != nil || cached != old {
				t.Fatalf("metadata cache changed before reopen: %v", err)
			}
			err = f.MakeReadable()
			if replacement == "symlink" || replacement == "absent" {
				if err == nil {
					t.Fatal("reopen unexpectedly succeeded")
				}
				if replacement == "symlink" && !errors.Is(err, syscall.ELOOP) {
					t.Fatal(err)
				}
				if replacement == "absent" && !errors.Is(err, os.ErrNotExist) {
					t.Fatal(err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			got, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}
			current, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, extendedStat(current)) {
				t.Fatal("reopen kept stale metadata")
			}
			node, err := f.ToNode(false, t.Logf)
			if err != nil {
				t.Fatal(err)
			}
			if node.Inode != got.Inode || !node.ModTime.Equal(got.ModTime) || node.Mode != got.Mode {
				t.Fatal("Stat/ToNode metadata mismatch")
			}
		})
	}
}

var directPathStatSink *ExtendedFileInfo

func TestDirectPathStatAllocationComparison(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatal(err)
	}
	wrapped := testing.AllocsPerRun(1000, func() {
		fi, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		directPathStatSink = extendedStat(fi)
	})
	direct := testing.AllocsPerRun(1000, func() {
		fi, err := localPathStat(path, false)
		if err != nil {
			t.Fatal(err)
		}
		directPathStatSink = fi
	})
	t.Logf("path-stat allocations per call: wrapped=%g direct=%g", wrapped, direct)
	if direct >= wrapped {
		t.Fatalf("direct conversion did not remove allocation: wrapped=%g direct=%g", wrapped, direct)
	}
}

// Once readable, metadata and content come from the opened descriptor even
// after the pathname names a different inode. Neither path-stat optimization
// nor later ToNode calls may replace that observation with a fresh path stat.
func TestDirectPathStatOpenedDescriptorReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "item")
	if err := os.WriteFile(path, []byte("opened bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := newLocalFile(path, O_NOFOLLOW, true)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err = f.Stat(); err != nil {
		t.Fatal(err)
	}
	if err = f.MakeReadable(); err != nil {
		t.Fatal(err)
	}
	opened, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Rename(path, path+"old"); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("replacement"), 0644); err != nil {
		t.Fatal(err)
	}
	got, err := f.Stat()
	if err != nil || !reflect.DeepEqual(got, opened) {
		t.Fatalf("descriptor observation changed: %v", err)
	}
	node, err := f.ToNode(false, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	if node.Inode != opened.Inode || node.Size != uint64(opened.Size) || node.Mode != opened.Mode {
		t.Fatal("ToNode changed descriptor observation")
	}
	buffer := make([]byte, 32)
	n, err := f.Read(buffer)
	if err != nil || !bytes.Equal(buffer[:n], []byte("opened bytes")) {
		t.Fatalf("opened data changed: %q %v", buffer[:n], err)
	}
}
