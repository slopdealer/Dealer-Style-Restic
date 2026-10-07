package repository

// WITNESS (S020 review, mechanism 4: prune and index).
//
// TestWitnessS020PackInfoDifferential
//   Stock's packInfoFromIndex (copied verbatim from ba802d42 below) against
//   S020's packInfoFromIndexWithHeaders (AssociatedCountsInRange instead of the
//   two loops over All, prunePackRun instead of a map write per blob) on
//   randomized index states: duplicate blobs across packs and inside a pack,
//   more than 255 copies of one blob (the saturating counter), mixed packs,
//   compressed and uncompressed blobs, a pack listed in two index files, a pack
//   split over two index files, an unsaved index next to the merged one, used
//   blobs that are in no index (must give ErrIndexIncomplete with the same
//   message). Compared: the error, the diagnostic text, every packInfo, the
//   statistics and the resulting keep set with its counters.
//
// TestWitnessS020PruneEndToEnd
//   Real repository (memory backend), random blobs with duplicates, then
//   PlanPrune + Execute with the option matrix, sized so that the repack
//   listing takes each of its three paths (bounded collect, routes, fallback).
//   Afterwards: stock's check (index, packs, read all data) and every kept blob
//   is loaded and hashed. One variant interrupts the first prune after the
//   repack (index removal fails) and prunes again; one has packs that no index
//   lists; one has an index entry whose pack file is gone (prune must refuse).
//
// OS: any. Run from the tree root:
//   go test -run 'TestWitnessS020P' -count=1 -p 2 ./internal/repository

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// witnessStockPackInfoFromIndex is stock's packInfoFromIndex, unchanged.
func witnessStockPackInfoFromIndex(ctx context.Context, idx restic.ListBlobser, usedBlobs *index.AssociatedSet[uint8], stats *PruneStats, printer restic.Printer) (*index.AssociatedSet[uint8], map[restic.ID]packInfo, error) {
	err := idx.ListBlobs(ctx, func(blob restic.PackBlob) {
		bh := blob.Handle()
		count, ok := usedBlobs.Get(bh)
		if ok {
			if count < math.MaxUint8 {
				count++
			}
			usedBlobs.Set(bh, count)
		}
	})
	if err != nil {
		return nil, nil, err
	}

	missingBlobs := restic.NewBlobSet()
	for bh, count := range usedBlobs.All() {
		if count == 0 {
			missingBlobs.Insert(bh)
		}
	}

	if len(missingBlobs) != 0 {
		printer.E("%v not found in the index\n\n"+
			"Integrity check failed: Data seems to be missing.\n"+
			"Will not start prune to prevent (additional) data loss!\n"+
			"Please report this error (along with the output of the 'prune' run) at\n"+
			"https://github.com/restic/restic/issues/new/choose", missingBlobs)
		return nil, nil, ErrIndexIncomplete
	}

	indexPack := make(map[restic.ID]packInfo)

	sz, err := pack.Size(ctx, idx, true)
	if err != nil {
		return nil, nil, err
	}
	for pid, hdrSize := range sz {
		indexPack[pid] = packInfo{tpe: restic.NumBlobTypes, usedSize: uint64(hdrSize)}
	}

	hasDuplicates := false
	err = idx.ListBlobs(ctx, func(blob restic.PackBlob) {
		packID := blob.PackID()
		h := blob.Handle()

		ip := indexPack[packID]

		if ip.tpe == restic.NumBlobTypes {
			ip.tpe = h.Type
		}
		if ip.tpe != h.Type {
			ip.tpe = restic.InvalidBlob
		}

		size := uint64(blob.CiphertextLength())
		dupCount, _ := usedBlobs.Get(h)
		switch {
		case dupCount >= 2:
			hasDuplicates = true
			ip.unusedSize += size
			ip.unusedBlobs++
			ip.duplicateBlobs++
			stats.Size.Duplicate += size
			stats.Blobs.Duplicate++
		case dupCount == 1:
			ip.usedSize += size
			ip.usedBlobs++
			stats.Size.Used += size
			stats.Blobs.Used++
		default:
			ip.unusedSize += size
			ip.unusedBlobs++
			stats.Size.Unused += size
			stats.Blobs.Unused++
		}
		if !blob.IsCompressed() {
			ip.uncompressed = true
		}
		indexPack[packID] = ip
	})
	if err != nil {
		return nil, nil, err
	}

	if hasDuplicates {
		err = idx.ListBlobs(ctx, func(blob restic.PackBlob) {
			packID := blob.PackID()
			bh := blob.Handle()
			count, ok := usedBlobs.Get(bh)
			if !ok || count == 1 {
				return
			}

			ip := indexPack[packID]
			size := uint64(blob.CiphertextLength())
			switch {
			case ip.usedBlobs > 0, ip.duplicateBlobs == ip.unusedBlobs, count == 0:
				ip.usedSize += size
				ip.usedBlobs++
				ip.unusedSize -= size
				ip.unusedBlobs--
				stats.Size.Used += size
				stats.Blobs.Used++
				stats.Size.Duplicate -= size
				stats.Blobs.Duplicate--
				usedBlobs.Set(bh, 1)
			default:
				count--
				if count == 1 {
					count = 0
				}
				usedBlobs.Set(bh, count)
			}
			indexPack[packID] = ip
		})
		if err != nil {
			return nil, nil, err
		}
	}

	for _, count := range usedBlobs.All() {
		if count != 1 {
			panic("internal error during blob selection")
		}
	}

	return usedBlobs, indexPack, nil
}

