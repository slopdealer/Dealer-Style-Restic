package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/rest"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/restic"
)

type b23SocketBody struct {
	io.ReadCloser
	moved     *atomic.Int64
	afterRead func(int64)
	closes    *atomic.Int64
}

func (b b23SocketBody) Read(p []byte) (int, error) {
	n, e := b.ReadCloser.Read(p)
	total := b.moved.Add(int64(n))
	if b.afterRead != nil {
		b.afterRead(total)
	}
	return n, e
}

func (b b23SocketBody) Close() error {
	err := b.ReadCloser.Close()
	b.closes.Add(1)
	return err
}

type b23SocketTransport struct {
	base      *http.Transport
	moved     atomic.Int64
	closes    atomic.Int64
	afterRead func(int64)
}

func (s *b23SocketTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := s.base.RoundTrip(r)
	if err == nil {
		response.Body = b23SocketBody{response.Body, &s.moved, s.afterRead, &s.closes}
	}
	return response, err
}

func b23OpenSocketBackend(t *testing.T, server *httptest.Server) (*retry.Backend, *b23SocketTransport) {
	t.Helper()
	transport := &b23SocketTransport{base: &http.Transport{}}
	if server.EnableHTTP2 {
		transport.base = server.Client().Transport.(*http.Transport).Clone()
	}
	t.Cleanup(transport.base.CloseIdleConnections)
	u, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	be, err := rest.Open(context.Background(), rest.Config{URL: u, Connections: 1}, transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	return retry.New(sema.NewBackend(be), 100*time.Millisecond, nil, nil), transport
}

// The server advertises exactly the requested length, then really terminates
// its TCP response. No synthetic body error or retry implementation is used.
func TestB23SocketInterruptedResponse(t *testing.T) {
	retry.TestFastRetries(t)
	for _, cut := range []int{17, 382, 650, 1145} {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			key := testKey(t)
			blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			var mu sync.Mutex
			var ranges [][2]int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var first, last int64
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &first, &last); err != nil || first < 0 || last < first || last >= int64(len(data)) {
					http.Error(w, "invalid range", 416)
					return
				}
				mu.Lock()
				ranges = append(ranges, [2]int64{first, last - first + 1})
				attempt := len(ranges)
				mu.Unlock()
				w.Header().Set("Content-Length", fmt.Sprint(last-first+1))
				w.WriteHeader(http.StatusPartialContent)
				p := data[first : last+1]
				if attempt == 1 {
					_, _ = w.Write(p[:cut])
					w.(http.Flusher).Flush()
					panic(http.ErrAbortHandler)
				}
				_, _ = w.Write(p)
			}))
			defer server.Close()
			be, transport := b23OpenSocketBackend(t, server)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			callbacks := 0
			err = streamPackWithPolicy(sema.WithAdditionalLoadSlot(ctx), be.Load, nil, dec, &key, restic.Hash(data), blobs, func(h restic.BlobHandle, p []byte, e error) error {
				if e != nil {
					return e
				}
				if callbacks >= len(blobs) || h != blobs[callbacks].BlobHandle || restic.Hash(p) != h.ID {
					return errors.New("wrong/repeated callback")
				}
				callbacks++
				return nil
			}, 512, false)
			mu.Lock()
			gotRanges := append([][2]int64(nil), ranges...)
			mu.Unlock()
			if err != nil || callbacks != 3 || len(gotRanges) != 2 {
				t.Fatalf("err=%v callbacks=%d ranges=%v", err, callbacks, gotRanges)
			}
			wantRange := [2]int64{0, int64(len(data))}
			if gotRanges[0] != wantRange || gotRanges[1] != wantRange {
				t.Fatalf("retry ranges=%v", gotRanges)
			}
			wantMoved := int64(cut + len(data))
			if transport.moved.Load() != wantMoved {
				t.Fatalf("consumed=%d want=%d", transport.moved.Load(), wantMoved)
			}
			t.Logf("TCP cut=%d callbacks=%d consumed=%d pack=%d ranges=%v", cut, callbacks, transport.moved.Load(), len(data), gotRanges)
		})
	}
}

// Cancel after a real response has delivered a window but while the handler
// owns its borrowed plaintext. Cancellation must join the handler, retain its
// bytes until return, and prevent any later callback or transport replay.
func TestB23SocketCancellationJoinsCallback(t *testing.T) {
	retry.TestFastRetries(t)
	key := testKey(t)
	blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data[:blobs[0].Length])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	be, _ := b23OpenSocketBackend(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	entered, unblock := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(unblock) })
	callbacks := 0
	done := make(chan error, 1)
	go func() {
		done <- streamPackWithPolicy(sema.WithAdditionalLoadSlot(ctx), be.Load, nil, dec, &key, restic.Hash(data), blobs, func(h restic.BlobHandle, p []byte, e error) error {
			callbacks++
			if e != nil {
				return e
			}
			before := bytes.Clone(p)
			close(entered)
			<-unblock
			if !bytes.Equal(before, p) || restic.Hash(p) != h.ID {
				return errors.New("borrowed bytes changed")
			}
			return nil
		}, 512, false)
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("callback not reached")
	}
	cancel()
	select {
	case err := <-done:
		t.Fatalf("returned with borrowed callback active: %v", err)
	default:
	}
	release.Do(func() { close(unblock) })
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) || callbacks != 1 || requests.Load() != 1 {
			t.Fatalf("err=%v callbacks=%d requests=%d", err, callbacks, requests.Load())
		}
	case <-time.After(6 * time.Second):
		t.Fatal("canceled stream did not join")
	}
}

