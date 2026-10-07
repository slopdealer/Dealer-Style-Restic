package repository

import (
	"bytes"
	"io"
	"runtime"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

type b17TrackedCompressedReader struct{ input *bytes.Reader }

func (r *b17TrackedCompressedReader) Read(dst []byte) (int, error) { return r.input.Read(dst) }

func b17TrackReader(t *testing.T, dec *zstd.Decoder) <-chan struct{} {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatal(err)
	}
	payload := enc.EncodeAll(bytes.Repeat([]byte("reader ownership"), 65536), nil)
	enc.Close()
	reader := &b17TrackedCompressedReader{input: bytes.NewReader(payload)}
	released := make(chan struct{})
	runtime.SetFinalizer(reader, func(*b17TrackedCompressedReader) { close(released) })
	if err := dec.Reset(reader); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, dec); err != nil {
		t.Fatal(err)
	}
	return released
}

// This is a lifetime diagnostic, deliberately outside all timing measurements.
// Keep the decoder live across forced collections to distinguish reader EOF
// from release of the decoder's reference to that reader.
func TestB17SynchronousDecoderReaderLifetime(t *testing.T) {
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	released := b17TrackReader(t, dec)
	for range 3 {
		runtime.GC()
		select {
		case <-released:
			t.Fatal("reader released before Reset(nil)")
		default:
		}
	}
	if err := dec.Reset(nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	done := false
	for time.Now().Before(deadline) && !done {
		runtime.GC()
		select {
		case <-released:
			done = true
		case <-time.After(10 * time.Millisecond):
		}
	}
	runtime.KeepAlive(dec)
	if !done {
		t.Fatal("reader reference did not become collectible after Reset(nil)")
	}
	t.Log("EOF retained reader through three GCs; Reset(nil) released it while decoder remained live")
}
