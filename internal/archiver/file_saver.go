package archiver

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/fs"
	"github.com/restic/restic/internal/restic"
	"golang.org/x/sync/errgroup"
)

const chunkReadBufSize = 512 * 1024 // matches chunker internal read buffer size

type knownBlobSaverAsync interface {
	LookupStoredBlobSize(bh restic.BlobHandle) (uint, bool)
	SaveKnownBlobAsync(ctx context.Context, id restic.ID, cb func(newID restic.ID, known bool, sizeInRepo int, err error))
}

// fileSaver concurrently saves incoming files to the repo.
type fileSaver struct {
	saveFilePool  *bufferPool
	smallFilePool *bufferPool
	uploader      restic.BlobSaverAsync
	knownSaver    knownBlobSaverAsync
	hashBuffers   chan []byte
	pairHashes    *parentPairHashLane

	chunkerFactory restic.ChunkerFactory

	ch chan<- saveFileJob

	CompleteBlob func(bytes uint64)

	NodeFromFileInfo func(snPath, filename string, meta toNoder, ignoreXattrListError bool) (*data.Node, error)
}

// newFileSaver returns a new file saver. A worker pool with fileWorkers is
// started, it is stopped when ctx is cancelled.
func newFileSaver(ctx context.Context, wg *errgroup.Group, uploader restic.BlobSaverAsync, chunkerFactory restic.ChunkerFactory, fileWorkers uint) *fileSaver {
	ch := make(chan saveFileJob)
	debug.Log("new file saver with %v file workers", fileWorkers)

	s := &fileSaver{
		uploader:       uploader,
		saveFilePool:   newBufferPool(min(2*1024*1024, chunkerFactory.MaxChunkSize())),
		smallFilePool:  newBufferPool(min(64*1024, chunkerFactory.MaxChunkSize())),
		chunkerFactory: chunkerFactory,
		ch:             ch,

		CompleteBlob: func(uint64) {},
	}
	if knownSaver, ok := uploader.(knownBlobSaverAsync); ok {
		s.knownSaver = knownSaver
		if fileWorkers > 0 {
			s.pairHashes = &parentPairHashLane{active: make(chan struct{}, 1)}
			s.hashBuffers = make(chan []byte, 1)
			s.hashBuffers <- nil // allocate only on the first admitted chunk
		}
	}

	for range fileWorkers {
		wg.Go(func() error {
			s.worker(ctx, ch)
			return nil
		})
	}

	return s
}

func (s *fileSaver) TriggerShutdown() {
	close(s.ch)
}

// fileCompleteFunc is called when the file has been saved.
type fileCompleteFunc func(*data.Node, ItemStats)

// Save stores the file f and returns the data once it has been completed. The
// file is closed by Save. completeReading is only called if the file was read
// successfully. complete is always called. If completeReading is called, then
// this will always happen before calling complete. The callbacks must not block.
func (s *fileSaver) Save(ctx context.Context, snPath string, target string, file fs.File, start func(), completeReading func(), complete fileCompleteFunc) futureNode {
	return s.saveWithParent(ctx, snPath, target, file, nil, start, completeReading, complete)
}

func (s *fileSaver) saveWithParent(ctx context.Context, snPath string, target string, file fs.File, parent *data.Node, start func(), completeReading func(), complete fileCompleteFunc) futureNode {
	fn, ch := newFutureNode()
	job := saveFileJob{
		snPath: snPath,
		target: target,
		file:   file,
		parent: parent,
		ch:     ch,

		start:           start,
		completeReading: completeReading,
		complete:        complete,
	}

	select {
	case s.ch <- job:
	case <-ctx.Done():
		debug.Log("not sending job, context is cancelled: %v", ctx.Err())
		_ = file.Close()
		close(ch)
	}

	return fn
}

type saveFileJob struct {
	snPath string
	target string
	file   fs.File
	parent *data.Node
	ch     chan<- futureNodeResult

	start           func()
	completeReading func()
	complete        fileCompleteFunc
}

