package data

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strconv"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/restic"
)

// LoadParentTree keeps the authenticated plaintext available for error replay.
// Successful parent nodes are interpreted once, without the nested unmarshal.
func LoadParentTree(ctx context.Context, loader restic.BlobLoader, content restic.ID) (TreeNodeIterator, error) {
	iterator, _, err := LoadParentTreeWithPayload(ctx, loader, content)
	return iterator, err
}

// LoadParentTreeWithPayload exposes the same authenticated plaintext used by
// the iterator. Callers must treat it as immutable and bound any extra retention.
func LoadParentTreeWithPayload(ctx context.Context, loader restic.BlobLoader, content restic.ID) (TreeNodeIterator, []byte, error) {
	payload, err := loader.LoadBlob(ctx, restic.BlobHandle{Type: restic.TreeBlob, ID: content}, nil)
	if err != nil {
		return nil, nil, err
	}
	iterator, err := newParentTreeIterator(payload)
	if err != nil {
		return nil, nil, err
	}
	return iterator, payload, nil
}

func newParentTreeIterator(payload []byte) (TreeNodeIterator, error) {
	return newDirectTreeIterator(payload)
}

func newDirectTreeIterator(payload []byte) (TreeNodeIterator, error) {
	t := &treeIterator{dec: *json.NewDecoder(bytes.NewReader(payload))}
	if err := t.init(); err != nil {
		return nil, err
	}
	return func(yield func(NodeOrError) bool) {
		if t.started {
			panic("tree iterator is single use only")
		}
		t.started = true
		for {
			n, err := nextParentNode(t, payload)
			if err != nil && errors.Is(err, io.EOF) {
				return
			}
			if !yield(NodeOrError{Node: n, Error: err}) || err != nil {
				return
			}
		}
	}, nil
}

func nextParentNode(t *treeIterator, payload []byte) (*Node, error) {
	if !t.dec.More() {
		// Keep the ordinary framing and unknown-field interpretation.
		return t.next()
	}
	start := t.dec.InputOffset()
	type nodeJSON Node
	n := new(Node)
	err := t.dec.Decode((*nodeJSON)(n))
	if err != nil {
		// Scanner failures originate outside Node.UnmarshalJSON. Preserve their
		// wrapper and absolute offsets. A bytes.Reader has no transport errors.
		if _, syntax := err.(*json.SyntaxError); syntax || err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, err
		}
		// Decode consumed this complete JSON value. Replaying the original
		// method preserves semantic-error precedence, types and local offsets.
		end := t.dec.InputOffset()
		raw := bytes.TrimLeft(payload[start:end], " \t\r\n,")
		ordinary := new(Node)
		if err := ordinary.UnmarshalJSON(raw); err != nil {
			return nil, err
		}
		return ordinary, nil
	}
	// The JSON decoder has already removed the outer JSON escaping. Names
	// written by Node.MarshalJSON contain a second layer of escaping, which
	// Node.UnmarshalJSON removes with strconv.Unquote for every name. The call
	// is skipped only for names it returns unchanged; every other name gets
	// it, so a name is rejected here exactly when stock rejects it.
	if !nodeNameIsPlain(n.Name) {
		n.Name, err = strconv.Unquote(`"` + n.Name + `"`)
		if err != nil {
			return nil, errors.Wrap(err, "Unquote")
		}
	}
	if n.LinkTargetRaw != nil {
		n.LinkTarget = string(n.LinkTargetRaw)
		n.LinkTargetRaw = nil
	}
	return n, nil
}
