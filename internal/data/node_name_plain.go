package data

import "unicode/utf8"

// nodeNameIsPlain reports whether strconv.Unquote(`"` + name + `"`), which
// Node.UnmarshalJSON applies to every node name, returns name unchanged and
// without an error, so that the call can be skipped.
//
// That holds when the name has no backslash (no escape sequence to decode),
// no double quote (it would end the quoted string early: a syntax error), no
// byte below 0x20 (Unquote rejects a raw newline; the other control bytes go
// through Unquote as well, so that nothing depends on which of them it
// accepts) and is valid UTF-8 (Unquote replaces an invalid byte by U+FFFD).
// Every other name must go through strconv.Unquote.
func nodeNameIsPlain(name string) bool {
	ascii := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		if c < 0x20 || c == '"' || c == '\\' {
			return false
		}
		if c >= utf8.RuneSelf {
			ascii = false
		}
	}
	return ascii || utf8.ValidString(name)
}
