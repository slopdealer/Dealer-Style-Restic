// ballastgen: writes the big-index workload's "ballast" into an existing restic repository using restic's own
// internal repository/packer APIs (real format, fast). It is compiled once inside a COPY of the pinned stock tree
// (cmd/ballastgen/main.go) with the judge's toolchain and run once, untimed, by workload_gen.py --profile big-index.
//
// The ballast is N tiny unique data blobs referenced by one snapshot (tag "ballast", path /ballast, host league,
// time 2020-01-01, so every workload snapshot is newer and "latest" never selects it). Tree layout: root -> D dirs ->
// F files per dir -> K content blobs per file. forget --keep-tag ballast keeps it; prune therefore keeps every blob,
// so every phase that loads the index carries all N entries.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/restic/restic/internal/backend/local"
	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

func main() {
	repoPath := flag.String("repo", "", "existing restic repository (local)")
	passwordFile := flag.String("password-file", "", "password file")
	blobs := flag.Int("blobs", 1500000, "number of unique data blobs")
	blobBytes := flag.Int("blob-bytes", 48, "plaintext bytes per blob")
	perFile := flag.Int("per-file", 2000, "content blobs per file node")
	filesPerDir := flag.Int("files-per-dir", 25, "file nodes per directory tree")
	seed := flag.String("seed", "restic-v2-ballast-1", "content seed")
	flag.Parse()
	ctx := context.Background()
	pw, err := os.ReadFile(*passwordFile)
	check(err)
	be, err := local.Open(ctx, local.Config{Path: *repoPath, Connections: 2}, nil)
	check(err)
	repo, err := repository.New(be, repository.Options{Compression: repository.CompressionAuto})
	check(err)
	check(repo.SearchKey(ctx, strings.TrimRight(string(pw), "\r\n"), 1, ""))
	// the pristine repository has no index yet; nothing to load

	started := time.Now()
	ids := make(restic.IDs, *blobs)
	content := func(i int) []byte {
		out := make([]byte, 0, *blobBytes)
		var counter [16]byte
		for block := 0; len(out) < *blobBytes; block++ {
			binary.LittleEndian.PutUint64(counter[:8], uint64(i))
			binary.LittleEndian.PutUint64(counter[8:], uint64(block))
			h := sha256.Sum256(append([]byte(*seed), counter[:]...))
			out = append(out, h[:]...)
		}
		return out[:*blobBytes]
	}
	var rootID restic.ID
	err = repo.WithBlobUploader(ctx, func(ctx context.Context, up restic.BlobSaverWithAsync) error {
		workers := runtime.GOMAXPROCS(0)
		var wg sync.WaitGroup
		errs := make(chan error, workers)
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				for i := w; i < *blobs; i += workers {
					buf := content(i)
					id := restic.Hash(buf)
					ids[i] = id
					if _, _, _, err := up.SaveBlob(ctx, restic.DataBlob, buf, id, false); err != nil {
						errs <- err
						return
					}
				}
			}(w)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			return err
		}
		fmt.Fprintf(os.Stderr, "saved %d data blobs in %s\n", *blobs, time.Since(started))
		mtime := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
		root := data.NewTreeWriter(up)
		files := (*blobs + *perFile - 1) / *perFile
		dirs := (files + *filesPerDir - 1) / *filesPerDir
		for d := 0; d < dirs; d++ {
			dir := data.NewTreeWriter(up)
			for f := d * *filesPerDir; f < (d+1)*(*filesPerDir) && f < files; f++ {
				lo, hi := f**perFile, (f+1)**perFile
				if hi > *blobs {
					hi = *blobs
				}
				node := &data.Node{Name: fmt.Sprintf("part-%06d.bin", f), Type: data.NodeTypeFile, Mode: 0o644,
					ModTime: mtime, AccessTime: mtime, ChangeTime: mtime, User: "root", Group: "root",
					Size: uint64((hi - lo) * *blobBytes), Links: 1, Content: append(restic.IDs{}, ids[lo:hi]...)}
				if err := dir.AddNode(node); err != nil {
					return err
				}
			}
			sub, err := dir.Finalize(ctx)
			if err != nil {
				return err
			}
			node := &data.Node{Name: fmt.Sprintf("d%04d", d), Type: data.NodeTypeDir, Mode: os.ModeDir | 0o755,
				ModTime: mtime, AccessTime: mtime, ChangeTime: mtime, User: "root", Group: "root", Subtree: &sub}
			if err := root.AddNode(node); err != nil {
				return err
			}
		}
		rootID, err = root.Finalize(ctx)
		return err
	})
	check(err)
	sn := &data.Snapshot{Time: time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC), Tree: &rootID, Paths: []string{"/ballast"},
		Hostname: "league", Username: "root", Tags: []string{"ballast"}}
	snID, err := data.SaveSnapshot(ctx, repo, sn)
	check(err)
	check(repo.Close())
	out, _ := json.Marshal(map[string]any{"snapshot": snID.String(), "tree": rootID.String(), "blobs": *blobs,
		"blob_bytes": *blobBytes, "per_file": *perFile, "files_per_dir": *filesPerDir, "seed": *seed,
		"seconds": time.Since(started).Seconds()})
	fmt.Println(string(out))
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "ballastgen:", err)
		os.Exit(1)
	}
}
