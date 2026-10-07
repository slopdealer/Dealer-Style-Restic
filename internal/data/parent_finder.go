package data

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/restic/restic/internal/errors"
)

// NewParentTreeFinder reads authenticated immutable parent bytes without a
// push-to-pull iterator boundary. It uses the existing parent interpretation
// and the existing ordered lookup. Closing does not parse the remaining tail.
func NewParentTreeFinder(payload []byte) (*TreeFinder, error) {
	t := &treeIterator{dec: *json.NewDecoder(bytes.NewReader(payload))}
	if err := t.init(); err != nil {
		return nil, err
	}
	done := false
	next := func() (NodeOrError, bool) {
		if done {
			return NodeOrError{}, false
		}
		n, err := nextParentNode(t, payload)
		if err != nil {
			done = true
			if errors.Is(err, io.EOF) {
				return NodeOrError{}, false
			}
		}
		return NodeOrError{Node: n, Error: err}, true
	}
	stop := func() {
		done = true
		// Drop only this reader's references. Tree saving owns any retained
		// plaintext independently, including completion after cancellation.
		t = nil
		payload = nil
	}
	return &TreeFinder{next: next, stop: stop}, nil
}
