package simulation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// BuildFreeAgentPoolActivity
//
// Runs ONCE per (pool, sim_date), result shared across every agent's
// ManageRosterActivity for that day. The day loop makes this call
// before the per-agent activities — collapsing
// what would be N agent-side queries into 1 day-side query.
//
// ListSimFreeAgentCandidates is the SQL contract (wiki
// puckdb/simulation.md > "Free agents and waivers"); the returned
// []int64 is the player_id list, NOT a richer struct. The activity that NEEDS richer state (positions, recent stats) joins
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
// pair. The day loop passes it as input to each agent's
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
// the last waiver_days. See ListSimFreeAgentCandidates in
// internal/sqlcdb/queries/sim.sql for the full predicate.
func (a *Activities) BuildFreeAgentPool(ctx context.Context, in BuildFreeAgentPoolInput) (BuildFreeAgentPoolResult, error) {
	return freeAgentActivity{q: a.Queries}.BuildFreeAgentPool(ctx, in)
}

// freeAgentQueries is BuildFreeAgentPool's only query.
type freeAgentQueries interface {
	ListSimFreeAgentCandidates(ctx context.Context, arg sqlcdb.ListSimFreeAgentCandidatesParams) ([]int64, error)
}

// freeAgentActivity runs BuildFreeAgentPool over its own query group.
type freeAgentActivity struct {
	q freeAgentQueries
}

// BuildFreeAgentPool is (*Activities).BuildFreeAgentPool's body.
func (f freeAgentActivity) BuildFreeAgentPool(ctx context.Context, in BuildFreeAgentPoolInput) (BuildFreeAgentPoolResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("BuildFreeAgentPool",
		"pool_id", in.PoolID,
		"season", in.Season,
		"sim_date", in.SimDate.Time,
		"waiver_days", in.WaiverDays,
	)

	rows, err := f.q.ListSimFreeAgentCandidates(ctx, sqlcdb.ListSimFreeAgentCandidatesParams{
		Season:     in.Season,
		GameDate:   in.SimDate,
		PoolID:     in.PoolID,
		WaiverDays: in.WaiverDays,
	})
	if err != nil {
		return BuildFreeAgentPoolResult{}, fmt.Errorf("simulation: list free agent candidates: %w", err)
	}

	logger.Debug("BuildFreeAgentPool result", "count", len(rows))
	return BuildFreeAgentPoolResult{PlayerIDs: rows}, nil
}
