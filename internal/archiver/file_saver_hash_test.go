package archiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

type b18HashUploader struct {
	mu           sync.Mutex
	known        map[restic.ID]int
	pending      []b06PendingUpload
	beforeLookup func()
	beforeKnown  func()
}

func (u *b18HashUploader) LookupStoredBlobSize(h restic.BlobHandle) (uint, bool) {
	if u.beforeLookup != nil {
		u.beforeLookup()
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	n, ok := u.known[h.ID]
	return uint(n), ok
}

func (u *b18HashUploader) SaveKnownBlobAsync(ctx context.Context, id restic.ID, cb chunkCompleteFunc) {
	if u.beforeKnown != nil {
		u.beforeKnown()
	}
	if ctx.Err() != nil {
		cb(restic.ID{}, false, 0, ctx.Err())
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pending = append(u.pending, b06PendingUpload{id: id, known: true, size: u.known[id], cb: cb})
}

func (u *b18HashUploader) SaveBlobAsync(ctx context.Context, _ restic.BlobType, b []byte, id restic.ID, _ bool, cb chunkCompleteFunc) {
	if ctx.Err() != nil {
		cb(restic.ID{}, false, 0, ctx.Err())
		return
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	u.pending = append(u.pending, b06PendingUpload{data: b, id: id, size: len(b), cb: cb})
}

func b18EnableHash(s *fileSaver) {
	s.hashBuffers = make(chan []byte, 1)
	s.hashBuffers <- nil
}

func TestB18HashCapacityAndOwnership(t *testing.T) {
	u := &b18HashUploader{known: make(map[restic.ID]int)}
	s := &fileSaver{knownSaver: u, uploader: u}
	b18EnableHash(s)
	var jobs sync.WaitGroup
	payload := b06Payload(asyncHashBufferSize/2+3, 901)
	want := bytes.Clone(payload)
	completed := 0
	cb := func(restic.ID, bool, int, error) { completed++ }
	if !s.tryHashChunk(t.Context(), payload, cb, &jobs) {
		t.Fatal("eligible chunk rejected")
	}
	clear(payload) // assembly may be immediately reused
	jobs.Wait()
	if len(u.pending) != 1 || !bytes.Equal(u.pending[0].data, want) || u.pending[0].id != restic.Hash(want) {
		t.Fatal("hash worker borrowed assembly or changed ID")
	}
	if cap(u.pending[0].data) != asyncHashBufferSize {
		t.Fatal("capacity bound changed")
	}
	if s.tryHashChunk(t.Context(), want, cb, &jobs) {
		t.Fatal("outstanding upload did not retain admission")
	}
	p := u.pending[0]
	p.cb(restic.ID{}, false, 0, errors.New("upload failed"))
	if completed != 1 || len(s.hashBuffers) != 1 {
		t.Fatal("failed callback did not release slot exactly once")
	}
	if s.tryHashChunk(t.Context(), make([]byte, asyncHashBufferSize+1), cb, &jobs) {
		t.Fatal("oversized chunk admitted")
	}
	if s.tryHashChunk(t.Context(), make([]byte, asyncHashMinSize-1), cb, &jobs) {
		t.Fatal("small chunk admitted")
	}
	u.known[restic.Hash(want)] = len(want)
	if !s.tryHashChunk(t.Context(), want, cb, &jobs) {
		t.Fatal("released slot not reusable")
	}
	jobs.Wait()
	if len(s.hashBuffers) != 1 || !u.pending[1].known || u.pending[1].data != nil {
		t.Fatal("known blob retained payload")
	}
	u.pending[1].cb(u.pending[1].id, true, 0, nil)
	if completed != 2 {
		t.Fatal("known callback changed completion count")
	}
}

type b18ShortReader struct {
	r        io.Reader
	division int
}

func (r *b18ShortReader) Read(p []byte) (int, error) { return r.r.Read(p[:min(len(p), r.division)]) }

func TestB18HashSaverEquivalence(t *testing.T) {
	base := b06Payload(7*1024*1024+173, 907)
	mutation := bytes.Clone(base)
	mutation[600013] ^= 7
	nearCut := bytes.Clone(base)
	nearCut[b06ExpectedChunks(base)[0].size-1] ^= 11
	for _, tc := range []struct {
		name  string
		bytes []byte
	}{
		{"unchanged", base}, {"mutation", mutation}, {"near-cut", nearCut},
		{"growth", append(bytes.Clone(base), b06Payload(1024*1024+31, 911)...)},
		{"truncation", base[:600013]}, {"empty", nil},
	} {
		for _, division := range []int{1, 13001, 524288, 600013} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, division), func(t *testing.T) {
				want := b06ExpectedChunks(tc.bytes)
				u := &b18HashUploader{known: make(map[restic.ID]int)}
				for i, p := range want {
					if i%2 == 0 {
						u.known[p.id] = p.size
					}
				}
				factory := repository.TestRepository(t).ChunkerFactory()
				s := newFileSaver(t.Context(), nil, u, factory, 0)
				b18EnableHash(s)
				s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
					return &data.Node{Type: data.NodeTypeFile, Size: 1}, nil
				}
				state := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}
				file := &b06SaverFile{r: &b18ShortReader{r: bytes.NewReader(tc.bytes), division: division}}
				var result futureNodeResult
				completed, reading := 0, 0
				s.saveFile(t.Context(), factory.NewChunker(), state, "/file", "file", file, func() {}, func() { reading++ }, func(r futureNodeResult) { result = r; completed++ })
				if reading != 1 || !file.closed || len(u.pending) != len(want) {
					t.Fatal("read or upload count changed")
				}
				byID := make(map[restic.ID]b06PendingUpload)
				for _, p := range want {
					byID[p.id] = p
				}
				for i := len(u.pending) - 1; i >= 0; i-- {
					p := u.pending[i]
					w, ok := byID[p.id]
					if !ok || p.size != w.size || (!p.known && !bytes.Equal(p.data, w.data)) {
						t.Fatal("payload or ID changed")
					}
					p.cb(p.id, p.known, p.size, nil)
				}
				ids := make([]restic.ID, len(want))
				for i, p := range want {
					ids[i] = p.id
				}
				if completed != 1 || result.err != nil || result.node.Size != uint64(len(tc.bytes)) || !slices.Equal(result.node.Content, ids) {
					t.Fatalf("ordered completion changed: %+v", result)
				}
			})
		}
	}
}

