package repository

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/feature"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
	"golang.org/x/sync/errgroup"
)

type blockedUploadBackend struct {
	backend.Backend
	thirdSave, uploadsBusy, readFailed chan struct{}
	bad                                restic.ID
	uploads                            atomic.Int32
	failOnce                           sync.Once
}

func (b *blockedUploadBackend) Properties() backend.Properties {
	p := b.Backend.Properties()
	p.Connections = 2
	return p
}

func (b *blockedUploadBackend) Save(ctx context.Context, h backend.Handle, rd backend.RewindReader) error {
	if h.Type != backend.PackFile {
		return b.Backend.Save(ctx, h, rd)
	}
	if b.uploads.Add(1) == 2 {
		close(b.uploadsBusy)
	}
	<-ctx.Done() // A stalled upload that correctly honors context cancellation.
	return ctx.Err()
}

func (b *blockedUploadBackend) Load(ctx context.Context, h backend.Handle, length int, offset int64, fn func(io.Reader) error) error {
	if h.Type != backend.PackFile || h.Name != b.bad.String() {
		return b.Backend.Load(ctx, h, length, offset, fn)
	}
	for _, ready := range []<-chan struct{}{b.thirdSave, b.uploadsBusy} {
		select {
		case <-ready:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	b.failOnce.Do(func() { close(b.readFailed) })
	return fmt.Errorf("injected source pack read failure")
}

type countSaveUploader struct {
	restic.BlobSaverWithAsync
	calls atomic.Int32
	third chan struct{}
}

func (u *countSaveUploader) started() {
	if u.calls.Add(1) == 3 {
		close(u.third)
	}
}

func (u *countSaveUploader) SaveBlob(ctx context.Context, typ restic.BlobType, buf []byte, id restic.ID, duplicate bool) (restic.ID, bool, int, error) {
	u.started()
	return u.BlobSaverWithAsync.SaveBlob(ctx, typ, buf, id, duplicate)
}

func (u *countSaveUploader) SaveBlobAsync(ctx context.Context, typ restic.BlobType, buf []byte, id restic.ID, duplicate bool, cb func(restic.ID, bool, int, error)) {
	u.started()
	u.BlobSaverWithAsync.SaveBlobAsync(ctx, typ, buf, id, duplicate, cb)
}

// TestRepackReadErrorCancelsQueuedSave is the red team's test (seat 2, finding
// 4), with the returned error compared as well.
// Uses real pack queueing, blob savers and backend Save calls. The only injected
// faults are one source read error and two destination transfers awaiting their
// contexts. The stock control below is copied from stock/repack.go unchanged.
func TestRepackReadErrorCancelsQueuedSave(t *testing.T) {
	hung, stockErr := runBlockedCopy(t, true)
	if hung {
		t.Fatal("stock control hung")
	}
	hung, err := runBlockedCopy(t, false)
	if hung {
		t.Fatal("candidate cannot return source read error until caller cancels the parent upload context")
	}
	// the command must report the read error, not the cancellation of the
	// save that was given up because of it
	if !strings.Contains(stockErr.Error(), "injected source pack read failure") {
		t.Fatalf("stock control returned %v", stockErr)
	}
	if err.Error() != stockErr.Error() {
		t.Fatalf("returned %q, stock returns %q", err, stockErr)
	}
}

func runBlockedCopy(t *testing.T, stock bool) (bool, error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	be := &blockedUploadBackend{Backend: TestBackend(t), thirdSave: make(chan struct{}), uploadsBusy: make(chan struct{}), readFailed: make(chan struct{})}
	sourceBackend := &blockedUploadBackend{Backend: TestBackend(t), thirdSave: be.thirdSave, uploadsBusy: be.uploadsBusy, readFailed: be.readFailed}
	src, _ := TestRepositoryWithBackend(t, sourceBackend, 1, Options{PackSize: MinPackSize})
	dst, _ := TestRepositoryWithBackend(t, be, 1, Options{PackSize: MinPackSize})
	// One failed pack in the first nibble group gives one worker the read error;
	// the other has three independently indexed, valid 5 MiB blobs to upload.
	for n := 0; ; n++ {
		be.bad = restic.Hash([]byte(fmt.Sprintf("bad pack %d", n)))
		if be.bad[0]&15 == 0 {
			break
		}
	}
	sourceBackend.bad = be.bad
	packs := restic.NewIDSet(be.bad)
	badBlob := restic.Hash([]byte("unreadable blob"))
	keep := restic.NewBlobSet()
	keep.Insert(restic.BlobHandle{ID: badBlob, Type: restic.DataBlob})
	if err := src.idx.StorePack(ctx, be.bad, pack.Blobs{{BlobHandle: restic.BlobHandle{ID: badBlob, Type: restic.DataBlob}, Length: 64}}, &internalRepository{src}); err != nil {
		t.Fatal(err)
	}
	for n := 1; n <= 3; n++ {
		plain := bytes.Repeat([]byte{byte(n)}, 5<<20)
		blobID := restic.Hash(plain)
		for salt := 1; ; salt++ {
			nonce := bytes.Repeat([]byte{byte(salt)}, src.Key().NonceSize())
			ciphertext := append(bytes.Clone(nonce), src.Key().Seal(nil, nonce, plain, nil)...)
			var contents bytes.Buffer
			p := pack.NewPacker(src.Key(), &contents)
			if _, err := p.Add(restic.DataBlob, blobID, ciphertext, 0); err != nil {
				t.Fatal(err)
			}
			if err := p.Finalize(); err != nil {
				t.Fatal(err)
			}
			id := restic.Hash(contents.Bytes())
			if id[0]&15 == 0 {
				continue
			}
			// Bypass the stalled-write wrapper for setup only.
			if err := sourceBackend.Backend.Save(ctx, backend.Handle{Type: backend.PackFile, Name: id.String()}, backend.NewByteReader(contents.Bytes(), sourceBackend.Hasher())); err != nil {
				t.Fatal(err)
			}
			if err := src.idx.StorePack(ctx, id, p.Blobs(), &internalRepository{src}); err != nil {
				t.Fatal(err)
			}
			packs.Insert(id)
			keep.Insert(restic.BlobHandle{ID: blobID, Type: restic.DataBlob})
			break
		}
	}
	if err := src.idx.Flush(ctx, &internalRepository{src}); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- dst.WithBlobUploader(ctx, func(uploadCtx context.Context, uploader restic.BlobSaverWithAsync) error {
			u := &countSaveUploader{BlobSaverWithAsync: uploader, third: be.thirdSave}
			if stock {
				return stockRepackControl(uploadCtx, src, dst, u, packs, keep, restic.NoopCounter, func(string, ...any) {})
			}
			return CopyBlobs(uploadCtx, src, dst, u, packs, keep, restic.NoopCounter, nil)
		})
	}()
	select {
	case <-be.readFailed:
	case err := <-done:
		t.Fatalf("stock=%v finished before injecting read error: %v", stock, err)
	case <-time.After(10 * time.Second):
		cancel()
		<-done
		t.Fatalf("stock=%v setup failed to reach stalled upload/read error", stock)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatalf("stock=%v incorrectly succeeded", stock)
		}
		t.Logf("stock=%v returned without parent cancellation: %v", stock, err)
		return false, err
	case <-time.After(2 * time.Second):
		cancel()
		select {
		case err := <-done:
			t.Logf("stock=%v returned only after parent cancellation: %v", stock, err)
		case <-time.After(5 * time.Second):
			t.Fatal("test could not retire canceled copy")
		}
		return true, nil
	}
}
func stockRepackControl(
	ctx context.Context,
	repo *Repository,
	dstRepo restic.Repository,
	uploader restic.BlobSaverWithAsync,
	packs restic.IDSet,
	keepBlobs repackBlobSet,
	p restic.Counter,
	logf LogFunc,
) error {

	wg, wgCtx := errgroup.WithContext(ctx)

	if feature.Flag.Enabled(feature.S3Restore) {
		job, err := repo.StartWarmup(ctx, packs)
		if err != nil {
			return err
		}
		if job.HandleCount() != 0 {
			logf("warming up %d packs from cold storage, this may take a while...", job.HandleCount())
			if err := job.Wait(ctx); err != nil {
				return err
			}
		}
	}

	var keepMutex sync.Mutex
	downloadQueue := make(chan index.PackBlobs)
	wg.Go(func() error {
		defer close(downloadQueue)
		for pbs := range repo.listPacksFromIndex(wgCtx, packs) {
			var packBlobs pack.Blobs
			keepMutex.Lock()
			// filter out unnecessary blobs
			for _, entry := range pbs.Blobs {
				h := restic.BlobHandle{ID: entry.ID, Type: entry.Type}
				if keepBlobs.Has(h) {
					packBlobs = append(packBlobs, entry)
				}
			}
			keepMutex.Unlock()

			select {
			case downloadQueue <- index.PackBlobs{PackID: pbs.PackID, Blobs: packBlobs}:
			case <-wgCtx.Done():
				return wgCtx.Err()
			}
		}
		return wgCtx.Err()
	})

	worker := func() error {
		for t := range downloadQueue {
			err := repo.loadBlobsFromPack(wgCtx, t.PackID, t.Blobs, func(blob restic.BlobHandle, buf []byte, err error) error {
				if err != nil {
					// a required blob couldn't be retrieved
					return err
				}

				keepMutex.Lock()
				// recheck whether some other worker was faster
				shouldKeep := keepBlobs.Has(blob)
				if shouldKeep {
					keepBlobs.Delete(blob)
				}
				keepMutex.Unlock()

				if !shouldKeep {
					return nil
				}

				// We do want to save already saved blobs!
				_, _, _, err = uploader.SaveBlob(wgCtx, blob.Type, buf, blob.ID, true)
				if err != nil {
					return err
				}

				debug.Log("  saved blob %v", blob.ID)
				return nil
			})
			if err != nil {
				return err
			}
			p.Add(1)
		}
		return nil
	}

	// as packs are streamed the concurrency is limited by IO
	// reduce by one to ensure that uploading is always possible
	repackWorkerCount := int(repo.Connections() - 1)
	if repo != dstRepo {
		// no need to share the upload and download connections for different repositories
		repackWorkerCount = int(repo.Connections())
	}
	for i := 0; i < repackWorkerCount; i++ {
		wg.Go(worker)
	}

	return wg.Wait()
}
