package index

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestPackGroupMembershipMatchesValues(t *testing.T) {
	idx := NewIndex()
	for i := 0; i < 19; i++ {
		var id restic.ID
		id[0] = byte(i)
		id[1] = 1
		var blobs pack.Blobs
		for j := 0; j < 23; j++ {
			var bh restic.BlobHandle
			bh.Type = restic.DataBlob
			if j%3 == 0 {
				bh.Type = restic.TreeBlob
			}
			bh.ID[0] = byte(j)
			bh.ID[1] = 1
			blobs = append(blobs, pack.Blob{BlobHandle: bh, Offset: uint(j * 100), Length: 100, UncompressedLength: uint(j)})
		}
		// Includes identical blob handles in different packs, compressed blobs,
		// mixed types and exact duplicate entries in each per-file index.
		blobs = append(blobs, blobs[0])
		idx.StorePack(id, blobs)
	}
	fromValues := make(map[restic.ID]int)
	for pb := range idx.Values() {
		fromValues[pb.PackID()]++
	}
	fromGroups := make(map[restic.ID]int)
	for pbs := range idx.EachByPackForCheck(context.Background(), restic.NewIDSet()) {
		if _, seen := fromGroups[pbs.PackID]; seen {
			t.Fatal("pack emitted more than once")
		}
		fromGroups[pbs.PackID] = len(pbs.Blobs)
	}
	if !reflect.DeepEqual(fromValues, fromGroups) {
		t.Fatalf("group membership differs: values=%v groups=%v", fromValues, fromGroups)
	}
}

func TestEachByPackCancelledGatherReleasesIndex(t *testing.T) {
	idx := NewIndex()
	var id restic.ID
	id[0] = 1
	idx.StorePack(id, pack.Blobs{{BlobHandle: restic.BlobHandle{ID: id, Type: restic.DataBlob}, Length: 100}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for range idx.EachByPackForCheck(ctx, restic.NewIDSet()) {
		t.Fatal("cancelled gather published a pack")
	}
	done := make(chan struct{})
	go func() {
		idx.StorePack(id, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled gather retained index read lock")
	}
}
