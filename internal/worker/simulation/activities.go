package simulation

import (
	"context"
	"fmt"
	"strconv"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// Activities — Temporal activity layer for the simulation package.
//
// One Activities instance is constructed at worker startup and
// registered via `worker.RegisterActivity(activities)`. Methods on
// this struct become Temporal activities; their inputs MUST be
// JSON-marshalable since Temporal serializes them across the worker
// boundary.
//
// The struct is stateless across calls — each method receives the
// per-call payload through its second parameter and consults the
// shared dependencies (Queries, ProviderConfigs) on the receiver.
// Adding a method here exposes a new Temporal activity; removing one
// requires a worker restart and is NOT a backwards-compatible change.
// ============================================================================

// Activities holds the shared dependencies every sim activity uses.
//
// Wiring grows by activity: read-only activities (BuildFreeAgentPool,
// UpdateStandings) only need Queries; LLM-driven activities
// (DraftPickActivity, ManageRosterActivity) additionally need Tx for
// atomic writes, Signaler for cost-cap pause signals, and
// ProviderConfigs to construct LLM clients per agent.
//
// The struct is constructed once at worker startup and shared across
// every activity invocation. Methods on the receiver may run
// concurrently — the agent cache is mutex-guarded; Queries / Tx /
// Signaler are themselves either stateless or already concurrency-
// safe (pgxpool / Temporal client).
type Activities struct {
	// Queries is the narrowed sqlc handle covering every sim query
	// any read-only activity in this package needs. Tests inject a
	// mock that implements just the methods their activity touches.
	Queries SimQueries

	// Tx wraps the "begin → run a query batch → commit" dance for
	// atomic-commit activities (DraftPickActivity, ManageRosterActivity).
	// Read-only activities ignore it.
	Tx Transactor

	// Signaler sends signals back to the parent workflow. Used by
	// LLM activities to fire "pause" when the cost cap is reached.
	Signaler WorkflowSignaler

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

// evictPoolAgents drops this worker's cached agents of a pool that
// reached a terminal status and reports how many it dropped. Other
// workers keep theirs until the maxCachedAgents bound pushes them out.
func (a *Activities) evictPoolAgents(poolID int32) int {
	return a.agents.evictPool(poolID)
}

// SimQueries is the union of sqlc-generated query methods the
// simulation activities use. Methods get added here as new activities
// land. Keeping the union narrow (rather than `*sqlcdb.Queries`) makes
// tests trivial — a mock only needs to implement what its activity
// calls.
//
// Naming convention: keep the method signatures byte-identical to
// sqlcdb's so *sqlcdb.Queries structurally satisfies SimQueries — the
// PgxTransactor relies on this, passing sqlcdb.New(tx) directly to the
// InTx callback without a wrapper.
type SimQueries interface {
	// Read-only — used by BuildFreeAgentPool, UpdateStandings, the
	// pre-flight checks in DraftPickActivity / ManageRosterActivity.
	ListSimFreeAgentCandidates(ctx context.Context, arg sqlcdb.ListSimFreeAgentCandidatesParams) ([]int64, error)
	ListSimAgentTotalsByPool(ctx context.Context, poolID int32) ([]sqlcdb.SimAgentTotal, error)
	GetSimPool(ctx context.Context, id int32) (sqlcdb.SimPool, error)
	GetSimTransactionDraftPick(ctx context.Context, arg sqlcdb.GetSimTransactionDraftPickParams) (sqlcdb.SimTransaction, error)
	ExistsSimDailyTurnMarker(ctx context.Context, arg sqlcdb.ExistsSimDailyTurnMarkerParams) (bool, error)

	// Atomic-commit writes — called inside Transactor.InTx callbacks.
	UpsertSimStanding(ctx context.Context, arg sqlcdb.UpsertSimStandingParams) error
	InsertSimRoster(ctx context.Context, arg sqlcdb.InsertSimRosterParams) error
	// ExistsSimRosterPlayer is the commit-time claimability probe (add_player
	// and waiver resolution) that converts a UNIQUE(pool_id, player_id)
	// conflict into a clean rejection instead of a tx-aborting violation.
	ExistsSimRosterPlayer(ctx context.Context, arg sqlcdb.ExistsSimRosterPlayerParams) (bool, error)
	// DeleteSimRosterRows is the rows-affected drop used by waiver resolution.
	// The plan step already confirmed the row exists inside the transaction,
	// so 0 rows here is an invariant violation that fails the transaction —
	// not a tolerated "vanished drop" (that case is handled by the plan).
	DeleteSimRosterRows(ctx context.Context, arg sqlcdb.DeleteSimRosterRowsParams) (int64, error)
	InsertSimTransactionDraftPick(ctx context.Context, arg sqlcdb.InsertSimTransactionDraftPickParams) (sqlcdb.SimTransaction, error)
	InsertSimTransactionCostCapReached(ctx context.Context, arg sqlcdb.InsertSimTransactionCostCapReachedParams) (sqlcdb.SimTransaction, error)
	IncrementSimPoolLLMCost(ctx context.Context, arg sqlcdb.IncrementSimPoolLLMCostParams) (pgtype.Numeric, error)
	UpdateSimPoolStatus(ctx context.Context, arg sqlcdb.UpdateSimPoolStatusParams) error
	SetSimAgentDraftPosition(ctx context.Context, arg sqlcdb.SetSimAgentDraftPositionParams) error
	SetSimAgentTeamNameAndSummary(ctx context.Context, arg sqlcdb.SetSimAgentTeamNameAndSummaryParams) error

	// ManageRosterActivity writes — atomic-commit handlers per tool action.
	DeleteSimRoster(ctx context.Context, arg sqlcdb.DeleteSimRosterParams) error
	UpdateSimRosterSlot(ctx context.Context, arg sqlcdb.UpdateSimRosterSlotParams) error
	UpdateSimAgentNotes(ctx context.Context, arg sqlcdb.UpdateSimAgentNotesParams) error
	InsertSimWaiverClaim(ctx context.Context, arg sqlcdb.InsertSimWaiverClaimParams) (sqlcdb.SimWaiverClaim, error)
	InsertSimLineupMove(ctx context.Context, arg sqlcdb.InsertSimLineupMoveParams) error
	InsertSimTransactionAdd(ctx context.Context, arg sqlcdb.InsertSimTransactionAddParams) (sqlcdb.SimTransaction, error)
	InsertSimTransactionDrop(ctx context.Context, arg sqlcdb.InsertSimTransactionDropParams) (sqlcdb.SimTransaction, error)
	InsertSimTransactionClaim(ctx context.Context, arg sqlcdb.InsertSimTransactionClaimParams) (sqlcdb.SimTransaction, error)
	InsertSimTransactionLineupSet(ctx context.Context, arg sqlcdb.InsertSimTransactionLineupSetParams) (sqlcdb.SimTransaction, error)
	InsertSimTransactionPass(ctx context.Context, arg sqlcdb.InsertSimTransactionPassParams) (sqlcdb.SimTransaction, error)
	InsertSimTransactionDailyTurnDone(ctx context.Context, arg sqlcdb.InsertSimTransactionDailyTurnDoneParams) error
	InsertSimTransactionError(ctx context.Context, arg sqlcdb.InsertSimTransactionErrorParams) (sqlcdb.SimTransaction, error)

	// CollectDayStatsActivity reads + writes — gathers a day's NHL
	// game stats and rolls them up to per-player + per-agent rows.
	ListSimDayGames(ctx context.Context, arg sqlcdb.ListSimDayGamesParams) ([]sqlcdb.ListSimDayGamesRow, error)
	GetGameSkaterStatsByGame(ctx context.Context, gameID int64) ([]sqlcdb.GetGameSkaterStatsByGameRow, error)
	GetGameGoalieStatsByGame(ctx context.Context, gameID int64) ([]sqlcdb.GetGameGoalieStatsByGameRow, error)
	ListSimActiveRosterByAgent(ctx context.Context, arg sqlcdb.ListSimActiveRosterByAgentParams) ([]sqlcdb.SimRoster, error)
	UpsertSimAgentDailyPlayerStat(ctx context.Context, arg sqlcdb.UpsertSimAgentDailyPlayerStatParams) error
	AggregateSimAgentDailyStats(ctx context.Context, arg sqlcdb.AggregateSimAgentDailyStatsParams) error
	RecomputeSimAgentTotalsCounting(ctx context.Context, poolID int32) error
	RecomputeSimAgentTotalsGAA(ctx context.Context, poolID int32) error

	// ProcessWaiversActivity reads + writes — resolves due claims by
	// priority, applies winner roster mutations, demotes winners.
	ListSimWaiverClaimsDue(ctx context.Context, arg sqlcdb.ListSimWaiverClaimsDueParams) ([]sqlcdb.SimWaiverClaim, error)
	// ListSimWaiverClaimsForDuePlayers groups all pending claims per player
	// (across due dates) so the contested resolution honors waiver priority.
	ListSimWaiverClaimsForDuePlayers(ctx context.Context, arg sqlcdb.ListSimWaiverClaimsForDuePlayersParams) ([]sqlcdb.SimWaiverClaim, error)
	ListSimWaiverPriorityByPool(ctx context.Context, poolID int32) ([]sqlcdb.SimWaiverPriority, error)
	UpdateSimWaiverPriority(ctx context.Context, arg sqlcdb.UpdateSimWaiverPriorityParams) error
	UpdateSimWaiverClaimStatus(ctx context.Context, arg sqlcdb.UpdateSimWaiverClaimStatusParams) error
	// CancelSimWaiverClaimsForPlayer voids the loser/cross-day pending claims
	// once a player is won, so they can't resolve as phantom uncontested wins.
	CancelSimWaiverClaimsForPlayer(ctx context.Context, arg sqlcdb.CancelSimWaiverClaimsForPlayerParams) error

	// SimPoolWorkflow boot-up reads — load pool config + agent list +
	// season calendar range at workflow start (and after ContinueAsNew).
	ListSimAgentsByPool(ctx context.Context, poolID int32) ([]sqlcdb.SimAgent, error)
	GetSeason(ctx context.Context, id int32) (sqlcdb.Season, error)

	// LoadDraftCandidates reads — prior-season club stats + batched
	// per-player position lookups for the draft phase. Also used by
	// BuildManageRosterContext's roster/position-catalog reads (see
	// loadPlayersByIDs in context_activity.go) — one query per call
	// site instead of one GetPlayer per player.
	GetClubSkaterStatsBySeason(ctx context.Context, arg sqlcdb.GetClubSkaterStatsBySeasonParams) ([]sqlcdb.GetClubSkaterStatsBySeasonRow, error)
	GetClubGoalieStatsBySeason(ctx context.Context, arg sqlcdb.GetClubGoalieStatsBySeasonParams) ([]sqlcdb.GetClubGoalieStatsBySeasonRow, error)
	GetPlayersByIDs(ctx context.Context, ids []int64) ([]sqlcdb.Player, error)

	// Turn telemetry — every LLM-driven activity writes a sim_agent_turns
	// header plus the per-round, per-tool-call, and per-message children.
	// DeleteSimAgentTurnIdempotent runs first to clear any prior row at
	// the same (pool, agent, phase, sim_date) coordinate so a Temporal
	// retry can rewrite the row instead of colliding with the partial
	// unique indexes. See worker/simulation/telemetry.go for the call site.
	DeleteSimAgentTurnIdempotent(ctx context.Context, arg sqlcdb.DeleteSimAgentTurnIdempotentParams) error
	InsertSimAgentTurn(ctx context.Context, arg sqlcdb.InsertSimAgentTurnParams) (int32, error)
	InsertSimAgentTurnRound(ctx context.Context, arg []sqlcdb.InsertSimAgentTurnRoundParams) (int64, error)
	InsertSimAgentToolCall(ctx context.Context, arg []sqlcdb.InsertSimAgentToolCallParams) (int64, error)
	InsertSimAgentTurnMessage(ctx context.Context, arg []sqlcdb.InsertSimAgentTurnMessageParams) (int64, error)
	GetSimPoolRecordFullMessages(ctx context.Context, id int32) (bool, error)

	// BuildManageRosterContext reads — listings the daily prompt
	// builder needs that aren't already covered above.
	GetSimAgent(ctx context.Context, id int32) (sqlcdb.SimAgent, error)
	ListSimRosterByAgent(ctx context.Context, arg sqlcdb.ListSimRosterByAgentParams) ([]sqlcdb.SimRoster, error)
	GetSimStandingsLatestDate(ctx context.Context, poolID int32) (pgtype.Date, error)
	ListSimStandingsByDate(ctx context.Context, arg sqlcdb.ListSimStandingsByDateParams) ([]sqlcdb.SimStanding, error)
	ListSimPlayersOnWaivers(ctx context.Context, arg sqlcdb.ListSimPlayersOnWaiversParams) ([]sqlcdb.ListSimPlayersOnWaiversRow, error)
	// ListSimWaiverClaimsPendingByAgent feeds the daily working state so a
	// duplicate pending claim by the same agent is rejected as a clean action
	// error instead of violating the pending-one-per-agent-player unique index.
	ListSimWaiverClaimsPendingByAgent(ctx context.Context, arg sqlcdb.ListSimWaiverClaimsPendingByAgentParams) ([]sqlcdb.SimWaiverClaim, error)
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
func loadPlayersByIDs(ctx context.Context, q SimQueries, ids []int64) (map[int64]sqlcdb.Player, error) {
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

// ============================================================================
// BuildFreeAgentPoolActivity
//
// Runs ONCE per (pool, sim_date), result shared across every agent's
// ManageRosterActivity for that day. The day loop in Phase 3.2 makes
// this call before fanning out to per-agent activities — collapsing
// what would be N agent-side queries into 1 day-side query.
//
// PLAN.md > "Free Agent Pool" specifies the SQL contract; the
// returned []int64 is the player_id list, NOT a richer struct. The
// activity that NEEDS richer state (positions, recent stats) joins
// against players + recent_skater_stats / recent_goalie_stats on its
// own — this activity's job is just "who's eligible for add_player".
// ============================================================================

// BuildFreeAgentPoolInput is the per-call payload. Pool/Season/SimDate
// come from the workflow's day-loop iterator; WaiverDays is part of
// PoolConfig and lives in sim_pools.config (loaded by the workflow on
// pool creation, then carried forward in ContinueAsNew state).
type BuildFreeAgentPoolInput struct {
	PoolID     int32       `json:"pool_id"`
	Season     int32       `json:"season"`
	SimDate    pgtype.Date `json:"sim_date"`
	WaiverDays int32       `json:"waiver_days"`
}

// BuildFreeAgentPoolResult holds the FA pool for one (pool, sim_date)
// pair. Phase 3.2 passes it as input to each agent's
// ManageRosterActivity.
type BuildFreeAgentPoolResult struct {
	PlayerIDs []int64 `json:"player_ids"`
}

// BuildFreeAgentPool returns the list of player_ids eligible for an
// add_player call on this date. Read-only — no idempotency pre-flight
// (rerunning is free), no cost-cap pre-flight (no LLM call).
//
// The query joins game_skater_stats / game_goalie_stats with games to
// find every player who has appeared in a regular-season game up to
// sim_date, EXCEPT players currently on any pool roster, EXCEPT
// players with a pending waiver claim, EXCEPT players dropped within
// the last waiver_days. See PLAN.md > "Free Agent Pool" > "Query
// shape" for the full predicate.
func (a *Activities) BuildFreeAgentPool(ctx context.Context, in BuildFreeAgentPoolInput) (BuildFreeAgentPoolResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("BuildFreeAgentPool",
		"pool_id", in.PoolID,
		"season", in.Season,
		"sim_date", in.SimDate.Time,
		"waiver_days", in.WaiverDays,
	)

	rows, err := a.Queries.ListSimFreeAgentCandidates(ctx, sqlcdb.ListSimFreeAgentCandidatesParams{
		Season:   in.Season,
		GameDate: in.SimDate,
		PoolID:   in.PoolID,
		// sqlc named the fourth param "Column4" because the SQL
		// references $4 only inside an interval expression rather
		// than as a top-level value with a column tag. The semantic
		// is "waiver_days days back".
		Column4: in.WaiverDays,
	})
	if err != nil {
		return BuildFreeAgentPoolResult{}, fmt.Errorf("simulation: list free agent candidates: %w", err)
	}

	logger.Debug("BuildFreeAgentPool result", "count", len(rows))
	return BuildFreeAgentPoolResult{PlayerIDs: rows}, nil
}

// ============================================================================
// UpdateStandingsActivity
//
// Wraps the pure-function scoring engine (RankAllCategories) with two
// DB calls: read all current sim_agent_totals rows for the pool, write
// per-(agent, category) rows back to sim_standings via UpsertSimStanding.
// PLAN.md > "Day Loop" calls this after CollectDayStatsActivity so
// the standings reflect the day's just-completed games.
//
// Idempotent under retry: UpsertSimStanding uses ON CONFLICT DO UPDATE,
// and the rankings are a deterministic function of the totals (totals
// snapshot is the input, ranking is a pure function). Partial-failure
// recovery: a retry overwrites whatever the previous run wrote.
//
// No transaction wrapper here. The standings table is read-mostly
// after the day completes, and the upserts are commutative across
// (agent, category) keys. If we ever need standings to flip
// atomically (e.g., a UI that polls during the loop and shouldn't see
// half-written state), the activity would need to wrap in a tx — but
// that's a later concern.
// ============================================================================

// UpdateStandingsInput is the per-call payload. The workflow passes
// pool_id + the day whose standings we're computing. The standings
// reflect cumulative-through-this-date totals (sim_agent_totals is
// the source of truth and is updated by CollectDayStatsActivity
// before this runs).
//
// AgentIDs is the full set of agents in the pool. It ensures every
// agent appears in the ranking even when they have no totals rows yet
// (e.g. their goalies haven't played) — without this, the pool size
// used for point scaling shrinks to just the agents that have played,
// and cross-day totals become incomparable.
type UpdateStandingsInput struct {
	PoolID   int32       `json:"pool_id"`
	SimDate  pgtype.Date `json:"sim_date"`
	AgentIDs []int32     `json:"agent_ids"`
}

// UpdateStandingsResult is mostly diagnostic. StandingsWritten counts
// upsert calls (one per agent per category). For an N-team pool with
// the 9 Crapettes 2025 categories, expect 9*N rows per day.
type UpdateStandingsResult struct {
	StandingsWritten int `json:"standings_written"`
}

// UpdateStandings ranks every category from the current totals and
// upserts the resulting (value, roto_points) pairs into sim_standings.
//
// Algorithm:
//  1. Load every sim_agent_totals row for the pool (one row per
//     agent per category).
//  2. Group by category to build []CategoryStats, padding zero-valued
//     rows for any agent in AgentIDs that has no totals yet. This
//     ensures the pool size used for point scaling is always the full
//     agent count, not just the agents whose players have scored.
//  3. Run the scoring engine (RankAllCategories — handles GAA's
//     zero-TOI worst-rank rule and tied-share fractional points).
//  4. Upsert each (agent, category, value, roto_points) row.
//
// Empty input (no totals — day 1 of the sim, before any games have
// been played and before CollectDayStatsActivity has run) with no
// AgentIDs is a valid no-op; returns 0 standings written without error.
func (a *Activities) UpdateStandings(ctx context.Context, in UpdateStandingsInput) (UpdateStandingsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("UpdateStandings", "pool_id", in.PoolID, "sim_date", in.SimDate.Time)

	totals, err := a.Queries.ListSimAgentTotalsByPool(ctx, in.PoolID)
	if err != nil {
		return UpdateStandingsResult{}, fmt.Errorf("simulation: list agent totals: %w", err)
	}
	if len(totals) == 0 && len(in.AgentIDs) == 0 {
		logger.Debug("UpdateStandings: no totals to rank — empty pool or pre-first-game")
		return UpdateStandingsResult{}, nil
	}

	cats, err := groupTotalsByCategory(totals, in.AgentIDs)
	if err != nil {
		return UpdateStandingsResult{}, fmt.Errorf("simulation: group totals: %w", err)
	}
	if len(cats) == 0 {
		logger.Debug("UpdateStandings: no categories to rank")
		return UpdateStandingsResult{}, nil
	}
	rankings := RankAllCategories(cats)

	written := 0
	for cat, rows := range rankings {
		for _, r := range rows {
			value, err := numericFromFloat(r.Value)
			if err != nil {
				return UpdateStandingsResult{StandingsWritten: written}, fmt.Errorf("simulation: encode value: %w", err)
			}
			rotoPts, err := numericFromFloat(r.RotoPoints)
			if err != nil {
				return UpdateStandingsResult{StandingsWritten: written}, fmt.Errorf("simulation: encode roto_points: %w", err)
			}
			err = a.Queries.UpsertSimStanding(ctx, sqlcdb.UpsertSimStandingParams{
				PoolID:     in.PoolID,
				Date:       in.SimDate,
				AgentID:    int32(r.AgentID),
				Category:   string(cat),
				Value:      value,
				RotoPoints: rotoPts,
			})
			if err != nil {
				return UpdateStandingsResult{StandingsWritten: written},
					fmt.Errorf("simulation: upsert standing (agent=%d, cat=%s): %w", r.AgentID, cat, err)
			}
			written++
		}
	}

	logger.Debug("UpdateStandings result", "rows_written", written, "categories", len(rankings))
	return UpdateStandingsResult{StandingsWritten: written}, nil
}

// groupTotalsByCategory turns flat sqlc rows into the []CategoryStats
// shape RankAllCategories expects. The input is one row per
// (agent, category); the output is one CategoryStats per category
// containing the per-agent stat rows.
//
// agentIDs is the full pool agent list. Any agent that has no row in
// totals for a given category gets a zero-valued AgentCategoryStat so
// that the ranking pool size is always len(agentIDs). This prevents
// points from compressing when, e.g., an agent's goalies haven't
// played yet — for lower-is-better categories a zero stat is correctly
// ranked best (or tied best), and for higher-is-better it ties last.
//
// Conversion notes:
//   - Value is pgtype.Numeric in the DB → float64 in the scoring
//     engine. Counting categories store integers so the round-trip
//     is exact; GAA isn't stored in sim_agent_totals.value (the
//     query computes it on read), but the goalie components are.
//   - GoalieGA / GoalieTOISeconds are pgtype.Int4 → int. NULL
//     becomes 0 — fine for non-GAA categories which ignore those
//     fields, and consistent with the "zero-TOI worst-rank" rule
//     for the GAA category.
func groupTotalsByCategory(totals []sqlcdb.SimAgentTotal, agentIDs []int32) ([]CategoryStats, error) {
	byCat := make(map[Category][]AgentCategoryStat)
	for _, t := range totals {
		val, err := NumericToFloat(t.Value)
		if err != nil {
			return nil, fmt.Errorf("decode totals value (agent=%d, cat=%s): %w", t.AgentID, t.Category, err)
		}
		stat := AgentCategoryStat{
			AgentID:          int64(t.AgentID),
			Value:            val,
			GoalieGA:         int(t.GoalieGA.Int32), // .Int32 is 0 when !Valid
			GoalieTOISeconds: int(t.GoalieTOISeconds.Int32),
		}
		cat := Category(t.Category)
		byCat[cat] = append(byCat[cat], stat)
	}

	// Pad each category with zero-valued rows for agents that have no
	// totals row yet. Without this, the ranking pool shrinks to only
	// the agents that appear in sim_agent_totals and cross-day point
	// totals become incomparable (a 3-agent ranking one day and a
	// 5-agent ranking the next inflate early-game leaders).
	for cat, stats := range byCat {
		present := make(map[int64]struct{}, len(stats))
		for _, s := range stats {
			present[s.AgentID] = struct{}{}
		}
		for _, id := range agentIDs {
			if _, ok := present[int64(id)]; !ok {
				byCat[cat] = append(byCat[cat], AgentCategoryStat{AgentID: int64(id)})
			}
		}
	}

	out := make([]CategoryStats, 0, len(byCat))
	for cat, stats := range byCat {
		out = append(out, CategoryStats{Category: cat, Stats: stats})
	}
	return out, nil
}

// numericFromFloat converts a float64 (from the scoring engine) to a
// pgtype.Numeric (for the DB write). Routed through string parsing
// so the pgtype handles the big.Int/exponent encoding correctly. Six
// digits of fractional precision is more than enough for roto points
// (typically integer or .5).
func numericFromFloat(f float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(strconv.FormatFloat(f, 'f', 6, 64)); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

// NumericToFloat converts a pgtype.Numeric (DB read) to float64
// (scoring engine). NULL becomes 0 — sim_agent_totals.value is
// NOT NULL by schema, but the helper handles invalid Numerics
// defensively so a future loosening of the column nullability can't
// silently NaN the scoring engine.
func NumericToFloat(n pgtype.Numeric) (float64, error) {
	if !n.Valid {
		return 0, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return 0, err
	}
	return f.Float64, nil
}
