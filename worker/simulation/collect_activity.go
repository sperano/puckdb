package simulation

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/sqlcdb"
	"go.temporal.io/sdk/activity"
)

// ============================================================================
// CollectDayStatsActivity — score a day's NHL games against every
// agent's active roster.
//
// Lifecycle:
//
//	1. ListSimDayGames(season, simDate). No FINAL regular-season games
//	   of the season on that date → return Skipped result; the
//	   workflow's day loop is wired to no-op when there's no game data.
//	2. For each game on the date, fetch every skater + goalie stat row.
//	   These are pool-independent NHL facts; we cache them by player_id
//	   so each agent's per-roster scan is a map lookup.
//	3. Atomic commit (Transactor.InTx): for every agent, list active
//	   roster (BN/IR excluded), emit one
//	   sim_agent_daily_player_stats row per (player, category) where
//	   the player has a stat that day, then aggregate up via the
//	   sqlc-managed AggregateSimAgentDailyStats helper. Finally
//	   recompute pool-wide totals (counting + GAA components).
//
// Idempotency: every write uses ON CONFLICT DO UPDATE. Reruns of the
// same (pool, sim_date) overwrite identically because the row set is
// deterministic — the day's roster is locked before scoring runs
// (PLAN.md Day Loop step 4), so attempt N+1 produces the same
// (player, category) keys as attempt N.
//
// PLAN.md > "CollectDayStatsActivity" and > "Goalie decision
// semantics" are the canonical specs.
// ============================================================================

// CollectDayStatsInput is the per-day, per-pool payload. AgentIDs is
// workflow-supplied — typically every agent in the pool. The activity
// doesn't query sim_agents itself so the workflow stays the source of
// truth for "who participates this day" (allows future per-agent
// pause / opt-out without changing this activity).
type CollectDayStatsInput struct {
	PoolID   int32       `json:"pool_id"`
	Season   int32       `json:"season"`
	SimDate  pgtype.Date `json:"sim_date"`
	AgentIDs []int32     `json:"agent_ids"`
}

// CollectDayStatsResult is mostly diagnostic. The workflow uses
// Skipped to decide whether to chain UpdateStandingsActivity (skipped
// no-game days don't need a standings recompute either).
type CollectDayStatsResult struct {
	Skipped           bool   `json:"skipped"`
	SkipReason        string `json:"skip_reason,omitempty"`
	GamesScored       int    `json:"games_scored"`
	AgentsProcessed   int    `json:"agents_processed"`
	PlayerRowsWritten int    `json:"player_rows_written"`
}

// SkipReasonNoGames is the SkipReason when no FINAL regular-season
// games of the input season landed on this date — typical for the
// All-Star break and pre/post-season days that fall in the calendar
// range.
const SkipReasonNoGames = "no_games"

// CollectDayStats is the Temporal-activity entry point.
//
// Returns an error only on DB failure; "no games today" is a clean
// success with Skipped=true.
func (a *Activities) CollectDayStats(ctx context.Context, in CollectDayStatsInput) (CollectDayStatsResult, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("CollectDayStats start",
		"pool_id", in.PoolID,
		"season", in.Season,
		"sim_date", in.SimDate.Time,
		"agent_count", len(in.AgentIDs),
	)

	games, err := a.Queries.ListSimDayGames(ctx, sqlcdb.ListSimDayGamesParams{
		Season:   in.Season,
		GameDate: in.SimDate,
	})
	if err != nil {
		return CollectDayStatsResult{}, fmt.Errorf("simulation: list day games: %w", err)
	}
	if len(games) == 0 {
		logger.Debug("CollectDayStats skipped — no games for season on date")
		return CollectDayStatsResult{Skipped: true, SkipReason: SkipReasonNoGames}, nil
	}

	skaterByPlayer, goalieByPlayer, err := a.fetchDayStats(ctx, games)
	if err != nil {
		return CollectDayStatsResult{}, err
	}

	playerRowsWritten := 0
	err = a.Tx.InTx(ctx, func(q SimQueries) error {
		for _, agentID := range in.AgentIDs {
			n, err := writeAgentDayStats(ctx, q, in, agentID, skaterByPlayer, goalieByPlayer)
			if err != nil {
				return err
			}
			playerRowsWritten += n
		}
		// Pool-wide totals recompute (counting + GAA components). Both
		// queries are SUM over the up-to-date sim_agent_daily_stats so
		// they MUST run after every agent's aggregate has landed.
		if err := q.RecomputeSimAgentTotalsCounting(ctx, in.PoolID); err != nil {
			return fmt.Errorf("recompute totals (counting): %w", err)
		}
		if err := q.RecomputeSimAgentTotalsGAA(ctx, in.PoolID); err != nil {
			return fmt.Errorf("recompute totals (GAA): %w", err)
		}
		return nil
	})
	if err != nil {
		return CollectDayStatsResult{}, err
	}

	logger.Debug("CollectDayStats complete",
		"games_scored", len(games),
		"agents_processed", len(in.AgentIDs),
		"player_rows", playerRowsWritten,
	)
	return CollectDayStatsResult{
		GamesScored:       len(games),
		AgentsProcessed:   len(in.AgentIDs),
		PlayerRowsWritten: playerRowsWritten,
	}, nil
}

