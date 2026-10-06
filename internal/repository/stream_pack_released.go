package repository

import (
	"context"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// loadBlobsFromPackReleased is loadBlobsFromPack for handlers that use the
// backend themselves (repack and copy save the blobs, repair packs uploads
// them again). Each window is read into memory, the backend connection is
// released, and only then are the blobs decoded and handed to the handler, so
// a handler never runs inside the backend's Load callback. With a single
// backend connection a handler that saves from inside Load waits for the
// connection its own reader holds.
//
// The price is one backend request per window instead of one per pack range.
// The windows, the range buffer and every check are those of the restore path.
func (r *Repository) loadBlobsFromPackReleased(ctx context.Context, packID restic.ID, blobs pack.Blobs, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error) error {
	return streamPackWindowed(ctx, r.be.Load, r.LoadBlob, r.getZstdDecoder(), r.key, packID, blobs, handleBlobFn, restoreStreamWindow, true)
}
