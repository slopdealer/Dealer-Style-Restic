package repository

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend/rest"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/restic"
)

// A real socket closes after a short body with the full Content-Length still
// advertised. Cuts cover the middle and boundary of processing windows.
func TestCoalescedSocketInterruption(t *testing.T) {
	retry.TestFastRetries(t)
	for _, cut := range []int{100, 382, 600, 764, 1020} {
		t.Run(fmt.Sprint(cut), func(t *testing.T) {
			key := testKey(t)
			blobs, body := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
			var mu sync.Mutex
			attempts, sent := 0, 0
			var ranges [][2]int
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				var first, last int
				if _, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &first, &last); err != nil || first < 0 || last < first || last >= len(body) {
					http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
					return
				}
				mu.Lock()
				attempts++
				attempt := attempts
				ranges = append(ranges, [2]int{first, last - first + 1})
				mu.Unlock()
				if attempt == 1 {
					conn, rw, err := w.(http.Hijacker).Hijack()
					if err != nil {
						return
					}
					defer conn.Close()
					fmt.Fprintf(rw, "HTTP/1.1 206 Partial Content\r\nContent-Length: %d\r\nConnection: close\r\n\r\n", last-first+1)
					n, _ := rw.Write(body[first : first+cut])
					_ = rw.Flush()
					mu.Lock()
					sent += n
					mu.Unlock()
					return
				}
				w.Header().Set("Content-Length", fmt.Sprint(last-first+1))
				w.WriteHeader(http.StatusPartialContent)
				n, _ := w.Write(body[first : last+1])
				mu.Lock()
				sent += n
				mu.Unlock()
			}))
			defer server.Close()
			transport := &http.Transport{}
			defer transport.CloseIdleConnections()
			u, err := url.Parse(server.URL + "/repository/")
			if err != nil {
				t.Fatal(err)
			}
			be, err := rest.Open(t.Context(), rest.Config{URL: u, Connections: 1}, transport, nil)
			if err != nil {
				t.Fatal(err)
			}
			rb := retry.New(sema.NewBackend(be), time.Second, nil, nil)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			delivered := 0
			err = streamPackWithPolicy(ctx, rb.Load, nil, dec, &key, restic.Hash(body), blobs,
				func(h restic.BlobHandle, p []byte, blobErr error) error {
					if blobErr != nil {
						return blobErr
					}
					if delivered >= len(blobs) || h != blobs[delivered].BlobHandle || restic.Hash(p) != h.ID {
						return fmt.Errorf("duplicate, reordered or corrupt callback %d", delivered)
					}
					delivered++
					return nil
				}, 512, false)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			wantBytes := cut + len(body)
			if delivered != len(blobs) || attempts != 2 || sent != wantBytes {
				t.Fatalf("callbacks=%d attempts=%d bytes=%d, want %d", delivered, attempts, sent, wantBytes)
			}
			for _, span := range ranges {
				if span != [2]int{0, len(body)} {
					t.Fatalf("unexpected retry range %v", span)
				}
			}
			t.Logf("cut=%d requests=%d sent=%d pack=%d callbacks=%d ranges=%v", cut, attempts, sent, len(body), delivered, ranges)
		})
	}
}

// Parse actual HTTP chunk framing without requiring a listening socket. The
// first response has every data byte but lacks the terminating zero chunk.
// The response metadata supplies the requested length, as required by REST;
// an HTTP/1.1 response with unknown length is rejected before consumption.
func TestCoalescedChunkedTerminal(t *testing.T) {
	retry.TestFastRetries(t)
	key := testKey(t)
	blobs, body := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
	attempts, delivered := 0, 0
	rt := b23RoundTripper(func(req *http.Request) (*http.Response, error) {
		attempts++
		var wire bytes.Buffer
		fmt.Fprintf(&wire, "HTTP/1.1 206 Partial Content\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n", len(body))
		wire.Write(body)
		wire.WriteString("\r\n")
		if attempts > 1 {
			wire.WriteString("0\r\n\r\n")
		}
		resp, err := http.ReadResponse(bufio.NewReader(&wire), req)
		if err == nil {
			resp.ContentLength = int64(len(body))
		}
		return resp, err
	})
	u, _ := url.Parse("http://rest.invalid/repository/")
	be, err := rest.Open(t.Context(), rest.Config{URL: u, Connections: 1}, rt, nil)
	if err != nil {
		t.Fatal(err)
	}
	rb := retry.New(sema.NewBackend(be), time.Second, nil, nil)
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	err = streamPackWithPolicy(t.Context(), rb.Load, nil, dec, &key, restic.Hash(body), blobs,
		func(h restic.BlobHandle, p []byte, blobErr error) error {
			if blobErr != nil {
				return blobErr
			}
			if delivered >= len(blobs) || h != blobs[delivered].BlobHandle || restic.Hash(p) != h.ID {
				return fmt.Errorf("invalid callback %d", delivered)
			}
			if delivered == len(blobs)-1 && attempts != 2 {
				return fmt.Errorf("final callback before response settlement")
			}
			delivered++
			return nil
		}, 512, false)
	if err != nil || attempts != 2 || delivered != len(blobs) {
		t.Fatalf("error=%v attempts=%d callbacks=%d", err, attempts, delivered)
	}
}
