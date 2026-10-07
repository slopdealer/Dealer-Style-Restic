package repository_test

import (
	"bytes"
	"context"
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
	rtest "github.com/restic/restic/internal/test"
)

type heldCallbackBlobUploader struct {
	restic.BlobSaverWithAsync
	started    chan struct{}
	release    <-chan struct{}
	firstErr   chan error
	once       sync.Once
	corrupt    bool
	corruptOnce sync.Once
	corruptID  restic.ID
}

type delayedFirstBlobUploader struct {
	restic.BlobSaverWithAsync
	nextSave chan struct{}
	checked  chan error
	once     sync.Once
	calls    atomic.Uint32
}

func (u *delayedFirstBlobUploader) SaveBlobAsync(ctx context.Context, tpe restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool, cb func(restic.ID, bool, int, error)) {
	if u.calls.Add(1) == 1 {
		// Hold the first slice until the pack reader has decoded another blob.
		// Compressed blobs reuse the decoder's output backing array.
		expected := append([]byte(nil), buf...)
		go func() {
			select {
			case <-u.nextSave:
			case <-ctx.Done():
				err := ctx.Err()
				u.checked <- err
				cb(restic.ID{}, false, 0, err)
				return
			}

			if !bytes.Equal(buf, expected) {
				err := errors.New("asynchronous repack input changed after callback return")
				u.checked <- err
				cb(restic.ID{}, false, 0, err)
				return
			}
			u.checked <- nil
			u.BlobSaverWithAsync.SaveBlobAsync(ctx, tpe, buf, id, storeDuplicate, cb)
		}()
		return
	}

	u.once.Do(func() { close(u.nextSave) })
	u.BlobSaverWithAsync.SaveBlobAsync(ctx, tpe, buf, id, storeDuplicate, cb)
}

type countingBlobUploader struct {
	restic.BlobSaverWithAsync
	mu         sync.Mutex
	syncSaves  int
	asyncSaves int
}

func (u *countingBlobUploader) SaveBlob(ctx context.Context, tpe restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool) (restic.ID, bool, int, error) {
	u.mu.Lock()
	u.syncSaves++
	u.mu.Unlock()
	return u.BlobSaverWithAsync.SaveBlob(ctx, tpe, buf, id, storeDuplicate)
}

func (u *countingBlobUploader) SaveBlobAsync(ctx context.Context, tpe restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool, cb func(restic.ID, bool, int, error)) {
	u.mu.Lock()
	u.asyncSaves++
	u.mu.Unlock()
	u.BlobSaverWithAsync.SaveBlobAsync(ctx, tpe, buf, id, storeDuplicate, cb)
}

type twoConnectionBackend struct {
	backend.Backend
}

func (be *twoConnectionBackend) Properties() backend.Properties {
	p := be.Backend.Properties()
	p.Connections = 2
	return p
}

func (be *twoConnectionBackend) Unwrap() backend.Backend {
	return be.Backend
}

func (u *heldCallbackBlobUploader) SaveBlobAsync(ctx context.Context, tpe restic.BlobType, buf []byte, id restic.ID, storeDuplicate bool, cb func(restic.ID, bool, int, error)) {
	holdCallback := true
	if u.corrupt {
		u.corruptOnce.Do(func() {
			u.corruptID = id
			if len(buf) > 0 {
				buf[0] ^= 0xff
			}
		})
		holdCallback = id.Equal(u.corruptID)
	}
	u.BlobSaverWithAsync.SaveBlobAsync(ctx, tpe, buf, id, storeDuplicate, func(newID restic.ID, known bool, size int, err error) {
		if holdCallback {
			u.once.Do(func() {
				close(u.started)
				if u.firstErr != nil {
					u.firstErr <- err
				}
			})
			select {
			case <-u.release:
			case <-ctx.Done():
				if err == nil {
					err = ctx.Err()
				}
			}
		}
		cb(newID, known, size, err)
	})
}

func randomSize(random *rand.Rand, min, max int) int {
	return random.Intn(max-min) + min
}

