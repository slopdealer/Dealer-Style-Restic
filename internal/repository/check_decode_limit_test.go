package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"hash"
	"io"
	"os"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// The two tests of the red team (seat 2, finding 1): check's streaming decode
// must keep the ceiling on decoded bytes that upstream's check and restore
// have.

// Use the same two iterator entry points, with a small decoder ceiling so the
// lost cumulative output check can be demonstrated without a 64 GiB allocation.
func TestCheckStreamKeepsCumulativeDecodeLimit(t *testing.T) {
	repo, _ := TestRepositoryWithBackend(t, nil, 2, Options{})
	const limit = 16 << 10
	plain := bytes.Repeat([]byte{0x42}, 8<<10)
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(1024), zstd.WithSingleSegment(false), zstd.WithEncoderCRC(false))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	frame := enc.EncodeAll(plain, nil)
	compressed := bytes.Repeat(frame, 3)
	wantPlain := bytes.Repeat(plain, 3)
	nonce := bytes.Repeat([]byte{1}, repo.Key().NonceSize())
	ciphertext := append(bytes.Clone(nonce), repo.Key().Seal(nil, nonce, compressed, nil)...)
	entry := pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash(wantPlain)}, Length: uint(len(ciphertext)), UncompressedLength: uint(len(wantPlain))}
	makeIterator := func() *packBlobIterator {
		dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(limit))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(dec.Close)
		return newPackBlobIterator(restic.ID{}, newByteReader(bytes.Clone(ciphertext)), 0, pack.Blobs{entry}, repo.Key(), dec)
	}
	upstream, err := makeIterator().Next()
	if err != nil || upstream.Err == nil {
		t.Fatalf("eager control failed to reject over-limit output: iterator %v, blob %v", err, upstream.Err)
	}
	t.Logf("upstream iterator rejects cumulative output: %v", upstream.Err)
	// The stream cannot read the ceiling of the decoder it is given; in check
	// both are upstream's 64 GiB. Here the same small ceiling is set for both.
	it := makeIterator()
	it.hashLimit = limit
	candidate, err := it.NextHash()
	if err != nil {
		t.Fatal(err)
	}
	if candidate.Err == nil {
		t.Fatal("streaming checker accepted cumulative decoded output beyond the decoder ceiling")
	}
	if candidate.Err.Error() != upstream.Err.Error() || !errors.Is(candidate.Err, zstd.ErrDecoderSizeExceeded) {
		t.Fatalf("streaming checker reports %q, the eager one %q", candidate.Err, upstream.Err)
	}

	// exactly at the ceiling both accept, one byte less of a ceiling both refuse
	for _, test := range []struct {
		limit uint64
		ok    bool
	}{{uint64(len(wantPlain)), true}, {uint64(len(wantPlain)) - 1, false}} {
		dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(test.limit))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(dec.Close)
		eager, err := newPackBlobIterator(restic.ID{}, newByteReader(bytes.Clone(ciphertext)), 0, pack.Blobs{entry}, repo.Key(), dec).Next()
		if err != nil {
			t.Fatal(err)
		}
		it := newPackBlobIterator(restic.ID{}, newByteReader(bytes.Clone(ciphertext)), 0, pack.Blobs{entry}, repo.Key(), dec)
		it.hashLimit = test.limit
		stream, err := it.NextHash()
		if err != nil {
			t.Fatal(err)
		}
		if (eager.Err == nil) != test.ok || (stream.Err == nil) != test.ok {
			t.Fatalf("ceiling %d: eager %v, stream %v, want accepted=%v", test.limit, eager.Err, stream.Err, test.ok)
		}
		if !test.ok && stream.Err.Error() != eager.Err.Error() {
			t.Fatalf("ceiling %d: stream reports %q, eager %q", test.limit, stream.Err, eager.Err)
		}
	}
}

