package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/cenkalti/backoff/v4"
	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/backend/mock"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

type b23CountReader struct {
	io.Reader
	bytes *int
}

// Hold both ordinary allocation permits before the bounded worker encounters
// corruption or decoder promotion. Only the ordinary workers can release
// those permits. Corruption requires actual indexed alternate-pack recovery.
func TestB23RepositoryRecoveryAtSaturatedAllocation(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		connections                 uint
		overflow, disk, understated bool
	}{
		{"explicit-one", 1, false, false, false},
		{"explicit-two", 2, false, false, false},
		{"default-overflow-mock", 2, true, false, false},
		{"default-overflow-local", 2, true, true, false},
		{"understated-one", 1, false, false, true},
		{"understated-two", 2, false, false, true},
		{"understated-overflow", 2, true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			connections := tc.connections
			key := testKey(t)
			blobs, data := buildPackfileWithoutHeader([]int{600000, 600000, 600000, 600000, 600000, 600000}, &key, true)
			corrupt := bytes.Clone(data)
			for i, b := range blobs {
				if tc.understated {
					// Complete authenticated bytes, deliberately understated output
					// capacity: promotion must yield before saturated admission.
					blobs[i].UncompressedLength = 1
				} else {
					corrupt[b.Offset+16] ^= 1
				}
			}
			originalID, alternateID := restic.Hash(corrupt), restic.Hash(data)
			m := mock.NewBackend()
			m.PropertiesFn = func() backend.Properties { return backend.Properties{Connections: connections} }
			m.OpenReaderFn = func(_ context.Context, h backend.Handle, n int, off int64) (io.ReadCloser, error) {
				p := corrupt
				if h.Name == alternateID.String() {
					p = data
				}
				return io.NopCloser(bytes.NewReader(bytes.Clone(p[int(off) : int(off)+n]))), nil
			}
			mi := index.NewMasterIndex()
			idx := index.NewIndex()
			idx.StorePack(alternateID, blobs)
			mi.Insert(idx)
			var limited backend.Backend = m
			if tc.overflow {
				limited = b23OverflowBackend{m}
			}
			if tc.disk {
				cfg := local.NewConfig()
				cfg.Path = t.TempDir()
				cfg.SetAllowAdditionalLoadSlot(true)
				lb, err := local.Create(context.Background(), cfg, nil)
				if err != nil {
					t.Fatal(err)
				}
				for _, value := range []struct {
					id    restic.ID
					bytes []byte
				}{{originalID, corrupt}, {alternateID, data}} {
					if err := lb.Save(context.Background(), backend.Handle{Type: backend.PackFile, Name: value.id.String()}, backend.NewByteReader(value.bytes, lb.Hasher())); err != nil {
						t.Fatal(err)
					}
				}
				limited = lb
			}
			r := &Repository{key: &key, be: sema.NewBackend(limited), idx: mi}
			defer func() {
				if r.dec != nil {
					r.dec.Close()
				}
				if r.smallRestoreDecoder != nil {
					r.smallRestoreDecoder.Close()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Ordinary workers use unmarked contexts exactly as the restorer does.
			// The repository alone marks the bounded worker for optional overflow.
			permits := make(chan struct{}, 2)
			permits <- struct{}{}
			permits <- struct{}{}
			promotion := make(chan struct{})
			admissions, releases := 0, 0
			boundedCtx := restic.WithSmallRestoreLoad(ctx, func(ctx context.Context) (func(), error) {
				admissions++
				close(promotion)
				select {
				case permits <- struct{}{}:
					return func() { <-permits; releases++ }, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			})
			results := make(chan error, 3)
			run := func(workCtx context.Context, ordinary bool) {
				if ordinary {
					defer func() { <-permits }()
				}
				callbacks := 0
				err := r.loadBlobsFromPack(workCtx, originalID, append(pack.Blobs(nil), blobs...), func(h restic.BlobHandle, p []byte, e error) error {
					if e != nil {
						return e
					}
					if callbacks >= len(blobs) || h != blobs[callbacks].BlobHandle || restic.Hash(p) != h.ID {
						return errors.New("wrong recovered content/order")
					}
					callbacks++
					return nil
				})
				if err == nil && callbacks != len(blobs) {
					err = fmt.Errorf("callbacks=%d", callbacks)
				}
				results <- err
			}
			go run(boundedCtx, false)
			select {
			case <-promotion:
			case <-ctx.Done():
				t.Fatal("bounded worker never reached saturated allocation")
			}
			go run(ctx, true)
			go run(ctx, true)
			for range 3 {
				select {
				case err := <-results:
					if err != nil {
						t.Errorf("recovery: %v", err)
					}
				case <-time.After(6 * time.Second):
					t.Fatal("recovery workers did not join")
				}
			}
			if admissions != 1 || releases != 1 {
				t.Fatalf("permit lifetime: admissions=%d releases=%d", admissions, releases)
			}
		})
	}
}

func (r b23CountReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	*r.bytes += n
	return n, err
}

// This backend distinguishes a body interruption from failure after the
// consumer succeeds. The retry and connection admission wrappers are real.
type b23FaultBackend struct {
	backend.Backend
	data         []byte
	cut          int
	terminal     bool
	permanent    bool
	calls, moved int
	ranges       [][2]int64
}

func (b *b23FaultBackend) Load(_ context.Context, _ backend.Handle, n int, off int64, fn func(io.Reader) error) error {
	b.calls++
	b.ranges = append(b.ranges, [2]int64{off, int64(n)})
	if n <= 0 || off < 0 || off+int64(n) > int64(len(b.data)) {
		return backoff.Permanent(fmt.Errorf("invalid range %d/%d", off, n))
	}
	p := bytes.Clone(b.data[int(off) : int(off)+n])
	if b.calls == 1 && b.cut >= 0 && b.cut < len(p) {
		p = p[:b.cut]
	}
	err := fn(b23CountReader{bytes.NewReader(p), &b.moved})
	if err != nil {
		return err
	}
	if b.terminal && (b.calls == 1 || b.permanent) {
		err := errors.New("post-consumer failure")
		if b.permanent {
			return backoff.Permanent(err)
		}
		return err
	}
	return nil
}

func TestB23StreamFaultMatrix(t *testing.T) {
	retry.TestFastRetries(t)
	for _, tc := range []struct {
		name                string
		cut, rejectAt       int
		terminal, permanent bool
		cancelOnReject      bool
	}{
		{name: "partial-first-blob", cut: 17},
		{name: "after-first-window", cut: 400},
		{name: "inside-second-window", cut: 650},
		{name: "post-consumer-transient", cut: -1, terminal: true},
		{name: "post-consumer-permanent", cut: -1, terminal: true, permanent: true},
		{name: "handler-rejection", cut: -1, rejectAt: 1},
		{name: "handler-rejection-after-earlier-delivery", cut: -1, rejectAt: 2},
		{name: "handler-rejection-with-cancellation", cut: -1, rejectAt: 2, cancelOnReject: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			key := testKey(t)
			blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			m := mock.NewBackend()
			m.PropertiesFn = func() backend.Properties { return backend.Properties{Connections: 1} }
			fault := &b23FaultBackend{Backend: m, data: data, cut: tc.cut, terminal: tc.terminal, permanent: tc.permanent}
			be := retry.New(sema.NewBackend(fault), time.Second, nil, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			seen := make(map[restic.ID]int)
			rejection := errors.New("handler rejected after observable work")
			callbacks, callsOnReject := 0, 0
			err = streamPackWithPolicy(sema.WithAdditionalLoadSlot(ctx), be.Load, nil, dec, &key, restic.Hash(data), blobs,
				func(h restic.BlobHandle, p []byte, blobErr error) error {
					callbacks++
					seen[h.ID]++
					if callbacks > len(blobs) || h != blobs[callbacks-1].BlobHandle {
						t.Errorf("callback order changed at %d", callbacks)
					}
					if seen[h.ID] != 1 {
						t.Errorf("repeated callback for %v", h)
					}
					if blobErr != nil || restic.Hash(p) != h.ID {
						t.Errorf("bad delivery: %v", blobErr)
					}
					if tc.rejectAt != 0 && callbacks == tc.rejectAt {
						callsOnReject = fault.calls
						if tc.cancelOnReject {
							cancel()
						}
						return rejection
					}
					return nil
				}, 512, false)
			if tc.rejectAt != 0 {
				if !errors.Is(err, rejection) || callbacks != tc.rejectAt || fault.calls != callsOnReject {
					t.Fatalf("rejection replay: err=%v callbacks=%d loads=%d", err, callbacks, fault.calls)
				}
			} else if tc.permanent {
				if err == nil {
					t.Fatal("terminal backend failure silently accepted")
				}
			} else if err != nil || callbacks != len(blobs) {
				t.Fatalf("err=%v callbacks=%d want=%d", err, callbacks, len(blobs))
			}
			t.Logf("loads=%d consumed_bytes=%d pack_bytes=%d callbacks=%d ranges=%v error=%v", fault.calls, fault.moved, len(data), callbacks, fault.ranges, err)
		})
	}
}

type b23CanceledReader struct {
	ctx     context.Context
	prefix  io.Reader
	entered chan struct{}
	once    sync.Once
}

func (r *b23CanceledReader) Read(p []byte) (int, error) {
	n, err := r.prefix.Read(p)
	if n != 0 || err != io.EOF {
		return n, err
	}
	r.once.Do(func() { close(r.entered) })
	<-r.ctx.Done()
	return 0, r.ctx.Err()
}

func TestB23StreamCancellationDuringBody(t *testing.T) {
	retry.TestFastRetries(t)
	key := testKey(t)
	blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered := make(chan struct{})
	m := mock.NewBackend()
	m.OpenReaderFn = func(ctx context.Context, _ backend.Handle, n int, off int64) (io.ReadCloser, error) {
		end := int(off) + n
		cut := int(blobs[0].Length) + 2
		if end > cut {
			end = cut
		}
		if end < int(off) {
			end = int(off)
		}
		return io.NopCloser(&b23CanceledReader{ctx: ctx, prefix: bytes.NewReader(data[int(off):end]), entered: entered}), nil
	}
	be := retry.New(sema.NewBackend(m), time.Second, nil, nil)
	callbacks := 0
	done := make(chan error, 1)
	go func() {
		done <- streamPackWithPolicy(sema.WithAdditionalLoadSlot(ctx), be.Load, nil, dec, &key, restic.Hash(data), blobs,
			func(h restic.BlobHandle, p []byte, e error) error {
				callbacks++
				if e != nil {
					return e
				}
				if restic.Hash(p) != h.ID {
					return errors.New("bad content")
				}
				return nil
			}, 512, false)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("body did not block")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || callbacks != 1 {
			t.Fatalf("body cancellation: %v callbacks=%d", err, callbacks)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("body cancellation did not join")
	}
}

func TestB23StreamCancellationJoinsBorrowedCallback(t *testing.T) {
	key := testKey(t)
	blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, unblock := make(chan struct{}), make(chan struct{})
	callbacks := 0
	done := make(chan error, 1)
	load := func(_ context.Context, _ backend.Handle, n int, off int64, fn func(io.Reader) error) error {
		return fn(bytes.NewReader(bytes.Clone(data[int(off) : int(off)+n])))
	}
	go func() {
		done <- streamPackWithPolicy(ctx, load, nil, dec, &key, restic.Hash(data), blobs, func(h restic.BlobHandle, p []byte, e error) error {
			callbacks++
			if e != nil {
				return e
			}
			before := bytes.Clone(p)
			close(entered)
			<-unblock
			if !bytes.Equal(p, before) || restic.Hash(p) != h.ID {
				return errors.New("borrowed buffer changed before callback return")
			}
			return nil
		}, 512, false)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("callback did not start")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("returned before borrowed callback joined: %v", err)
	default:
	}
	close(unblock)
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || callbacks != 1 {
			t.Fatalf("callback cancellation: %v callbacks=%d", err, callbacks)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("callback cancellation did not join")
	}
}

// All workers start together and every selected copy is corrupt. Recovery
// takes the same explicitly bounded connection path to a valid alternate.
func TestB23StreamSaturatedAlternateRecovery(t *testing.T) {
	for _, connections := range []uint{1, 2} {
		t.Run(fmt.Sprint(connections), func(t *testing.T) {
			key := testKey(t)
			blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
			corrupt := bytes.Clone(data)
			for _, b := range blobs {
				corrupt[b.Offset+16] ^= 1
			}
			m := mock.NewBackend()
			m.PropertiesFn = func() backend.Properties { return backend.Properties{Connections: connections} }
			m.OpenReaderFn = func(_ context.Context, h backend.Handle, n int, off int64) (io.ReadCloser, error) {
				p := corrupt
				if h.Name == "alternate" {
					p = data
				}
				return io.NopCloser(bytes.NewReader(bytes.Clone(p[int(off) : int(off)+n]))), nil
			}
			be := sema.NewBackend(m)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = sema.WithAdditionalLoadSlot(ctx) // cancellable, no overflow enabled
			start := make(chan struct{})
			results := make(chan error, 3)
			var ready sync.WaitGroup
			ready.Add(3)
			for range 3 {
				go func() {
					dec, err := zstd.NewReader(nil)
					if err != nil {
						results <- err
						ready.Done()
						return
					}
					defer dec.Close()
					ready.Done()
					<-start
					recoverBlob := func(ctx context.Context, h restic.BlobHandle, _ []byte) ([]byte, error) {
						for _, b := range blobs {
							if b.BlobHandle != h {
								continue
							}
							var p []byte
							err := be.Load(ctx, backend.Handle{Type: backend.PackFile, Name: "alternate"}, int(b.Length), int64(b.Offset), func(rd io.Reader) error {
								cipher := make([]byte, b.Length)
								if _, err := io.ReadFull(rd, cipher); err != nil {
									return err
								}
								var err error
								p, err = key.Open(nil, cipher[:key.NonceSize()], cipher[key.NonceSize():], nil)
								return err
							})
							return p, err
						}
						return nil, errors.New("unknown handle")
					}
					callbacks := 0
					err = streamPackWithPolicy(ctx, be.Load, recoverBlob, dec, &key, restic.Hash(data), append(blobs[:0:0], blobs...), func(h restic.BlobHandle, p []byte, e error) error {
						callbacks++
						if e != nil {
							return e
						}
						if restic.Hash(p) != h.ID {
							return errors.New("invalid recovered content")
						}
						return nil
					}, 512, false)
					if err == nil && callbacks != len(blobs) {
						err = fmt.Errorf("callbacks %d", callbacks)
					}
					results <- err
				}()
			}
			ready.Wait()
			close(start)
			for range 3 {
				select {
				case err := <-results:
					if err != nil {
						t.Errorf("saturated recovery: %v", err)
					}
				case <-time.After(6 * time.Second):
					t.Fatal("workers did not join after cancellation")
				}
			}
		})
	}
}

// An understated compressed size must release its response before waiting for
// ordinary allocation admission. Admission itself probes the only connection.
func TestB23UnderstatedAdmissionReleasesConnection(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(fmt.Sprintf("cancel=%v", canceled), func(t *testing.T) {
			key := testKey(t)
			blobs, data := buildPackfileWithoutHeader([]int{600000, 600000, 600000}, &key, true)
			for i := range blobs {
				blobs[i].UncompressedLength = 1
			}
			m := mock.NewBackend()
			m.PropertiesFn = func() backend.Properties { return backend.Properties{Connections: 1} }
			m.OpenReaderFn = func(_ context.Context, _ backend.Handle, n int, off int64) (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(bytes.Clone(data[int(off) : int(off)+n]))), nil
			}
			be := sema.NewBackend(m)
			r := &Repository{be: be, key: &key}
			defer func() {
				if r.dec != nil {
					r.dec.Close()
				}
				if r.smallRestoreDecoder != nil {
					r.smallRestoreDecoder.Close()
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			ctx = sema.WithAdditionalLoadSlot(ctx)
			entered := make(chan struct{})
			admissions, releases, callbacks := 0, 0, 0
			ctx = restic.WithSmallRestoreLoad(ctx, func(ctx context.Context) (func(), error) {
				admissions++
				// Deadlocks if promotion runs while its response owns the token.
				if err := be.Load(ctx, backend.Handle{Type: backend.PackFile, Name: "admission-probe"}, 1, 0, func(rd io.Reader) error {
					var p [1]byte
					_, err := io.ReadFull(rd, p[:])
					return err
				}); err != nil {
					return nil, err
				}
				close(entered)
				if canceled {
					<-ctx.Done()
					return nil, ctx.Err()
				}
				return func() { releases++ }, nil
			})
			done := make(chan error, 1)
			go func() {
				done <- r.loadBlobsFromPack(ctx, restic.Hash(data), blobs, func(h restic.BlobHandle, p []byte, e error) error {
					callbacks++
					if e != nil {
						return e
					}
					if restic.Hash(p) != h.ID {
						return errors.New("wrong decoded content")
					}
					return nil
				})
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("promotion retained the response connection")
			}
			if canceled {
				cancel()
			}
			select {
			case err := <-done:
				if canceled {
					if !errors.Is(err, context.Canceled) || callbacks != 0 || releases != 0 {
						t.Fatalf("cancellation: %v callbacks=%d releases=%d", err, callbacks, releases)
					}
				} else if err != nil || callbacks != 3 || admissions != 1 || releases != 1 {
					t.Fatalf("promotion: %v callbacks=%d admissions=%d releases=%d", err, callbacks, admissions, releases)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("promotion did not join")
			}
		})
	}
}