func createRandomBlobs(t testing.TB, random *rand.Rand, repo restic.Repository, blobs int, pData float32, smallBlobs bool) {
	// two loops to allow creating multiple pack files
	for blobs > 0 {
		rtest.OK(t, repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
			for blobs > 0 {
				blobs--
				var (
					tpe    restic.BlobType
					length int
				)

				if random.Float32() < pData {
					tpe = restic.DataBlob
					if smallBlobs {
						length = randomSize(random, 1*1024, 20*1024) // 1KiB to 20KiB of data
					} else {
						length = randomSize(random, 10*1024, 1024*1024) // 10KiB to 1MiB of data
					}
				} else {
					tpe = restic.TreeBlob
					length = randomSize(random, 1*1024, 20*1024) // 1KiB to 20KiB
				}

				buf := make([]byte, length)
				random.Read(buf)

				id, exists, _, err := uploader.SaveBlob(ctx, tpe, buf, restic.ID{}, false)
				if err != nil {
					t.Fatalf("SaveFrom() error %v", err)
				}

				if exists {
					t.Errorf("duplicate blob %v/%v ignored", id, restic.DataBlob)
					continue
				}

				if rand.Float32() < 0.2 {
					break
				}
			}
			return nil
		}))
	}
}

func createRandomWrongBlob(t testing.TB, random *rand.Rand, repo restic.Repository) restic.BlobHandle {
	length := randomSize(random, 10*1024, 1024*1024) // 10KiB to 1MiB of data
	buf := make([]byte, length)
	random.Read(buf)
	id := restic.Hash(buf)
	// invert first data byte
	buf[0] ^= 0xff

	rtest.OK(t, repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		_, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, buf, id, false)
		return err
	}))
	return restic.BlobHandle{ID: id, Type: restic.DataBlob}
}