func TestB18CanceledHashReturnsAdmission(t *testing.T) {
	u := &b18HashUploader{known: make(map[restic.ID]int)}
	entered, resume := make(chan struct{}), make(chan struct{})
	u.beforeLookup = func() { close(entered); <-resume }
	s := &fileSaver{knownSaver: u, uploader: u}
	b18EnableHash(s)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var jobs sync.WaitGroup
	completed := 0
	if !s.tryHashChunk(ctx, b06Payload(700013, 929), func(_ restic.ID, _ bool, _ int, err error) {
		if !errors.Is(err, context.Canceled) {
			t.Error("canceled job did not preserve error")
		}
		completed++
	}, &jobs) {
		t.Fatal("eligible canceled job was not exercised")
	}
	<-entered
	cancel()
	close(resume)
	jobs.Wait()
	if completed != 1 || len(s.hashBuffers) != 1 {
		t.Fatal("canceled upload retained admission")
	}
}

func TestB18KnownSubmissionBackpressure(t *testing.T) {
	payload := b06Payload(700013, 941)
	id := restic.Hash(payload)
	entered, resume := make(chan struct{}), make(chan struct{})
	u := &b18HashUploader{known: map[restic.ID]int{id: len(payload)}}
	var first sync.Once
	u.beforeKnown = func() { first.Do(func() { close(entered); <-resume }) }
	s := &fileSaver{knownSaver: u, uploader: u}
	b18EnableHash(s)
	var jobs sync.WaitGroup
	if !s.tryHashChunk(t.Context(), payload, func(restic.ID, bool, int, error) {}, &jobs) {
		t.Fatal("eligible job rejected")
	}
	<-entered
	// Release the blocked worker even if the assertion fails, so failure itself
	// does not strand an owned slot or a test goroutine.
	accepted := s.tryHashChunk(t.Context(), payload, func(restic.ID, bool, int, error) {}, &jobs)
	close(resume)
	jobs.Wait()
	if accepted {
		t.Fatal("blocked known submission released admission early")
	}
	if len(s.hashBuffers) != 1 {
		t.Fatal("known submission retained admission after returning")
	}
}

