package repository

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// This is the selected-base pipe implementation, with optional producer
// buffering as a competing construction. It remains independent of the new
// verification helper. Decoder errors precede encoder errors; only successful
// encoding drains the remaining decoder output.
func verifyIndexPipeReference(compressed []byte, encode func(io.Writer) error, buffered bool) error {
	dec, err := zstd.NewReader(bytes.NewReader(compressed), zstd.WithDecoderConcurrency(1), zstd.WithDecoderMaxMemory(16*1024*1024*1024))
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}
	defer dec.Close()
	expected, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		var encodeErr error
		if buffered {
			w := bufio.NewWriterSize(writer, 32*1024)
			encodeErr = encode(w)
			// Flush even on encoder failure: otherwise the accepted prefix is
			// lost and decoder/encoder error precedence can change.
			flushErr := w.Flush()
			if encodeErr == nil {
				encodeErr = flushErr
			}
		} else {
			encodeErr = encode(writer)
		}
		_ = writer.CloseWithError(encodeErr)
		done <- encodeErr
	}()
	cmp := streamCompareWriter{actual: dec, buf: make([]byte, 32*1024), equal: true}
	_, copyErr := io.Copy(&cmp, expected)
	_ = expected.Close()
	encodeErr := <-done
	if cmp.err != nil {
		return fmt.Errorf("decompression failed: %w", cmp.err)
	}
	if encodeErr != nil {
		return fmt.Errorf("encoding failed: %w", encodeErr)
	}
	if copyErr != nil {
		return fmt.Errorf("encoding failed: %w", copyErr)
	}
	extra, err := io.Copy(io.Discard, dec)
	if err != nil {
		return fmt.Errorf("decompression failed: %w", err)
	}
	if extra != 0 || !cmp.equal {
		return errors.New("data mismatch")
	}
	return nil
}

func TestIndexVerifyDirectBoundaries(t *testing.T) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	data := bytes.Repeat([]byte("index\x00entry\xff,"), 9000)
	frame := enc.EncodeAll(data, nil)
	badCRC := bytes.Clone(frame)
	badCRC[len(badCRC)-1] ^= 0xff
	concat := enc.EncodeAll(data[:12345], nil)
	concat = append(concat, enc.EncodeAll(data[12345:], nil)...)
	for _, tc := range []struct {
		name                 string
		compressed, expected []byte
		want                 string
	}{
		{"equal", frame, data, ""},
		{"empty", enc.EncodeAll(nil, nil), nil, ""},
		{"short", frame, data[:len(data)-1], "data mismatch"},
		{"long", frame, append(bytes.Clone(data), 'x'), "data mismatch"},
		{"mismatch", frame, bytes.Repeat([]byte("x"), len(data)), "data mismatch"},
		{"concatenated", concat, data, ""},
		{"checksum", badCRC, data, "decompression failed:"},
		{"checksum after mismatch", badCRC, bytes.Repeat([]byte("x"), len(data)), "decompression failed:"},
		{"checksum on drain", badCRC, nil, "decompression failed:"},
		{"trailing junk", append(bytes.Clone(frame), 0xde, 0xad), data, "decompression failed:"},
		{"truncated", frame[:len(frame)-1], data, "decompression failed:"},
	} {
		for _, fragment := range []int{1, 7, 32768, 65537, len(data) + 1} {
			t.Run(fmt.Sprintf("%s/%d", tc.name, fragment), func(t *testing.T) {
				encode := func(w io.Writer) error {
					for off := 0; off < len(tc.expected); {
						end := min(off+fragment, len(tc.expected))
						if _, err := w.Write(tc.expected[off:end]); err != nil {
							return err
						}
						off = end
					}
					return nil
				}
				got := verifyCompressedIndexStream(tc.compressed, encode)
				ref := verifyIndexPipeReference(tc.compressed, encode, false)
				buffered := verifyIndexPipeReference(tc.compressed, encode, true)
				if errorText(got) != errorText(ref) || errorText(got) != errorText(buffered) {
					t.Fatalf("direct %v, reference %v, buffered %v", got, ref, buffered)
				}
				if tc.want == "" && got != nil || tc.want != "" && (got == nil || !bytes.HasPrefix([]byte(got.Error()), []byte(tc.want))) {
					t.Fatalf("got %v, want %q", got, tc.want)
				}
			})
		}
	}
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestIndexVerifyDirectEncoderFailures(t *testing.T) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderConcurrency(1), zstd.WithEncoderCRC(true))
	if err != nil {
		t.Fatal(err)
	}
	defer enc.Close()
	data := bytes.Repeat([]byte("payload"), 10000)
	frame := enc.EncodeAll(data, nil)
	bad := bytes.Clone(frame)
	bad[len(bad)-1] ^= 0xff
	wantErr := errors.New("encoder failure")
	for _, compressed := range [][]byte{frame, bad, {0xff}} {
		for _, prefix := range []int{0, 1, 32768, len(data)} {
			encode := func(w io.Writer) error {
				if _, err := w.Write(data[:prefix]); err != nil {
					return err
				}
				return wantErr
			}
			got := verifyCompressedIndexStream(compressed, encode)
			ref := verifyIndexPipeReference(compressed, encode, false)
			buffered := verifyIndexPipeReference(compressed, encode, true)
			if errorText(got) != errorText(ref) || errorText(got) != errorText(buffered) {
				t.Fatalf("prefix %d: direct %v, reference %v, buffered %v", prefix, got, ref, buffered)
			}
			if got == nil {
				t.Fatal("encoder failure accepted")
			}
		}
	}
}

