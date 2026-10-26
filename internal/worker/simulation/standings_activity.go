package simulation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/pgconv"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// UpdateStandingsActivity
//
// Wraps the pure-function scoring engine (RankAllCategories) with two
// DB calls: read all current sim_agent_totals rows for the pool, write
// per-(agent, category) rows back to sim_standings via UpsertSimStanding.
// The day loop (wiki puckdb/simulation.md > "Day loop") calls this after
// CollectDayStatsActivity so the standings reflect the day's just-completed games.
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
// agent appears in each ranked category even when they have no totals
// row in it yet (e.g. their goalies haven't played) — without this,
// the pool size used for point scaling shrinks to just the agents that
// have played, and cross-day totals become incomparable.
type UpdateStandingsInput struct {
	PoolID   int32       `json:"pool_id"`
	SimDate  pgtype.Date `json:"sim_date"`
	AgentIDs []int32     `json:"agent_ids"`
}

// UpdateStandingsResult is mostly diagnostic. StandingsWritten counts
// upsert calls (one per agent per ranked category). For an N-team pool
// with the 9 Crapettes 2025 categories, expect 9*N rows per day once
// every category has a totals row; a category no agent has a row in
// yet (W, GA and GAA before any rostered goalie has played) is not
// ranked.
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
	return standingsActivity{q: a.Queries}.UpdateStandings(ctx, in)
}

// standingsQueries is UpdateStandings' read of the pool totals and its
// per-(agent, category) upsert.
type standingsQueries interface {
	ListSimAgentTotalsByPool(ctx context.Context, poolID int32) ([]sqlcdb.SimAgentTotal, error)
	UpsertSimStanding(ctx context.Context, arg sqlcdb.UpsertSimStandingParams) error
}

// standingsActivity runs UpdateStandings over its own query group.
type standingsActivity struct {
	q standingsQueries
}

// UpdateStandings is (*Activities).UpdateStandings' body.
func (s standingsActivity) UpdateStandings(ctx context.Context, in UpdateStandingsInput) (UpdateStandingsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("UpdateStandings", "pool_id", in.PoolID, "sim_date", in.SimDate.Time)

	totals, err := s.q.ListSimAgentTotalsByPool(ctx, in.PoolID)
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
			value, err := pgconv.NumericFromFloat(r.Value)
			if err != nil {
				return UpdateStandingsResult{StandingsWritten: written}, fmt.Errorf("simulation: encode value: %w", err)
			}
			rotoPts, err := pgconv.NumericFromFloat(r.RotoPoints)
			if err != nil {
				return UpdateStandingsResult{StandingsWritten: written}, fmt.Errorf("simulation: encode roto_points: %w", err)
			}
			err = s.q.UpsertSimStanding(ctx, sqlcdb.UpsertSimStandingParams{
				PoolID:     in.PoolID,
				Date:       in.SimDate,
				AgentID:    r.AgentID,
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
// totals for a category some agent has a row in gets a zero-valued
// AgentCategoryStat, so that the ranking pool size is always
// len(agentIDs). This prevents points from compressing when, e.g., an
// agent's goalies haven't played yet: the padded agent ties last in
// the higher-is-better categories and in GAA (RankGAA ranks zero
// goalie TOI worst), and ranks best (or tied best) in GA.
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
		val, err := pgconv.NumericToFloat(t.Value)
		if err != nil {
			return nil, fmt.Errorf("decode totals value (agent=%d, cat=%s): %w", t.AgentID, t.Category, err)
		}
		stat := AgentCategoryStat{
			AgentID:          t.AgentID,
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
		present := make(map[int32]struct{}, len(stats))
		for _, s := range stats {
			present[s.AgentID] = struct{}{}
		}
		for _, id := range agentIDs {
			if _, ok := present[id]; !ok {
				byCat[cat] = append(byCat[cat], AgentCategoryStat{AgentID: id})
			}
		}
	}

	out := make([]CategoryStats, 0, len(byCat))
	for cat, stats := range byCat {
		out = append(out, CategoryStats{Category: cat, Stats: stats})
	}
	return out, nil
}
