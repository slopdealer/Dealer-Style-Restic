package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository"
)

// The fault is in the reader consumed by the real local Save: local.Save has
// created and partially written a temporary file when the interruption lands.
// This does not replace Save with a model or pretend blob callbacks are durable.
type verifyInterruptedUpload struct {
	backend.Backend
	mu       sync.Mutex
	failed   bool
	bytes    int
	attempts int
	retry    bool
}

func (b *verifyInterruptedUpload) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	// Fault the large raw data pack, not whichever small tree pack races it.
	if h.Type != backend.PackFile || h.IsMetadata {
		return b.Backend.Save(ctx, h, rd)
	}
	b.mu.Lock()
	b.attempts++
	interrupt := !b.failed
	b.failed = true
	b.mu.Unlock()
	if !interrupt {
		return b.Backend.Save(ctx, h, rd)
	}
	partial := &verifyPartialReader{RewindReader: rd, remaining: 4096}
	err := b.Backend.Save(ctx, h, partial)
	b.mu.Lock()
	b.bytes = partial.read
	b.mu.Unlock()
	if b.retry {
		return err
	}
	return backoff.Permanent(err)
}

type verifyPartialReader struct {
	backend.RewindReader
	remaining int
	read      int
}

func (r *verifyPartialReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	p = p[:min(len(p), r.remaining)]
	n, err := r.RewindReader.Read(p)
	r.remaining -= n
	r.read += n
	return n, err
}

func TestSaveVerifyRealLocalPartialUpload(t *testing.T) {
	for _, retry := range []bool{false, true} {
		name := "interrupted"
		if retry {
			name = "retry-rewind"
		}
		t.Run(name, func(t *testing.T) {
			env, cleanup := withTestEnvironment(t)
			defer cleanup()
			env.gopts.Compression = repository.CompressionOff
			env.gopts.NoCache = true
			testRunInit(t, env.gopts)
			original := make([]byte, 1<<20)
			_, _ = rand.New(rand.NewSource(13)).Read(original)
			path := filepath.Join(env.testdata, "durable")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			testRunBackup(t, "", []string{env.testdata}, BackupOptions{}, env.gopts)
			committed := testListSnapshots(t, env.gopts, 1)[0]
			changed := make([]byte, 1<<20)
			_, _ = rand.New(rand.NewSource(21)).Read(changed)
			if err := os.WriteFile(path, changed, 0600); err != nil {
				t.Fatal(err)
			}
			var faulty *verifyInterruptedUpload
			env.gopts.BackendInnerTestHook = func(be backend.Backend) (backend.Backend, error) {
				faulty = &verifyInterruptedUpload{Backend: be, retry: retry}
				return faulty, nil
			}
			err := testRunBackupAssumeFailure(t, "", []string{env.testdata}, BackupOptions{}, env.gopts)
			if retry && err != nil {
				t.Fatalf("retry did not complete: %v", err)
			}
			if !retry && !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatalf("interruption error lost: %v", err)
			}
			if faulty == nil || !faulty.failed || faulty.bytes != 4096 {
				t.Fatalf("no real partial write: %#v", faulty)
			}
			if retry && faulty.attempts < 2 {
				t.Fatal("retry never dispatched")
			}
			t.Logf("real local upload: interrupted after %d bytes; pack Save attempts %d; retry=%v", faulty.bytes, faulty.attempts, retry)
			env.gopts.BackendInnerTestHook = nil
			count := 1
			if retry {
				count = 2
			}
			ids := testListSnapshots(t, env.gopts, count)
			found := false
			for _, id := range ids {
				if id == committed {
					found = true
				}
			}
			if !found {
				t.Fatal("previously committed snapshot lost")
			}
			if err := filepath.WalkDir(env.repo, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && bytes.Contains([]byte(d.Name()), []byte("-tmp-")) {
					t.Errorf("partial upload temporary survived: %s", p)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			testRunCheck(t, env.gopts)
			out := filepath.Join(env.base, "restore-committed")
			testRunRestore(t, env.gopts, out, committed.String())
			var restored []byte
			if err := filepath.WalkDir(out, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && d.Name() == "durable" {
					restored, err = os.ReadFile(p)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, restored) {
				t.Fatal("committed content changed after interrupted upload")
			}
			// A separately built, digest-checked stock reader is a test fixture,
			// never part of production. Some trusted build adapters discard new
			// non-Go fixtures; record that absence instead of claiming a check.
			stockPath := os.Getenv("RESTIC_TEST_STOCK_BINARY")
			if stockPath == "" {
				stockPath = filepath.Join("testdata", "b13-stock-restic")
			}
			stock, stockErr := os.ReadFile(stockPath)
			if os.IsNotExist(stockErr) {
				t.Log("stock-reader fixture absent: interrupted repository stock check remains untested")
				return
			}
			if stockErr != nil {
				t.Fatal(stockErr)
			}
			sum := sha256.Sum256(stock)
			if hex.EncodeToString(sum[:]) != "7d39ed17b7322fc241a0a6caca2b52a06d52fb359ef6e4a24b8c929b6e92f054" {
				t.Fatal("stock-reader digest mismatch")
			}
			passwordFile := filepath.Join(env.base, "stock-password")
			if err := os.WriteFile(passwordFile, []byte(env.gopts.Password), 0600); err != nil {
				t.Fatal(err)
			}
			stockTarget := filepath.Join(env.base, "stock-restore")
			for _, args := range [][]string{{"check", "--read-data"}, {"restore", committed.String(), "--target", stockTarget}} {
				argv := append([]string{"-r", env.repo, "--password-file", passwordFile, "--no-cache"}, args...)
				cmd := exec.CommandContext(t.Context(), stockPath, argv...)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("stock %v failed: %v\n%s", args, err, output)
				}
			}
			var stockRestored []byte
			if err := filepath.WalkDir(stockTarget, func(p string, d os.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && d.Name() == "durable" {
					stockRestored, err = os.ReadFile(p)
				}
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(original, stockRestored) {
				t.Fatal("stock reader lost committed content")
			}
			if retry {
				// Backend retry must finish the replacement data as well as leave
				// earlier commits readable. A callback alone cannot prove this.
				var latest string
				for _, id := range ids {
					if id != committed {
						latest = id.String()
					}
				}
				latestTarget := filepath.Join(env.base, "stock-retried-restore")
				argv := []string{"-r", env.repo, "--password-file", passwordFile, "--no-cache", "restore", latest, "--target", latestTarget}
				if output, err := exec.CommandContext(t.Context(), stockPath, argv...).CombinedOutput(); err != nil {
					t.Fatalf("stock retry restore failed: %v\n%s", err, output)
				}
				var retried []byte
				if err := filepath.WalkDir(latestTarget, func(p string, d os.DirEntry, err error) error {
					if err == nil && !d.IsDir() && d.Name() == "durable" {
						retried, err = os.ReadFile(p)
					}
					return err
				}); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(changed, retried) {
					t.Fatal("stock reader lost replacement content after rewind retry")
				}
				t.Log("stock restored replacement snapshot after backend rewind retry")
			}
			t.Logf("pinned stock %x read interrupted repository: check --read-data and committed snapshot restore passed", sum)
		})
	}
}
