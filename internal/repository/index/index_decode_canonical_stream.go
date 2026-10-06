package index

import (
	"bufio"
	"bytes"
	"io"
	"math"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// DecodeCanonicalIndexStream validates canonical tokens while filling a
// private index. It removes the second scan/decompression at the price of table
// growth. Publication still waits for terminal stream validation.
func DecodeCanonicalIndexStream(open func() (io.ReadCloser, error), id restic.ID) (*Index, bool) {
	return decodeCanonicalIndexStreamLimited(open, id, 16*1024*1024*1024)
}

func decodeCanonicalIndexStreamLimited(open func() (io.ReadCloser, error), id restic.ID, maxBytes int64) (*Index, bool) {
	rd, err := open()
	if err != nil {
		return nil, false
	}
	defer rd.Close()
	limited := &io.LimitedReader{R: rd, N: maxBytes + 1}
	p := canonicalStreamParser{rd: bufio.NewReaderSize(limited, 64*1024)}
	idx := NewIndex()
	if !p.document(idx) || limited.N == 0 {
		return nil, false
	}
	idx.ids = append(idx.ids, id)
	idx.final = true
	return idx, true
}

type canonicalStreamParser struct {
	rd     *bufio.Reader
	buf    []byte
	pos    int
	packs  int
	counts [restic.NumBlobTypes]int
}

// Tokens are at most 66 bytes. Retain one borrowed window and refill only at a
// token boundary; this avoids a reader method for every digit and punctuation.
func (p *canonicalStreamParser) available(n int) bool {
	if len(p.buf)-p.pos >= n {
		return true
	}
	_, _ = p.rd.Discard(p.pos)
	p.pos = 0
	p.buf, _ = p.rd.Peek(64 * 1024)
	return len(p.buf) >= n
}

func (p *canonicalStreamParser) take(s string) bool {
	if !p.available(len(s)) || !bytes.HasPrefix(p.buf[p.pos:], []byte(s)) {
		return false
	}
	p.pos += len(s)
	return true
}

func (p *canonicalStreamParser) id(dst *restic.ID) bool {
	if !p.take(`"`) || !p.available(65) {
		return false
	}
	b := p.buf[p.pos : p.pos+65]
	if b[64] != '"' || !canonicalHexID(b[:64], dst) {
		return false
	}
	p.pos += 65
	return true
}

func (p *canonicalStreamParser) number() (uint, bool) {
	var n uint64
	count := 0
	first := byte(0)
	for {
		if !p.available(1) {
			return 0, false
		}
		ch := p.buf[p.pos]
		if ch < '0' || ch > '9' {
			break
		}
		if count == 0 {
			first = ch
		}
		count++
		if count > 10 || count > 1 && first == '0' {
			return 0, false
		}
		n = n*10 + uint64(ch-'0')
		if n > math.MaxUint32 {
			return 0, false
		}
		p.pos++
	}
	return uint(n), count > 0
}

func (p *canonicalStreamParser) blob(idx *Index, packIndex uint32) bool {
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

func (p *canonicalStreamParser) pack(idx *Index) bool {
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

func (p *canonicalStreamParser) document(idx *Index) bool {
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
	_, _ = p.rd.Discard(p.pos)
	p.pos = 0
	p.buf = nil
	var tail [8192]byte
	_, err := io.CopyBuffer(indexJSONWhitespaceWriter{}, p.rd, tail[:])
	return err == nil
}
