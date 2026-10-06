package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// The table of TestStreamPack (blobs, expected number of backend requests,
// a short first read, the three invalid uses), run against both pack streams
// that production uses: streamPack for repack, copy and repair packs, where
// the handler runs after Load has returned, and streamPackWithPolicy for
// restore, where one request serves a pack range and is decoded in windows.
// TestStreamPack itself calls streamPack only.
func TestStreamPackTableOnProductionPaths(t *testing.T) {
	type streamFn func(ctx context.Context, load backendLoadFn, dec *zstd.Decoder, key *crypto.Key, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error
	for _, path := range []struct {
		name   string
		stream streamFn
	}{
		{"repack and repair", func(ctx context.Context, load backendLoadFn, dec *zstd.Decoder, key *crypto.Key, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error {
			// what Repository.loadBlobsFromPackReleased calls
			return streamPack(ctx, load, nil, dec, key, restic.ID{}, blobs, handle)
		}},
		{"restore", func(ctx context.Context, load backendLoadFn, dec *zstd.Decoder, key *crypto.Key, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error {
			// what Repository.loadBlobsFromPack calls for an ordinary restore worker
			return streamPackWithPolicy(ctx, load, nil, dec, key, restic.ID{}, blobs, handle, restoreStreamWindow, true)
		}},
	} {
		for _, version := range []uint{1, 2} {
			t.Run(fmt.Sprintf("%s/v%d", path.name, version), func(t *testing.T) {
				testStreamPackTable(t, version, func(ctx context.Context, load backendLoadFn, dec *zstd.Decoder, key *crypto.Key, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error {
					return path.stream(ctx, load, dec, key, blobs, handle)
				})
			})
		}
	}
}

func testStreamPackTable(t *testing.T, version uint, stream func(ctx context.Context, load backendLoadFn, dec *zstd.Decoder, key *crypto.Key, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	key := testKey(t)
	blobSizes := []int{1522811, 10, 5231, 18812, 123123, 12301, 892242, 28616, 13351, 252287, 188883, 18883}
	packfileBlobs, packfile := buildPackfileWithoutHeader(blobSizes, &key, version == 2)

	loadCalls := 0
	shortFirstLoad := false
	loadBytes := func(length int, offset int64) []byte {
		data := packfile
		if offset > int64(len(data)) {
			offset = 0
			length = 0
		}
		data = data[offset:]
		if length > len(data) {
			length = len(data)
		}
		if shortFirstLoad {
			length /= 2
			shortFirstLoad = false
		}
		return data[:length]
	}
	load := func(ctx context.Context, h backend.Handle, length int, offset int64, fn func(rd io.Reader) error) error {
		data := loadBytes(length, offset)
		loadCalls++
		err := fn(bytes.NewReader(data))
		if err == nil {
			return nil
		}
		var permanent *backoff.PermanentError
		if errors.As(err, &permanent) {
			return err
		}
		return fn(bytes.NewReader(loadBytes(length, offset)))
	}
	production := func(ctx context.Context, blobs pack.Blobs, handle func(restic.BlobHandle, []byte, error) error) error {
		return stream(ctx, load, dec, &key, blobs, handle)
	}

	regular := []struct {
		blobs          pack.Blobs
		calls          int
		shortFirstLoad bool
	}{
		{packfileBlobs[1:2], 1, false},
		{packfileBlobs[2:5], 1, false},
		{packfileBlobs[2:8], 1, false},
		{pack.Blobs{packfileBlobs[0], packfileBlobs[4], packfileBlobs[2]}, 1, false},
		{pack.Blobs{packfileBlobs[0], packfileBlobs[len(packfileBlobs)-1]}, 2, false},
		{packfileBlobs[:], 1, true},
	}
	for i, test := range regular {
		blobs := append(pack.Blobs(nil), test.blobs...)
		gotBlobs := make(map[restic.ID]int)
		wantBlobs := make(map[restic.ID]int)
		for _, blob := range blobs {
			wantBlobs[blob.ID] = 1
		}
		loadCalls = 0
		shortFirstLoad = test.shortFirstLoad
		err := production(t.Context(), blobs, func(blob restic.BlobHandle, buf []byte, err error) error {
			gotBlobs[blob.ID]++
			if id := restic.Hash(buf); !id.Equal(blob.ID) {
				t.Fatalf("v%d regular %d: wrong id %v for blob %v", version, i, id, blob.ID)
			}
			return err
		})
		if err != nil {
			t.Fatalf("v%d regular %d: %v", version, i, err)
		}
		if !cmp.Equal(wantBlobs, gotBlobs) {
			t.Fatalf("v%d regular %d: %s", version, i, cmp.Diff(wantBlobs, gotBlobs))
		}
		if loadCalls != test.calls {
			t.Errorf("v%d regular %d: %d load calls, the table of TestStreamPack expects %d", version, i, loadCalls, test.calls)
		}
	}
	shortFirstLoad = false

	invalid := []struct {
		blobs pack.Blobs
		err   string
	}{
		{pack.Blobs{packfileBlobs[3], packfileBlobs[8], packfileBlobs[3], packfileBlobs[4]}, "overlapping blobs in pack"},
		{pack.Blobs{{Offset: 123, Length: 20000}}, "ciphertext verification failed"},
		{pack.Blobs{{Offset: 123, Length: 10}}, "invalid blob length"},
	}
	for i, test := range invalid {
		err := production(t.Context(), test.blobs, func(_ restic.BlobHandle, _ []byte, err error) error { return err })
		if err == nil {
			t.Fatalf("v%d invalid %d: wanted error %q, got nil", version, i, test.err)
		}
		if !strings.Contains(err.Error(), test.err) {
			t.Fatalf("v%d invalid %d: error should contain %q but was %q", version, i, test.err, err)
		}
	}
}
