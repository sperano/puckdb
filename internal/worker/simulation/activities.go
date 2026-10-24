package simulation

import (
	"context"

	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
)

// ============================================================================
// Activities — Temporal activity layer for the simulation package.
//
// One Activities instance is constructed at worker startup and registered via
// RegisterActivities, which pins the names already serialized in Temporal
// history. Activity inputs MUST be JSON-marshalable since Temporal serializes
// them across the worker boundary.
//
// The struct is stateless across calls — each method receives the
// per-call payload through its second parameter and consults the
// shared dependencies (Queries, ProviderConfigs) on the receiver.
// Adding a method here does not expose it until RegisterActivities includes
// it. Removing or changing a registered name is NOT backwards-compatible.
// ============================================================================

// Activities holds the shared dependencies every sim activity uses.
//
// Wiring grows by activity: read-only activities (BuildFreeAgentPool,
// UpdateStandings) only need Queries; LLM-driven activities
// (DraftPickActivity, ManageRosterActivity) additionally need Tx for
// atomic writes and ProviderConfigs to construct LLM clients per agent.
//
// The struct is constructed once at worker startup and shared across
// every activity invocation. Methods on the receiver may run
// concurrently — the agent cache is mutex-guarded; Queries / Tx are
// themselves concurrency-safe (pgxpool).
type Activities struct {
	// Queries is the sqlc handle covering every sim query (SimQueries).
	// Each activity method hands it to its own activity type, typed by
	// that activity's query group (freeAgentActivity.q is a
	// freeAgentQueries), so tests drive an activity through a fake of
	// its group alone.
	Queries SimQueries

	// Tx wraps the "begin → run a query batch → commit" dance for the
	// activities that commit atomically (CollectDayStats, DraftPick,
	// ManageRoster, ProcessWaivers, PickTeamName), narrowed per
	// activity like Queries (narrowTx). Read-only activities ignore it.
	Tx Transactor[SimQueries]

	// ProviderConfigs maps llm.Provider → connection details. Each
	// agent's NewAgent call resolves its provider via this map and
	// applies its per-agent timeout / api_base override on top.
	// Unused by BuildFreeAgentPool / UpdateStandings.
	ProviderConfigs map[llm.Provider]llm.ProviderConfig

	// AgentFactory builds an *Agent from the per-pool config. Tests
	// override it to inject a mock LLM client; the production wiring
	// in cmd/worker.go uses defaultAgentFactory which calls NewAgent.
	//
	// Nil means "use defaultAgentFactory" — letting the zero-value
	// struct still work for read-only activities that never need an
	// Agent.
	AgentFactory AgentFactory

	// agents holds one *Agent per (pool, agent, pool size, agent
	// config) so the system prompt + tool list stay byte-stable across
	// the turns of one agent and the LLM client is built once. It is
	// bounded (maxCachedAgents) and drops a pool's agents when this
	// worker writes the pool's terminal status (evictPoolAgents).
	//
	// The zero value is ready to use, which keeps the zero-value
	// Activities struct usable for tests that only exercise read-only
	// activities.
	agents agentCache
}

// AgentFactory is the constructor signature for *Agent. Production
// uses defaultAgentFactory (a thin wrapper around NewAgent); tests
// override it to inject a *Agent backed by a mock llm.Client.
//
// agentID is the sim_agents.id (used as a stable identifier for the
// metric label and error messages — pre-PickTeamName the agent has no
// other name to reference).
type AgentFactory func(agentID int32, cfg AgentConfig, providerConfigs map[llm.Provider]llm.ProviderConfig, numTeams int) (*Agent, error)

// defaultAgentFactory is the production AgentFactory. Equivalent to
// calling NewAgent directly; exists only as a typed value for the
// AgentFactory field's zero-value fallback.
func defaultAgentFactory(agentID int32, cfg AgentConfig, providerConfigs map[llm.Provider]llm.ProviderConfig, numTeams int) (*Agent, error) {
	return NewAgent(agentID, cfg, providerConfigs, numTeams)
}

