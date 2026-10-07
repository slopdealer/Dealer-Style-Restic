//go:build linux

package repository

// WITNESS FOR THE LINUX BOX (S020 review, mechanism 2). NOT RUN by the
// reviewer: the files under test are Linux-only (check_wait_identity_linux.go,
// check_wait_watch_linux.go) and the review machine is Windows. The file is
// compile-checked with GOOS=linux, and its flow (index load, early read,
// settle, change, control, offer) was run on Windows in a throwaway copy with
// injected probes.
//
// It runs the production path: Checker.PrepareWaitPackRead, Checker.LoadIndex
// (the early read of one pack), Settle(true) (lock confirmed), then a change to
// the pack file, then Checker.ReadPacks with the held bytes on offer. For every
// kind of change it asks one question: does `check --read-data` still report
// the damage? A fresh checker without held bytes is the control.
//
// Every pack file is made writable BEFORE the command starts and no change
// calls chmod or closes a writable descriptor unless that is the change under
// test: both would raise inotify events of their own.
//
// Sub-tests and what the reviewer expects by reading:
//   overwrite-restore-mtime  expected PASS (ctime and inotify IN_MODIFY see it)
//   replace-by-rename        expected PASS (other inode)
//   delete                   expected PASS (stat fails)
//   write-through-hard-link  expected PASS (inotify watches the inode)
//   fresh-shared-mapping     expected PASS (the first write fault updates mtime)
//   dirty-shared-mapping     expected FAIL: a page that was already dirty and
//                            writable before the early read is changed after it;
//                            no fault, so no timestamp update, and inotify does
//                            not report mmap writes. If this passes, the limit
//                            is narrower than the reviewer thinks.
//   network-mount            only with RESTIC_WITNESS_NFS_CLIENT_DIR (an empty
//                            directory on an NFS/SMB/FUSE mount) and
//                            RESTIC_WITNESS_NFS_SERVER_DIR (the same directory
//                            as the server sees it). The pack is damaged through
//                            the server path. Expected FAIL on a default NFS
//                            mount (attribute cache 3 s, no remote inotify).
//
// Run on the box, from the tree root:
//   go test -run TestWitnessS020CheckWaitPackRealProbes -count=1 -p 2 -v ./internal/repository
// NFS loopback (root):
//   apt-get install -y nfs-kernel-server
//   mkdir -p /srv/witness/r1 /mnt/witness && echo '/srv/witness 127.0.0.1(rw,no_root_squash,sync,no_subtree_check)' >> /etc/exports
//   exportfs -ra && mount -t nfs 127.0.0.1:/srv/witness /mnt/witness
//   RESTIC_WITNESS_NFS_CLIENT_DIR=/mnt/witness/r1 RESTIC_WITNESS_NFS_SERVER_DIR=/srv/witness/r1 \
//     go test -run 'TestWitnessS020CheckWaitPackRealProbes/network-mount' -count=1 -v ./internal/repository

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/restic"
)

func s020ReadAllPacks(ctx context.Context, chkr *Checker) (errs []error) {
	errChan := make(chan error)
	go chkr.ReadPacks(ctx, func(packs map[restic.ID]int64) map[restic.ID]int64 { return packs }, restic.NewNoopPrinter(), errChan)
	for err := range errChan {
		errs = append(errs, err)
	}
	return errs
}

//go:noinline
func s020Peek(m []byte, i int) byte { return m[i] }

//go:noinline
func s020Poke(m []byte, i int, v byte) { m[i] = v }

