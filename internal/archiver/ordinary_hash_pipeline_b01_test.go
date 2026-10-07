package archiver

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

type b01PipelineRejectUploader struct {
	t       *testing.T
	lane    *parentPairHashLane
	cancel  context.CancelFunc
	failure error
	calls   int
}

func (u *b01PipelineRejectUploader) LookupStoredBlobSize(restic.BlobHandle) (uint, bool) {
	return 0, false
}
func (u *b01PipelineRejectUploader) SaveKnownBlobAsync(context.Context, restic.ID, func(restic.ID, bool, int, error)) {
	u.t.Fatal("unknown blob submitted as known")
}
func (u *b01PipelineRejectUploader) SaveBlobAsync(_ context.Context, typ restic.BlobType, payload []byte, id restic.ID, _ bool, cb func(restic.ID, bool, int, error)) {
	u.calls++
	if typ != restic.DataBlob || id != restic.Hash(payload) {
		u.t.Error("ordinary stream supplied an uncertified identity")
	}
	// Saver callbacks can run synchronously. They must not wait for a lane
	// held by the reader that is currently submitting this detached payload.
	other := u.lane.tryOrdinary()
	if other == nil {
		u.t.Error("synchronous save retained hash admission")
	} else {
		other.write([]byte("other reader"))
		if other.finish() != restic.Hash([]byte("other reader")) {
			u.t.Error("interleaved reader identity differs")
		}
	}
	cb(restic.ID{}, false, 0, u.failure)
	clear(payload)
	u.cancel()
}

func TestB01OrdinaryPipelineSynchronousRejectionReleasesAdmission(t *testing.T) {
	const size = 3*1024*1024 + 17
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	lane := &parentPairHashLane{active: make(chan struct{}, 1)}
	failure := errors.New("synchronous upload rejection")
	u := &b01PipelineRejectUploader{t: t, lane: lane, cancel: cancel, failure: failure}
	saver := newFileSaver(ctx, nil, u, bufferTestFactory{size}, 0)
	payload := bytes.Repeat([]byte{71}, size+31)
	saver.NodeFromFileInfo = func(string, string, toNoder, bool) (*data.Node, error) {
		return &data.Node{Type: data.NodeTypeFile, Size: uint64(len(payload))}, nil
	}
	state := &fileChunkState{ctx: ctx, readBuf: make([]byte, chunkReadBufSize), ordinaryHashes: lane}
	f := &bufferTestFile{r: bytes.NewReader(payload)}
	completed := 0
	var result futureNodeResult
	saver.saveFile(ctx, bufferTestFactory{size}.NewChunker(), state, "/file", "file", f, func() {}, func() { t.Error("rejected file reported reading complete") }, func(r futureNodeResult) { completed++; result = r })
	if u.calls != 1 || completed != 1 || !f.closed || !errors.Is(result.err, failure) || len(lane.active) != 0 {
		t.Fatalf("calls=%d completed=%d closed=%v err=%v admission=%d", u.calls, completed, f.closed, result.err, len(lane.active))
	}
	if restic.Hash(payload[:size]) != restic.Hash(bytes.Repeat([]byte{71}, size)) {
		t.Fatal("detached upload destruction overwrote source bytes")
	}
}
