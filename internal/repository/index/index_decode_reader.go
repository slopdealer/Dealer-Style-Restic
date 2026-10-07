package index

import (
	"encoding/json"
	"errors"
	"io"
	"math"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// DecodeIndexReader attempts to decode a single-field packs document without
// retaining all its plaintext or typed packs. On false, the caller must use
// DecodeIndex on the complete plaintext to preserve diagnostics and precedence.
// No partial index is returned, including on a read error after the document.
func DecodeIndexReader(rd io.Reader, id restic.ID) (*Index, bool) {
	return decodeIndexReaderLimited(rd, id, 16*1024*1024*1024)
}

func decodeIndexReaderLimited(rd io.Reader, id restic.ID, maxBytes uint64) (*Index, bool) {
	guard := &indexJSONGuardReader{rd: rd, maxBytes: maxBytes}
	dec := json.NewDecoder(guard)
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, false
	}
	if tok, err := dec.Token(); err != nil || tok != "packs" {
		return nil, false
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, false
	}
	idx := NewIndex()
	var scratch packJSON
	for dec.More() {
		clear(scratch.Blobs[:cap(scratch.Blobs)])
		scratch = packJSON{Blobs: scratch.Blobs[:0]}
		if err := dec.Decode(&scratch); err != nil {
			return nil, false
		}
		for _, blob := range scratch.Blobs {
			if blob.Type >= restic.NumBlobTypes || blob.Offset > math.MaxUint32 || blob.Length > math.MaxUint32 || blob.UncompressedLength > math.MaxUint32 {
				return nil, false
			}
		}
		// Defer any pack-count overflow panic until eager parsing has resolved
		// errors later in the complete document.
		if uint64(len(idx.packs)) >= math.MaxUint32 {
			return nil, false
		}
		packID := idx.addToPacks(scratch.ID)
		for _, blob := range scratch.Blobs {
			idx.store(packID, pack.Blob{
				BlobHandle: restic.BlobHandle{Type: blob.Type, ID: blob.ID},
				Offset:     blob.Offset, Length: blob.Length,
				UncompressedLength: blob.UncompressedLength,
			})
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return nil, false
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return nil, false
	}
	// Decode(&extra) could retain an arbitrarily long whitespace trailer. Check
	// buffered and remaining bytes with bounded scratch, preserving terminal
	// reader errors already observed by Decoder during read-ahead.
	var tail [8192]byte
	_, err := io.CopyBuffer(indexJSONWhitespaceWriter{}, io.MultiReader(dec.Buffered(), guard), tail[:])
	if err != nil || guard.failed != nil {
		return nil, false
	}
	idx.ids = append(idx.ids, id)
	idx.final = true
	return idx, true
}

var errIndexJSONStreamGuard = errors.New("index JSON stream exceeds eager limits")
var errIndexJSONTrailing = errors.New("non-whitespace after index JSON")

// Eager JSON limits nesting over the complete document. Enforce that limit
// independently of whether this Go build's Decoder counts enclosing tokens
// during per-pack decoding. Count all output bytes as well, matching the
// DecodeAll cumulative output ceiling.
type indexJSONGuardReader struct {
	rd              io.Reader
	maxBytes, bytes uint64
	depth           int
	quoted, escaped bool
	failed          error
}

func (r *indexJSONGuardReader) Read(p []byte) (int, error) {
	if r.failed != nil {
		return 0, r.failed
	}
	n, err := r.rd.Read(p)
	if uint64(n) > r.maxBytes-r.bytes {
		r.failed = errIndexJSONStreamGuard
		return n, r.failed
	}
	r.bytes += uint64(n)
	for _, ch := range p[:n] {
		if r.quoted {
			if r.escaped {
				r.escaped = false
			} else if ch == '\\' {
				r.escaped = true
			} else if ch == '"' {
				r.quoted = false
			}
		} else {
			switch ch {
			case '"':
				r.quoted = true
			case '{', '[':
				r.depth++
				if r.depth > 10000 {
					r.failed = errIndexJSONStreamGuard
					return n, r.failed
				}
			case '}', ']':
				r.depth--
			}
		}
	}
	if err != nil && err != io.EOF {
		r.failed = err
	}
	return n, err
}

type indexJSONWhitespaceWriter struct{}

func (indexJSONWhitespaceWriter) Write(p []byte) (int, error) {
	for i, ch := range p {
		if ch != ' ' && ch != '\n' && ch != '\r' && ch != '\t' {
			return i, errIndexJSONTrailing
		}
	}
	return len(p), nil
}
