package archiver

import "github.com/restic/restic/internal/data"

// Use only plaintext admitted by the existing retention budget. If that
// budget is full, lookup keeps its ordinary iterator and allocation lifetime.
func parentTreeFinder(previous data.TreeNodeIterator, parent *parentTreeBytes) *data.TreeFinder {
	if parent != nil {
		if finder, err := data.NewParentTreeFinder(parent.payload); err == nil {
			return finder
		}
	}
	return data.NewTreeFinder(previous)
}
