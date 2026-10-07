package archiver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/restic"
)

// Fixed boundaries isolate storage ownership from content-defined splitting.
type bufferTestChunker struct{ size, remaining int }

func (c *bufferTestChunker) Reset() { c.remaining = c.size }
func (c *bufferTestChunker) NextSplitPoint(b []byte) int {
	if len(b) < c.remaining {
		c.remaining -= len(b)
		return -1
	}
	n := c.remaining
	c.remaining = c.size
	return n
}

type bufferTestFactory struct{ size int }

func (f bufferTestFactory) NewChunker() restic.Chunker {
	c := &bufferTestChunker{size: f.size}
	c.Reset()
	return c
}
func (f bufferTestFactory) MaxChunkSize() int    { return f.size }
func (f bufferTestFactory) ZeroChunk() restic.ID { return restic.ID{} }

type bufferTestFile struct {
	fs.File
	r      io.Reader
	closed bool
}

func (f *bufferTestFile) Read(b []byte) (int, error) { return f.r.Read(b) }
func (f *bufferTestFile) Close() error               { f.closed = true; return nil }

type bufferTestShortReader struct{ r io.Reader }

func (r bufferTestShortReader) Read(b []byte) (int, error) {
	if len(b) > 13001 {
		b = b[:13001]
	}
	return r.r.Read(b)
}

type bufferTestPending struct {
	buf []byte
	cb  func(restic.ID, bool, int, error)
}

// This saver deliberately lacks knownBlobSaverAsync and retains borrowed input
// until callbacks are explicitly settled, after all assembly buffers are reused.
type bufferTestUploader struct {
	pending []bufferTestPending
	onSave  func()
}

func (u *bufferTestUploader) SaveBlobAsync(_ context.Context, _ restic.BlobType, b []byte, _ restic.ID, _ bool, cb func(restic.ID, bool, int, error)) {
	u.pending = append(u.pending, bufferTestPending{b, cb})
	if u.onSave != nil {
		u.onSave()
	}
}

func TestFileSaverBufferOwnership(t *testing.T) {
	for _, size := range []int{1024, 3 * 1024 * 1024, 8 * 1024 * 1024} {
		t.Run(string(rune('a'+size/1024/1024)), func(t *testing.T) {
			factory := bufferTestFactory{size}
			u := &bufferTestUploader{}
			s := newFileSaver(t.Context(), nil, u, factory, 0)
			s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
				// Metadata is a stale hint: actual bytes must determine final size.
				return &data.Node{Type: data.NodeTypeFile, Size: 1}, nil
			}
			payload := make([]byte, 2*size+31)
			for i := range payload {
				payload[i] = byte(i*31 + i/size)
			}
			f := &bufferTestFile{r: bufferTestShortReader{bytes.NewReader(payload)}}
			state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
			reading, completed := false, 0
			var result futureNodeResult
			s.saveFile(t.Context(), factory.NewChunker(), state, "/file", "file", f, func() {}, func() { reading = true }, func(r futureNodeResult) { completed++; result = r })
			if !f.closed || !reading || completed != 0 {
				t.Fatalf("premature completion: closed=%v reading=%v completed=%d", f.closed, reading, completed)
			}
			if len(u.pending) != 3 {
				t.Fatalf("got %d chunks", len(u.pending))
			}
			for i, p := range u.pending {
				end := (i + 1) * size
				if end > len(payload) {
					end = len(payload)
				}
				if !bytes.Equal(p.buf, payload[i*size:end]) {
					t.Fatalf("payload %d overwritten by reuse", i)
				}
			}
			// Reverse callback order must preserve content order and finish once.
			for i := len(u.pending) - 1; i >= 0; i-- {
				p := u.pending[i]
				p.cb(restic.Hash(p.buf), false, len(p.buf), nil)
			}
			if completed != 1 || result.err != nil || result.node.Size != uint64(len(payload)) {
				t.Fatalf("bad result: completions=%d result=%+v", completed, result)
			}
			for i, p := range u.pending {
				if result.node.Content[i] != restic.Hash(p.buf) {
					t.Fatal("content order changed")
				}
			}
		})
	}
}

