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

func (p *snapshotMetadataLoad) get(fallback restic.LoaderUnpacked) restic.LoaderUnpacked {
	if p == nil {
		return fallback
	}
	<-p.done
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
