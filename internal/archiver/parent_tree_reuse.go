package archiver

import (
	"sync/atomic"

	"github.com/restic/restic/internal/restic"
)

// This budget covers extra plaintext lifetime, not the iterator's existing
// lifetime. A full budget or oversized allocation simply uses ordinary hashing.
const parentTreeRetentionMax = 1 << 20

type parentTreeBudget struct {
	used atomic.Int64
}

type parentTreeBytes struct {
	id      restic.ID
	payload []byte
	budget  *parentTreeBudget
}

func (b *parentTreeBudget) retain(id restic.ID, payload []byte) *parentTreeBytes {
	size := int64(cap(payload))
	if size > parentTreeRetentionMax {
		return nil
	}
	for {
		used := b.used.Load()
		if used+size > parentTreeRetentionMax {
			return nil
		}
		if b.used.CompareAndSwap(used, used+size) {
			return &parentTreeBytes{id: id, payload: payload, budget: b}
		}
	}
}

// The traversal owns this until dispatch; then the tree job owns it. Early
// traversal exits, cancelled dispatch and completed jobs all release ownership.
func (p *parentTreeBytes) release() {
	if p != nil {
		p.budget.used.Add(-int64(cap(p.payload)))
		p.payload = nil
	}
}
