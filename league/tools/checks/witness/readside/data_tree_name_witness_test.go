package data

// WITNESS (readside review, cross-area: internal/data/parent_tree.go:87-95).
//
// What it shows: the candidate's direct tree decoder (LoadTree ->
// newDirectTreeIterator -> nextParentNode, also used by LoadTreeNodeReader for
// ls and restore) only runs strconv.Unquote on a node name that contains a
// backslash. Stock runs Node.UnmarshalJSON, which always unquotes. A name
// that, after JSON decoding, holds a raw double quote or a raw newline and no
// backslash is therefore REJECTED by stock ("Unquote: invalid syntax") and
// ACCEPTED by the candidate. restic itself never writes such a name
// (strconv.Quote escapes both), so this only concerns trees written by other
// tools or damaged-but-rehashed trees: stock `check`, `ls`, `restore` report
// an error for the tree, the candidate does not.
//
// The stock decoder is still in the candidate tree as NewTreeNodeIterator
// (treeIterator.next), so both arms run in one binary.
//
// OS: any. Run in the candidate tree:
//   go test -run TestWitnessTreeNameUnquoteLeniency -count=1 ./internal/data
// Expected on the candidate: FAIL (the divergence is reported by t.Errorf).

import (
	"bytes"
	"context"
	"testing"

	"github.com/restic/restic/internal/restic"
)

type witnessTreeLoader map[restic.ID][]byte

func (l witnessTreeLoader) LoadBlob(_ context.Context, h restic.BlobHandle, _ []byte) ([]byte, error) {
	return l[h.ID], nil
}

func witnessDecode(t *testing.T, it TreeNodeIterator, err error) (names []string, firstErr error) {
	t.Helper()
	if err != nil {
		return nil, err
	}
	for item := range it {
		if item.Error != nil {
			return names, item.Error
		}
		names = append(names, item.Node.Name)
	}
	return names, nil
}

func TestWitnessTreeNameUnquoteLeniency(t *testing.T) {
	for _, tc := range []struct {
		desc string
		tree string
	}{
		// JSON string "a\"b" decodes to a"b : a raw quote, no backslash left
		{"raw double quote in name", `{"nodes":[{"name":"a\"b","type":"file","content":[]}]}` + "\n"},
		// JSON string "a\nb" decodes to a<LF>b : a raw newline, no backslash left
		{"raw newline in name", `{"nodes":[{"name":"a\nb","type":"file","content":[]}]}` + "\n"},
		// control: what restic writes for the name a"b (strconv.Quote, then JSON)
		{"control: restic-written quote", `{"nodes":[{"name":"a\\\"b","type":"file","content":[]}]}` + "\n"},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			payload := []byte(tc.tree)
			id := restic.Hash(payload)

			stockIt, stockErr := NewTreeNodeIterator(bytes.NewReader(payload))
			stockNames, stockErr := witnessDecode(t, stockIt, stockErr)

			candIt, candErr := LoadTree(context.Background(), witnessTreeLoader{id: payload}, id)
			candNames, candErr := witnessDecode(t, candIt, candErr)

			rd, rdErr := LoadTreeNodeReader(context.Background(), witnessTreeLoader{id: payload}, id)
			var readerNames []string
			var readerErr = rdErr
			if rdErr == nil {
				for {
					item, ok := rd.Next()
					if !ok {
						break
					}
					if item.Error != nil {
						readerErr = item.Error
						break
					}
					readerNames = append(readerNames, item.Node.Name)
				}
			}

			t.Logf("stock decoder:        names=%q err=%v", stockNames, stockErr)
			t.Logf("candidate LoadTree:   names=%q err=%v", candNames, candErr)
			t.Logf("candidate NodeReader: names=%q err=%v", readerNames, readerErr)

			if (stockErr == nil) != (candErr == nil) {
				t.Errorf("DIVERGENCE: stock err=%v, candidate LoadTree err=%v (names %q)", stockErr, candErr, candNames)
			}
			if (stockErr == nil) != (readerErr == nil) {
				t.Errorf("DIVERGENCE: stock err=%v, candidate TreeNodeReader err=%v (names %q)", stockErr, readerErr, readerNames)
			}
		})
	}
}
