//go:build linux

package main

// WITNESS FOR THE LINUX BOX (S020 review, mechanism 1: backup "compression
// preparation" during the lock wait). NOT RUN by the reviewer: the file under
// test (backup_compression_source_linux.go) is Linux-only and the review
// machine is Windows. Compile-checked with GOOS=linux.
//
// What it shows: before the repository lock is confirmed, `restic backup`
// walks the backup source and calls open(2) on every entry it visits
// (backup_compression_source_linux.go:26), and only then asks what kind of
// file it is (:30-36). Stock restic never opens a FIFO, a socket or a device
// node of the source: it calls lstat and stores the node.
//
//   TestWitnessS020BackupWaitOpensFIFO: a process is blocked in
//   open(fifo, O_WRONLY), waiting for a reader. The walk opens the FIFO for
//   reading (O_NONBLOCK) and closes it again. The writer's open returns, and
//   its write then fails with EPIPE (or is lost in the pipe). Expected on
//   S020: FAIL.
//
//   TestWitnessS020BackupWaitReadsUnchangedFiles: the walk reads up to 2 MiB of
//   every regular file of 512 KiB or more that it meets, up to 8 MiB, on every
//   backup, also when nothing changed (stock opens no unchanged file when a
//   parent snapshot exists). The test only reports the byte count.
//
// Run on the box, from the tree root:
//   go test -run 'TestWitnessS020BackupWait' -count=1 -p 2 -v ./cmd/restic

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestWitnessS020BackupWaitOpensFIFO(t *testing.T) {
	root := t.TempDir()
	fifo := filepath.Join(root, "queue.fifo")
	if err := unix.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}

	type result struct {
		openErr, writeErr error
	}
	writer := make(chan result, 1)
	go func() {
		// blocks until some process opens the FIFO for reading
		fd, err := unix.Open(fifo, unix.O_WRONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			writer <- result{openErr: err}
			return
		}
		time.Sleep(50 * time.Millisecond) // the walk has closed its end by now
		_, werr := unix.Write(fd, []byte("one queued job\n"))
		_ = unix.Close(fd)
		writer <- result{writeErr: werr}
	}()

	select {
	case res := <-writer:
		t.Fatalf("the writer was not blocked: %+v", res)
	case <-time.After(200 * time.Millisecond):
	}

	repo := backupCompressionTestRepo(t)
	shelf := repo.NewCompressionPreparation()
	if shelf == nil {
		t.Fatal("no compression preparation for a version 2 repository with compression auto")
	}
	defer shelf.Release()
	// what backup runs during the lock wait, with the source as its only target
	acquireBackupCompression(t.Context(), shelf, []string{root})

	select {
	case res := <-writer:
		t.Errorf("SIDE EFFECT ON THE BACKUP SOURCE: the lock-wait walk opened the FIFO %v. A writer that was blocked in open() "+
			"was released (open error: %v) and its write ended with: %v. Stock restic does not open FIFOs, sockets or device nodes of the source.",
			filepath.Base(fifo), res.openErr, res.writeErr)
	case <-time.After(500 * time.Millisecond):
		t.Log("the FIFO was not opened")
		// release the writer so that the test can end
		fd, err := unix.Open(fifo, unix.O_RDONLY|unix.O_NONBLOCK, 0)
		if err == nil {
			<-writer
			_ = unix.Close(fd)
		}
	}
}

func TestWitnessS020BackupWaitReadsUnchangedFiles(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.bin", "b.bin", "c.bin", "d.bin", "e.bin"} {
		if err := os.WriteFile(filepath.Join(root, name), make([]byte, 3<<20), 0600); err != nil {
			t.Fatal(err)
		}
	}
	repo := backupCompressionTestRepo(t)
	shelf := repo.NewCompressionPreparation()
	defer shelf.Release()
	acquireBackupCompression(t.Context(), shelf, []string{root})
	stats := shelf.Stats()
	t.Logf("bytes of the source read before the lock was confirmed: %d (chunks %d, entries %d)", stats.ReadBytes, stats.Chunks, stats.Entries)
	if stats.ReadBytes == 0 {
		t.Fatal("the walk read nothing: the mechanism is not active here")
	}
}
