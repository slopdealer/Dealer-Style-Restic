package index

import (
	"bytes"
	"math"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// decodeIndexEager keeps the ordinary decoder as the compatibility authority.
// Only the canonical serialization is interpreted directly. Validation settles
// the entire document before lookup storage exists, including storage overflow.
// Fallback therefore holds no partial index or typed pack/blob representation.
func decodeIndexEager(buf []byte, id restic.ID) (*Index, error) {
	// Preserve the ordinary decoder's instrumentation sites and debug filters.
	if debug.IsEnabled() {
		return DecodeIndex(buf, id)
	}
	if idx, ok := decodeCanonicalIndex(buf, id); ok {
		return idx, nil
	}
	return DecodeIndex(buf, id)
}

func decodeCanonicalIndex(buf []byte, id restic.ID) (*Index, bool) {
	p := canonicalIndexParser{buf: buf}
	if !p.document(nil) {
		return nil, false
	}
	idx := NewIndex()
	if p.packs > 0 {
		idx.packs = make(restic.IDs, 0, p.packs)
	}
	for typ, count := range p.counts {
		idx.Preallocate(restic.BlobType(typ), count)
	}
	// The same immutable plaintext is scanned again. No offsets table or typed
	// records survive the first pass; IDs are decoded only during emission.
	p = canonicalIndexParser{buf: buf}
	if !p.document(idx) {
		return nil, false
	}
	idx.ids = append(idx.ids, id)
	idx.final = true
	return idx, true
}

type canonicalIndexParser struct {
	buf    []byte
	pos    int
	packs  int
	counts [restic.NumBlobTypes]int
}

func (p *canonicalIndexParser) take(s string) bool {
	if !bytes.HasPrefix(p.buf[p.pos:], []byte(s)) {
		return false
	}
	p.pos += len(s)
	return true
}

func (p *canonicalIndexParser) id(dst *restic.ID) bool {
	if !p.take(`"`) || len(p.buf)-p.pos < 65 {
		return false
	}
	s := p.buf[p.pos : p.pos+64]
	if !canonicalHexID(s, dst) {
		return false
	}
	p.pos += 64
	if !p.take(`"`) {
		return false
	}
	return true
}

func (p *canonicalIndexParser) number() (uint, bool) {
	start := p.pos
	var n uint64
	for p.pos < len(p.buf) {
		ch := p.buf[p.pos]
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + uint64(ch-'0')
		if n > math.MaxUint32 {
			return 0, false
		}
		p.pos++
	}
	if p.pos == start || p.pos-start > 1 && p.buf[start] == '0' {
		return 0, false
	}
	return uint(n), true
}

func (p *canonicalIndexParser) blob(idx *Index, packIndex uint32) bool {
	if !p.take(`{"id":`) {
		return false
	}
	var blob pack.Blob
	var dst *restic.ID
	if idx != nil {
		dst = &blob.ID
	}
	if !p.id(dst) || !p.take(`,"type":`) {
		return false
	}
	if p.take(`"data"`) {
		blob.Type = restic.DataBlob
	} else if p.take(`"tree"`) {
		blob.Type = restic.TreeBlob
	} else {
		return false
	}
	if !p.take(`,"offset":`) {
		return false
	}
	var ok bool
	if blob.Offset, ok = p.number(); !ok || !p.take(`,"length":`) {
		return false
	}
	if blob.Length, ok = p.number(); !ok {
		return false
	}
	if p.take(`,"uncompressed_length":`) {
		if blob.UncompressedLength, ok = p.number(); !ok {
			return false
		}
	}
	if !p.take(`}`) {
		return false
	}
	p.counts[blob.Type]++
	if idx != nil {
		idx.store(packIndex, blob)
	}
	return true
}

func (p *canonicalIndexParser) pack(idx *Index) bool {
	if !p.take(`{"id":`) {
		return false
	}
	var id restic.ID
	var dst *restic.ID
	if idx != nil {
		dst = &id
	}
	if !p.id(dst) || !p.take(`,"blobs":[`) {
		return false
	}
	p.packs++
	if uint64(p.packs) > math.MaxUint32 {
		return false
	}
	var packIndex uint32
	if idx != nil {
		packIndex = idx.addToPacks(id)
	}
	if !p.take(`]`) {
		for {
			if !p.blob(idx, packIndex) {
				return false
			}
			if p.take(`]`) {
				break
			}
			if !p.take(`,`) {
				return false
			}
		}
	}
	return p.take(`}`)
}

func (p *canonicalIndexParser) document(idx *Index) bool {
	if !p.take(`{"packs":[`) {
		return false
	}
	if !p.take(`]`) {
		for {
			if !p.pack(idx) {
				return false
			}
			if p.take(`]`) {
				break
			}
			if !p.take(`,`) {
				return false
			}
		}
	}
	if !p.take(`}`) {
		return false
	}
	for _, ch := range p.buf[p.pos:] {
		if ch != ' ' && ch != '\n' && ch != '\r' && ch != '\t' {
			return false
		}
	}
	return true
}
