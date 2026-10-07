package repository

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/cache"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
)

type snapshotPrepareBackend struct {
	backend.Backend
	connections uint
	loads       int
	load        func(context.Context, backend.Handle, int, int64, func(io.Reader) error) error
}

func (b *snapshotPrepareBackend) Properties() backend.Properties {
	p := b.Backend.Properties()
	if b.connections != 0 {
		p.Connections = b.connections
	}
	return p
}

func (b *snapshotPrepareBackend) Load(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
	b.loads++
	if b.load != nil {
		return b.load(ctx, h, n, off, fn)
	}
	return b.Backend.Load(ctx, h, n, off, fn)
}

func snapshotPrepareFixture(t *testing.T, version uint, payload []byte) (*Repository, backend.Backend, restic.ID, int64) {
	t.Helper()
	r, be := TestRepositoryWithBackend(t, nil, version, Options{})
	id, err := r.SaveUnpacked(context.Background(), restic.WriteableSnapshotFile, payload)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := be.Stat(context.Background(), backend.Handle{Type: backend.SnapshotFile, Name: id.String()})
	if err != nil {
		t.Fatal(err)
	}
	r.be = retry.New(be, time.Second, nil, nil)
	return r, be, id, fi.Size
}

func TestPreparedSnapshotConsumesOnceAndAuthenticatesAtConsumption(t *testing.T) {
	for _, version := range []uint{1, 2} {
		payload := bytes.Repeat([]byte(`{"tree":"a"}`), 100)
		r, be, id, size := snapshotPrepareFixture(t, version, payload)
		fault := &snapshotPrepareBackend{Backend: be, connections: 2}
		r.be = retry.New(fault, time.Second, nil, nil)
		p := r.PrepareSnapshot(context.Background(), id, size)
		cached, ok := p.(*preparedSnapshot)
		if !ok || fault.loads != 1 || len(cached.raw) != int(size) || cap(cached.raw) != int(size)+1 {
			t.Fatalf("prepared=%T loads=%d", p, fault.loads)
		}
		got, err := p.LoadUnpacked(context.Background(), restic.SnapshotFile, id)
		if err != nil || !bytes.Equal(got, payload) || cached.raw != nil || fault.loads != 1 {
			t.Fatalf("first load err=%v loads=%d retained=%d", err, fault.loads, len(cached.raw))
		}
		got, err = p.LoadUnpacked(context.Background(), restic.SnapshotFile, id)
		if err != nil || !bytes.Equal(got, payload) || fault.loads != 2 {
			t.Fatalf("second load err=%v loads=%d", err, fault.loads)
		}
	}
	// A matching storage hash cannot substitute for authentication.
	r, be, _, _ := snapshotPrepareFixture(t, 2, []byte(`{}`))
	raw := bytes.Repeat([]byte{0x39}, 80)
	id := restic.Hash(raw)
	h := backend.Handle{Type: backend.SnapshotFile, Name: id.String()}
	if err := be.Save(context.Background(), h, backend.NewByteReader(raw, be.Hasher())); err != nil {
		t.Fatal(err)
	}
	p := r.PrepareSnapshot(context.Background(), id, int64(len(raw)))
	_, got := p.LoadUnpacked(context.Background(), restic.SnapshotFile, id)
	_, want := r.LoadUnpacked(context.Background(), restic.SnapshotFile, id)
	if got == nil || want == nil || got.Error() != want.Error() {
		t.Fatalf("authentication got=%v want=%v", got, want)
	}
}

func TestPreparedSnapshotRejectsIncompleteOrUnsettledAttempts(t *testing.T) {
	for _, mode := range []string{"terminal", "callback-terminal", "short", "long", "hash", "missing", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			r, be, id, size := snapshotPrepareFixture(t, 1, []byte(`{"time":"now"}`))
			raw, err := r.LoadRaw(context.Background(), restic.SnapshotFile, id)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			marker := errors.New("terminal transfer failure")
			fault := &snapshotPrepareBackend{Backend: be, connections: 2}
			fault.load = func(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
				if fault.loads > 1 {
					return be.Load(ctx, h, n, off, fn)
				}
				body := append([]byte(nil), raw...)
				switch mode {
				case "short":
					body = body[:len(body)-1]
				case "long":
					body = append(body, 0)
				case "hash":
					body[0] ^= 1
				case "missing":
					return marker
				case "cancel":
					cancel()
				case "callback-terminal":
					return fn(&snapshotTerminalReader{data: body, err: marker})
				}
				if err := fn(bytes.NewReader(body)); err != nil {
					return err
				}
				if mode == "terminal" {
					return marker
				}
				return nil
			}
			r.be = retry.New(fault, time.Second, nil, nil)
			p := r.PrepareSnapshot(ctx, id, size)
			if p != r {
				t.Fatalf("retained failed %s attempt", mode)
			}
			got, err := p.LoadUnpacked(context.Background(), restic.SnapshotFile, id)
			if err != nil || !bytes.Equal(got, []byte(`{"time":"now"}`)) || fault.loads != 2 {
				t.Fatalf("fallback err=%v loads=%d", err, fault.loads)
			}
		})
	}
}

