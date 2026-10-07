package repository

import (
	"bytes"
	"context"
	"errors"
	"github.com/restic/restic/internal/backend/mock"
	"github.com/restic/restic/internal/backend/sema"
	"io"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/restic"
)

func TestBoundedRestoreRangesAndDecodeFallback(t *testing.T) {
	for _, understated := range []bool{false, true} {
		t.Run(map[bool]string{false: "bounded", true: "understated-index"}[understated], func(t *testing.T) {
			key := testKey(t)
			blobs, data := buildPackfileWithoutHeader([]int{600000, 600000, 600000}, &key, true)
			if understated {
				for i := range blobs {
					blobs[i].UncompressedLength = 1
				}
			}
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			small, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(restic.SmallRestoreBlobLimit), zstd.WithDecodeAllCapLimit(true))
			if err != nil {
				t.Fatal(err)
			}
			defer small.Close()
			admissions := 0
			ctx := context.WithValue(context.Background(), smallRestoreDecodeKey{}, smallRestoreDecodePolicy{small, func(context.Context) error { admissions++; return nil }})
			loads, callbacks := 0, 0
			load := func(_ context.Context, _ backend.Handle, n int, off int64, fn func(io.Reader) error) error {
				loads++
				if n > maxStreamPackRequestSize {
					t.Fatalf("request %d exceeds span bound", n)
				}
				return fn(bytes.NewReader(data[int(off) : int(off)+n]))
			}
			pool := streamPackBufferPool
			streamPackBufferPool = make(chan []byte, 2)
			sentinel := make([]byte, 0, maxStreamPackChunkSize)
			streamPackBufferPool <- sentinel
			defer func() { streamPackBufferPool = pool }()
			err = streamPackWithPolicy(ctx, load, nil, dec, &key, restic.ID{}, blobs, func(h restic.BlobHandle, b []byte, e error) error {
				callbacks++
				if e != nil {
					t.Fatal(e)
				}
				if restic.Hash(b) != h.ID {
					t.Fatal("wrong plaintext")
				}
				return nil
			}, restic.SmallRestoreCiphertextLimit, false)
			if err != nil {
				t.Fatal(err)
			}
			wantLoads := 1
			if understated {
				wantLoads = 4 // coalesced response yields, then three ordinary windows
			}
			if loads != wantLoads || callbacks != 3 {
				t.Fatalf("loads/callbacks %d/%d", loads, callbacks)
			}
			if understated && admissions != 3 {
				t.Fatalf("unrestricted decoding bypassed admission: %d", admissions)
			}
			if !understated && admissions != 0 {
				t.Fatalf("valid bounded decode needed admission: %d", admissions)
			}
			if len(streamPackBufferPool) != 1 || &(<-streamPackBufferPool)[:cap(sentinel)][0] != &sentinel[:cap(sentinel)][0] {
				t.Fatal("bounded stream used ordinary pool")
			}
		})
	}
}

func TestBoundedRestoreDecoderCacheOwnership(t *testing.T) {
	r := &Repository{}
	first, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("concurrent streams share a decoder")
	}
	r.putSmallRestoreDecoder(first)
	r.putSmallRestoreDecoder(second)
	if _, err := second.DecodeAll(nil, nil); err != zstd.ErrDecoderClosed {
		t.Fatalf("excess decoder remains open: %v", err)
	}
	reused, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer reused.Close()
	if reused != first {
		t.Fatal("idle decoder was not reused")
	}
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	plain := bytes.Repeat([]byte("bounded restore"), 1000)
	encoded := enc.EncodeAll(plain, nil)
	// An undersized destination must fail without poisoning later reuse.
	if _, err := reused.DecodeAll(encoded, make([]byte, 0, 1)); err == nil {
		t.Fatal("cap limit was ignored")
	}
	got, err := reused.DecodeAll(encoded, make([]byte, 0, len(plain)))
	if err != nil || !bytes.Equal(plain, got) {
		t.Fatalf("reuse after capped decode failure: %v", err)
	}
}

func TestBoundedRestoreDecoderCancellationDuringCallback(t *testing.T) {
	key := testKey(t)
	blobs, data := buildPackfileWithoutHeader([]int{600000}, &key, true)
	be := mock.NewBackend()
	be.OpenReaderFn = func(_ context.Context, _ backend.Handle, n int, off int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data[int(off) : int(off)+n])), nil
	}
	r := &Repository{be: be, key: &key}
	first, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	r.putSmallRestoreDecoder(first)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx = restic.WithSmallRestoreLoad(ctx, func(context.Context) (func(), error) {
		t.Error("valid bounded blob entered ordinary recovery")
		return func() {}, nil
	})
	entered, unblock := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- r.loadBlobsFromPack(ctx, restic.ID{}, blobs, func(_ restic.BlobHandle, _ []byte, err error) error {
			if err != nil {
				return err
			}
			close(entered)
			<-unblock
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not start")
	}
	cancel()
	second, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second == first {
		t.Fatal("cancellation returned decoder before callback completion")
	}
	close(unblock)
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("stream: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stream did not join")
	}
	reused, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer reused.Close()
	if reused != first {
		t.Fatal("completed stream did not return decoder")
	}
	r.dec.Close()
}