type fileChunkState struct {
	readBuf []byte
	bpos    uint
	bmax    uint
	closed  bool
	guided  *parentCutReader
	ctx     context.Context
	chunkID restic.ID
	hashed  bool
	ordinaryHashes *parentPairHashLane
}

func (s *fileChunkState) reset() {
	s.bpos = 0
	s.bmax = 0
	s.closed = false
}

// readNextChunk reads from rd and returns the next chunk of data. io.EOF is
// returned when all chunks have been read.
func (s *fileChunkState) readNextChunk(rd io.Reader, chnker restic.Chunker, data []byte) ([]byte, error) {
	s.hashed = false
	if s.guided != nil {
		return s.guided.read(s.ctx, s, rd, chnker, data)
	}
	if s.ordinaryHashes != nil {
		return s.readNextChunkHashed(rd, chnker, data)
	}
	return s.readNextChunkOrdinary(rd, chnker, data)
}

func (s *fileChunkState) readNextChunkOrdinary(rd io.Reader, chnker restic.Chunker, data []byte) ([]byte, error) {
	data = data[:0]
	for {
		if s.bpos >= s.bmax {
			n, err := io.ReadFull(rd, s.readBuf)

			if err == io.ErrUnexpectedEOF {
				err = nil
			}

			// io.EOF only happens when the end of the file has been reached.
			// If this is the case, we need to return the data we have read so far.
			if err == io.EOF && !s.closed {
				s.closed = true

				if len(data) > 0 {
					return data, nil
				}
			}

			if err != nil {
				return nil, err
			}

			s.bpos = 0
			s.bmax = uint(n)
		}

		split := chnker.NextSplitPoint(s.readBuf[s.bpos:s.bmax])
		if split == -1 {
			data = append(data, s.readBuf[s.bpos:s.bmax]...)
			s.bpos = s.bmax
		} else {
			data = append(data, s.readBuf[s.bpos:s.bpos+uint(split)]...)
			s.bpos += uint(split)
			return data, nil
		}
	}
}

