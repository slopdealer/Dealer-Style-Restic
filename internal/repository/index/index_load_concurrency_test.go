package index

import (
	"context"
	"errors"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

type admissionTestLoader struct {
	restic.LoaderUnpacked
	entered chan struct{}
	release chan struct{}
	active atomic.Int64
	peak atomic.Int64
}

func (*admissionTestLoader) Connections() uint { return 32 }

func (r *admissionTestLoader) LoadUnpacked(ctx context.Context, _ restic.FileType, _ restic.ID) ([]byte, error) {
	n := r.active.Add(1)
	defer r.active.Add(-1)
	for old := r.peak.Load(); old < n; old = r.peak.Load() {
		if r.peak.CompareAndSwap(old, n) {
			break
		}
	}
	r.entered <- struct{}{}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-r.release:
		return []byte(`{"packs":[]}`), nil
	}
}

type admissionTestLister int

func (l admissionTestLister) List(_ context.Context, _ restic.FileType, fn func(restic.ID, int64) error) error {
	for i := 0; i < int(l); i++ {
		var id restic.ID
		id[0] = byte(i)
		if err := fn(id, 1); err != nil {
			return err
		}
	}
	return nil
}

func TestIndexLoadAdmissionAndCancellation(t *testing.T) {
	// the loader reports 32 connections: the admitted loads are the workers of
	// ForAllIndexes, which follow the connections and not the CPU count alone
	width := int(indexLoadWorkers((*admissionTestLoader)(nil).Connections(), runtime.GOMAXPROCS(0)))
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		r := &admissionTestLoader{entered: make(chan struct{}, 4*width), release: make(chan struct{})}
		var callbacks atomic.Int64
		done := make(chan error, 1)
		go func() {
			done <- ForAllIndexes(ctx, admissionTestLister(3*width), r, func(_ restic.ID, idx *Index, err error) error {
				if err != nil {
					return err
				}
				if idx == nil || !idx.Final() {
					return errors.New("missing decoded index")
				}
				callbacks.Add(1)
				return nil
			})
		}()
		for range width {
			select {
			case <-r.entered:
			case <-ctx.Done():
				t.Fatal("loaders did not fill admission")
			}
		}
		if canceled {
			cancel()
		} else {
			close(r.release)
		}
		err := <-done
		cancel()
		if canceled && !errors.Is(err, context.Canceled) || !canceled && err != nil {
			t.Fatalf("canceled=%v err=%v", canceled, err)
		}
		if r.peak.Load() > int64(width) || r.active.Load() != 0 {
			t.Fatalf("peak=%d active=%d width=%d", r.peak.Load(), r.active.Load(), width)
		}
		if !canceled && callbacks.Load() != int64(3*width) {
			t.Fatalf("callbacks=%d", callbacks.Load())
		}
	}
}
