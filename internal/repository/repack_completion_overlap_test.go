package repository

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestRepackCompletionOverlapHeldPredecessor(t *testing.T) {
	queue := make(chan index.PackBlobs, 3)
	for i := 0; i < 3; i++ {
		queue <- index.PackBlobs{Blobs: make(pack.Blobs, 1)}
	}
	close(queue)
	release := make(chan struct{})
	defer close(release)
	started := make(chan int, 3)
	wantErr := errors.New("predecessor failed")
	var calls int
	var drained atomic.Bool
	done := make(chan error, 1)
	go func() {
		done <- runRepackCompletionOverlap(queue, func(task index.PackBlobs) *repackPackCompletion {
			calls++
			started <- calls
			if calls == 1 {
				return &repackPackCompletion{descriptors: 1, finish: func() error { <-release; return wantErr }, drain: func() { <-release }}
			}
			return &repackPackCompletion{descriptors: 1, finish: func() error { return nil }, drain: func() { drained.Store(true) }}
		}, restic.NoopCounter)
	}()
	for i := 1; i <= 2; i++ {
		select {
		case got := <-started:
			if got != i {
				t.Fatalf("start %d, want %d", got, i)
			}
		case <-time.After(time.Second):
			t.Fatal("successor did not start before predecessor completion")
		}
	}
	select {
	case <-started:
		t.Fatal("third pack admitted before predecessor settled")
	case err := <-done:
		t.Fatalf("returned before callback completion: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not join")
	}
	if !drained.Load() {
		t.Fatal("admitted successor was not drained after predecessor failure")
	}
}

func TestRepackCompletionOverlapDescriptorBound(t *testing.T) {
	queue := make(chan index.PackBlobs, 2)
	queue <- index.PackBlobs{Blobs: make(pack.Blobs, maxRepackOverlapDescriptors)}
	queue <- index.PackBlobs{Blobs: make(pack.Blobs, 1)}
	close(queue)
	release := make(chan struct{})
	defer close(release)
	started := make(chan int, 2)
	var calls int
	done := make(chan error, 1)
	go func() {
		done <- runRepackCompletionOverlap(queue, func(task index.PackBlobs) *repackPackCompletion {
			calls++
			started <- calls
			if calls == 1 {
				return &repackPackCompletion{descriptors: len(task.Blobs), finish: func() error { <-release; return nil }, drain: func() { <-release }}
			}
			return &repackPackCompletion{descriptors: 1, finish: func() error { return nil }, drain: func() {}}
		}, restic.NoopCounter)
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("first pack did not start")
	}
	select {
	case <-started:
		t.Fatal("overlap exceeded descriptor bound")
	case <-time.After(20 * time.Millisecond):
	}
	release <- struct{}{}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("did not finish after descriptor capacity became available")
	}
}

func TestRepackCompletionOverlapImmediateErrorAndEOF(t *testing.T) {
	for _, fail := range []bool{false, true} {
		queue := make(chan index.PackBlobs, 2)
		queue <- index.PackBlobs{}
		queue <- index.PackBlobs{}
		close(queue)
		calls, completed := 0, 0
		wantErr := errors.New("stream failed")
		err := runRepackCompletionOverlap(queue, func(task index.PackBlobs) *repackPackCompletion {
			calls++
			var readErr error
			if fail {
				readErr = wantErr
			}
			return &repackPackCompletion{readErr: readErr, finish: func() error { completed++; return readErr }, drain: func() { completed++ }}
		}, restic.NoopCounter)
		if fail {
			if !errors.Is(err, wantErr) || calls != 1 || completed != 1 {
				t.Fatalf("failure admission: %v %d %d", err, calls, completed)
			}
		} else if err != nil || calls != 2 || completed != 2 {
			t.Fatalf("EOF completion: %v %d %d", err, calls, completed)
		}
	}
}
