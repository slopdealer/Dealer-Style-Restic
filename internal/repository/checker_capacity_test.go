package repository

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/cache"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestCapacityCheckAdmission(t *testing.T) {
	blobs := pack.Blobs{{Length: checkCipherCapacity, UncompressedLength: 8 << 20}}
	if !capacityCheckTask(blobs, checkCipherCapacity+100) {
		t.Fatal("large decoded length denied streaming admission")
	}
	for _, change := range []func(pack.Blobs){
		func(b pack.Blobs) { b[0].Length++ },
		func(b pack.Blobs) { b[0].Offset = ^uint(0) },
	} {
		b := append(pack.Blobs(nil), blobs...)
		change(b)
		if capacityCheckTask(b, checkCipherCapacity+100) {
			t.Fatal("oversized or overflowing task admitted")
		}
	}
	if capacityCheckTask(blobs, checkCipherCapacity+checkHeaderCapacity+1) || capacityCheckTask(nil, 0) {
		t.Fatal("unbounded header or empty task admitted")
	}
	rd := newBufReader(bufio.NewReader(bytes.NewReader(nil)))
	rd.max = checkCipherCapacity
	if _, err := rd.ReadFull(checkCipherCapacity + 1); err != errCheckCapacity || cap(rd.buf) != 0 {
		t.Fatalf("refusal allocated storage: %v, %d", err, cap(rd.buf))
	}
}

// The capacity route reuses its decoder and ciphertext slot across mixed packs.
// Refusals and failed reads must detach the old input before reuse; a false
// small-output hint cannot bypass streaming validation or header equality.
func TestCapacityCheckReuseAndSmallOutputHint(t *testing.T) {
	key := crypto.NewRandomKey()
	dec, err := newCapacityCheckDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	rd := bufio.NewReaderSize(nil, smallCheckStreamBufferSize)
	state := &checkCapacityState{}
	var slot *byte
	for _, tc := range []struct {
		window int
		hint   bool
	}{
		{512 << 10, false}, {2 << 20, false}, {512 << 10, true}, {512 << 10, false},
	} {
		data, blobs, id := b13WindowPack(t, key, tc.window)
		if tc.hint {
			blobs[1].UncompressedLength = 1
		}
		r := b11Repository(key, &b11ScriptBackend{data: data}, id, blobs)
		err := checkPackCapacity(context.Background(), r, id, blobs, int64(len(data)), rd, dec, state)
		var refused *checkCapacityRefusal
		wantRefusal := tc.window > checkWindowCapacity
		if wantRefusal != errors.As(err, &refused) || (!wantRefusal && !tc.hint && err != nil) || (tc.hint && (err == nil || !strings.Contains(err.Error(), "not contained in index"))) {
			t.Fatalf("window=%d hint=%v: %v", tc.window, tc.hint, err)
		}
		if rd.Buffered() != 0 {
			t.Fatal("pack reader retained input")
		}
		if _, err := dec.Read(make([]byte, 1)); err != zstd.ErrDecoderNilInput {
			t.Fatalf("decoder retained input: %v", err)
		}
		if cap(state.cipher) != checkCipherCapacity {
			t.Fatalf("cipher capacity grew: %d", cap(state.cipher))
		}
		if slot != nil && slot != &state.cipher[0] {
			t.Fatal("worker replaced its charged ciphertext slot")
		}
		slot = &state.cipher[0]
	}
}

