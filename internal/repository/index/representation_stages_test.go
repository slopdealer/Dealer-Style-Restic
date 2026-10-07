package index

import (
	"bytes"
	"fmt"
	"runtime"
	"testing"
	"time"
	"unsafe"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// A generated representation discriminator, not a frozen-workload speed test.
// It retains four completed tables with zero active loads, then measures
// destination assembly separately from decompression and canonical decoding.
func TestIndexRepresentationStages(t *testing.T) {
	for _, entries := range []int{65535, 65536, 65537} {
		for _, adopt := range []bool{false, true} {
			t.Run(fmt.Sprintf("entries=%d/adopt=%v", entries, adopt), func(t *testing.T) {
				representationStages(t, entries, adopt)
			})
		}
	}
}

func representationStages(t *testing.T, entries int, adopt bool) {
	const files = 4
	encoded := make([][]byte, files)
	enc, err := zstd.NewWriter(nil)
	if err != nil { t.Fatal(err) }
	defer enc.Close()
	for f := range files {
		idx := NewIndex()
		for p := 0; p < (entries+127)/128; p++ {
			var blobs pack.Blobs
			for b := range 128 {
				if p*128+b >= entries { break }
				v := uint64(f*entries+p*128+b+1)
				var id restic.ID
				for i := range 8 { id[i] = byte(v >> (8*i)) }
				blobs = append(blobs, pack.Blob{BlobHandle: restic.BlobHandle{ID: id, Type: restic.DataBlob}, Offset: uint(b*64), Length: 64})
			}
			var pid restic.ID
			pid[0], pid[1], pid[2] = byte(p), byte(p>>8), byte(f)
			pid[3] = 1
			idx.StorePack(pid, blobs)
		}
		idx.Finalize()
		var raw bytes.Buffer
		if err := idx.Encode(&raw); err != nil { t.Fatal(err) }
		encoded[f] = enc.EncodeAll(raw.Bytes(), nil)
		t.Logf("file %d: canonical len/cap=%d/%d compressed len/cap=%d/%d", f, raw.Len(), raw.Cap(), len(encoded[f]), cap(encoded[f]))
	}
	dec, err := zstd.NewReader(nil)
	if err != nil { t.Fatal(err) }
	defer dec.Close()
	retained := make([]*Index, 0, files)
	var allocatedBefore, allocatedAfter runtime.MemStats
	measure := func(stage string, fn func()) {
		runtime.ReadMemStats(&allocatedBefore)
		start := time.Now()
		fn()
		elapsed := time.Since(start)
		runtime.ReadMemStats(&allocatedAfter)
		t.Logf("%s: wall=%s allocated=%d heap=%d", stage, elapsed, allocatedAfter.TotalAlloc-allocatedBefore.TotalAlloc, allocatedAfter.HeapAlloc)
	}
	for f := range files {
		var plaintext []byte
		measure("decompress", func() { plaintext, err = dec.DecodeAll(encoded[f], nil) })
		if err != nil { t.Fatal(err) }
		t.Logf("plaintext len/cap=%d/%d", len(plaintext), cap(plaintext))
		var idx *Index
		measure("canonical decode", func() { idx, err = decodeIndexEager(plaintext, restic.ID{byte(f+1)}) })
		if err != nil { t.Fatal(err) }
		retained = append(retained, idx)
		plaintext = nil
	}
	var tableBytes uintptr
	for _, idx := range retained {
		for typ := range restic.NumBlobTypes {
			m := &idx.byType[typ]
			tableBytes += uintptr(cap(m.buckets))*unsafe.Sizeof(uint(0))
			for _, block := range m.blockList.blockList { tableBytes += uintptr(cap(block))*unsafe.Sizeof(indexEntry{}) }
		}
	}
	t.Logf("zero active loads; %d completed tables retain at least %d table bytes (excludes packs, notes and decoder caches)", len(retained), tableBytes)
	mi := NewMasterIndex()
	for _, idx := range retained { mi.Insert(idx) }
	if adopt {
		measure("adopt first exclusively owned result", func() {
			if !mi.adoptLoadedIndex(retained[0]) { t.Fatal("adoption refused unique fixture") }
		})
	}
	measure("destination merge", func() { err = mi.MergeFinalIndexes() })
	if err != nil { t.Fatal(err) }
	if got := mi.idx[0].Len(restic.DataBlob); got != uint(files*entries) { t.Fatalf("lost entries: %d", got) }
	runtime.KeepAlive(retained)
}
