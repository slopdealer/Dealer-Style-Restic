package pack

import (
	"encoding/binary"

	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/crypto"
)

// ListOwnedTail lists a captured pack tail. The caller must exclusively own
// tail: successful authentication decrypts its header in place. In particular,
// the complete pack hash must have been checked before calling this function.
// Entries own their values and do not retain references to tail.
func ListOwnedTail(k *crypto.Key, tail []byte) (Blobs, uint32, error) {
	buf, err := openOwnedTail(k, tail)
	if err != nil {
		return nil, 0, err
	}
	hdrSize := headerLengthSize + uint32(len(buf)) + crypto.Extension
	entries := make(Blobs, 0, uint(len(buf))/plainEntrySize)
	pos := uint(0)
	for len(buf) > 0 {
		entry, size, err := parseHeaderEntry(buf)
		if err != nil {
			return nil, 0, err
		}
		entry.Offset = pos
		entries = append(entries, entry)
		pos += entry.Length
		buf = buf[size:]
	}
	return entries, hdrSize, nil
}

// openOwnedTail preserves readHeader's checks and their order, but uses the
// bytes already captured by the caller rather than allocating a read buffer.
func openOwnedTail(k *crypto.Key, tail []byte) ([]byte, error) {
	size := int64(len(tail))
	if size < int64(minFileSize) {
		return nil, errors.Wrap(InvalidFileError{Message: "file is too short"}, "readHeader")
	}
	hlen := binary.LittleEndian.Uint32(tail[len(tail)-headerLengthSize:])
	var err error
	switch {
	case hlen == 0:
		err = InvalidFileError{Message: "header length is zero"}
	case hlen < crypto.Extension:
		err = InvalidFileError{Message: "header length is too short"}
	case int64(hlen) > size-int64(headerLengthSize):
		err = InvalidFileError{Message: "header is larger than file"}
	case int64(hlen) > MaxHeaderSize-int64(headerLengthSize):
		err = InvalidFileError{Message: "header is larger than maxHeaderSize"}
	}
	if err != nil {
		return nil, errors.Wrap(err, "readHeader")
	}
	buf := tail[len(tail)-headerLengthSize-int(hlen) : len(tail)-headerLengthSize]
	if len(buf) < crypto.CiphertextLength(0) {
		return nil, errors.New("invalid header, too short")
	}
	nonce, ciphertext := buf[:k.NonceSize()], buf[k.NonceSize():]
	return k.Open(ciphertext[:0], nonce, ciphertext, nil)
}