func TestCapacityCheckCacheRefusalPreservesEntry(t *testing.T) {
	key := crypto.NewRandomKey()
	data, blobs, id := b13WindowPack(t, key, 2<<20)
	be := &b13CheckBackend{data: data}
	r := b11Repository(key, sema.NewBackend(be), id, blobs)
	c := cache.TestNewCache(t)
	r.UseCache(c, func(string, ...any) {})
	h := backend.Handle{Type: backend.PackFile, Name: id.String(), IsMetadata: true}
	ctx := context.Background()
	if err := r.be.Load(ctx, h, len(data), 0, func(rd io.Reader) error {
		_, err := io.Copy(io.Discard, rd)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dec, err := newCapacityCheckDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	rd := bufio.NewReaderSize(nil, smallCheckStreamBufferSize)
	err = checkPackCapacity(ctx, r, id, blobs, int64(len(data)), rd, dec, &checkCapacityState{})
	var refused *checkCapacityRefusal
	if !errors.As(err, &refused) || !c.Has(h) || be.calls.Load() != 1 {
		t.Fatalf("refusal discarded/reloaded cache: %v cached=%v loads=%d", err, c.Has(h), be.calls.Load())
	}
	ordinary, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer ordinary.Close()
	if err := checkPack(ctx, r, id, blobs, int64(len(data)), rd, ordinary); err != nil {
		t.Fatal(err)
	}
	if !c.Has(h) || be.calls.Load() != 1 {
		t.Fatal("ordinary recovery did not reuse intact cached bytes")
	}
}

func b13WindowPack(t *testing.T, key *crypto.Key, window int) ([]byte, pack.Blobs, restic.ID) {
	t.Helper()
	plain := bytes.Repeat([]byte("same bytes still require authentication and hashing\n"), 100000)
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithWindowSize(window), zstd.WithSingleSegment(false))
	if err != nil {
		t.Fatal(err)
	}
	payload := enc.EncodeAll(plain, nil)
	enc.Close()
	nonce := crypto.NewRandomNonce()
	encrypted := key.Seal(append([]byte(nil), nonce...), nonce, payload, nil)
	var out bytes.Buffer
	p := pack.NewPacker(key, &out)
	prefix := []byte("authenticated prefix")
	prefixNonce := crypto.NewRandomNonce()
	prefixCipher := key.Seal(append([]byte(nil), prefixNonce...), prefixNonce, prefix, nil)
	if _, err := p.Add(restic.DataBlob, restic.Hash(prefix), prefixCipher, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Add(restic.DataBlob, restic.Hash(plain), encrypted, len(plain)); err != nil {
		t.Fatal(err)
	}
	if err := p.Finalize(); err != nil {
		t.Fatal(err)
	}
	blobs, _, err := pack.List(key, bytes.NewReader(out.Bytes()), int64(out.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), blobs, restic.Hash(out.Bytes())
}

func TestCapacityCheckWindowsAndFailures(t *testing.T) {
	key := crypto.NewRandomKey()
	for _, window := range []int{512 << 10, 2 << 20} {
		data, blobs, id := b13WindowPack(t, key, window)
		if !capacityCheckTask(blobs, int64(len(data))) || smallCheckTask(blobs) {
			t.Fatal("fixture did not exercise new route")
		}
		be := &b11ScriptBackend{data: data}
		r := b11Repository(key, be, id, blobs)
		dec, err := newCapacityCheckDecoder()
		if err != nil {
			t.Fatal(err)
		}
		rd := bufio.NewReaderSize(nil, smallCheckStreamBufferSize)
		err = checkPackCapacity(context.Background(), r, id, blobs, int64(len(data)), rd, dec, &checkCapacityState{})
		dec.Close()
		if window == 512<<10 && err != nil {
			t.Fatalf("small window: %v", err)
		}
		if window == 2<<20 {
			if _, ok := err.(*checkCapacityRefusal); !ok || be.calls != 1 {
				t.Fatalf("policy became an error/retry: %v calls=%d", err, be.calls)
			}
			ordinary, _ := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
			defer ordinary.Close()
			if err := checkPack(context.Background(), r, id, blobs, int64(len(data)), rd, ordinary); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, tc := range []struct{ kind, mode, want string }{
		{"ordinary", "", ""}, {"ordinary", "partial-good", "second attempt"},
		{"ordinary", "partial", "partial download"}, {"auth", "", "decrypting blob"},
		{"wrong-hash", "", "wrong data"}, {"ordinary", "pack-hash", "unexpected pack id"},
		{"ordinary", "cancel", "context canceled"},
	} {
		data, blobs, id := b11MakePack(key, 1500<<10, tc.kind)
		ctx, cancel := context.WithCancel(context.Background())
		be := &b11ScriptBackend{data: data, mode: tc.mode, cancel: cancel}
		r := b11Repository(key, be, id, blobs)
		dec, err := newCapacityCheckDecoder()
		if err != nil {
			t.Fatal(err)
		}
		err = checkPackCapacity(ctx, r, id, blobs, int64(len(data)), bufio.NewReaderSize(nil, smallCheckStreamBufferSize), dec, &checkCapacityState{})
		dec.Close()
		cancel()
		if (tc.want == "" && err != nil) || (tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want))) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
}

type b13CheckBackend struct {
	backend.Backend
	data         []byte
	calls        atomic.Int64
	entered      chan struct{}
	firstCorrupt bool
}

func (b *b13CheckBackend) Properties() backend.Properties { return backend.Properties{Connections: 2} }
func (b *b13CheckBackend) AllowAdditionalLoadSlot() bool  { return true }
func (b *b13CheckBackend) IsPermanentError(error) bool    { return false }
func (b *b13CheckBackend) Load(ctx context.Context, _ backend.Handle, _ int, _ int64, fn func(io.Reader) error) error {
	call := b.calls.Add(1)
	if b.entered != nil {
		select {
		case b.entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return ctx.Err()
	}
	data := b.data
	if b.firstCorrupt && call == 1 {
		data = bytes.Clone(data)
		data[0] ^= 1
	}
	return fn(bytes.NewReader(data))
}

func TestCapacityCheckDoesNotLoseEarlierCorruption(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	key := crypto.NewRandomKey()
	data, blobs, id := b13WindowPack(t, key, 2<<20)
	be := &b13CheckBackend{data: data, firstCorrupt: true}
	r := b11Repository(key, sema.NewBackend(be), id, blobs)
	errs := collectErrors(ctx, func(ctx context.Context, errs chan<- error) {
		newChecker(r).ReadPacks(ctx, func(p map[restic.ID]int64) map[restic.ID]int64 { return p }, restic.NewNoopPrinter(), errs)
	})
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "decrypting blob") || be.calls.Load() != 2 {
		t.Fatalf("earlier corruption erased on resource replay: errs=%v calls=%d", errs, be.calls.Load())
	}
}

type b13CountPrinter struct {
	restic.Printer
	count atomic.Uint64
}

func (p *b13CountPrinter) NewCounter(string) restic.Counter { return p }
func (p *b13CountPrinter) Add(n uint64)                     { p.count.Add(n) }
func (*b13CountPrinter) SetMax(uint64)                      {}
func (p *b13CountPrinter) Get() (uint64, uint64)            { return p.count.Load(), 0 }
func (*b13CountPrinter) Done()                              {}

func TestCapacityCheckPromotionAndCancellation(t *testing.T) {
	key := crypto.NewRandomKey()
	data, blobs, id := b13WindowPack(t, key, 2<<20)
	for _, cancelLoad := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		be := &b13CheckBackend{data: data}
		if cancelLoad {
			be.entered = make(chan struct{}, 1)
		}
		notifications := atomic.Int64{}
		wrapped := retry.New(sema.NewBackend(be), time.Millisecond, func(string, error, time.Duration) { notifications.Add(1) }, nil)
		r := b11Repository(key, wrapped, id, blobs)
		printer := &b13CountPrinter{Printer: restic.NewNoopPrinter()}
		done := make(chan []error, 1)
		go func() {
			done <- collectErrors(ctx, func(ctx context.Context, errs chan<- error) {
				newChecker(r).ReadPacks(ctx, func(p map[restic.ID]int64) map[restic.ID]int64 { return p }, printer, errs)
			})
		}()
		if cancelLoad {
			select {
			case <-be.entered:
				cancel()
			case <-time.After(5 * time.Second):
				t.Fatal("load did not enter")
			}
		}
		select {
		case errs := <-done:
			if !cancelLoad && (len(errs) != 0 || be.calls.Load() != 2 || printer.count.Load() != 1 || notifications.Load() != 0) {
				t.Fatal(fmt.Sprintf("promotion errs=%v calls=%d progress=%d backend-failures=%d", errs, be.calls.Load(), printer.count.Load(), notifications.Load()))
			}
		case <-time.After(5 * time.Second):
			t.Fatal("promotion/cancellation left workers waiting")
		}
		cancel()
	}
}

type b13OccupiedCheckBackend struct {
	*b13CheckBackend
	packName string
	ordinary chan struct{}
	refused  chan struct{}
}

func (b *b13OccupiedCheckBackend) Load(ctx context.Context, h backend.Handle, _ int, _ int64, fn func(io.Reader) error) error {
	if h.Name != b.packName {
		b.ordinary <- struct{}{}
		<-ctx.Done()
		return ctx.Err()
	}
	err := fn(bytes.NewReader(b.data))
	if err == nil {
		b.refused <- struct{}{}
	}
	return err
}

func TestCapacityCheckCanceledPromotionWithOrdinaryWorkersOccupied(t *testing.T) {
	key := crypto.NewRandomKey()
	var data []byte
	var blobs pack.Blobs
	var id restic.ID
	// ListPacks progresses by the low nibble. Arrange the refusing pack after
	// the two ordinary tasks; random authenticated pack IDs supply that order.
	for attempt := 0; attempt < 256; attempt++ {
		data, blobs, id = b13WindowPack(t, key, 2<<20)
		if id[0]&15 == 15 {
			break
		}
	}
	if id[0]&15 != 15 {
		t.Fatal("could not arrange pack enumeration")
	}
	be := &b13OccupiedCheckBackend{b13CheckBackend: &b13CheckBackend{data: data}, packName: id.String(), ordinary: make(chan struct{}, 2), refused: make(chan struct{}, 1)}
	r := b11Repository(key, sema.NewBackend(be), id, blobs)
	idx := index.NewIndex()
	for n := byte(0); n < 2; n++ {
		var packID restic.ID
		packID[0] = n
		packID[1] = 99
		idx.StorePack(packID, pack.Blobs{{BlobHandle: restic.BlobHandle{ID: restic.Hash([]byte{n}), Type: restic.DataBlob}, Length: 3 << 20}})
	}
	r.idx.Insert(idx)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan []error, 1)
	go func() {
		done <- collectErrors(ctx, func(ctx context.Context, errs chan<- error) {
			newChecker(r).ReadPacks(ctx, func(p map[restic.ID]int64) map[restic.ID]int64 { return p }, restic.NewNoopPrinter(), errs)
		})
	}()
	for n := 0; n < 2; n++ {
		select {
		case <-be.ordinary:
		case <-ctx.Done():
			t.Fatal("ordinary workers did not enter")
		}
	}
	select {
	case <-be.refused:
	case <-ctx.Done():
		t.Fatal("bounded worker did not unwind refusal beside occupied workers")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("canceled promotion did not join workers")
	}
}
