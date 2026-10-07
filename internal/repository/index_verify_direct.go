package repository

import (
	"fmt"
	"io"

	"github.com/restic/restic/internal/errors"
)

// verifyIndexEncoding borrows each encoder write only for the duration of Write.
// The second encode feeds comparison directly, avoiding the pipe rendezvous and
// the copy of expected bytes. Actual bytes still use bounded scratch storage.
func verifyIndexEncoding(actual io.Reader, encode func(io.Writer) error) error {
	cmp := streamCompareWriter{actual: actual, buf: make([]byte, 32*1024), equal: true}
	encodeErr := encode(&cmp)
	// A decoder failure can propagate through the encoder as a writer error.
	// Retain its original diagnostic before considering encoder failures.
	if cmp.err != nil {
		return fmt.Errorf("decompression failed: %w", cmp.err)
	}
	if encodeErr != nil {
		return fmt.Errorf("encoding failed: %w", encodeErr)
	}
	// A mismatch is not permission to skip checksum or terminal frame errors.
	extra, err := io.Copy(io.Discard, actual)
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}
	if extra != 0 || !cmp.equal {
		return errors.New("data mismatch")
	}
	return nil
}
