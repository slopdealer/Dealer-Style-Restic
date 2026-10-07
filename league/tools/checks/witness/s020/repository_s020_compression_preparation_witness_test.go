package repository

// WITNESS (S020 review, mechanism 1: backup "compression preparation").
//
// internal/repository/compression_preparation.go compresses chunks of the
// backup source during the lock wait and hands the compressed bytes to
// saveAndEncrypt later (repository.go:439). The file is not OS-specific (only
// the walk over the source is Linux-only), so its contract can be run here.
//
// TestWitnessS020CompressionPreparationBytes shows:
//  1. a prepared entry holds exactly the bytes the repository's own encoder
//     produces for the same chunk: what reaches the pack is byte for byte what
//     the ordinary path stores (repository format unchanged);
//  2. a blob saved through a prepared entry reads back intact;
//  3. an entry whose compressed bytes belong to OTHER data of the same length
//     (a source file that changed between the lock wait and the backup, or a
//     damaged shelf) is rejected by the decode-and-compare step and the blob is
//     stored correctly by the ordinary path;
//  4. relation to repair R4: the shelf has its own copy of the 16-window
//     sampler and takes its four constants from the sampler R4 deletes
//     (autoCompressionMinSampledSize, ...SampleWindow, ...SampleWindows,
//     ...SampleSize). For the R4 witness blob (one random 64 KiB block sixteen
//     times) the shelf prepares nothing, so it does not store that blob raw by
//     itself: whatever saveAndEncrypt decides, decides.
// Expected on S020: PASS.
//
// OS: any. Run from the tree root:
//   go test -run TestWitnessS020CompressionPreparationBytes -count=1 -p 2 -v ./internal/repository

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/restic/chunker"
	"github.com/restic/restic/internal/backend/mem"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

func s020AutoRepo(t *testing.T) *Repository {
	t.Helper()
	TestUseLowSecurityKDFParameters(t)
	r, err := New(mem.New(), Options{Compression: CompressionAuto})
	if err != nil {
		t.Fatal(err)
	}
	pol := chunker.Pol(0x3DA3358B4DC173)
	if err := r.Init(context.Background(), 2, rtest.TestPassword, &pol); err != nil {
		t.Fatal(err)
	}
	return r
}

// s020Text returns log-like text that zstd shrinks about tenfold (the shelf
// holds at most 256 KiB of compressed output, so the fixture must compress well).
func s020Text(rng *rand.Rand, n int) []byte {
	levels := []string{"INFO", "WARN", "DEBUG", "ERROR"}
	var buf bytes.Buffer
	line := rng.Intn(1 << 20)
	for buf.Len() < n {
		line++
		fmt.Fprintf(&buf, "2026-10-07T12:%02d:%02d %s request id=%07d status=%d bytes=%d\n",
			line/60%60, line%60, levels[rng.Intn(len(levels))], line, 200+rng.Intn(4), 4096*(1+rng.Intn(3)))
	}
	return buf.Bytes()[:n]
}

