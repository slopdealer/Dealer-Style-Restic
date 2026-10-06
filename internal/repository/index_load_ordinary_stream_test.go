package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mock"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type ordinaryStreamLoadOutcome struct {
	loadErr, decodeErr, panicText string
	blobs                         map[pack.PackedBlob]int
	packs                         restic.IDSet
	ids                           restic.IDs
	final                         bool
}

func ordinaryStreamLoadResult(load func() (*index.Index, error, error)) (out ordinaryStreamLoadOutcome) {
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

func TestOrdinaryIndexStreamCompatibility(t *testing.T) {
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
					got := ordinaryStreamLoadResult(func() (*index.Index, error, error) {
						return ordinaryStreamDecode(repo, context.Background(), id)
					})
					want := ordinaryStreamLoadResult(eager)
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("got=%+v want=%+v", got, want)
					}
				})
			}
		})
	}
}

func TestOrdinaryIndexStreamIntegrityAndRetry(t *testing.T) {
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
			got := ordinaryStreamLoadResult(func() (*index.Index, error, error) { return ordinaryStreamDecode(repo, ctx, loadedID) })
			gotLoads := loads
			loads = 0
			want := ordinaryStreamLoadResult(func() (*index.Index, error, error) {
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
type ordinaryIndexOverrideReader struct {
	*Repository
	calls int
}

func (r *ordinaryIndexOverrideReader) LoadUnpacked(context.Context, restic.FileType, restic.ID) ([]byte, error) {
	r.calls++
	return []byte(`{"packs":[]}`), nil
}

func (r *ordinaryIndexOverrideReader) List(ctx context.Context, typ restic.FileType, fn func(restic.ID, int64) error) error {
	return fn(restic.Hash([]byte("override")), 1)
}

func TestOrdinaryIndexStreamWrapperBoundary(t *testing.T) {
	repo, err := New(mock.NewBackend(), Options{})
	if err != nil {
		t.Fatal(err)
	}
	wrapper := &ordinaryIndexOverrideReader{Repository: repo}
	if _, ok := any(wrapper).(interface {
		LoadIndexStream(context.Context, restic.ID) (*index.Index, []byte, error)
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

func ordinaryStreamDecode(repo *Repository, ctx context.Context, id restic.ID) (*index.Index, error, error) {
	loader := &ordinaryIndexStreamRepository{internalRepository: &internalRepository{repo}}
	idx, buf, err := loader.LoadIndexStream(ctx, id)
	if err != nil {
		return nil, err, nil
	}
	if idx != nil {
		return idx, nil, nil
	}
	idx, err = index.DecodeIndex(buf, id)
	return idx, nil, err
}

func TestOrdinaryIndexStreamCompleteLoad(t *testing.T) {
	repo, _, _ := TestRepositoryWithVersion(t, restic.StableRepoVersion)
	blobID := restic.Hash([]byte("test blob"))
	packID := restic.Hash([]byte("test pack"))
	source := index.NewMasterIndex()
	blobs := make(pack.Blobs, 50000)
	for i := range blobs {
		bid := restic.Hash([]byte(fmt.Sprint(i)))
		if i == 0 {
			bid = blobID
		}
		blobs[i] = pack.Blob{BlobHandle: restic.BlobHandle{ID: bid, Type: restic.DataBlob}, Offset: uint(i * 33), Length: 33}
	}
	if err := source.StorePack(context.Background(), packID, blobs, &internalRepository{repo}); err != nil {
		t.Fatal(err)
	}
	if err := source.Flush(context.Background(), &internalRepository{repo}); err != nil {
		t.Fatal(err)
	}
	for _, callback := range []bool{false, true} {
		repo.DiscardIndex()
		calls := 0
		var cb func(restic.ID, *index.Index, error) error
		if callback {
			cb = func(_ restic.ID, idx *index.Index, err error) error {
				calls++
				if err != nil || idx == nil {
					t.Fatalf("idx=%v err=%v", idx, err)
				}
				return nil
			}
		}
		loader := &ordinaryStreamObserved{ordinaryIndexStreamRepository: &ordinaryIndexStreamRepository{internalRepository: &internalRepository{repo}}}
		if err := repo.idx.Load(context.Background(), loader, restic.NoopCounter, cb); err != nil {
			t.Fatal(err)
		}
		if loader.streamed.Load() != 1 {
			t.Fatalf("large file missed streaming: files=%d bytes=%d stream=%d fallback=%d", loader.files.Load(), uint64(0), loader.streamed.Load(), uint64(0))
		}
		if len(repo.idx.Lookup(restic.BlobHandle{ID: blobID, Type: restic.DataBlob})) == 0 {
			t.Fatal("load lost blob")
		}
		if callback && calls != 1 {
			t.Fatalf("callbacks=%d", calls)
		}
		if err := repo.idx.Load(context.Background(), loader, restic.NoopCounter, cb); err != nil {
			t.Fatal(err)
		}
		if callback && calls != 1 {
			t.Fatalf("incremental callback repeated: %d", calls)
		}
	}
}

// Test instrumentation belongs to the caller; production retains no counters.
type ordinaryStreamObserved struct {
	*ordinaryIndexStreamRepository
	files, streamed atomic.Uint64
}

func (r *ordinaryStreamObserved) LoadIndexStream(ctx context.Context, id restic.ID) (*index.Index, []byte, error) {
	r.files.Add(1)
	idx, buf, err := r.ordinaryIndexStreamRepository.LoadIndexStream(ctx, id)
	if idx != nil {
		r.streamed.Add(1)
	}
	return idx, buf, err
}

func TestOrdinaryIndexStreamLargeTerminalErrors(t *testing.T) {
	repo, be := TestRepositoryWithBackend(t, nil, 2, Options{})
	var document strings.Builder
	fmt.Fprintf(&document, `{"packs":[{"id":"%s","blobs":[`, restic.Hash([]byte("large pack")))
	for i := 0; i < 50000; i++ {
		if i > 0 {
			document.WriteByte(',')
		}
		fmt.Fprintf(&document, `{"id":"%s","type":"data","offset":%d,"length":33}`, restic.Hash([]byte(fmt.Sprint(i))), i*33)
	}
	document.WriteString(`]}]}`)
	plain := []byte(document.String())
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	valid := append([]byte{2}, enc.EncodeAll(plain, nil)...)
	if len(valid) < 1024*1024 {
		t.Fatalf("large case too small: %d", len(valid))
	}
	corrupt := bytes.Clone(valid)
	corrupt[len(corrupt)-1] ^= 255
	trailing := append([]byte{2}, enc.EncodeAll(append(bytes.Clone(plain), []byte("true")...), nil)...)
	split := append([]byte{2}, enc.EncodeAll(plain[:100], nil)...)
	split = append(split, enc.EncodeAll(plain[100:], nil)...)
	// An early malformed canonical field must not hide a later CRC failure.
	malformed := bytes.Clone(plain)
	malformed[2] = 'X'
	malformedCRC := append([]byte{2}, enc.EncodeAll(malformed, nil)...)
	malformedCRC[len(malformedCRC)-1] ^= 255
	if len(malformedCRC) < 1024*1024 {
		t.Fatal("malformed case missed streaming admission")
	}
	for n, payload := range [][]byte{valid, corrupt, valid[:len(valid)-1], trailing, split, malformedCRC} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			nonce := crypto.NewRandomNonce()
			ciphertext := repo.key.Seal(nonce, nonce, payload, nil)
			id := restic.Hash(ciphertext)
			h := backend.Handle{Type: backend.IndexFile, Name: id.String()}
			if err := be.Save(context.Background(), h, backend.NewByteReader(ciphertext, be.Hasher())); err != nil {
				t.Fatal(err)
			}
			loader := &ordinaryIndexStreamRepository{internalRepository: &internalRepository{repo}}
			if n == 0 || n == 4 {
				idx, _, err := loader.LoadIndexStream(context.Background(), id)
				if err != nil || idx == nil {
					t.Fatalf("large canonical stream missed: idx=%v err=%v", idx, err)
				}
			}
			got := ordinaryStreamLoadResult(func() (*index.Index, error, error) { return ordinaryStreamDecode(repo, context.Background(), id) })
			want := ordinaryStreamLoadResult(func() (*index.Index, error, error) {
				buf, err := repo.LoadUnpacked(context.Background(), restic.IndexFile, id)
				if err != nil {
					return nil, err, nil
				}
				idx, err := index.DecodeIndex(buf, id)
				return idx, nil, err
			})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("terminal precedence differs: got=%+v want=%+v", got, want)
			}
		})
	}
}
