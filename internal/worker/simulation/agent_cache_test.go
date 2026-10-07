package simulation

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cacheTestConfig is the agent config the cache tests share.
func cacheTestConfig() AgentConfig {
	return AgentConfig{Provider: "anthropic", Model: "claude-haiku-4-5", Strategy: "balanced"}
}

// countingFactory wraps the real NewAgent and counts its calls, so the
// tests see the production system prompt and every build.
func countingFactory(builds *atomic.Int32) AgentFactory {
	return func(agentID int32, cfg AgentConfig, pcs map[llm.Provider]llm.ProviderConfig, numTeams int) (*Agent, error) {
		builds.Add(1)
		return NewAgent(agentID, cfg, pcs, numTeams)
	}
}

// newCacheTestActivities returns Activities that build real agents
// through countingFactory.
func newCacheTestActivities(builds *atomic.Int32) *Activities {
	return &Activities{
		ProviderConfigs: defaultProviderConfigs(),
		AgentFactory:    countingFactory(builds),
	}
}

// The SIM-I1 regression: an agent first built for a 0-team request
// (the old team-name turn) must not serve a later 10-team request, and
// each pool size gets its own prompt and entry.
func TestGetOrCreateAgent_DistinctTeamCountsGetDistinctPrompts(t *testing.T) {
	var builds atomic.Int32
	acts := newCacheTestActivities(&builds)
	cfg := cacheTestConfig()

	zero, err := acts.getOrCreateAgent(1, 7, cfg, 0)
	require.NoError(t, err)
	ten, err := acts.getOrCreateAgent(1, 7, cfg, 10)
	require.NoError(t, err)
	twelve, err := acts.getOrCreateAgent(1, 7, cfg, 12)
	require.NoError(t, err)

	assert.Contains(t, zero.SystemPrompt(), "a 0-team rotisserie pool")
	assert.Contains(t, ten.SystemPrompt(), "a 10-team rotisserie pool")
	assert.Contains(t, twelve.SystemPrompt(), "a 12-team rotisserie pool")
	assert.NotSame(t, zero, ten)
	assert.NotSame(t, ten, twelve)
	assert.Equal(t, int32(3), builds.Load())
	assert.Equal(t, 3, acts.agents.len())
}

func TestGetOrCreateAgent_SameInputsReuseAgent(t *testing.T) {
	var builds atomic.Int32
	acts := newCacheTestActivities(&builds)
	cfg := cacheTestConfig()

	first, err := acts.getOrCreateAgent(1, 7, cfg, 10)
	require.NoError(t, err)
	second, err := acts.getOrCreateAgent(1, 7, cfg, 10)
	require.NoError(t, err)

	assert.Same(t, first, second)
	assert.Equal(t, int32(1), builds.Load())
	assert.Equal(t, first.SystemPrompt(), second.SystemPrompt())
}

// Every AgentConfig field the constructor reads changes the agent, so
// each one must produce a separate entry.
func TestGetOrCreateAgent_ConfigChangesAreSeparateEntries(t *testing.T) {
	temperature := 0.2
	variants := map[string]func(*AgentConfig){
		"strategy":    func(c *AgentConfig) { c.Strategy = "punt PIM" },
		"model":       func(c *AgentConfig) { c.Model = "claude-sonnet-4-7" },
		"provider":    func(c *AgentConfig) { c.Provider = "openai" },
		"timeout":     func(c *AgentConfig) { c.TimeoutSeconds = 45 },
		"api base":    func(c *AgentConfig) { c.APIBase = "https://example.test/v1" },
		"max tokens":  func(c *AgentConfig) { c.MaxTokens = 512 },
		"temperature": func(c *AgentConfig) { c.Temperature = &temperature },
	}
	for name, mutate := range variants {
		t.Run(name, func(t *testing.T) {
			var builds atomic.Int32
			acts := newCacheTestActivities(&builds)
			base := cacheTestConfig()
			changed := cacheTestConfig()
			mutate(&changed)

			a, err := acts.getOrCreateAgent(1, 7, base, 10)
			require.NoError(t, err)
			b, err := acts.getOrCreateAgent(1, 7, changed, 10)
			require.NoError(t, err)

			assert.NotSame(t, a, b)
			assert.Equal(t, changed, b.Config)
			assert.Equal(t, int32(2), builds.Load())
		})
	}
}

// Same pool size and config, but a different pool or agent, is still a
// different agent.
func TestGetOrCreateAgent_PoolAndAgentAreSeparateEntries(t *testing.T) {
	var builds atomic.Int32
	acts := newCacheTestActivities(&builds)
	cfg := cacheTestConfig()

	a, err := acts.getOrCreateAgent(1, 7, cfg, 10)
	require.NoError(t, err)
	otherAgent, err := acts.getOrCreateAgent(1, 8, cfg, 10)
	require.NoError(t, err)
	otherPool, err := acts.getOrCreateAgent(2, 7, cfg, 10)
	require.NoError(t, err)

	assert.NotSame(t, a, otherAgent)
	assert.NotSame(t, a, otherPool)
	assert.Equal(t, int32(8), otherAgent.ID)
	assert.Equal(t, int32(3), builds.Load())
}

func TestGetOrCreateAgent_FactoryErrorIsNotCached(t *testing.T) {
	calls := 0
	acts := &Activities{
		AgentFactory: func(int32, AgentConfig, map[llm.Provider]llm.ProviderConfig, int) (*Agent, error) {
			calls++
			if calls == 1 {
				return nil, fmt.Errorf("provider down")
			}
			return &Agent{}, nil
		},
	}

	_, err := acts.getOrCreateAgent(1, 7, cacheTestConfig(), 10)
	require.ErrorContains(t, err, "provider down")
	assert.Zero(t, acts.agents.len())

	got, err := acts.getOrCreateAgent(1, 7, cacheTestConfig(), 10)
	require.NoError(t, err)
	assert.NotNil(t, got)
	assert.Equal(t, 2, calls)
}