// fetchDayStats issues one (skater + goalie) batch query per game on
// the date, then keys the results by player_id. Reading happens
// outside the activity's write transaction — no need to hold the tx
// open across N round-trips.
//
// Multi-game days for one player (theoretical — NHL doesn't schedule
// these) get accumulated at write time; the per-player map keys
// remain unique while the slice values capture each game's row.
func (a *Activities) fetchDayStats(ctx context.Context, games []sqlcdb.ListSimDayGamesRow) (
	skaters map[int64][]sqlcdb.GetGameSkaterStatsByGameRow,
	goalies map[int64][]sqlcdb.GetGameGoalieStatsByGameRow,
	err error,
) {
	skaters = make(map[int64][]sqlcdb.GetGameSkaterStatsByGameRow, len(games)*40)
	goalies = make(map[int64][]sqlcdb.GetGameGoalieStatsByGameRow, len(games)*4)

	for _, g := range games {
		srows, err := a.Queries.GetGameSkaterStatsByGame(ctx, g.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("get skater stats for game %d: %w", g.ID, err)
		}
		for _, r := range srows {
			skaters[r.PlayerID] = append(skaters[r.PlayerID], r)
		}
		grows, err := a.Queries.GetGameGoalieStatsByGame(ctx, g.ID)
		if err != nil {
			return nil, nil, fmt.Errorf("get goalie stats for game %d: %w", g.ID, err)
		}
		for _, r := range grows {
			goalies[r.PlayerID] = append(goalies[r.PlayerID], r)
		}
	}
	return skaters, goalies, nil
}

// writeAgentDayStats walks one agent's active roster and emits the
// per-player rows for everyone who appeared in the day's games.
// Returns the count of rows written (diagnostic for the result).
//
// "Active" is the slot filter that excludes BN/IR (PLAN.md > "Slot
// rules" — bench / IR players don't score even if they played).
func writeAgentDayStats(
	ctx context.Context,
	q SimQueries,
	in CollectDayStatsInput,
	agentID int32,
	skaterByPlayer map[int64][]sqlcdb.GetGameSkaterStatsByGameRow,
	goalieByPlayer map[int64][]sqlcdb.GetGameGoalieStatsByGameRow,
) (int, error) {
	roster, err := q.ListSimActiveRosterByAgent(ctx, sqlcdb.ListSimActiveRosterByAgentParams{
		PoolID: in.PoolID, AgentID: agentID,
	})
	if err != nil {
		return 0, fmt.Errorf("list active roster (agent %d): %w", agentID, err)
	}

	rows := 0
	for _, slot := range roster {
		// Skater path.
		if srows, ok := skaterByPlayer[slot.PlayerID]; ok {
			contribs := summarizeSkater(srows)
			for _, c := range contribs {
				if err := writePlayerCategoryRow(ctx, q, in, agentID, slot.PlayerID, c); err != nil {
					return rows, err
				}
				rows++
			}
		}
		// Goalie path. Note: a player can appear in BOTH maps only if
		// the data is corrupt (a player is either a skater or a goalie
		// in any given game). We branch on which map matched.
		if grows, ok := goalieByPlayer[slot.PlayerID]; ok {
			contribs := summarizeGoalie(grows)
			for _, c := range contribs {
				if err := writePlayerCategoryRow(ctx, q, in, agentID, slot.PlayerID, c); err != nil {
					return rows, err
				}
				rows++
			}
		}
	}

	if rows > 0 {
		if err := q.AggregateSimAgentDailyStats(ctx, sqlcdb.AggregateSimAgentDailyStatsParams{
			PoolID: in.PoolID, AgentID: agentID, Date: in.SimDate,
		}); err != nil {
			return rows, fmt.Errorf("aggregate daily stats (agent %d): %w", agentID, err)
		}
	}
	return rows, nil
}

