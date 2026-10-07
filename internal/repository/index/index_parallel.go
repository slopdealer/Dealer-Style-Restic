package index

import (
	"context"
	"runtime"
	"sync"

	"github.com/restic/restic/internal/restic"
)

// ForAllIndexes loads all index files in parallel and calls the given callback.
// It is guaranteed that the function is not run concurrently. If the callback
// returns an error, this function is cancelled and also returns that error.
func ForAllIndexes(ctx context.Context, lister restic.Lister, repo restic.LoaderUnpacked,
	fn func(id restic.ID, index *Index, err error) error) error {

	// decoding an index can take quite some time such that this can be both CPU- or IO-bound
	// Each active load also owns decompressed plaintext until decoding completes.
	// Keep enough workers to use the CPUs, without adding another connection's
	// worth of transient plaintext. Two workers still overlap IO on one core.
	workerCount := min(repo.Connections()+uint(runtime.GOMAXPROCS(0)), uint(max(2, runtime.GOMAXPROCS(0))))

	var m sync.Mutex
	return restic.ParallelList(ctx, lister, restic.IndexFile, workerCount, func(ctx context.Context, id restic.ID, size int64) error {
		var err error
		var idx *Index

		var buf []byte
		if loader, ok := repo.(ordinaryIndexStreamLoader); ok && size >= 1024*1024 {
			idx, buf, err = loader.LoadIndexStream(ctx, id)
		} else {
			buf, err = repo.LoadUnpacked(ctx, restic.IndexFile, id)
		}
		if err == nil && idx == nil {
			idx, err = decodeIndexEager(buf, id)
		}

		m.Lock()
		defer m.Unlock()
		return fn(id, idx, err)
	})
}

// indexFileLoader is an explicit opt-in. Ordinary LoaderUnpacked wrappers keep
// control over every read unless they implement this capability themselves.
type indexFileLoader interface {
	LoadIndexFile(context.Context, restic.ID) (*Index, error, error)
}

func loadIndexFile(ctx context.Context, repo restic.LoaderUnpacked, id restic.ID) (*Index, error, error) {
	if loader, ok := repo.(indexFileLoader); ok {
		return loader.LoadIndexFile(ctx, id)
	}
	buf, err := repo.LoadUnpacked(ctx, restic.IndexFile, id)
	if err != nil {
		return nil, err, nil
	}
	idx, err := DecodeIndex(buf, id)
	return idx, nil, err
}

// ordinaryIndexStreamLoader is private opt-in: public repository wrappers keep
// their LoadUnpacked override. A rejected stream returns full plaintext for the
// inherited eager decoder; a partial index never escapes.
type ordinaryIndexStreamLoader interface {
	LoadIndexStream(context.Context, restic.ID) (*Index, []byte, error)
}