// s020FlipByteKeepMode damages one byte in the middle of the file. Unlike
// s020FlipByte it does not call chmod, which alone would raise IN_ATTRIB.
func s020FlipByteKeepMode(t *testing.T, path string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err = f.ReadAt(b[:], fi.Size()/3); err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if _, err = f.WriteAt(b[:], fi.Size()/3); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

// s020MapPack maps a pack file shared and writable. The file stays open and
// mapped until cleanup: closing a writable descriptor raises IN_CLOSE_WRITE.
func s020MapPack(t *testing.T, path string) (m []byte, cleanup func()) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	m, err = syscall.Mmap(int(f.Fd()), 0, int(fi.Size()), syscall.PROT_READ|syscall.PROT_WRITE, syscall.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	return m, func() {
		_ = syscall.Munmap(m)
		_ = f.Close()
	}
}

type s020PackChange struct {
	name         string
	expectBlind  bool // the reviewer expects check to MISS this change
	needsNetwork bool
	// beforeCapture runs before the command starts; the packs are writable.
	beforeCapture func(t *testing.T, packs []string) (cleanup func(), state any)
	// apply changes the pack after the early read.
	apply func(t *testing.T, clientPath, serverPath string, state any) (cleanup func())
}

func s020PackChanges() []s020PackChange {
	noCleanup := func() {}
	return []s020PackChange{
		{name: "overwrite-restore-mtime", apply: func(t *testing.T, path, _ string, _ any) func() {
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			s020FlipByteKeepMode(t, path)
			if err := os.Chtimes(path, fi.ModTime(), fi.ModTime()); err != nil {
				t.Fatal(err)
			}
			return noCleanup
		}},
		{name: "replace-by-rename", apply: func(t *testing.T, path, _ string, _ any) func() {
			buf, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			buf[len(buf)/3] ^= 0xff
			fi, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path+".new", buf, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path+".new", fi.ModTime(), fi.ModTime()); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(path+".new", path); err != nil {
				t.Fatal(err)
			}
			return noCleanup
		}},
		{name: "delete", apply: func(t *testing.T, path, _ string, _ any) func() {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			return noCleanup
		}},
		{name: "write-through-hard-link", apply: func(t *testing.T, path, _ string, _ any) func() {
			// repo/data/xx/<id>: the second name goes into the repository root
			link := filepath.Join(filepath.Dir(filepath.Dir(filepath.Dir(path))), "witness-link")
			if err := os.Link(path, link); err != nil {
				t.Fatal(err)
			}
			s020FlipByteKeepMode(t, link)
			return noCleanup
		}},
		{name: "fresh-shared-mapping", apply: func(t *testing.T, path, _ string, _ any) func() {
			// the first write to a clean page faults, and the fault updates mtime
			m, cleanup := s020MapPack(t, path)
			s020Poke(m, len(m)/3, s020Peek(m, len(m)/3)^0xff)
			return cleanup
		}},
		{name: "dirty-shared-mapping", expectBlind: true,
			// before the early read: map every pack writable and write one byte
			// per page back unchanged, which leaves the pages dirty and writable
			beforeCapture: func(t *testing.T, packs []string) (func(), any) {
				mappings := map[string][]byte{}
				var cleanups []func()
				for _, path := range packs {
					m, cleanup := s020MapPack(t, path)
					cleanups = append(cleanups, cleanup)
					for i := 0; i < len(m); i += 4096 {
						// The helpers are not inlined: the stores are not optimized away.
						b := s020Peek(m, i)
						s020Poke(m, i, b^1)
						s020Poke(m, i, b)
					}
					mappings[path] = m
				}
				return func() {
					for _, cleanup := range cleanups {
						cleanup()
					}
				}, mappings
			},
			apply: func(t *testing.T, path, _ string, state any) func() {
				m := state.(map[string][]byte)[path]
				if m == nil {
					t.Fatalf("no mapping for %v", path)
				}
				// no page fault: the page is already dirty and writable
				s020Poke(m, len(m)/3, s020Peek(m, len(m)/3)^0xff)
				return noCleanup
			}},
		{name: "network-mount", expectBlind: true, needsNetwork: true, apply: func(t *testing.T, _, serverPath string, _ any) func() {
			// damaged on the server, not through this client
			s020FlipByteKeepMode(t, serverPath)
			return noCleanup
		}},
	}
}

