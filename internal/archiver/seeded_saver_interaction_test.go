package archiver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

type b06PendingUpload struct {
	data  []byte
	id    restic.ID
	size  int
	known bool
	cb    func(restic.ID, bool, int, error)
}

type b06DelayedUploader struct {
	known   map[restic.ID]int
	pending []b06PendingUpload
}

func (u *b06DelayedUploader) LookupStoredBlobSize(bh restic.BlobHandle) (uint, bool) {
	n, ok := u.known[bh.ID]
	return uint(n), ok
}
func (u *b06DelayedUploader) SaveKnownBlobAsync(_ context.Context, id restic.ID, cb func(restic.ID, bool, int, error)) {
	u.pending = append(u.pending, b06PendingUpload{id: id, size: u.known[id], known: true, cb: cb})
}
func (u *b06DelayedUploader) SaveBlobAsync(_ context.Context, _ restic.BlobType, b []byte, id restic.ID, _ bool, cb func(restic.ID, bool, int, error)) {
	u.pending = append(u.pending, b06PendingUpload{data: b, id: id, size: len(b), cb: cb})
}

type b06SaverFile struct {
	fs.File
	r      io.Reader
	closed bool
}

func (f *b06SaverFile) Read(p []byte) (int, error) { return f.r.Read(p) }
func (f *b06SaverFile) Close() error               { f.closed = true; return nil }

type b06ChangingReader struct {
	r      io.Reader
	change func()
}

func (r *b06ChangingReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if r.change != nil {
		r.change()
		r.change = nil
	}
	return n, err
}

func b06Payload(n int, seed uint64) []byte {
	b := make([]byte, n)
	for i := range b {
		seed ^= seed << 13
		seed ^= seed >> 7
		seed ^= seed << 17
		b[i] = byte(seed)
	}
	return b
}

// The oracle uses the dependency directly, not the optimized Reset wrapper.
func b06ExpectedChunks(payload []byte) []b06PendingUpload {
	const polynomial = chunker.Pol(0x3DA3358B4DC173)
	c := chunker.NewBase(polynomial)
	var chunks []b06PendingUpload
	start := 0
	for offset := 0; offset < len(payload); {
		end := min(offset+13001, len(payload))
		n := c.NextSplitPoint(payload[offset:end])
		if n == -1 {
			offset = end
			continue
		}
		offset += n
		b := payload[start:offset]
		chunks = append(chunks, b06PendingUpload{data: b, id: restic.Hash(b), size: len(b)})
		start = offset
	}
	if start < len(payload) {
		b := payload[start:]
		chunks = append(chunks, b06PendingUpload{data: b, id: restic.Hash(b), size: len(b)})
	}
	return chunks
}

func b06SeededSaverMutation(t testing.TB) {
	t.Helper()
	factory := repository.TestRepository(t).ChunkerFactory()
	worker := factory.NewChunker()
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
	for _, grow := range []bool{false, true} {
		original := b06Payload(2*1024*1024+17, 73)
		path := t.TempDir() + "/changing-file"
		if err := os.WriteFile(path, original, 0600); err != nil {
			t.Fatal(err)
		}
		rd, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := rd.Stat()
		if err != nil {
			t.Fatal(err)
		}
		expected := original[:chunkReadBufSize]
		change := func() {
			if err := os.Truncate(path, chunkReadBufSize); err != nil {
				t.Fatal(err)
			}
		}
		if grow {
			tail := b06Payload(9*1024*1024+31, 97)
			expected = append(bytes.Clone(original), tail...)
			change = func() {
				out, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0600)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := out.Write(tail); err != nil {
					t.Fatal(err)
				}
				if err := out.Close(); err != nil {
					t.Fatal(err)
				}
			}
		}
		want := b06ExpectedChunks(expected)
		u := &b06DelayedUploader{known: map[restic.ID]int{want[0].id: want[0].size}}
		s := newFileSaver(t.Context(), nil, u, factory, 0)
		var hint uint64 = uint64(info.Size())
		s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
			return &data.Node{Type: data.NodeTypeFile, Size: hint}, nil
		}
		var result [2]futureNodeResult
		var complete, reading [2]int
		file := &b06SaverFile{r: &b06ChangingReader{r: rd, change: change}}
		s.saveFile(t.Context(), worker, state, "/first", path, file, func() {}, func() { reading[0]++ }, func(r futureNodeResult) { complete[0]++; result[0] = r })
		if err := rd.Close(); err != nil {
			t.Fatal(err)
		}
		if !file.closed || reading[0] != 1 || complete[0] != 0 || len(u.pending) != len(want) {
			t.Fatal("changed file did not finish reading independently of delayed uploads")
		}
		// Keep every first-file callback outstanding while resetting the same
		// worker and reusing its assembly storage for another file.
		next := b06Payload(3*1024*1024+41, 103)
		wantNext := b06ExpectedChunks(next)
		hint = 1
		s.saveFile(t.Context(), worker, state, "/second", "second", &b06SaverFile{r: bytes.NewReader(next)}, func() {}, func() { reading[1]++ }, func(r futureNodeResult) { complete[1]++; result[1] = r })
		all := append(append([]b06PendingUpload(nil), want...), wantNext...)
		if len(u.pending) != len(all) || reading[1] != 1 || complete != [2]int{} {
			t.Fatal("reset changed chunk count or callback independence")
		}
		for i, p := range u.pending {
			if p.id != all[i].id || p.size != all[i].size || (!p.known && !bytes.Equal(p.data, all[i].data)) {
				t.Fatalf("borrowed upload or chunk boundary changed at %d", i)
			}
		}
		for i := len(u.pending) - 1; i >= 0; i-- {
			p := u.pending[i]
			p.cb(p.id, p.known, p.size, nil)
		}
		for i, chunks := range [][]b06PendingUpload{want, wantNext} {
			ids := make([]restic.ID, len(chunks))
			var n uint64
			for j, p := range chunks {
				ids[j] = p.id
				n += uint64(p.size)
			}
			if complete[i] != 1 || result[i].err != nil || result[i].node.Size != n || !slices.Equal(result[i].node.Content, ids) {
				t.Fatalf("reverse settlement changed file %d: completions=%v result=%+v", i, complete, result[i])
			}
		}
	}
	t.Log("B06 seeded saver: append/truncate, two files per worker, known/new chunks, stale size hints and reverse callbacks")
}

