package data

import "bytes"

// PreparationSize reports a small, completely framed next JSON object without
// interpreting it. It is only an allocation admission hint: Next remains the
// authority for syntax, fields and errors. Unknown or large values stay on the
// synchronous path. The scan allocates nothing and never changes decoder state.
func (r *TreeNodeReader) PreparationSize(limit int) (int, bool) {
	if r.ended || limit <= 0 {
		return 0, false
	}
	start := int(r.tree.dec.InputOffset())
	for start < len(r.payload) {
		switch r.payload[start] {
		case ' ', '\t', '\r', '\n', ',':
			start++
		default:
			goto value
		}
	}
value:
	if start >= len(r.payload) || r.payload[start] != '{' {
		return 0, false
	}
	depth := 0
	end := min(len(r.payload), start+limit)
	for i := start; i < end; i++ {
		c := r.payload[i]
		switch c {
		case '"':
			// Strings dominate ordinary node input (notably content IDs).
			// Skip their interiors with the byte search instead of branching
			// over every byte. An escaped quote does not close the string.
			for {
				n := bytes.IndexByte(r.payload[i+1:end], '"')
				if n < 0 {
					return 0, false
				}
				i += n + 1
				escapes := 0
				for j := i - 1; j >= start && r.payload[j] == '\\'; j-- {
					escapes++
				}
				if escapes%2 == 0 {
					break
				}
			}
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				return i - start + 1, true
			}
		}
	}
	return 0, false
}