type witnessCapturePrinter struct {
	restic.Printer
	errors []string
}

func (p *witnessCapturePrinter) E(msg string, args ...any) {
	p.errors = append(p.errors, fmt.Sprintf(msg, args...))
}

func witnessRandID(rng *rand.Rand) (id restic.ID) {
	binary.LittleEndian.PutUint64(id[:], rng.Uint64())
	binary.LittleEndian.PutUint64(id[8:], rng.Uint64())
	return id
}

func witnessRandomMaster(t *testing.T, rng *rand.Rand) (*index.MasterIndex, []restic.BlobHandle, string) {
	mi := index.NewMasterIndex()
	nPacks := 3 + rng.Intn(60)
	nIndexes := 1 + rng.Intn(4)
	indexes := make([]*index.Index, nIndexes)
	for i := range indexes {
		indexes[i] = index.NewIndex()
	}
	var pool []restic.BlobHandle
	type stored struct {
		id    restic.ID
		blobs pack.Blobs
	}
	var all []stored
	for p := 0; p < nPacks; p++ {
		id := witnessRandID(rng)
		packType := restic.BlobType(1 + rng.Intn(2))
		mixed := rng.Intn(15) == 0
		n := 1 + rng.Intn(30)
		blobs := make(pack.Blobs, 0, n)
		offset := uint(0)
		for b := 0; b < n; b++ {
			var h restic.BlobHandle
			if len(pool) > 0 && rng.Intn(4) == 0 {
				h = pool[rng.Intn(len(pool))]
			} else {
				h = restic.BlobHandle{ID: witnessRandID(rng), Type: packType}
				if mixed && rng.Intn(2) == 0 {
					h.Type = restic.BlobType(1 + rng.Intn(2))
				}
				pool = append(pool, h)
			}
			length := uint(40 + rng.Intn(4000))
			ul := uint(0)
			if rng.Intn(2) == 0 {
				ul = length * 3
			}
			blobs = append(blobs, pack.Blob{BlobHandle: h, Offset: offset, Length: length, UncompressedLength: ul})
			offset += length
		}
		all = append(all, stored{id, blobs})
		target := indexes[rng.Intn(nIndexes)]
		switch rng.Intn(10) {
		case 0:
			target.StorePack(id, blobs)
			indexes[rng.Intn(nIndexes)].StorePack(id, blobs)
		case 1:
			cut := rng.Intn(len(blobs) + 1)
			target.StorePack(id, blobs[:cut])
			indexes[rng.Intn(nIndexes)].StorePack(id, blobs[cut:])
		default:
			target.StorePack(id, blobs)
		}
	}
	if rng.Intn(8) == 0 && len(pool) > 0 {
		// more copies of one blob than the eight-bit counter can hold
		h := pool[rng.Intn(len(pool))]
		copies := 250 + rng.Intn(30)
		for c := 0; c < copies; c++ {
			indexes[rng.Intn(nIndexes)].StorePack(witnessRandID(rng), pack.Blobs{{BlobHandle: h, Length: 100}})
		}
	}
	layout := []string{"merged", "merged+unsaved", "unmerged-final"}[rng.Intn(3)]
	for i, idx := range indexes {
		idx.Finalize()
		if layout != "unmerged-final" || i == 0 {
			if err := idx.SetID(witnessRandID(rng)); err != nil {
				t.Fatal(err)
			}
		}
		mi.Insert(idx)
	}
	if err := mi.MergeFinalIndexes(); err != nil {
		t.Fatal(err)
	}
	if layout == "merged+unsaved" {
		for k := 0; k < 3; k++ {
			h := restic.BlobHandle{ID: witnessRandID(rng), Type: restic.DataBlob}
			pool = append(pool, h)
			blobs := pack.Blobs{{BlobHandle: h, Length: 77}, {BlobHandle: pool[rng.Intn(len(pool))], Offset: 77, Length: 99}}
			if err := mi.StorePack(context.Background(), witnessRandID(rng), blobs, nil); err != nil {
				t.Fatal(err)
			}
		}
		s := all[rng.Intn(len(all))]
		if err := mi.StorePack(context.Background(), s.id, s.blobs[:1+rng.Intn(len(s.blobs))], nil); err != nil {
			t.Fatal(err)
		}
	}
	return mi, pool, layout
}

