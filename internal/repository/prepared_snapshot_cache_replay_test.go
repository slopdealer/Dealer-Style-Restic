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
	"testing"
	"time"
)

// Counts actual backend requests below the production replay seam.
type b01CacheTrialBackend struct {
	backend.Backend
	network int
}

func (b *b01CacheTrialBackend) Load(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
	b.network++
	return b.Backend.Load(ctx, h, n, off, fn)
}

func TestB01CacheReplayTrial(t *testing.T) {
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
			b := &b01CacheTrialBackend{Backend: be}
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
			owner := retry.New(b, time.Nanosecond, func(msg string, err error, _ time.Duration) {
				reports = append(reports, msg+":"+strings.ReplaceAll(err.Error(), base, "CACHE"))
			}, nil)
			r.be = owner
			r.cache = nil
			r.UseCache(c, t.Logf)

			if replay {
				var finish func()
				ctx, finish = retry.WithPreparedLoad(ctx, owner, h, raw)
				defer finish()
			}
			got, e := r.LoadUnpacked(ctx, restic.SnapshotFile, id)
			if !broken {
				if e != nil || !bytes.Equal(got, payload) || !c.Has(h) || len(reports) != 0 {
					cancel()
					t.Fatalf("replay=%v err=%v cached=%v reports=%v", replay, e, c.Has(h), reports)
				}
				if replay && (b.network != 0) {
					cancel()
					t.Fatal("replay did not spend exactly once")
				}
				if _, e := r.LoadUnpacked(ctx, restic.SnapshotFile, id); e != nil {
					cancel()
					t.Fatal(e)
				}
				if replay && b.network != 0 {
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
					attempts = b.network
				} else if normalized != expectedError || strings.Join(reports, "\n") != strings.Join(expectedReports, "\n") || b.network+1 != attempts {
					cancel()
					t.Fatalf("cache error differs: err=%q want=%q reports=%v want=%v network=%d attempts=%d", normalized, expectedError, reports, expectedReports, b.network, attempts)
				}
			}
			cancel()
		}
	}
}

func TestPreparedSnapshotWarmBetweenPreparationAndConcurrentConsumption(t *testing.T) {
	payload := []byte(`{"time":"2026-01-01T00:00:00Z"}`)
	r, be, id, size := snapshotPrepareFixture(t, 2, payload)
	fault := &snapshotPrepareBackend{Backend: be, connections: 2}
	r.be = retry.New(fault, time.Second, nil, nil)
	c, err := cache.New(r.Config().ID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.UseCache(c, t.Logf)
	staged := r.PrepareSnapshot(t.Context(), id, size)
	p, ok := staged.(*preparedSnapshot)
	if !ok || fault.loads != 1 {
		t.Fatal("did not stage")
	}
	// Another ordinary loader wins cache population before staged consumption.
	if _, err := r.LoadUnpacked(t.Context(), restic.SnapshotFile, id); err != nil {
		t.Fatal(err)
	}
	if fault.loads != 2 {
		t.Fatal("ordinary loader did not populate")
	}
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			got, err := p.LoadUnpacked(t.Context(), restic.SnapshotFile, id)
			if err == nil && !bytes.Equal(got, payload) {
				err = fmt.Errorf("different snapshot")
			}
			errs <- err
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}
	if fault.loads != 2 || p.raw != nil {
		t.Fatalf("loads=%d retained=%d", fault.loads, len(p.raw))
	}
}
