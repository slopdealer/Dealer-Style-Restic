package global

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

// Authentication does not establish freshness for config: unlike keys, its
// handle is not content addressed. This is an executable discriminator for
// proposals to accept config bytes fetched before the ordinary config load.
func TestOpeningEarlyConfigCanHideReplacement(t *testing.T) {
	for _, replacement := range []string{"valid", "unsupported", "malformed", "unauthenticated"} {
		t.Run(replacement, func(t *testing.T) {
			ctx := context.Background()
			original, be := repository.TestRepositoryWithBackend(t, nil, 2, repository.Options{})
			early, err := original.LoadRaw(ctx, restic.ConfigFile, restic.ID{})
			if err != nil {
				t.Fatal(err)
			}
			cfg := original.Config()
			cfg.ID = "replacement-config"
			if replacement == "unsupported" {
				cfg.Version = restic.MaxRepoVersion + 1
			}
			plain, err := json.Marshal(cfg)
			if err != nil {
				t.Fatal(err)
			}
			if replacement == "malformed" {
				plain = []byte("{")
			}
			nonce := crypto.NewRandomNonce()
			current := original.Key().Seal(append([]byte(nil), nonce...), nonce, plain, nil)
			if replacement == "unauthenticated" {
				current[len(current)-1] ^= 1
			}
			h := backend.Handle{Type: backend.ConfigFile}
			if err := be.Remove(ctx, h); err != nil {
				t.Fatal(err)
			}
			if err := be.Save(ctx, h, backend.NewByteReader(current, be.Hasher())); err != nil {
				t.Fatal(err)
			}
			cached := &openingFaultBackend{Backend: be, load: func(ctx context.Context, handle backend.Handle, n int, off int64, fn func(io.Reader) error) error {
				if handle.Type == backend.ConfigFile {
					return fn(bytes.NewReader(early))
				}
				return be.Load(ctx, handle, n, off, fn)
			}}
			open := func(b backend.Backend) (*repository.Repository, error) {
				if err := hasRepositoryConfig(ctx, b, "test", Options{}); err != nil {
					return nil, err
				}
				r, err := repository.New(b, repository.Options{})
				if err != nil {
					return nil, err
				}
				return r, searchOpeningKey(ctx, r, rtest.TestPassword, "", nil)
			}
			stale, err := open(cached)
			if err != nil || stale.Config().ID != original.Config().ID {
				t.Fatalf("early acceptance: %v", err)
			}
			fresh, err := open(be)
			if replacement == "valid" {
				if err != nil || fresh.Config().ID != cfg.ID {
					t.Fatalf("current acceptance: %v", err)
				}
			} else if err == nil {
				t.Fatal("ordinary config acceptance failed to detect replacement")
			} else if replacement == "unsupported" && !strings.Contains(err.Error(), "unsupported repository version") {
				t.Fatal(err)
			}
			t.Logf("replacement=%s earlyErr=nil currentErr=%v", replacement, err)
		})
	}
}
