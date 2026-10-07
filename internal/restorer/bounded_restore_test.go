package restorer

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

type boundedTestBlob struct {
	id, pack restic.ID
	size     uint
}

func (b boundedTestBlob) PackID() restic.ID { return b.pack }
func (b boundedTestBlob) Handle() restic.BlobHandle {
	return restic.BlobHandle{ID: b.id, Type: restic.DataBlob}
}
func (b boundedTestBlob) CiphertextLength() uint             { return b.size }
func (b boundedTestBlob) UncompressedCiphertextLength() uint { return b.size }
func (b boundedTestBlob) PlaintextLength() uint              { return b.size }
func (b boundedTestBlob) IsCompressed() bool                 { return false }

// Three packs with a large needed blob occupy the three ordinary workers (the
// configured two and the additional one); the small pack must still progress
// on the bounded worker. Recovery admission needs an ordinary permit: it must
// wait, and must terminate on cancellation.
func TestBoundedRestoreProgressAndRecoveryCancellation(t *testing.T) {
	if extraOrdinaryRestoreWorkers(2) != 1 {
		t.Skip("needs a free core for the additional ordinary worker")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan bool, 4)
	recovery := make(chan error, 1)
	ordinaryReady := make(chan struct{})
	var ordinaryStarted atomic.Int32
	loader := func(ctx context.Context, _ restic.ID, _ []restic.BlobHandle, _ func(restic.BlobHandle, []byte, error) error) error {
		acquire, bounded := restic.SmallRestoreLoad(ctx)
		started <- bounded
		if bounded {
			<-ordinaryReady
			release, err := acquire(ctx)
			if err == nil {
				release()
			}
			recovery <- err
			return err
		}
		if ordinaryStarted.Add(1) == 3 {
			close(ordinaryReady)
		}
		<-ctx.Done()
		return ctx.Err()
	}
	blobs := make(map[restic.ID]boundedTestBlob)
	lookup := func(h restic.BlobHandle) []restic.PackBlob { return []restic.PackBlob{blobs[h.ID]} }
	r := newFileRestorer(t.TempDir(), loader, lookup, 2, false, false, nil, nil, restic.ID{})
	r.additionalWorker = true
	for i := 0; i < 4; i++ {
		id := restic.Hash([]byte(fmt.Sprint("blob", i)))
		pack := restic.Hash([]byte(fmt.Sprint("pack", i)))
		size := uint(restic.MediumRestoreBlobLimit + 65)
		if i == 3 {
			size = 1
		}
		blobs[id] = boundedTestBlob{id: id, pack: pack, size: size}
		r.addFile(fmt.Sprint(i), restic.IDs{id}, int64(size), nil)
	}
	done := make(chan error, 1)
	go func() { done <- r.restoreFiles(ctx) }()
	boundedCount := 0
	for i := 0; i < 4; i++ {
		select {
		case bounded := <-started:
			if bounded {
				boundedCount++
			}
		case <-time.After(5 * time.Second):
			t.Fatal("bounded work did not progress")
		}
	}
	if boundedCount != 1 {
		t.Fatalf("bounded workers: %d", boundedCount)
	}
	select {
	case err := <-recovery:
		t.Fatalf("recovery bypassed occupied ordinary permits: %v", err)
	default:
	}
	cancel()
	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("restore: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("workers did not join")
	}
	if err := <-recovery; err != context.Canceled {
		t.Fatalf("recovery: %v", err)
	}
}
