package repository

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/restic"
)

type b23IndexSaveCounter struct {
	backend.Backend
	saves atomic.Int64
}

func (b *b23IndexSaveCounter) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if h.Type == backend.IndexFile {
		b.saves.Add(1)
	}
	return b.Backend.Save(ctx, h, rd)
}

// These are complete-save acceptance tests, independent of the comparison
// writer. A divergent second encode must never reach persistent storage.
func TestB23IndexSaveAcceptance(t *testing.T) {
	for _, mode := range []string{"equal", "different", "short", "long", "encoder-error"} {
		t.Run(mode, func(t *testing.T) {
			repo, be := TestRepositoryWithBackend(t, nil, 2, Options{})
			counter := &b23IndexSaveCounter{Backend: be}
			repo.be = counter
			expected := []byte("{\"packs\":[]}\n")
			calls := 0
			sentinel := errors.New("second encode rejected")
			id, err := repo.saveIndexStream(t.Context(), func(w io.Writer) error {
				calls++
				value := bytes.Clone(expected)
				if calls == 2 {
					switch mode {
					case "different":
						value[1] = 'x'
					case "short":
						value = value[:len(value)-1]
					case "long":
						value = append(value, 'x')
					case "encoder-error":
						return sentinel
					}
				}
				// Reuse one byte of scratch after every Write. The writer may borrow it
				// during the call, but retaining it would change this independent result.
				var scratch [1]byte
				for _, v := range value {
					scratch[0] = v
					if _, err := w.Write(scratch[:]); err != nil {
						return err
					}
					scratch[0] = 0xff
				}
				return nil
			})
			if calls != 2 {
				t.Fatalf("encodes %d, want 2", calls)
			}
			if mode == "equal" {
				if err != nil || id.IsNull() || counter.saves.Load() != 1 {
					t.Fatalf("accepted save: %v %v %d", id, err, counter.saves.Load())
				}
				got, err := repo.LoadUnpacked(t.Context(), restic.IndexFile, id)
				if err != nil || !bytes.Equal(got, expected) {
					t.Fatalf("stored bytes %q: %v", got, err)
				}
			} else {
				if err == nil || !id.IsNull() || counter.saves.Load() != 0 {
					t.Fatalf("rejected save persisted: %v %v %d", id, err, counter.saves.Load())
				}
				if mode == "encoder-error" {
					if !errors.Is(err, sentinel) {
						t.Fatalf("lost encoder error: %v", err)
					}
				} else if !strings.Contains(err.Error(), "data mismatch") {
					t.Fatalf("lost mismatch distinction: %v", err)
				}
			}
		})
	}
}

func TestB23IndexSaveLegacyFallback(t *testing.T) {
	repo, be := TestRepositoryWithBackend(t, nil, 1, Options{})
	counter := &b23IndexSaveCounter{Backend: be}
	repo.be = counter
	expected := []byte("{\"packs\":[]}\n")
	calls := 0
	id, err := repo.saveIndexStream(t.Context(), func(w io.Writer) error { calls++; _, err := w.Write(expected); return err })
	if err != nil || calls != 1 || counter.saves.Load() != 1 {
		t.Fatalf("legacy save: %v calls%d saves%d", err, calls, counter.saves.Load())
	}
	got, err := repo.LoadUnpacked(t.Context(), restic.IndexFile, id)
	if err != nil || !bytes.Equal(got, expected) {
		t.Fatalf("legacy bytes %q: %v", got, err)
	}
}
