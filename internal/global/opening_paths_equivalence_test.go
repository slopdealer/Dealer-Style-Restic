package global

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/backend/location"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// Compare the real opening stack with speculation disabled at its existing
// eligibility seam. The ordinary arm still uses the same retry backend.
func TestOpeningActualPathsMatchOrdinary(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	be, err := local.Create(ctx, local.Config{Path: dir, Connections: 2}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	original, _ := repository.TestRepositoryWithBackend(t, be, 2, repository.Options{})
	ids := []string{original.KeyID().String()}
	for len(ids) < 21 {
		key, err := repository.AddKey(ctx, original, rtest.TestPassword, "", "", original.Key())
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, key.ID().String())
	}
	// With 21 distinct IDs at least two share their first hex digit.
	prefix := ""
	seen := map[string]bool{}
	for _, id := range ids {
		if seen[id[:1]] {
			prefix = id[:1]
			break
		}
		seen[id[:1]] = true
	}
	if prefix == "" {
		t.Fatal("no ambiguous prefix")
	}
	registry := location.NewRegistry()
	registry.Register(local.NewFactory())
	for _, tc := range []struct {
		name, password, hint string
		connections          int
	}{
		{"correct", rtest.TestPassword, "", 2},
		{"wrong", "wrong", "", 2},
		{"correct-one-connection", rtest.TestPassword, "", 1},
		{"wrong-one-connection", "wrong", "", 1},
		{"hint-valid", rtest.TestPassword, ids[0], 2},
		{"hint-missing", rtest.TestPassword, strings.Repeat("0", 64), 2},
		{"hint-ambiguous", rtest.TestPassword, prefix, 2},
		{"wrong-hint-valid", "wrong", ids[0], 2},
		{"wrong-hint-missing", "wrong", strings.Repeat("0", 64), 2},
		{"wrong-hint-ambiguous", "wrong", prefix, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var errors [2]string
			var orders [2][]string
			for arm := range 2 {
				caseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				opts := Options{Repo: dir, Backends: registry, Password: tc.password, KeyHint: tc.hint, NoCache: true, Term: &openingTerminal{}}
				opts.Extended = map[string]string{"local.connections": fmt.Sprint(tc.connections)}
				opts.BackendInnerTestHook = func(b backend.Backend) (backend.Backend, error) {
					return &openingFaultBackend{Backend: b, load: func(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
						if h.Type == backend.KeyFile {
							orders[arm] = append(orders[arm], h.Name)
						}
						return b.Load(ctx, h, n, off, fn)
					}}, nil
				}
				opts.BackendTestHook = func(b backend.Backend) (backend.Backend, error) {
					b.(*retry.Backend).MaxElapsedTime = time.Millisecond
					if arm == 1 {
						return &openingFaultBackend{Backend: b}, nil
					}
					return b, nil
				}
				opened, err := OpenRepository(caseCtx, opts, restic.NewNoopPrinter())
				cancel()
				if err != nil {
					errors[arm] = err.Error()
				} else if opened.Config().ID != original.Config().ID {
					t.Fatal("wrong repository")
				}
			}
			if errors[0] != errors[1] {
				t.Fatalf("staged=%q ordinary=%q", errors[0], errors[1])
			}
			if tc.password == rtest.TestPassword && errors[0] != "" {
				t.Fatal(errors[0])
			}
			if tc.password != rtest.TestPassword && errors[0] == "" {
				t.Fatal("wrong password accepted")
			}
			// also for a wrong password with the staged listing: the same key
			// files, once each, in the same order
			if fmt.Sprint(orders[0]) != fmt.Sprint(orders[1]) {
				t.Fatalf("attempt order differs: %v", orders)
			}
			t.Logf("staged loads=%d ordinary loads=%d error=%q", len(orders[0]), len(orders[1]), errors[0])
		})
	}
}
