package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"hash"
	"io"
	"math"
	"runtime"
	"sync"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/chunker"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/cache"
	"github.com/restic/restic/internal/backend/dryrun"
	"github.com/restic/restic/internal/backend/sema"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"

	"golang.org/x/sync/errgroup"
)

const MinPackSize = 4 * 1024 * 1024
const DefaultPackSize = 16 * 1024 * 1024
const MaxPackSize = 128 * 1024 * 1024

// CompressionAuto samples large data blobs before encoding the whole blob.
// The windows are spread across the input so that one local region doesn't
// decide whether the complete blob is compressed.
const (
	autoCompressionMinSampledSize = 512 * 1024
	autoCompressionSampleWindow   = 2 * 1024
	autoCompressionSampleWindows  = 16
	autoCompressionSampleSize     = autoCompressionSampleWindow * autoCompressionSampleWindows
)

type compressionSample struct {
	input   []byte
	encoded []byte
}

var compressionSamplePool = sync.Pool{New: func() any {
	return &compressionSample{
		input:   make([]byte, autoCompressionSampleSize),
		encoded: make([]byte, 0, autoCompressionSampleSize+1024),
	}
}}

// Repository is used to access a repository in a backend.
type Repository struct {
	be    backend.Backend
	cfg   restic.Config
	key   *crypto.Key
	keyID restic.ID
	idx   *index.MasterIndex
	cache *cache.Cache

	opts Options

	packerWg    *errgroup.Group
	mainWg      *errgroup.Group
	blobSaver   *sync.WaitGroup
	uploader    *packerUploader
	treePM      *packerManager
	dataPM      *packerManager
	packerCount int

	allocEnc sync.Once
	allocDec sync.Once
	enc      *zstd.Encoder
	dec      *zstd.Decoder

	allocRestoreDec sync.Once
	restoreDec      *zstd.Decoder

	// One idle limited decoder is retained; checked-out decoders have a single
	// synchronous stream owner. Concurrent restores allocate independently.
	smallRestoreDecoderMu sync.Mutex
	smallRestoreDecoder   *zstd.Decoder
	smallRestoreClosed    bool

	zeroChunkOnce sync.Once
	zeroChunkID   restic.ID
}

// internalRepository allows using SaveUnpacked and RemoveUnpacked with all FileTypes
type internalRepository struct {
	*Repository
}

type Options struct {
	Compression   CompressionMode
	PackSize      uint
	NoExtraVerify bool
}

// CompressionMode configures if data should be compressed.
type CompressionMode uint

// Constants for the different compression levels.
const (
	CompressionAuto    CompressionMode = 0
	CompressionOff     CompressionMode = 1
	CompressionMax     CompressionMode = 2
	CompressionFastest CompressionMode = 3
	CompressionBetter  CompressionMode = 4
	CompressionInvalid CompressionMode = 5
)

// Set implements the method needed for pflag command flag parsing.
func (c *CompressionMode) Set(s string) error {
	switch s {
	case "auto":
		*c = CompressionAuto
	case "off":
		*c = CompressionOff
	case "max":
		*c = CompressionMax
	case "fastest":
		*c = CompressionFastest
	case "better":
		*c = CompressionBetter
	default:
		*c = CompressionInvalid
		return fmt.Errorf("invalid compression mode %q, must be one of (auto|off|fastest|better|max)", s)
	}

	return nil
}

func (c *CompressionMode) String() string {
	switch *c {
	case CompressionAuto:
		return "auto"
	case CompressionOff:
		return "off"
	case CompressionMax:
		return "max"
	case CompressionFastest:
		return "fastest"
	case CompressionBetter:
		return "better"
	default:
		return "invalid"
	}

}
func (c *CompressionMode) Type() string {
	return "mode"
}

// New returns a new repository with backend be.
func New(be backend.Backend, opts Options) (*Repository, error) {
	if opts.Compression == CompressionInvalid {
		return nil, errors.New("invalid compression mode")
	}

	if opts.PackSize == 0 {
		opts.PackSize = DefaultPackSize
	}
	if opts.PackSize > MaxPackSize {
		return nil, fmt.Errorf("pack size larger than limit of %v MiB", MaxPackSize/1024/1024)
	} else if opts.PackSize < MinPackSize {
		return nil, fmt.Errorf("pack size smaller than minimum of %v MiB", MinPackSize/1024/1024)
	}

	repo := &Repository{
		be:          be,
		opts:        opts,
		idx:         index.NewMasterIndex(),
		packerCount: defaultPackerCount,
	}

	return repo, nil
}

// setConfig assigns the given config and updates the repository parameters accordingly
func (r *Repository) setConfig(cfg restic.Config) {
	r.cfg = cfg
}

// Config returns the repository configuration.
func (r *Repository) Config() restic.Config {
	return r.cfg
}

// PackSize return the target size of a pack file when uploading
func (r *Repository) PackSize() uint {
	return r.opts.PackSize
}

// UseCache replaces the backend with the wrapped cache.
func (r *Repository) UseCache(c *cache.Cache, errorLog func(string, ...any)) {
	if c == nil {
		return
	}
	debug.Log("using cache")
	r.cache = c
	r.be = c.Wrap(r.be, errorLog)
}

func (r *Repository) Cache() *cache.Cache {
	return r.cache
}

// SetDryRun sets the repo backend into dry-run mode.
func (r *Repository) SetDryRun() {
	r.be = dryrun.New(r.be)
}

func (r *Repository) Checker() *Checker {
	return newChecker(r)
}

// LoadUnpacked loads and decrypts the file with the given type and ID.
func (r *Repository) LoadUnpacked(ctx context.Context, t restic.FileType, id restic.ID) ([]byte, error) {
	debug.Log("load %v with id %v", t, id)

	if t == restic.ConfigFile {
		id = restic.ID{}
	}

	buf, err := r.LoadRaw(ctx, t, id)
	if err != nil {
		return nil, err
	}

	if len(buf) < crypto.CiphertextLength(0) {
		return nil, fmt.Errorf("invalid data in %v file, too short", t)
	}

	nonce, ciphertext := buf[:r.key.NonceSize()], buf[r.key.NonceSize():]
	plaintext, err := r.key.Open(ciphertext[:0], nonce, ciphertext, nil)
	if err != nil {
		return nil, err
	}
	if t != restic.ConfigFile {
		return r.decompressUnpacked(plaintext)
	}

	return plaintext, nil
}

type haver interface {
	Has(backend.Handle) bool
}

// sortCachedPacksFirst moves all cached pack files to the front of blobs.
func sortCachedPacksFirst(cache haver, blobs []*pack.PackedBlob) {
	if cache == nil {
		return
	}

	// no need to sort a list with one element
	if len(blobs) == 1 {
		return
	}

	cached := blobs[:0]
	noncached := make([]*pack.PackedBlob, 0, len(blobs)/2)

	for _, blob := range blobs {
		if cache.Has(backend.Handle{Type: backend.PackFile, Name: blob.PackID().String()}) {
			cached = append(cached, blob)
			continue
		}
		noncached = append(noncached, blob)
	}

	copy(blobs[len(cached):], noncached)
}

// LoadBlob loads a blob from the repository.
// It may use all of buf[:cap(buf)] as scratch space.
func (r *Repository) LoadBlob(ctx context.Context, bh restic.BlobHandle, buf []byte) ([]byte, error) {
	debug.Log("load %v (buf len %v, cap %d)", bh, len(buf), cap(buf))

	// lookup packs
	blobs := r.idx.Lookup(bh)
	if len(blobs) == 0 {
		debug.Log("id %v not found in index", bh.ID)
		return nil, errors.Errorf("id %v not found in repository", bh.ID)
	}

	// try cached pack files first
	sortCachedPacksFirst(r.cache, blobs)

	buf, err := r.loadBlob(ctx, blobs, buf)
	if err != nil {
		if r.cache != nil {
			for _, blob := range blobs {
				h := backend.Handle{Type: backend.PackFile, Name: blob.PackID().String(), IsMetadata: blob.Blob.Type.IsMetadata()}
				// ignore errors as there's not much we can do here
				_ = r.cache.Forget(h)
			}
		}

		buf, err = r.loadBlob(ctx, blobs, buf)
	}
	return buf, err
}