func TestFileSaverBufferCancellationAndFailure(t *testing.T) {
	for _, cancelDuringSave := range []bool{false, true} {
		ctx, cancel := context.WithCancel(t.Context())
		factory := bufferTestFactory{3 * 1024 * 1024}
		u := &bufferTestUploader{}
		if cancelDuringSave {
			u.onSave = cancel
		}
		s := newFileSaver(ctx, nil, u, factory, 0)
		s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
			return &data.Node{Type: data.NodeTypeFile}, nil
		}
		f := &bufferTestFile{r: bytes.NewReader(make([]byte, factory.size+31))}
		reading, completed := false, 0
		var result futureNodeResult
		s.saveFile(ctx, factory.NewChunker(), &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}, "/file", "file", f, func() {}, func() { reading = true }, func(r futureNodeResult) { completed++; result = r })
		failure := errors.New("delayed upload failure")
		for _, p := range u.pending {
			p.cb(restic.ID{}, false, 0, failure)
		}
		if !f.closed || completed != 1 || result.node != nil {
			t.Fatalf("bad failure settlement: %+v, completed=%d", result, completed)
		}
		if cancelDuringSave {
			if reading || !errors.Is(result.err, context.Canceled) {
				t.Fatalf("bad cancellation: %v", result.err)
			}
		} else if !reading || !errors.Is(result.err, failure) {
			t.Fatalf("bad delayed failure: %v", result.err)
		}
		cancel()
	}
}

func TestFileChunkBufferEOFTails(t *testing.T) {
	for _, n := range []int{0, 1, chunkReadBufSize - 1, chunkReadBufSize, 3*1024*1024 + 17} {
		payload := bytes.Repeat([]byte{37}, n)
		r := bufferTestShortReader{bytes.NewReader(payload)}
		c := bufferTestFactory{3 * 1024 * 1024}.NewChunker()
		state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
		var got []byte
		for {
			b, err := state.readNextChunk(r, c, make([]byte, 0, 1024))
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, b...)
		}
		if !bytes.Equal(got, payload) {
			t.Fatalf("EOF tail lost at length %d", n)
		}
	}
}

// Mutation happens after the first read and after NodeFromFileInfo's size hint.
// Both extension and truncation must still follow the bytes observed at EOF.
type bufferTestMutatingReader struct {
	r      io.Reader
	mutate func()
}

func (r *bufferTestMutatingReader) Read(b []byte) (int, error) {
	n, err := r.r.Read(b)
	if r.mutate != nil {
		r.mutate()
		r.mutate = nil
	}
	return n, err
}

func TestFileSaverSizeHintChangingFile(t *testing.T) {
	for _, grow := range []bool{false, true} {
		t.Run(map[bool]string{false: "truncate", true: "append"}[grow], func(t *testing.T) {
			original := bytes.Repeat([]byte{19}, 1024*1024)
			path := t.TempDir() + "/file"
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			rd, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer rd.Close()
			expected := original[:chunkReadBufSize]
			mutate := func() {
				if err := os.Truncate(path, chunkReadBufSize); err != nil {
					t.Fatal(err)
				}
			}
			if grow {
				tail := bytes.Repeat([]byte{83}, 3*1024*1024+31)
				expected = append(append([]byte{}, original...), tail...)
				mutate = func() {
					out, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
					if err != nil {
						t.Fatal(err)
					}
					if _, err = out.Write(tail); err != nil {
						t.Fatal(err)
					}
					if err = out.Close(); err != nil {
						t.Fatal(err)
					}
				}
			}
			u := &bufferTestUploader{}
			factory := bufferTestFactory{3 * 1024 * 1024}
			saver := newFileSaver(t.Context(), nil, u, factory, 0)
			saver.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
				info, err := rd.Stat()
				if err != nil {
					return nil, err
				}
				return &data.Node{Type: data.NodeTypeFile, Size: uint64(info.Size())}, nil
			}
			file := &bufferTestFile{r: &bufferTestMutatingReader{r: rd, mutate: mutate}}
			var result futureNodeResult
			completed, read := 0, false
			saver.saveFile(t.Context(), factory.NewChunker(), &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}, "/file", path, file, func() {}, func() { read = true }, func(r futureNodeResult) { result = r; completed++ })
			var got []byte
			for _, p := range u.pending {
				got = append(got, p.buf...)
			}
			if !bytes.Equal(got, expected) || !read || !file.closed || completed != 0 {
				t.Fatalf("mutation reading differs: got=%d want=%d read=%v closed=%v completed=%d", len(got), len(expected), read, file.closed, completed)
			}
			for i := len(u.pending) - 1; i >= 0; i-- {
				p := u.pending[i]
				p.cb(restic.Hash(p.buf), false, len(p.buf), nil)
			}
			if completed != 1 || result.err != nil || result.node.Size != uint64(len(expected)) {
				t.Fatalf("mutation completion: %+v", result)
			}
		})
	}
}

type bufferTestErrorReader struct {
	remaining int
	err       error
}

