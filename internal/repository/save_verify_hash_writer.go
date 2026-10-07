package repository

import (
	"hash"
	"io"
)

// saveVerifyHashWriter borrows decoder-owned bytes for the duration of Write.
// Zero remaining bytes is not EOF: empty writes and decoder terminal work
// remain legal. Excess output declines the streaming route; its caller then
// runs the ordinary decoder to obtain the inherited diagnostic.
type saveVerifyHashWriter struct {
	h         hash.Hash
	remaining int64
	exceeded  bool
}

func (w *saveVerifyHashWriter) Write(p []byte) (int, error) {
	if int64(len(p)) > w.remaining {
		w.exceeded = true
		return 0, io.ErrShortWrite
	}
	n, err := w.h.Write(p)
	w.remaining -= int64(n)
	return n, err
}