func (r *Repository) loadBlob(ctx context.Context, blobs []*pack.PackedBlob, buf []byte) ([]byte, error) {
	var lastError error
	for _, blob := range blobs {
		debug.Log("blob %v found: %v", blob.Handle(), blob)
		// load blob from pack
		h := backend.Handle{Type: backend.PackFile, Name: blob.PackID().String(), IsMetadata: blob.Blob.Type.IsMetadata()}

		switch {
		case cap(buf) < int(blob.Blob.Length):
			buf = make([]byte, blob.Blob.Length)
		case len(buf) != int(blob.Blob.Length):
			buf = buf[:blob.Blob.Length]
		}

		_, err := backend.ReadAt(ctx, r.be, h, int64(blob.Blob.Offset), buf)
		if err != nil {
			debug.Log("error loading blob %v: %v", blob, err)
			lastError = err
			continue
		}

		it := newPackBlobIterator(blob.PackID(), newByteReader(buf), blob.Blob.Offset, pack.Blobs{blob.Blob}, r.key, r.getZstdDecoder())
		pbv, err := it.Next()

		if err == nil {
			err = pbv.Err
		}
		if err != nil {
			debug.Log("error decoding blob %v: %v", blob, err)
			lastError = err
			continue
		}

		plaintext := pbv.Plaintext
		if len(plaintext) > cap(buf) {
			return plaintext, nil
		}
		// move decrypted data to the start of the buffer
		buf = buf[:len(plaintext)]
		copy(buf, plaintext)
		return buf, nil
	}

	if lastError != nil {
		return nil, lastError
	}

	return nil, errors.Errorf("loading %v from %v packs failed", blobs[0].Handle(), len(blobs))
}

func (r *Repository) getZstdEncoder() *zstd.Encoder {
	r.allocEnc.Do(func() {
		opts := []zstd.EOption{
			// Set the compression level configured.
			zstd.WithEncoderLevel(r.zstdEncoderLevel()),
			// Disable CRC, we have enough checks in place, makes the
			// compressed data four bytes shorter.
			zstd.WithEncoderCRC(false),
			// Set a window of 512kbyte, so we have good lookbehind for usual
			// blob sizes.
			zstd.WithWindowSize(512 * 1024),
		}

		enc, err := zstd.NewWriter(nil, opts...)
		if err != nil {
			panic(err)
		}
		r.enc = enc
	})
	return r.enc
}

func (r *Repository) zstdEncoderLevel() zstd.EncoderLevel {
	switch r.opts.Compression {
	case CompressionFastest:
		return zstd.SpeedFastest
	case CompressionBetter:
		return zstd.SpeedBetterCompression
	case CompressionMax:
		return zstd.SpeedBestCompression
	default:
		return zstd.SpeedDefault
	}
}

func (r *Repository) getZstdDecoder() *zstd.Decoder {
	r.allocDec.Do(func() {
		opts := []zstd.DOption{
			// Use all available cores.
			zstd.WithDecoderConcurrency(0),
			// Limit the maximum decompressed memory. Set to a very high,
			// conservative value.
			zstd.WithDecoderMaxMemory(16 * 1024 * 1024 * 1024),
		}

		dec, err := zstd.NewReader(nil, opts...)
		if err != nil {
			panic(err)
		}
		r.dec = dec
	})
	return r.dec
}

// saveAndEncrypt encrypts data and stores it to the backend as type t. If data
// is small enough, it will be packed together with other small blobs. The
// caller must ensure that the id matches the data. Returned is the size data
// occupies in the repo (compressed or not, including the encryption overhead).
func (r *Repository) saveAndEncrypt(ctx context.Context, t restic.BlobType, data []byte, id restic.ID) (size int, err error) {
	debug.Log("save id %v (%v, %d bytes)", id, t, len(data))

	uncompressedLength := 0
	if r.cfg.Version > 1 {
		// we have a repo v2, so compression is available. if the user opts to
		// not compress, we won't compress any data, but everything else is
		// compressed.
		// uncompressedLength != 0 is used to indicate compressed data. Thus, a zero-sized blob
		// cannot be compressed. This special case is only relevant for tests, normal operation does not
		// generate zero-sized blobs.
		if len(data) > 0 && (r.opts.Compression != CompressionOff || t != restic.DataBlob) {
			useCompression := true
			sampledAutoData := t == restic.DataBlob && r.opts.Compression == CompressionAuto && len(data) >= autoCompressionMinSampledSize
			if sampledAutoData {
				useCompression = r.autoDataBlobLooksCompressible(data)
			}

			if useCompression {
				compressed := r.getZstdEncoder().EncodeAll(data, nil)
				if sampledAutoData && len(compressed)+4 >= len(data) {
					// A compressed pack entry carries a four-byte uncompressed
					// length. Keep the original bytes when that header and payload
					// would not save space.
					uncompressedLength = 0
				} else {
					uncompressedLength = len(data)
					data = compressed
				}
			}
		}
	}

	nonce := crypto.NewRandomNonce()

	ciphertext := make([]byte, 0, crypto.CiphertextLength(len(data)))
	ciphertext = append(ciphertext, nonce...)

	// encrypt blob
	ciphertext = r.key.Seal(ciphertext, nonce, data, nil)

	if err := r.verifyCiphertext(ciphertext, uncompressedLength, id); err != nil {
		//nolint:revive,staticcheck // ignore linter warnings about error message spelling
		return 0, fmt.Errorf("Detected data corruption while saving blob %v: %w\nCorrupted blobs are either caused by hardware issues or software bugs. Please open an issue at https://github.com/restic/restic/issues/new/choose for further troubleshooting.", id, err)
	}

	// find suitable packer and add blob
	var pm *packerManager

	switch t {
	case restic.TreeBlob:
		pm = r.treePM
	case restic.DataBlob:
		pm = r.dataPM
	default:
		panic(fmt.Sprintf("invalid type: %v", t))
	}

	return pm.SaveBlob(ctx, t, id, ciphertext, uncompressedLength)
}

// autoDataBlobLooksCompressible estimates whether CompressionAuto should pay
// to encode a large data blob. False positives only cost the full encoding;
// false negatives store the original bytes, which remain readable by stock.
func (r *Repository) autoDataBlobLooksCompressible(data []byte) bool {
	sample := compressionSamplePool.Get().(*compressionSample)
	defer func() {
		clear(sample.input)
		clear(sample.encoded[:cap(sample.encoded)])
		sample.encoded = sample.encoded[:0]
		if cap(sample.encoded) <= autoCompressionSampleSize+1024 {
			compressionSamplePool.Put(sample)
		}
	}()

	lastStart := len(data) - autoCompressionSampleWindow
	for i := 0; i < autoCompressionSampleWindows; i++ {
		start := i * lastStart / (autoCompressionSampleWindows - 1)
		copy(sample.input[i*autoCompressionSampleWindow:(i+1)*autoCompressionSampleWindow], data[start:start+autoCompressionSampleWindow])
	}

	encoded := r.getZstdEncoder().EncodeAll(sample.input, sample.encoded[:0])
	sample.encoded = encoded
	// Require at least a one-percent sample reduction to justify the full pass.
	return len(encoded)*100 < len(sample.input)*99
}

func (r *Repository) verifyCiphertext(buf []byte, uncompressedLength int, id restic.ID) error {
	if r.opts.NoExtraVerify {
		return nil
	}
	if uncompressedLength == 0 && len(buf) >= streamSaveVerifyMinSize {
		return r.verifyRawCiphertextStream(buf, id)
	}

	nonce, ciphertext := buf[:r.key.NonceSize()], buf[r.key.NonceSize():]
	plaintext, err := r.key.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	if uncompressedLength != 0 {
		if uncompressedLength >= streamedSaveVerifyMinSize {
			if handled, err := verifySavedCompressedHash(plaintext, id); handled {
				return err
			}
		}
		// DecodeAll will allocate a slice if it is not large enough since it
		// knows the decompressed size (because we're using EncodeAll)
		plaintext, err = r.getZstdDecoder().DecodeAll(plaintext, nil)
		if err != nil {
			return fmt.Errorf("decompression failed: %w", err)
		}
	}
	if !restic.Hash(plaintext).Equal(id) {
		return errors.New("hash mismatch")
	}

	return nil
}

func (r *Repository) compressUnpacked(p []byte) ([]byte, error) {
	// compression is only available starting from version 2
	if r.cfg.Version < 2 {
		return p, nil
	}

	// version byte
	out := []byte{2}
	out = r.getZstdEncoder().EncodeAll(p, out)
	return out, nil
}