func TestB18ZeroShortcutDoesNotAllocateHashSlot(t *testing.T) {
	factory := repository.TestRepository(t).ChunkerFactory()
	u := &b18HashUploader{known: map[restic.ID]int{factory.ZeroChunk(): 524288}}
	s := newFileSaver(t.Context(), nil, u, factory, 0)
	b18EnableHash(s)
	s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile}, nil
	}
	file := &b06SaverFile{r: bytes.NewReader(make([]byte, 3*524288))}
	completed := 0
	s.saveFile(t.Context(), factory.NewChunker(), &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}, "/zeros", "zeros", file, func() {}, func() {}, func(futureNodeResult) { completed++ })
	for _, p := range u.pending {
		if !p.known || p.data != nil {
			t.Fatal("zero shortcut changed")
		}
		p.cb(p.id, true, 0, nil)
	}
	if completed != 1 {
		t.Fatal("zero file did not complete once")
	}
	if buf := <-s.hashBuffers; buf != nil {
		t.Fatal("zero shortcut allocated off-reader capacity")
	}
}

type b18ErrorReader struct{ err error }

func (r b18ErrorReader) Read([]byte) (int, error) { return 0, r.err }

func TestB18ReadFailureWithOutstandingHash(t *testing.T) {
	u := &b18HashUploader{known: make(map[restic.ID]int)}
	factory := repository.TestRepository(t).ChunkerFactory()
	s := newFileSaver(t.Context(), nil, u, factory, 0)
	b18EnableHash(s)
	s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile}, nil
	}
	failure := errors.New("read failed after chunk")
	file := &b06SaverFile{r: io.MultiReader(bytes.NewReader(b06Payload(4*1024*1024, 937)), b18ErrorReader{failure})}
	completed, reading := 0, 0
	var result futureNodeResult
	s.saveFile(t.Context(), factory.NewChunker(), &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}, "/bad", "bad", file, func() {}, func() { reading++ }, func(r futureNodeResult) { result = r; completed++ })
	if len(u.pending) == 0 || len(s.hashBuffers) != 0 {
		t.Fatal("did not exercise outstanding admitted payload")
	}
	for _, p := range u.pending {
		p.cb(p.id, false, p.size, nil)
	}
	if completed != 1 || reading != 0 || !errors.Is(result.err, failure) || len(s.hashBuffers) != 1 {
		t.Fatal("late callbacks changed error completion or retained capacity")
	}
}

func TestB18HashCancellationAndReadError(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		u := &b18HashUploader{known: make(map[restic.ID]int)}
		factory := repository.TestRepository(t).ChunkerFactory()
		s := newFileSaver(t.Context(), nil, u, factory, 0)
		b18EnableHash(s)
		s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
			return &data.Node{Type: data.NodeTypeFile}, nil
		}
		ctx, cancel := context.WithCancel(t.Context())
		failure := errors.New("short read failed")
		var r io.Reader = &b06FailingReader{err: failure}
		if canceled {
			cancel()
			r = bytes.NewReader(b06Payload(2*1024*1024, 919))
			failure = context.Canceled
		}
		file := &b06SaverFile{r: r}
		var result futureNodeResult
		completed, reading := 0, 0
		s.saveFile(ctx, factory.NewChunker(), &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}, "/bad", "bad", file, func() {}, func() { reading++ }, func(r futureNodeResult) { result = r; completed++ })
		cancel()
		for _, p := range u.pending {
			p.cb(restic.ID{}, false, 0, context.Canceled)
		}
		if completed != 1 || reading != 0 || !file.closed || !errors.Is(result.err, failure) {
			t.Fatalf("failure settlement changed: %+v", result)
		}
		if len(s.hashBuffers) != 1 {
			t.Fatal("failure retained hash admission")
		}
	}
}

