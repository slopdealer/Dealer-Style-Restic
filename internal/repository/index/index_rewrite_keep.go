package index

import (
	"context"
	"slices"
	"time"

	"github.com/restic/restic/internal/restic"
)

// Rewrite reloads and decodes every index file, although most of them are kept
// as they are: a kept file holds no removed pack, is full and not oversized,
// and none of its packs was already met in another index file. All of that is
// known from the file's pack IDs and blob counts, which this process has seen
// when it loaded or wrote the file. An index file is named by the hash of its
// content, so what was seen then is what a reload would decode now.

// indexFileNote is what Rewrite needs to know about an index file to keep it.
type indexFileNote struct {
	packs  restic.IDs
	counts [restic.NumBlobTypes]uint
}

// noteIndexFile remembers the packs and blob counts of the index file id.
// idx must hold exactly the content of that file.
func (mi *MasterIndex) noteIndexFile(id restic.ID, idx *Index) {
	idx.m.RLock()
	note := indexFileNote{packs: slices.Clone(idx.packs)}
	for typ := range idx.byType {
		note.counts[typ] = idx.byType[typ].len()
	}
	idx.m.RUnlock()

	mi.idxMutex.Lock()
	defer mi.idxMutex.Unlock()
	if mi.fileNotes == nil {
		mi.fileNotes = make(map[restic.ID]indexFileNote)
	}
	mi.fileNotes[id] = note
}

// rewriteKeepsUnread returns the index files which Rewrite keeps without
// reading them again.
//
// Rewrite compares the content of each pack entry (PackBlobsHash, which covers
// the pack ID) with the entries of the index files it processed before, in
// whatever order the loaders deliver them. A file whose pack IDs occur in no
// other index file, and only once in the file itself, can match none of them
// in any order, and no other file can match one of its entries. Such a file is
// kept by the ordinary loop exactly when it holds no excluded pack, is full
// and is not oversized; these are decided here with the same Full and
// Oversized on the file's blob counts.
//
// A pack ID that occurs twice (the duplicate entries of old repositories, or
// the leftovers of an interrupted run) sends every file that lists it through
// the ordinary loop, which reads it and compares the content. When any of the
// index files is unknown to this process, nothing is skipped.
func (mi *MasterIndex) rewriteKeepsUnread(indexes restic.IDSet, excludePacks restic.IDSet) restic.IDSet {
	mi.idxMutex.RLock()
	defer mi.idxMutex.RUnlock()

	listed := make(map[restic.ID]uint8)
	for id := range indexes {
		note, ok := mi.fileNotes[id]
		if !ok {
			return nil
		}
		for _, packID := range note.packs {
			if listed[packID] < 2 {
				listed[packID]++
			}
		}
	}

	kept := restic.NewIDSet()
	now := time.Now()
	for id := range indexes {
		note := mi.fileNotes[id]

		// a freshly decoded index of this file, as far as Full and Oversized look
		fresh := &Index{created: now}
		for typ := range fresh.byType {
			fresh.byType[typ].numentries = note.counts[typ]
		}
		if !Full(fresh) || Oversized(fresh) {
			continue
		}

		keep := true
		for _, packID := range note.packs {
			if excludePacks.Has(packID) || listed[packID] != 1 {
				keep = false
				break
			}
		}
		if keep {
			kept.Insert(id)
		}
	}
	return kept
}

// rawIndexFileLoader reads a file as it is stored and returns an error when
// its content does not hash to its name, as the repository's LoadRaw does.
type rawIndexFileLoader interface {
	LoadRaw(ctx context.Context, t restic.FileType, id restic.ID) ([]byte, error)
}

// verifyKeptIndexFile reads an index file which Rewrite keeps without decoding
// it again and checks that it is still the file that was loaded: its content
// must hash to its name. This is the check with which upstream's reload of
// the file begins; content that passes it is the content this process has
// decoded before. A file that cannot be read, or that has changed, fails the
// rewrite with the error of upstream's reload.
func verifyKeptIndexFile(ctx context.Context, repo rawIndexFileLoader, id restic.ID) error {
	_, err := repo.LoadRaw(ctx, restic.IndexFile, id)
	return err
}