type snapshotTerminalReader struct {
	data []byte
	err  error
}

func (r *snapshotTerminalReader) Read(p []byte) (int, error) {
	n := copy(p, r.data)
	r.data = r.data[n:]
	if len(r.data) == 0 {
		return n, r.err
	}
	return n, nil
}

func TestPreparedSnapshotBoundConnectionsAndQuietFallback(t *testing.T) {
	r, be, id, size := snapshotPrepareFixture(t, 2, []byte(`{}`))
	fault := &snapshotPrepareBackend{Backend: be, connections: 1}
	r.be = retry.New(fault, time.Second, nil, nil)
	if r.PrepareSnapshot(context.Background(), id, size) != r || fault.loads != 0 {
		t.Fatal("one connection speculated")
	}
	fault.connections = 2
	for _, n := range []int64{0, -1, PreparedSnapshotLimit + 1} {
		if r.PrepareSnapshot(context.Background(), id, n) != r || fault.loads != 0 {
			t.Fatal("out-of-bound metadata speculated")
		}
	}
	marker := errors.New("quiet transport failure")
	fault.load = func(context.Context, backend.Handle, int, int64, func(io.Reader) error) error { return marker }
	reports := 0
	r.be = retry.New(fault, time.Second, func(string, error, time.Duration) { reports++ }, nil)
	if r.PrepareSnapshot(context.Background(), id, size) != r || reports != 0 || fault.loads != 1 {
		t.Fatalf("reports=%d loads=%d", reports, fault.loads)
	}
}

func TestPreparedSnapshotCancellationReleasesCiphertext(t *testing.T) {
	r, _, id, size := snapshotPrepareFixture(t, 2, []byte(`{}`))
	p := r.PrepareSnapshot(context.Background(), id, size).(*preparedSnapshot)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.LoadUnpacked(ctx, restic.SnapshotFile, id)
	if !errors.Is(err, context.Canceled) || p.raw != nil {
		t.Fatalf("err=%v retained=%d", err, len(p.raw))
	}
}

func TestPreparedSnapshotLeavesCachePopulationToOrdinaryLoad(t *testing.T) {
	r, be, id, size := snapshotPrepareFixture(t, 2, []byte(`{}`))
	fault := &snapshotPrepareBackend{Backend: be, connections: 2}
	r.be = retry.New(fault, time.Second, nil, nil)
	c, err := cache.New(r.Config().ID, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r.UseCache(c, t.Logf)
	h := backend.Handle{Type: backend.SnapshotFile, Name: id.String()}
	p := r.PrepareSnapshot(t.Context(), id, size)
	if p == r || fault.loads != 1 || c.Has(h) {
		t.Fatal("cold preparation absent or populated cache early")
	}
	if _, err := p.LoadUnpacked(t.Context(), restic.SnapshotFile, id); err != nil {
		t.Fatal(err)
	}
	if !c.Has(h) || fault.loads != 1 {
		t.Fatal("replay failed ordinary cache population")
	}
	if r.PrepareSnapshot(t.Context(), id, size) != r || fault.loads != 1 {
		t.Fatal("warm cache caused extra request")
	}
}

func TestPreparedSnapshotMalformedPayloadMatchesOrdinary(t *testing.T) {
	for _, mode := range []string{"short-ciphertext", "unsupported-encoding", "malformed-zstd", "malformed-json"} {
		t.Run(mode, func(t *testing.T) {
			r, be, _, _ := snapshotPrepareFixture(t, 2, []byte(`{}`))
			payload := []byte{4}
			if mode == "malformed-zstd" {
				payload = []byte{2, 0xde, 0xad}
			}
			if mode == "malformed-json" {
				payload = []byte(`{bad json`)
			}
			nonce := crypto.NewRandomNonce()
			raw := r.key.Seal(append([]byte(nil), nonce...), nonce, payload, nil)
			if mode == "short-ciphertext" {
				raw = []byte{1}
			}
			id := restic.Hash(raw)
			h := backend.Handle{Type: backend.SnapshotFile, Name: id.String()}
			if err := be.Save(context.Background(), h, backend.NewByteReader(raw, be.Hasher())); err != nil {
				t.Fatal(err)
			}
			p := r.PrepareSnapshot(context.Background(), id, int64(len(raw)))
			_, got := data.LoadSnapshot(context.Background(), p, id)
			_, want := data.LoadSnapshot(context.Background(), r, id)
			if got == nil || want == nil || got.Error() != want.Error() {
				t.Fatalf("got=%v want=%v", got, want)
			}
		})
	}
}
