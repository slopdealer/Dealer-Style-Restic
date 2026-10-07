package archiver

import (
	"context"

	"github.com/restic/restic/internal/restic"
)

// parentPairHashLane shares one speculative hash worker across all file
// readers. It borrows existing assembly only while the parent validators also
// borrow it: finish joins before replay, publication, cancellation or reuse.
// No upload or completion callback borrows this lane or its assembly.
type parentPairHashLane struct {
	active chan struct{}
}

type parentPairHashJob struct {
	lane  *parentPairHashLane
	done  chan struct{}
	ids   [2]restic.ID
	valid [2]bool
}

func (l *parentPairHashLane) try(first, second []byte) *parentPairHashJob {
	if l == nil || (len(first) <= asyncHashBufferSize && len(second) <= asyncHashBufferSize) {
		return nil
	}
	select {
	case l.active <- struct{}{}:
	default:
		return nil
	}
	job := &parentPairHashJob{lane: l, done: make(chan struct{})}
	go func() {
		defer close(job.done)
		for i, part := range [2][]byte{first, second} {
			if len(part) > asyncHashBufferSize {
				job.ids[i] = restic.Hash(part)
				job.valid[i] = true
			}
		}
	}()
	return job
}

func (j *parentPairHashJob) finish() ([2]restic.ID, [2]bool) {
	if j == nil {
		return [2]restic.ID{}, [2]bool{}
	}
	<-j.done
	<-j.lane.active
	return j.ids, j.valid
}

// The hash is accepted only after its parent-predicted boundary is certified.
// Unknown uploads still receive their own exact-size copy, with ordinary saver
// admission and callback lifetime. Known submissions carry no borrowed bytes.
func (s *fileSaver) saveChunkWithID(ctx context.Context, data []byte, id restic.ID, complete chunkCompleteFunc) {
	_, known := s.knownSaver.LookupStoredBlobSize(restic.BlobHandle{ID: id, Type: restic.DataBlob})
	if known {
		s.knownSaver.SaveKnownBlobAsync(ctx, id, complete)
		return
	}
	owned := make([]byte, len(data))
	copy(owned, data)
	s.uploader.SaveBlobAsync(ctx, restic.DataBlob, owned, id, false, complete)
}
