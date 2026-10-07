package repository

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestPruneHeaderOverlapPendingTransfer(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	idx := headerOverlapLister{list: func(ctx context.Context, fn func(restic.PackBlob)) error {
		close(started)
		<-release
		fn(&pack.PackedBlob{Pack: restic.ID{1}})
		return nil
	}}
	p := startPruneHeaderSizes(context.Background(), idx)
	<-started
	taken := make(chan map[restic.ID]int64, 1)
	go func() { sizes, _ := p.take(); taken <- sizes }()
	select {
	case <-taken:
		t.Fatal("transferred an unfinished result")
	default:
	}
	close(release)
	select {
	case sizes := <-taken:
		if len(sizes) != 1 || p.sizes != nil {
			t.Fatalf("transfer retained or lost result: %v", sizes)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("transfer did not join completion")
	}
	p.discard()
}

func TestPruneHeaderOverlapTraversalErrorPrecedesHeader(t *testing.T) {
	want := errors.New("counting failed")
	used := index.NewAssociatedSet[uint8](index.NewMasterIndex())
	idx := headerOverlapLister{list: func(context.Context, func(restic.PackBlob)) error { return want }}
	called := false
	_, _, err := packInfoFromIndexWithHeaders(context.Background(), idx, used, &PruneStats{}, restic.NewNoopPrinter(), func() (map[restic.ID]int64, error) {
		called = true
		return nil, errors.New("header failed")
	})
	if err != want || called {
		t.Fatalf("counting error displaced: %v, header consumed=%v", err, called)
	}
}

// Isolate the exact header calculation over an actual in-memory index. This
// excludes loading, tree traversal, command startup and concurrent contention.
func BenchmarkPruneHeaderPreparation(b *testing.B) {
	for _, entries := range []int{100000, 1500000} {
		b.Run(fmt.Sprint(entries), func(b *testing.B) {
			idx := index.NewIndex()
			const perPack = 128
			var blobs pack.Blobs
			for i := 0; i < entries; i++ {
				var id restic.ID
				binary.LittleEndian.PutUint64(id[:], uint64(i+1))
				blobs = append(blobs, pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: id}, Offset: uint(i%perPack) * 100, Length: 100})
				if len(blobs) == perPack || i == entries-1 {
					var pid restic.ID
					binary.LittleEndian.PutUint64(pid[:], uint64(i/perPack+1))
					idx.StorePack(pid, blobs)
					blobs = blobs[:0]
				}
			}
			mi := index.NewMasterIndex()
			mi.Insert(idx)
			lister := headerOverlapLister{list: func(ctx context.Context, fn func(restic.PackBlob)) error {
				for blob := range mi.Values() {
					if err := ctx.Err(); err != nil {
						return err
					}
					fn(blob)
				}
				return nil
			}}
			for _, direct := range []bool{false, true} {
				b.Run(fmt.Sprintf("value=%v", direct), func(b *testing.B) {
					b.ReportAllocs()
					for i := 0; i < b.N; i++ {
						var sizes map[restic.ID]int64
						var err error
						if direct {
							sizes, err = mi.PackHeaderSizes(context.Background())
						} else {
							sizes, err = pack.Size(context.Background(), lister, true)
						}
						if err != nil || len(sizes) != (entries+perPack-1)/perPack {
							b.Fatalf("incomplete preparation: %d packs, %v", len(sizes), err)
						}
					}
				})
			}
		})
	}
}

type headerOverlapLister struct {
	list func(context.Context, func(restic.PackBlob)) error
}

func (l headerOverlapLister) ListBlobs(ctx context.Context, fn func(restic.PackBlob)) error {
	return l.list(ctx, fn)
}

func TestPruneHeaderOverlapExactSizes(t *testing.T) {
	blobs := []*pack.PackedBlob{
		{Pack: restic.ID{1}, Blob: pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{2}}, Length: 100}},
		{Pack: restic.ID{1}, Blob: pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{2}}, Length: 100}},
		{Pack: restic.ID{3}, Blob: pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.TreeBlob, ID: restic.ID{4}}, Length: 200, UncompressedLength: 500}},
	}
	idx := headerOverlapLister{list: func(ctx context.Context, fn func(restic.PackBlob)) error {
		for _, b := range blobs {
			fn(b)
		}
		return nil
	}}
	want, err := pack.Size(context.Background(), idx, true)
	if err != nil {
		t.Fatal(err)
	}
	p := startPruneHeaderSizes(context.Background(), idx)
	defer p.discard()
	got, err := p.take()
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v / %v, want %v", got, err, want)
	}
	if p.sizes != nil {
		t.Fatal("owner retained transferred map")
	}
}

func TestPruneHeaderOverlapDiscardJoins(t *testing.T) {
	started, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	idx := headerOverlapLister{list: func(ctx context.Context, fn func(restic.PackBlob)) error {
		close(started)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	}}
	p := startPruneHeaderSizes(context.Background(), idx)
	<-started
	stopped := make(chan struct{})
	go func() { p.discard(); close(stopped) }()
	<-canceled
	select {
	case <-stopped:
		t.Fatal("discard returned while worker still owned index")
	default:
	}
	close(release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("worker not joined")
	}
	if p.sizes != nil || !errors.Is(p.err, context.Canceled) {
		t.Fatalf("retained partial result: %v / %v", p.sizes, p.err)
	}
}

func TestPruneHeaderOverlapMissingPrecedesHeaderError(t *testing.T) {
	used := index.NewAssociatedSet[uint8](index.NewMasterIndex())
	used.Insert(restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{1}})
	idx := headerOverlapLister{list: func(context.Context, func(restic.PackBlob)) error { return nil }}
	called := false
	_, _, err := packInfoFromIndexWithHeaders(context.Background(), idx, used, &PruneStats{}, restic.NewNoopPrinter(), func() (map[restic.ID]int64, error) {
		called = true
		return nil, errors.New("header error")
	})
	if err != ErrIndexIncomplete || called {
		t.Fatalf("error precedence changed: %v, header consumed=%v", err, called)
	}
}

// B16 review: cancellation after private completion must still join and drop
// the completed map, even when no consumer ever takes it.
func TestB16PruneCompletedHeaderDiscard(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	idx := headerOverlapLister{list: func(_ context.Context, fn func(restic.PackBlob)) error {
		fn(&pack.PackedBlob{Pack: restic.ID{1}, Blob: pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.ID{2}}, Length: 100}})
		return nil
	}}
	p := startPruneHeaderSizes(ctx, idx)
	<-p.done
	if len(p.sizes) == 0 {
		t.Fatal("fixture did not publish a private map")
	}
	cancel()
	p.discard()
	if p.sizes != nil {
		t.Fatal("completed unused map retained")
	}
	p.discard()
}
