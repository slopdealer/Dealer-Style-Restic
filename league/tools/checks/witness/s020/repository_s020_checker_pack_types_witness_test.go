package repository

// WITNESS (S020 review, mechanism 2b: checker pack types from the load
// callbacks, internal/repository/checker_pack_types.go, checker.go:116-171).
//
// S020's Checker.LoadIndex no longer walks the merged index to find the blob
// type of every pack (computePackTypes); on a fresh load it takes the types
// from the per-index-file groups it already builds. The map it fills is also
// the list of packs for which LoadIndex reports its three hints, and one of
// them (ErrIncompletePackEntry: a pack whose blobs are split over index files)
// is an ERROR in `restic check`. A pack missing from the map would lose that
// error.
//
// The test writes random index files (packs listed in one file, listed
// identically in two, split over two, mixed packs) and compares the hints of
// LoadIndex with what the layout must give by definition, and the pack set with
// stock's computePackTypes on the loaded index. Expected on S020: PASS.
//
// OS: any. Run from the tree root:
//   go test -run TestWitnessS020CheckerPackTypes -count=1 -p 2 -v ./internal/repository

import (
	"context"
	"math/rand"
	"sort"
	"testing"

	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

func TestWitnessS020CheckerPackTypes(t *testing.T) {
	ctx := context.Background()
	total := map[string]int{}
	for round := 0; round < 40; round++ {
		rng := rand.New(rand.NewSource(int64(round)))
		repo, unpacked, be := TestRepositoryWithVersion(t, 2)
		_ = repo

		nIndexes := 2 + rng.Intn(4)
		indexes := make([]*index.Index, nIndexes)
		for i := range indexes {
			indexes[i] = index.NewIndex()
		}
		want := map[string]bool{}
		for p := 0; p < 30+rng.Intn(40); p++ {
			var id restic.ID
			rng.Read(id[:])
			tpe := restic.BlobType(1 + rng.Intn(2))
			mixed := false
			n := 2 + rng.Intn(5)
			blobs := make(pack.Blobs, 0, n)
			offset := uint(0)
			for b := 0; b < n; b++ {
				h := restic.BlobHandle{Type: tpe}
				rng.Read(h.ID[:])
				if b == n-1 && rng.Intn(8) == 0 {
					h.Type = 3 - tpe // the other type: a mixed pack
					mixed = true
				}
				length := uint(50 + rng.Intn(500))
				blobs = append(blobs, pack.Blob{BlobHandle: h, Offset: offset, Length: length})
				offset += length
			}
			a := rng.Intn(nIndexes)
			b := (a + 1 + rng.Intn(nIndexes-1)) % nIndexes
			switch rng.Intn(6) {
			case 0: // listed identically in two index files
				indexes[a].StorePack(id, blobs)
				indexes[b].StorePack(id, blobs)
				want["duplicate "+id.String()] = true
			case 1: // split over two index files
				cut := 1 + rng.Intn(len(blobs)-1)
				indexes[a].StorePack(id, blobs[:cut])
				indexes[b].StorePack(id, blobs[cut:])
				want["incomplete "+id.String()] = true
			default:
				indexes[a].StorePack(id, blobs)
			}
			if mixed {
				want["mixed "+id.String()] = true
			}
		}
		for _, idx := range indexes {
			idx.Finalize()
			if _, err := idx.SaveIndex(ctx, unpacked); err != nil {
				t.Fatal(err)
			}
		}

		fresh := TestOpenBackend(t, be)
		chkr := newChecker(fresh)
		hints, errs := chkr.LoadIndex(ctx, restic.NoopTerminalCounterFactory)
		if len(errs) != 0 {
			t.Fatalf("round %d: LoadIndex errors: %v", round, errs)
		}
		got := map[string]bool{}
		for _, hint := range hints {
			switch h := hint.(type) {
			case *ErrIncompletePackEntry:
				got["incomplete "+h.PackID.String()] = true
				total["incomplete"]++
			case *ErrDuplicatePacks:
				got["duplicate "+h.PackID.String()] = true
				total["duplicate"]++
			case *ErrMixedPack:
				got["mixed "+h.PackID.String()] = true
				total["mixed"]++
			default:
				t.Fatalf("round %d: unexpected hint %T %v", round, hint, hint)
			}
		}
		var diff []string
		for k := range want {
			if !got[k] {
				diff = append(diff, "missing: "+k)
			}
		}
		for k := range got {
			if !want[k] {
				diff = append(diff, "unexpected: "+k)
			}
		}
		if len(diff) != 0 {
			sort.Strings(diff)
			t.Fatalf("round %d: hints differ from the layout:\n%v", round, diff)
		}

		// the pack set stock walks: computePackTypes on the loaded index
		stockTypes, err := computePackTypes(ctx, fresh)
		if err != nil {
			t.Fatal(err)
		}
		mixedStock := 0
		for id, tpe := range stockTypes {
			if tpe == restic.InvalidBlob {
				mixedStock++
				if !got["mixed "+id.String()] {
					t.Fatalf("round %d: stock's computePackTypes calls pack %v mixed, LoadIndex gave no hint", round, id.Str())
				}
			}
		}
		if mixedStock != countPrefix(got, "mixed ") {
			t.Fatalf("round %d: %d mixed packs by stock's walk, %d hints", round, mixedStock, countPrefix(got, "mixed "))
		}
	}
	t.Logf("hints compared over 40 repositories: %v", total)
	for _, kind := range []string{"incomplete", "duplicate", "mixed"} {
		if total[kind] == 0 {
			t.Fatalf("no %s case was generated", kind)
		}
	}
}

func countPrefix(m map[string]bool, prefix string) int {
	n := 0
	for k := range m {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			n++
		}
	}
	return n
}
