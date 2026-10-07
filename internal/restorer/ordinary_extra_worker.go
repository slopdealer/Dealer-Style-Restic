package restorer

import (
	"runtime"

	"github.com/restic/restic/internal/restic"
)

// One further ordinary restore worker runs beside the configured ones, and
// only on a backend that grants the additional load slot: the local backend
// with its default connection count (fileRestorer.additionalWorker, set from
// the repository). A worker is a CPU pipeline (decrypt, decode, hash, write),
// so the additional one is also only started where the configured workers
// leave a core free. Everywhere else a restore runs exactly as before.
//
// What an ordinary worker holds is the range window it reads (its ciphertext)
// and the decoded copy of a compressed blob. All ordinary workers of a restore
// report those bytes, per window, to one hold budget
// (restoreHoldBudget, behind restic.RestoreHoldBudget). The configured workers never wait on it. The
// additional worker starts a window only when the budget has room for it, so
// it works while the others hold little and stands still while they hold
// large blobs: it adds to the low stretches, not to the peak. It also shares
// the block decoders of the configured workers (repository.getRestoreZstdDecoder).
const (
	// per configured worker; the budget does not grow with the extra worker
	ordinaryRestoreHoldPerWorker = 5 << 20
)

func extraOrdinaryRestoreWorkers(configured int) int {
	if configured < runtime.GOMAXPROCS(0) {
		return 1
	}
	return 0
}

func newOrdinaryRestoreHoldBudget(workers int) restic.RestoreHoldBudget {
	if workers < 1 {
		workers = 1
	}
	return newRestoreHoldBudget(int64(workers)*ordinaryRestoreHoldPerWorker, workers)
}
