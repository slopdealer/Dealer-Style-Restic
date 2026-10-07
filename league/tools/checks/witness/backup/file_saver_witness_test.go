package archiver

// WITNESS (review of S019-merge, area backup): the whole candidate file-saving path
// (file_saver.go, file_saver_hash.go, ordinary_hash_pipeline.go, parent_cut_reader.go,
// parent_cut_long.go, parent_pair_hash.go, buffer.go) against stock restic's contract.
//
// For every file the test computes what stock would store: the chunk list of
// github.com/restic/chunker v0.5.0 BaseChunker fed in 512 KiB tiles, and SHA-256 of
// every chunk. It then runs the candidate's fileSaver with 4 workers and checks:
//   1. node.Content equals the stock chunk ID list, in order; node.Size equals the bytes read;
//   2. every blob handed to SaveBlobAsync with a precomputed ID hashes to that ID
//      (hashing moved off the reader goroutine, hash pipelines, speculative pair hashes);
//   3. no buffer handed to SaveBlobAsync changes before its callback has returned
//      (pooled / reused assembly buffers);
//   4. SaveKnownBlobAsync is only used for blobs that really are in the index;
//   5. every content ID is either in the index or was uploaded with exactly the chunk bytes.
// Files are saved with and without a parent node. Parent hints are: the same content, an
// edited older version (insert, delete, overwrite, append, truncate, prepend), a different
// file, a parent whose blob sizes in the index are wrong, and a parent with missing blobs.
// Readers return short reads, single bytes, or data together with io.EOF. The size reported
// by stat is sometimes wrong (file grew or shrank after stat).
//
// TestWitnessFileSaverCancelAndErrors: cancellation in the middle of saving, a failing
// reader and a failing uploader must not deadlock or panic.
//
// OS: any. Run from the candidate tree:
//   go test -run 'TestWitnessFileSaver' -count=1 -p 2 ./internal/archiver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	"golang.org/x/sync/errgroup"
)

type witnessChunk struct {
	id   restic.ID
	data []byte
}

func witnessStockChunks(content []byte, pol chunker.Pol) []witnessChunk {
	bc := chunker.NewBase(pol)
	var chunks []witnessChunk
	const tile = 512 * 1024
	start, pos := 0, 0
	for pos < len(content) {
		end := min(pos+tile, len(content))
		buf := content[pos:end]
		off := pos
		for len(buf) > 0 {
			n := bc.NextSplitPoint(buf)
			if n < 0 {
				break
			}
			off += n
			chunks = append(chunks, witnessChunk{restic.Hash(content[start:off]), content[start:off]})
			start = off
			buf = buf[n:]
		}
		pos = end
	}
	if start < len(content) {
		chunks = append(chunks, witnessChunk{restic.Hash(content[start:]), content[start:]})
	}
	return chunks
}

type witnessUploader struct {
	mu       sync.Mutex
	index    map[restic.ID]uint   // blobs "already in the repository": id -> size the index reports
	real     map[restic.ID]bool   // ids that really are stored (index may also hold lies about size only)
	uploaded map[restic.ID][]byte // blobs stored during this run
	problems []string
	sem      chan struct{}
	failAt   int // fail the n-th upload (1-based); 0 = never
	uploads  int
	known    int
	delay    time.Duration
}

func newWitnessUploader() *witnessUploader {
	return &witnessUploader{index: map[restic.ID]uint{}, real: map[restic.ID]bool{}, uploaded: map[restic.ID][]byte{}, sem: make(chan struct{}, 4)}
}

func (u *witnessUploader) problem(format string, args ...any) {
	u.mu.Lock()
	if len(u.problems) < 20 {
		u.problems = append(u.problems, fmt.Sprintf(format, args...))
	}
	u.mu.Unlock()
}

