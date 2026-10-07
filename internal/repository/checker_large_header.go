package repository

import (
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// Small headers retain the direct slice path. Large headers can avoid the
// complete decoded entry slice, whose lifetime would overlap membership checks.
const largeCheckHeaderThreshold = 64 << 10

// Called only after the backend has completed and the full pack hash matches.
// Input belongs to this completed attempt, so a retry never sees decrypted
// header bytes from a previous callback.
func checkLargePackHeader(r *Repository, id restic.ID, idxHdrSize int, tail []byte, errs []error) error {
	entries, hdrSize, err := pack.ListOwnedTailEntries(r.Key(), tail)
	if err != nil {
		return &ErrPackData{PackID: id, errs: append(errs, err)}
	}
	if uint32(idxHdrSize) != hdrSize {
		debug.Log("Pack header size does not match, want %v, got %v", idxHdrSize, hdrSize)
		errs = append(errs, errors.Errorf("pack header size does not match, want %v, got %v", idxHdrSize, hdrSize))
	}
	for blob := range entries {
		if !r.idx.ContainsPackBlob(id, blob) {
			errs = append(errs, errors.Errorf("blob %v is not contained in index or position is incorrect", blob.ID))
		}
	}
	if len(errs) > 0 {
		return &ErrPackData{PackID: id, errs: errs}
	}
	return nil
}
