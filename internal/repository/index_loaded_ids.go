package repository

import (
	"context"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/restic"
)

// LoadedIndexIDs returns the IDs of the index files whose content makes up
// the index held in memory. Index files are named by the hash of their
// content, so a caller that compares this set with a listing of the index
// files knows whether the index in memory is the one a load would produce now.
func (r *Repository) LoadedIndexIDs() restic.IDSet {
	return r.idx.IDs()
}

// LoadedIndexFiles returns the index files of LoadedIndexIDs, each with the
// size with which it was listed when it was loaded (-1 when it was not loaded
// from a listing). A name is the hash of the content that was read, but it
// says nothing about what is stored under that name now; a caller that
// compares names compares these sizes with a new listing as well.
func (r *Repository) LoadedIndexFiles() map[restic.ID]int64 {
	return r.idx.LoadedFileSizes()
}

// LoadAddedIndexFiles loads the index files in added (ID to size, as listed)
// into the index held in memory. The result is what LoadIndex produces for
// the loaded and the added files together, but the files that are already
// loaded are not read again: a second LoadIndex lists and reads every index
// file and only skips inserting the known ones.
//
// None of the files in added may be part of the loaded index. On an error the
// caller must discard the index.
func (r *Repository) LoadAddedIndexFiles(ctx context.Context, added map[restic.ID]int64) error {
	// the loader LoadIndex uses
	loader := &ordinaryIndexStreamRepository{internalRepository: &internalRepository{r}}
	err := index.ForAllIndexes(ctx, addedIndexFiles(added), loader, func(_ restic.ID, idx *index.Index, err error) error {
		if err != nil {
			return err
		}
		r.idx.Insert(idx)
		return nil
	})
	if err != nil {
		return err
	}
	if err := r.idx.MergeFinalIndexes(); err != nil {
		return err
	}

	// the checks of loadIndexWithCallback after a load
	if r.cfg.Version < 2 {
		for blob := range r.idx.Values() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if blob.IsCompressed() {
				return errors.New("index uses feature not supported by repository version 1")
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return r.prepareCache()
}

// addedIndexFiles lists a known set of index files.
type addedIndexFiles map[restic.ID]int64

func (files addedIndexFiles) List(ctx context.Context, t restic.FileType, fn func(restic.ID, int64) error) error {
	if t != restic.IndexFile {
		return nil
	}
	for id, size := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := fn(id, size); err != nil {
			return err
		}
	}
	return ctx.Err()
}
