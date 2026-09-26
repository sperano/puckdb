package draftrank

import (
	"slices"
	"sync"

	"github.com/google/uuid"
)

// snapshotCache keeps the most recently used decoded snapshots.
type snapshotCache struct {
	mu       sync.Mutex
	capacity int
	order    []uuid.UUID
	entries  map[uuid.UUID]*Snapshot
}

func newSnapshotCache(capacity int) *snapshotCache {
	return &snapshotCache{capacity: capacity, entries: make(map[uuid.UUID]*Snapshot, capacity)}
}

func (c *snapshotCache) get(id uuid.UUID) (*Snapshot, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	snapshot, cached := c.entries[id]
	if cached {
		c.touch(id)
	}
	return snapshot, cached
}

func (c *snapshotCache) put(id uuid.UUID, snapshot *Snapshot) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, cached := c.entries[id]; !cached && len(c.order) >= c.capacity {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	c.entries[id] = snapshot
	c.touch(id)
}

// touch moves id to the most recently used end.
func (c *snapshotCache) touch(id uuid.UUID) {
	c.order = slices.DeleteFunc(c.order, func(other uuid.UUID) bool { return other == id })
	c.order = append(c.order, id)
}