func witnessSetContent(a *index.AssociatedSet[uint8]) map[restic.BlobHandle][2]int {
	out := make(map[restic.BlobHandle][2]int)
	if a == nil {
		return out
	}
	for bh, v := range a.All() {
		e := out[bh]
		e[0] = int(v)
		e[1]++ // All reports a handle once per index entry
		out[bh] = e
	}
	return out
}

func witnessNormalize(messages []string) string {
	fields := strings.FieldsFunc(strings.Join(messages, "|"), func(r rune) bool { return r == ' ' || r == '{' || r == '}' })
	sort.Strings(fields)
	return strings.Join(fields, " ")
}

type witnessPackInfoResult struct {
	err      error
	panicked any
	messages []string
	packs    map[restic.ID]packInfo
	stats    PruneStats
	keep     map[restic.BlobHandle][2]int
}

func TestWitnessS020PackInfoDifferential(t *testing.T) {
	ctx := context.Background()
	layouts := map[string]int{}
	incomplete, duplicates := 0, 0
	for round := 0; round < 600; round++ {
		rng := rand.New(rand.NewSource(int64(round)))
		mi, pool, layout := witnessRandomMaster(t, rng)
		layouts[layout]++
		repo := &Repository{idx: mi}

		usedFraction := []float64{0, 0.2, 0.6, 1}[rng.Intn(4)]
		var used []restic.BlobHandle
		for _, h := range pool {
			if rng.Float64() < usedFraction {
				used = append(used, h)
			}
		}
		if rng.Intn(6) == 0 {
			// used blobs that no index knows
			for k := 0; k < 1+rng.Intn(3); k++ {
				used = append(used, restic.BlobHandle{ID: witnessRandID(rng), Type: restic.BlobType(1 + rng.Intn(2))})
			}
		}

		run := func(fn func(usedBlobs *index.AssociatedSet[uint8], stats *PruneStats, printer restic.Printer) (*index.AssociatedSet[uint8], map[restic.ID]packInfo, error)) (res witnessPackInfoResult) {
			set := index.NewAssociatedSet[uint8](mi)
			for _, h := range used {
				set.Insert(h)
			}
			printer := &witnessCapturePrinter{Printer: restic.NewNoopPrinter()}
			defer func() {
				res.panicked = recover()
				res.messages = printer.errors
			}()
			keep, packs, err := fn(set, &res.stats, printer)
			res.err, res.packs, res.keep = err, packs, witnessSetContent(keep)
			return res
		}

		want := run(func(usedBlobs *index.AssociatedSet[uint8], stats *PruneStats, printer restic.Printer) (*index.AssociatedSet[uint8], map[restic.ID]packInfo, error) {
			return witnessStockPackInfoFromIndex(ctx, repo, usedBlobs, stats, printer)
		})
		variants := map[string]witnessPackInfoResult{
			"packInfoFromIndex": run(func(usedBlobs *index.AssociatedSet[uint8], stats *PruneStats, printer restic.Printer) (*index.AssociatedSet[uint8], map[restic.ID]packInfo, error) {
				return packInfoFromIndex(ctx, repo, usedBlobs, stats, printer)
			}),
			"withHeaders": run(func(usedBlobs *index.AssociatedSet[uint8], stats *PruneStats, printer restic.Printer) (*index.AssociatedSet[uint8], map[restic.ID]packInfo, error) {
				return packInfoFromIndexWithHeaders(ctx, repo, usedBlobs, stats, printer, func() (map[restic.ID]int64, error) {
					return mi.PackHeaderSizes(ctx)
				})
			}),
		}
		if want.err == ErrIndexIncomplete {
			incomplete++
		}
		if want.stats.Blobs.Duplicate > 0 {
			duplicates++
		}
		for name, got := range variants {
			where := fmt.Sprintf("round %d layout %s variant %s", round, layout, name)
			if want.err != got.err {
				t.Fatalf("%s: error: stock %v, S020 %v", where, want.err, got.err)
			}
			if fmt.Sprint(want.panicked) != fmt.Sprint(got.panicked) {
				t.Fatalf("%s: panic: stock %v, S020 %v", where, want.panicked, got.panicked)
			}
			// BlobSet.String walks a map: the order of the handles is random in
			// stock too. Compare the handles as a sorted list.
			if witnessNormalize(want.messages) != witnessNormalize(got.messages) {
				t.Fatalf("%s: diagnostics differ:\nstock %q\nS020  %q", where, want.messages, got.messages)
			}
			if want.err != nil {
				continue
			}
			if !reflect.DeepEqual(want.packs, got.packs) {
				for id, w := range want.packs {
					if g := got.packs[id]; g != w {
						t.Errorf("%s: pack %v: stock %+v, S020 %+v", where, id.Str(), w, g)
					}
				}
				t.Fatalf("%s: pack information differs (%d packs in stock, %d in S020)", where, len(want.packs), len(got.packs))
			}
			if want.stats != got.stats {
				t.Fatalf("%s: statistics differ:\nstock %+v\nS020  %+v", where, want.stats, got.stats)
			}
			if !reflect.DeepEqual(want.keep, got.keep) {
				t.Fatalf("%s: keep set differs (%d handles in stock, %d in S020)", where, len(want.keep), len(got.keep))
			}
		}
	}
	t.Logf("layouts %v; %d rounds ended with ErrIndexIncomplete; %d rounds had duplicates", layouts, incomplete, duplicates)
	if incomplete == 0 || duplicates == 0 {
		t.Fatal("a case class was never exercised")
	}
}

