package draftrank

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestSnapshotCache_EvictsLeastRecentlyUsed(t *testing.T) {
	const capacity = 2
	cache := newSnapshotCache(capacity)
	first, second, third := uuid.New(), uuid.New(), uuid.New()
	cache.put(first, &Snapshot{})
	cache.put(second, &Snapshot{})
	_, cached := cache.get(first)
	assert.True(t, cached)
	cache.put(third, &Snapshot{})

	_, cached = cache.get(second)
	assert.False(t, cached, "second was least recently used")
	_, cached = cache.get(first)
	assert.True(t, cached)
	_, cached = cache.get(third)
	assert.True(t, cached)

	cache.put(third, &Snapshot{})
	assert.Len(t, cache.order, capacity, "re-putting an entry does not grow the cache")
}
