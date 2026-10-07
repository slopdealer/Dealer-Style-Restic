package repository

import "github.com/restic/restic/internal/repository/pack"

// extraCheckWorker reports whether ReadPacks may hand packs to its extra
// worker. That worker reads through the backend's additional load slot, which
// only the local backend at its default connection count grants. On every
// other backend (REST, S3, SFTP, a local repository opened with an explicit
// -o local.connections=N) its loads would join the ordinary connection
// semaphore: one more check worker than the backend has connections, so that
// a pack read waits for a connection while it already counts as started.
// There the extra worker takes nothing and check runs with exactly the
// configured number of workers, as stock does.
func (r *Repository) extraCheckWorker() bool {
	return r.AdditionalRestoreWorker()
}

// extraCheckTask reports whether a pack is offered to the extra worker first.
func extraCheckTask(hasSlot bool, blobs pack.Blobs, size int64) bool {
	return hasSlot && (smallCheckTask(blobs) || capacityCheckTask(blobs, size))
}