// failingIndexRemove lets a prune write its new packs and index files and then
// fail when it removes the old index files: the state an interrupted prune
// leaves behind (old and new copies of every repacked blob are indexed).
type failingIndexRemove struct {
	backend.Backend
	fail bool
}

func (b *failingIndexRemove) Remove(ctx context.Context, h backend.Handle) error {
	if b.fail && h.Type == backend.IndexFile {
		return fmt.Errorf("witness: index removal refused")
	}
	return b.Backend.Remove(ctx, h)
}

func (b *failingIndexRemove) Unwrap() backend.Backend { return b.Backend }

func TestWitnessS020PruneEndToEnd(t *testing.T) {
	type variant struct {
		name      string
		blobs     int
		opts      PruneOptions
		exact     bool // every unused blob must be gone afterwards
		interrupt bool
		orphan    bool // packs that no index file lists
		missing   bool // an index entry whose pack file is gone: prune must refuse
	}
	unlimited := func(uint64) uint64 { return math.MaxUint64 }
	zero := func(uint64) uint64 { return 0 }
	fivePercent := func(used uint64) uint64 { return used / 20 }
	variants := []variant{
		{name: "collect/max-unused-0", blobs: 3000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: zero}, exact: true},
		{name: "routes/max-unused-0", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: zero}, exact: true},
		{name: "fallback/max-unused-0", blobs: 75000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: zero}, exact: true},
		{name: "routes/max-unused-5pct", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: fivePercent}},
		{name: "routes/max-unused-unlimited", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: unlimited}},
		{name: "routes/max-repack-size", blobs: 14000, opts: PruneOptions{MaxRepackBytes: 300 << 10, MaxUnusedBytes: zero}},
		{name: "routes/repack-cacheable-only", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: zero, RepackCacheableOnly: true}},
		{name: "routes/repack-small", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: unlimited, SmallPackBytes: 1 << 20}},
		{name: "routes/repack-uncompressed", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: unlimited, RepackUncompressed: true}},
		{name: "routes/unsafe-recovery", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: zero, UnsafeRecovery: true}},
		{name: "routes/interrupted-then-again", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: zero}, exact: true, interrupt: true},
		{name: "routes/packs-missing-from-the-index", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: zero}, exact: true, orphan: true},
		{name: "routes/index-entry-without-pack", blobs: 14000, opts: PruneOptions{MaxRepackBytes: math.MaxUint64, MaxUnusedBytes: zero}, missing: true},
	}
	for vi, v := range variants {
		t.Run(v.name, func(t *testing.T) {
			ctx := context.Background()
			rng := rand.New(rand.NewSource(int64(4242 + vi)))
			be := &failingIndexRemove{Backend: TestBackend(t)}
			// half of the data is written without compression (--repack-uncompressed)
			repo, _ := TestRepositoryWithBackend(t, be, 2, Options{Compression: CompressionOff})

			content := make(map[restic.BlobHandle][]byte, v.blobs)
			var handles []restic.BlobHandle
			rounds := 12 + rng.Intn(10)
			perRound := v.blobs / rounds
			for r := 0; r < rounds; r++ {
				if r == rounds/2 {
					repo = TestOpenBackend(t, be) // compression on from here
					if err := repo.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
						t.Fatal(err)
					}
				}
				err := repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
					for i := 0; i < perRound; i++ {
						tpe := restic.DataBlob
						if rng.Intn(3) == 0 {
							tpe = restic.TreeBlob
						}
						buf := make([]byte, 30+rng.Intn(170))
						rng.Read(buf)
						id, _, _, err := uploader.SaveBlob(ctx, tpe, buf, restic.ID{}, false)
						if err != nil {
							return err
						}
						h := restic.BlobHandle{ID: id, Type: tpe}
						content[h] = buf
						handles = append(handles, h)
					}
					// store some earlier blobs once more: duplicates across packs
					for i := 0; i < perRound/15 && len(handles) > 0; i++ {
						h := handles[rng.Intn(len(handles))]
						if _, _, _, err := uploader.SaveBlob(ctx, h.Type, content[h], h.ID, true); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}

			listFiles := func(tpe backend.FileType) map[string]bool {
				names := map[string]bool{}
				if err := be.List(ctx, tpe, func(fi backend.FileInfo) error { names[fi.Name] = true; return nil }); err != nil {
					t.Fatal(err)
				}
				return names
			}
			if v.orphan {
				// one more round of blobs whose index file is then removed: its
				// packs exist in the repository but no index lists them
				before := listFiles(backend.IndexFile)
				packsBefore := listFiles(backend.PackFile)
				r := TestOpenBackend(t, be)
				if err := r.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
					t.Fatal(err)
				}
				err := r.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
					for i := 0; i < 300; i++ {
						buf := make([]byte, 30+rng.Intn(170))
						rng.Read(buf)
						if _, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, buf, restic.ID{}, false); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				removed := 0
				for name := range listFiles(backend.IndexFile) {
					if !before[name] {
						if err := be.Remove(ctx, backend.Handle{Type: backend.IndexFile, Name: name}); err != nil {
							t.Fatal(err)
						}
						removed++
					}
				}
				orphans := len(listFiles(backend.PackFile)) - len(packsBefore)
				if removed == 0 || orphans == 0 {
					t.Fatalf("fixture: %d index files removed, %d orphan packs", removed, orphans)
				}
				t.Logf("%d packs are in the repository and in no index", orphans)
			}

			keep := restic.NewBlobSet()
			for _, h := range handles {
				if rng.Intn(2) == 0 {
					keep.Insert(h)
				}
			}
			usedFn := func(_ context.Context, _ restic.Repository, usedBlobs restic.FindBlobSet) error {
				for h := range keep {
					usedBlobs.Insert(h)
				}
				return nil
			}

			prune := func(opts PruneOptions) error {
				r := TestOpenBackend(t, be)
				if err := r.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
					t.Fatal(err)
				}
				plan, err := PlanPrune(ctx, opts, r, usedFn, restic.NewNoopPrinter())
				if err != nil {
					t.Fatalf("PlanPrune: %v", err)
				}
				keepLen := 0
				if plan.keepBlobs != nil { // nil when nothing is repacked
					keepLen = plan.keepBlobs.Len()
				}
				t.Logf("plan: repack %d packs, remove %d packs, keep set %d blobs", len(plan.repackPacks), len(plan.removePacks), keepLen)
				return plan.Execute(ctx, restic.NewNoopPrinter())
			}

			if v.missing {
				// remove one pack file that the index lists: stock refuses to prune
				for name := range listFiles(backend.PackFile) {
					if err := be.Remove(ctx, backend.Handle{Type: backend.PackFile, Name: name}); err != nil {
						t.Fatal(err)
					}
					break
				}
				r := TestOpenBackend(t, be)
				if err := r.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
					t.Fatal(err)
				}
				_, err := PlanPrune(ctx, v.opts, r, usedFn, restic.NewNoopPrinter())
				t.Logf("PlanPrune with a listed pack missing: %v", err)
				if err == nil {
					t.Fatal("prune planned although a pack listed in the index is missing from the repository")
				}
				return
			}

			if v.interrupt {
				be.fail = true
				if err := prune(v.opts); err == nil {
					t.Fatal("the interrupted prune was expected to fail")
				}
				be.fail = false
			}
			if err := prune(v.opts); err != nil {
				t.Fatalf("prune: %v", err)
			}

			after := TestOpenBackend(t, be)
			TestCheckRepo(t, after)
			if err := after.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
				t.Fatal(err)
			}
			for h := range keep {
				buf, err := after.LoadBlob(ctx, h, nil)
				if err != nil {
					t.Fatalf("kept blob %v is not readable after prune: %v", h, err)
				}
				if restic.Hash(buf) != h.ID || string(buf) != string(content[h]) {
					t.Fatalf("kept blob %v has other content after prune", h)
				}
			}
			if v.exact {
				existing := restic.NewBlobSet()
				if err := after.ListBlobs(ctx, func(pb restic.PackBlob) { existing.Insert(pb.Handle()) }); err != nil {
					t.Fatal(err)
				}
				if !existing.Equals(keep) {
					t.Fatalf("with max-unused 0 the index must hold exactly the kept blobs: %d kept, %d indexed", len(keep), len(existing))
				}
			}
		})
	}
}
