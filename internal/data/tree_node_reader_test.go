package data

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/restic/restic/internal/restic"
)

type readerTestLoader []byte

func (b readerTestLoader) LoadBlob(context.Context, restic.BlobHandle, []byte) ([]byte, error) {
	return b, nil
}

func TestTreeNodeReaderMatchesLoadTree(t *testing.T) {
	for _, payload := range []string{
		`{"nodes":[]}`,
		`{"before":{"nested":1},"nodes":[{"name":"a","type":"file"},{"name":"b","type":"dir"}],"after":[]}`,
		`{"nodes":[{"name":"a\\\\b","type":"symlink","linktarget_raw":"AP8="}]}`,
		`{"nodes":[{"name":"a","type":"file"},{"name":1}]}`,
		`{"nodes":[{"name":"a","type":"file"},{"name":`,
		`{"nodes":[]`,
		`{"nodes":[],"tail":`,
		`{"nodes":false}`,
		`[]`,
	} {
		t.Run(payload, func(t *testing.T) {
			loader := readerTestLoader(payload)
			ordinary, oe := LoadTree(context.Background(), loader, restic.ID{})
			reader, re := LoadTreeNodeReader(context.Background(), loader, restic.ID{})
			if fmt.Sprintf("%T:%v", oe, oe) != fmt.Sprintf("%T:%v", re, re) {
				t.Fatalf("initial error differs: %v / %v", oe, re)
			}
			if oe != nil {
				return
			}
			defer reader.Close()
			for want := range ordinary {
				got, ok := reader.Next()
				if !ok || !reflect.DeepEqual(got.Node, want.Node) || fmt.Sprintf("%T:%v", got.Error, got.Error) != fmt.Sprintf("%T:%v", want.Error, want.Error) {
					t.Fatalf("node/error differs: %#v / %#v", got, want)
				}
			}
			if _, ok := reader.Next(); ok {
				t.Fatal("reader yielded beyond ordinary termination")
			}
			if reader.payload != nil || !reader.ended || !reflect.DeepEqual(reader.tree, treeIterator{}) {
				t.Fatal("reader did not release terminal state")
			}
		})
	}
}

func TestTreeNodeReaderEarlyClose(t *testing.T) {
	r, err := LoadTreeNodeReader(context.Background(), readerTestLoader(`{"nodes":[{"name":"a"},{"name":"b"}]}`), restic.ID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := r.Next(); !ok {
		t.Fatal("missing first node")
	}
	r.Close()
	r.Close()
	if _, ok := r.Next(); ok || r.payload != nil || !r.ended {
		t.Fatal("closed reader retains or yields state")
	}
}