// Enable the same optional overflow interface as default local backends. The
// generic Backend field otherwise delegates all operations to the real mock.
type b23OverflowBackend struct{ backend.Backend }

func (b23OverflowBackend) AllowAdditionalLoadSlot() bool { return true }

func TestB23SocketRejectionLeavesBackendUsable(t *testing.T) {
	retry.TestFastRetries(t)
	for _, cancelParent := range []bool{false, true} {
		t.Run(fmt.Sprint(cancelParent), func(t *testing.T) {
			key := testKey(t)
			blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			var requests atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				var first, last int64
				if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-%d", &first, &last); err != nil || first < 0 || last < first || last >= int64(len(data)) {
					http.Error(w, "invalid range", 416)
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(last-first+1))
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(data[first : last+1])
			}))
			defer server.Close()
			be, _ := b23OpenSocketBackend(t, server)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			callbacks := 0
			rejection := errors.New("reject after second observable delivery")
			packID := restic.Hash(data)
			err = streamPackWithPolicy(sema.WithAdditionalLoadSlot(ctx), be.Load, nil, dec, &key, packID, blobs, func(h restic.BlobHandle, p []byte, e error) error {
				if e != nil {
					return e
				}
				if callbacks >= len(blobs) || h != blobs[callbacks].BlobHandle || restic.Hash(p) != h.ID {
					return errors.New("wrong callback")
				}
				callbacks++
				if callbacks == 2 {
					if cancelParent {
						cancel()
					}
					return rejection
				}
				return nil
			}, 512, false)
			if !errors.Is(err, rejection) || callbacks != 2 || requests.Load() != 1 {
				t.Fatalf("err=%v callbacks=%d requests=%d", err, callbacks, requests.Load())
			}
			// Use a fresh parent context and the SAME retry/semaphore/backend stack.
			// A scoped consumer cancellation must not open its file's circuit breaker.
			recoveryCtx, recoveryCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer recoveryCancel()
			err = be.Load(sema.WithAdditionalLoadSlot(recoveryCtx), backend.Handle{Type: backend.PackFile, Name: packID.String()}, len(data), 0, func(rd io.Reader) error {
				got, e := io.ReadAll(rd)
				if e == nil && !bytes.Equal(got, data) {
					return errors.New("subsequent response differs")
				}
				return e
			})
			if err != nil || requests.Load() != 2 {
				t.Fatalf("backend unavailable after rejection: %v requests=%d", err, requests.Load())
			}
		})
	}
}

// A complete HTTP/2 DATA payload is not a settled response: reset the stream
// before END_STREAM. REST.Load must observe its terminal read failure, retry,
// and deliver the held final window only after a settled replacement response.
func TestB23HTTP2ResetAfterPayload(t *testing.T) {
	retry.TestFastRetries(t)
	key := testKey(t)
	blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	var requests atomic.Int64
	var protocolError atomic.Bool
	consumed := make(chan struct{})
	var signal sync.Once
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ProtoMajor != 2 {
			protocolError.Store(true)
		}
		attempt := requests.Add(1)
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(data)
		w.(http.Flusher).Flush()
		if attempt == 1 {
			// Establish a post-consumer failure, rather than letting RST race
			// the DATA reads and discard payload before it reaches the consumer.
			select {
			case <-consumed:
				panic(http.ErrAbortHandler)
			case <-r.Context().Done():
				return
			}
		}
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	be, transport := b23OpenSocketBackend(t, server)
	transport.afterRead = func(total int64) {
		if total >= int64(len(data)) {
			signal.Do(func() { close(consumed) })
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	callbacks := 0
	err = streamPackWithPolicy(sema.WithAdditionalLoadSlot(ctx), be.Load, nil, dec, &key, restic.Hash(data), blobs, func(h restic.BlobHandle, p []byte, e error) error {
		if e != nil {
			return e
		}
		if callbacks >= len(blobs) || h != blobs[callbacks].BlobHandle || restic.Hash(p) != h.ID {
			return errors.New("wrong/replayed HTTP2 callback")
		}
		if callbacks == len(blobs)-1 && (requests.Load() != 2 || transport.closes.Load() != 2) {
			return errors.New("final window delivered before response settled")
		}
		callbacks++
		return nil
	}, 512, false)
	if err != nil || callbacks != 3 || requests.Load() != 2 || protocolError.Load() {
		t.Fatalf("HTTP2 settlement err=%v callbacks=%d requests=%d wrongProtocol=%v", err, callbacks, requests.Load(), protocolError.Load())
	}
	if transport.moved.Load() != 2*int64(len(data)) {
		t.Fatalf("HTTP2 consumed=%d want=%d", transport.moved.Load(), 2*len(data))
	}
	t.Logf("HTTP2 reset after full payload: consumed=%d pack=%d callbacks=%d requests=%d", transport.moved.Load(), len(data), callbacks, requests.Load())
}
