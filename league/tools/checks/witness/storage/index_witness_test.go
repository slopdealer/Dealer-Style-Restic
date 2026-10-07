package index

// WITNESS (storage review; mechanisms "streamed per-pack index Encode", "canonical /
// streaming index decoders", "position-addressed AssociatedSet slots").
// OS: any (pure Go). Expected on the candidate: all PASS.
//
//  1. TestWitnessEncodeIdentity: Index.Encode (per-pack streaming writer) is byte
//     identical to stock's json.Encoder over generatePackList, for random indexes
//     with mixed packs, duplicate entries and empty indexes.
//  2. TestWitnessDecodeDifferential: for canonical and non-canonical documents, every
//     fast decoder either declines (falls back to DecodeIndex) or yields exactly
//     what DecodeIndex yields; decodeIndexEager agrees with DecodeIndex on errors.
//  3. TestWitnessSlotEqualsBlobIndex: EachSlot's slot equals MasterIndex.blobIndex
//     for every entry of a merged index with duplicate handles, and SlotUnknown is
//     reported only for the non-merged indexes.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"strings"
	"testing"

	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func witnessRandomID(rnd *rand.Rand) restic.ID {
	var id restic.ID
	rnd.Read(id[:])
	return id
}

func witnessRandomIndex(rnd *rand.Rand, packs, maxBlobs int, dupes bool) *Index {
	idx := NewIndex()
	var seen []pack.Blob
	for p := 0; p < packs; p++ {
		packID := witnessRandomID(rnd)
		var blobs pack.Blobs
		offset := uint(0)
		n := rnd.Intn(maxBlobs + 1)
		for b := 0; b < n; b++ {
			blob := pack.Blob{
				BlobHandle: restic.BlobHandle{Type: restic.DataBlob, ID: witnessRandomID(rnd)},
				Offset:     offset,
				Length:     uint(33 + rnd.Intn(5000)),
			}
			if rnd.Intn(3) == 0 {
				blob.Type = restic.TreeBlob
			}
			if rnd.Intn(2) == 0 {
				blob.UncompressedLength = blob.Length + uint(rnd.Intn(9000))
			}
			if dupes && len(seen) > 0 && rnd.Intn(6) == 0 {
				// the same handle again, in this or another pack
				blob.BlobHandle = seen[rnd.Intn(len(seen))].BlobHandle
			}
			offset += blob.Length
			blobs = append(blobs, blob)
			seen = append(seen, blob)
			if dupes && rnd.Intn(15) == 0 {
				blobs = append(blobs, blob) // exact duplicate entry
			}
		}
		idx.StorePack(packID, blobs)
	}
	return idx
}

