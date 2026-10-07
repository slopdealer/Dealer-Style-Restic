package repository

import (
	"bytes"
	"context"
	"crypto/sha256"
	stdErrors "errors"
	"io"
	"math/rand"
	"sync"
	"testing"

	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/test"
)

func TestPackerWriteThroughHashIncludesMergedPayloadAndFinalHeader(t *testing.T) {
	pm := newPackerManager(crypto.NewRandomKey(), restic.DataBlob, DefaultPackSize, 2, func(context.Context, restic.BlobType, *packer) error {
		return nil
	})
	pm.hashOnWrite = true

	first, err := pm.newPacker()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.tmpfile.Close() })
	second, err := pm.newPacker()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = second.tmpfile.Close() })

	firstPayload := []byte("first pack payload")
	secondPayload := []byte("merged pack payload")
	if _, err := first.Add(restic.DataBlob, restic.Hash(firstPayload), firstPayload, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := second.Add(restic.DataBlob, restic.Hash(secondPayload), secondPayload, 0); err != nil {
		t.Fatal(err)
	}
	if err := second.bufWr.Flush(); err != nil {
		t.Fatal(err)
	}
	if _, err := second.tmpfile.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if err := first.Merge(second.Packer, second.tmpfile); err != nil {
		t.Fatal(err)
	}
	if err := first.Finalize(); err != nil {
		t.Fatal(err)
	}
	if err := first.bufWr.Flush(); err != nil {
		t.Fatal(err)
	}

	if _, err := first.tmpfile.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	fileHash := sha256.New()
	if _, err := io.Copy(fileHash, first.tmpfile); err != nil {
		t.Fatal(err)
	}
	if got, want := first.hashWr.Sum(nil), fileHash.Sum(nil); !bytes.Equal(got, want) {
		t.Fatalf("write-through hash %x does not match finalized pack hash %x", got, want)
	}
}

func TestPackerManagerClosesMergedFilesWhenQueueFails(t *testing.T) {
	queueErr := stdErrors.New("queue stopped")
	pm := newPackerManager(crypto.NewRandomKey(), restic.DataBlob, 1024, 2, func(context.Context, restic.BlobType, *packer) error {
		return queueErr
	})

	first, err := pm.newPacker()
	if err != nil {
		t.Fatal(err)
	}
	second, err := pm.newPacker()
	if err != nil {
		_ = first.tmpfile.Close()
		t.Fatal(err)
	}
	pm.packers[0] = first
	pm.packers[1] = second

	for _, p := range []*packer{first, second} {
		payload := []byte("valid pack data")
		if _, err := p.Add(restic.DataBlob, restic.Hash(payload), payload, 0); err != nil {
			t.Fatal(err)
		}
	}

	if err := pm.Flush(context.Background()); !stdErrors.Is(err, queueErr) {
		t.Fatalf("Flush() error = %v, want %v", err, queueErr)
	}

	for i, p := range []*packer{first, second} {
		if _, err := p.tmpfile.Write([]byte("must fail after cleanup")); err == nil {
			t.Errorf("packer %d temporary file remains open after failed flush", i)
		}
	}
}

func randomID(rd io.Reader) restic.ID {
	id := restic.ID{}
	_, err := io.ReadFull(rd, id[:])
	if err != nil {
		panic(err)
	}
	return id
}

const maxBlobSize = 1 << 20

func fillPacks(t testing.TB, rnd *rand.Rand, pm *packerManager, buf []byte) (bytes int) {
	for range 102 {
		l := rnd.Intn(maxBlobSize)
		id := randomID(rnd)
		buf = buf[:l]
		// Only change a few bytes so we know we're not benchmarking the RNG.
		rnd.Read(buf[:min(l, 4)])

		n, err := pm.SaveBlob(context.TODO(), restic.DataBlob, id, buf, 0)
		if err != nil {
			t.Fatal(err)
		}
		if n != l+37 && n != l+37+36 {
			t.Errorf("Add() returned invalid number of bytes: want %v, got %v", l, n)
		}
		bytes += n
	}
	err := pm.Flush(context.TODO())
	if err != nil {
		t.Fatal(err)
	}
	return bytes
}

const randomSeed = 23

var (
	once      sync.Once
	totalSize int64
)

func TestPackerManager(t *testing.T) {
	bytes := testPackerManager(t)
	once.Do(func() { totalSize = bytes })
}

func testPackerManager(t testing.TB) int64 {
	rnd := rand.New(rand.NewSource(randomSeed))

	savedBytes := 0
	pm := newPackerManager(crypto.NewRandomKey(), restic.DataBlob, DefaultPackSize, defaultPackerCount, func(ctx context.Context, tp restic.BlobType, p *packer) error {
		err := p.Finalize()
		if err != nil {
			return err
		}
		savedBytes += int(p.Size())
		return nil
	})

	blobBuf := make([]byte, maxBlobSize)

	bytes := fillPacks(t, rnd, pm, blobBuf)
	// bytes does not include the last pack headers
	test.Assert(t, savedBytes == (bytes+36) || savedBytes == (bytes+72), "unexpected number of saved bytes, got %v, expected %v", savedBytes, bytes)

	t.Logf("saved %d bytes", bytes)
	return int64(bytes)
}

func TestPackerManagerWithOversizeBlob(t *testing.T) {
	packFiles := 0
	sizeLimit := uint(512 * 1024)
	pm := newPackerManager(crypto.NewRandomKey(), restic.DataBlob, sizeLimit, defaultPackerCount, func(ctx context.Context, tp restic.BlobType, p *packer) error {
		packFiles++
		return nil
	})

	for _, i := range []uint{sizeLimit / 2, sizeLimit, sizeLimit / 3} {
		_, err := pm.SaveBlob(context.TODO(), restic.DataBlob, restic.ID{}, make([]byte, i), 0)
		test.OK(t, err)
	}
	test.OK(t, pm.Flush(context.TODO()))

	// oversized blob must be stored in a separate packfile
	test.Assert(t, packFiles == 2, "unexpected number of packfiles %v, expected 2", packFiles)
}

func BenchmarkPackerManager(t *testing.B) {
	// Run testPackerManager if it hasn't run already, to set totalSize.
	once.Do(func() {
		totalSize = testPackerManager(t)
	})

	rnd := rand.New(rand.NewSource(randomSeed))
	blobBuf := make([]byte, maxBlobSize)

	t.ReportAllocs()
	t.SetBytes(totalSize)
	t.ResetTimer()

	for i := 0; i < t.N; i++ {
		rnd.Seed(randomSeed)
		pm := newPackerManager(crypto.NewRandomKey(), restic.DataBlob, DefaultPackSize, defaultPackerCount, func(ctx context.Context, t restic.BlobType, p *packer) error {
			return nil
		})
		fillPacks(t, rnd, pm, blobBuf)
	}
}