// Two files share one admission owner. The second must progress inline while
// the first is stalled in lookup, and neither may borrow the other's payload.
func TestB18SharedSlotAcrossFiles(t *testing.T) {
	factory := repository.TestRepository(t).ChunkerFactory()
	u := &b18HashUploader{known: make(map[restic.ID]int)}
	entered, resume := make(chan struct{}), make(chan struct{})
	var lookups atomic.Int32
	u.beforeLookup = func() {
		if lookups.Add(1) == 1 {
			close(entered)
			<-resume
		}
	}
	s := newFileSaver(t.Context(), nil, u, factory, 0)
	b18EnableHash(s)
	s.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile}, nil
	}
	payloads := [][]byte{b06Payload(700013, 953), b06Payload(710013, 967)}
	var results [2]futureNodeResult
	var completed [2]int
	var reading [2]int
	save := func(i int) {
		file := &b06SaverFile{r: bytes.NewReader(payloads[i])}
		s.saveFile(t.Context(), factory.NewChunker(), &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}, "/file", "file", file, func() {}, func() { reading[i]++ }, func(r futureNodeResult) { results[i] = r; completed[i]++ })
	}
	done := make(chan struct{})
	go func() { save(0); close(done) }()
	<-entered
	// No further admissions are possible until the first lookup and upload finish.
	save(1)
	close(resume)
	<-done
	if reading != [2]int{1, 1} || completed != [2]int{} || len(s.hashBuffers) != 0 {
		t.Fatal("shared admission changed reading/completion ownership")
	}
	var charged int
	for _, p := range u.pending {
		if cap(p.data) == asyncHashBufferSize {
			charged++
		}
		if p.id != restic.Hash(p.data) {
			t.Fatal("concurrent reader overwrote upload")
		}
	}
	if charged != 1 {
		t.Fatalf("want one charged slot, got %d", charged)
	}
	for i := len(u.pending) - 1; i >= 0; i-- {
		p := u.pending[i]
		p.cb(p.id, false, p.size, nil)
	}
	for i, payload := range payloads {
		want := b06ExpectedChunks(payload)
		ids := make([]restic.ID, len(want))
		for j, p := range want {
			ids[j] = p.id
		}
		if completed[i] != 1 || results[i].err != nil || results[i].node.Size != uint64(len(payload)) || !slices.Equal(results[i].node.Content, ids) {
			t.Fatalf("file %d did not settle ordinarily: %+v", i, results[i])
		}
	}
	if len(s.hashBuffers) != 1 {
		t.Fatal("shared slot not returned")
	}
}

// The callback's payload lifetime and the submitter's lifetime are distinct.
// Finishing the former must not admit unlimited blocked submitters.
type b18EarlyCallbackUploader struct {
	entered chan struct{}
	resume  chan struct{}
}

func (u *b18EarlyCallbackUploader) LookupStoredBlobSize(restic.BlobHandle) (uint, bool) {
	return 0, false
}
func (u *b18EarlyCallbackUploader) SaveKnownBlobAsync(context.Context, restic.ID, chunkCompleteFunc) {
	panic("unexpected known blob")
}
func (u *b18EarlyCallbackUploader) SaveBlobAsync(_ context.Context, _ restic.BlobType, b []byte, id restic.ID, _ bool, cb chunkCompleteFunc) {
	cb(id, false, len(b), nil)
	close(u.entered)
	<-u.resume
}
func TestB18UnknownSubmissionBackpressure(t *testing.T) {
	u := &b18EarlyCallbackUploader{entered: make(chan struct{}), resume: make(chan struct{})}
	s := &fileSaver{knownSaver: u, uploader: u}
	b18EnableHash(s)
	var jobs sync.WaitGroup
	var completed atomic.Int32
	payload := b06Payload(700013, 971)
	cb := func(restic.ID, bool, int, error) { completed.Add(1) }
	if !s.tryHashChunk(t.Context(), payload, cb, &jobs) {
		t.Fatal("first job rejected")
	}
	<-u.entered
	// Inspect admission directly so a failing variant cannot launch another
	// uploader that would close the same test channel twice.
	var premature bool
	select {
	case b := <-s.hashBuffers:
		premature = true
		s.hashBuffers <- b
	default:
	}
	close(u.resume)
	jobs.Wait()
	if premature {
		t.Fatal("callback released admission before submission returned")
	}
	if completed.Load() != 1 || len(s.hashBuffers) != 1 {
		t.Fatal("submission did not release exactly once")
	}
}

func TestB18UnknownCallbackRetainsAdmission(t *testing.T) {
	u := &b18HashUploader{known: make(map[restic.ID]int)}
	s := &fileSaver{knownSaver: u, uploader: u}
	b18EnableHash(s)
	var jobs sync.WaitGroup
	entered, resume, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	payload := b06Payload(700013, 977)
	if !s.tryHashChunk(t.Context(), payload, func(restic.ID, bool, int, error) { close(entered); <-resume }, &jobs) {
		t.Fatal("job rejected")
	}
	jobs.Wait()
	p := u.pending[0]
	go func() { p.cb(p.id, false, p.size, nil); close(done) }()
	<-entered
	var premature bool
	select {
	case b := <-s.hashBuffers:
		premature = true
		s.hashBuffers <- b
	default:
	}
	close(resume)
	<-done
	if premature {
		t.Fatal("admission released while completion callback still ran")
	}
	if len(s.hashBuffers) != 1 {
		t.Fatal("completion callback retained admission")
	}
}