func TestWitnessS020CheckWaitPackRealProbes(t *testing.T) {
	if debug.IsEnabled() {
		t.Skip("the mechanism is switched off while the debug log is on")
	}
	for _, c := range s020PackChanges() {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "repo")
			serverDir := dir
			if c.needsNetwork {
				client, server := os.Getenv("RESTIC_WITNESS_NFS_CLIENT_DIR"), os.Getenv("RESTIC_WITNESS_NFS_SERVER_DIR")
				if client == "" || server == "" {
					t.Skip("set RESTIC_WITNESS_NFS_CLIENT_DIR and RESTIC_WITNESS_NFS_SERVER_DIR (see the file comment)")
				}
				dir, serverDir = filepath.Join(client, "repo"), filepath.Join(server, "repo")
				// remove an earlier run through the client, so that its caches follow
				if err := os.RemoveAll(dir); err != nil {
					t.Fatal(err)
				}
			}
			s020LocalRepoWithPacks(t, dir)

			// Make every pack writable now. A chmod after the early read would
			// raise IN_ATTRIB and hide what the change under test does by itself.
			var packs []string
			err := filepath.Walk(filepath.Join(dir, "data"), func(path string, info os.FileInfo, err error) error {
				if err != nil || !info.Mode().IsRegular() {
					return err
				}
				packs = append(packs, path)
				return os.Chmod(path, 0600)
			})
			if err != nil || len(packs) == 0 {
				t.Fatalf("no pack files: %v", err)
			}
			var state any
			if c.beforeCapture != nil {
				var cleanup func()
				cleanup, state = c.beforeCapture(t, packs)
				defer cleanup()
			}
			// let the file system clock move past the timestamps set above
			time.Sleep(50 * time.Millisecond)

			// the command: wait work, then confirmation
			r, _ := TestOpenLocal(t, dir)
			chkr := newChecker(r)
			p := chkr.PrepareWaitPackRead(ctx)
			if p == nil {
				t.Fatal("the mechanism did not start on a local Linux repository")
			}
			defer p.Settle(false)
			if hints, errs := chkr.LoadIndex(ctx, restic.NoopTerminalCounterFactory); len(errs) != 0 || len(hints) != 0 {
				t.Fatalf("LoadIndex: %v %v", hints, errs)
			}
			// Settle cancels a read that is still running. In the command the
			// 200 ms wait gives it time; here wait for it explicitly.
			p.mu.Lock()
			started := p.started
			p.mu.Unlock()
			if !started {
				t.Fatal("no pack was admitted for the early read")
			}
			<-p.done
			p.Settle(true)
			if p.Stats().Completed != 1 {
				t.Fatalf("the early read did not complete: %+v", p.Stats())
			}
			h := backend.Handle{Type: backend.PackFile, Name: p.id.String()}
			l := backend.AsBackend[*local.Local](r.be)
			clientPath := l.Filename(h)
			serverPath := filepath.Join(serverDir, strings.TrimPrefix(clientPath, dir))
			time.Sleep(50 * time.Millisecond)

			// the pack changes after the early read
			cleanup := c.apply(t, clientPath, serverPath, state)
			defer cleanup()

			// control: a checker without held bytes
			rc, _ := TestOpenLocal(t, dir)
			control := newChecker(rc)
			if hints, errs := control.LoadIndex(ctx, restic.NoopTerminalCounterFactory); len(errs) != 0 || len(hints) != 0 {
				t.Fatalf("control LoadIndex: %v %v", hints, errs)
			}
			controlErrs := s020ReadAllPacks(ctx, control)
			t.Logf("control, packs read now: %d errors %v", len(controlErrs), controlErrs)
			if len(controlErrs) == 0 {
				t.Skip("the control does not see the change either (page cache of this client?): nothing to compare")
			}

			// subject: the command goes on with the held bytes on offer
			subjectErrs := s020ReadAllPacks(p.Context(ctx), chkr)
			stats := p.Stats()
			t.Logf("with the lock-wait bytes on offer: %d errors %v; offered=%d rejected=%d replayed=%d", len(subjectErrs), subjectErrs, stats.Offered, stats.Rejected, stats.Replayed)
			if len(subjectErrs) == 0 {
				t.Errorf("CHECK PASSES ON A CHANGED PACK (%s): check --read-data used the bytes of the lock wait for pack %v and reported no error; reading it now reports %v",
					c.name, p.id.Str(), controlErrs)
			} else if c.expectBlind {
				t.Logf("the reviewer expected this change to go unnoticed; it was noticed. The limit is narrower than stated in REVIEW-s020.md")
			}
		})
	}
}
