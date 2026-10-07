package restorer

import (
	"context"
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/restic"
)

func TestPrefetchBudgetCountsHiddenAttributeTail(t *testing.T) {
	// encoding/json reuses a slice when a duplicate field supplies a shorter
	// array, leaving the prior second attribute in the backing slice's tail.
	value := base64.StdEncoding.EncodeToString(make([]byte, 8<<20))
	payload := []byte(fmt.Sprintf(`{"nodes":[{"name":"file","type":"file","content":null,"extended_attributes":[{"name":"keep","value":"YQ=="},{"name":"old","value":"%s"}],"extended_attributes":[{"name":"keep","value":"YQ=="}]}]}`, value))
	load := func(context.Context, restic.ID) (data.TreeNodeIterator, error) {
		// Exercise the real metadata decoder, including duplicate-field reuse.
		return data.LoadMetadataTree(context.Background(), hiddenAttributeTreePayload{payload}, restic.ID{})
	}
	tree, err := load(context.Background(), restic.ID{})
	if err != nil {
		t.Fatal(err)
	}
	var node *data.Node
	for item := range tree {
		if item.Error != nil {
			t.Fatal(item.Error)
		}
		node = item.Node
	}
	if node == nil {
		t.Fatal("empty fixture")
	}
	attrs := node.ExtendedAttributes
	if len(attrs) != 1 || cap(attrs) < 2 || len(attrs[:cap(attrs)][1].Value) != 8<<20 {
		t.Fatal("JSON decoder did not retain the hidden attribute")
	}
	visibleEstimate := estimatePreparedNodeBytes(node)
	trueEstimate := estimateCarriedNodeBytes(node)
	if trueEstimate <= maxPreparedTreeBytes {
		t.Fatal("fixture must exceed the prefetch budget")
	}
	p := &restoreTreePrefetch{load: load}
	prepared, terminal := p.prepare(context.Background(), restic.ID{})
	if terminal || prepared.loadErr != nil || prepared.iteratorErr != nil {
		t.Fatalf("invalid test fixture: %+v terminal=%v", prepared, terminal)
	}
	if !prepared.tooLarge {
		t.Fatalf("prefetch accepted an 8 MiB hidden attribute against its %d-byte budget; visible estimate=%d, complete estimate including slack=%d", maxPreparedTreeBytes, visibleEstimate, trueEstimate)
	}
}

type hiddenAttributeTreePayload struct{ payload []byte }

func (r hiddenAttributeTreePayload) LoadBlob(context.Context, restic.BlobHandle, []byte) ([]byte, error) {
	return r.payload, nil
}
