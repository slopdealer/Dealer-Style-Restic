package global

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/backend/location"
	"github.com/restic/restic/internal/backend/mock"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
	"github.com/restic/restic/internal/ui"
)

func openingID(n int) string { return fmt.Sprintf("%064x", n) }

func TestOpenKeyListingOverlap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	listed := make(chan struct{})
	statEntered := make(chan struct{})
	statRelease := make(chan struct{})
	be := &mock.Backend{
		StatFn: func(ctx context.Context, _ backend.Handle) (backend.FileInfo, error) {
			close(statEntered)
			select {
			case <-statRelease:
				return backend.FileInfo{Size: 1}, nil
			case <-ctx.Done():
				return backend.FileInfo{}, ctx.Err()
			}
		},
		ListFn: func(_ context.Context, _ backend.FileType, fn func(backend.FileInfo) error) error {
			close(listed)
			return fn(backend.FileInfo{Name: openingID(1), Size: 123})
		},
	}
	wrapped := retry.New(sema.NewBackend(be), time.Second, nil, nil)
	staged, finish := prepareOpenKeyListing(ctx, wrapped, Options{Password: "supplied"})
	defer finish()
	checked := make(chan error, 1)
	go func() { checked <- hasRepositoryConfig(ctx, staged, "unused", Options{}) }()
	select {
	case <-statEntered:
	case <-ctx.Done():
		t.Fatal("Stat did not start")
	}
	select {
	case <-listed:
	case <-ctx.Done():
		t.Fatal("List did not overlap blocked Stat")
	}
	close(statRelease)
	if err := <-checked; err != nil {
		t.Fatal(err)
	}
	got := 0
	if err := staged.List(ctx, backend.KeyFile, func(fi backend.FileInfo) error {
		got++
		if fi.Name != openingID(1) || fi.Size != 123 {
			t.Fatal(fi)
		}
		return nil
	}); err != nil || got != 1 {
		t.Fatalf("List=%v; calls=%d", err, got)
	}
}

