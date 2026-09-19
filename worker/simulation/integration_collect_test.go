//go:build integration

package simulation

// DB-backed CollectDayStats season isolation: games from another
// season on the same calendar date must not reach pool scoring. The
// stub-query unit tests can only check the params passed; only a real
// query proves the season predicate filters rows.

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// Fixture constants. Seasons, dates and IDs are disjoint from every
// other integration fixture in this package.
const (
	collectSeasonPool    int32 = 20302031
	collectSeasonOther   int32 = 20312032
	collectSharedDate          = "2030-11-15" // both seasons have a game
	collectOtherOnlyDate       = "2030-11-16" // only the other season does
	collectTeamHome      int64 = 9300
	collectTeamAway      int64 = 9301
	collectPlayerID      int64 = 9310
	collectGamePool      int64 = 93001 // pool season, shared date
	collectGameOther     int64 = 93002 // other season, shared date
	collectGameOtherOnly int64 = 93003 // other season, other-only date
	collectPoolGoals           = 1
	collectOtherGoals          = 4
)

// seedCollectSeasonFixture seeds one skater who scores in a pool-season
// game and in other-season games, and returns a pool on the pool
// season with that skater in agent 1's active C slot.
func seedCollectSeasonFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) (poolID, agentID int32) {
	t.Helper()
	for _, season := range []int32{collectSeasonPool, collectSeasonOther} {
		seedSeasonWithBounds(t, ctx, pool, season, "2030-10-01", "2031-04-15")
		seedTeam(t, ctx, pool, season, collectTeamHome, "CSH")
		seedTeam(t, ctx, pool, season, collectTeamAway, "CSA")
	}
	seedPlayer(t, ctx, pool, collectPlayerID, sqlcdb.PlayerPositionC)

	games := []struct {
		id     int64
		season int32
		date   string
		goals  int
	}{
		{collectGamePool, collectSeasonPool, collectSharedDate, collectPoolGoals},
		{collectGameOther, collectSeasonOther, collectSharedDate, collectOtherGoals},
		{collectGameOtherOnly, collectSeasonOther, collectOtherOnlyDate, collectOtherGoals},
	}
	for _, g := range games {
		seedGame(t, ctx, pool, g.id, g.season, g.date, collectTeamHome, collectTeamAway)
		seedGameSkaterStats(t, ctx, pool, g.id, collectPlayerID, collectTeamHome, g.goals, 0)
	}

	poolID, agentID, _ = createIntegrationPool(t, ctx, pool, collectSeasonPool, 1,
		t.Name(), collectSharedDate, collectOtherOnlyDate)
	require.NoError(t, sqlcdb.New(pool).InsertSimRoster(ctx, sqlcdb.InsertSimRosterParams{
		PoolID:      poolID,
		AgentID:     agentID,
		PlayerID:    collectPlayerID,
		Slot:        string(SlotC),
		AcquiredAt:  mustPgDate(collectSharedDate),
		AcquiredVia: string(AcquiredViaDraft),
	}))
	return poolID, agentID
}

// runCollectDayStats executes the real activity against the DB.
func runCollectDayStats(t *testing.T, pool *pgxpool.Pool, in CollectDayStatsInput) CollectDayStatsResult {
	t.Helper()
	var ts testsuite.WorkflowTestSuite
	env := ts.NewTestActivityEnvironment()
	acts := &Activities{Queries: sqlcdb.New(pool), Tx: NewPgxTransactor(pool)}
	env.RegisterActivity(acts.CollectDayStats)

	future, err := env.ExecuteActivity(acts.CollectDayStats, in)
	require.NoError(t, err)
	var got CollectDayStatsResult
	require.NoError(t, future.Get(&got))
	return got
}

// queryGoalsValue returns the G value in table for the pool/agent,
// or 0 when no row exists. table is a trusted test constant.
func queryGoalsValue(t *testing.T, ctx context.Context, pool *pgxpool.Pool, table string, poolID, agentID int32) float64 {
	t.Helper()
	var v float64
	err := pool.QueryRow(ctx,
		`SELECT COALESCE(SUM(value), 0)::FLOAT8 FROM `+table+`
		 WHERE pool_id = $1 AND agent_id = $2 AND category = 'G'`,
		poolID, agentID).Scan(&v)
	require.NoError(t, err)
	return v
}

func TestIntegrationCollectDayStatsSeasonIsolation(t *testing.T) {
	pool := requireIntegrationEnv(t)
	ctx := context.Background()
	poolID, agentID := seedCollectSeasonFixture(t, ctx, pool)

	// Shared date: only the pool-season game is scored.
	got := runCollectDayStats(t, pool, CollectDayStatsInput{
		PoolID:   poolID,
		Season:   collectSeasonPool,
		SimDate:  mustPgDate(collectSharedDate),
		AgentIDs: []int32{agentID},
	})
	assert.False(t, got.Skipped)
	assert.Equal(t, 1, got.GamesScored, "other-season game on the same date must not be scored")

	for _, table := range []string{"sim_agent_daily_player_stats", "sim_agent_daily_stats", "sim_agent_totals"} {
		assert.Equalf(t, float64(collectPoolGoals), queryGoalsValue(t, ctx, pool, table, poolID, agentID),
			"%s G must count only the pool-season game", table)
	}

	// Other-only date: the no-games skip is evaluated within the
	// pool season, so the other season's game doesn't count.
	got = runCollectDayStats(t, pool, CollectDayStatsInput{
		PoolID:   poolID,
		Season:   collectSeasonPool,
		SimDate:  mustPgDate(collectOtherOnlyDate),
		AgentIDs: []int32{agentID},
	})
	assert.True(t, got.Skipped)
	assert.Equal(t, SkipReasonNoGames, got.SkipReason)
	assert.Equal(t, float64(collectPoolGoals), queryGoalsValue(t, ctx, pool, "sim_agent_totals", poolID, agentID),
		"skipped day must leave totals unchanged")
}