func (u *witnessUploader) SaveBlobAsync(ctx context.Context, t restic.BlobType, buf []byte, id restic.ID, _ bool, cb func(restic.ID, bool, int, error)) {
	snapshot := append([]byte(nil), buf...)
	u.sem <- struct{}{} // the repository applies backpressure in the caller, too
	go func() {
		defer func() { <-u.sem }()
		if ctx.Err() != nil {
			cb(restic.ID{}, false, 0, ctx.Err())
			return
		}
		if u.delay > 0 {
			time.Sleep(u.delay)
		}
		if !bytes.Equal(buf, snapshot) {
			u.problem("buffer of %d bytes was MODIFIED after SaveBlobAsync and before its callback", len(snapshot))
		}
		h := restic.Hash(snapshot)
		if !id.IsNull() && id != h {
			u.problem("precomputed ID %v does not match the %d bytes handed over (their hash is %v)", id.Str(), len(snapshot), h.Str())
		}
		u.mu.Lock()
		u.uploads++
		fail := u.failAt > 0 && u.uploads == u.failAt
		_, inIndex := u.real[h]
		_, already := u.uploaded[h]
		if !inIndex && !already && !fail {
			u.uploaded[h] = snapshot
		}
		u.mu.Unlock()
		if fail {
			cb(restic.ID{}, false, 0, errors.New("witness: injected upload failure"))
			return
		}
		out := h
		if !id.IsNull() {
			out = id // the repository trusts a precomputed ID
		}
		// Ownership of buf ends when the callback runs: stock releases its pooled buffer
		// inside the callback as well, and the repository does not touch the plaintext
		// after saveBlob returned. So buf is deliberately NOT checked after cb.
		cb(out, inIndex || already, len(snapshot), nil)
	}()
}

func (u *witnessUploader) LookupStoredBlobSize(bh restic.BlobHandle) (uint, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	n, ok := u.index[bh.ID]
	return n, ok
}

func (u *witnessUploader) SaveKnownBlobAsync(ctx context.Context, id restic.ID, cb func(restic.ID, bool, int, error)) {
	u.mu.Lock()
	if !u.real[id] {
		if len(u.problems) < 20 {
			u.problems = append(u.problems, fmt.Sprintf("blob %v reported as KNOWN but it is not stored in the repository", id.Str()))
		}
	}
	u.known++
	u.mu.Unlock()
	go func() {
		if ctx.Err() != nil {
			cb(restic.ID{}, false, 0, ctx.Err())
			return
		}
		cb(id, true, 0, nil)
	}()
}

type witnessFile struct {
	name     string
	content  []byte
	pos      int
	statSize uint64
	mode     int // 0 full reads, 1 random short reads, 2 data+EOF together, 3 tiny reads
	failAt   int // return an error once pos reaches failAt (>0)
	rnd      *rand.Rand
	closed   bool
}

var _ fs.File = &witnessFile{}

func (f *witnessFile) MakeReadable() error                 { return nil }
func (f *witnessFile) Readdirnames(int) ([]string, error)  { return nil, errors.New("not a dir") }
func (f *witnessFile) Stat() (*fs.ExtendedFileInfo, error) { return nil, errors.New("unused") }
func (f *witnessFile) Close() error                        { f.closed = true; return nil }
func (f *witnessFile) ToNode(bool, func(string, ...any)) (*data.Node, error) {
	return &data.Node{Name: f.name, Type: data.NodeTypeFile, Size: f.statSize}, nil
}

func (f *witnessFile) Read(p []byte) (int, error) {
	if f.failAt > 0 && f.pos >= f.failAt {
		return 0, errors.New("witness: injected read failure")
	}
	if f.pos >= len(f.content) {
		return 0, io.EOF
	}
	n := len(p)
	switch f.mode {
	case 1:
		n = 1 + f.rnd.Intn(len(p))
	case 3:
		n = 1 + f.rnd.Intn(7)
	}
	n = min(n, len(p), len(f.content)-f.pos)
	if f.failAt > 0 {
		n = min(n, max(1, f.failAt-f.pos))
	}
	copy(p, f.content[f.pos:f.pos+n])
	f.pos += n
	if f.mode == 2 && f.pos == len(f.content) {
		return n, io.EOF
	}
	return n, nil
}

