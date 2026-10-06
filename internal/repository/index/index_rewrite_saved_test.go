package index

import (
	"sync"
	"testing"

	"github.com/restic/restic/internal/restic"
)

func TestRewriteSavedConcurrentSettlement(t *testing.T) {
	var saved rewriteSavedIndexes
	obsolete := restic.NewIDSet()
	var wg sync.WaitGroup
	for i := range 128 {
		id := restic.ID{byte(i), 1}
		obsolete.Insert(id)
		wg.Add(1)
		go func() {
			defer wg.Done()
			saved.record(id)
			saved.record(id)
		}()
	}
	wg.Wait()
	remove := restic.ID{1, 2}
	obsolete.Insert(remove)
	saved.preserveOutputs(obsolete)
	if len(obsolete) != 1 || !obsolete.Has(remove) {
		t.Fatal("successful outputs removed, or obsolete input retained")
	}
}
