package restic

import "context"

// SmallRestoreBlobLimit bounds the needed blobs assigned to the extra restore
// worker. Ciphertext has a separate allowance for encryption framing.
const SmallRestoreBlobLimit = 1 << 20
const SmallRestoreCiphertextLimit = SmallRestoreBlobLimit + 64

// Medium uncompressed blobs borrow ciphertext storage. Compressed blobs also
// own decoder output and scratch, so keep their existing smaller admission bound.
const MediumRestoreBlobLimit = 3 << 20

func BoundedRestoreBlob(plaintext, ciphertext uint, compressed bool) bool {
	limit := uint(SmallRestoreBlobLimit)
	if !compressed {
		limit = MediumRestoreBlobLimit
	}
	return plaintext <= limit && ciphertext <= limit+64
}

type smallRestoreLoadKey struct{}

// WithSmallRestoreLoad marks a stream with separate range-buffer ownership.
// acquire reserves ordinary-worker allocation capacity for exceptional recovery.
// Wrappers still receive and control the ordinary LoadBlobsFromPack call.
func WithSmallRestoreLoad(ctx context.Context, acquire func(context.Context) (func(), error)) context.Context {
	return context.WithValue(ctx, smallRestoreLoadKey{}, acquire)
}

func SmallRestoreLoad(ctx context.Context) (func(context.Context) (func(), error), bool) {
	acquire, ok := ctx.Value(smallRestoreLoadKey{}).(func(context.Context) (func(), error))
	return acquire, ok
}
