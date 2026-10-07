package repository

import (
	"bytes"
	"context"
	"fmt"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/cache"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/restic"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPreparedSnapshotReplayCacheFailureParity(t *testing.T) {
	payload := []byte(`{"time":"2026-01-01T00:00:00Z"}`)
	r, be, id, _ := snapshotPrepareFixture(t, 2, payload)
	raw, err := r.LoadRaw(t.Context(), restic.SnapshotFile, id)
	if err != nil || restic.Hash(raw) != id {
		t.Fatal("unverified staged ciphertext")
	}
	retry.TestFastRetries(t)
	h := backend.Handle{Type: backend.SnapshotFile, Name: id.String()}
	for _, broken := range []bool{false, true} {
		var expectedReports []string
		var expectedError string
		attempts := 0
		for _, replay := range []bool{false, true} {
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			b := &snapshotPrepareBackend{Backend: be, connections: 2}
			base := t.TempDir()
			c, err := cache.New(r.Config().ID, base)
			if err != nil {
				cancel()
				t.Fatal(err)
			}
			if broken {
				dir := filepath.Join(base, r.Config().ID, "snapshots")
				if err := os.Remove(dir); err != nil {
					cancel()
					t.Fatal(err)
				}
				if err := os.WriteFile(dir, []byte("not a directory"), 0600); err != nil {
					cancel()
					t.Fatal(err)
				}
			}
			var reports []string
			retrying := retry.New(b, time.Nanosecond, func(msg string, err error, _ time.Duration) {
				reports = append(reports, msg+":"+strings.ReplaceAll(err.Error(), base, "CACHE"))
			}, nil)
			r.be = retrying
			r.cache = nil
			r.UseCache(c, t.Logf)
			var finish func()
			var tokenCtx context.Context
			if replay {
				tokenCtx, finish = retry.WithPreparedLoad(ctx, retrying, h, raw)
				ctx = tokenCtx
			}
			got, e := r.LoadUnpacked(ctx, restic.SnapshotFile, id)
			if !broken {
				if e != nil || !bytes.Equal(got, payload) || !c.Has(h) || len(reports) != 0 {
					cancel()
					t.Fatalf("replay=%v err=%v cached=%v reports=%v", replay, e, c.Has(h), reports)
				}
				if replay && (b.loads != 0) {
					cancel()
					t.Fatal("replay did not spend exactly once")
				}
				if _, e := r.LoadUnpacked(ctx, restic.SnapshotFile, id); e != nil {
					cancel()
					t.Fatal(e)
				}
				if replay && b.loads != 0 {
					cancel()
					t.Fatal("second read bypassed populated cache")
				}
			} else {
				if e == nil || got != nil || c.Has(h) {
					cancel()
					t.Fatal("accepted failed cache write")
				}
				normalized := strings.ReplaceAll(e.Error(), base, "CACHE")
				if !replay {
					expectedReports = reports
					expectedError = normalized
					attempts = b.loads
				} else if normalized != expectedError || strings.Join(reports, "\n") != strings.Join(expectedReports, "\n") || b.loads+1 != attempts {
					cancel()
					t.Fatalf("cache error differs: err=%q want=%q reports=%v want=%v replay=%d network=%d attempts=%d", normalized, expectedError, reports, expectedReports, 1, b.loads, attempts)
				}
			}
			if finish != nil {
				finish()
			}
			cancel()
		}
	}
}

// Exercise the actual preparation, cache writer and ordinary authentication.
func TestPreparedSnapshotReplayColdWarmAndDamagedCache(t *testing.T) {
	for _, mode := range []string{"cold", "warm-between", "damaged-between", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			payload := []byte(`{"time":"2026-01-01T00:00:00Z"}`)
			r, be, id, size := snapshotPrepareFixture(t, 2, payload)
			b := &snapshotPrepareBackend{Backend: be, connections: 2}
			r.be = retry.New(b, time.Second, nil, nil)
			c, err := cache.New(r.Config().ID, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			r.UseCache(c, t.Logf)
			h := backend.Handle{Type: backend.SnapshotFile, Name: id.String()}
			p, ok := r.PrepareSnapshot(t.Context(), id, size).(*preparedSnapshot)
			if !ok || c.Has(h) || b.loads != 1 {
				t.Fatalf("prepared=%v cached=%v loads=%d", ok, c.Has(h), b.loads)
			}
			if mode == "warm-between" || mode == "damaged-between" {
				if _, err := r.LoadUnpacked(t.Context(), restic.SnapshotFile, id); err != nil {
					t.Fatal(err)
				}
				if mode == "damaged-between" {
					path := filepath.Join(c.BaseDir(), r.Config().ID, "snapshots", h.Name[:2], h.Name)
					if err := os.WriteFile(path, []byte("damaged"), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			before := b.loads
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if mode == "cancel" {
				cancel()
			}
			got, err := p.LoadUnpacked(ctx, restic.SnapshotFile, id)
			if mode == "cancel" {
				if err != context.Canceled || p.raw != nil || c.Has(h) || b.loads != before {
					t.Fatalf("err=%v cached=%v loads=%d", err, c.Has(h), b.loads)
				}
				return
			}
			if err != nil || !bytes.Equal(got, payload) || !c.Has(h) || p.raw != nil || b.loads != before {
				t.Fatalf("err=%v cached=%v loads=%d want=%d", err, c.Has(h), b.loads, before)
			}
			if _, err := r.LoadUnpacked(t.Context(), restic.SnapshotFile, id); err != nil || b.loads != before {
				t.Fatalf("subsequent cached read err=%v loads=%d", err, b.loads)
			}
			if r.PrepareSnapshot(t.Context(), id, size) != r || b.loads != before {
				t.Fatal("warm cache prepared again")
			}
		})
	}
}

func TestPreparedSnapshotReplayTransientCacheWriteFailure(t *testing.T) {
	payload := []byte(`{}`)
	r, be, id, size := snapshotPrepareFixture(t, 2, payload)
	b := &snapshotPrepareBackend{Backend: be, connections: 2}
	base := t.TempDir()
	c, err := cache.New(r.Config().ID, base)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(base, r.Config().ID, "snapshots")
	reports := 0
	retry.TestFastRetries(t)
	r.be = retry.New(b, time.Second, func(_ string, _ error, _ time.Duration) {
		reports++
		if reports == 1 {
			if err := os.Remove(broken); err != nil {
				t.Error(err)
			}
			if err := os.Mkdir(broken, 0700); err != nil {
				t.Error(err)
			}
		}
	}, nil)
	r.UseCache(c, t.Logf)
	p, ok := r.PrepareSnapshot(t.Context(), id, size).(*preparedSnapshot)
	if !ok {
		t.Fatal("no preparation")
	}
	if err := os.Remove(broken); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(broken, []byte("block cache writer"), 0600); err != nil {
		t.Fatal(err)
	}
	got, err := p.LoadUnpacked(t.Context(), restic.SnapshotFile, id)
	h := backend.Handle{Type: backend.SnapshotFile, Name: id.String()}
	if err != nil || !bytes.Equal(got, payload) || reports != 1 || b.loads != 2 || !c.Has(h) {
		t.Fatalf("err=%v reports=%d loads=%d cached=%v", err, reports, b.loads, c.Has(h))
	}
}

type concurrentSnapshotBackend struct {
	backend.Backend
	loads atomic.Int32
}

func (b *concurrentSnapshotBackend) Load(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
	b.loads.Add(1)
	return b.Backend.Load(ctx, h, n, off, fn)
}
func (b *concurrentSnapshotBackend) Properties() backend.Properties {
	p := b.Backend.Properties()
	p.Connections = 2
	return p
}

func TestPreparedSnapshotReplayConcurrentCachePopulation(t *testing.T) {
	payload := []byte(`{}`)
	r, be, id, size := snapshotPrepareFixture(t, 2, payload)
	b := &concurrentSnapshotBackend{Backend: be}
	r.be = retry.New(b, time.Second, nil, nil)
	c, err := cache.New(r.Config().ID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.UseCache(c, t.Logf)
	p, ok := r.PrepareSnapshot(t.Context(), id, size).(*preparedSnapshot)
	if !ok {
		t.Fatal("no preparation")
	}
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			got, err := p.LoadUnpacked(t.Context(), restic.SnapshotFile, id)
			if err != nil || !bytes.Equal(got, payload) {
				t.Errorf("err=%v got=%q", err, got)
			}
		}()
	}
	close(start)
	wg.Wait()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.raw != nil || !c.Has(backend.Handle{Type: backend.SnapshotFile, Name: id.String()}) || b.loads.Load() > 2 {
		t.Fatalf("retained=%d loads=%d", len(p.raw), b.loads.Load())
	}
}

func TestPreparedSnapshotReplayCanceledWarmCacheMatchesOrdinary(t *testing.T) {
	r, be, id, size := snapshotPrepareFixture(t, 2, []byte(`{}`))
	r.be = retry.New(be, time.Second, nil, nil)
	c, err := cache.New(r.Config().ID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.UseCache(c, t.Logf)
	p := r.PrepareSnapshot(t.Context(), id, size)
	if p == r {
		t.Fatal("no preparation")
	}
	if _, err := r.LoadUnpacked(t.Context(), restic.SnapshotFile, id); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	want, werr := r.LoadUnpacked(ctx, restic.SnapshotFile, id)
	got, gerr := p.LoadUnpacked(ctx, restic.SnapshotFile, id)
	if !bytes.Equal(got, want) || fmt.Sprint(gerr) != fmt.Sprint(werr) {
		t.Fatalf("prepared got=%q err=%v; ordinary got=%q err=%v", got, gerr, want, werr)
	}
}