func TestIndexVerifyDirectAuthenticationAndCancellation(t *testing.T) {
	repo, _ := TestRepositoryWithBackend(t, nil, 2, Options{})
	// Authentication failure must not invoke the encoder.
	buf := sealUnpackedForTest(t, repo, []byte{2, 0xff})
	buf[len(buf)-1] ^= 1
	if err := repo.verifyCompressedIndexStream(buf, func(io.Writer) error { t.Fatal("encoder called before authentication"); return nil }); err == nil {
		t.Fatal("unauthenticated ciphertext accepted")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := repo.saveIndexStream(ctx, func(w io.Writer) error { _, err := w.Write([]byte("{\"packs\":[]}\n")); return err })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled save: %v", err)
	}
}

// Full save includes both index encodes, compression, encryption, verification,
// ciphertext hashing and backend Save. The reference and buffered arms differ
// only in verification, not in the first encoding or persisted representation.
func BenchmarkIndexVerifyCompleteSave(b *testing.B) {
	for _, shape := range []struct{ packs, blobs int }{{1000, 1}, {10, 1000}} {
		idx := index.NewIndex()
		for i := 0; i < shape.packs; i++ {
			blobs := make(pack.Blobs, shape.blobs)
			for j := range blobs {
				blobs[j] = pack.Blob{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.Hash([]byte(fmt.Sprint(i, ":", j)))}, Offset: uint(j * 1234), Length: 1234}
			}
			idx.StorePack(restic.Hash([]byte(fmt.Sprint("pack", i))), blobs)
		}
		for _, mode := range []string{"direct", "pipe", "buffered"} {
			b.Run(fmt.Sprintf("packs%d-blobs%d/%s", shape.packs, shape.blobs, mode), func(b *testing.B) {
				repo, _ := TestRepositoryWithBackend(b, nil, 2, Options{})
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					if mode == "direct" {
						id, err := repo.saveIndexStream(context.Background(), idx.Encode)
						if err != nil {
							b.Fatal(err)
						}
						if err := repo.be.Remove(context.Background(), backend.Handle{Type: backend.IndexFile, Name: id.String()}); err != nil {
							b.Fatal(err)
						}
						continue
					}
					var payload bytes.Buffer
					payload.WriteByte(2)
					enc, err := zstd.NewWriter(&payload, zstd.WithEncoderLevel(repo.zstdEncoderLevel()), zstd.WithEncoderCRC(false), zstd.WithWindowSize(512*1024), zstd.WithEncoderConcurrency(1))
					if err != nil {
						b.Fatal(err)
					}
					if err := idx.Encode(enc); err != nil {
						b.Fatal(err)
					}
					if err := enc.Close(); err != nil {
						b.Fatal(err)
					}
					nonce := crypto.NewRandomNonce()
					ciphertext := crypto.NewBlobBuffer(payload.Len())[:0]
					ciphertext = append(ciphertext, nonce...)
					ciphertext = repo.key.Seal(ciphertext, nonce, payload.Bytes(), nil)
					plain, err := repo.key.Open(nil, nonce, ciphertext[len(nonce):], nil)
					if err != nil {
						b.Fatal(err)
					}
					if err := verifyIndexPipeReference(plain[1:], idx.Encode, mode == "buffered"); err != nil {
						b.Fatal(err)
					}
					id := restic.Hash(ciphertext)
					if err := repo.be.Save(context.Background(), backend.Handle{Type: backend.IndexFile, Name: id.String()}, backend.NewByteReader(ciphertext, repo.be.Hasher())); err != nil {
						b.Fatal(err)
					}
					if err := repo.be.Remove(context.Background(), backend.Handle{Type: backend.IndexFile, Name: id.String()}); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
