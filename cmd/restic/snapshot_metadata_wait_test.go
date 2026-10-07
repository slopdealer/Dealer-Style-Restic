package main

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/restic/restic/internal/backend"
	"github.com/restic/restic/internal/backend/retry"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

type metadataTargetList struct {
	ids   []restic.ID
	sizes []int64
	err   error
}

func (l metadataTargetList) List(ctx context.Context, typ restic.FileType, fn func(restic.ID, int64) error) error {
	if typ != restic.SnapshotFile {
		return errors.New("unexpected listing")
	}
	for i, id := range l.ids {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := fn(id, l.sizes[i]); err != nil {
			return err
		}
	}
	return l.err
}

func TestSnapshotMetadataTargetsOnlyOneListedDemand(t *testing.T) {
	a := restic.ID{0xab, 1}
	b := restic.ID{0xab, 2}
	for _, tc := range []struct {
		name, request string
		list          metadataTargetList
		want          bool
	}{
		{"full", a.String(), metadataTargetList{ids: []restic.ID{a, b}, sizes: []int64{80, 80}}, true},
		{"prefix", a.String()[:4], metadataTargetList{ids: []restic.ID{a, b}, sizes: []int64{80, 80}}, true},
		{"subfolder", a.String() + ":dir", metadataTargetList{ids: []restic.ID{a}, sizes: []int64{80}}, true},
		{"sole-latest", "latest", metadataTargetList{ids: []restic.ID{a}, sizes: []int64{80}}, true},
		{"many-latest", "latest", metadataTargetList{ids: []restic.ID{a, b}, sizes: []int64{80, 80}}, false},
		{"ambiguous", "ab", metadataTargetList{ids: []restic.ID{a, b}, sizes: []int64{80, 80}}, false},
		{"absent", b.String(), metadataTargetList{ids: []restic.ID{a}, sizes: []int64{80}}, false},
		{"late-list-error", a.String(), metadataTargetList{ids: []restic.ID{a}, sizes: []int64{80}, err: errors.New("listing failed")}, false},
		{"oversized", a.String(), metadataTargetList{ids: []restic.ID{a}, sizes: []int64{repository.PreparedSnapshotLimit + 1}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, _, ok := snapshotMetadataTarget(context.Background(), tc.list, tc.request)
			if ok != tc.want || (ok && id != a) {
				t.Fatalf("target=%v ok=%v", id, ok)
			}
		})
	}
}

type metadataWaitBackend struct {
	backend.Backend
	active                     bool
	mu                         sync.Mutex
	lists                      []backend.FileType
	started, canceled, release chan struct{}
	start, cancel              sync.Once
}

func (b *metadataWaitBackend) Properties() backend.Properties {
	p := b.Backend.Properties()
	p.Connections = 2
	return p
}
func (b *metadataWaitBackend) List(ctx context.Context, typ backend.FileType, fn func(backend.FileInfo) error) error {
	if b.active {
		b.mu.Lock()
		b.lists = append(b.lists, typ)
		b.mu.Unlock()
	}
	return b.Backend.List(ctx, typ, fn)
}
func (b *metadataWaitBackend) Load(ctx context.Context, h backend.Handle, n int, off int64, fn func(io.Reader) error) error {
	if b.active && h.Type == backend.SnapshotFile {
		b.start.Do(func() { close(b.started) })
		<-ctx.Done()
		b.cancel.Do(func() { close(b.canceled) })
		<-b.release
		return ctx.Err()
	}
	return b.Backend.Load(ctx, h, n, off, fn)
}

func TestSnapshotMetadataFailedConfirmationJoinsResponseBeforeRelease(t *testing.T) {
	ctx := context.Background()
	gate := &metadataWaitBackend{Backend: repository.TestBackend(t), started: make(chan struct{}), canceled: make(chan struct{}), release: make(chan struct{})}
	r, _ := repository.TestRepositoryWithBackend(t, retry.New(gate, time.Second, nil, nil), 2, repository.Options{})
	id, err := r.SaveUnpacked(ctx, restic.WriteableSnapshotFile, []byte(`{"time":"2026-01-01T00:00:00Z"}`))
	if err != nil {
		t.Fatal(err)
	}
	gate.active = true
	load := startRepositoryIndexLoadAfterSnapshots(ctx, r, true)
	p := startSnapshotMetadataLoad(ctx, r, load, id.String())
	var release sync.Once
	cleanup := func() { release.Do(func() { close(gate.release) }) }
	defer cleanup()
	defer load.discard()
	defer p.finish()
	select {
	case <-gate.started:
	case <-time.After(5 * time.Second):
		cleanup()
		t.Fatal("snapshot read did not start")
	}
	settled := make(chan struct{})
	go func() { load.settle(false); p.settle(false); close(settled) }()
	select {
	case <-gate.canceled:
	case <-time.After(5 * time.Second):
		cleanup()
		t.Fatal("snapshot read not canceled")
	}
	select {
	case <-settled:
		t.Fatal("settlement returned with live response")
	default:
	}
	cleanup()
	select {
	case <-settled:
	case <-time.After(5 * time.Second):
		t.Fatal("settlement did not join response")
	}
	if p.loader != nil {
		t.Fatal("discarded ciphertext still retained")
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if len(gate.lists) == 0 || gate.lists[0] != backend.SnapshotFile || (len(gate.lists) > 1 && gate.lists[1] != backend.IndexFile) {
		t.Fatalf("listing order=%v", gate.lists)
	}
}

func TestSnapshotMetadataCancelWhileListingIsPending(t *testing.T) {
	r := repository.TestRepository(t)
	load := &repositoryIndexLoad{snapshots: newPreparedSnapshots()}
	p := startSnapshotMetadataLoad(context.Background(), r, load, "latest")
	if p == nil {
		t.Fatal("expected preparation")
	}
	p.settle(false)
	if p.loader != nil {
		t.Fatal("retained fallback after failed confirmation")
	}
}
