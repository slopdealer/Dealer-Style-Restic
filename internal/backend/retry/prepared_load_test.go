package retry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/mock"
)

func TestPreparedLoadOneUseRetryAndScope(t *testing.T) {
	TestFastRetries(t)
	h := backend.Handle{Type: backend.SnapshotFile, Name: "snapshot"}
	var network int
	base := &mock.Backend{OpenReaderFn: func(_ context.Context, _ backend.Handle, _ int, _ int64) (io.ReadCloser, error) {
		network++
		return io.NopCloser(bytes.NewBufferString("network")), nil
	}}
	be := New(base, time.Second, nil, nil)
	ctx, finish := WithPreparedLoad(t.Context(), be, h, []byte("prepared"))
	defer finish()
	// An unrelated handle, range or backend must not spend this token.
	for _, call := range []func() error{
		func() error {
			return be.Load(ctx, backend.Handle{Type: backend.SnapshotFile, Name: "other"}, 0, 0, func(io.Reader) error { return nil })
		},
		func() error { return be.Load(ctx, h, 1, 0, func(io.Reader) error { return nil }) },
		func() error {
			return New(base, time.Second, nil, nil).Load(ctx, h, 0, 0, func(io.Reader) error { return nil })
		},
	} {
		if err := call(); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	err := be.Load(ctx, h, 0, 0, func(rd io.Reader) error {
		raw, e := io.ReadAll(rd)
		if e != nil {
			return e
		}
		calls++
		if calls == 1 {
			if string(raw) != "prepared" {
				t.Fatal(string(raw))
			}
			return errors.New("consumer failed")
		}
		if string(raw) != "network" {
			t.Fatal(string(raw))
		}
		return nil
	})
	if err != nil || calls != 2 || network != 4 {
		t.Fatalf("err=%v calls=%d network=%d", err, calls, network)
	}
}

func TestPreparedLoadReleaseCancelAndCircuitBreaker(t *testing.T) {
	h := backend.Handle{Type: backend.SnapshotFile, Name: "snapshot"}
	be := New(&mock.Backend{}, time.Second, nil, nil)
	ctx, finish := WithPreparedLoad(t.Context(), be, h, []byte("prepared"))
	token := ctx.Value(preparedLoadKey{}).(*preparedLoad)
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if err := be.Load(canceled, h, 0, 0, func(io.Reader) error { t.Fatal("canceled consumer"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	be.failedLoads.Store(h, time.Now())
	if err := be.Load(ctx, h, 0, 0, func(io.Reader) error { t.Fatal("open breaker consumer"); return nil }); err == nil {
		t.Fatal("bypassed circuit breaker")
	}
	finish()
	finish()
	if token.raw != nil {
		t.Fatal("retained abandoned ciphertext")
	}
}

func TestPreparedLoadConcurrentInvocationIsolation(t *testing.T) {
	h := backend.Handle{Type: backend.SnapshotFile, Name: "snapshot"}
	var network atomic.Int32
	base := &mock.Backend{OpenReaderFn: func(_ context.Context, _ backend.Handle, _ int, _ int64) (io.ReadCloser, error) {
		network.Add(1)
		return io.NopCloser(bytes.NewBufferString("network")), nil
	}}
	be := New(base, time.Second, nil, nil)
	ctx, finish := WithPreparedLoad(t.Context(), be, h, []byte("prepared"))
	defer finish()
	var prepared atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := be.Load(ctx, h, 0, 0, func(rd io.Reader) error {
				raw, e := io.ReadAll(rd)
				if string(raw) == "prepared" {
					prepared.Add(1)
				}
				return e
			})
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if prepared.Load() != 1 || network.Load() != 7 {
		t.Fatalf("prepared=%d network=%d", prepared.Load(), network.Load())
	}
	// A different invocation owns its own token, even for the same handle.
	ctx2, finish2 := WithPreparedLoad(t.Context(), be, h, []byte("second"))
	defer finish2()
	if err := be.Load(ctx2, h, 0, 0, func(rd io.Reader) error {
		raw, e := io.ReadAll(rd)
		if string(raw) != "second" {
			t.Error(string(raw))
		}
		return e
	}); err != nil {
		t.Fatal(err)
	}
}
