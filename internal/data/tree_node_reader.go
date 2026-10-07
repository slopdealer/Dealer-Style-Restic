package data

import (
	"bytes"
	"context"
	"encoding/json"
	"io"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/restic"
)

// TreeNodeReader exposes the same direct decoder as LoadTree to a single
// sequential owner, without adapting a push iterator through a coroutine.
// Next and Close must not be called concurrently.
type TreeNodeReader struct {
	payload []byte
	tree    treeIterator
	ended   bool
}

func LoadTreeNodeReader(ctx context.Context, loader restic.BlobLoader, content restic.ID) (*TreeNodeReader, error) {
	payload, err := loader.LoadBlob(ctx, restic.BlobHandle{Type: restic.TreeBlob, ID: content}, nil)
	if err != nil {
		return nil, err
	}
	r := &TreeNodeReader{payload: payload, tree: treeIterator{dec: *json.NewDecoder(bytes.NewReader(payload))}}
	if err := r.tree.init(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *TreeNodeReader) Next() (NodeOrError, bool) {
	if r.ended {
		return NodeOrError{}, false
	}
	node, err := nextParentNode(&r.tree, r.payload)
	if errors.Is(err, io.EOF) {
		r.Close()
		return NodeOrError{}, false
	}
	if err != nil {
		r.Close()
	}
	return NodeOrError{Node: node, Error: err}, true
}

func (r *TreeNodeReader) Close() {
	r.ended = true
	r.payload = nil
	r.tree = treeIterator{}
}
