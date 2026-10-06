package global

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/backend/location"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// Use the complete opening path on the same stored bytes. Config Stat failures
// precede authentication; failures after authentication must not trigger another
// discovery or expose a partially opened repository. One connection takes the
// ordinary path and must retain the same precedence.
func TestB01OpeningConfigFailureMatrix(t *testing.T) {
	for _, mode := range []string{"missing", "empty", "malformed", "unauthenticated", "unsupported"} {
		t.Run(mode, func(t *testing.T) {
			ctx := t.Context()
			dir := t.TempDir()
			be, err := local.Create(ctx, local.Config{Path: dir, Connections: 2}, t.Logf)
			if err != nil {
				t.Fatal(err)
			}
			original, _ := repository.TestRepositoryWithBackend(t, be, 2, repository.Options{})
			config := backend.Handle{Type: backend.ConfigFile}
			if err := be.Remove(ctx, config); err != nil {
				t.Fatal(err)
			}
			if mode != "missing" {
				var raw []byte
				if mode != "empty" {
					cfg := original.Config()
					if mode == "unsupported" {
						cfg.Version = restic.MaxRepoVersion + 1
					}
					plain, err := json.Marshal(cfg)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "malformed" {
						plain = []byte("{")
					}
					nonce := crypto.NewRandomNonce()
					raw = original.Key().Seal(append([]byte(nil), nonce...), nonce, plain, nil)
					if mode == "unauthenticated" {
						raw[len(raw)-1] ^= 1
					}
				}
				if err := be.Save(ctx, config, backend.NewByteReader(raw, be.Hasher())); err != nil {
					t.Fatal(err)
				}
			}
			registry := location.NewRegistry()
			registry.Register(local.NewFactory())
			for _, connections := range []int{1, 2} {
				for _, password := range []string{rtest.TestPassword, "wrong password"} {
					t.Run(fmt.Sprintf("connections%d/correct%v", connections, password == rtest.TestPassword), func(t *testing.T) {
						var diagnostics [2]string
						var keyLoads, configLoads [2]atomic.Int32
						for arm := range 2 {
							caseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
							opts := Options{Repo: dir, Backends: registry, Password: password, NoCache: true, Term: &openingTerminal{}, Extended: map[string]string{"local.connections": fmt.Sprint(connections)}}
							opts.BackendInnerTestHook = func(b backend.Backend) (backend.Backend, error) {
								return &openingFaultBackend{Backend: b, load: func(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
									if h.Type == backend.KeyFile {
										keyLoads[arm].Add(1)
									}
									if h.Type == backend.ConfigFile {
										configLoads[arm].Add(1)
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
							if opened != nil || err == nil {
								t.Fatalf("arm %d exposed failed repository: %v", arm, err)
							}
							diagnostics[arm] = err.Error()
						}
						if diagnostics[0] != diagnostics[1] {
							t.Fatalf("staged=%q ordinary=%q", diagnostics[0], diagnostics[1])
						}
						statFailure := mode == "missing" || mode == "empty"
						for arm := range 2 {
							wantKeys, wantConfig := int32(1), int32(1)
							if statFailure {
								wantKeys, wantConfig = 0, 0
							} else if password != rtest.TestPassword {
								// a wrong password costs one key search on both
								// arms, with or without the staged listing
								wantConfig = 0
							}
							if keyLoads[arm].Load() != wantKeys || configLoads[arm].Load() != wantConfig {
								t.Fatalf("arm%d key/config loads=%d/%d want=%d/%d", arm, keyLoads[arm].Load(), configLoads[arm].Load(), wantKeys, wantConfig)
							}
						}
						if mode == "unsupported" && password == rtest.TestPassword && !strings.Contains(diagnostics[0], "unsupported repository version") {
							t.Fatal(diagnostics[0])
						}
					})
				}
			}
		})
	}
}
