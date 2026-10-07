package repository

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/restic"
)

func TestCompressLockUnpackedUsesRawZstdFrame(t *testing.T) {
	repo := &Repository{cfg: restic.Config{Version: 2}}
	data := []byte(`{"pid":123,"exclusive":false}`)

	compressed, err := repo.compressLockUnpacked(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(compressed) == 0 || compressed[0] != 2 {
		t.Fatalf("unexpected unpacked-file version prefix: %v", compressed)
	}
	if repo.enc != nil {
		t.Fatal("compressLockUnpacked initialized the repository zstd encoder")
	}

	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()

	got, err := decoder.DecodeAll(compressed[1:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("decoded lock data differs: got %q, want %q", got, data)
	}
}

func TestAppendRawZstdFrame(t *testing.T) {
	decoder, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer decoder.Close()

	for _, size := range []int{0, 1, 255, 256, 65791, 65792, 128 * 1024, 128*1024 + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			want := make([]byte, size)
			for i := range want {
				want[i] = byte(i * 31)
			}

			frame := appendRawZstdFrame(nil, want)
			got, err := decoder.DecodeAll(frame, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("decoded frame differs for %d bytes", size)
			}
		})
	}
}
