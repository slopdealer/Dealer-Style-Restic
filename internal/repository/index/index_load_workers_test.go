package index

import (
	"bytes"
	"context"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/restic"
)

func TestIndexLoadWorkers(t *testing.T) {
	stock := func(connections uint, procs int) uint { return connections + uint(procs) }

	for connections := uint(1); connections <= 16; connections++ {
		for procs := 1; procs <= 64; procs++ {
			got := indexLoadWorkers(connections, procs)
			// one and two CPUs: exactly stock
			if procs <= 2 && got != stock(connections, procs) {
				t.Errorf("connections=%d procs=%d: %d workers, stock has %d", connections, procs, got, stock(connections, procs))
			}
			// every connection can download while an index file is decoded
			if got < connections+1 {
				t.Errorf("connections=%d procs=%d: %d workers cannot keep %d downloads in flight", connections, procs, got, connections)
			}
			if got > stock(connections, procs) {
				t.Errorf("connections=%d procs=%d: %d workers, more than stock's %d", connections, procs, got, stock(connections, procs))
			}
			if got < uint(procs) {
				t.Errorf("connections=%d procs=%d: %d workers, fewer than CPUs", connections, procs, got)
			}
			// fewer CPUs must never mean fewer downloads than with more CPUs allows
			if procs > 1 && min(got, connections) < min(indexLoadWorkers(connections, procs-1), connections) {
				t.Errorf("connections=%d: download slots fall from %d to %d CPUs", connections, procs-1, procs)
			}
		}
	}

	for _, c := range []struct {
		connections uint
		procs       int
		want        uint
	}{
		{2, 1, 3}, {2, 2, 4}, {5, 1, 6}, {5, 2, 7}, // as stock
		{2, 4, 4}, // local backend, four CPUs: unchanged by this repair
		{5, 4, 7}, {5, 96, 96}, {2, 0, 3},
	} {
		if got := indexLoadWorkers(c.connections, c.procs); got != c.want {
			t.Errorf("connections=%d procs=%d: %d workers, want %d", c.connections, c.procs, got, c.want)
		}
	}
}

// connectionLimitedIndexRepo serves the same index file under many names and
// admits as many loads at a time as it has connections, like a backend.
type connectionLimitedIndexRepo struct {
	connections uint
	files       int
	index       []byte

	slots chan struct{}

	mu       sync.Mutex
	inFlight uint
	maxSeen  uint
	full     chan struct{}
	fullOnce sync.Once
}

func (r *connectionLimitedIndexRepo) Connections() uint { return r.connections }

func (r *connectionLimitedIndexRepo) List(ctx context.Context, _ restic.FileType, fn func(restic.ID, int64) error) error {
	for i := 0; i < r.files; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := fn(restic.Hash([]byte{byte(i), byte(i >> 8)}), int64(len(r.index))); err != nil {
			return err
		}
	}
	return nil
}

func (r *connectionLimitedIndexRepo) LoadUnpacked(ctx context.Context, _ restic.FileType, _ restic.ID) ([]byte, error) {
	select {
	case r.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-r.slots }()

	r.mu.Lock()
	r.inFlight++
	r.maxSeen = max(r.maxSeen, r.inFlight)
	if r.inFlight == r.connections {
		r.fullOnce.Do(func() { close(r.full) })
	}
	r.mu.Unlock()

	// a download takes a while: stay until every connection is in use
	select {
	case <-r.full:
	case <-time.After(2 * time.Second):
	case <-ctx.Done():
	}

	r.mu.Lock()
	r.inFlight--
	r.mu.Unlock()
	return bytes.Clone(r.index), ctx.Err()
}

// The number of index files that are downloaded at the same time is the
// backend's connection count, also on a machine with one or two CPUs.
func TestForAllIndexesDownloadsOnEveryConnection(t *testing.T) {
	var encoded bytes.Buffer
	idx := NewIndex()
	idx.Finalize()
	if err := idx.Encode(&encoded); err != nil {
		t.Fatal(err)
	}

	for _, procs := range []int{1, 2, 4} {
		for _, connections := range []uint{2, 5} {
			old := runtime.GOMAXPROCS(procs)
			repo := &connectionLimitedIndexRepo{
				connections: connections,
				files:       40,
				index:       encoded.Bytes(),
				slots:       make(chan struct{}, connections),
				full:        make(chan struct{}),
			}
			seen := 0
			err := ForAllIndexes(context.Background(), repo, repo, func(_ restic.ID, idx *Index, err error) error {
				if err != nil {
					return err
				}
				if idx == nil {
					t.Error("no index")
				}
				seen++
				return nil
			})
			runtime.GOMAXPROCS(old)
			if err != nil {
				t.Fatal(err)
			}
			if seen != repo.files {
				t.Errorf("GOMAXPROCS=%d connections=%d: %d of %d index files delivered", procs, connections, seen, repo.files)
			}
			if repo.maxSeen != connections {
				t.Errorf("GOMAXPROCS=%d connections=%d: at most %d index files were downloaded at the same time, stock downloads %d",
					procs, connections, repo.maxSeen, connections)
			}
		}
	}
}
