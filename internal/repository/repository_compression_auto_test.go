package repository

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"testing"

	"github.com/restic/restic/internal/repository/crypto"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/test"
)

// Under the default CompressionAuto every data blob is stored as stock stores
// it: the encoder's output for the whole blob (EncodeAll, same encoder
// options), whatever its size. A blob that does not compress is stored in
// compressed form too, a few bytes larger than its data, so that the index
// knows no "uncompressed" blob that `prune --repack-uncompressed` would
// rewrite on every run.
//
// The inputs are built against decisions that look at a part of the blob
// only: redundancy at long range, short compressible stretches between
// incompressible ones, and compressible data behind an incompressible prefix.

type autoCompressionCase struct {
	name string
	data []byte
}

func autoCompressionCases() []autoCompressionCase {
	rnd := rand.New(rand.NewSource(20261006))
	random := func(n int) []byte {
		buf := make([]byte, n)
		rnd.Read(buf)
		return buf
	}
	text := func(n int) []byte {
		words := []string{"backup", "snapshot", "repository", "restore", "index", "pack", "blob", "tree", "the", "of", "and", "\n"}
		var buf bytes.Buffer
		for buf.Len() < n {
			buf.WriteString(words[rnd.Intn(len(words))])
			buf.WriteByte(' ')
		}
		return buf.Bytes()[:n]
	}
	const KiB, MiB = 1 << 10, 1 << 20

	var cases []autoCompressionCase
	add := func(name string, data []byte) {
		cases = append(cases, autoCompressionCase{name, data})
	}

	// one incompressible block, repeated: the redundancy is only visible
	// across the block distance
	for _, block := range []int{3 * KiB, 4 * KiB, 64 * KiB, 100*KiB + 7, 256 * KiB, 500 * KiB} {
		for _, size := range []int{512 * KiB, MiB, 2*MiB + 333} {
			data := bytes.Repeat(random(block), size/block+1)[:size]
			add(fmt.Sprintf("random block of %d repeated to %d", block, size), data)
		}
	}
	// one stretch of an incompressible blob occurs twice
	for _, distance := range []int{3 * KiB, 70 * KiB, 300 * KiB, 511 * KiB} {
		for _, length := range []int{8 * KiB, 100 * KiB} {
			data := random(MiB)
			start := 200*KiB + 123
			copy(data[start+distance:start+distance+length], data[start:start+length])
			add(fmt.Sprintf("%d bytes repeated once at distance %d", length, distance), data)
		}
	}
	// sparse runs inside incompressible data, also where sixteen evenly spread
	// windows of 2 KiB do not look
	for _, run := range []int{600, 8 * KiB, 30 * KiB, 100 * KiB} {
		for _, at := range []int{3 * KiB, 80 * KiB, 500*KiB + 11, MiB - run} {
			data := random(MiB)
			clear(data[at : at+run])
			add(fmt.Sprintf("zero run of %d at %d", run, at), data)
		}
	}
	data := random(2 * MiB)
	for at := 70 * KiB; at+4*KiB < len(data); at += 140 * KiB {
		clear(data[at : at+4*KiB])
	}
	add("many zero runs of 4 KiB", data)
	// text behind an incompressible prefix, and inside incompressible data
	for _, textLen := range []int{6 * KiB, 40 * KiB, 400 * KiB} {
		add(fmt.Sprintf("%d bytes of text after a random prefix", textLen), append(random(600*KiB), text(textLen)...))
		data := random(MiB)
		copy(data[333*KiB+5:], text(textLen))
		add(fmt.Sprintf("%d bytes of text inside random data", textLen), data)
	}
	// the ordinary cases
	add("random 1 MiB", random(MiB))
	add("random 8 MiB", random(8*MiB))
	add("random, exactly 512 KiB", random(512*KiB))
	add("random, one byte below 512 KiB", random(512*KiB-1))
	add("random 4 KiB", random(4*KiB))
	add("text 1 MiB", text(MiB))
	add("zeros 1 MiB", make([]byte, MiB))

	// random mixtures of incompressible, zero, text and repeated segments
	for i := 0; i < 24; i++ {
		size := 512*KiB + rnd.Intn(MiB)
		data := make([]byte, 0, size)
		for len(data) < size {
			n := 1 + rnd.Intn(200*KiB)
			if rnd.Intn(4) != 0 {
				n = 1 + rnd.Intn(16*KiB)
			}
			n = min(n, size-len(data))
			switch kind := rnd.Intn(8); {
			case kind == 0:
				data = append(data, make([]byte, n)...)
			case kind == 1:
				data = append(data, text(n)...)
			case kind == 2 && len(data) > n:
				from := rnd.Intn(len(data) - n + 1)
				data = append(data, data[from:from+n]...)
			default:
				data = append(data, random(n)...)
			}
		}
		add(fmt.Sprintf("mixture %d of %d bytes", i, size), data)
	}
	return cases
}

