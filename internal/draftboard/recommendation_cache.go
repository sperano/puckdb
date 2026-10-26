package draftboard

import (
	"slices"
	"sync"

	"github.com/sperano/puckdb/internal/draftrecommend"
)

// DefaultRecommendationCacheSize bounds the number of persisted
// recommendation results retained by one board service process.
const DefaultRecommendationCacheSize = 64

// recommendationCache keeps the most recently used recommendation runs.
type recommendationCache struct {
	mu       sync.Mutex
	capacity int
	order    []string
	entries  map[string]draftrecommend.StoredRun
}

func newRecommendationCache(capacity int) *recommendationCache {
	return &recommendationCache{
		capacity: capacity,
		entries:  make(map[string]draftrecommend.StoredRun, capacity),
	}
}

func (c *recommendationCache) get(key string) (draftrecommend.StoredRun, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	run, cached := c.entries[key]
	if cached {
		c.touch(key)
	}
	return run, cached
}

func (c *recommendationCache) put(key string, run draftrecommend.StoredRun) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, cached := c.entries[key]; !cached && len(c.order) >= c.capacity {
		delete(c.entries, c.order[0])
		c.order = c.order[1:]
	}
	c.entries[key] = run
	c.touch(key)
}

func (c *recommendationCache) touch(key string) {
	c.order = slices.DeleteFunc(c.order, func(other string) bool { return other == key })
	c.order = append(c.order, key)
}