type boundedSlotBackend struct{ backend.Backend }

func (boundedSlotBackend) AllowAdditionalLoadSlot() bool { return true }

func TestBoundedRestoreRepositoryObtainsOverflowAdmission(t *testing.T) {
	key := testKey(t)
	blobs, data := buildPackfileWithoutHeader([]int{600000, 600000}, &key, true)
	entered := make(chan struct{}, 2)
	m := mock.NewBackend()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.OpenReaderFn = func(ctx context.Context, h backend.Handle, n int, off int64) (io.ReadCloser, error) {
		if h.Name == "ordinary" {
			entered <- struct{}{}
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return io.NopCloser(bytes.NewReader(data[int(off) : int(off)+n])), nil
	}
	be := sema.NewBackend(boundedSlotBackend{m})
	ordinaryDone := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			ordinaryDone <- be.Load(ctx, backend.Handle{Type: backend.PackFile, Name: "ordinary"}, 1, 0, func(io.Reader) error { return nil })
		}()
	}
	for i := 0; i < 2; i++ {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			t.Fatal("ordinary backend loads did not enter")
		}
	}
	r := &Repository{be: be, key: &key}
	defer func() {
		if r.smallRestoreDecoder != nil {
			r.smallRestoreDecoder.Close()
		}
		if r.dec != nil {
			r.dec.Close()
		}
	}()
	boundedCtx := restic.WithSmallRestoreLoad(ctx, func(context.Context) (func(), error) { t.Error("valid blobs required recovery"); return func() {}, nil })
	done := make(chan error, 1)
	abort := errors.New("callback abort")
	callbacks := 0
	go func() {
		done <- r.loadBlobsFromPack(boundedCtx, restic.Hash([]byte("pack")), blobs, func(_ restic.BlobHandle, _ []byte, err error) error {
			callbacks++
			if err != nil {
				return err
			}
			return abort
		})
	}()
	select {
	case err := <-done:
		if !errors.Is(err, abort) {
			t.Fatalf("callback error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("repository stream failed to obtain overflow admission")
	}
	if callbacks != 1 {
		t.Fatalf("callback abort retried: %d calls", callbacks)
	}
	cancel()
	for i := 0; i < 2; i++ {
		select {
		case <-ordinaryDone:
		case <-time.After(5 * time.Second):
			t.Fatal("ordinary loads did not join")
		}
	}
}

func TestBoundedRestoreDecoderCancellationDuringRecovery(t *testing.T) {
	key := testKey(t)
	blobs, data := buildPackfileWithoutHeader([]int{600000}, &key, true)
	blobs[0].UncompressedLength = 1
	be := mock.NewBackend()
	be.OpenReaderFn = func(_ context.Context, _ backend.Handle, n int, off int64) (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(data[int(off) : int(off)+n])), nil
	}
	r := &Repository{be: be, key: &key}
	first, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	r.putSmallRestoreDecoder(first)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	entered := make(chan struct{})
	ctx = restic.WithSmallRestoreLoad(ctx, func(ctx context.Context) (func(), error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	done := make(chan error, 1)
	go func() {
		done <- r.loadBlobsFromPack(ctx, restic.ID{}, blobs, func(restic.BlobHandle, []byte, error) error {
			t.Error("callback after canceled recovery")
			return nil
		})
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery admission did not start")
	}
	second, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second == first {
		t.Fatal("decoder returned before recovery joined")
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("stream: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("canceled recovery did not join")
	}
	reused, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	defer reused.Close()
	if reused != first {
		t.Fatal("completed recovery did not return decoder")
	}
	r.dec.Close()
}

func TestBoundedRestoreDecoderShutdown(t *testing.T) {
	r := &Repository{be: mock.NewBackend()}
	idle, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	active, err := r.takeSmallRestoreDecoder()
	if err != nil {
		t.Fatal(err)
	}
	r.putSmallRestoreDecoder(idle)
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := idle.DecodeAll(nil, nil); err != zstd.ErrDecoderClosed {
		t.Fatalf("idle decoder: %v", err)
	}
	if _, err := active.DecodeAll(nil, nil); err == zstd.ErrDecoderClosed {
		t.Fatal("shutdown closed an active decoder")
	}
	r.putSmallRestoreDecoder(active)
	if _, err := active.DecodeAll(nil, nil); err != zstd.ErrDecoderClosed {
		t.Fatalf("returned decoder: %v", err)
	}
	if _, err := r.takeSmallRestoreDecoder(); err != zstd.ErrDecoderClosed {
		t.Fatalf("post-close checkout: %v", err)
	}
}