// selectBlobs splits the list of all blobs randomly into two lists. A blob
// will be contained in the firstone with probability p.
func selectBlobs(t *testing.T, random *rand.Rand, repo restic.Repository, p float32) (list1, list2 restic.BlobSet) {
	list1 = restic.NewBlobSet()
	list2 = restic.NewBlobSet()

	blobs := restic.NewBlobSet()

	err := repo.List(context.TODO(), restic.PackFile, func(id restic.ID, size int64) error {
		handles, err := repo.ListPackHandles(context.TODO(), id, size)
		if err != nil {
			t.Fatalf("error listing pack %v: %v", id, err)
		}

		for _, h := range handles {
			if blobs.Has(h) {
				t.Errorf("ignoring duplicate blob %v", h)
				return nil
			}
			blobs.Insert(h)

			if random.Float32() <= p {
				list1.Insert(h)
			} else {
				list2.Insert(h)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return list1, list2
}

func listPacks(t *testing.T, repo restic.Lister) restic.IDSet {
	return listFiles(t, repo, restic.PackFile)
}

func listFiles(t *testing.T, repo restic.Lister, tpe restic.FileType) restic.IDSet {
	list := restic.NewIDSet()
	err := repo.List(context.TODO(), tpe, func(id restic.ID, size int64) error {
		list.Insert(id)
		return nil
	})

	if err != nil {
		t.Fatal(err)
	}

	return list
}

func findPacksForBlobs(t *testing.T, repo restic.Repository, blobs restic.BlobSet) restic.IDSet {
	packs := restic.NewIDSet()

	for h := range blobs {
		list := repo.LookupBlob(h)
		if len(list) == 0 {
			t.Fatal("Failed to find blob", h.ID.Str(), "with type", h.Type)
		}

		for _, pb := range list {
			packs.Insert(pb.PackID())
		}
	}

	return packs
}

func repack(t *testing.T, repo *repository.Repository, be backend.Backend, packs restic.IDSet, blobs restic.BlobSet) {
	rtest.OK(t, repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		return repository.CopyBlobs(ctx, repo, repo, uploader, packs, blobs, restic.NoopCounter, nil)
	}))

	for id := range packs {
		rtest.OK(t, be.Remove(context.TODO(), backend.Handle{Type: backend.PackFile, Name: id.String()}))
	}
}

func rebuildAndReloadIndex(t *testing.T, repo *repository.Repository) {
	rtest.OK(t, repository.RepairIndex(context.TODO(), repo, repository.RepairIndexOptions{
		ReadAllPacks: true,
	}, restic.NewNoopPrinter()))

	rtest.OK(t, repo.LoadIndex(context.TODO(), restic.NoopTerminalCounterFactory))
}

func TestRepack(t *testing.T) {
	repository.TestAllVersions(t, testRepack)
}

func testRepack(t *testing.T, version uint) {
	repo, _, be := repository.TestRepositoryWithVersion(t, version)

	seed := time.Now().UnixNano()
	random := rand.New(rand.NewSource(seed))
	t.Logf("rand seed is %v", seed)

	// add a small amount of blobs twice to create multiple pack files
	createRandomBlobs(t, random, repo, 10, 0.7, false)
	createRandomBlobs(t, random, repo, 10, 0.7, false)

	packsBefore := listPacks(t, repo)

	// Running repack on empty ID sets should not do anything at all.
	repack(t, repo, be, nil, nil)

	packsAfter := listPacks(t, repo)

	if !packsAfter.Equals(packsBefore) {
		t.Fatalf("packs are not equal, Repack modified something. Before:\n  %v\nAfter:\n  %v",
			packsBefore, packsAfter)
	}

	removeBlobs, keepBlobs := selectBlobs(t, random, repo, 0.2)

	removePacks := findPacksForBlobs(t, repo, removeBlobs)

	repack(t, repo, be, removePacks, keepBlobs)
	rebuildAndReloadIndex(t, repo)

	packsAfter = listPacks(t, repo)
	for id := range removePacks {
		if packsAfter.Has(id) {
			t.Errorf("pack %v still present although it should have been repacked and removed", id.Str())
		}
	}

	for h := range keepBlobs {
		list := repo.LookupBlob(h)
		if len(list) == 0 {
			t.Errorf("unable to find blob %v in repo", h.ID.Str())
			continue
		}

		if len(list) != 1 {
			t.Errorf("expected one pack in the list, got: %v", list)
			continue
		}

		pb := list[0]

		if removePacks.Has(pb.PackID()) {
			t.Errorf("lookup returned pack ID %v that should've been removed", pb.PackID())
		}
	}

	for h := range removeBlobs {
		if _, found := repo.LookupBlobSize(h); found {
			t.Errorf("blob %v still contained in the repo", h)
		}
	}
}

func TestRepackCopy(t *testing.T) {
	repository.TestAllVersions(t, testRepackCopy)
}

func TestRepackWaitsForAsyncSaveError(t *testing.T) {
	repository.TestAllVersions(t, testRepackWaitsForAsyncSaveError)
}

func testRepackWaitsForAsyncSaveError(t *testing.T, version uint) {
	repo, _, _ := repository.TestRepositoryWithVersion(t, version)
	random := rand.New(rand.NewSource(time.Now().UnixNano()))
	createRandomBlobs(t, random, repo, 10, 1, false)
	keepBlobs, _ := selectBlobs(t, random, repo, 1)
	packs := findPacksForBlobs(t, repo, keepBlobs)
	if len(packs) == 0 {
		t.Fatal("test setup did not create any packs to repack")
	}

	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	uploaderStarted := make(chan struct{})
	firstSaveErr := make(chan error, 1)
	done := make(chan error, 1)
	go func() {
		done <- repo.WithBlobUploader(context.Background(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
			delayed := &heldCallbackBlobUploader{
				BlobSaverWithAsync: uploader,
				started:           uploaderStarted,
				release:           release,
				firstErr:          firstSaveErr,
				corrupt:           true,
			}
			return repository.CopyBlobs(ctx, repo, repo, delayed, packs, keepBlobs, restic.NoopCounter, nil)
		})
	}()

	select {
	case <-uploaderStarted:
	case err := <-done:
		t.Fatalf("CopyBlobs returned before starting an asynchronous save: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the asynchronous save callback")
	}

	// The callback has started and cannot report completion until release is
	// closed. CopyBlobs must therefore still own the pack completion boundary.
	select {
	case err := <-done:
		t.Fatalf("CopyBlobs returned before the delayed save completed: %v", err)
	case <-time.After(25 * time.Millisecond):
	}

	unblock()
	var saveErr error
	select {
	case saveErr = <-firstSaveErr:
		if saveErr == nil {
			t.Fatal("expected the corrupted asynchronous payload to fail verification")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the asynchronous save error")
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("CopyBlobs reported success after the delayed save failed")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CopyBlobs did not return after the delayed save completed")
	}
	remainingPacks := listPacks(t, repo)
	for packID := range packs {
		if !remainingPacks.Has(packID) {
			t.Fatalf("source pack %v was removed after the repack save failed", packID)
		}
	}
}

func TestRepackCancelsPendingAsyncSave(t *testing.T) {
	repository.TestAllVersions(t, testRepackCancelsPendingAsyncSave)
}

func TestRepackKeepsAsyncInputAfterStreamAdvances(t *testing.T) {
	repository.TestAllVersions(t, testRepackKeepsAsyncInputAfterStreamAdvances)
}

func testRepackKeepsAsyncInputAfterStreamAdvances(t *testing.T, version uint) {
	repo, _ := repository.TestRepositoryWithBackend(t, &twoConnectionBackend{Backend: repository.TestBackend(t)}, version, repository.Options{})
	rtest.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		for i := 0; i < 4; i++ {
			buf := bytes.Repeat([]byte{byte(i + 1)}, 64*1024)
			if _, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, buf, restic.ID{}, false); err != nil {
				return err
			}
		}
		return nil
	}))

	keepBlobs, _ := selectBlobs(t, rand.New(rand.NewSource(1)), repo, 1)
	packs := findPacksForBlobs(t, repo, keepBlobs)
	if len(packs) != 1 {
		t.Fatalf("test setup created %d packs, want one", len(packs))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var checked *delayedFirstBlobUploader
	err := repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		checked = &delayedFirstBlobUploader{
			BlobSaverWithAsync: uploader,
			nextSave:           make(chan struct{}),
			checked:            make(chan error, 1),
		}
		return repository.CopyBlobs(ctx, repo, repo, checked, packs, keepBlobs, restic.NoopCounter, nil)
	})
	rtest.OK(t, err)
	select {
	case err := <-checked.checked:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out checking the delayed asynchronous input")
	}
}

