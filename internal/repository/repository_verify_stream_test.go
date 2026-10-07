package repository

import (
	"bytes"
	stderrors "errors"
	"io"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
)

func TestVerifyCompressedIndex(t *testing.T) {
	data := []byte("{\"packs\":[]}\n")
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	compressed := enc.EncodeAll(data, nil)
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}

	if err := verifyCompressedIndex(compressed, data); err != nil {
		t.Fatalf("matching index failed verification: %v", err)
	}
	if err := verifyCompressedIndex(compressed, data[:len(data)-1]); err == nil || err.Error() != "data mismatch" {
		t.Fatalf("short expected index: got %v, want data mismatch", err)
	}
	if err := verifyCompressedIndex(compressed, append(bytes.Clone(data), 'x')); err == nil || err.Error() != "data mismatch" {
		t.Fatalf("long expected index: got %v, want data mismatch", err)
	}
}

func TestVerifyCompressedIndexStream(t *testing.T) {
	data := []byte("{\"packs\":[{\"id\":\"example\"}]}\n")
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	compressed := enc.EncodeAll(data, nil)
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}

	encode := func(encoded []byte) func(io.Writer) error {
		return func(w io.Writer) error {
			_, err := w.Write(encoded)
			return err
		}
	}
	if err := verifyCompressedIndexStream(compressed, encode(data)); err != nil {
		t.Fatalf("matching streamed index failed verification: %v", err)
	}
	if err := verifyCompressedIndexStream(compressed, encode(data[:len(data)-1])); err == nil || err.Error() != "data mismatch" {
		t.Fatalf("short streamed index: got %v, want data mismatch", err)
	}
	if err := verifyCompressedIndexStream(compressed, encode(append(bytes.Clone(data), 'x'))); err == nil || err.Error() != "data mismatch" {
		t.Fatalf("long streamed index: got %v, want data mismatch", err)
	}

	wantErr := stderrors.New("index encode failed")
	if err := verifyCompressedIndexStream(compressed, func(w io.Writer) error {
		if _, err := w.Write(data); err != nil {
			return err
		}
		return wantErr
	}); !stderrors.Is(err, wantErr) {
		t.Fatalf("got %v, want the index encode error", err)
	}
}

func TestVerifyCompressedIndexStreamReadsThroughMismatch(t *testing.T) {
	expected := []byte("{\"packs\":[{\"id\":\"expected\"}]}\n")
	actual := []byte("{\"packs\":[{\"id\":\"corrupt!\"}]}\n")
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	compressed := enc.EncodeAll(actual, nil)
	if err := enc.Close(); err != nil {
		t.Fatal(err)
	}
	compressed[len(compressed)-1] ^= 0xff

	err = verifyCompressedIndexStream(compressed, func(w io.Writer) error {
		_, err := w.Write(expected)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), "decompression failed:") {
		t.Fatalf("corrupt frame after a content mismatch: got %v, want a decompression error", err)
	}
}

func TestCompareStreamBytesReadsThroughMismatch(t *testing.T) {
	wantErr := stderrors.New("decoder failed after mismatch")
	got, err := compareStreamBytes(&errorAfterByteReader{err: wantErr}, []byte("expected"))
	if got {
		t.Fatal("mismatching stream compared equal")
	}
	if !stderrors.Is(err, wantErr) {
		t.Fatalf("got error %v, want %v", err, wantErr)
	}
}

func TestVerifyUnpackedStreamingMatchesDecodeAll(t *testing.T) {
	repo, _ := TestRepositoryWithBackend(t, nil, 2, Options{})
	data := []byte("{\"packs\":[{\"id\":\"example\"}]}\n")
	enc, err := zstd.NewWriter(nil,
		zstd.WithEncoderConcurrency(1),
		zstd.WithEncoderCRC(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()

	frame := enc.EncodeAll(data, nil)
	mid := len(data) / 2
	firstFrame := enc.EncodeAll(data[:mid], nil)
	secondFrame := enc.EncodeAll(data[mid:], nil)

	valid := append([]byte{2}, frame...)
	concatenated := append([]byte{2}, firstFrame...)
	concatenated = append(concatenated, secondFrame...)
	trailingInput := append(bytes.Clone(valid), 0xde, 0xad, 0xbe, 0xef)
	truncatedFrame := bytes.Clone(valid[:len(valid)-1])
	badChecksum := bytes.Clone(valid)
	badChecksum[len(badChecksum)-1] ^= 0xff

	for _, test := range []struct {
		name    string
		encoded []byte
	}{
		{name: "single frame", encoded: valid},
		{name: "concatenated frames", encoded: concatenated},
		{name: "trailing input", encoded: trailingInput},
		{name: "truncated frame", encoded: truncatedFrame},
		{name: "bad checksum after frame data", encoded: badChecksum},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Use the incumbent DecodeAll path as the acceptance and output
			// reference for the new streaming verification path.
			expected, decodeErr := repo.decompressUnpacked(test.encoded)
			ciphertext := sealUnpackedForTest(t, repo, test.encoded)
			verifyErr := repo.verifyUnpacked(ciphertext, restic.IndexFile, expected)
			if (decodeErr == nil) != (verifyErr == nil) {
				t.Fatalf("streaming verification result differs from DecodeAll: DecodeAll error %v, verification error %v", decodeErr, verifyErr)
			}
		})
	}

	// A valid compressed payload with different authenticated plaintext must
	// still fail the exact comparison.
	changed := bytes.Clone(data)
	changed[len(changed)-2] ^= 1
	mismatching := append([]byte{2}, enc.EncodeAll(changed, nil)...)
	ciphertext := sealUnpackedForTest(t, repo, mismatching)
	if err := repo.verifyUnpacked(ciphertext, restic.IndexFile, data); err == nil || err.Error() != "data mismatch" {
		t.Fatalf("authenticated plaintext mismatch: got %v, want data mismatch", err)
	}
}

func sealUnpackedForTest(t *testing.T, repo *Repository, plaintext []byte) []byte {
	t.Helper()
	nonce := crypto.NewRandomNonce()
	ciphertext := append([]byte(nil), nonce...)
	return repo.Key().Seal(ciphertext, nonce, plaintext, nil)
}

type errorAfterByteReader struct {
	err  error
	done bool
}

func (r *errorAfterByteReader) Read(p []byte) (int, error) {
	if !r.done {
		r.done = true
		return copy(p, []byte("x")), nil
	}
	return 0, r.err
}

var _ io.Reader = (*errorAfterByteReader)(nil)
