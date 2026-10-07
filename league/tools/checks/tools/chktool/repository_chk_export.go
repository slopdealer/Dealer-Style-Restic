package repository

import "github.com/restic/restic/internal/restic"

// ChkInternal exists only in the scratch copy of the stock tree that chktool is built from (release checks): it
// hands out the unexported view of the repository that may write index files.
func ChkInternal(r *Repository) restic.Unpacked[restic.FileType] {
	return &internalRepository{r}
}