// A frame that declares more content than the ceiling is refused before any
// of it is decoded, with the eager decoder's error.
func TestCheckStreamRefusesDeclaredOversize(t *testing.T) {
	repo, _ := TestRepositoryWithBackend(t, nil, 2, Options{})
	const limit = 16 << 10
	plain := make([]byte, 64<<10)
	enc, err := zstd.NewWriter(nil, zstd.WithWindowSize(1024), zstd.WithSingleSegment(false), zstd.WithEncoderCRC(false))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	compressed := enc.EncodeAll(plain, nil)
	var hdr zstd.Header
	if err := hdr.Decode(compressed); err != nil || !hdr.HasFCS || hdr.FrameContentSize != uint64(len(plain)) {
		t.Fatalf("fixture does not declare its content size: %+v, %v", hdr, err)
	}
	nonce := bytes.Repeat([]byte{1}, repo.Key().NonceSize())
	ciphertext := append(bytes.Clone(nonce), repo.Key().Seal(nil, nonce, compressed, nil)...)
	entry := pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash(plain)}, Length: uint(len(ciphertext)), UncompressedLength: uint(len(plain))}
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(limit))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	eager, err := newPackBlobIterator(restic.ID{}, newByteReader(bytes.Clone(ciphertext)), 0, pack.Blobs{entry}, repo.Key(), dec).Next()
	if err != nil || eager.Err == nil {
		t.Fatalf("eager control: %v, %v", err, eager.Err)
	}
	it := newPackBlobIterator(restic.ID{}, newByteReader(bytes.Clone(ciphertext)), 0, pack.Blobs{entry}, repo.Key(), dec)
	it.hashLimit = limit
	hashed := &countingHash{Hash: sha256.New()}
	it.blobHash = hashed
	stream, err := it.NextHash()
	if err != nil {
		t.Fatal(err)
	}
	if stream.Err == nil || stream.Err.Error() != eager.Err.Error() {
		t.Fatalf("stream reports %v, eager %v", stream.Err, eager.Err)
	}
	if hashed.n != 0 {
		t.Fatalf("%d bytes were decoded before the declared size was refused", hashed.n)
	}
}

type countingHash struct {
	hash.Hash
	n int
}

func (c *countingHash) Write(p []byte) (int, error) {
	c.n += len(p)
	return c.Hash.Write(p)
}

// Opt in explicitly: this hashes 64 GiB + 1 twice, but retains only a small
// compressed pack and decoder window. The eager control checks the declared
// content size and rejects it before allocating the enormous plaintext.
func TestLargeCheckRejectsUnrestorableBlob(t *testing.T) {
	if os.Getenv("RESTIC_LARGE_CHECK_TEST") != "1" {
		t.Skip("set RESTIC_LARGE_CHECK_TEST=1 for the production-limit reproduction")
	}
	ctx := context.Background()
	repo, be := TestRepositoryWithBackend(t, nil, 2, Options{})
	const logical = int64(64<<30) + 1
	const block = 128 << 10
	compressed := []byte{0x28, 0xb5, 0x2f, 0xfd, 0xc0, 0x38}
	compressed = binary.LittleEndian.AppendUint64(compressed, uint64(logical))
	for remaining := logical; remaining > 0; {
		n := min(remaining, block)
		header := uint32(n)<<3 | 2 // RLE block
		remaining -= n
		if remaining == 0 {
			header |= 1
		}
		compressed = append(compressed, byte(header), byte(header>>8), byte(header>>16), 0)
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	_, controlErr := dec.DecodeAll(compressed, nil)
	dec.Close()
	if controlErr != zstd.ErrDecoderSizeExceeded {
		t.Fatalf("eager control error %v, expected size limit", controlErr)
	}
	t.Logf("eager checker rejects %d-byte plaintext before allocation: %v", logical, controlErr)
	digest := sha256.New()
	for remaining := logical; remaining > 0; {
		n := min(remaining, 16<<30)
		if _, err := io.CopyN(digest, zeroReader{}, n); err != nil {
			t.Fatal(err)
		}
		remaining -= n
		t.Logf("computed expected plaintext hash: %d bytes remain", remaining)
	}
	blobID := restic.IDFromHash(digest.Sum(nil))
	nonce := bytes.Repeat([]byte{1}, repo.Key().NonceSize())
	ciphertext := append(bytes.Clone(nonce), repo.Key().Seal(nil, nonce, compressed, nil)...)
	var contents bytes.Buffer
	p := pack.NewPacker(repo.Key(), &contents)
	// The stored size hint is deliberately wrong but fits the repository format;
	// the upstream decode guard still detects this pathological blob.
	if _, err := p.Add(restic.DataBlob, blobID, ciphertext, 1); err != nil {
		t.Fatal(err)
	}
	if err := p.Finalize(); err != nil {
		t.Fatal(err)
	}
	packID := restic.Hash(contents.Bytes())
	if err := be.Save(ctx, backend.Handle{Type: backend.PackFile, Name: packID.String()}, backend.NewByteReader(contents.Bytes(), be.Hasher())); err != nil {
		t.Fatal(err)
	}
	if err := repo.idx.StorePack(ctx, packID, p.Blobs(), &internalRepository{repo}); err != nil {
		t.Fatal(err)
	}
	if err := repo.idx.Flush(ctx, &internalRepository{repo}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LoadBlob(ctx, restic.BlobHandle{ID: blobID, Type: restic.DataBlob}, nil); err == nil {
		t.Fatal("restore control unexpectedly accepted the over-limit blob")
	} else {
		t.Logf("restore control rejects blob: %v", err)
	}
	t.Logf("checking real %d-byte pack; declared blob plaintext is %d bytes", contents.Len(), logical)
	if errs := runReadPacks(repo.Checker()); len(errs) == 0 {
		t.Fatal("full read-data check reports healthy for a blob both upstream check and restore reject")
	}
}
