package index

import "github.com/restic/restic/internal/restic"

// Invalid bytes carry all high bits. OR-ing the decoded nibbles validates a
// complete canonical ID without a branch per digit. Uppercase stays unsupported
// here so the ordinary JSON decoder remains its compatibility authority.
var canonicalHexNibbles = func() [256]byte {
	var table [256]byte
	for i := range table {
		table[i] = 255
	}
	for i := byte(0); i < 10; i++ {
		table['0'+i] = i
	}
	for i := byte(0); i < 6; i++ {
		table['a'+i] = 10 + i
	}
	return table
}()

// The parser has already checked the extent; this helper also keeps its own
// length contract. Both validation and emission still reject invalid digits.
func canonicalHexID(src []byte, dst *restic.ID) bool {
	if len(src) != 64 {
		return false
	}
	var digits byte
	if dst == nil {
		for _, ch := range src {
			digits |= canonicalHexNibbles[ch]
		}
	} else {
		for i := range dst {
			hi, lo := canonicalHexNibbles[src[2*i]], canonicalHexNibbles[src[2*i+1]]
			digits |= hi | lo
			dst[i] = hi<<4 | lo
		}
	}
	return digits < 16
}