// categoryContribution carries one player's contribution to one
// category for the day. value is the counting-category number;
// goalieGA + goalieTOI are populated only for the GAA category and
// stay zero elsewhere (mirroring the schema's Int4 NULLABLE shape).
type categoryContribution struct {
	category  Category
	value     float64
	goalieGA  int
	goalieTOI int
}

// summarizeSkater accumulates a skater's per-game stats into one
// contribution per category. PLAN.md > "Categories" lists the six
// skater categories: G, A, +/-, PIM, PPP, SOG. A player who plays
// multiple games on a date (theoretical in V1) gets their values
// summed before upserting.
func summarizeSkater(rows []sqlcdb.GetGameSkaterStatsByGameRow) []categoryContribution {
	var g, a, pm, pim, ppp, sog int
	for _, r := range rows {
		g += int(r.Goals)
		a += int(r.Assists)
		pm += int(r.PlusMinus)
		pim += int(r.PenaltyMinutes)
		ppp += int(r.PowerPlayPoints)
		sog += int(r.ShotsOnGoal)
	}
	return []categoryContribution{
		{category: CategoryG, value: float64(g)},
		{category: CategoryA, value: float64(a)},
		{category: CategoryPM, value: float64(pm)},
		{category: CategoryPIM, value: float64(pim)},
		{category: CategoryPPP, value: float64(ppp)},
		{category: CategorySOG, value: float64(sog)},
	}
}

// summarizeGoalie accumulates a goalie's per-game stats into per-
// category contributions. PLAN.md > "Categories" lists three goalie
// categories:
//
//   - W: count = 1 if the row's decision is exactly "W". L / OTL / T /
//     NULL all contribute 0 (PLAN.md > "Goalie decision semantics").
//   - GA: SUM of goals_against across ALL rows including
//     NULL-decision rows (a pulled starter still let in goals).
//   - GAA: stored as components (goalie_ga + goalie_toi_seconds);
//     value=0 since per-day GAA isn't itself a score (the totals
//     query computes pool-wide GAA from accumulated components).
func summarizeGoalie(rows []sqlcdb.GetGameGoalieStatsByGameRow) []categoryContribution {
	var w, ga, toi int
	for _, r := range rows {
		if r.Decision.Valid && r.Decision.GoalieDecision == sqlcdb.GoalieDecisionW {
			w++
		}
		ga += int(r.GoalsAgainst)
		toi += int(r.TOISeconds)
	}
	return []categoryContribution{
		{category: CategoryW, value: float64(w)},
		{category: CategoryGA, value: float64(ga)},
		{category: CategoryGAA, value: 0, goalieGA: ga, goalieTOI: toi},
	}
}

// writePlayerCategoryRow upserts one (pool, agent, date, player,
// category) row. ON CONFLICT DO UPDATE in the SQL ensures retries
// overwrite cleanly (PLAN.md > "Idempotency > Stat activities").
func writePlayerCategoryRow(
	ctx context.Context,
	q SimQueries,
	in CollectDayStatsInput,
	agentID int32,
	playerID int64,
	c categoryContribution,
) error {
	value, err := numericFromFloat(c.value)
	if err != nil {
		return fmt.Errorf("encode value for player %d category %s: %w", playerID, c.category, err)
	}
	params := sqlcdb.UpsertSimAgentDailyPlayerStatParams{
		PoolID:   in.PoolID,
		AgentID:  agentID,
		Date:     in.SimDate,
		PlayerID: playerID,
		Category: string(c.category),
		Value:    value,
	}
	if c.goalieGA != 0 || c.goalieTOI != 0 {
		// Either both or neither — but the AggregateSimAgentDailyStats
		// SQL uses NULLIF(SUM(COALESCE(...))) so a 0 component is
		// indistinguishable from NULL at the rollup. Leave as-is for
		// non-GAA rows (schema NULL via !Valid) so the per-player
		// table truthfully says "no goalie components for this row".
		params.GoalieGA = pgtype.Int4{Int32: int32(c.goalieGA), Valid: true}
		params.GoalieTOISeconds = pgtype.Int4{Int32: int32(c.goalieTOI), Valid: true}
	}
	if err := q.UpsertSimAgentDailyPlayerStat(ctx, params); err != nil {
		return fmt.Errorf("upsert player %d category %s: %w", playerID, c.category, err)
	}
	return nil
}
