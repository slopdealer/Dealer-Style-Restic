package main

// Backend requests per process (repair R1).
//
// The test runs one backup, restore, ls, check, forget, forget --prune and
// prune against the in-memory backend and counts every backend operation of
// each command by kind and file type, below the retry layer, without a local
// cache (so every load is a request). It does that twice: with a settle delay
// of zero, where the lock code starts no wait work and every command reads in
// stock's order, and with the production settle delay of 200 ms, where the
// commands use the wait work of this tree.
//
// The numbers are logged, not asserted, apart from one invariant: the wait
// work must never save or remove anything. The file uses only helpers of the
// stock test suite, so it also runs in a stock tree. Run:
//
//	go test -run 'TestLockWaitRequestCounts' -count=1 -v ./cmd/restic
//
// With RESTIC_R1_REQUEST_LATENCY set (for example 30ms) every backend
// operation takes that long, and the wall time of each command is logged as
// well: what the requests cost on a remote repository.

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

type requestCounter struct {
	backend.Backend
	latency time.Duration
	mu      sync.Mutex
	counts  map[string]int
}

func (c *requestCounter) add(op string, t backend.FileType) {
	c.mu.Lock()
	c.counts[op+" "+t.String()]++
	c.mu.Unlock()
	time.Sleep(c.latency)
}

func (c *requestCounter) take() map[string]int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := c.counts
	c.counts = make(map[string]int)
	return out
}

func (c *requestCounter) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	c.add("Save", h.Type)
	return c.Backend.Save(ctx, h, rd)
}

func (c *requestCounter) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
	c.add("Load", h.Type)
	return c.Backend.Load(ctx, h, length, offset, fn)
}

func (c *requestCounter) Stat(ctx context.Context, h backend.Handle) (backend.FileInfo, error) {
	c.add("Stat", h.Type)
	return c.Backend.Stat(ctx, h)
}

func (c *requestCounter) Remove(ctx context.Context, h backend.Handle) error {
	c.add("Remove", h.Type)
	return c.Backend.Remove(ctx, h)
}

func (c *requestCounter) List(ctx context.Context, t backend.FileType, fn func(backend.FileInfo) error) error {
	c.add("List", t)
	return c.Backend.List(ctx, t, fn)
}

func formatRequestCounts(counts map[string]int) (string, int) {
	keys := make([]string, 0, len(counts))
	total := 0
	for k, n := range counts {
		keys = append(keys, k)
		total += n
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", strings.ReplaceAll(k, " ", "."), counts[k]))
	}
	return strings.Join(parts, " "), total
}

func TestLockWaitRequestCounts(t *testing.T) {
	type row struct {
		command string
		counts  map[string]int
		elapsed time.Duration
	}
	var latency time.Duration
	if value := os.Getenv("RESTIC_R1_REQUEST_LATENCY"); value != "" {
		var err error
		latency, err = time.ParseDuration(value)
		rtest.OK(t, err)
	}
	measure := func(wait time.Duration) []row {
		env, cleanup := withTestEnvironment(t)
		defer cleanup()
		repository.TestSetLockTimeout(t, wait)
		defer repository.TestSetLockTimeout(t, 0)

		counter := &requestCounter{counts: make(map[string]int), latency: latency}
		env.gopts.Backends.Register(mem.NewFactory())
		env.gopts.Repo = "mem:"
		env.gopts.NoCache = true
		env.gopts.BackendTestHook = nil
		env.gopts.BackendInnerTestHook = func(be backend.Backend) (backend.Backend, error) {
			counter.Backend = be
			return counter, nil
		}

		restic.TestDisableCheckPolynomial(t)
		rtest.OK(t, withTermStatus(t, env.gopts, func(ctx context.Context, gopts global.Options) error {
			return runInit(ctx, InitOptions{}, gopts, nil, gopts.Term)
		}))

		write := func(name string) {
			path := filepath.Join(env.testdata, "x", name)
			rtest.OK(t, os.MkdirAll(filepath.Dir(path), 0o700))
			rtest.OK(t, appendRandomData(path, 100*1024))
		}
		backup := func() {
			// a relative path, so that restore does not have to recreate the
			// parents of the temporary directory
			testRunBackup(t, env.testdata, []string{"x"}, BackupOptions{}, env.gopts)
		}

		var rows []row
		record := func(command string, run func()) {
			counter.take()
			start := time.Now()
			run()
			rows = append(rows, row{command, counter.take(), time.Since(start)})
		}

		write("one")
		backup()
		write("two")
		record("backup", backup)
		record("restore latest", func() {
			testRunRestore(t, env.gopts, filepath.Join(env.base, "restore"), "latest")
		})
		record("ls latest", func() {
			testRunLs(t, env.gopts, "latest")
		})
		record("check", func() {
			testRunCheck(t, env.gopts)
		})
		record("forget --keep-last 1", func() {
			testRunForget(t, env.gopts, ForgetOptions{Last: 1})
		})
		write("three")
		backup()
		record("forget --keep-last 1 --prune", func() {
			testRunForget(t, env.gopts, ForgetOptions{Last: 1, Prune: true})
		})
		write("four")
		backup()
		testRunForget(t, env.gopts, ForgetOptions{Last: 1})
		record("prune", func() {
			testRunPrune(t, env.gopts, PruneOptions{MaxUnused: "0"})
		})
		testRunCheck(t, env.gopts)
		return rows
	}

	stockOrder := measure(0)
	waitWork := measure(200 * time.Millisecond)
	for i, stock := range stockOrder {
		with := waitWork[i]
		stockText, stockTotal := formatRequestCounts(stock.counts)
		withText, withTotal := formatRequestCounts(with.counts)
		t.Logf("R1COUNT %-30s stock-order total=%-3d %s", stock.command+":", stockTotal, stockText)
		t.Logf("R1COUNT %-30s wait-work   total=%-3d %s", with.command+":", withTotal, withText)
		if latency > 0 {
			// the stock-order run has no settle delay: add it for comparison
			t.Logf("R1TIME  %-30s at %v per request: stock order %4d ms + 200 ms settle delay = %4d ms; with wait work %4d ms",
				stock.command+":", latency, stock.elapsed.Milliseconds(), stock.elapsed.Milliseconds()+200, with.elapsed.Milliseconds())
		}

		// reads may move and be repeated; writes may not
		for key, n := range with.counts {
			if (strings.HasPrefix(key, "Save ") || strings.HasPrefix(key, "Remove ")) && stock.counts[key] != n {
				t.Errorf("%s: %s = %d with wait work, %d in stock order", with.command, key, n, stock.counts[key])
			}
		}
	}
}