// saveFile stores the file f in the repo, then closes it.
func (s *fileSaver) saveFile(ctx context.Context, chnker restic.Chunker, chunkState *fileChunkState, snPath string, target string, f fs.File, start func(), finishReading func(), finish func(res futureNodeResult)) {
	start()

	fnr := futureNodeResult{
		snPath: snPath,
		target: target,
	}
	var lock sync.Mutex
	var hashing sync.WaitGroup
	// Reading failures must also join jobs that still refer to this file's
	// completion state. Jobs submit uploads, but never wait for their callbacks.
	defer hashing.Wait()
	remaining := 0
	isCompleted := false

	completeBlob := func() {
		lock.Lock()
		defer lock.Unlock()

		remaining--
		if remaining == 0 && fnr.err == nil {
			if isCompleted {
				panic("completed twice")
			}
			for _, id := range fnr.node.Content {
				if id.IsNull() {
					panic("completed file with null ID")
				}
			}
			isCompleted = true
			finish(fnr)
		}
	}
	completeError := func(err error) {
		lock.Lock()
		defer lock.Unlock()

		if fnr.err == nil {
			if isCompleted {
				panic("completed twice")
			}
			isCompleted = true
			fnr.err = fmt.Errorf("failed to save %v: %w", target, err)
			fnr.node = nil
			fnr.stats = ItemStats{}
			finish(fnr)
		}
	}

	debug.Log("%v", snPath)

	node, err := s.NodeFromFileInfo(snPath, target, f, false)
	if err != nil {
		_ = f.Close()
		completeError(err)
		return
	}

	if node.Type != data.NodeTypeFile {
		_ = f.Close()
		completeError(errors.Errorf("node type %q is wrong", node.Type))
		return
	}

	chnker.Reset()
	chunkState.reset()

	// Size is only a reservation hint. The chunker and actual EOF still decide
	// content. Two stable sizes avoid discarding a buffer on every file-size
	// transition. Overflow belongs to this file and is released at its end.
	pool := s.saveFilePool
	if node.Size <= uint64(s.smallFilePool.defaultSize) {
		pool = s.smallFilePool
	}
	buf := pool.Get()
	defer buf.Release()
	if g := chunkState.guided; g != nil {
		defer g.reclaim(buf)
	}

	node.Content = []restic.ID{}
	node.Size = 0
	var idx int
	for {
		chunkData, err := chunkState.readNextChunk(f, chnker, buf.Data)
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = f.Close()
			completeError(err)
			return
		}

		// The assembly remains private to this file. Uploaders receive only the
		// detached payload below, so the next chunk may reuse this storage.
		buf.Data = chunkData
		chunkSize := len(chunkData)
		node.Size += uint64(chunkSize)

		// test if the context has been cancelled, return the error
		if ctx.Err() != nil {
			_ = f.Close()
			completeError(ctx.Err())
			return
		}

		// add a place to store the saveBlob result
		pos := idx

		lock.Lock()
		node.Content = append(node.Content, restic.ID{})
		lock.Unlock()

		completeChunk := func(newID restic.ID, known bool, sizeInRepo int, err error) {
			if err != nil {
				completeError(err)
				return
			}

			lock.Lock()
			if !known {
				fnr.stats.DataBlobs++
				fnr.stats.DataSize += uint64(chunkSize)
				fnr.stats.DataSizeInRepo += uint64(sizeInRepo)
			}
			node.Content[pos] = newID
			lock.Unlock()

			completeBlob()
		}
		zero := s.knownSaver != nil && chunkSize == chunker.MinSize && restic.ZeroPrefixLen(chunkData) == chunker.MinSize
		if chunkState.hashed {
			s.saveChunkWithID(ctx, chunkData, chunkState.chunkID, completeChunk)
		} else if !zero && s.tryHashChunk(ctx, chunkData, completeChunk, &hashing) {
			// The bounded worker owns a copy, including through unknown uploads.
		} else {
			s.saveChunkInline(ctx, chunkData, zero, completeChunk)
		}
		idx++

		// test if the context has been cancelled, return the error
		if ctx.Err() != nil {
			_ = f.Close()
			completeError(ctx.Err())
			return
		}

		s.CompleteBlob(uint64(chunkSize))
	}

	err = f.Close()
	if err != nil {
		completeError(err)
		return
	}

	// No job may start using completion state after the EOF settlement below.
	hashing.Wait()
	lock.Lock()
	fnr.node = node
	// require one additional completeFuture() call to ensure that the future only completes
	// after reaching the end of this method
	remaining += idx + 1
	lock.Unlock()
	finishReading()
	completeBlob()
}

func (s *fileSaver) worker(ctx context.Context, jobs <-chan saveFileJob) {
	chnker := s.chunkerFactory.NewChunker()
	chunkState := &fileChunkState{readBuf: make([]byte, chunkReadBufSize)}

	for {
		var job saveFileJob
		var ok bool
		select {
		case <-ctx.Done():
			return
		case job, ok = <-jobs:
			if !ok {
				return
			}
		}

		chunkState.guided = nil
		chunkState.ctx = ctx
		chunkState.ordinaryHashes = s.pairHashes
		if job.parent != nil && len(job.parent.Content) >= 3 && s.knownSaver != nil {
			chunkState.guided = &parentCutReader{parent: job.parent, lookup: s.knownSaver.LookupStoredBlobSize, factory: s.chunkerFactory, hashes: s.pairHashes}
		}
		s.saveFile(ctx, chnker, chunkState, job.snPath, job.target, job.file, job.start, func() {
			if job.completeReading != nil {
				job.completeReading()
			}
		}, func(res futureNodeResult) {
			if job.complete != nil {
				job.complete(res.node, res.stats)
			}
			job.ch <- res
			close(job.ch)
		})
	}
}