func TestRepackUsesSynchronousPathForOversizedBlob(t *testing.T) {
	repository.TestAllVersions(t, testRepackUsesSynchronousPathForOversizedBlob)
}

func testRepackUsesSynchronousPathForOversizedBlob(t *testing.T, version uint) {
	repo, _ := repository.TestRepositoryWithBackend(t, &twoConnectionBackend{Backend: repository.TestBackend(t)}, version, repository.Options{})
	const oversizedBlobSize = 8<<20 + 1
	buf := bytes.Repeat([]byte{0x7f}, oversizedBlobSize)
	var blobID restic.ID
	rtest.OK(t, repo.WithBlobUploader(context.Background(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		var err error
		blobID, _, _, err = uploader.SaveBlob(ctx, restic.DataBlob, buf, restic.ID{}, false)
		return err
	}))
	keepBlobs := restic.NewBlobSet(restic.BlobHandle{ID: blobID, Type: restic.DataBlob})
	packs := findPacksForBlobs(t, repo, keepBlobs)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var counted *countingBlobUploader
	err := repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		counted = &countingBlobUploader{BlobSaverWithAsync: uploader}
		return repository.CopyBlobs(ctx, repo, repo, counted, packs, keepBlobs, restic.NoopCounter, nil)
	})
	rtest.OK(t, err)
	counted.mu.Lock()
	syncSaves, asyncSaves := counted.syncSaves, counted.asyncSaves
	counted.mu.Unlock()
	if syncSaves != 1 || asyncSaves != 0 {
		t.Fatalf("oversized blob save counts = sync %d, async %d; want sync 1, async 0", syncSaves, asyncSaves)
	}
}

func testRepackCancelsPendingAsyncSave(t *testing.T, version uint) {
	repo, _, _ := repository.TestRepositoryWithVersion(t, version)
	random := rand.New(rand.NewSource(time.Now().UnixNano()))
	createRandomBlobs(t, random, repo, 10, 1, false)
	keepBlobs, _ := selectBlobs(t, random, repo, 1)
	packs := findPacksForBlobs(t, repo, keepBlobs)
	if len(packs) == 0 {
		t.Fatal("test setup did not create any packs to repack")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release := make(chan struct{})
	uploaderStarted := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
			delayed := &heldCallbackBlobUploader{
				BlobSaverWithAsync: uploader,
				started:           uploaderStarted,
				release:           release,
			}
			return repository.CopyBlobs(ctx, repo, repo, delayed, packs, keepBlobs, restic.NoopCounter, nil)
		})
	}()

	select {
	case <-uploaderStarted:
	case err := <-done:
		t.Fatalf("CopyBlobs returned before starting an asynchronous save: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the asynchronous save callback")
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected cancellation from the interrupted repack, got %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CopyBlobs did not drain the pending save after cancellation")
	}
}

// oneConnectionBackend limits concurrent backend operations to test repack with
// the minimum connection count required by CopyBlobs.
type oneConnectionBackend struct {
	backend.Backend
}

func (be *oneConnectionBackend) Properties() backend.Properties {
	p := be.Backend.Properties()
	p.Connections = 1
	return p
}

func (be *oneConnectionBackend) Unwrap() backend.Backend {
	return be.Backend
}

