package archiver

import (
	"crypto/sha256"
	"hash"
	"io"

	"github.com/restic/restic/internal/restic"
)

// ordinaryHashStream shares admission with parent-pair hashing. It borrows
// append-only assembly, never a read tile or detached upload. At most one span
// is outstanding; growth joins it so retired assembly cannot remain retained.
type ordinaryHashStream struct {
	lane      *parentPairHashLane
	parts     chan []byte
	ack       chan struct{}
	done      chan struct{}
	pending   bool
	processed int
	id        restic.ID
}

func (l *parentPairHashLane) tryOrdinary() *ordinaryHashStream {
	if l == nil {
		return nil
	}
	select {
	case l.active <- struct{}{}:
	default:
		return nil
	}
	return newOrdinaryHashStream(l, sha256.New())
}

func newOrdinaryHashStream(l *parentPairHashLane, h hash.Hash) *ordinaryHashStream {
	j := &ordinaryHashStream{lane: l, parts: make(chan []byte), ack: make(chan struct{}, 1), done: make(chan struct{})}
	go func() {
		for b := range j.parts {
			_, _ = h.Write(b)
			j.ack <- struct{}{}
		}
		h.Sum(j.id[:0])
		close(j.done)
	}()
	return j
}

func (j *ordinaryHashStream) join() {
	if j != nil && j.pending {
		<-j.ack
		j.pending = false
	}
}

func (j *ordinaryHashStream) write(data []byte) {
	j.join()
	if j.processed < len(data) {
		j.parts <- data[j.processed:]
		j.processed = len(data)
		j.pending = true
	}
}

func (j *ordinaryHashStream) finish() restic.ID {
	j.join()
	close(j.parts)
	<-j.done
	<-j.lane.active
	return j.id
}

// Unguided discovery stays authoritative. A stream is admitted only after an
// oversized uncut prefix exists, leaving small chunks and the zero fast path
// unchanged. Reading and scanning the next tile overlap hashing that prefix.
func (s *fileChunkState) readNextChunkHashed(rd io.Reader, c restic.Chunker, data []byte) (out []byte, err error) {
	data = data[:0]
	var job *ordinaryHashStream
	defer func() {
		if job != nil {
			if err == nil {
				job.write(data)
			}
			id := job.finish()
			if err == nil {
				s.chunkID, s.hashed = id, true
			}
		}
	}()
	for {
		if s.bpos >= s.bmax {
			n, e := io.ReadFull(rd, s.readBuf)
			if e == io.ErrUnexpectedEOF {
				e = nil
			}
			if e == io.EOF && !s.closed {
				s.closed = true
				if len(data) > 0 {
					return data, nil
				}
			}
			if e != nil {
				return nil, e
			}
			s.bpos, s.bmax = 0, uint(n)
		}
		split := c.NextSplitPoint(s.readBuf[s.bpos:s.bmax])
		end := s.bmax
		if split != -1 {
			end = s.bpos + uint(split)
		}
		span := s.readBuf[s.bpos:end]
		if job != nil && len(data)+len(span) > cap(data) {
			job.join()
		}
		data = append(data, span...)
		s.bpos = end
		if split != -1 {
			return data, nil
		}
		if job == nil && len(data) > asyncHashBufferSize {
			job = s.ordinaryHashes.tryOrdinary()
		}
		if job != nil {
			job.write(data)
		}
	}
}