// A config the key cannot encode fails the request instead of sharing
// an entry with another config.
func TestGetOrCreateAgent_UnencodableConfigErrors(t *testing.T) {
	var builds atomic.Int32
	acts := newCacheTestActivities(&builds)
	cfg := cacheTestConfig()
	nan := math.NaN()
	cfg.Temperature = &nan

	_, err := acts.getOrCreateAgent(1, 7, cfg, 10)
	require.ErrorContains(t, err, "agent cache key")
	assert.Zero(t, builds.Load())
}

// Concurrent first requests for one agent build it once and all get
// the same value.
func TestGetOrCreateAgent_ConcurrentRequestsBuildOnce(t *testing.T) {
	const callers = 32
	var builds atomic.Int32
	acts := newCacheTestActivities(&builds)
	cfg := cacheTestConfig()

	got := make([]*Agent, callers)
	var wg sync.WaitGroup
	for i := range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			agent, err := acts.getOrCreateAgent(1, 7, cfg, 10)
			assert.NoError(t, err)
			got[i] = agent
		}()
	}
	wg.Wait()

	assert.Equal(t, int32(1), builds.Load())
	for _, agent := range got {
		assert.Same(t, got[0], agent)
	}
}

// The cache never holds more than its capacity and drops the least
// recently used entry first; an entry touched since stays.
func TestAgentCache_BoundEvictsLeastRecentlyUsed(t *testing.T) {
	const capacity = 2
	var builds atomic.Int32
	acts := newCacheTestActivities(&builds)
	acts.agents.capacity = capacity
	cfg := cacheTestConfig()

	first, err := acts.getOrCreateAgent(1, 1, cfg, 10)
	require.NoError(t, err)
	_, err = acts.getOrCreateAgent(1, 2, cfg, 10)
	require.NoError(t, err)
	// Touch agent 1 so agent 2 is the least recently used.
	again, err := acts.getOrCreateAgent(1, 1, cfg, 10)
	require.NoError(t, err)
	require.Same(t, first, again)

	_, err = acts.getOrCreateAgent(1, 3, cfg, 10)
	require.NoError(t, err)
	assert.Equal(t, capacity, acts.agents.len())
	assert.Equal(t, int32(3), builds.Load())

	kept, err := acts.getOrCreateAgent(1, 1, cfg, 10)
	require.NoError(t, err)
	assert.Same(t, first, kept, "recently used agent must survive the eviction")
	assert.Equal(t, int32(3), builds.Load())

	_, err = acts.getOrCreateAgent(1, 2, cfg, 10)
	require.NoError(t, err)
	assert.Equal(t, int32(4), builds.Load(), "least recently used agent must have been evicted")
	assert.Equal(t, capacity, acts.agents.len())
}

func TestAgentCache_DefaultBound(t *testing.T) {
	var cache agentCache
	assert.Equal(t, maxCachedAgents, cache.limit())
	for i := range maxCachedAgents + 1 {
		key, err := newAgentKey(1, int32(i), cacheTestConfig(), 10)
		require.NoError(t, err)
		_, err = cache.getOrBuild(key, func() (*Agent, error) { return &Agent{}, nil })
		require.NoError(t, err)
	}
	assert.Equal(t, maxCachedAgents, cache.len())
}

// Ending one pool drops only its agents: the other pool keeps reusing
// its cached agents, and the ended pool's are rebuilt if asked again.
func TestEvictPoolAgents_DropsOnlyThatPool(t *testing.T) {
	var builds atomic.Int32
	acts := newCacheTestActivities(&builds)
	cfg := cacheTestConfig()

	ended, err := acts.getOrCreateAgent(1, 7, cfg, 10)
	require.NoError(t, err)
	_, err = acts.getOrCreateAgent(1, 8, cfg, 10)
	require.NoError(t, err)
	live, err := acts.getOrCreateAgent(2, 9, cfg, 12)
	require.NoError(t, err)

	acts.evictPoolAgents(1)
	assert.Equal(t, 1, acts.agents.len())

	stillLive, err := acts.getOrCreateAgent(2, 9, cfg, 12)
	require.NoError(t, err)
	assert.Same(t, live, stillLive)

	rebuilt, err := acts.getOrCreateAgent(1, 7, cfg, 10)
	require.NoError(t, err)
	assert.NotSame(t, ended, rebuilt)
	assert.Equal(t, ended.SystemPrompt(), rebuilt.SystemPrompt(),
		"a rebuilt agent carries the same byte-stable prompt")
	assert.Equal(t, int32(4), builds.Load())
}

func TestEvictPoolAgents_UnknownPoolIsNoop(t *testing.T) {
	var cache agentCache
	assert.Zero(t, cache.evictPool(1))

	key, err := newAgentKey(1, 7, cacheTestConfig(), 10)
	require.NoError(t, err)
	_, err = cache.getOrBuild(key, func() (*Agent, error) { return &Agent{}, nil })
	require.NoError(t, err)
	assert.Zero(t, cache.evictPool(2))
	assert.Equal(t, 1, cache.len())
}

func TestPoolStatusEnded(t *testing.T) {
	cases := map[PoolStatus]bool{
		PoolStatusDraft:     false,
		PoolStatusRunning:   false,
		PoolStatusPaused:    true,
		PoolStatusComplete:  true,
		PoolStatusCancelled: true,
	}
	for status, want := range cases {
		assert.Equal(t, want, status.ended(), "status=%s", status)
	}
}
