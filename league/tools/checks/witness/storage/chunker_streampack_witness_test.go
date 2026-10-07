package repository

// WITNESS (storage review).
// OS: any (pure Go). Expected on the candidate: all PASS.
//
//  1. TestWitnessChunkerDifferential (mechanism "exact / segmented chunker", the
//     re-implemented Rabin scan in chunker_exact.go and chunker_segments.go):
//     identical split points to github.com/restic/chunker BaseChunker for random,
//     constant, periodic and sparse streams, three polynomials, and buffer sizes
//     chosen around the 64 / 256 / 512 / 1024 byte edges of the segmented scan,
//     including the forced cut at MaxSize and a Reset in the middle of a chunk.
//  2. TestWitnessStockStreamPackTableOnProductionPath (mechanism "one request per
//     pack range, read in windows"): the stock TestStreamPack table calls the frozen
//     streamPack(), which production no longer uses (loadBlobsFromPack calls
//     streamPackWithPolicy -> streamPackCoalesced). This runs the same table,
//     both repository versions, against the production entry point.

import (
	"bytes"
	"context"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/cenkalti/backoff/v4"
	"github.com/google/go-cmp/cmp"
	"github.com/klauspost/compress/zstd"
	"github.com/restic/chunker"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestWitnessChunkerDifferential(t *testing.T) {
	rnd := rand.New(rand.NewSource(20261006))
	pols := []chunker.Pol{testChunkerPol}
	for i := 0; i < 2; i++ {
		p, err := chunker.RandomPolynomial()
		if err != nil {
			t.Fatal(err)
		}
		pols = append(pols, p)
	}

	random := make([]byte, 24<<20)
	rnd.Read(random)
	sparse := make([]byte, 12<<20)
	for i := 0; i < 40; i++ {
		at := rnd.Intn(len(sparse) - 70000)
		rnd.Read(sparse[at : at+1+rnd.Intn(65536)])
	}
	streams := []struct {
		name string
		data []byte
		fine bool // only small buffers: thousands of calls across the scan's edges
	}{
		{"random", random, false},
		{"ones (forced MaxSize cuts)", bytes.Repeat([]byte{1}, 20<<20), false},
		{"zeros", make([]byte, 3<<20), false},
		{"period7", bytes.Repeat([]byte{9, 200, 3, 77, 0, 255, 18}, (20<<20)/7), false},
		{"sparse", sparse, false},
		{"random, small buffers", random[:6<<20], true},
		{"sparse, small buffers", sparse[:6<<20], true},
		{"ones, small buffers", bytes.Repeat([]byte{1}, 12<<20), true},
	}
	edges := []int{1, 2, 63, 64, 65, 127, 255, 256, 257, 511, 512, 513, 575, 576, 577, 1023, 1024, 1025, 1087, 1088,
		1535, 1536, 1600, 4096, 65536, 524224, 524288, 524289, 1 << 20, 2<<20 + 3}

	for _, pol := range pols {
		for _, s := range streams {
			ref := chunker.NewBase(pol)
			seg := newSegmentedChunkerFactory(&chunkerFactory{pol: pol}).NewChunker()
			exact := newExactChunkerFactory(&chunkerFactory{pol: pol}).NewChunker()
			if _, ok := seg.(*segmentedChunker); !ok {
				t.Fatalf("segmented chunker not selected for pol %v", pol)
			}
			pos, cuts, calls := 0, 0, 0
			for pos < len(s.data) {
				n := edges[rnd.Intn(len(edges))]
				if rnd.Intn(3) == 0 {
					n = 1 + rnd.Intn(3<<20)
				}
				if s.fine {
					n = edges[rnd.Intn(24)] // 1 .. 4096
					if rnd.Intn(2) == 0 {
						n = 1 + rnd.Intn(6000)
					}
				}
				n = min(n, len(s.data)-pos)
				buf := s.data[pos : pos+n]
				want := ref.NextSplitPoint(buf)
				if got := seg.NextSplitPoint(buf); got != want {
					t.Fatalf("pol %v %s: segmented cut %d, BaseChunker %d (offset %d, buffer %d)", pol, s.name, got, want, pos, n)
				}
				if got := exact.NextSplitPoint(buf); got != want {
					t.Fatalf("pol %v %s: exact cut %d, BaseChunker %d (offset %d, buffer %d)", pol, s.name, got, want, pos, n)
				}
				calls++
				if want >= 0 {
					pos += want
					cuts++
				} else {
					pos += n
				}
				// small buffers: one Reset early on, so chunks still reach MaxSize
				if (!s.fine && calls%97 == 0) || (s.fine && calls == 700) {
					// a new file starts in the middle of a chunk
					ref.Reset(pol)
					seg.Reset()
					exact.Reset()
				}
			}
			if cuts == 0 {
				t.Fatalf("pol %v %s: no cut seen", pol, s.name)
			}
			t.Logf("pol %v %-27s %4d cuts in %5d calls: identical", pol, s.name, cuts, calls)
		}
	}
}

func TestWitnessStockStreamPackTableOnProductionPath(t *testing.T) {
	for _, version := range []uint{1, 2} {
		witnessStreamPackTable(t, version)
	}
}

func witnessStreamPackTable(t *testing.T, version uint) {
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
		// what Repository.loadBlobsFromPack calls for an ordinary caller
		return streamPackWithPolicy(ctx, load, nil, dec, &key, restic.ID{}, blobs, handle, restoreStreamWindow, true)
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
			t.Errorf("v%d regular %d: %d load calls, stock table expects %d", version, i, loadCalls, test.calls)
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
