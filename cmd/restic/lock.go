package main

import (
	"context"

	"github.com/restic/restic/internal/debug"
	"github.com/restic/restic/internal/global"
	"github.com/restic/restic/internal/repository"
	"github.com/restic/restic/internal/restic"
)

func internalOpenWithLocked(ctx context.Context, gopts global.Options, dryRun bool, exclusive bool, printer restic.Printer) (context.Context, *repository.Repository, func(), error) {
	return internalOpenWithLockedWaitWork(ctx, gopts, dryRun, exclusive, printer, nil)
}

func internalOpenWithLockedWaitWork(ctx context.Context, gopts global.Options, dryRun bool, exclusive bool, printer restic.Printer, startWaitWork func(context.Context, *repository.Repository) func(bool)) (context.Context, *repository.Repository, func(), error) {
	// Index loading and checker validation may emit debug logs. Preserve their
	// normal output order by keeping the synchronous path when debug is enabled.
	if startWaitWork != nil && debug.IsEnabled() {
		startWaitWork = nil
	}

	repo, err := global.OpenRepository(ctx, gopts, printer)
	if err != nil {
		return nil, nil, nil, err
	}

	unlock := func() {}
	if !dryRun {
		printRetry := func(msg string) {
			if !gopts.JSON {
				printer.P("%s", msg)
			}
		}
		if startWaitWork == nil {
			unlock, ctx, err = repository.LockRepo(ctx, repo, exclusive, gopts.RetryLock, printRetry, printer.E)
		} else {
			unlock, ctx, err = repository.LockRepoWithWaitWork(ctx, repo, exclusive, gopts.RetryLock, printRetry, printer.E, func(lockCtx context.Context) func(bool) {
				return startWaitWork(lockCtx, repo)
			})
		}
		if err != nil {
			return nil, nil, nil, err
		}
	} else {
		repo.SetDryRun()
	}

	return ctx, repo, unlock, nil
}

func openWithReadLock(ctx context.Context, gopts global.Options, noLock bool, printer restic.Printer) (context.Context, *repository.Repository, func(), error) {
	// TODO enforce read-only operations once the locking code has moved to the repository
	// As in-depth hardening, put the repository into read-only mode if noLock is true
	// Not possible if the repository has to be locked.
	return internalOpenWithLocked(ctx, gopts, noLock, false, printer)
}

func openWithReadLockAndWaitWork(ctx context.Context, gopts global.Options, noLock bool, printer restic.Printer, startWaitWork func(context.Context, *repository.Repository) func(bool)) (context.Context, *repository.Repository, func(), error) {
	return internalOpenWithLockedWaitWork(ctx, gopts, noLock, false, printer, startWaitWork)
}

func openWithAppendLock(ctx context.Context, gopts global.Options, dryRun bool, printer restic.Printer) (context.Context, *repository.Repository, func(), error) {
	// TODO enforce non-exclusive operations once the locking code has moved to the repository
	return internalOpenWithLocked(ctx, gopts, dryRun, false, printer)
}

func openWithAppendLockAndWaitWork(ctx context.Context, gopts global.Options, dryRun bool, printer restic.Printer, startWaitWork func(context.Context, *repository.Repository) func(bool)) (context.Context, *repository.Repository, func(), error) {
	return internalOpenWithLockedWaitWork(ctx, gopts, dryRun, false, printer, startWaitWork)
}

func openWithExclusiveLock(ctx context.Context, gopts global.Options, dryRun bool, printer restic.Printer) (context.Context, *repository.Repository, func(), error) {
	return internalOpenWithLocked(ctx, gopts, dryRun, true, printer)
}

func openWithExclusiveLockAndWaitWork(ctx context.Context, gopts global.Options, dryRun bool, printer restic.Printer, startWaitWork func(context.Context, *repository.Repository) func(bool)) (context.Context, *repository.Repository, func(), error) {
	return internalOpenWithLockedWaitWork(ctx, gopts, dryRun, true, printer, startWaitWork)
}
