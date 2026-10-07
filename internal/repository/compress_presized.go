package repository

// encodeBlob returns exactly what r.getZstdEncoder().EncodeAll(data, nil)
// returns.
//
// EncodeAll appends to the destination it is given. For input below 1 MiB it
// starts from a buffer as large as the input; for larger input it starts from
// an empty one and appends block by block, so the destination is reallocated
// and copied again and again while it grows. For a blob that does not
// compress this is several times the blob's size in allocations and copies
// (and one more reallocation for a smaller blob, whose output is a few bytes
// longer than its input). Since the decision under CompressionAuto is taken
// from the encoder's output for the whole blob, every such blob goes through
// here.
//
// From presizedEncodeMinSize on, the destination is therefore allocated once,
// with room for the largest output the encoder can produce for that input
// size. Only the capacity of the destination differs: the encoder, its
// options and the bytes it produces are the same.
func (r *Repository) encodeBlob(data []byte) []byte {
	enc := r.getZstdEncoder()
	if len(data) < presizedEncodeMinSize {
		return enc.EncodeAll(data, nil)
	}
	return enc.EncodeAll(data, make([]byte, 0, enc.MaxEncodedSize(len(data))))
}

// presizedEncodeMinSize is the smallest blob that is stored uncompressed
// when it does not compress (autoCompressionMinRawSize): from here on a blob
// may be incompressible media data in bulk.
const presizedEncodeMinSize = autoCompressionMinRawSize