func TestOpenKeyListingBoundAndOrder(t *testing.T) {
	quiet := &mock.Backend{ListFn: func(_ context.Context, _ backend.FileType, fn func(backend.FileInfo) error) error {
		if err := fn(backend.FileInfo{Name: "not-an-id"}); err != nil {
			return err
		}
		for n := 1; n <= 1000; n++ {
			fi := backend.FileInfo{Name: openingID(n), Size: int64(n)}
			if err := fn(fi); err != nil {
				return err
			}
			if err := fn(fi); err != nil {
				return err
			}
		}
		return nil
	}}
	b, finish := startOpenKeyListing(context.Background(), &mock.Backend{}, quiet)
	defer finish()
	s := b.(*openKeyListing)
	<-s.done
	if len(s.files) != maxKeys+1 || !errors.Is(s.err, errKeyListingFull) {
		t.Fatalf("records=%d, err=%v", len(s.files), s.err)
	}
	calls := 0
	stop := errors.New("accepted stop")
	err := b.List(context.Background(), backend.KeyFile, func(fi backend.FileInfo) error {
		calls++
		if fi.Name != openingID(calls) || fi.Size != int64(calls) {
			t.Fatal(fi)
		}
		if calls == maxKeys+1 {
			return stop
		}
		return nil
	})
	if err != stop || calls != maxKeys+1 || s.files != nil {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestOpenKeyListingFailedAttemptFallsBack(t *testing.T) {
	failure := errors.New("failure after usable candidate")
	quiet := &mock.Backend{ListFn: func(_ context.Context, _ backend.FileType, fn func(backend.FileInfo) error) error {
		if err := fn(backend.FileInfo{Name: openingID(1)}); err != nil {
			return err
		}
		return failure
	}}
	var ordinaryCalls int
	ordinary := &mock.Backend{ListFn: func(_ context.Context, _ backend.FileType, fn func(backend.FileInfo) error) error {
		ordinaryCalls++
		return fn(backend.FileInfo{Name: openingID(2)})
	}}
	b, finish := startOpenKeyListing(context.Background(), ordinary, quiet)
	defer finish()
	for range 2 {
		if err := b.List(context.Background(), backend.KeyFile, func(fi backend.FileInfo) error {
			if fi.Name != openingID(2) {
				t.Fatal("failed speculation was accepted")
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if ordinaryCalls != 2 {
		t.Fatal(ordinaryCalls)
	}
}

func TestOpenKeyListingQuietFailure(t *testing.T) {
	var calls, reports atomic.Int32
	be := &mock.Backend{ListFn: func(_ context.Context, _ backend.FileType, fn func(backend.FileInfo) error) error {
		if calls.Add(1) == 1 {
			return errors.New("speculative network failure")
		}
		return fn(backend.FileInfo{Name: openingID(1)})
	}}
	ordinary := retry.New(sema.NewBackend(be), time.Second,
		func(string, error, time.Duration) { reports.Add(1) },
		func(string, int) { reports.Add(1) })
	b, finish := prepareOpenKeyListing(context.Background(), ordinary, Options{Password: "supplied"})
	defer finish()
	<-b.(*openKeyListing).done
	if reports.Load() != 0 || calls.Load() != 1 {
		t.Fatal("speculation retried or emitted diagnostics")
	}
	if err := b.List(context.Background(), backend.KeyFile, func(backend.FileInfo) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if reports.Load() != 0 || calls.Load() != 2 {
		t.Fatal("fallback did not use exactly one ordinary successful attempt")
	}
}

func TestOpenKeyListingFailedConfigJoins(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	exited := make(chan struct{})
	be := &mock.Backend{
		StatFn: func(context.Context, backend.Handle) (backend.FileInfo, error) {
			return backend.FileInfo{Size: 0}, nil
		},
		ListFn: func(ctx context.Context, _ backend.FileType, _ func(backend.FileInfo) error) error {
			close(entered)
			<-ctx.Done()
			close(exited)
			return ctx.Err()
		},
	}
	b, finish := startOpenKeyListing(ctx, be, sema.NewBackend(be))
	<-entered
	err := hasRepositoryConfig(ctx, b, "unused", Options{})
	if err == nil || !strings.Contains(err.Error(), "zero size") {
		t.Fatalf("config error=%v", err)
	}
	finish()
	select {
	case <-exited:
	default:
		t.Fatal("failed config left discovery running")
	}
}

func TestOpenKeyListingCancellationAndJoin(t *testing.T) {
	entered, exited := make(chan struct{}), make(chan struct{})
	quiet := &mock.Backend{ListFn: func(ctx context.Context, _ backend.FileType, _ func(backend.FileInfo) error) error {
		close(entered)
		<-ctx.Done()
		close(exited)
		return ctx.Err()
	}}
	_, finish := startOpenKeyListing(context.Background(), &mock.Backend{}, quiet)
	<-entered
	finish()
	select {
	case <-exited:
	default:
		t.Fatal("finish returned before List exited")
	}
	// A successful key cancels the listing; do not dispatch later candidates.
	quiet.ListFn = func(_ context.Context, _ backend.FileType, fn func(backend.FileInfo) error) error {
		for n := 1; n <= 3; n++ {
			if err := fn(backend.FileInfo{Name: openingID(n)}); err != nil {
				return err
			}
		}
		return nil
	}
	b, finish := startOpenKeyListing(context.Background(), &mock.Backend{}, quiet)
	defer finish()
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := b.List(ctx, backend.KeyFile, func(backend.FileInfo) error { calls++; cancel(); return nil })
	if err != context.Canceled || calls != 1 {
		t.Fatalf("err=%v calls=%d", err, calls)
	}
}

func TestOpenKeyListingEligibility(t *testing.T) {
	for _, opts := range []Options{{}, {Password: "supplied", KeyHint: "123"}, {Password: "supplied", InsecureNoPassword: true}} {
		be := retry.New(&mock.Backend{ListFn: func(context.Context, backend.FileType, func(backend.FileInfo) error) error {
			t.Fatal("ineligible speculation ran")
			return nil
		}}, time.Second, nil, nil)
		b, finish := prepareOpenKeyListing(context.Background(), be, opts)
		finish()
		if b != be {
			t.Fatal("ineligible wrapper installed")
		}
	}
	be := retry.New(&mock.Backend{PropertiesFn: func() backend.Properties { return backend.Properties{Connections: 1} }}, time.Second, nil, nil)
	b, finish := prepareOpenKeyListing(context.Background(), be, Options{Password: "supplied"})
	finish()
	if b != be {
		t.Fatal("one-connection backend speculated")
	}
}

type openingTerminal struct {
	ui.Terminal
	prompts  int
	password string
	terminal bool
}

func (t *openingTerminal) InputIsTerminal() bool { return t.terminal }
func (t *openingTerminal) ReadPassword(context.Context, string) (string, error) {
	t.prompts++
	return t.password, nil
}

// Exercise the real OpenRepository integration, wrapper stack, password search,
// key hints and config diagnostics. Low KDF parameters affect tests only.
func TestOpenKeyListingOpenRepository(t *testing.T) {
	for _, tc := range []struct {
		name, password, hint string
		limit                uint
		config               string
		prompt               bool
		want                 string
	}{
		{"success", rtest.TestPassword, "", 2, "valid", false, ""},
		{"one-connection", rtest.TestPassword, "", 1, "valid", false, ""},
		{"wrong-password", "wrong", "", 2, "valid", false, "wrong password"},
		{"hint-exact", rtest.TestPassword, "exact", 2, "valid", false, ""},
		{"hint-missing", rtest.TestPassword, strings.Repeat("0", 64), 2, "valid", false, ""},
		{"prompt", "", "", 2, "valid", true, ""},
		{"missing-config", "", "", 2, "missing", false, "unable to open config"},
		{"empty-config", "", "", 2, "empty", false, "zero size"},
		{"missing-config-supplied", rtest.TestPassword, "", 2, "missing", false, "unable to open config"},
		{"empty-config-supplied", rtest.TestPassword, "", 2, "empty", false, "zero size"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			dir := t.TempDir()
			be, err := local.Create(ctx, local.Config{Path: dir, Connections: tc.limit}, t.Logf)
			if err != nil {
				t.Fatal(err)
			}
			r, _ := repository.TestRepositoryWithBackend(t, be, 2, repository.Options{})
			if tc.hint == "exact" {
				tc.hint = r.KeyID().String()
			}
			if tc.config == "missing" {
				if err := os.Remove(dir + "/config"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.config == "empty" {
				if err := os.Remove(dir + "/config"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(dir+"/config", nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			registry := location.NewRegistry()
			registry.Register(local.NewFactory())
			term := &openingTerminal{password: rtest.TestPassword, terminal: true}
			var speculativeLists atomic.Int32
			opts := Options{Repo: dir, Backends: registry, Password: tc.password, KeyHint: tc.hint, NoCache: true, Term: term}
			opts.Extended = map[string]string{"local.connections": fmt.Sprint(tc.limit)}
			opts.BackendTestHook = func(b backend.Backend) (backend.Backend, error) {
				rb := b.(*retry.Backend)
				rb.MaxElapsedTime = time.Millisecond
				return b, nil
			}
			opts.BackendInnerTestHook = func(b backend.Backend) (backend.Backend, error) {
				return &openingObservedBackend{Backend: b, listed: &speculativeLists}, nil
			}
			opened, err := OpenRepository(ctx, opts, restic.NewNoopPrinter())
			if tc.want == "" {
				if err != nil {
					t.Fatal(err)
				}
				if opened.Config().ID != r.Config().ID {
					t.Fatal("different repository")
				}
			} else if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v, want %s", err, tc.want)
			}
			if (term.prompts > 0) != tc.prompt {
				t.Fatalf("prompts=%d", term.prompts)
			}
			if tc.want == "" && tc.password != "" && tc.hint == "" && speculativeLists.Load() != 1 {
				t.Fatalf("listing count=%d", speculativeLists.Load())
			}
		})
	}
}

type openingObservedBackend struct {
	backend.Backend
	listed *atomic.Int32
}

func (b *openingObservedBackend) List(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
	if typ == backend.KeyFile {
		b.listed.Add(1)
	}
	return b.Backend.List(ctx, typ, fn)
}
func (b *openingObservedBackend) Unwrap() backend.Backend { return b.Backend }

// Successful discovery never caches a key body. Removal/corruption between
// discovery and acceptance must still be observed by the ordinary Load path.
func TestOpenKeyListingCurrentKeyBytes(t *testing.T) {
	r, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	b, finish := startOpenKeyListing(context.Background(), be, be)
	defer finish()
	<-b.(*openKeyListing).done
	h := backend.Handle{Type: backend.KeyFile, Name: r.KeyID().String()}
	if err := be.Remove(context.Background(), h); err != nil {
		t.Fatal(err)
	}
	if err := be.Save(context.Background(), h, backend.NewByteReader([]byte("changed key"), be.Hasher())); err != nil {
		t.Fatal(err)
	}
	fresh, err := repository.New(b, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.SearchKey(context.Background(), rtest.TestPassword, maxKeys, ""); err == nil {
		t.Fatal("prefetch hid changed key bytes")
	}
}

// This is a discriminator, not a claim that opening is an atomic snapshot.
// Earlier discovery misses a concurrent addition; one fresh search repairs that
// case. It also demonstrates why refreshing must start a new search counter.
func TestOpenKeyListingConcurrentAdditionDiscriminator(t *testing.T) {
	ctx := context.Background()
	r, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
	b, finish := startOpenKeyListing(ctx, be, be)
	defer finish()
	<-b.(*openKeyListing).done
	const newPassword = "newly added password"
	if _, err := repository.AddKey(ctx, r, newPassword, "", "", r.Key()); err != nil {
		t.Fatal(err)
	}
	fresh, err := repository.New(b, repository.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := fresh.SearchKey(ctx, newPassword, maxKeys, ""); !errors.Is(err, repository.ErrNoKeyFound) {
		t.Fatalf("earlier discovery returned %v, want ErrNoKeyFound", err)
	}
	if err := fresh.SearchKey(ctx, newPassword, maxKeys, ""); err != nil {
		t.Fatalf("ordinary fresh discovery did not observe added key: %v", err)
	}
	if fresh.Config().ID != r.Config().ID {
		t.Fatal("refresh opened a different repository")
	}
}
