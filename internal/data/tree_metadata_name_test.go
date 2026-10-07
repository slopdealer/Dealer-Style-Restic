package data

import (
	"context"
	"math/rand"
	"strings"
	"testing"

	"github.com/restic/restic/internal/restic"
)

// LoadMetadataTree is a third decoder for tree documents (restore's second
// pass). Like the direct decoder it may skip strconv.Unquote only for names
// that the call returns unchanged; for every other name it has to return what
// stock's Node.UnmarshalJSON returns, error text included. The trees are the
// adversarial set of TestTreeDecodersAgreeWithStockOnAdversarialNames.

func decodeTreeMetadata(payload []byte) decodedTree {
	return collectTree(LoadMetadataTree(context.Background(), payloadLoader(payload), restic.Hash(payload)))
}

func compareMetadataDecoder(t *testing.T, payload []byte) (rejected bool) {
	t.Helper()
	stock := decodeTreeStock(payload)
	if got := decodeTreeMetadata(payload); !equalDecodedTrees(stock, got) {
		t.Fatalf("tree %q:\nstock            names=%q err=%q\nLoadMetadataTree names=%q err=%q", payload, stock.names, stock.err, got.names, got.err)
	}
	return stock.err != ""
}

func TestMetadataTreeAgreesWithStockOnAdversarialNames(t *testing.T) {
	// a raw quote and a raw newline in a name: stock rejects both
	for _, name := range []string{`a\"b`, `a\nb`} {
		if !compareMetadataDecoder(t, treeWithNames(name)) {
			t.Errorf("stock accepts the name %s: the test does not test what it should", name)
		}
	}
	if compareMetadataDecoder(t, treeWithNames(`a\\\"b`)) {
		t.Error("stock rejects a quote as restic writes it")
	}

	accepted, rejected := 0, 0
	count := func(wasRejected bool) {
		if wasRejected {
			rejected++
		} else {
			accepted++
		}
	}
	for _, a := range jsonNamePieces {
		count(compareMetadataDecoder(t, treeWithNames(a)))
		for _, b := range jsonNamePieces {
			count(compareMetadataDecoder(t, treeWithNames(a+b)))
			count(compareMetadataDecoder(t, treeWithNames(a+"x"+b)))
		}
	}
	rnd := rand.New(rand.NewSource(20261007))
	for i := 0; i < 30000; i++ {
		names := make([]string, 1+rnd.Intn(3))
		for j := range names {
			var name strings.Builder
			for n := rnd.Intn(7); n > 0; n-- {
				name.WriteString(jsonNamePieces[rnd.Intn(len(jsonNamePieces))])
			}
			names[j] = name.String()
		}
		count(compareMetadataDecoder(t, treeWithNames(names...)))
	}
	if accepted < 1000 || rejected < 1000 {
		t.Fatalf("one-sided sample: %d trees accepted, %d rejected", accepted, rejected)
	}
	t.Logf("%d trees accepted and %d rejected by stock, LoadMetadataTree agrees on all", accepted, rejected)
}
