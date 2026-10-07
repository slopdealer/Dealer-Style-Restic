package restorer

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestRestoreMetadataPoolFinishSettlesWorkBeforeTraversalError(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{})
	warnings := make([]string, 0, 1)
	res := &Restorer{Warn: func(message string) { warnings = append(warnings, message) }}
	pool := newRestoreMetadataPool(context.Background(), res)
	if err := pool.schedule("/earlier", func(ctx context.Context, warn func(string)) error {
		close(started)
		select {
		case <-release:
			warn("earlier warning")
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}); err != nil {
		t.Fatal(err)
	}
	<-started

	traversalErr := errors.New("later subtree failed")
	finished := make(chan error, 1)
	go func() { finished <- pool.finish(traversalErr) }()

	select {
	case err := <-finished:
		close(release)
		t.Fatalf("finish returned before earlier metadata settled: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)

	select {
	case err := <-finished:
		if !errors.Is(err, traversalErr) {
			t.Fatalf("finish returned %v, want traversal error %v", err, traversalErr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("finish did not join metadata worker")
	}
	if len(warnings) != 1 || warnings[0] != "earlier warning" {
		t.Fatalf("warnings = %v, want earlier warning before traversal error", warnings)
	}
}

func TestRestoreMetadataPoolReportsResultsInTraversalOrder(t *testing.T) {
	releaseFirst := make(chan struct{})
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	var reported []string
	res := &Restorer{
		Warn: func(message string) { reported = append(reported, "warn:"+message) },
		Error: func(location string, err error) error {
			reported = append(reported, "error:"+location)
			return err
		},
	}
	pool := newRestoreMetadataPool(context.Background(), res)
	if err := pool.schedule("/first", func(_ context.Context, warn func(string)) error {
		close(firstStarted)
		<-releaseFirst
		warn("first warning")
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	secondErr := errors.New("second metadata failed")
	if err := pool.schedule("/second", func(_ context.Context, _ func(string)) error {
		close(secondStarted)
		return secondErr
	}); err != nil {
		close(releaseFirst)
		pool.close()
		t.Fatal(err)
	}
	<-firstStarted
	<-secondStarted
	close(releaseFirst)

	err := pool.drain()
	// drain stops scheduling with cancellation; finish returns the settled
	// worker error to the traversal caller.
	if !errors.Is(err, context.Canceled) || !errors.Is(pool.firstErr, secondErr) {
		pool.close()
		t.Fatalf("drain returned %v, settled error %v, want cancellation and %v", err, pool.firstErr, secondErr)
	}
	if err := pool.finish(nil); !errors.Is(err, secondErr) {
		t.Fatalf("finish returned %v, want %v", err, secondErr)
	}
	want := []string{"warn:first warning", "error:/second"}
	if fmt.Sprint(reported) != fmt.Sprint(want) {
		t.Fatalf("reported %v, want %v", reported, want)
	}
}