func (r *bufferTestErrorReader) Read(b []byte) (int, error) {
	if r.remaining == 0 {
		return 0, r.err
	}
	n := min(len(b), r.remaining)
	clear(b[:n])
	r.remaining -= n
	return n, nil
}
func TestFileSaverSizeHintReadError(t *testing.T) {
	for _, at := range []int{0, 17, chunkReadBufSize, chunkReadBufSize + 17} {
		failure := errors.New("scheduled read error")
		u := &bufferTestUploader{}
		factory := bufferTestFactory{chunkReadBufSize}
		s := newFileSaver(t.Context(), nil, u, factory, 0)
		s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
			return &data.Node{Type: data.NodeTypeFile, Size: 1}, nil
		}
		f := &bufferTestFile{r: &bufferTestErrorReader{remaining: at, err: failure}}
		completed, read := 0, false
		var result futureNodeResult
		s.saveFile(t.Context(), factory.NewChunker(), &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}, "/file", "file", f, func() {}, func() { read = true }, func(r futureNodeResult) { completed++; result = r })
		for _, p := range u.pending {
			p.cb(restic.Hash(p.buf), false, len(p.buf), nil)
		}
		if completed != 1 || read || !f.closed || !errors.Is(result.err, failure) || result.node != nil {
			t.Fatalf("read error at %d: %+v", at, result)
		}
	}
}

type bufferTestKnownUploader struct {
	bufferTestUploader
	known       restic.ID
	settleKnown func()
}

func (u *bufferTestKnownUploader) LookupStoredBlobSize(bh restic.BlobHandle) (uint, bool) {
	return 3, bh.ID == u.known
}
func (u *bufferTestKnownUploader) SaveKnownBlobAsync(_ context.Context, id restic.ID, cb func(restic.ID, bool, int, error)) {
	u.settleKnown = func() { cb(id, true, 3, nil) }
}
func TestFileSaverSizeHintKnownCallbacks(t *testing.T) {
	const size = 65536
	payload := append(bytes.Repeat([]byte{17}, size), bytes.Repeat([]byte{31}, size)...)
	payload = append(payload, 57)
	u := &bufferTestKnownUploader{known: restic.Hash(payload[:size])}
	factory := bufferTestFactory{size}
	s := newFileSaver(t.Context(), nil, u, factory, 0)
	s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile, Size: uint64(len(payload))}, nil
	}
	completed := 0
	var result futureNodeResult
	s.saveFile(t.Context(), factory.NewChunker(), &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}, "/file", "file", &bufferTestFile{r: bytes.NewReader(payload)}, func() {}, func() {}, func(r futureNodeResult) { completed++; result = r })
	if u.settleKnown == nil || len(u.pending) != 2 || completed != 0 {
		t.Fatal("wrong known path")
	}
	if !bytes.Equal(u.pending[0].buf, payload[size:2*size]) || !bytes.Equal(u.pending[1].buf, payload[2*size:]) {
		t.Fatal("detached payload overwritten")
	}
	for i := len(u.pending) - 1; i >= 0; i-- {
		p := u.pending[i]
		p.cb(restic.Hash(p.buf), false, len(p.buf), nil)
	}
	if completed != 0 {
		t.Fatal("completed before delayed known callback")
	}
	u.settleKnown()
	if completed != 1 || result.err != nil || result.node.Content[0] != u.known || result.node.Size != uint64(len(payload)) {
		t.Fatalf("known settlement: %+v", result)
	}
}

func TestFileSaverFileLocalOverflowReleased(t *testing.T) {
	factory := bufferTestFactory{8 * 1024 * 1024}
	u := &bufferTestUploader{}
	s := newFileSaver(t.Context(), nil, u, factory, 0)
	var hint uint64
	s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile, Size: hint}, nil
	}
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
	for _, n := range []int{1024, 8*1024*1024 + 31, 1024, 8*1024*1024 + 31, 1024} {
		hint = uint64(n)
		payload := bytes.Repeat([]byte{byte(n)}, n)
		start := len(u.pending)
		completed := 0
		s.saveFile(t.Context(), factory.NewChunker(), state, "/file", "file", &bufferTestFile{r: bytes.NewReader(payload)}, func() {}, func() {}, func(r futureNodeResult) {
			if r.err != nil || r.node.Size != uint64(n) {
				t.Fatalf("bad result: %+v", r)
			}
			completed++
		})
		if completed != 0 {
			t.Fatal("completed before callbacks")
		}
		if n > 2*1024*1024 && len(s.saveFilePool.pool) != 0 {
			t.Fatal("overflow retained across files")
		}
		if n < 64*1024 && len(s.smallFilePool.pool) != 1 {
			t.Fatal("small reservation not reused")
		}
		var got []byte
		for _, p := range u.pending[start:] {
			got = append(got, p.buf...)
		}
		if !bytes.Equal(got, payload) {
			t.Fatal("payload lost after assembly release")
		}
		for i := len(u.pending) - 1; i >= start; i-- {
			p := u.pending[i]
			p.cb(restic.Hash(p.buf), false, len(p.buf), nil)
		}
		if completed != 1 {
			t.Fatal("wrong completion count")
		}
	}
}
