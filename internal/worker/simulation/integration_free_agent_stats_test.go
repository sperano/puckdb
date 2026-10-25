//go:build integration

package simulation

// TestIntegrationFreeAgentStatsQueries validates the two free-agent
// ranking aggregation queries (ListSimFreeAgentSkaterStats /
// ListSimFreeAgentGoalieStats) against real PostgreSQL. SQL-parity
// coverage for the SIM-U3 wire-through: the queries must sum season-
// to-date and last-7-day counting categories over a list of player
// ids, and must exclude games on sim_date itself (the daily turn runs
// before CollectDayStats scores today, so today's boxscores must not
// leak into the agent's decision).
//
// It seeds a tiny season by hand (no Temporal/Redis) and asserts the
// exact aggregate per player, including a sentinel game on sim_date
// that must be excluded.

import (
	"context"
	"testing"

	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	faStatsSeason int32 = 20282029
	faStatsSimDay       = "2025-02-08"

	faSkaterA int64 = 9801 // C, team 4801
	faSkaterB int64 = 9802 // LW, team 4802
	faGoalie  int64 = 9803 // G, team 4801

	faGameOld    int64 = 9901 // 2025-01-20 — outside last-7 window
	faGameRecent int64 = 9902 // 2025-02-01 — first day of last-7 window
	faGameLatest int64 = 9903 // 2025-02-07 — last day before sim_date
	faGameToday  int64 = 9904 // 2025-02-08 — sim_date, must be excluded
)

func TestIntegrationFreeAgentStatsQueries(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	ctx := context.Background()
	q := sqlcdb.New(pgPool)

	seedSeasonWithBounds(t, ctx, pgPool, faStatsSeason, "2024-10-01", "2025-06-30")
	seedTeam(t, ctx, pgPool, faStatsSeason, 4801, "T1")
	seedTeam(t, ctx, pgPool, faStatsSeason, 4802, "T2")
	seedPlayer(t, ctx, pgPool, faSkaterA, sqlcdb.PlayerPositionC)
	seedPlayer(t, ctx, pgPool, faSkaterB, sqlcdb.PlayerPositionLW)
	seedPlayer(t, ctx, pgPool, faGoalie, sqlcdb.PlayerPositionG)

	seedGame(t, ctx, pgPool, faGameOld, faStatsSeason, "2025-01-20", 4801, 4802)
	seedGame(t, ctx, pgPool, faGameRecent, faStatsSeason, "2025-02-01", 4801, 4802)
	seedGame(t, ctx, pgPool, faGameLatest, faStatsSeason, "2025-02-07", 4801, 4802)
	seedGame(t, ctx, pgPool, faGameToday, faStatsSeason, "2025-02-08", 4801, 4802)

	// Skater A: 3 games before sim_date + 1 sentinel game ON sim_date.
	seedGameSkaterStatsLine(t, ctx, pgPool, faGameOld, faSkaterA, 4801, sqlcdb.PlayerPositionC, skaterLine{Goals: 1, Assists: 1, SOG: 1})
	seedGameSkaterStatsLine(t, ctx, pgPool, faGameRecent, faSkaterA, 4801, sqlcdb.PlayerPositionC, skaterLine{Goals: 2, Assists: 0, PPP: 1, SOG: 2})
	seedGameSkaterStatsLine(t, ctx, pgPool, faGameLatest, faSkaterA, 4801, sqlcdb.PlayerPositionC, skaterLine{Goals: 1, Assists: 1, SOG: 4})
	seedGameSkaterStatsLine(t, ctx, pgPool, faGameToday, faSkaterA, 4801, sqlcdb.PlayerPositionC, skaterLine{Goals: 100, Assists: 100, SOG: 100})

	// Skater B: one recent game.
	seedGameSkaterStatsLine(t, ctx, pgPool, faGameRecent, faSkaterB, 4802, sqlcdb.PlayerPositionLW, skaterLine{Goals: 3, Assists: 1, SOG: 1})

	// Goalie: one game before sim_date (win) + one sentinel game ON sim_date.
	seedGameGoalieStats(t, ctx, pgPool, faGameLatest, faGoalie, 4801, goalieLine{Win: true, GoalsAgainst: 2, Saves: 30, TOISeconds: 3600})
	seedGameGoalieStats(t, ctx, pgPool, faGameToday, faGoalie, 4801, goalieLine{Win: true, GoalsAgainst: 10, Saves: 30, TOISeconds: 3600})

	simDate := mustPgDate(faStatsSimDay)

	skaterRows, err := q.ListSimFreeAgentSkaterStats(ctx, sqlcdb.ListSimFreeAgentSkaterStatsParams{
		PlayerIds: []int64{faSkaterA, faSkaterB},
		Season:    faStatsSeason,
		GameDate:  simDate,
	})
	require.NoError(t, err)

	byPlayer := map[int64]sqlcdb.ListSimFreeAgentSkaterStatsRow{}
	for _, r := range skaterRows {
		byPlayer[r.PlayerID] = r
	}

	require.Contains(t, byPlayer, faSkaterA)
	a := byPlayer[faSkaterA]
	assert.Equal(t, int32(4), a.SeasonGoals, "season goals exclude the sim_date sentinel (1+2+1)")
	assert.Equal(t, int32(2), a.SeasonAssists, "season assists exclude the sim_date sentinel (1+0+1)")
	assert.Equal(t, int32(1), a.SeasonPpp, "season PPP = 0+1+0")
	assert.Equal(t, int32(7), a.SeasonSog, "season SOG = 1+2+4")
	assert.Equal(t, int32(3), a.RecentGoals, "recent goals exclude both the old game and the sim_date sentinel (2+1)")
	assert.Equal(t, int32(1), a.RecentAssists, "recent assists = 0+1")
	assert.Equal(t, int32(1), a.RecentPpp, "recent PPP = 1+0")
	assert.Equal(t, int32(6), a.RecentSog, "recent SOG = 2+4")

	require.Contains(t, byPlayer, faSkaterB)
	b := byPlayer[faSkaterB]
	assert.Equal(t, int32(3), b.SeasonGoals)
	assert.Equal(t, int32(1), b.SeasonAssists)
	assert.Equal(t, int32(3), b.RecentGoals)
	assert.Equal(t, int32(1), b.RecentAssists)

	goalieRows, err := q.ListSimFreeAgentGoalieStats(ctx, sqlcdb.ListSimFreeAgentGoalieStatsParams{
		PlayerIds: []int64{faGoalie},
		Season:    faStatsSeason,
		GameDate:  simDate,
	})
	require.NoError(t, err)

	require.Len(t, goalieRows, 1)
	g := goalieRows[0]
	assert.Equal(t, int32(1), g.SeasonWins, "season wins exclude the sim_date sentinel")
	assert.Equal(t, int32(2), g.SeasonGa, "season GA exclude the sim_date sentinel")
	assert.Equal(t, int32(3600), g.SeasonToi)
	assert.Equal(t, int32(1), g.RecentWins)
	assert.Equal(t, int32(2), g.RecentGa)
	assert.Equal(t, int32(3600), g.RecentToi)
}