func testRepackCopy(t *testing.T, version uint) {
	// test with minimal possible connection count
	repo, _ := repository.TestRepositoryWithBackend(t, &oneConnectionBackend{Backend: repository.TestBackend(t)}, version, repository.Options{})
	dstRepo, _ := repository.TestRepositoryWithBackend(t, &oneConnectionBackend{Backend: repository.TestBackend(t)}, version, repository.Options{})
	rtest.Equals(t, repo.Connections(), 1)
	rtest.Equals(t, dstRepo.Connections(), 1)

	seed := time.Now().UnixNano()
	random := rand.New(rand.NewSource(seed))
	t.Logf("rand seed is %v", seed)

	// add a small amount of blobs twice to create multiple pack files
	createRandomBlobs(t, random, repo, 10, 0.7, false)
	createRandomBlobs(t, random, repo, 10, 0.7, false)

	_, keepBlobs := selectBlobs(t, random, repo, 0.2)
	copyPacks := findPacksForBlobs(t, repo, keepBlobs)

	rtest.OK(t, repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		return repository.CopyBlobs(ctx, repo, dstRepo, uploader, copyPacks, keepBlobs, restic.NoopCounter, nil)
	}))
	rebuildAndReloadIndex(t, dstRepo)

	for h := range keepBlobs {
		list := dstRepo.LookupBlob(h)
		if len(list) == 0 {
			t.Errorf("unable to find blob %v in repo", h.ID.Str())
			continue
		}

		if len(list) != 1 {
			t.Errorf("expected one pack in the list, got: %v", list)
			continue
		}
	}
}

func TestRepackWrongBlob(t *testing.T) {
	repository.TestAllVersions(t, testRepackWrongBlob)
}

func testRepackWrongBlob(t *testing.T, version uint) {
	// disable verification to allow adding corrupted blobs to the repository
	repo, _ := repository.TestRepositoryWithBackend(t, nil, version, repository.Options{NoExtraVerify: true})

	seed := time.Now().UnixNano()
	random := rand.New(rand.NewSource(seed))
	t.Logf("rand seed is %v", seed)

	createRandomBlobs(t, random, repo, 5, 0.7, false)
	createRandomWrongBlob(t, random, repo)

	// just keep all blobs, but also rewrite every pack
	_, keepBlobs := selectBlobs(t, random, repo, 0)
	rewritePacks := findPacksForBlobs(t, repo, keepBlobs)

	err := repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		return repository.CopyBlobs(ctx, repo, repo, uploader, rewritePacks, keepBlobs, restic.NoopCounter, nil)
	})
	if err == nil {
		t.Fatal("expected repack to fail but got no error")
	}
	t.Logf("found expected error: %v", err)
}

func TestRepackBlobFallback(t *testing.T) {
	repository.TestAllVersions(t, testRepackBlobFallback)
}

func testRepackBlobFallback(t *testing.T, version uint) {
	// disable verification to allow adding corrupted blobs to the repository
	repo, _ := repository.TestRepositoryWithBackend(t, nil, version, repository.Options{NoExtraVerify: true})

	seed := time.Now().UnixNano()
	random := rand.New(rand.NewSource(seed))
	t.Logf("rand seed is %v", seed)

	length := randomSize(random, 10*1024, 1024*1024) // 10KiB to 1MiB of data
	buf := make([]byte, length)
	random.Read(buf)
	id := restic.Hash(buf)

	// corrupted copy
	modbuf := make([]byte, len(buf))
	copy(modbuf, buf)
	// invert first data byte
	modbuf[0] ^= 0xff

	// create pack with broken copy
	rtest.OK(t, repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		_, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, modbuf, id, false)
		return err
	}))

	// find pack with damaged blob
	keepBlobs := restic.NewBlobSet(restic.BlobHandle{Type: restic.DataBlob, ID: id})
	rewritePacks := findPacksForBlobs(t, repo, keepBlobs)

	// create pack with valid copy
	rtest.OK(t, repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		_, _, _, err := uploader.SaveBlob(ctx, restic.DataBlob, buf, id, true)
		return err
	}))

	// repack must fallback to valid copy
	rtest.OK(t, repo.WithBlobUploader(context.TODO(), func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		return repository.CopyBlobs(ctx, repo, repo, uploader, rewritePacks, keepBlobs, restic.NoopCounter, nil)
	}))

	keepBlobs = restic.NewBlobSet(restic.BlobHandle{Type: restic.DataBlob, ID: id})
	packs := findPacksForBlobs(t, repo, keepBlobs)
	rtest.Assert(t, len(packs) == 3, "unexpected number of copies: %v", len(packs))
}