func TestCompressionAutoStoresWhatStockStores(t *testing.T) {
	repo, _, _ := TestRepositoryWithVersion(t, 2)
	// the production default; the test helper replaces Auto by Fastest
	repo.opts.Compression = CompressionAuto

	cases := autoCompressionCases()
	ids := make([]restic.ID, len(cases))
	test.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		for i, c := range cases {
			id, _, _, err := up.SaveBlob(ctx, restic.DataBlob, c.data, restic.ID{}, false)
			if err != nil {
				return err
			}
			ids[i] = id
		}
		return nil
	}))

	incompressible := 0
	for i, c := range cases {
		h := restic.BlobHandle{Type: restic.DataBlob, ID: ids[i]}
		pbs := repo.LookupBlob(h)
		if len(pbs) != 1 {
			t.Fatalf("%s: stored %d times", c.name, len(pbs))
		}
		// stock stores the encoder's output for every data blob
		stock := repo.getZstdEncoder().EncodeAll(c.data, nil)
		stockStored := uint(crypto.CiphertextLength(len(stock)))
		if len(stock) >= len(c.data) {
			incompressible++
		}

		if !pbs[0].IsCompressed() {
			t.Errorf("%s: stored uncompressed (%d bytes of data, the encoder's output has %d); stock stores the encoder's output",
				c.name, len(c.data), len(stock))
			continue
		}
		if stored := pbs[0].CiphertextLength(); stored != stockStored {
			t.Errorf("%s: %d bytes stored, stock stores %d", c.name, stored, stockStored)
		}
		if got := pbs[0].PlaintextLength(); got != uint(len(c.data)) {
			t.Errorf("%s: plaintext length %d recorded for %d bytes of data", c.name, got, len(c.data))
		}

		got, err := repo.LoadBlob(context.Background(), h, nil)
		test.OK(t, err)
		if !bytes.Equal(got, c.data) {
			t.Errorf("%s: blob does not read back", c.name)
		}
	}
	if incompressible < 5 || incompressible > len(cases)-5 {
		t.Fatalf("one-sided inputs: the encoder does not shrink %d of %d blobs", incompressible, len(cases))
	}
	t.Logf("%d blobs, %d of them not shrunk by the encoder: all stored in compressed form with stock's size", len(cases), incompressible)
}

// With an explicit compression mode nothing is stored uncompressed.
func TestCompressionModesOtherThanAutoAlwaysCompress(t *testing.T) {
	data := make([]byte, 1<<20)
	rand.New(rand.NewSource(1)).Read(data)
	for _, mode := range []CompressionMode{CompressionFastest, CompressionBetter, CompressionMax} {
		repo, _, _ := TestRepositoryWithVersion(t, 2)
		repo.opts.Compression = mode
		var id restic.ID
		test.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, up restic.BlobSaverWithAsync) error {
			var err error
			id, _, _, err = up.SaveBlob(ctx, restic.DataBlob, data, restic.ID{}, false)
			return err
		}))
		pbs := repo.LookupBlob(restic.BlobHandle{Type: restic.DataBlob, ID: id})
		if len(pbs) != 1 || !pbs[0].IsCompressed() {
			t.Fatalf("mode %v: incompressible blob was not stored in compressed form", mode)
		}
	}
}
