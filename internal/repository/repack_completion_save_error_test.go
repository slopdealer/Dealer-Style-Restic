package repository

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"

	"golang.org/x/sync/semaphore"
)

// scriptedRepackUploader stores nothing. It records every save it is asked
// for and holds the callbacks of asynchronous saves until the test delivers
// them, in an order and with the errors the test chooses.
type scriptedRepackUploader struct {
	mu        sync.Mutex
	async     []restic.BlobHandle
	callbacks []func(error)
	delivered int
	sync      []restic.BlobHandle
	syncErr   error
	corrupt   bool
}

func (u *scriptedRepackUploader) SaveBlob(_ context.Context, tpe restic.BlobType, buf []byte, id restic.ID, _ bool) (restic.ID, bool, int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.sync = append(u.sync, restic.BlobHandle{Type: tpe, ID: id})
	return id, false, len(buf), u.syncErr
}

func (u *scriptedRepackUploader) SaveBlobAsync(_ context.Context, tpe restic.BlobType, buf []byte, id restic.ID, _ bool, cb func(restic.ID, bool, int, error)) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if restic.Hash(buf) != id {
		u.corrupt = true
	}
	size := len(buf)
	u.async = append(u.async, restic.BlobHandle{Type: tpe, ID: id})
	u.callbacks = append(u.callbacks, func(err error) {
		// as the real uploader does: from another goroutine
		done := make(chan struct{})
		go func() {
			defer close(done)
			cb(id, false, size, err)
		}()
		<-done
		u.mu.Lock()
		u.delivered++
		u.mu.Unlock()
	})
}

// deliver calls back for the i-th asynchronous save.
func (u *scriptedRepackUploader) deliver(i int, err error) {
	u.mu.Lock()
	cb := u.callbacks[i]
	u.mu.Unlock()
	cb(err)
}

// repackTestPacks stores packs of the given numbers of small blobs (plus,
// where asked for, one blob above the asynchronous copy limit) and returns
// each pack as repack's download queue would hand it out, with its blobs in
// the order in which the pack is read.
func repackTestPacks(t *testing.T, repo *Repository, blobCounts []int, oversized []bool) []index.PackBlobs {
	t.Helper()
	rnd := rand.New(rand.NewSource(20261007))
	var result []index.PackBlobs
	for p, n := range blobCounts {
		var handles []restic.BlobHandle
		rtest.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, up restic.BlobSaverWithAsync) error {
			save := func(size int) error {
				buf := make([]byte, size)
				rnd.Read(buf)
				id, _, _, err := up.SaveBlob(ctx, restic.DataBlob, buf, restic.ID{}, false)
				handles = append(handles, restic.BlobHandle{Type: restic.DataBlob, ID: id})
				return err
			}
			for i := 0; i < n; i++ {
				if err := save(3000 + rnd.Intn(3000)); err != nil {
					return err
				}
			}
			if oversized != nil && oversized[p] {
				return save(maxAsyncRepackCopyBytes + 1)
			}
			return nil
		}))
		packs := restic.NewIDSet()
		for _, h := range handles {
			for _, pb := range repo.LookupBlob(h) {
				packs.Insert(pb.PackID())
			}
		}
		if len(packs) != 1 {
			t.Fatalf("pack %d: the blobs of one upload session went into %d packs", p, len(packs))
		}
		for pbs := range repo.idx.ListPacks(context.Background(), packs) {
			pbs.Blobs.Sort()
			result = append(result, pbs)
		}
	}
	if len(result) != len(blobCounts) {
		t.Fatalf("%d packs listed, %d stored", len(result), len(blobCounts))
	}
	return result
}

func repackKeepSet(packs ...index.PackBlobs) restic.BlobSet {
	keep := restic.NewBlobSet()
	for _, p := range packs {
		for _, b := range p.Blobs {
			keep.Insert(b.BlobHandle)
		}
	}
	return keep
}

// stillRunning reports that no value arrives on done for a short while.
func stillRunning(done <-chan error) bool {
	select {
	case <-done:
		return false
	case <-time.After(20 * time.Millisecond):
		return true
	}
}

