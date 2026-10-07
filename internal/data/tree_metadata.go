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

// metadataContent validates every content ID without retaining the slice.
// The outer JSON decoder already validates framing and string escaping. The
// fast path still calls the ordinary ID parser for every ID. Unusual values use
// the ordinary slice decoder; semantic errors are replayed at the node boundary.
type metadataContent struct{}

func (*metadataContent) UnmarshalJSON(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		return nil
	}
	ordinary := func() error {
		var ids restic.IDs
		return json.Unmarshal(raw, &ids)
	}
	if len(raw) < 2 || raw[0] != '[' {
		return ordinary()
	}
	i := 1
	skipSpace := func() {
		for i < len(raw) && (raw[i] == ' ' || raw[i] == '\t' || raw[i] == '\r' || raw[i] == '\n') {
			i++
		}
	}
	skipSpace()
	if i == len(raw)-1 && raw[i] == ']' {
		return nil
	}
	for {
		if i+66 > len(raw) || raw[i] != '"' || raw[i+65] != '"' {
			return ordinary()
		}
		var id restic.ID
		if err := id.UnmarshalJSON(raw[i : i+66]); err != nil {
			return err
		}
		i += 66
		skipSpace()
		if i == len(raw)-1 && raw[i] == ']' {
			return nil
		}
		if i >= len(raw) || raw[i] != ',' {
			return ordinary()
		}
		i++
		skipSpace()
	}
}

type metadataNodeBase Node
type metadataNodeJSON struct {
	metadataNodeBase
	Content metadataContent `json:"content"`
}

// LoadMetadataTree authenticates and decodes a tree with ordinary field and
// error interpretation, but returns no file content IDs. It is only for a
// metadata traversal: planning and verification must keep using LoadTree.
func LoadMetadataTree(ctx context.Context, loader restic.BlobLoader, content restic.ID) (TreeNodeIterator, error) {
	payload, err := loader.LoadBlob(ctx, restic.BlobHandle{Type: restic.TreeBlob, ID: content}, nil)
	if err != nil {
		return nil, err
	}
	return newMetadataTreeIterator(payload)
}

func newMetadataTreeIterator(payload []byte) (TreeNodeIterator, error) {
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
			n, err := nextMetadataNode(t, payload)
			if err != nil && errors.Is(err, io.EOF) {
				return
			}
			if !yield(NodeOrError{Node: n, Error: err}) || err != nil {
				return
			}
		}
	}, nil
}

func nextMetadataNode(t *treeIterator, payload []byte) (*Node, error) {
	if !t.dec.More() {
		return t.next()
	}
	start := t.dec.InputOffset()
	wire := new(metadataNodeJSON)
	n := (*Node)(&wire.metadataNodeBase)
	err := t.dec.Decode(wire)
	if err != nil {
		if _, syntax := err.(*json.SyntaxError); syntax || err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, err
		}
		end := t.dec.InputOffset()
		raw := bytes.TrimLeft(payload[start:end], " \t\r\n,")
		ordinary := new(Node)
		if err := ordinary.UnmarshalJSON(raw); err != nil {
			return nil, err
		}
		ordinary.Content = nil
		return ordinary, nil
	}
	// As in nextParentNode: strconv.Unquote is skipped only for names it
	// returns unchanged, so a name is rejected exactly when stock rejects it.
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
