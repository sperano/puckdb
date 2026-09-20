//go:build integration

package simulation

// Integration tests for the simulation package.
//
// Build tag isolation:
//
//   //go:build integration
//
// These tests are NOT compiled by `go test ./...`. To run them:
//
//   go test -tags=integration ./worker/simulation/...
//
// Suite-level setup (TestMain, env vars, seed helpers, worker
// bootstrap) lives in integration_helpers_test.go, shared with the
// livellm build tag.
//
// What's covered here:
//
//   TestIntegrationSchemaSmoke — pins the JSONB→typed-columns
//   migration cascade. Inserts a pool + 2 agents + a roster row, reads
//   them back, asserts every typed column round-trips through pgx.
//   Catches the integration-time failures that unit tests can't see:
//   mismatched column lists in sqlc-generated Scan calls, NOT NULL
//   constraint violations, type-coercion errors at the wire boundary.

import (
	"context"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ============================================================================
// TestIntegrationSchemaSmoke — pins the JSONB→typed-columns migration.
//
// Inserts a sim_pool with every typed column populated, two
// sim_agents with the runtime-tunable columns set, a sim_rosters
// row tying back to a real player, then reads everything via the
// sqlc Get/List queries and asserts the round-trip.
//
// This is the test we'd FIRST write any time the schema changes:
// it catches column-count mismatches, NOT NULL violations, FK
// problems, and type-coercion errors at the actual wire boundary.
// Unit tests with stub queries can't see these.
// ============================================================================

func TestIntegrationSchemaSmoke(t *testing.T) {
	pool := requireIntegrationEnv(t)
	ctx := context.Background()
	q := sqlcdb.New(pool)

	const seasonID = 20242025
	seedSeason(t, ctx, pool, seasonID)

	// Insert a pool exercising every typed column. Categories TEXT[]
	// and the eight roster_* INT columns are the migration's
	// load-bearing changes; mis-projecting any of them in the sqlc
	// layer would surface here.
	capUSD, err := pgNumericFromFloat(200.0)
	require.NoError(t, err)
	pool1, err := q.InsertSimPool(ctx, sqlcdb.InsertSimPoolParams{
		Name:                 "integration_test_pool",
		Season:               seasonID,
		Status:               string(PoolStatusDraft),
		NumTeams:             2,
		WaiverDays:           2,
		DraftRounds:          1,
		MaxLLMCostUsdPerPool: capUSD,
		Categories:           []string{"G", "A", "PIM", "W", "GA"},
		RosterC:              2,
		RosterLW:             2,
		RosterRW:             2,
		RosterD:              3,
		RosterG:              2,
		RosterUtil:           1,
		RosterBN:             5,
		RosterIR:             2,
		StopAfter:            StopAfterNever.String(),
	})
	require.NoError(t, err, "InsertSimPool")
	assert.NotZero(t, pool1.ID, "auto-generated id")
	assert.True(t, pool1.WorkflowID.Valid, "workflow_id generated column populated")
	assert.Equal(t, fmt.Sprintf("sim-pool-%d", pool1.ID), pool1.WorkflowID.String,
		"workflow_id matches the GENERATED ALWAYS AS expression")

	// Read back via GetSimPool — exercises the matching SELECT path.
	got, err := q.GetSimPool(ctx, pool1.ID)
	require.NoError(t, err)
	assert.Equal(t, "integration_test_pool", got.Name)
	assert.Equal(t, int32(seasonID), got.Season)
	assert.Equal(t, int32(2), got.NumTeams)
	assert.Equal(t, int32(1), got.DraftRounds)
	assert.Equal(t, []string{"G", "A", "PIM", "W", "GA"}, got.Categories,
		"TEXT[] round-trips intact")
	assert.Equal(t, int32(2), got.RosterC)
	assert.Equal(t, int32(3), got.RosterD)
	assert.Equal(t, int32(5), got.RosterBN)
	gotCap, err := pgFloatFromNumeric(got.MaxLLMCostUsdPerPool)
	require.NoError(t, err)
	assert.InDelta(t, 200.0, gotCap, 0.001)

	// Insert two agents. temperature is the only nullable column —
	// agent 1 sets it, agent 2 leaves it NULL. No Name / DraftPosition
	// params anymore: identity is team_name (written by Phase 0's
	// PickTeamName) and draft_position stays NULL until the workflow's
	// RecordDraftOrder persists the shuffled order.
	temp, err := pgNumericFromFloat(0.7)
	require.NoError(t, err)
	agent1, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
		PoolID:         pool1.ID,
		Provider:       "anthropic",
		Model:          "claude-sonnet-4-7",
		Strategy:       "balanced",
		TimeoutSeconds: 30,
		Temperature:    temp,
		APIBase:        "",
		MaxTokens:      4096,
	})
	require.NoError(t, err)
	_, err = q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
		PoolID:   pool1.ID,
		Provider: "anthropic",
		Model:    "claude-haiku-4-5",
		Strategy: "aggressive",
		// temperature, api_base, max_tokens, timeout_seconds left at
		// their zero values — exercises the NULL temperature path.
	})
	require.NoError(t, err)

	// Read agents back via ListSimAgentsByPool — pins the LIST query's
	// column order matches the GET query's column order (would have
	// caught a regression where one Scan list was updated but the
	// other wasn't). Ordering is draft_position NULLS LAST then id;
	// both rows are pre-draft (NULL) so insertion order holds.
	listed, err := q.ListSimAgentsByPool(ctx, pool1.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, "claude-sonnet-4-7", listed[0].Model)
	assert.Equal(t, int32(30), listed[0].TimeoutSeconds)
	assert.True(t, listed[0].Temperature.Valid, "agent 1's temperature is set")
	assert.False(t, listed[0].DraftPosition.Valid, "draft_position is NULL until RecordDraftOrder")
	assert.Empty(t, listed[0].TeamName, "team_name is empty until PickTeamName")
	assert.Equal(t, "claude-haiku-4-5", listed[1].Model)
	assert.False(t, listed[1].Temperature.Valid, "agent 2's temperature is NULL")
	assert.Equal(t, int32(0), listed[1].MaxTokens, "max_tokens defaults to 0 when omitted")

	// Insert a sim_rosters row to verify the FK to players works.
	const playerID int64 = 8478402 // McDavid — purely conventional, the FK doesn't care which ID
	seedPlayer(t, ctx, pool, playerID, sqlcdb.PlayerPositionC)

	err = q.InsertSimRoster(ctx, sqlcdb.InsertSimRosterParams{
		PoolID:      pool1.ID,
		AgentID:     agent1.ID,
		PlayerID:    playerID,
		Slot:        string(SlotBN),
		AcquiredAt:  pgtype.Date{Valid: true, Time: pool1.CreatedAt.Time},
		AcquiredVia: string(AcquiredViaDraft),
	})
	require.NoError(t, err, "InsertSimRoster")

	rosterRows, err := q.ListSimRosterByAgent(ctx, sqlcdb.ListSimRosterByAgentParams{
		PoolID: pool1.ID, AgentID: agent1.ID,
	})
	require.NoError(t, err)
	require.Len(t, rosterRows, 1)
	assert.Equal(t, playerID, rosterRows[0].PlayerID)
	assert.Equal(t, string(SlotBN), rosterRows[0].Slot)
	assert.Equal(t, string(AcquiredViaDraft), rosterRows[0].AcquiredVia)

	// Final cleanup: the pool's ON DELETE CASCADE should pull the
	// agents + roster rows when we drop the pool. Pin so a future
	// schema change can't silently lose the cascade.
	_, err = pool.Exec(ctx, `DELETE FROM sim_pools WHERE id = $1`, pool1.ID)
	require.NoError(t, err)
	remaining, err := q.ListSimAgentsByPool(ctx, pool1.ID)
	require.NoError(t, err)
	assert.Empty(t, remaining, "ON DELETE CASCADE swept the agents")
}

