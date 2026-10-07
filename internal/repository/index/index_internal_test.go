package index

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

func TestEncodeMatchesLegacyPackListEncoding(t *testing.T) {
	idx := NewIndex()
	firstPack := restic.NewRandomID()
	secondPack := restic.NewRandomID()
	idx.StorePack(firstPack, pack.Blobs{
		{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.NewRandomID()}, Offset: 0, Length: 17},
		{BlobHandle: restic.BlobHandle{Type: restic.TreeBlob, ID: restic.NewRandomID()}, Offset: 17, Length: 29},
	})
	idx.StorePack(secondPack, pack.Blobs{
		{BlobHandle: restic.BlobHandle{Type: restic.TreeBlob, ID: restic.NewRandomID()}, Offset: 0, Length: 31},
		{BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: restic.NewRandomID()}, Offset: 31, Length: 43, UncompressedLength: 59},
	})

	legacyPacks, err := idx.generatePackList()
	if err != nil {
		t.Fatal(err)
	}
	var want bytes.Buffer
	if err := json.NewEncoder(&want).Encode(jsonIndex{Packs: legacyPacks}); err != nil {
		t.Fatal(err)
	}

	var got bytes.Buffer
	if err := idx.Encode(&got); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Fatalf("encoded index differs from pack-list JSON\n got: %s\nwant: %s", got.Bytes(), want.Bytes())
	}

	var partial indexPartialWriter
	if err := idx.Encode(&partial); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(partial.Bytes(), want.Bytes()) {
		t.Fatalf("partial-write index differs from pack-list JSON\n got: %s\nwant: %s", partial.Bytes(), want.Bytes())
	}
}

type indexShortWriter struct{}

func (indexShortWriter) Write([]byte) (int, error) { return 0, nil }

type indexPartialWriter struct {
	bytes.Buffer
}

func (w *indexPartialWriter) Write(p []byte) (int, error) {
	if len(p) > 1 {
		p = p[:1]
	}
	return w.Buffer.Write(p)
}

func TestEncodeReturnsShortWrite(t *testing.T) {
	idx := NewIndex()
	err := idx.Encode(indexShortWriter{})
	if err != io.ErrShortWrite {
		t.Fatalf("Encode error = %v, want %v", err, io.ErrShortWrite)
	}
}

func TestIndexOversized(t *testing.T) {
	idx := NewIndex()

	// Add blobs up to indexMaxBlobs + pack.MaxHeaderEntries - 1
	packID := idx.addToPacks(restic.NewRandomID())
	id := restic.NewRandomID()
	for i := uint(0); i < indexMaxBlobs+pack.MaxHeaderEntries-1; i++ {
		// Directly modify ID to avoid benchmarking NewRandomID
		id[0] = byte(i)
		id[1] = byte(i >> 8)
		id[2] = byte(i >> 16)
		id[3] = byte(i >> 24)

		idx.store(packID, pack.Blob{
			BlobHandle: restic.BlobHandle{
				Type: restic.DataBlob,
				ID:   id,
			},
			Length: 100,
			Offset: uint(i) * 100,
		})
	}

	rtest.Assert(t, !Oversized(idx), "index should not be considered oversized")

	// Add one more blob to exceed the limit
	idx.store(packID, pack.Blob{
		BlobHandle: restic.BlobHandle{
			Type: restic.DataBlob,
			ID:   restic.NewRandomID(),
		},
		Length: 100,
		Offset: uint(indexMaxBlobs+pack.MaxHeaderEntries) * 100,
	})

	rtest.Assert(t, Oversized(idx), "index should be considered oversized")
}
