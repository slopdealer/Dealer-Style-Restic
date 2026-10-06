package checker

import (
	"context"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

// LoadSnapshotsFromList is LoadSnapshots for a caller that has already listed
// the snapshot files: it takes the listing instead of listing them again.
func (c *Checker) LoadSnapshotsFromList(ctx context.Context, list restic.Lister, snapshotFilter *data.SnapshotFilter, args []string) error {
	var err error
	c.snapshots, err = restic.MemorizeList(ctx, list, restic.SnapshotFile)
	c.args = args
	c.snapshotFilter = snapshotFilter
	return err
}