type b06FailingReader struct {
	first bool
	err   error
}

func (r *b06FailingReader) Read(p []byte) (int, error) {
	if !r.first {
		r.first = true
		n := min(len(p), 600013)
		clear(p[:n])
		return n, nil
	}
	return 0, r.err
}

func b06SeededSaverFailureReset(t testing.TB) {
	t.Helper()
	factory := repository.TestRepository(t).ChunkerFactory()
	worker := factory.NewChunker()
	state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
	for _, canceled := range []bool{false, true} {
		u := &b06DelayedUploader{known: map[restic.ID]int{}}
		s := newFileSaver(t.Context(), nil, u, factory, 0)
		s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
			return &data.Node{Type: data.NodeTypeFile, Size: 1}, nil
		}
		failure := errors.New("read failed after a prefix")
		ctx, cancel := context.WithCancel(t.Context())
		if canceled {
			cancel()
		}
		f := &b06SaverFile{r: &b06FailingReader{err: failure}}
		if canceled {
			f.r = bytes.NewReader(b06Payload(2*1024*1024, 111))
		}
		completed, read := 0, 0
		var bad futureNodeResult
		s.saveFile(ctx, worker, state, "/bad", "bad", f, func() {}, func() { read++ }, func(r futureNodeResult) { bad = r; completed++ })
		wantErr := failure
		if canceled {
			wantErr = context.Canceled
		}
		if completed != 1 || read != 0 || !f.closed || bad.node != nil || !errors.Is(bad.err, wantErr) {
			t.Fatalf("wrong failure settlement: %+v", bad)
		}
		cancel()
		for _, p := range u.pending {
			p.cb(p.id, false, p.size, nil)
		}
		if completed != 1 {
			t.Fatal("late callback completed failed file again")
		}
		u.pending = nil
		payload := b06Payload(2*1024*1024+17, 119)
		want := b06ExpectedChunks(payload)
		var good futureNodeResult
		completed = 0
		s.saveFile(t.Context(), worker, state, "/good", "good", &b06SaverFile{r: bytes.NewReader(payload)}, func() {}, func() {}, func(r futureNodeResult) { good = r; completed++ })
		if len(u.pending) != len(want) {
			t.Fatal("incomplete previous file changed next split count")
		}
		for i := len(u.pending) - 1; i >= 0; i-- {
			p := u.pending[i]
			if p.id != want[i].id || !bytes.Equal(p.data, want[i].data) {
				t.Fatal("incomplete previous file changed next chunk")
			}
			p.cb(p.id, false, p.size, nil)
		}
		if completed != 1 || good.err != nil || good.node.Size != uint64(len(payload)) {
			t.Fatalf("next job failed: %+v", good)
		}
	}
	t.Log("B06 seeded saver: read failure/cancellation then a good file on the same worker")
}

func TestB06SeededSaverMutation(t *testing.T)     { b06SeededSaverMutation(t) }
func TestB06SeededSaverFailureReset(t *testing.T) { b06SeededSaverFailureReset(t) }
