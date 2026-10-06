package restic

import "context"

// RestoreHoldBudget counts the bytes that the ordinary restore workers of one
// restore hold at the same time in range windows and decoded blobs. A worker
// takes what a window will hold before it allocates or reads it and gives it
// back after the last callback of that window. The configured workers take
// without waiting (Force), as they always did; the additional worker waits for
// room (Acquire), so it only adds to what the others hold while that is low.
// The implementation is the restorer's.
//
// This file declares types only; the implementation is in the restorer
// package. On the build that was measured, code added to this package moved
// the key derivation code in the binary, and its run time changed by about
// 3% (6 ms per process) in one direction or the other. The effect belongs to
// one compiler version, one linker layout and one machine.
type RestoreHoldBudget interface {
	// Force takes n bytes without waiting and returns the amount taken.
	Force(n int64) int64
	// Acquire waits until there is room and returns the amount taken.
	Acquire(ctx context.Context, n int64) (int64, error)
	// Release returns bytes taken by Force or Acquire.
	Release(n int64)
	// Workers is the number of configured ordinary workers of the restore.
	Workers() int
}

// RestoreHoldKey is the context key of a RestoreHoldUse: the pack streams of
// an ordinary restore worker carry one.
type RestoreHoldKey struct{}

// RestoreHoldUse is how one ordinary restore worker uses the budget. Wait is
// true for the additional worker only.
type RestoreHoldUse struct {
	Budget RestoreHoldBudget
	Wait   bool
}