func witnessLegacyEncode(t *testing.T, idx *Index) []byte {
	t.Helper()
	list, err := idx.generatePackList()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(jsonIndex{Packs: list}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestWitnessEncodeIdentity(t *testing.T) {
	rnd := rand.New(rand.NewSource(7))
	for i := 0; i < 200; i++ {
		idx := witnessRandomIndex(rnd, rnd.Intn(40), 30, i%2 == 0)
		want := witnessLegacyEncode(t, idx)
		var got bytes.Buffer
		if err := idx.Encode(&got); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatalf("round %d: Encode differs from stock encoding\n got: %.300s\nwant: %.300s", i, got.Bytes(), want)
		}
		// a second Encode (what the save verification compares against) is identical
		var again bytes.Buffer
		if err := idx.Encode(&again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(again.Bytes(), want) {
			t.Fatalf("round %d: second Encode differs from the first", i)
		}
	}
}

func witnessDump(t *testing.T, idx *Index) string {
	t.Helper()
	var counts [restic.NumBlobTypes]uint
	for typ := range idx.byType {
		counts[typ] = idx.byType[typ].len()
	}
	ids, _ := idx.IDs()
	return fmt.Sprintf("%v|%v|%v|%s", counts, idx.packs, ids, witnessLegacyEncode(t, idx))
}

func TestWitnessDecodeDifferential(t *testing.T) {
	rnd := rand.New(rand.NewSource(11))
	var docs [][]byte
	for i := 0; i < 60; i++ {
		idx := witnessRandomIndex(rnd, rnd.Intn(25), 20, i%2 == 0)
		var buf bytes.Buffer
		if err := idx.Encode(&buf); err != nil {
			t.Fatal(err)
		}
		doc := buf.Bytes()
		docs = append(docs, doc)
		s := string(doc)
		// documents another writer could produce, and malformed ones
		docs = append(docs,
			[]byte(strings.ReplaceAll(s, ",", ", ")),
			[]byte(strings.ReplaceAll(s, `"type":"data"`, `"type":"DATA"`)),
			[]byte(strings.ReplaceAll(s, `"offset":`, `"offset":0`)),
			[]byte(strings.ReplaceAll(s, `"length":`, `"length":1e2,"x":`)),
			[]byte(strings.Replace(s, `{"packs":`, `{"supersedes":[],"packs":`, 1)),
			[]byte(strings.Replace(s, "]}\n", `],"supersedes":[]}`, 1)),
			[]byte(strings.Replace(s, "]}\n", `],"packs":[]}`, 1)),
			[]byte(strings.ToUpper(s[:len(s)/2])+s[len(s)/2:]),
			[]byte(s+"  \n\t"),
			[]byte(s+"x"),
			[]byte(s+s),
			[]byte(s[:len(s)/2]),
			[]byte(strings.ReplaceAll(s, `"blobs":[`, `"blobs":[ `)),
			[]byte(strings.ReplaceAll(s, `,"uncompressed_length":`, `,"uncompressed_length":0,"uncompressed_length":`)),
			[]byte(strings.ReplaceAll(s, `"offset":`, `"length":7,"offset":`)),
			[]byte(strings.ReplaceAll(s, `"length":`, `"length":4294967296`)),
		)
	}
	docs = append(docs, []byte(`{"packs":null}`), []byte(`{"packs":[]}`), []byte(`{}`), []byte(``), []byte(`null`),
		[]byte(`{"packs":[{"id":"`+strings.Repeat("ab", 32)+`","blobs":null}]}`),
		[]byte(`{"packs":[{"id":"`+strings.Repeat("ab", 32)+`","blobs":[]}]}`),
		[]byte(`{"packs":[{"id":"`+strings.Repeat("AB", 32)+`","blobs":[]}]}`),
		[]byte(`{"packs":[{"id":"`+strings.Repeat("ab", 31)+`","blobs":[]}]}`))

	id := witnessRandomID(rnd)
	accepted := map[string]int{}
	for n, doc := range docs {
		ref, refErr := func() (idx *Index, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			return DecodeIndex(doc, id)
		}()
		check := func(name string, got *Index, ok bool) {
			if !ok {
				return
			}
			accepted[name]++
			if refErr != nil {
				t.Fatalf("doc %d: %s accepted a document that DecodeIndex rejects (%v): %.200s", n, name, refErr, doc)
			}
			if a, b := witnessDump(t, got), witnessDump(t, ref); a != b {
				t.Fatalf("doc %d: %s differs from DecodeIndex\n got: %.300s\nwant: %.300s", n, name, a, b)
			}
		}

		eager, eagerErr := func() (idx *Index, err error) {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("panic: %v", r)
				}
			}()
			return decodeIndexEager(doc, id)
		}()
		if (eagerErr == nil) != (refErr == nil) {
			t.Fatalf("doc %d: decodeIndexEager err=%v, DecodeIndex err=%v: %.200s", n, eagerErr, refErr, doc)
		}
		check("eager", eager, eagerErr == nil)

		canon, ok := decodeCanonicalIndex(doc, id)
		check("canonical", canon, ok)

		rd, ok := DecodeIndexReader(iotestOneByte(doc), id)
		check("reader", rd, ok)

		st, ok := DecodeCanonicalIndexStream(func() (io.ReadCloser, error) {
			return io.NopCloser(iotestOneByte(doc)), nil
		}, id)
		check("stream", st, ok)
		st2, ok := DecodeCanonicalIndexStream(func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(doc)), nil
		}, id)
		check("stream-whole", st2, ok)
	}
	t.Logf("%d documents; accepted by fast paths: %v", len(docs), accepted)
	for _, name := range []string{"canonical", "reader", "stream", "stream-whole"} {
		if accepted[name] < 60 {
			t.Errorf("%s accepted only %d documents: the canonical documents did not reach it", name, accepted[name])
		}
	}
}

