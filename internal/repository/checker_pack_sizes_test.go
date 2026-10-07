package repository

import (
	"context"
	"path/filepath"
	"sort"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

func collectPreparedPackErrors(ctx context.Context, f func(context.Context, chan<- error)) (errs []error) {
	errChan := make(chan error)
	go f(ctx, errChan)
	for err := range errChan {
		errs = append(errs, err)
	}
	return errs
}

func packErrorStrings(errs []error) []string {
	result := make([]string, len(errs))
	for i, err := range errs {
		result[i] = err.Error()
	}
	sort.Strings(result)
	return result
}

func TestPreparedPackSizesObserveLaterBackendChanges(t *testing.T) {
	ctx := context.Background()
	repo, be := TestFromFixture(t, filepath.Join("..", "checker", "testdata", "checker-test-repo.tar.gz"))
	rtest.OK(t, repo.LoadIndex(ctx, restic.NoopTerminalCounterFactory))
	c := newChecker(repo)
	prepared := c.PreparePackSizes(ctx)
	expected, err := pack.Size(ctx, repo, false)
	rtest.OK(t, err)
	rtest.Equals(t, expected, prepared.sizes)
	rtest.OK(t, prepared.err)

	// These model changes made by a writer that completes during settlement.
	// The expected map stays fixed, but actual membership must be read later.
	var missing restic.ID
	for id := range expected {
		missing = id
		break
	}
	rtest.Assert(t, !missing.IsNull(), "fixture has no indexed packs")
	rtest.OK(t, be.Remove(ctx, backend.Handle{Type: backend.PackFile, Name: missing.String()}))
	orphan := restic.Hash([]byte("new pack published while confirmation is pending"))
	rtest.OK(t, be.Save(ctx, backend.Handle{Type: backend.PackFile, Name: orphan.String()}, backend.NewByteReader([]byte("orphan"), be.Hasher())))

	ordinary := collectPreparedPackErrors(ctx, c.Packs)
	early := collectPreparedPackErrors(ctx, func(ctx context.Context, errs chan<- error) {
		c.PacksWithPreparedSizes(ctx, prepared, errs)
	})
	rtest.Equals(t, packErrorStrings(ordinary), packErrorStrings(early))
	var sawMissing, sawOrphan bool
	for _, err := range early {
		if e, ok := err.(*ErrPackMetadata); ok {
			sawMissing = sawMissing || (e.ID == missing && e.Missing)
			sawOrphan = sawOrphan || (e.ID == orphan && e.Orphaned)
		}
	}
	rtest.Assert(t, sawMissing && sawOrphan, "later membership changes were not observed: %v", early)
	rtest.Assert(t, prepared.repo == nil && prepared.sizes == nil, "prepared result was not transferred")
	// A transferred object falls back safely if used again.
	again := collectPreparedPackErrors(ctx, func(ctx context.Context, errs chan<- error) {
		c.PacksWithPreparedSizes(ctx, prepared, errs)
	})
	rtest.Equals(t, packErrorStrings(ordinary), packErrorStrings(again))
}

func TestPreparedPackSizesCancellationDropsPartialMap(t *testing.T) {
	repo, _ := TestFromFixture(t, filepath.Join("..", "checker", "testdata", "checker-test-repo.tar.gz"))
	rtest.OK(t, repo.LoadIndex(context.Background(), restic.NoopTerminalCounterFactory))
	c := newChecker(repo)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	prepared := c.PreparePackSizes(ctx)
	rtest.Equals(t, context.Canceled, prepared.err)
	rtest.Assert(t, prepared.sizes == nil, "canceled preparation retained partial sizes")

	// Drain errors, as callers of the frozen Packs API already must do.
	errs := collectPreparedPackErrors(ctx, func(ctx context.Context, errs chan<- error) {
		c.PacksWithPreparedSizes(ctx, prepared, errs)
	})
	rtest.Equals(t, 1, len(errs))
	rtest.Equals(t, context.Canceled, errs[0])
}

func TestPreparedPackSizesReuseForReadData(t *testing.T) {
	ctx := context.Background()
	repo, _ := TestFromFixture(t, filepath.Join("..", "checker", "testdata", "checker-test-repo.tar.gz"))
	rtest.OK(t, repo.LoadIndex(ctx, restic.NoopTerminalCounterFactory))
	c := newChecker(repo)
	prepared := c.PreparePackSizes(ctx)
	readSizes := prepared.ForReadPacks()
	expected, err := pack.Size(ctx, repo, false)
	rtest.OK(t, err)
	errs := collectPreparedPackErrors(ctx, func(ctx context.Context, errs chan<- error) {
		c.PacksWithPreparedSizes(ctx, prepared, errs)
	})
	rtest.Equals(t, 0, len(errs))
	rtest.Equals(t, expected, readSizes.sizes)

	// A read-data filter is allowed to alter its map. This is safe only after
	// metadata checking finishes, and must not alter the loaded index itself.
	errs = collectPreparedPackErrors(ctx, func(ctx context.Context, errs chan<- error) {
		c.ReadPacksWithPreparedSizes(ctx, readSizes, func(sizes map[restic.ID]int64) map[restic.ID]int64 {
			rtest.Equals(t, expected, sizes)
			for id := range sizes {
				delete(sizes, id)
				break
			}
			return sizes
		}, restic.NewNoopPrinter(), errs)
	})
	rtest.Equals(t, 0, len(errs))
	rtest.Assert(t, readSizes.repo == nil && readSizes.sizes == nil, "read-data did not consume the shared descriptor")
	again, err := pack.Size(ctx, repo, false)
	rtest.OK(t, err)
	rtest.Equals(t, expected, again)
}