func (r *Repository) compressLockUnpacked(p []byte) ([]byte, error) {
	// Compression is only available starting from version 2.
	if r.cfg.Version < 2 {
		return p, nil
	}

	// Keep the v2 Zstandard envelope without constructing the repository's
	// general-purpose encoder for this small transient record.
	out := []byte{2}
	out = appendRawZstdFrame(out, p)
	return out, nil
}

const maxZstdRawBlockSize = 128 * 1024

// appendRawZstdFrame appends a valid single-segment Zstandard frame containing
// raw blocks. It is used for lock JSON to avoid allocating an encoder with a
// large workspace for a record of only a few hundred bytes.
func appendRawZstdFrame(dst, data []byte) []byte {
	dst = append(dst, 0x28, 0xb5, 0x2f, 0xfd) // Zstandard magic number.

	size := uint64(len(data))
	switch {
	case size <= 255:
		// Single segment, one-byte frame content size.
		dst = append(dst, 0x20, byte(size))
	case size <= 65791:
		// Two-byte content size stores the value minus 256.
		dst = append(dst, 0x60)
		dst = appendZstdLittleEndian(dst, size-256, 2)
	case size <= math.MaxUint32:
		dst = append(dst, 0xa0)
		dst = appendZstdLittleEndian(dst, size, 4)
	default:
		dst = append(dst, 0xe0)
		dst = appendZstdLittleEndian(dst, size, 8)
	}

	for offset := 0; ; {
		blockSize := min(maxZstdRawBlockSize, len(data)-offset)
		lastBlock := offset+blockSize == len(data)
		blockHeader := uint32(blockSize) << 3
		if lastBlock {
			blockHeader |= 1
		}
		dst = append(dst, byte(blockHeader), byte(blockHeader>>8), byte(blockHeader>>16))
		dst = append(dst, data[offset:offset+blockSize]...)
		offset += blockSize
		if lastBlock {
			return dst
		}
	}
}

func appendZstdLittleEndian(dst []byte, value uint64, size int) []byte {
	for i := 0; i < size; i++ {
		dst = append(dst, byte(value))
		value >>= 8
	}
	return dst
}

func (r *Repository) decompressUnpacked(p []byte) ([]byte, error) {
	// compression is only available starting from version 2
	if r.cfg.Version < 2 {
		return p, nil
	}

	if len(p) == 0 {
		// too short for version header
		return p, nil
	}
	if p[0] == '[' || p[0] == '{' {
		// probably raw JSON
		return p, nil
	}
	// version
	if p[0] != 2 {
		return nil, errors.New("not supported encoding format")
	}

	return r.getZstdDecoder().DecodeAll(p[1:], nil)
}

// SaveUnpacked encrypts data and stores it in the backend. Returned is the
// storage hash.
func (r *Repository) SaveUnpacked(ctx context.Context, t restic.WriteableFileType, buf []byte) (id restic.ID, err error) {
	return r.saveUnpacked(ctx, t.ToFileType(), buf)
}

func (r *internalRepository) SaveUnpacked(ctx context.Context, t restic.FileType, buf []byte) (id restic.ID, err error) {
	return r.Repository.saveUnpacked(ctx, t, buf)
}

// SaveIndexStream stores an index encoded by encode. This optional path lets
// Index.SaveIndex avoid retaining the complete JSON representation while the
// repository creates the compressed payload.
func (r *internalRepository) SaveIndexStream(ctx context.Context, encode func(io.Writer) error) (restic.ID, error) {
	return r.Repository.saveIndexStream(ctx, encode)
}

func (r *Repository) saveIndexStream(ctx context.Context, encode func(io.Writer) error) (restic.ID, error) {
	// Older repositories store unpacked data without compression. Keep their
	// established representation and use the ordinary save path.
	if r.cfg.Version < 2 {
		var buf bytes.Buffer
		if err := encode(&buf); err != nil {
			return restic.ID{}, err
		}
		return r.saveUnpacked(ctx, restic.IndexFile, buf.Bytes())
	}

	var payload bytes.Buffer
	payload.WriteByte(2)
	enc, err := zstd.NewWriter(&payload,
		zstd.WithEncoderLevel(r.zstdEncoderLevel()),
		zstd.WithEncoderCRC(false),
		zstd.WithWindowSize(512*1024),
		// A streamed writer owns its workspace for the duration of this save.
		// Keep concurrent prune saves from multiplying encoder workers.
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		return restic.ID{}, err
	}
	encodeErr := encode(enc)
	closeErr := enc.Close()
	if encodeErr != nil {
		return restic.ID{}, encodeErr
	}
	if closeErr != nil {
		return restic.ID{}, closeErr
	}

	return r.saveCompressedIndex(ctx, payload.Bytes(), encode)
}

func (r *Repository) saveCompressedIndex(ctx context.Context, payload []byte, encode func(io.Writer) error) (restic.ID, error) {
	ciphertext := crypto.NewBlobBuffer(len(payload))
	ciphertext = ciphertext[:0]
	nonce := crypto.NewRandomNonce()
	ciphertext = append(ciphertext, nonce...)
	ciphertext = r.key.Seal(ciphertext, nonce, payload, nil)

	if !r.opts.NoExtraVerify {
		err := r.verifyCompressedIndexStream(ciphertext, encode)
		if err != nil {
			//nolint:revive,staticcheck // ignore linter warnings about error message spelling
			return restic.ID{}, fmt.Errorf("Detected data corruption while saving file of type %v: %w\nCorrupted data is either caused by hardware issues or software bugs. Please open an issue at https://github.com/restic/restic/issues/new/choose for further troubleshooting.", restic.IndexFile, err)
		}
	}

	id := restic.Hash(ciphertext)
	h := backend.Handle{Type: backend.FileType(restic.IndexFile), Name: id.String()}
	if err := r.be.Save(ctx, h, backend.NewByteReader(ciphertext, r.be.Hasher())); err != nil {
		debug.Log("error saving blob %v: %v", h, err)
		return restic.ID{}, err
	}

	debug.Log("blob %v saved", h)
	return id, nil
}

func (r *Repository) saveUnpacked(ctx context.Context, t restic.FileType, buf []byte) (id restic.ID, err error) {
	p := buf
	if t != restic.ConfigFile {
		if t == restic.LockFile {
			p, err = r.compressLockUnpacked(p)
		} else {
			p, err = r.compressUnpacked(p)
		}
		if err != nil {
			return restic.ID{}, err
		}
	}

	ciphertext := crypto.NewBlobBuffer(len(p))
	ciphertext = ciphertext[:0]
	nonce := crypto.NewRandomNonce()
	ciphertext = append(ciphertext, nonce...)

	ciphertext = r.key.Seal(ciphertext, nonce, p, nil)

	if err := r.verifyUnpacked(ciphertext, t, buf); err != nil {
		//nolint:revive,staticcheck // ignore linter warnings about error message spelling
		return restic.ID{}, fmt.Errorf("Detected data corruption while saving file of type %v: %w\nCorrupted data is either caused by hardware issues or software bugs. Please open an issue at https://github.com/restic/restic/issues/new/choose for further troubleshooting.", t, err)
	}

	if t == restic.ConfigFile {
		id = restic.ID{}
	} else {
		id = restic.Hash(ciphertext)
	}
	h := backend.Handle{Type: backend.FileType(t), Name: id.String()}

	err = r.be.Save(ctx, h, backend.NewByteReader(ciphertext, r.be.Hasher()))
	if err != nil {
		debug.Log("error saving blob %v: %v", h, err)
		return restic.ID{}, err
	}

	debug.Log("blob %v saved", h)
	return id, nil
}

func (r *Repository) verifyUnpacked(buf []byte, t restic.FileType, expected []byte) error {
	if r.opts.NoExtraVerify {
		return nil
	}

	nonce, ciphertext := buf[:r.key.NonceSize()], buf[r.key.NonceSize():]
	plaintext, err := r.key.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	// Index saves already hold the encoded JSON while verification is running.
	// Compare the decompressed stream directly so verification does not allocate
	// another full copy of that JSON. Keep the existing path for raw and legacy
	// index encodings, and for every other unpacked file type.
	if t == restic.IndexFile && r.cfg.Version >= 2 && len(plaintext) > 0 && plaintext[0] == 2 {
		return verifyCompressedIndex(plaintext[1:], expected)
	}
	if t != restic.ConfigFile {
		plaintext, err = r.decompressUnpacked(plaintext)
		if err != nil {
			return fmt.Errorf("decompression failed: %w", err)
		}
	}

	if !bytes.Equal(plaintext, expected) {
		return errors.New("data mismatch")
	}
	return nil
}