func witnessContent(rnd *rand.Rand, size int) []byte {
	b := make([]byte, size)
	switch rnd.Intn(8) {
	case 0: // all zero: every chunk is the special zero chunk
	case 1: // no natural cut: maximum size chunks
		for i := range b {
			b[i] = 0xff
		}
	case 2: // random with zero runs
		_, _ = rnd.Read(b)
		for k := 0; k < 3 && size > 0; k++ {
			at := rnd.Intn(size)
			for i := at; i < min(size, at+rnd.Intn(2<<20)); i++ {
				b[i] = 0
			}
		}
	default:
		_, _ = rnd.Read(b)
	}
	return b
}

func witnessEdit(rnd *rand.Rand, old []byte) []byte {
	if len(old) == 0 {
		return witnessContent(rnd, rnd.Intn(100000))
	}
	at := rnd.Intn(len(old))
	extra := make([]byte, 1+rnd.Intn(300000))
	_, _ = rnd.Read(extra)
	switch rnd.Intn(7) {
	case 0: // insert
		return append(append(append([]byte(nil), old[:at]...), extra...), old[at:]...)
	case 1: // delete
		end := min(len(old), at+len(extra))
		return append(append([]byte(nil), old[:at]...), old[end:]...)
	case 2: // overwrite in place (same size, the common "same size, new mtime" case)
		out := append([]byte(nil), old...)
		copy(out[at:], extra)
		return out
	case 3: // append
		return append(append([]byte(nil), old...), extra...)
	case 4: // truncate
		return append([]byte(nil), old[:at]...)
	case 5: // prepend
		return append(extra, old...)
	default: // flip one byte
		out := append([]byte(nil), old...)
		out[at] ^= 0x55
		return out
	}
}

func witnessSize(rnd *rand.Rand) int {
	switch rnd.Intn(10) {
	case 0:
		return []int{0, 1, 2, 63, 64, 65, 64*1024 - 1, 64 * 1024, 64*1024 + 1, 512*1024 - 1, 512 * 1024, 512*1024 + 1, 1024 * 1024, 2 * 1024 * 1024, 2*1024*1024 + 1}[rnd.Intn(15)]
	case 1, 2, 3:
		return rnd.Intn(70000)
	case 4, 5, 6:
		return rnd.Intn(4 << 20)
	default:
		return 4<<20 + rnd.Intn(22<<20)
	}
}