func TestWitnessS020CompressionPreparationBytes(t *testing.T) {
	ctx := context.Background()
	rng := rand.New(rand.NewSource(20))
	repo := s020AutoRepo(t)
	shelf := repo.NewCompressionPreparation()
	if shelf == nil {
		t.Fatal("no shelf for a version 2 repository with compression auto")
	}

	// "files" of 600 to 1000 KiB: one chunk each, or a first chunk and a tail
	var files [][]byte
	for i := 0; i < 6; i++ {
		files = append(files, s020Text(rng, (600+rng.Intn(400))<<10))
	}
	// the R4 witness shape: one incompressible 64 KiB block sixteen times
	block := make([]byte, 64<<10)
	rng.Read(block)
	r4 := bytes.Repeat(block, 16)[:1000<<10]

	for _, f := range files {
		shelf.AddReader(ctx, bytes.NewReader(f))
	}
	shelf.FinishProducer()
	stats := shelf.Stats()
	t.Logf("shelf: %d entries, stats %+v", len(shelf.entries), stats)
	if len(shelf.entries) < 2 {
		t.Fatalf("%d entries prepared: the fixture does not exercise the mechanism", len(shelf.entries))
	}

	// 4. the R4 blob on a shelf of its own (the first one is full by now)
	r4Shelf := repo.NewCompressionPreparation()
	r4Shelf.AddReader(ctx, bytes.NewReader(r4))
	r4Shelf.FinishProducer()
	r4Stats := r4Shelf.Stats()
	t.Logf("R4 blob: chunks=%d attempts=%d rejected=%d entries=%d", r4Stats.Chunks, r4Stats.Attempts, r4Stats.Rejected, len(r4Shelf.entries))
	if r4Stats.Attempts == 0 {
		t.Fatal("the R4 blob was not examined: the fixture is wrong")
	}
	if len(r4Shelf.entries) != 0 || r4Stats.Rejected != r4Stats.Attempts {
		t.Errorf("the shelf prepared an entry for the long-range-redundant blob of repair R4")
	}
	r4Shelf.Release()

	// 1. every entry holds what the repository's encoder produces
	raws := make(map[restic.ID][]byte)
	for _, e := range shelf.entries {
		var raw []byte
		for _, f := range files {
			if e.length <= len(f) && restic.Hash(f[:e.length]) == e.id {
				raw = f[:e.length]
			} else if e.length <= len(f) && restic.Hash(f[len(f)-e.length:]) == e.id {
				raw = f[len(f)-e.length:]
			}
		}
		if raw == nil {
			t.Fatalf("entry %v (%d bytes) is not a first or last chunk of a fixture file", e.id.Str(), e.length)
		}
		raws[e.id] = raw
		ordinary := repo.getZstdEncoder().EncodeAll(raw, nil)
		if !bytes.Equal(ordinary, e.encoded) {
			t.Errorf("entry %v: prepared bytes (%d) differ from the repository encoder's output (%d)", e.id.Str(), len(e.encoded), len(ordinary))
		}
	}

	// 3. one entry gets the compressed form of other data of the same length
	var hostile restic.ID
	for i := range shelf.entries {
		e := &shelf.entries[i]
		other := s020Text(rng, e.length)
		e.encoded = repo.getZstdEncoder().EncodeAll(other, nil)
		hostile = e.id
		break
	}

	shelf.Activate()
	err := repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		for id, raw := range raws {
			got, known, _, err := uploader.SaveBlob(ctx, restic.DataBlob, raw, restic.ID{}, false)
			if err != nil {
				return err
			}
			if got != id || known {
				t.Errorf("blob %v: saved as %v, known=%v", id.Str(), got.Str(), known)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	stats = shelf.Stats()
	t.Logf("after the backup: matches=%d consumed=%d invalid=%d", stats.Matches, stats.Consumed, stats.Invalid)
	if stats.Invalid != 1 {
		t.Errorf("the entry with foreign compressed bytes was not rejected exactly once: invalid=%d", stats.Invalid)
	}
	if int(stats.Consumed) != len(raws)-1 {
		t.Errorf("consumed %d prepared entries, expected %d", stats.Consumed, len(raws)-1)
	}
	shelf.Release()

	// 2. and 3.: everything reads back, also through a fresh repository handle
	fresh := TestOpenBackend(t, repo.be)
	if err := fresh.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
		t.Fatal(err)
	}
	for id, raw := range raws {
		buf, err := fresh.LoadBlob(ctx, restic.BlobHandle{Type: restic.DataBlob, ID: id}, nil)
		if err != nil {
			t.Fatalf("blob %v (hostile entry: %v) does not load: %v", id.Str(), id == hostile, err)
		}
		if !bytes.Equal(buf, raw) {
			t.Fatalf("blob %v (hostile entry: %v) reads back with other content", id.Str(), id == hostile)
		}
	}
	TestCheckRepo(t, fresh)
}
