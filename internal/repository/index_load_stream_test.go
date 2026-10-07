package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mock"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type streamLoadOutcome struct {
	loadErr, decodeErr, panicText string
	blobs                         map[pack.PackedBlob]int
	packs                         restic.IDSet
	ids                           restic.IDs
	final                         bool
}

func streamLoadResult(load func() (*index.Index, error, error)) (out streamLoadOutcome) {
	defer func() {
		if p := recover(); p != nil {
			out.panicText = fmt.Sprint(p)
		}
	}()
	idx, le, de := load()
	if le != nil {
		out.loadErr = le.Error()
	}
	if de != nil {
		out.decodeErr = de.Error()
	}
	if idx == nil {
		return out
	}
	out.blobs = make(map[pack.PackedBlob]int)
	for pb := range idx.Values() {
		out.blobs[*pb]++
	}
	out.packs = idx.Packs()
	out.ids, _ = idx.IDs()
	out.final = idx.Final()
	return out
}

func TestLoadIndexFileStreamCompatibility(t *testing.T) {
	for _, version := range []uint{1, 2} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			repo, be := TestRepositoryWithBackend(t, nil, version, Options{})
			enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
			if err != nil {
				t.Fatal(err)
			}
			defer enc.Close()
			pid := `"` + strings.Repeat("1", 64) + `"`
			blob := `{"id":` + pid + `,"type":"data","offset":1,"length":33}`
			good := []byte(`{"packs":[{"id":` + pid + `,"blobs":[` + blob + `]}, {"blobs":[{}]}]}`)
			plainCases := [][]byte{good, []byte(`{"packs":[]}`), []byte(`{"PACKS":[]}`), []byte(`{"packs":null}`), []byte(`{"packs":[{"blobs":[{"offset":4294967296}]},{"id":"bad"}]}`), []byte(`{"packs":[{"blobs":[{"offset":4294967296}]}]}`), []byte(`{"packs":[],"packs":[]}`), []byte(`{"packs":[]}true`)}
			var cases [][]byte
			for _, plain := range plainCases {
				cases = append(cases, plain, append([]byte{2}, enc.EncodeAll(plain, nil)...))
			}
			valid := append([]byte{2}, enc.EncodeAll(good, nil)...)
			corrupt := bytes.Clone(valid)
			corrupt[len(corrupt)-1] ^= 255
			split := append([]byte{2}, enc.EncodeAll(good[:20], nil)...)
			split = append(split, enc.EncodeAll(good[20:], nil)...)
			cases = append(cases, split, corrupt, valid[:len(valid)-1], append(bytes.Clone(valid), 1, 2, 3), []byte{9}, []byte{}, []byte{2})
			for num, payload := range cases {
				t.Run(fmt.Sprint(num), func(t *testing.T) {
					nonce := crypto.NewRandomNonce()
					ciphertext := repo.key.Seal(nonce, nonce, payload, nil)
					id := restic.Hash(ciphertext)
					h := backend.Handle{Type: backend.IndexFile, Name: id.String()}
					if err := be.Save(context.Background(), h, backend.NewByteReader(ciphertext, be.Hasher())); err != nil {
						t.Fatal(err)
					}
					eager := func() (*index.Index, error, error) {
						buf, err := repo.LoadUnpacked(context.Background(), restic.IndexFile, id)
						if err != nil {
							return nil, err, nil
						}
						idx, err := index.DecodeIndex(buf, id)
						return idx, nil, err
					}
					got := streamLoadResult(func() (*index.Index, error, error) {
						return (&internalRepository{repo}).LoadIndexFile(context.Background(), id)
					})
					want := streamLoadResult(eager)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("got=%+v want=%+v", got, want)
					}
				})
			}
		})
	}
}

func TestLoadIndexFileStreamIntegrityAndRetry(t *testing.T) {
	be := mock.NewBackend()
	repo, err := New(be, Options{})
	if err != nil {
		t.Fatal(err)
	}
	repo.key = crypto.NewRandomKey()
	repo.cfg.Version = 2
	payload := []byte(`{"packs":[]}`)
	nonce := crypto.NewRandomNonce()
	ciphertext := repo.key.Seal(nonce, nonce, payload, nil)
	id := restic.Hash(ciphertext)
	damaged := bytes.Clone(ciphertext)
	damaged[len(damaged)-1] ^= 255
	for _, mode := range []string{"hash-retry", "hash-failure", "authentication", "short", "cancelled"} {
		t.Run(mode, func(t *testing.T) {
			loads := 0
			loadedID := id
			ctx := context.Background()
			if mode == "authentication" {
				loadedID = restic.Hash(damaged)
			}
			if mode == "short" {
				loadedID = restic.Hash([]byte("short"))
			}
			if mode == "cancelled" {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			be.OpenReaderFn = func(ctx context.Context, h backend.Handle, length int, offset int64) (io.ReadCloser, error) {
				loads++
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				data := ciphertext
				if mode == "hash-failure" || mode == "authentication" || (mode == "hash-retry" && loads == 1) {
					data = damaged
				}
				if mode == "short" {
					data = []byte("short")
				}
				return io.NopCloser(bytes.NewReader(data)), nil
			}
			got := streamLoadResult(func() (*index.Index, error, error) { return (&internalRepository{repo}).LoadIndexFile(ctx, loadedID) })
			gotLoads := loads
			loads = 0
			want := streamLoadResult(func() (*index.Index, error, error) {
				buf, err := repo.LoadUnpacked(ctx, restic.IndexFile, loadedID)
				if err != nil {
					return nil, err, nil
				}
				idx, err := index.DecodeIndex(buf, loadedID)
				return idx, nil, err
			})
			if !reflect.DeepEqual(got, want) || gotLoads != loads {
				t.Fatalf("got=%+v loads=%d want=%+v loads=%d", got, gotLoads, want, loads)
			}
		})
	}
}

// Embedding the public repository and overriding LoadUnpacked must remain an
// effective boundary; only the private adapter has the streaming capability.
type indexOverrideReader struct {
	*Repository
	calls int
}

func (r *indexOverrideReader) LoadUnpacked(context.Context, restic.FileType, restic.ID) ([]byte, error) {
	r.calls++
	return []byte(`{"packs":[]}`), nil
}

func (r *indexOverrideReader) List(ctx context.Context, typ restic.FileType, fn func(restic.ID, int64) error) error {
	return fn(restic.Hash([]byte("override")), 1)
}

func TestLoadIndexFileStreamWrapperBoundary(t *testing.T) {
	repo, err := New(mock.NewBackend(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &indexOverrideReader{Repository: repo}
	if _, ok := any(wrapper).(interface {
		LoadIndexFile(context.Context, restic.ID) (*index.Index, error, error)
	}); ok {
		t.Fatal("streaming method leaked into wrapper")
	}
	callbacks := 0
	err = index.ForAllIndexes(context.Background(), wrapper, wrapper, func(id restic.ID, idx *index.Index, err error) error {
		callbacks++
		if err != nil || idx == nil || !idx.Final() {
			t.Fatalf("overridden loader not used: idx=%v err=%v", idx, err)
		}
		return nil
	})
	if err != nil || wrapper.calls != 1 || callbacks != 1 {
		t.Fatalf("err=%v loads=%d callbacks=%d", err, wrapper.calls, callbacks)
	}
}
