package repository

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// PreparedPackSizes contains only index-derived information. It performs no
// backend listing or pack reads, so it can be prepared during lock settlement.
// The caller owns the result until transferring it to PacksWithPreparedSizes.
type PreparedPackSizes struct {
	repo  *Repository
	sizes map[restic.ID]int64
	err   error
}

// ForReadPacks shares the immutable expected sizes with a subsequent read-data
// pass. PacksWithPreparedSizes only reads this map (it removes matched entries
// from its separate backend-listing map). The read-data pass must start after
// pack metadata checking finishes, as its filter may modify the shared map.
func (p *PreparedPackSizes) ForReadPacks() *PreparedPackSizes {
	if p == nil {
		return nil
	}
	copy := *p
	return &copy
}

func (c *Checker) PreparePackSizes(ctx context.Context) *PreparedPackSizes {
	sizes, err := pack.Size(ctx, c.repo, false)
	if err != nil {
		// A partial size map cannot be used and need not survive cancellation.
		sizes = nil
	}
	return &PreparedPackSizes{repo: c.repo, sizes: sizes, err: err}
}

func (c *Checker) takePackSizes(ctx context.Context, prepared *PreparedPackSizes) (map[restic.ID]int64, error) {
	if prepared == nil || prepared.repo != c.repo {
		return pack.Size(ctx, c.repo, false)
	}
	sizes, err := prepared.sizes, prepared.err
	// Transfer once; another call uses the normal synchronous path.
	prepared.repo, prepared.sizes, prepared.err = nil, nil, nil
	return sizes, err
}
