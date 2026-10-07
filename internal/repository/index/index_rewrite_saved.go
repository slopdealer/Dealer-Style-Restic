package index

import (
	"sync"

	"github.com/restic/restic/internal/restic"
)

// rewriteSavedIndexes bridges the concurrent save stage and the later delete
// stage. A content-addressed output can have the same ID as an obsolete input.
// Once all savers have joined, deletion must preserve every successful output.
type rewriteSavedIndexes struct {
	m sync.Mutex
	ids restic.IDSet
}

func (s *rewriteSavedIndexes) record(id restic.ID) {
	s.m.Lock()
	defer s.m.Unlock()
	if s.ids == nil { s.ids = restic.NewIDSet() }
	s.ids.Insert(id)
}

// Called only after the save workers have joined successfully.
func (s *rewriteSavedIndexes) preserveOutputs(obsolete restic.IDSet) {
	for id := range s.ids { obsolete.Delete(id) }
}