// ============================================================================
// TestIntegrationSeedSmallSeason — sanity-checks the parameterized
// scenario builder in isolation, so a seed-SQL bug surfaces here with
// a row-count diff instead of inside a full-workflow test as a cryptic
// zero-stats failure.
// ============================================================================

func TestIntegrationSeedSmallSeason(t *testing.T) {
	pool := requireIntegrationEnv(t)
	ctx := context.Background()

	cfg := smallSeasonConfig{
		Season:         20252026, // distinct season — doesn't share rows with other tests
		StartDate:      "2025-10-06",
		Days:           3,
		GamesPerDay:    2,
		NumTeams:       4,
		SkatersPerTeam: 10,
		GoaliesPerTeam: 2,
		Seed:           42,
	}
	data := seedSmallSeason(t, ctx, pool, cfg, 32)

	require.Len(t, data.TeamIDs, 4)
	require.Len(t, data.Skaters, 40)
	require.Len(t, data.Goalies, 8)
	require.Len(t, data.GameIDs, cfg.Days*cfg.GamesPerDay)

	// DB-side row counts match what the return value claims.
	var n int
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM games WHERE season=$1`, cfg.Season).Scan(&n))
	assert.Equal(t, len(data.GameIDs), n, "games rows")

	// Every game gets 2 teams × SkatersPerTeam skater rows + 2 goalie rows.
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM game_skater_stats WHERE game_id = ANY($1)`, data.GameIDs).Scan(&n))
	assert.Equal(t, len(data.GameIDs)*2*cfg.SkatersPerTeam, n, "game_skater_stats rows")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM game_goalie_stats WHERE game_id = ANY($1)`, data.GameIDs).Scan(&n))
	assert.Equal(t, len(data.GameIDs)*2, n, "game_goalie_stats rows")

	// Prior-season draft candidates: one club-stats row per player,
	// every score strictly positive (zero-score players are dropped
	// from the candidate pool by loadSkaterCandidates).
	prior := cfg.Season - 10001
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM club_skater_stats WHERE season=$1 AND goals + assists > 0 AND team_id >= $2`,
		prior, smallSeasonTeamIDBase).Scan(&n))
	assert.Equal(t, len(data.Skaters), n, "club_skater_stats candidate rows")
	require.NoError(t, pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM club_goalie_stats WHERE season=$1 AND wins > 0 AND team_id >= $2`,
		prior, smallSeasonTeamIDBase).Scan(&n))
	assert.Equal(t, len(data.Goalies), n, "club_goalie_stats candidate rows")

	// A recorded stat line round-trips: pick the first skater's line
	// on day 1 (their team plays on every rotation with NumTeams=4,
	// GamesPerDay=2) and compare the DB row against the returned line.
	day1 := cfg.StartDate
	skater := data.Skaters[0]
	line, ok := data.SkaterLines[day1][skater.ID]
	require.True(t, ok, "skater %d should have a recorded line on %s", skater.ID, day1)
	var goals, assists, sog, pim, ppp, pm int
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT s.goals, s.assists, s.shots_on_goal, s.penalty_minutes, s.power_play_points, s.plus_minus
		FROM game_skater_stats s JOIN games g ON g.id = s.game_id
		WHERE s.player_id=$1 AND g.game_date=$2::DATE
	`, skater.ID, day1).Scan(&goals, &assists, &sog, &pim, &ppp, &pm))
	assert.Equal(t, line.Goals, goals)
	assert.Equal(t, line.Assists, assists)
	assert.Equal(t, line.SOG, sog)
	assert.Equal(t, line.PIM, pim)
	assert.Equal(t, line.PPP, ppp)
	assert.Equal(t, line.PlusMinus, pm)

	// Same for a goalie line: exactly one goalie per team plays per
	// game; the day-1 playing goalie for the first team is index 0.
	goalie := data.Goalies[0]
	gline, ok := data.GoalieLines[day1][goalie.ID]
	require.True(t, ok, "goalie %d should have a recorded line on %s", goalie.ID, day1)
	var ga, saves, toi int
	var decision string
	require.NoError(t, pool.QueryRow(ctx, `
		SELECT gs.goals_against, gs.saves, gs.toi_seconds, COALESCE(gs.decision::TEXT, '')
		FROM game_goalie_stats gs JOIN games g ON g.id = gs.game_id
		WHERE gs.player_id=$1 AND g.game_date=$2::DATE
	`, goalie.ID, day1).Scan(&ga, &saves, &toi, &decision))
	assert.Equal(t, gline.GoalsAgainst, ga)
	assert.Equal(t, gline.Saves, saves)
	assert.Equal(t, gline.TOISeconds, toi)
	wantDecision := "L"
	if gline.Win {
		wantDecision = "W"
	}
	assert.Equal(t, wantDecision, decision)

	// Determinism: the same config must produce the same lines (the
	// inserts are ON CONFLICT DO NOTHING, so re-seeding is a no-op at
	// the DB layer and the returned lines must still match).
	data2 := seedSmallSeason(t, ctx, pool, cfg, 32)
	assert.Equal(t, data.SkaterLines, data2.SkaterLines, "seeded rand must be deterministic")
	assert.Equal(t, data.GoalieLines, data2.GoalieLines)
}
