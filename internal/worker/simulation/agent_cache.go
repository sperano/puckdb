package simulation

import (
	"container/list"
	"encoding/json"
	"fmt"
	"sync"
)

// maxCachedAgents bounds how many *Agent values one worker keeps. A
// worker typically runs a handful of pools of a dozen or so agents, so
// every live agent fits; the bound only matters for agents of pools
// that ended without this worker seeing their terminal status write
// (another worker ran it, or the workflow was terminated). Evicting a
// live agent is safe: rebuilding it yields the same byte-stable system
// prompt, so the provider-side prompt cache still hits.
const maxCachedAgents = 256

// agentKey identifies one cached *Agent by everything its constructor
// reads: the pool and agent ids, the pool size baked into the system
// prompt, and the agent's whole config (provider, model, strategy,
// timeout, API base, ...) in its JSON form, so a field added to
// AgentConfig joins the key without touching this type.
type agentKey struct {
	poolID   int32
	agentID  int32
	numTeams int
	config   string
}

// newAgentKey builds the cache key for one agent build request.
func newAgentKey(poolID, agentID int32, cfg AgentConfig, numTeams int) (agentKey, error) {
	encoded, err := json.Marshal(cfg)
	if err != nil {
		return agentKey{}, fmt.Errorf("simulation: agent cache key (agent %d): %w", agentID, err)
	}
	return agentKey{poolID: poolID, agentID: agentID, numTeams: numTeams, config: string(encoded)}, nil
}

// agentCacheEntry is one LRU list element's value.
type agentCacheEntry struct {
	key   agentKey
	agent *Agent
}

// agentCache is a mutex-guarded LRU of *Agent values. The zero value is
// ready to use with maxCachedAgents as its bound.
type agentCache struct {
	mu sync.Mutex
	// capacity overrides maxCachedAgents when > 0 (tests).
	capacity int
	// order holds *agentCacheEntry values, most recently used first.
	order   *list.List
	entries map[agentKey]*list.Element
}

// getOrBuild returns the cached agent for key, or calls build and
// caches its result, evicting the least recently used entry when the
// cache is full. A build error caches nothing. build runs under the
// lock: NewAgent does no I/O, and holding the lock keeps two
// concurrent first requests from building twice.
func (c *agentCache) getOrBuild(key agentKey, build func() (*Agent, error)) (*Agent, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.entries == nil {
		c.order = list.New()
		c.entries = map[agentKey]*list.Element{}
	}
	if elem, ok := c.entries[key]; ok {
		c.order.MoveToFront(elem)
		return elem.Value.(*agentCacheEntry).agent, nil
	}

	agent, err := build()
	if err != nil {
		return nil, err
	}
	c.entries[key] = c.order.PushFront(&agentCacheEntry{key: key, agent: agent})
	for c.order.Len() > c.limit() {
		c.remove(c.order.Back())
	}
	return agent, nil
}

// evictPool drops every cached agent of poolID and reports how many
// it dropped.
func (c *agentCache) evictPool(poolID int32) int {
	c.mu.Lock()
	defer c.mu.Unlock()

	evicted := 0
	for key, elem := range c.entries {
		if key.poolID == poolID {
			c.remove(elem)
			evicted++
		}
	}
	return evicted
}

// len reports the number of cached agents.
func (c *agentCache) len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.entries)
}

// limit is the effective bound.
func (c *agentCache) limit() int {
	if c.capacity > 0 {
		return c.capacity
	}
	return maxCachedAgents
}

// remove unlinks elem; the caller holds mu.
func (c *agentCache) remove(elem *list.Element) {
	c.order.Remove(elem)
	delete(c.entries, elem.Value.(*agentCacheEntry).key)
}
