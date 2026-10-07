package repository

import "github.com/restic/restic/internal/restic"

// prunePackRun accumulates consecutive updates to one pack without hashing its
// ID for every blob. The walk retains its order, including duplicate selection.
// It has one sequential owner; nobody reads packs until the walk is flushed.
// Revisiting a pack reloads the earlier committed value from the map.
type prunePackRun struct {
	packs  map[restic.ID]packInfo
	id     restic.ID
	info   packInfo
	active bool
}

func (r *prunePackRun) get(id restic.ID) *packInfo {
	if !r.active || r.id != id {
		r.flush()
		r.id = id
		r.info = r.packs[id]
		r.active = true
	}
	return &r.info
}

func (r *prunePackRun) flush() {
	if r.active {
		r.packs[r.id] = r.info
		r.active = false
	}
}