// The error of a save that fails AFTER its pack has been read is the pack's
// result: finish returns it, whichever save fails and in whatever order the
// callbacks arrive, and it returns the error of the first failing blob in the
// order the pack was read. finish never returns before the last callback.
// (The second, independent test of this path beside
// TestRepackCompletionNextPackAndFailure, which reaches it through CopyBlobs
// with a real uploader.)
func TestRepackPackCompletionReturnsAsyncSaveError(t *testing.T) {
	repo, _, _ := TestRepositoryWithVersion(t, 2)
	const blobs = 6
	failure := func(i int) error { return fmt.Errorf("save of blob %d failed", i) }

	for _, tc := range []struct {
		name  string
		fail  []int // positions in read order whose save fails
		order []int // order in which the callbacks arrive
		want  int   // position whose error is the result; -1: no error
	}{
		{"all saves succeed", nil, []int{0, 1, 2, 3, 4, 5}, -1},
		{"the first save fails", []int{0}, []int{0, 1, 2, 3, 4, 5}, 0},
		{"a save in the middle fails", []int{3}, []int{0, 1, 2, 3, 4, 5}, 3},
		{"the last save fails", []int{5}, []int{0, 1, 2, 3, 4, 5}, 5},
		{"the last save fails and is reported first", []int{5}, []int{5, 4, 3, 2, 1, 0}, 5},
		{"the first save fails and is reported last", []int{0}, []int{5, 4, 3, 2, 1, 0}, 0},
		{"two saves fail, the later one is reported first", []int{1, 4}, []int{4, 0, 5, 2, 3, 1}, 1},
		{"every save fails", []int{0, 1, 2, 3, 4, 5}, []int{3, 5, 1, 0, 4, 2}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pack := repackTestPacks(t, repo, []int{blobs}, nil)[0]
			keep := repackKeepSet(pack)
			var keepMutex sync.Mutex
			up := &scriptedRepackUploader{}
			ctx := context.Background()

			completion := startRepackPackCompletion(ctx, ctx, repo, up, keep, &keepMutex, semaphore.NewWeighted(maxAsyncRepackCopyBytes), pack)
			if completion.readErr != nil {
				t.Fatalf("reading the pack failed: %v", completion.readErr)
			}
			if completion.descriptors != blobs || len(up.async) != blobs || keep.Len() != 0 {
				t.Fatalf("%d descriptors, %d asynchronous saves, %d blobs left to keep; the pack has %d blobs", completion.descriptors, len(up.async), keep.Len(), blobs)
			}
			for i, h := range up.async {
				if h != pack.Blobs[i].BlobHandle {
					t.Fatalf("save %d is not for the blob at position %d of the pack", i, i)
				}
			}
			if up.corrupt {
				t.Fatal("a save was handed bytes that do not hash to its ID")
			}

			done := make(chan error, 1)
			go func() { done <- completion.finish() }()
			failing := map[int]bool{}
			for _, i := range tc.fail {
				failing[i] = true
			}
			for n, i := range tc.order {
				if !stillRunning(done) {
					t.Fatalf("finish returned after %d of %d callbacks", n, blobs)
				}
				var err error
				if failing[i] {
					err = failure(i)
				}
				up.deliver(i, err)
			}
			var got error
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("finish did not return after the last callback")
			}
			switch {
			case tc.want < 0 && got != nil:
				t.Fatalf("finish returned %v, every save succeeded", got)
			case tc.want >= 0 && (got == nil || got.Error() != failure(tc.want).Error()):
				t.Fatalf("finish returned %v, want the error of the save at position %d", got, tc.want)
			}
		})
	}
}