// verifyCompressedIndex compares a compressed index against the JSON passed to
// SaveUnpacked without retaining a second copy of the decompressed JSON.
func verifyCompressedIndex(compressed, expected []byte) error {
	dec, err := zstd.NewReader(bytes.NewReader(compressed),
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(16*1024*1024*1024),
	)
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}
	defer dec.Close()

	equal, err := compareStreamBytes(dec, expected)
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}
	if !equal {
		return errors.New("data mismatch")
	}
	return nil
}

// verifyCompressedIndexStream checks that decrypting and decompressing an
// encoded index produces exactly the same bytes as a second streaming encode.
// Comparison keeps the expected JSON bounded in memory, and the decoder is read
// through EOF even after a mismatch so frame errors are not hidden.
func (r *Repository) verifyCompressedIndexStream(buf []byte, encode func(io.Writer) error) error {
	nonce, ciphertext := buf[:r.key.NonceSize()], buf[r.key.NonceSize():]
	plaintext, err := r.key.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}
	if len(plaintext) == 0 || plaintext[0] != 2 {
		return errors.New("not supported encoding format")
	}
	return verifyCompressedIndexStream(plaintext[1:], encode)
}

func verifyCompressedIndexStream(compressed []byte, encode func(io.Writer) error) error {
	dec, err := zstd.NewReader(bytes.NewReader(compressed),
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(16*1024*1024*1024),
	)
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}
	defer dec.Close()

	return verifyIndexEncoding(dec, encode)
}

type streamCompareWriter struct {
	actual io.Reader
	buf    []byte
	equal  bool
	ended  bool
	err    error
}

func (w *streamCompareWriter) Write(expected []byte) (int, error) {
	length := len(expected)
	for len(expected) > 0 {
		if w.ended {
			w.equal = false
			return length, nil
		}
		n := min(len(expected), len(w.buf))
		got, err := io.ReadFull(w.actual, w.buf[:n])
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				w.ended = true
				w.equal = false
				return length, nil
			}
			w.err = err
			return 0, err
		}
		if !bytes.Equal(w.buf[:got], expected[:n]) {
			w.equal = false
		}
		expected = expected[n:]
	}
	return length, nil
}

func compareStreamBytes(src io.Reader, expected []byte) (bool, error) {
	cmp := byteStreamComparator{expected: expected, equal: true}
	if _, err := io.Copy(&cmp, src); err != nil {
		return false, err
	}
	return cmp.equal && cmp.offset == len(expected), nil
}

// byteStreamComparator checks all decoded bytes, but keeps reading after a
// mismatch so decoder errors are still reported before verification succeeds.
type byteStreamComparator struct {
	expected []byte
	offset   int
	equal    bool
}

func (c *byteStreamComparator) Write(p []byte) (int, error) {
	remaining := len(c.expected) - c.offset
	if len(p) > remaining {
		c.equal = false
		c.offset = len(c.expected)
		return len(p), nil
	}
	if !bytes.Equal(p, c.expected[c.offset:c.offset+len(p)]) {
		c.equal = false
	}
	c.offset += len(p)
	return len(p), nil
}

func (r *Repository) RemoveUnpacked(ctx context.Context, t restic.WriteableFileType, id restic.ID) error {
	return r.removeUnpacked(ctx, t.ToFileType(), id)
}

func (r *internalRepository) RemoveUnpacked(ctx context.Context, t restic.FileType, id restic.ID) error {
	return r.Repository.removeUnpacked(ctx, t, id)
}

func (r *Repository) removeUnpacked(ctx context.Context, t restic.FileType, id restic.ID) error {
	return r.be.Remove(ctx, backend.Handle{Type: backend.FileType(t), Name: id.String()})
}

func (r *Repository) WithBlobUploader(ctx context.Context, fn func(ctx context.Context, uploader restic.BlobSaverWithAsync) error) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	wg, ctx := errgroup.WithContext(ctx)
	// Pack uploader, command callback, descriptor-only completion lane, its
	// error publisher and CPU-bound blob savers. Payload admission is unchanged.
	wg.SetLimit(4 + runtime.GOMAXPROCS(0))
	r.mainWg = wg
	r.startPackUploader(ctx, wg)
	// blob saver are spawned on demand, use wait group to keep track of them
	r.blobSaver = &sync.WaitGroup{}
	saver := &blobSaverRepo{repo: r, knownCompletions: newKnownCompletionLane(wg)}
	wg.Go(func() error {
		defer saver.knownCompletions.finish()
		inCallback := true
		defer func() {
			// when the defer is called while inCallback is true, this means
			// that runtime.Goexit was called within `fn`. This should only happen
			// if a test uses t.Fatal within `fn`.
			if inCallback {
				cancel()
			}
		}()
		err := fn(ctx, saver)
		inCallback = false
		saver.knownCompletions.finish()
		if err != nil {
			return err
		}
		if err := r.flush(ctx); err != nil {
			return fmt.Errorf("error flushing repository: %w", err)
		}
		return nil
	})
	return wg.Wait()
}

func (r *Repository) startPackUploader(ctx context.Context, wg *errgroup.Group) {
	if r.packerWg != nil {
		panic("uploader already started")
	}

	innerWg, ctx := errgroup.WithContext(ctx)
	r.packerWg = innerWg
	r.uploader = newPackerUploader(ctx, innerWg, r, r.Connections())
	r.treePM = newPackerManager(r.key, restic.TreeBlob, r.PackSize(), r.packerCount, r.uploader.QueuePacker)
	r.dataPM = newPackerManager(r.key, restic.DataBlob, r.PackSize(), r.packerCount, r.uploader.QueuePacker)
	// Backends without their own checksum can use a pack hash collected on the
	// write path, avoiding a later read of the finalized temporary pack.
	hashOnWrite := r.be.Hasher() == nil
	r.treePM.hashOnWrite = hashOnWrite
	r.dataPM.hashOnWrite = hashOnWrite

	wg.Go(func() error {
		return innerWg.Wait()
	})
}

type blobSaverRepo struct {
	repo             *Repository
	knownCompletions *knownCompletionLane
}

func (r *blobSaverRepo) SaveBlob(ctx context.Context, t restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool) (newID restic.ID, known bool, size int, err error) {
	return r.repo.saveBlob(ctx, t, buf, id, storeDuplicate)
}

func (r *blobSaverRepo) SaveBlobAsync(ctx context.Context, t restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool, cb func(newID restic.ID, known bool, size int, err error)) {
	r.repo.saveBlobAsync(ctx, t, buf, id, storeDuplicate, cb)
}

// LookupStoredBlobSize checks only indexed blobs, excluding unfinished pending saves.
func (r *blobSaverRepo) LookupStoredBlobSize(bh restic.BlobHandle) (uint, bool) {
	return r.repo.idx.LookupIndexSize(bh)
}

// SaveKnownBlobAsync reports an indexed blob asynchronously without retaining its data.
func (r *blobSaverRepo) SaveKnownBlobAsync(ctx context.Context, id restic.ID, cb func(newID restic.ID, known bool, sizeInRepo int, err error)) {
	r.knownCompletions.submit(ctx, id, cb)
}

// Flush saves all remaining packs and the index
func (r *Repository) flush(ctx context.Context) error {
	r.flushBlobSaver()
	r.mainWg = nil

	if err := r.flushPackUploader(ctx); err != nil {
		return err
	}

	return r.idx.Flush(ctx, &internalRepository{r})
}

func (r *Repository) flushBlobSaver() {
	if r.blobSaver == nil {
		return
	}
	r.blobSaver.Wait()
	r.blobSaver = nil
}

// FlushPacks saves all remaining packs.
func (r *Repository) flushPackUploader(ctx context.Context) error {
	if r.packerWg == nil {
		return nil
	}

	err := r.treePM.Flush(ctx)
	if err != nil {
		return err
	}
	err = r.dataPM.Flush(ctx)
	if err != nil {
		return err
	}
	r.uploader.TriggerShutdown()
	err = r.packerWg.Wait()

	r.treePM = nil
	r.dataPM = nil
	r.uploader = nil
	r.packerWg = nil

	return err
}

func (r *Repository) Connections() uint {
	return r.be.Properties().Connections
}

