package pack

import (
	"iter"

	"github.com/restic/restic/internal/repository/crypto"
)

// ListOwnedTailEntries authenticates and validates an exclusively owned pack
// tail before returning any entries. Unlike ListOwnedTail, it does not build a
// second representation of the complete header. The caller must verify the
// complete pack hash first, and must keep tail unchanged until iteration ends.
// Yielded Blob values do not retain references to tail.
func ListOwnedTailEntries(k *crypto.Key, tail []byte) (iter.Seq[Blob], uint32, error) {
	buf, err := openOwnedTail(k, tail)
	if err != nil {
		return nil, 0, err
	}
	// Validate the entire document before publishing even its first entry.
	// A malformed late entry must take precedence over membership diagnostics.
	for remaining := buf; len(remaining) > 0; {
		_, size, err := parseHeaderEntry(remaining)
		if err != nil {
			return nil, 0, err
		}
		remaining = remaining[size:]
	}
	hdrSize := headerLengthSize + uint32(len(buf)) + crypto.Extension
	return func(yield func(Blob) bool) {
		pos := uint(0)
		for remaining := buf; len(remaining) > 0; {
			// The exclusive input was completely validated above.
			entry, size, _ := parseHeaderEntry(remaining)
			entry.Offset = pos
			if !yield(entry) {
				return
			}
			pos += entry.Length
			remaining = remaining[size:]
		}
	}, hdrSize, nil
}