func TestWitnessFileSaverEqualsStock(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261006))
	repo := repository.TestRepository(t)
	pol := repo.Config().ChunkerPolynomial
	factory := repo.ChunkerFactory()
	zeroID := restic.Hash(make([]byte, chunker.MinSize))
	if factory.ZeroChunk() != zeroID {
		t.Fatalf("ZeroChunk() is not the hash of %d zero bytes", chunker.MinSize)
	}

	type job struct {
		file   *witnessFile
		parent *data.Node
		want   []witnessChunk
		kind   string
		fn     futureNode
	}
	const rounds, perRound = 6, 28
	totalBytes, totalChunks, guidedFiles := 0, 0, 0
	for round := range rounds {
		up := newWitnessUploader()
		up.delay = time.Duration(rnd.Intn(3)) * 200 * time.Microsecond
		var jobs []*job
		for i := range perRound {
			content := witnessContent(rnd, witnessSize(rnd))
			j := &job{kind: "no-parent"}
			switch rnd.Intn(9) {
			case 0, 1: // unchanged content, read again (e.g. --ignore... or changed metadata only)
				j.kind = "parent-same"
				j.parent = &data.Node{}
				for _, c := range witnessStockChunks(content, pol) {
					j.parent.Content = append(j.parent.Content, c.id)
					up.index[c.id], up.real[c.id] = uint(len(c.data)), true
				}
			case 2, 3, 4: // edited version of the parent
				j.kind = "parent-edited"
				old := content
				j.parent = &data.Node{}
				for _, c := range witnessStockChunks(old, pol) {
					j.parent.Content = append(j.parent.Content, c.id)
					up.index[c.id], up.real[c.id] = uint(len(c.data)), true
				}
				content = witnessEdit(rnd, old)
			case 5: // parent is an unrelated file
				j.kind = "parent-unrelated"
				j.parent = &data.Node{}
				for _, c := range witnessStockChunks(witnessContent(rnd, len(content)), pol) {
					j.parent.Content = append(j.parent.Content, c.id)
					up.index[c.id], up.real[c.id] = uint(len(c.data)), true
				}
			case 6: // parent is right, but the index reports wrong sizes or lacks blobs
				j.kind = "parent-lying-index"
				j.parent = &data.Node{}
				for _, c := range witnessStockChunks(content, pol) {
					j.parent.Content = append(j.parent.Content, c.id)
					up.real[c.id] = true
					switch rnd.Intn(8) {
					case 0:
						up.index[c.id] = uint(len(c.data) + 1)
					case 1:
						up.index[c.id] = uint(max(1, len(c.data)-1))
					case 2:
						up.index[c.id] = uint(1 + rnd.Intn(chunker.MaxSize))
					case 3:
						up.index[c.id] = 0
					case 4:
						up.index[c.id] = chunker.MaxSize + 1
					default:
						up.index[c.id] = uint(len(c.data))
					}
				}
			}
			if j.parent != nil && len(j.parent.Content) >= 3 {
				guidedFiles++
			}
			f := &witnessFile{name: fmt.Sprintf("r%d-f%d", round, i), content: content, mode: rnd.Intn(4), rnd: rand.New(rand.NewSource(rnd.Int63())), statSize: uint64(len(content))}
			if f.mode == 3 && len(content) > 300000 {
				f.mode = 1 // tiny reads on big files only waste time
			}
			switch rnd.Intn(6) {
			case 0:
				f.statSize = uint64(rnd.Intn(len(content) + 1)) // file grew after stat
			case 1:
				f.statSize = uint64(len(content) + rnd.Intn(5<<20)) // file shrank after stat
			}
			j.file = f
			j.want = witnessStockChunks(content, pol)
			totalBytes += len(content)
			totalChunks += len(j.want)
			jobs = append(jobs, j)
		}

		wg, ctx := errgroup.WithContext(context.Background())
		s := newFileSaver(ctx, wg, up, factory, 4)
		s.NodeFromFileInfo = func(_, _ string, meta toNoder, ignore bool) (*data.Node, error) {
			return meta.ToNode(ignore, t.Logf)
		}
		for _, j := range jobs {
			j.fn = s.saveWithParent(ctx, j.file.name, j.file.name, j.file, j.parent, func() {}, func() {}, func(*data.Node, ItemStats) {})
		}
		for _, j := range jobs {
			res := j.fn.take(ctx)
			if res.err != nil {
				t.Fatalf("%s (%s, %d bytes): unexpected error %v", j.file.name, j.kind, len(j.file.content), res.err)
			}
			n := res.node
			if n.Size != uint64(len(j.file.content)) {
				t.Errorf("%s (%s): node.Size = %d, file has %d bytes", j.file.name, j.kind, n.Size, len(j.file.content))
			}
			if len(n.Content) != len(j.want) {
				t.Errorf("%s (%s, %d bytes): CHUNK COUNT differs: stock %d, candidate %d", j.file.name, j.kind, len(j.file.content), len(j.want), len(n.Content))
				continue
			}
			for k, c := range j.want {
				if n.Content[k] != c.id {
					t.Errorf("%s (%s, %d bytes): CHUNK %d differs: stock %v (%d bytes), candidate %v", j.file.name, j.kind, len(j.file.content), k, c.id.Str(), len(c.data), n.Content[k].Str())
					break
				}
				up.mu.Lock()
				stored, isUp := up.uploaded[c.id]
				isReal := up.real[c.id]
				up.mu.Unlock()
				if !isReal && !isUp {
					t.Errorf("%s (%s): chunk %d (%v) is referenced by the node but was NEVER STORED", j.file.name, j.kind, k, c.id.Str())
				}
				if isUp && !bytes.Equal(stored, c.data) {
					t.Errorf("%s (%s): chunk %d stored with wrong bytes", j.file.name, j.kind, k)
				}
			}
			if !j.file.closed {
				t.Errorf("%s: file was not closed", j.file.name)
			}
		}
		s.TriggerShutdown()
		if err := wg.Wait(); err != nil {
			t.Fatal(err)
		}
		for _, p := range up.problems {
			t.Errorf("round %d: %s", round, p)
		}
		t.Logf("round %d: %d files, uploads %d, known-without-upload %d", round, len(jobs), up.uploads, up.known)
	}
	t.Logf("total %d files, %d MiB, %d stock chunks compared; %d files had a parent with >= 3 chunks (guided reader)", rounds*perRound, totalBytes>>20, totalChunks, guidedFiles)
}