func (r *Repository) LookupBlob(bh restic.BlobHandle) []restic.PackBlob {
	entries := r.idx.Lookup(bh)
	out := make([]restic.PackBlob, len(entries))
	for i, e := range entries {
		out[i] = e
	}
	return out
}

// LookupBlobSize returns the size of blob id. Also returns pending blobs.
func (r *Repository) LookupBlobSize(bh restic.BlobHandle) (uint, bool) {
	return r.idx.LookupSize(bh)
}

// ListBlobs runs fn on all blobs known to the index. When the context is cancelled,
// the index iteration returns immediately with ctx.Err(). This blocks any modification of the index.
func (r *Repository) ListBlobs(ctx context.Context, fn func(restic.PackBlob)) error {
	for blob := range r.idx.Values() {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		fn(blob)
	}
	return nil
}

// listPacksFromIndex returns index entries for the given packs, grouped by pack file.
func (r *Repository) listPacksFromIndex(ctx context.Context, packs restic.IDSet) <-chan index.PackBlobs {
	return r.idx.ListPacks(ctx, packs)
}

func (r *Repository) clearIndex() {
	r.idx = index.NewMasterIndex()
}

// DiscardIndex replaces the in-memory index with an empty one. Callers must
// ensure no repository operation is using the current index.
func (r *Repository) DiscardIndex() {
	r.clearIndex()
}

// LoadIndex loads all index files from the backend in parallel and stores them
func (r *Repository) LoadIndex(ctx context.Context, p restic.TerminalCounterFactory) error {
	return r.loadIndexWithCallback(ctx, p, nil)
}

