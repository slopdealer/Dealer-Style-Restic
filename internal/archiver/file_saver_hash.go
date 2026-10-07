package archiver

import (
	"context"
	"sync"

	"github.com/restic/restic/internal/restic"
)

const asyncHashBufferSize = 1024 * 1024
const asyncHashMinSize = 256 * 1024

type chunkCompleteFunc = func(restic.ID, bool, int, error)

// The single reusable slot bounds retained payload capacity, not just the
// number of jobs. Busy and oversized chunks take the ordinary path immediately.
// The slot stays charged while cached and through an unknown blob's callback.
func (s *fileSaver) tryHashChunk(ctx context.Context, data []byte, complete chunkCompleteFunc, jobs *sync.WaitGroup) bool {
	if len(data) < asyncHashMinSize || len(data) > asyncHashBufferSize || s.hashBuffers == nil {
		return false
	}
	var buf []byte
	select {
	case buf = <-s.hashBuffers:
	default:
		return false
	}
	if buf == nil {
		buf = make([]byte, asyncHashBufferSize)
	}
	buf = buf[:len(data)]
	copy(buf, data)
	jobs.Add(1)
	go func() {
		defer jobs.Done()
		id := restic.Hash(buf)
		_, known := s.knownSaver.LookupStoredBlobSize(restic.BlobHandle{ID: id, Type: restic.DataBlob})
		if known {
			// Keep admission until submission returns. An uploader may apply
			// backpressure here; releasing early could accumulate blocked jobs.
			s.knownSaver.SaveKnownBlobAsync(ctx, id, complete)
			s.hashBuffers <- buf[:cap(buf)]
			return
		}
		// A callback can run before SaveBlobAsync returns. Keep admission
		// through both events, otherwise completed-but-blocked submissions
		// could accumulate outside the payload bound. Never wait in the
		// callback: uploaders may invoke it synchronously.
		var releaseLock sync.Mutex
		submitted, completed := false, false
		s.uploader.SaveBlobAsync(ctx, restic.DataBlob, buf, id, false, func(id restic.ID, known bool, size int, err error) {
			complete(id, known, size, err)
			releaseLock.Lock()
			completed = true
			if submitted {
				s.hashBuffers <- buf[:cap(buf)]
			}
			releaseLock.Unlock()
		})
		releaseLock.Lock()
		submitted = true
		if completed {
			s.hashBuffers <- buf[:cap(buf)]
		}
		releaseLock.Unlock()
	}()
	return true
}

func (s *fileSaver) saveChunkInline(ctx context.Context, data []byte, zero bool, complete chunkCompleteFunc) {
	var id restic.ID
	known := false
	if s.knownSaver != nil {
		if zero {
			id = s.chunkerFactory.ZeroChunk()
		} else {
			id = restic.Hash(data)
		}
		_, known = s.knownSaver.LookupStoredBlobSize(restic.BlobHandle{ID: id, Type: restic.DataBlob})
	}
	if known {
		s.knownSaver.SaveKnownBlobAsync(ctx, id, complete)
		return
	}
	owned := make([]byte, len(data))
	copy(owned, data)
	s.uploader.SaveBlobAsync(ctx, restic.DataBlob, owned, id, false, complete)
}