// getOrCreateAgent returns the cached *Agent for (pool, agent) built
// with exactly this config and pool size, or constructs and caches one.
//
// Every value the constructor reads is part of the key, so a caller
// passing a different numTeams or AgentConfig for the same agent gets
// an agent built from its own inputs instead of another call's cached
// prompt.
func (a *Activities) getOrCreateAgent(poolID, agentID int32, cfg AgentConfig, numTeams int) (*Agent, error) {
	key, err := newAgentKey(poolID, agentID, cfg, numTeams)
	if err != nil {
		return nil, err
	}
	factory := a.AgentFactory
	if factory == nil {
		factory = defaultAgentFactory
	}
	return a.agents.getOrBuild(key, func() (*Agent, error) {
		return factory(agentID, cfg, a.ProviderConfigs, numTeams)
	})
}

// agentSource is the per-worker agent cache the LLM-driven activities
// build their agents from; *Activities implements it, so tests pass an
// Activities with only an AgentFactory set.
type agentSource interface {
	getOrCreateAgent(poolID, agentID int32, cfg AgentConfig, numTeams int) (*Agent, error)
}

// agentEvictor is the part of the agent cache that SetPoolStatus and
// the cost-cap pause use: dropping a pool's agents once it stops.
type agentEvictor interface {
	evictPoolAgents(poolID int32) int
}

// pausingAgentSource is the agent cache of the activities that can
// trip the cost cap (DraftPick, ManageRoster): they build agents and,
// when pauseForCostCap pauses the pool, evict its agents.
type pausingAgentSource interface {
	agentSource
	agentEvictor
}

// evictPoolAgents drops this worker's cached agents of a pool that
// reached a terminal status and reports how many it dropped. Other
// workers keep theirs until the maxCachedAgents bound pushes them out.
func (a *Activities) evictPoolAgents(poolID int32) int {
	return a.agents.evictPool(poolID)
}

// SimQueries is every query the simulation activities run: the union
// of the per-activity query groups, each declared next to its consumer
// (freeAgentQueries in free_agent_activity.go, collectQueries in
// collect_activity.go, ...). Only the worker wiring holds it —
// Activities.Queries and the Transactor that hands each transaction to
// an activity — and every activity narrows it to its own group on
// entry, so a test double implements one group, never this union.
//
// Naming convention: keep the group method signatures byte-identical
// to sqlcdb's so *sqlcdb.Queries structurally satisfies SimQueries
// (checked in transactor.go) — the PgxTransactor relies on this,
// passing sqlcdb.New(tx) directly to the InTx callback without a
// wrapper.
type SimQueries interface {
	freeAgentQueries
	standingsQueries
	collectQueries
	waiverQueries
	rosterContextQueries
	poolStateQueries
	draftCandidateQueries
	draftPickQueries
	manageRosterQueries
	teamNameQueries
}

// playerLookupQueries batches player rows by id (loadPlayersByIDs) for
// the draft candidates and the daily roster context.
type playerLookupQueries interface {
	GetPlayersByIDs(ctx context.Context, ids []int64) ([]sqlcdb.Player, error)
}

// loadPlayersByIDs batches sqlcdb.Player lookups for ids, returning
// id → Player for every id that resolved to a row. Missing ids are
// silently absent from the returned map — GetPlayersByIDs uses
// WHERE id = ANY($1), so it can't distinguish "id not requested"
// from "id requested but no matching row"; callers that need a
// found/not-found distinction check the map directly (`p, ok :=
// players[id]`) rather than relying on an error.
//
// Empty ids → empty map, no DB round trip (mirrors the zero-players
// case every call site already has to handle for an empty roster /
// free-agent list).
func loadPlayersByIDs(ctx context.Context, q playerLookupQueries, ids []int64) (map[int64]sqlcdb.Player, error) {
	if len(ids) == 0 {
		return map[int64]sqlcdb.Player{}, nil
	}
	rows, err := q.GetPlayersByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make(map[int64]sqlcdb.Player, len(rows))
	for _, p := range rows {
		out[p.ID] = p
	}
	return out, nil
}