// loadIndexWithCallback loads all index files from the backend in parallel and stores them
func (r *Repository) loadIndexWithCallback(ctx context.Context, p restic.TerminalCounterFactory, cb func(id restic.ID, idx *index.Index, err error) error) error {
	debug.Log("Loading index")

	bar := p.NewCounterTerminalOnly("index files loaded")

	// Reclaim completed setup work, including transient KDF scratch, before
	// the index adds its live allocations to the process high-water mark.
	runtime.GC()

	err := r.loadIndexesStreaming(ctx, bar, cb)
	if err != nil {
		return err
	}

	// Trigger GC to reset garbage collection threshold
	runtime.GC()

	if r.cfg.Version < 2 {
		// sanity check
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()

		invalidIndex := false
		for blob := range r.idx.Values() {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if blob.IsCompressed() {
				invalidIndex = true
			}
		}
		if invalidIndex {
			return errors.New("index uses feature not supported by repository version 1")
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}

	// remove index files from the cache which have been removed in the repo
	return r.prepareCache()
}

// createIndexFromPacks creates a new index by reading all given pack files (with sizes).
// The index is added to the MasterIndex but not marked as finalized.
// Returned is the list of pack files which could not be read.
func (r *Repository) createIndexFromPacks(ctx context.Context, packsize map[restic.ID]int64, p restic.Counter) (invalid restic.IDs, err error) {
	var m sync.Mutex

	debug.Log("Loading index from pack files")

	// track spawned goroutines using wg, create a new context which is
	// cancelled as soon as an error occurs.
	wg, wgCtx := errgroup.WithContext(ctx)

	type FileInfo struct {
		restic.ID
		Size int64
	}
	ch := make(chan FileInfo)

	// send list of pack files through ch, which is closed afterwards
	wg.Go(func() error {
		defer close(ch)
		for id, size := range packsize {
			select {
			case <-wgCtx.Done():
				return wgCtx.Err()
			case ch <- FileInfo{id, size}:
			}
		}
		return nil
	})

	// a worker receives an pack ID from ch, reads the pack contents, and adds them to idx
	worker := func() error {
		for fi := range ch {
			entries, err := r.listPack(wgCtx, fi.ID, fi.Size)
			if err != nil {
				debug.Log("unable to list pack file %v", fi.ID.Str())
				m.Lock()
				invalid = append(invalid, fi.ID)
				m.Unlock()
			} else if err := r.idx.StorePack(wgCtx, fi.ID, entries, &internalRepository{r}); err != nil {
				return err
			}
			p.Add(1)
		}

		return nil
	}

	// decoding the pack header is usually quite fast, thus we are primarily IO-bound
	workerCount := int(r.Connections())
	// run workers on ch
	for range workerCount {
		wg.Go(worker)
	}

	err = wg.Wait()
	if err != nil {
		return invalid, err
	}

	// flush the index to the repository
	err = r.flush(ctx)
	if err != nil {
		return invalid, err
	}

	return invalid, nil
}

func (r *Repository) NewAssociatedBlobSet() restic.AssociatedBlobSet {
	return &associatedBlobSet{*index.NewAssociatedSet[struct{}](r.idx)}
}

// associatedBlobSet is a wrapper around index.AssociatedSet to implement the restic.AssociatedBlobSet interface.
type associatedBlobSet struct {
	index.AssociatedSet[struct{}]
}

func (s *associatedBlobSet) Intersect(other restic.AssociatedBlobSet) restic.AssociatedBlobSet {
	return &associatedBlobSet{*s.AssociatedSet.Intersect(other)}
}
func (s *associatedBlobSet) Sub(other restic.AssociatedBlobSet) restic.AssociatedBlobSet {
	return &associatedBlobSet{*s.AssociatedSet.Sub(other)}
}

// prepareCache initializes the local cache. indexIDs is the list of IDs of
// index files still present in the repo.
func (r *Repository) prepareCache() error {
	if r.cache == nil {
		return nil
	}

	packs := r.idx.Packs(restic.NewIDSet())

	ids := make(map[string]struct{})
	for id := range packs {
		ids[id.String()] = struct{}{}
	}

	// clear old packs
	return r.cache.Clear(backend.PackFile, ids)
}

// SearchKey finds a key with the supplied password, afterwards the config is
// read and parsed. It tries at most maxKeys key files in the repo.
func (r *Repository) SearchKey(ctx context.Context, password string, maxKeys int, keyHint string) error {
	key, err := searchKey(ctx, r, password, maxKeys, keyHint)
	if err != nil {
		return err
	}

	oldKey := r.key
	oldKeyID := r.keyID

	r.key = key.master
	r.keyID = key.ID()
	cfg, err := restic.LoadConfig(ctx, r)
	if err != nil {
		r.key = oldKey
		r.keyID = oldKeyID

		if err == crypto.ErrUnauthenticated {
			return fmt.Errorf("config or key %v is damaged: %w", key.ID(), err)
		}
		return fmt.Errorf("config cannot be loaded: %w", err)
	}

	r.setConfig(cfg)
	return nil
}

// Init creates a new master key with the supplied password, initializes and
// saves the repository config.
func (r *Repository) Init(ctx context.Context, version uint, password string, chunkerPolynomial *chunker.Pol) error {
	if version > restic.MaxRepoVersion {
		return fmt.Errorf("repository version %v too high", version)
	}

	if version < restic.MinRepoVersion {
		return fmt.Errorf("repository version %v too low", version)
	}

	_, err := r.be.Stat(ctx, backend.Handle{Type: backend.ConfigFile})
	if err != nil && !r.be.IsNotExist(err) {
		return err
	}
	if err == nil {
		return errors.New("repository master key and config already initialized")
	}
	// double check to make sure that a repository is not accidentally reinitialized
	// if the backend somehow fails to stat the config file. An initialized repository
	// must always contain at least one key file.
	if err := r.List(ctx, restic.KeyFile, func(_ restic.ID, _ int64) error {
		return errors.New("repository already contains keys")
	}); err != nil {
		return err
	}
	// Also check for snapshots to detect repositories with a misconfigured retention
	// policy that deletes files older than x days. For such repositories usually the
	// config and key files are removed first and therefore the check would not detect
	// the old repository.
	if err := r.List(ctx, restic.SnapshotFile, func(_ restic.ID, _ int64) error {
		return errors.New("repository already contains snapshots")
	}); err != nil {
		return err
	}

	cfg, err := restic.CreateConfig(version, chunkerPolynomial)
	if err != nil {
		return err
	}

	return r.init(ctx, password, cfg)
}

// init creates a new master key with the supplied password and uses it to save
// the config into the repo.
func (r *Repository) init(ctx context.Context, password string, cfg restic.Config) error {
	key, err := createMasterKey(ctx, r, password)
	if err != nil {
		return err
	}

	r.key = key.master
	r.keyID = key.ID()
	r.setConfig(cfg)
	return restic.SaveConfig(ctx, &internalRepository{r}, cfg)
}

// Key returns the current master key.
func (r *Repository) Key() *crypto.Key {
	return r.key
}

// KeyID returns the id of the current key in the backend.
func (r *Repository) KeyID() restic.ID {
	return r.keyID
}

// List runs fn for all files of type t in the repo.
func (r *Repository) List(ctx context.Context, t restic.FileType, fn func(restic.ID, int64) error) error {
	return r.be.List(ctx, backend.FileType(t), func(fi backend.FileInfo) error {
		id, err := restic.ParseID(fi.Name)
		if err != nil {
			debug.Log("unable to parse %v as an ID", fi.Name)
			return nil
		}
		return fn(id, fi.Size)
	})
}

// listPack returns blob entries from the pack file header including offsets.
func (r *Repository) listPack(ctx context.Context, id restic.ID, size int64) (pack.Blobs, error) {
	h := backend.Handle{Type: backend.PackFile, Name: id.String()}

	entries, _, err := pack.List(r.Key(), backend.ReaderAt(ctx, r.be, h), size)
	if err != nil {
		if r.cache != nil {
			// ignore error as there is not much we can do here
			_ = r.cache.Forget(h)
		}

		// retry on error
		entries, _, err = pack.List(r.Key(), backend.ReaderAt(ctx, r.be, h), size)
	}
	return pack.Blobs(entries), err
}

// ListPackHandles returns the blob handles stored in the pack file header.
func (r *Repository) ListPackHandles(ctx context.Context, id restic.ID, size int64) ([]restic.BlobHandle, error) {
	blobs, err := r.listPack(ctx, id, size)
	if err != nil {
		return nil, err
	}
	handles := make([]restic.BlobHandle, len(blobs))
	for i, blob := range blobs {
		handles[i] = blob.BlobHandle
	}
	return handles, nil
}

// Delete calls backend.Delete() if implemented, and returns an error
// otherwise.
func (r *Repository) Delete(ctx context.Context) error {
	return r.be.Delete(ctx)
}

// Close closes the repository by closing the backend.
func (r *Repository) Close() error {
	r.smallRestoreDecoderMu.Lock()
	r.smallRestoreClosed = true
	dec := r.smallRestoreDecoder
	r.smallRestoreDecoder = nil
	r.smallRestoreDecoderMu.Unlock()
	if dec != nil {
		dec.Close()
	}
	return r.be.Close()
}

// saveBlob saves a blob of type t into the repository.
// It takes care that no duplicates are saved; this can be overwritten
// by setting storeDuplicate to true.
// If id is the null id, it will be computed and returned.
// Also returns if the blob was already known before.
// If the blob was not known before, it returns the number of bytes the blob
// occupies in the repo (compressed or not, including encryption overhead).
func (r *Repository) saveBlob(ctx context.Context, t restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool) (newID restic.ID, known bool, size int, err error) {

	if int64(len(buf)) > math.MaxUint32 {
		return restic.ID{}, false, 0, fmt.Errorf("blob is larger than 4GB")
	}

	// compute plaintext hash if not already set
	if id.IsNull() {
		// Special case the hash calculation for all zero chunks. This is especially
		// useful for sparse files containing large all zero regions. For these we can
		// process chunks as fast as we can read the from disk.
		if len(buf) == chunker.MinSize && restic.ZeroPrefixLen(buf) == chunker.MinSize {
			newID = r.zeroChunk()
		} else {
			newID = restic.Hash(buf)
		}
	} else {
		newID = id
	}

	// first try to add to pending blobs; if not successful, this blob is already known
	known = !r.idx.AddPending(restic.BlobHandle{ID: newID, Type: t}, uint(len(buf)))

	// only save when needed or explicitly told
	if !known || storeDuplicate {
		size, err = r.saveAndEncrypt(ctx, t, buf, newID)
	}

	return newID, known, size, err
}

func (r *Repository) saveBlobAsync(ctx context.Context, t restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool, cb func(newID restic.ID, known bool, size int, err error)) {
	r.mainWg.Go(func() error {
		if ctx.Err() != nil {
			// fail fast if the context is cancelled
			cb(restic.ID{}, false, 0, ctx.Err())
			return ctx.Err()
		}
		newID, known, size, err := r.saveBlob(ctx, t, buf, id, storeDuplicate)
		cb(newID, known, size, err)
		return err
	})
}

type backendLoadFn func(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error
type loadBlobFn func(ctx context.Context, bh restic.BlobHandle, buf []byte) ([]byte, error)

// Skip sections with more than 1MB unused blobs
const maxUnusedRange = 1 * 1024 * 1024

// Limit each selected range to one quarter of a default pack. A single blob may be larger
// and is kept intact.
const maxStreamPackChunkSize = DefaultPackSize / 4

// streamPackBufferPool retains at most two bounded range buffers across pack
// streams. Buffers are cleared before being retained because uncompressed
// blobs are decrypted in place into the range buffer.
var streamPackBufferPool = make(chan []byte, 2)

func takeStreamPackBuffer() []byte {
	select {
	case buf := <-streamPackBufferPool:
		return buf[:0]
	default:
		return nil
	}
}

func putStreamPackBuffer(buf []byte) {
	// Retain buffers only when they can serve another ordinary range. A
	// one-off range containing a blob larger than the split target should not
	// occupy the cross-stream pool after its callbacks finish.
	if cap(buf) == 0 || cap(buf) > maxStreamPackChunkSize {
		return
	}
	clear(buf[:cap(buf)])
	select {
	case streamPackBufferPool <- buf[:0]:
	default:
	}
}

// LoadBlobsFromPack loads the listed blobs from the specified pack file. The plaintext blob is passed to
// the handleBlobFn callback or an error if decryption failed or the blob hash does not match.
// handleBlobFn is called at most once for each blob. If the callback returns an error,
// then LoadBlobsFromPack will abort and not retry it. The buf passed to the callback is only valid within
// this specific call. The callback must not keep a reference to buf.
func (r *Repository) LoadBlobsFromPack(ctx context.Context, packID restic.ID, handles []restic.BlobHandle, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error) error {
	blobs, err := r.blobsInPack(packID, handles)
	if err != nil {
		return err
	}
	return r.loadBlobsFromPack(ctx, packID, blobs, handleBlobFn)
}

func (r *Repository) blobsInPack(packID restic.ID, handles []restic.BlobHandle) (pack.Blobs, error) {
	blobs := make(pack.Blobs, 0, len(handles))
	for _, h := range handles {
		found := false
		for _, pb := range r.idx.Lookup(h) {
			if pb.PackID().Equal(packID) {
				blobs = append(blobs, pb.Blob)
				found = true
				break
			}
		}
		if !found {
			return nil, errors.Errorf("blob %v not found in pack %v", h, packID)
		}
	}
	return blobs, nil
}

func (r *Repository) loadBlobsFromPack(ctx context.Context, packID restic.ID, blobs pack.Blobs, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error) error {
	acquire, bounded := restic.SmallRestoreLoad(ctx)
	if !bounded {
		dec := r.getZstdDecoder()
		if use, _ := ctx.Value(restic.RestoreHoldKey{}).(restic.RestoreHoldUse); use.Budget != nil {
			// An ordinary restore worker of the restorer: its own decoder, and the
			// local backend's additional load slot (ruling 4) for the third worker.
			dec = r.getRestoreZstdDecoder(use.Budget.Workers())
			ctx = sema.WithAdditionalLoadSlot(ctx)
		}
		return streamPackWithPolicy(ctx, r.be.Load, r.LoadBlob, dec, r.key, packID, blobs, handleBlobFn, restoreStreamWindow, true)
	}
	// Revalidate the index entries actually selected by this loader. If the
	// classifier and loader disagree, retain the unrestricted ordinary path.
	for _, blob := range blobs {
		if !restic.BoundedRestoreBlob(blob.DataLength(), blob.Length, blob.IsCompressed()) {
			release, err := acquire(ctx)
			if err != nil {
				return err
			}
			defer release()
			return streamPackWithPolicy(ctx, r.be.Load, r.LoadBlob, r.getZstdDecoder(), r.key, packID, blobs, handleBlobFn, restoreStreamWindow, true)
		}
	}
	var release func()
	defer func() {
		if release != nil {
			release()
		}
	}()
	ensureOrdinary := func(recoveryCtx context.Context) error {
		// Keep the permit through synchronous callbacks and stream completion,
		// so an alternate large copy cannot overlap two ordinary large streams.
		if release == nil {
			var err error
			release, err = acquire(recoveryCtx)
			if err != nil {
				return err
			}
		}
		return nil
	}
	loadRecovery := func(recoveryCtx context.Context, h restic.BlobHandle, buf []byte) ([]byte, error) {
		if err := ensureOrdinary(recoveryCtx); err != nil {
			return nil, err
		}
		return r.LoadBlob(ctx, h, buf)
	}
	limitedDecoder, err := r.takeSmallRestoreDecoder()
	if err != nil {
		return err
	}
	defer r.putSmallRestoreDecoder(limitedDecoder)
	ctx = context.WithValue(ctx, smallRestoreDecodeKey{}, smallRestoreDecodePolicy{limitedDecoder, ensureOrdinary})
	return streamPackWithPolicy(sema.WithAdditionalLoadSlot(ctx), r.be.Load, loadRecovery,
		r.getZstdDecoder(), r.key, packID, blobs, handleBlobFn, restic.SmallRestoreCiphertextLimit, false)

}

// takeSmallRestoreDecoder transfers exclusive ownership out of the one-entry
// cache. DecodeAll is synchronous; no plaintext or iterator escapes the stream.
func (r *Repository) takeSmallRestoreDecoder() (*zstd.Decoder, error) {
	r.smallRestoreDecoderMu.Lock()
	if r.smallRestoreClosed {
		r.smallRestoreDecoderMu.Unlock()
		return nil, zstd.ErrDecoderClosed
	}
	dec := r.smallRestoreDecoder
	r.smallRestoreDecoder = nil
	r.smallRestoreDecoderMu.Unlock()
	if dec != nil {
		return dec, nil
	}
	return zstd.NewReader(nil, zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxMemory(restic.SmallRestoreBlobLimit), zstd.WithDecodeAllCapLimit(true))
}

func (r *Repository) putSmallRestoreDecoder(dec *zstd.Decoder) {
	r.smallRestoreDecoderMu.Lock()
	if !r.smallRestoreClosed && r.smallRestoreDecoder == nil {
		r.smallRestoreDecoder = dec
		dec = nil
	}
	r.smallRestoreDecoderMu.Unlock()
	if dec != nil {
		dec.Close()
	}
}

type smallRestoreDecodeKey struct{}
type smallRestoreDecodePolicy struct {
	decoder        *zstd.Decoder
	ensureOrdinary func(context.Context) error
}

func streamPack(ctx context.Context, beLoad backendLoadFn, loadBlobFn loadBlobFn, dec *zstd.Decoder, key *crypto.Key, packID restic.ID, blobs pack.Blobs, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error) error {
	return streamPackWindowed(ctx, beLoad, loadBlobFn, dec, key, packID, blobs, handleBlobFn, maxStreamPackChunkSize, true)
}

func streamPackWithPolicy(ctx context.Context, beLoad backendLoadFn, loadBlobFn loadBlobFn, dec *zstd.Decoder, key *crypto.Key, packID restic.ID, blobs pack.Blobs, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error, maxChunkSize uint, pooled bool) error {
	return streamPackCoalesced(ctx, beLoad, loadBlobFn, dec, key, packID, blobs, handleBlobFn, maxChunkSize, pooled)
}

func streamPackWindowed(ctx context.Context, beLoad backendLoadFn, loadBlobFn loadBlobFn, dec *zstd.Decoder, key *crypto.Key, packID restic.ID, blobs pack.Blobs, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error, maxChunkSize uint, pooled bool) error {
	if len(blobs) == 0 {
		// nothing to do
		return nil
	}
	blobs.Sort()

	lowerIdx := 0
	lastPos := blobs[0].Offset
	// Parts are loaded sequentially. Reuse the largest range buffer for this
	// stream instead of allocating a new one for every part. Retain a small
	// number across streams to reuse allocations between packs as well.
	var data []byte
	if pooled {
		data = takeStreamPackBuffer()
	}
	defer func() {
		if pooled {
			putStreamPackBuffer(data)
		}
	}()
	loadPart := func(part pack.Blobs) error {
		return streamPackPartWithBuffer(ctx, beLoad, loadBlobFn, dec, key, packID, part, &data, handleBlobFn)
	}

	for i := range blobs {
		if blobs[i].Offset < lastPos {
			// don't wait for streamPackPart to fail
			return errors.Errorf("overlapping blobs in pack %v", packID)
		}

		chunkSizeAfter := (blobs[i].Offset + blobs[i].Length) - blobs[lowerIdx].Offset
		split := false
		// split if the chunk would become larger than maxChunkSize. Oversized chunks are
		// handled by the requirement that the chunk contains at least one blob (i > lowerIdx)
		if i > lowerIdx && chunkSizeAfter >= maxChunkSize {
			split = true
		}
		// skip too large gaps as a new request is typically much cheaper than data transfers
		if blobs[i].Offset-lastPos > maxUnusedRange {
			split = true
		}

		if split {
			// load everything up to the skipped file section
			err := loadPart(blobs[lowerIdx:i])
			if err != nil {
				return err
			}
			lowerIdx = i
		}
		lastPos = blobs[i].Offset + blobs[i].Length
	}
	// load remainder
	return loadPart(blobs[lowerIdx:])
}

func streamPackPart(ctx context.Context, beLoad backendLoadFn, loadBlobFn loadBlobFn, dec *zstd.Decoder, key *crypto.Key, packID restic.ID, blobs pack.Blobs, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error) error {
	var data []byte
	return streamPackPartWithBuffer(ctx, beLoad, loadBlobFn, dec, key, packID, blobs, &data, handleBlobFn)
}

func streamPackPartWithBuffer(ctx context.Context, beLoad backendLoadFn, loadBlobFn loadBlobFn, dec *zstd.Decoder, key *crypto.Key, packID restic.ID, blobs pack.Blobs, data *[]byte, handleBlobFn func(blob restic.BlobHandle, buf []byte, err error) error) error {
	h := backend.Handle{Type: backend.PackFile, Name: packID.String(), IsMetadata: blobs[0].Type.IsMetadata()}

	dataStart := blobs[0].Offset
	dataEnd := blobs[len(blobs)-1].Offset + blobs[len(blobs)-1].Length

	debug.Log("streaming pack %v (%d to %d bytes), blobs: %v", packID, dataStart, dataEnd, len(blobs))

	dataSize := int(dataEnd - dataStart)
	sizeData := func() {
		if cap(*data) < dataSize {
			*data = make([]byte, dataSize)
		} else {
			*data = (*data)[:dataSize]
		}
	}
	// An ordinary restore worker takes the window's bytes from the restore's
	// hold budget inside the Load callback, before the window is allocated.
	hold := newRestoreWindowHold(ctx, blobs)
	defer hold.release(data)
	if hold == nil {
		sizeData()
	}
	err := beLoad(ctx, h, dataSize, int64(dataStart), func(rd io.Reader) error {
		if hold != nil {
			if herr := hold.admit(ctx); herr != nil {
				return herr
			}
			sizeData()
		}
		_, cerr := io.ReadFull(rd, *data)
		return cerr
	})
	// prevent callbacks after cancellation
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		// the context is only still valid if handleBlobFn never returned an error
		if loadBlobFn != nil {
			// Recovery loads need a backend connection: give the bytes back first,
			// so that no worker waits for a connection while it holds budget.
			hold.release(nil)
			// check whether we can get the remaining blobs somewhere else
			for _, entry := range blobs {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				buf, ierr := loadBlobFn(ctx, entry.BlobHandle, nil)
				if ctx.Err() != nil {
					return ctx.Err()
				}
				err = handleBlobFn(entry.BlobHandle, buf, ierr)
				if err != nil {
					break
				}
				if ctx.Err() != nil {
					return ctx.Err()
				}
			}
		}
		return errors.Wrap(err, "StreamPack")
	}

	it := newPackBlobIterator(packID, newByteReader(*data), dataStart, blobs, key, dec)
	if policy, ok := ctx.Value(smallRestoreDecodeKey{}).(smallRestoreDecodePolicy); ok {
		it.smallDecoder = policy.decoder
		it.ensureOrdinary = func() error { return policy.ensureOrdinary(ctx) }
		var maxPlaintext uint
		for _, blob := range blobs {
			if blob.IsCompressed() && blob.DataLength() > maxPlaintext {
				maxPlaintext = blob.DataLength()
			}
		}
		it.decode = make([]byte, 0, maxPlaintext)
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		val, err := it.Next()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err == errPackEOF {
			break
		} else if err != nil {
			return err
		}

		if val.Err != nil && loadBlobFn != nil {
			var ierr error
			// as above: no backend load while holding budget
			hold.release(nil)
			// check whether we can get a valid copy somewhere else
			buf, ierr := loadBlobFn(ctx, val.Handle, nil)
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if ierr == nil {
				// success
				val.Plaintext = buf
				val.Err = nil
			}
		}

		err = handleBlobFn(val.Handle, val.Plaintext, val.Err)
		if err != nil {
			return err
		}
		// ensure that each blob is only passed once to handleBlobFn
		blobs = blobs[1:]
	}

	return errors.Wrap(err, "StreamPack")
}

// discardReader allows the PackBlobIterator to perform zero copy
// reads if the underlying data source is a byte slice.
type discardReader interface {
	Discard(n int) (discarded int, err error)
	// ReadFull reads the next n bytes into a byte slice. The caller must not
	// retain a reference to the byte. Modifications are only allowed within
	// the boundaries of the returned slice.
	ReadFull(n int) (buf []byte, err error)
}

type byteReader struct {
	buf []byte
}

func newByteReader(buf []byte) *byteReader {
	return &byteReader{
		buf: buf,
	}
}

func (b *byteReader) Discard(n int) (discarded int, err error) {
	if len(b.buf) < n {
		return 0, io.ErrUnexpectedEOF
	}
	b.buf = b.buf[n:]
	return n, nil
}

func (b *byteReader) ReadFull(n int) (buf []byte, err error) {
	if len(b.buf) < n {
		return nil, io.ErrUnexpectedEOF
	}
	buf = b.buf[:n]
	b.buf = b.buf[n:]
	return buf, nil
}

type packBlobIterator struct {
	packID        restic.ID
	rd            discardReader
	currentOffset uint

	blobs pack.Blobs
	key   *crypto.Key
	dec   *zstd.Decoder

	smallDecoder   *zstd.Decoder
	ensureOrdinary func() error
	decode         []byte
	blobHash       hash.Hash
}

type packBlobValue struct {
	Handle    restic.BlobHandle
	Plaintext []byte
	Err       error
}

var errPackEOF = errors.New("reached EOF of pack file")

func newPackBlobIterator(packID restic.ID, rd discardReader, currentOffset uint,
	blobs pack.Blobs, key *crypto.Key, dec *zstd.Decoder) *packBlobIterator {
	return &packBlobIterator{
		packID:        packID,
		rd:            rd,
		currentOffset: currentOffset,
		blobs:         blobs,
		key:           key,
		dec:           dec,
	}
}

// Next returns the next blob, an error or ErrPackEOF if all blobs were read
func (b *packBlobIterator) Next() (packBlobValue, error) {
	entry, buf, err := b.readBlob()
	if err != nil {
		return packBlobValue{}, err
	}
	h := restic.BlobHandle{ID: entry.ID, Type: entry.Type}

	// decryption errors are likely permanent, give the caller a chance to skip them
	nonce, ciphertext := buf[:b.key.NonceSize()], buf[b.key.NonceSize():]
	plaintext, err := b.key.Open(ciphertext[:0], nonce, ciphertext, nil)
	if err != nil {
		err = fmt.Errorf("decrypting blob %v from pack %v failed: %w", h, b.packID.String(), err)
	}
	if err == nil && entry.IsCompressed() {
		// DecodeAll will allocate a slice if it is not large enough since it
		// knows the decompressed size (because we're using EncodeAll)
		if b.smallDecoder != nil {
			b.decode, err = b.smallDecoder.DecodeAll(plaintext, b.decode[:0])
			if err != nil {
				// Size policy is not a corruption verdict. Retry with the original
				// decoder under ordinary allocation admission to preserve diagnostics.
				if admissionErr := b.ensureOrdinary(); admissionErr != nil {
					return packBlobValue{}, admissionErr
				}
				b.decode, err = b.dec.DecodeAll(plaintext, b.decode[:0])
			}
		} else {
			b.decode, err = b.dec.DecodeAll(plaintext, b.decode[:0])
		}
		plaintext = b.decode
		if err != nil {
			err = fmt.Errorf("decompressing blob %v from pack %v failed: %w", h, b.packID.String(), err)
		}
	}
	if err == nil {
		id := restic.Hash(plaintext)
		if !id.Equal(entry.ID) {
			debug.Log("read blob %v/%v from pack %v: wrong data returned, hash is %v",
				h.Type, h.ID, b.packID.String(), id)
			err = fmt.Errorf("read blob %v from pack %v: wrong data returned, hash is %v",
				h, b.packID.String(), id)
		}
	}

	return packBlobValue{entry.BlobHandle, plaintext, err}, nil
}

// NextHash verifies the next blob without keeping its decompressed contents.
// It is used by the checker, which only needs the plaintext hash.
func (b *packBlobIterator) NextHash() (packBlobValue, error) {
	entry, buf, err := b.readBlob()
	if err != nil {
		return packBlobValue{}, err
	}
	h := restic.BlobHandle{ID: entry.ID, Type: entry.Type}

	nonce, ciphertext := buf[:b.key.NonceSize()], buf[b.key.NonceSize():]
	plaintext, err := b.key.Open(ciphertext[:0], nonce, ciphertext, nil)
	if err != nil {
		err = fmt.Errorf("decrypting blob %v from pack %v failed: %w", h, b.packID.String(), err)
	} else if entry.IsCompressed() {
		if b.blobHash == nil {
			b.blobHash = sha256.New()
		} else {
			b.blobHash.Reset()
		}

		err = b.dec.Reset(bytes.NewReader(plaintext))
		if err == nil {
			_, err = io.Copy(b.blobHash, b.dec)
		}
		if err != nil {
			err = fmt.Errorf("decompressing blob %v from pack %v failed: %w", h, b.packID.String(), err)
		} else {
			var sum [sha256.Size]byte
			var got restic.ID
			copy(got[:], b.blobHash.Sum(sum[:0]))
			if !got.Equal(entry.ID) {
				debug.Log("  read blob %v/%v from pack %v: wrong data returned, hash is %v", h.Type, h.ID, b.packID.String(), got)
				err = fmt.Errorf("read blob %v from pack %v: wrong data returned, hash is %v", h, b.packID.String(), got)
			}
		}
	} else if got := restic.Hash(plaintext); !got.Equal(entry.ID) {
		debug.Log("  read blob %v/%v from pack %v: wrong data returned, hash is %v", h.Type, h.ID, b.packID.String(), got)
		err = fmt.Errorf("read blob %v from pack %v: wrong data returned, hash is %v", h, b.packID.String(), got)
	}

	return packBlobValue{Handle: h, Err: err}, nil
}

func (b *packBlobIterator) readBlob() (pack.Blob, []byte, error) {
	if len(b.blobs) == 0 {
		return pack.Blob{}, nil, errPackEOF
	}

	entry := b.blobs[0]
	b.blobs = b.blobs[1:]

	skipBytes := int(entry.Offset - b.currentOffset)
	if skipBytes < 0 {
		return pack.Blob{}, nil, fmt.Errorf("overlapping blobs in pack %v", b.packID)
	}

	_, err := b.rd.Discard(skipBytes)
	if err != nil {
		return pack.Blob{}, nil, err
	}
	b.currentOffset = entry.Offset

	h := restic.BlobHandle{ID: entry.ID, Type: entry.Type}
	debug.Log("  process blob %v, skipped %d, %v", h, skipBytes, entry)

	buf, err := b.rd.ReadFull(int(entry.Length))
	if err != nil {
		debug.Log("    read error %v", err)
		return pack.Blob{}, nil, fmt.Errorf("readFull: %w", err)
	}

	b.currentOffset = entry.Offset + entry.Length

	if int(entry.Length) <= b.key.NonceSize() {
		debug.Log("%v", b.blobs)
		return pack.Blob{}, nil, fmt.Errorf("invalid blob length %v", entry)
	}

	return entry, buf, nil
}

func (r *Repository) zeroChunk() restic.ID {
	r.zeroChunkOnce.Do(func() {
		r.zeroChunkID = restic.Hash(make([]byte, chunker.MinSize))
	})
	return r.zeroChunkID
}