// A blob above the asynchronous copy limit is saved inside the read. Its
// error ends the read and is the pack's result, after the asynchronous saves
// admitted before it have been joined.
func TestRepackPackCompletionReturnsSynchronousSaveError(t *testing.T) {
	repo, _, _ := TestRepositoryWithVersion(t, 2)
	pack := repackTestPacks(t, repo, []int{3}, []bool{true})[0]
	keep := repackKeepSet(pack)
	var keepMutex sync.Mutex
	syncErr := errors.New("synchronous save failed")
	up := &scriptedRepackUploader{syncErr: syncErr}
	ctx := context.Background()

	completion := startRepackPackCompletion(ctx, ctx, repo, up, keep, &keepMutex, semaphore.NewWeighted(maxAsyncRepackCopyBytes), pack)
	if !errors.Is(completion.readErr, syncErr) || len(up.sync) != 1 {
		t.Fatalf("read result %v after %d synchronous saves; want the save's error after one", completion.readErr, len(up.sync))
	}
	done := make(chan error, 1)
	go func() { done <- completion.finish() }()
	for i := range up.async {
		if !stillRunning(done) {
			t.Fatalf("finish returned with %d asynchronous saves still pending", len(up.async)-i)
		}
		up.deliver(i, nil)
	}
	select {
	case got := <-done:
		if !errors.Is(got, syncErr) {
			t.Fatalf("finish returned %v, want the synchronous save's error", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("finish did not return")
	}
}

// Through the worker loop with the real pack completion: the save error of a
// pack is reported only after the pack behind it has been read. The worker
// must return that error, must have joined every save of both packs first,
// and must not read a third pack.
func TestRepackWorkerReturnsSaveErrorOfPredecessor(t *testing.T) {
	repo, _, _ := TestRepositoryWithVersion(t, 2)
	packs := repackTestPacks(t, repo, []int{4, 3, 5}, nil)
	keep := repackKeepSet(packs...)
	var keepMutex sync.Mutex
	up := &scriptedRepackUploader{}
	ctx := context.Background()
	budget := semaphore.NewWeighted(maxAsyncRepackCopyBytes)
	saveErr := errors.New("save of the second blob of the first pack failed")

	queue := make(chan index.PackBlobs, len(packs))
	for _, p := range packs {
		queue <- p
	}
	close(queue)

	started := make(chan int, len(packs))
	progress := &countingCounter{}
	done := make(chan error, 1)
	go func() {
		n := 0
		done <- runRepackCompletionOverlap(queue, func(task index.PackBlobs) *repackPackCompletion {
			c := startRepackPackCompletion(ctx, ctx, repo, up, keep, &keepMutex, budget, task)
			n++
			started <- n
			return c
		}, progress)
	}()

	// the second pack is read while every save of the first is pending
	for want := 1; want <= 2; want++ {
		select {
		case got := <-started:
			if got != want {
				t.Fatalf("pack %d was read, expected pack %d", got, want)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("pack %d was not read while the saves before it were pending", want)
		}
	}
	first, second := len(packs[0].Blobs), len(packs[1].Blobs)
	up.mu.Lock()
	asked := len(up.async)
	up.mu.Unlock()
	if asked != first+second {
		t.Fatalf("%d saves were asked for after two packs of %d and %d blobs", asked, first, second)
	}

	// first pack: one save fails; every callback but one of the second pack arrives
	for i := 0; i < first; i++ {
		var err error
		if i == 1 {
			err = saveErr
		}
		up.deliver(i, err)
	}
	for i := first; i < first+second-1; i++ {
		up.deliver(i, nil)
	}
	if !stillRunning(done) {
		t.Fatal("the worker returned while a save of the second pack was still pending")
	}
	up.deliver(first+second-1, nil)

	select {
	case got := <-done:
		if !errors.Is(got, saveErr) {
			t.Fatalf("the worker returned %v, want the save error of the first pack", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the worker did not return after the last callback")
	}
	select {
	case n := <-started:
		t.Fatalf("pack %d was read after the first pack had failed", n)
	default:
	}
	up.mu.Lock()
	defer up.mu.Unlock()
	if len(up.async) != first+second || up.delivered != first+second {
		t.Fatalf("%d saves asked for, %d called back; the two packs read hold %d blobs", len(up.async), up.delivered, first+second)
	}
	if got, _ := progress.Get(); got != 0 {
		t.Fatalf("progress counts %d packs, none was settled without an error", got)
	}
	if keep.Len() != len(packs[2].Blobs) {
		t.Fatalf("%d blobs are left to keep, the unread third pack holds %d", keep.Len(), len(packs[2].Blobs))
	}
}
