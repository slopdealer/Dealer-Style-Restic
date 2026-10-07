package repository

import "context"

// A repack saves on the context of its own group of workers, as upstream
// does: when one worker fails, a save of another worker that waits for a free
// uploader is given up, and the repack returns the error although uploads are
// stalled.
//
// Upstream's saves run inside the workers. Here they are submitted to the
// uploader's group, which takes the first error any of its functions returns
// as the error of the whole upload. A save that was given up because the
// repack was cancelled must not report that cancellation there as a failure
// of its own: the repack returns the reason, as it does upstream. The context
// of the repack's workers is marked for this.
type repackSaveKey struct{}

// withRepackSaves returns the context from which a repack derives the context
// of its workers and saves.
func withRepackSaves(ctx context.Context) context.Context {
	return context.WithValue(ctx, repackSaveKey{}, true)
}

// asyncSaveResult returns what an asynchronous save reports to the uploader's
// group. The callback of the save has received err in every case.
func asyncSaveResult(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil && ctx.Value(repackSaveKey{}) != nil {
		return nil
	}
	return err
}