// iotestOneByte returns a reader that yields small, irregular reads, so token
// boundaries fall on read boundaries.
func iotestOneByte(p []byte) io.Reader { return &witnessChoppyReader{p: p} }

type witnessChoppyReader struct {
	p []byte
	n int
}

func (r *witnessChoppyReader) Read(dst []byte) (int, error) {
	if len(r.p) == 0 {
		return 0, io.EOF
	}
	r.n++
	n := 1 + r.n%7
	n = min(n, len(r.p), len(dst))
	copy(dst, r.p[:n])
	r.p = r.p[n:]
	return n, nil
}

func TestWitnessSlotEqualsBlobIndex(t *testing.T) {
	rnd := rand.New(rand.NewSource(13))
	for round := 0; round < 30; round++ {
		mi := NewMasterIndex()
		var handles []restic.BlobHandle
		addIndex := func(final bool) {
			idx := NewIndex()
			for p := 0; p < 1+rnd.Intn(12); p++ {
				var blobs pack.Blobs
				off := uint(0)
				for b := 0; b < 1+rnd.Intn(40); b++ {
					h := restic.BlobHandle{Type: restic.DataBlob, ID: witnessRandomID(rnd)}
					if rnd.Intn(4) == 0 {
						h.Type = restic.TreeBlob
					}
					if len(handles) > 0 && rnd.Intn(3) == 0 {
						h = handles[rnd.Intn(len(handles))]
					}
					handles = append(handles, h)
					blobs = append(blobs, pack.Blob{BlobHandle: h, Offset: off, Length: uint(40 + rnd.Intn(100))})
					off += blobs[len(blobs)-1].Length
				}
				idx.StorePack(witnessRandomID(rnd), blobs)
			}
			if final {
				idx.Finalize()
				if err := idx.SetID(witnessRandomID(rnd)); err != nil {
					t.Fatal(err)
				}
			}
			mi.Insert(idx)
		}
		for i := 0; i < 1+rnd.Intn(6); i++ {
			addIndex(true)
		}
		if err := mi.MergeFinalIndexes(); err != nil {
			t.Fatal(err)
		}
		// more final indexes merged later (growth after the first merge), and
		// indexes that stay outside the merged one
		for i := 0; i < rnd.Intn(4); i++ {
			addIndex(true)
		}
		if round%2 == 0 {
			if err := mi.MergeFinalIndexes(); err != nil {
				t.Fatal(err)
			}
		}
		for i := 0; i < rnd.Intn(3); i++ {
			addIndex(false)
		}

		set := NewAssociatedSet[uint8](mi)
		stable := mi.idx[0]
		entries, known := 0, 0
		mi.EachSlot(func(pb *pack.PackedBlob, slot Slot) bool {
			entries++
			want := mi.blobIndex(pb.Blob.BlobHandle)
			if slot == SlotUnknown {
				return true
			}
			known++
			if int(slot) != want {
				t.Fatalf("round %d: slot %d != blobIndex %d for %v", round, slot, want, pb.Blob.BlobHandle)
			}
			return true
		})
		var total uint
		for typ := range stable.byType {
			total += stable.byType[typ].len()
		}
		if uint(known) != total {
			t.Fatalf("round %d: %d slots reported for %d entries of the merged index", round, known, total)
		}

		// the same counting pass prune planning runs, with and without slots
		for _, h := range handles {
			if rnd.Intn(2) == 0 {
				set.Set(h, 0)
			}
		}
		ref := NewAssociatedSet[uint8](mi)
		for h, v := range set.All() {
			ref.Set(h, v)
		}
		mi.EachSlot(func(pb *pack.PackedBlob, slot Slot) bool {
			IncrementAssociatedCountAt(set, pb.Blob.BlobHandle, slot)
			return true
		})
		for pb := range mi.Values() {
			h := pb.Blob.BlobHandle
			if c, ok := ref.Get(h); ok {
				if c < 255 {
					c++
				}
				ref.Set(h, c)
			}
		}
		for _, h := range handles {
			a, aok := set.Get(h)
			b, bok := ref.Get(h)
			if a != b || aok != bok {
				t.Fatalf("round %d: count for %v: slots %d/%v, lookups %d/%v", round, h, a, aok, b, bok)
			}
		}
		_ = entries
	}
}