func TestWitnessFileSaverCancelAndErrors(t *testing.T) {
	rnd := rand.New(rand.NewSource(3))
	repo := repository.TestRepository(t)
	pol := repo.Config().ChunkerPolynomial
	factory := repo.ChunkerFactory()

	run := func(name string, prepare func(up *witnessUploader, files []*witnessFile, cancel context.CancelFunc)) {
		up := newWitnessUploader()
		up.delay = 300 * time.Microsecond
		var files []*witnessFile
		var parents []*data.Node
		for i := range 16 {
			content := witnessContent(rnd, 3<<20+rnd.Intn(6<<20))
			parent := &data.Node{}
			for _, c := range witnessStockChunks(content, pol) {
				parent.Content = append(parent.Content, c.id)
				if i%2 == 0 {
					up.index[c.id], up.real[c.id] = uint(len(c.data)), true
				}
			}
			if i%3 == 0 {
				parent = nil
			}
			files = append(files, &witnessFile{name: fmt.Sprintf("%s-%d", name, i), content: content, mode: 1, rnd: rand.New(rand.NewSource(int64(i))), statSize: uint64(len(content))})
			parents = append(parents, parent)
		}
		base, cancel := context.WithCancel(context.Background())
		defer cancel()
		wg, ctx := errgroup.WithContext(base)
		s := newFileSaver(ctx, wg, up, factory, 4)
		s.NodeFromFileInfo = func(_, _ string, meta toNoder, ignore bool) (*data.Node, error) {
			return meta.ToNode(ignore, t.Logf)
		}
		prepare(up, files, cancel)
		done := make(chan string, 1)
		go func() {
			var fns []futureNode
			for i, f := range files {
				fns = append(fns, s.saveWithParent(ctx, f.name, f.name, f, parents[i], func() {}, func() {}, func(*data.Node, ItemStats) {}))
			}
			ok, failed := 0, 0
			for _, fn := range fns {
				if res := fn.take(ctx); res.err != nil {
					failed++
				} else {
					ok++
				}
			}
			s.TriggerShutdown()
			err := wg.Wait()
			done <- fmt.Sprintf("%d files ok, %d failed, group error: %v", ok, failed, err)
		}()
		select {
		case msg := <-done:
			t.Logf("%s: %s", name, msg)
		case <-time.After(60 * time.Second):
			t.Errorf("%s: DEADLOCK: file saver did not finish within 60s", name)
		}
		for _, p := range up.problems {
			t.Errorf("%s: %s", name, p)
		}
	}

	run("cancel", func(_ *witnessUploader, _ []*witnessFile, cancel context.CancelFunc) {
		go func() { time.Sleep(40 * time.Millisecond); cancel() }()
	})
	run("read-error", func(_ *witnessUploader, files []*witnessFile, _ context.CancelFunc) {
		for i, f := range files {
			if i%2 == 1 {
				f.failAt = 1 + rnd.Intn(len(f.content))
			}
		}
	})
	run("upload-error", func(up *witnessUploader, _ []*witnessFile, _ context.CancelFunc) {
		up.failAt = 7
	})
}
