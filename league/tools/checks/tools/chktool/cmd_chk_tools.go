package main

// Release-check helpers (final-candidate/checks). This file is copied into a SCRATCH copy of the STOCK tree's
// cmd/restic and registered in main.go there; the result is built as "chktool". It is never part of a tree under test.
//
//	chktool -r REPO chk-savetree --file FILE [--name PATH]
//	    stores FILE byte for byte as ONE tree blob (no validation, no re-encoding) and writes a snapshot that points
//	    at it. For trees restic itself never writes: a node name with a raw quote (R7), one inode recorded with two
//	    different link counts (R8).
//	chktool -r REPO chk-fakeindex --blobs N [--per-pack K]
//	    writes index files for N invented data blobs in invented packs (nothing is uploaded for the packs). For
//	    index-load time and memory at a scale no test repository reaches.

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"os"
	"time"

	"github.com/restic/restic/internal/data"
	"github.com/restic/restic/internal/errors"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/repository/index"
	"github.com/restic/restic/internal/repository/pack"
	"github.com/restic/restic/internal/restic"
	"github.com/restic/restic/internal/ui/progress"
	"github.com/spf13/cobra"
)

func newChkSaveTreeCommand(globalOptions *global.Options) *cobra.Command {
	var file, name string
	cmd := &cobra.Command{
		Use:               "chk-savetree --file FILE",
		Short:             "release checks: store FILE as one tree blob and write a snapshot for it",
		GroupID:           cmdGroupDefault,
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChkSaveTree(cmd.Context(), *globalOptions, file, name)
		},
	}
	cmd.Flags().StringVar(&file, "file", "", "file with the exact bytes of the tree blob")
	cmd.Flags().StringVar(&name, "name", "/chk", "path recorded in the snapshot")
	return cmd
}

func runChkSaveTree(ctx context.Context, gopts global.Options, file, name string) error {
	buf, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	printer := progress.NewTerminalPrinter(false, gopts.Verbosity, gopts.Term)
	ctx, repo, unlock, err := openWithAppendLock(ctx, gopts, false, printer)
	if err != nil {
		return err
	}
	defer unlock()
	if err = repo.LoadIndex(ctx, printer); err != nil {
		return err
	}
	var treeID restic.ID
	err = repo.WithBlobUploader(ctx, func(ctx context.Context, uploader restic.BlobSaverWithAsync) error {
		id, _, _, err := uploader.SaveBlob(ctx, restic.TreeBlob, buf, restic.ID{}, false)
		treeID = id
		return err
	})
	if err != nil {
		return errors.Fatalf("unable to save the tree blob: %v", err)
	}
	sn, err := data.NewSnapshot([]string{name}, []string{"chk"}, "chk", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if err != nil {
		return err
	}
	sn.Tree = &treeID
	id, err := data.SaveSnapshot(ctx, repo, sn)
	if err != nil {
		return err
	}
	printer.S("tree %v\nsnapshot %v\n", treeID.String(), id.String())
	return nil
}

func newChkFakeIndexCommand(globalOptions *global.Options) *cobra.Command {
	var blobs, perPack int
	cmd := &cobra.Command{
		Use:               "chk-fakeindex --blobs N",
		Short:             "release checks: write index files for N invented blobs",
		GroupID:           cmdGroupDefault,
		DisableAutoGenTag: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runChkFakeIndex(cmd.Context(), *globalOptions, blobs, perPack)
		},
	}
	cmd.Flags().IntVar(&blobs, "blobs", 1000000, "number of invented data blobs")
	cmd.Flags().IntVar(&perPack, "per-pack", 16, "blobs per invented pack")
	return cmd
}

func chkID(kind byte, n uint64) restic.ID {
	var seed [9]byte
	seed[0] = kind
	binary.LittleEndian.PutUint64(seed[1:], n)
	return restic.ID(sha256.Sum256(seed[:]))
}

func runChkFakeIndex(ctx context.Context, gopts global.Options, blobs, perPack int) error {
	printer := progress.NewTerminalPrinter(false, gopts.Verbosity, gopts.Term)
	ctx, repo, unlock, err := openWithAppendLock(ctx, gopts, false, printer)
	if err != nil {
		return err
	}
	defer unlock()
	idx := index.NewIndex()
	files := 0
	inFile := 0
	flush := func() error {
		if inFile == 0 {
			return nil
		}
		idx.Finalize()
		if _, err := idx.SaveIndex(ctx, repository.ChkInternal(repo)); err != nil {
			return err
		}
		files++
		inFile = 0
		idx = index.NewIndex()
		return nil
	}
	var n uint64
	for done := 0; done < blobs; {
		count := perPack
		if blobs-done < count {
			count = blobs - done
		}
		list := make(pack.Blobs, 0, count)
		offset := uint(0)
		for i := 0; i < count; i++ {
			n++
			length := uint(600000 + (n*7919)%900000)
			list = append(list, pack.Blob{BlobHandle: restic.BlobHandle{ID: chkID('b', n), Type: restic.DataBlob},
				Length: length, Offset: offset, UncompressedLength: length + 100})
			offset += length
		}
		idx.StorePack(chkID('p', n), list)
		done += count
		inFile += count
		if inFile >= 50000 {
			if err := flush(); err != nil {
				return err
			}
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if err := flush(); err != nil {
		return err
	}
	printer.S("wrote %d index files for %d invented blobs\n", files, blobs)
	return nil
}
