package main

import (
	"context"
	"strings"

	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

// snapshotMetadataLoad retains one ciphertext while index preparation runs.
// It neither selects a snapshot nor decodes one before lock confirmation.
type snapshotMetadataLoad struct {
	cancel context.CancelFunc
	done   chan struct{}
	loader restic.LoaderUnpacked
	// the snapshot file that was read, and its size
	id       restic.ID
	size     int64
	prepared bool
}

func startSnapshotMetadataLoad(ctx context.Context, repo *repository.Repository, load *repositoryIndexLoad, request string) *snapshotMetadataLoad {
	if load == nil || load.snapshots == nil || repo.Connections() < 2 {
		return nil
	}
	ctx, cancel := context.WithCancel(ctx)
	p := &snapshotMetadataLoad{cancel: cancel, done: make(chan struct{}), loader: repo}
	go func() {
		defer close(p.done)
		select {
		case <-ctx.Done():
			return
		case <-load.snapshots.ready:
		}
		if load.snapshots.err != nil {
			return
		}
		id, size, ok := snapshotMetadataTarget(ctx, load.snapshots.lister, request)
		if ok {
			p.loader = repo.PrepareSnapshot(ctx, id, size)
			p.id, p.size, p.prepared = id, size, p.loader != restic.LoaderUnpacked(repo)
		}
	}()
	return p
}

// A full or unambiguous prefix names one demand. "latest" names one demand
// only when the listing contains one snapshot. Never fetch all snapshots.
func snapshotMetadataTarget(ctx context.Context, list restic.Lister, request string) (restic.ID, int64, bool) {
	request, _, _ = strings.Cut(request, ":")
	var target restic.ID
	var size int64
	matches := 0
	err := list.List(ctx, restic.SnapshotFile, func(id restic.ID, n int64) error {
		if request == "latest" || strings.HasPrefix(id.String(), request) {
			matches++
			target, size = id, n
		}
		return nil
	})
	return target, size, err == nil && matches == 1 && size > 0 && size <= repository.PreparedSnapshotLimit
}

// get returns the loader that serves the snapshot read during the wait, or
// fallback. The snapshot is served only if confirmed, the listing taken after
// the lock was confirmed, lists it with the size it was read with: its name is
// the hash of what was read, the size is what is known about the file as it
// is stored now.
func (p *snapshotMetadataLoad) get(ctx context.Context, fallback restic.LoaderUnpacked, confirmed restic.Lister) restic.LoaderUnpacked {
	if p == nil {
		return fallback
	}
	<-p.done
	if !p.prepared {
		return p.loader
	}
	same := false
	err := confirmed.List(ctx, restic.SnapshotFile, func(id restic.ID, size int64) error {
		if id == p.id && size == p.size {
			same = true
		}
		return nil
	})
	if err != nil || !same {
		return fallback
	}
	return p.loader
}

func (p *snapshotMetadataLoad) finish() {
	if p == nil {
		return
	}
	p.cancel()
	<-p.done
	p.loader = nil
}

func (p *snapshotMetadataLoad) settle(confirmed bool) {
	if !confirmed {
		p.finish()
	}
}
