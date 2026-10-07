package repository

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend/rest"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/restic"
)

type b23RoundTripper func(*http.Request) (*http.Response, error)

func (f b23RoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type b23TerminalBody struct {
	*bytes.Reader
	readErr, closeErr error
	moved, closes     *int
}

func (b *b23TerminalBody) Read(p []byte) (int, error) {
	n, err := b.Reader.Read(p)
	*b.moved += n
	if err == io.EOF && b.readErr != nil {
		return n, b.readErr
	}
	return n, err
}
func (b *b23TerminalBody) Close() error { *b.closes++; return b.closeErr }

// Use REST.Load itself so its extra EOF read and Close are both exercised,
// followed by real transport retry and one explicit connection token.
func TestB23RESTTerminalReadAndClose(t *testing.T) {
	retry.TestFastRetries(t)
	for _, failure := range []string{"read", "close"} {
		t.Run(failure, func(t *testing.T) {
			key := testKey(t)
			blobs, data := buildPackfileWithoutHeader([]int{350, 350, 350}, &key, false)
			dec, err := zstd.NewReader(nil)
			if err != nil {
				t.Fatal(err)
			}
			defer dec.Close()
			attempts, moved, closes, callbacks := 0, 0, 0, 0
			var ranges [][2]int64
			rt := b23RoundTripper(func(req *http.Request) (*http.Response, error) {
				attempts++
				var first, last int64
				if _, err := fmt.Sscanf(req.Header.Get("Range"), "bytes=%d-%d", &first, &last); err != nil {
					return nil, err
				}
				if first < 0 || last < first || last >= int64(len(data)) {
					return nil, fmt.Errorf("bad range %d-%d", first, last)
				}
				ranges = append(ranges, [2]int64{first, last - first + 1})
				body := &b23TerminalBody{Reader: bytes.NewReader(bytes.Clone(data[first : last+1])), moved: &moved, closes: &closes}
				if attempts == 1 {
					if failure == "read" {
						body.readErr = errors.New("terminal EOF frame failed")
					} else {
						body.closeErr = errors.New("terminal Close failed")
					}
				}
				return &http.Response{StatusCode: http.StatusPartialContent, Status: "206 Partial Content", Header: make(http.Header), Body: body, ContentLength: last - first + 1, Request: req}, nil
			})
			u, err := url.Parse("http://rest.invalid/repository/")
			if err != nil {
				t.Fatal(err)
			}
			be, err := rest.Open(context.Background(), rest.Config{URL: u, Connections: 1}, rt, nil)
			if err != nil {
				t.Fatal(err)
			}
			rb := retry.New(sema.NewBackend(be), time.Second, nil, nil)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = streamPackWithPolicy(sema.WithAdditionalLoadSlot(ctx), rb.Load, nil, dec, &key, restic.Hash(data), blobs, func(h restic.BlobHandle, p []byte, e error) error {
				if e != nil {
					return e
				}
				if callbacks >= len(blobs) || h != blobs[callbacks].BlobHandle || restic.Hash(p) != h.ID {
					return errors.New("replayed/reordered/corrupt callback")
				}
				callbacks++
				return nil
			}, 512, false)
			if err != nil || callbacks != len(blobs) || attempts < 2 || closes != attempts {
				t.Fatalf("REST settlement: %v callbacks=%d attempts=%d closes=%d", err, callbacks, attempts, closes)
			}
			t.Logf("REST failure=%s attempts=%d closes=%d consumed_bytes=%d pack_bytes=%d callbacks=%d ranges=%v", failure, attempts, closes, moved, len(data), callbacks, ranges)
		})
	}
}
