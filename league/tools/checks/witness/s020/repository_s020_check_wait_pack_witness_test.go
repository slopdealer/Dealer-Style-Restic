package repository

// WITNESS (S020 review, mechanism 2: check reads one pack during the lock wait).
//
// What the mechanism does (internal/repository/check_wait_pack.go): during the
// lock wait `check --read-data` reads one pack of at most 2 MiB into memory.
// After the lock is confirmed, checkPack does NOT read that pack from the
// backend again: it verifies the bytes held in memory, provided that
// identity() (a stat of the file) returns the same eight numbers as right
// after the early read and watch() (inotify) has no event queued.
//
// What this test shows: the "pack is intact" verdict of check then rests on
// identity() and watch() alone. The test uses the real capture / Settle /
// checkWaitPackOffer / checkPack code and a real local repository, and replaces
// only the two probes by a MODEL of a network file system mounted as a local
// repository (NFS, SMB, FUSE): the stat answers from an attribute cache for 3
// seconds, and changes made on the server raise no inotify event on the client.
// A pack that is damaged on the storage after the early read is then reported
// as intact; the same check without the held bytes reports the damage.
//
// This is a model, not a measurement of NFS. Whether a real NFS client behaves
// like the model is decided by the Linux box test
// TestWitnessS020CheckWaitPackRealProbes (file
// s020_check_wait_pack_linux_witness_test.go) with RESTIC_WITNESS_NFS_CLIENT_DIR
// and RESTIC_WITNESS_NFS_SERVER_DIR set. On a local disk file system the real
// probes are expected to reject the held bytes (same Linux test, default mode).
//
// OS: any. Run from the tree root:
//   go test -run TestWitnessS020CheckWaitPackBlindProbes -count=1 -p 2 -v ./internal/repository
// Expected on S020: FAIL (the damaged pack passes).

import (
	"bufio"
	"context"
	"math/rand"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
)

// s020LocalRepoWithPacks creates a local repository with a few small packs and
// returns its directory.
func s020LocalRepoWithPacks(t *testing.T, dir string) {
	t.Helper()
	ctx := context.Background()
	lbe, err := local.Create(ctx, local.Config{Path: dir, Connections: 2}, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	repo, _ := TestRepositoryWithBackend(t, lbe, 2, Options{})
	rng := rand.New(rand.NewSource(7))
	for round := 0; round < 2; round++ {
		err = repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
			for i := 0; i < 20; i++ {
				buf := make([]byte, 2000+rng.Intn(3000))
				rng.Read(buf)
				tpe := restic.DataBlob
				if i%4 == 0 {
					tpe = restic.TreeBlob
				}
				if _, _, _, err := uploader.SaveBlob(ctx, tpe, buf, restic.ID{}, false); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := lbe.Close(); err != nil {
		t.Fatal(err)
	}
}

func s020VerifyPack(t *testing.T, ctx context.Context, r *Repository, id restic.ID, blobs pack.Blobs, size int64) error {
	t.Helper()
	dec, err := zstd.NewReader(nil, zstd.WithDecoderConcurrency(1))
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	return checkPack(ctx, r, id, append(pack.Blobs(nil), blobs...), size, bufio.NewReaderSize(nil, maxStreamBufferSize), dec)
}

// s020FlipByte damages one byte in the middle of the file, keeping its size.
func s020FlipByte(t *testing.T, path string) {
	t.Helper()
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	fi, err := f.Stat()
	if err != nil {
		t.Fatal(err)
	}
	var b [1]byte
	if _, err = f.ReadAt(b[:], fi.Size()/3); err != nil {
		t.Fatal(err)
	}
	b[0] ^= 0xff
	if _, err = f.WriteAt(b[:], fi.Size()/3); err != nil {
		t.Fatal(err)
	}
	if err = f.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestWitnessS020CheckWaitPackBlindProbes(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "repo")
	s020LocalRepoWithPacks(t, dir)

	r, _ := TestOpenLocal(t, dir)
	if err := r.LoadIndex(ctx, restic.NoopTerminalCounterFactory); err != nil {
		t.Fatal(err)
	}
	sizes, err := pack.Size(ctx, r, false)
	if err != nil {
		t.Fatal(err)
	}
	var id restic.ID
	for id = range sizes {
		break
	}
	pb := <-r.listPacksFromIndex(ctx, restic.NewIDSet(id))
	size := sizes[id]
	l := backend.AsBackend[*local.Local](r.be)
	path := l.Filename(backend.Handle{Type: backend.PackFile, Name: id.String()})

	// The model of a network mount: stat answers from an attribute cache.
	var mu sync.Mutex
	var cached [8]uint64
	var cachedAt time.Time
	attributeCache := func(backend.Handle) ([8]uint64, bool) {
		mu.Lock()
		defer mu.Unlock()
		if !cachedAt.IsZero() && time.Since(cachedAt) < 3*time.Second {
			return cached, true
		}
		fi, err := os.Stat(path)
		if err != nil {
			return [8]uint64{}, false
		}
		cached = [8]uint64{1, 1, uint64(fi.Size()), uint64(fi.ModTime().Unix()), uint64(fi.ModTime().Nanosecond())}
		cachedAt = time.Now()
		return cached, true
	}
	// ... and changes made on the server raise no event on the client.
	silentWatch := func(backend.Handle) (func() bool, func(), bool) {
		return func() bool { return true }, func() {}, true
	}

	pctx, cancel := context.WithCancel(ctx)
	p := &CheckWaitPack{repo: r, owner: backend.AsBackend[*retry.Backend](r.be), ctx: pctx, cancel: cancel,
		done: make(chan struct{}), identity: attributeCache, watch: silentWatch}
	defer p.Settle(false)

	// the lock wait: the early read
	p.capture(pb.PackID, pb.Blobs)
	if !p.started {
		t.Fatalf("fixture pack of %d bytes was not admitted", size)
	}
	<-p.done
	p.Settle(true) // lock confirmed
	if p.Stats().Completed != 1 {
		t.Fatalf("early read did not complete: %+v", p.Stats())
	}

	// the pack is damaged on the storage after the early read
	s020FlipByte(t, path)

	// control: what check --read-data says when it reads the pack now
	controlErr := s020VerifyPack(t, ctx, r, id, pb.Blobs, size)
	t.Logf("control (pack read from the backend now): %v", controlErr)
	if controlErr == nil {
		t.Fatal("the control did not notice the damage; the fixture is wrong")
	}

	// subject: the same verification with the bytes of the lock wait on offer
	subjectErr := s020VerifyPack(t, p.Context(ctx), r, id, pb.Blobs, size)
	t.Logf("with the lock-wait bytes on offer: err=%v, stats=%+v", subjectErr, p.Stats())
	if subjectErr == nil {
		t.Errorf("CHECK PASSES ON A DAMAGED PACK: pack %v was damaged on the storage after the early read; "+
			"with probes that cannot see the change (attribute cache, no remote inotify) check verified the bytes "+
			"it read before the lock was confirmed and reported no error. Reading the pack now reports: %v",
			id.Str(), controlErr)
	}
}
