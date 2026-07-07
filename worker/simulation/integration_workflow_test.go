//go:build integration

package simulation

// Workflow-driven integration test: spins up a real Temporal worker
// running the full SimPoolWorkflow, with a mock LLM that returns
// deterministic tool calls. After the workflow completes, asserts
// the SUM-invariants on the sim_agent_* tables.
//
// Required env vars (in addition to the suite-level PUCKDB_TEST_PG_URL):
//
//   PUCKDB_TEST_REDIS_ADDR       Redis host:port for ProgressActivities
//                                state. Defaults to localhost:6379.
//                                The integer DB index is 15 (chosen to
//                                avoid collision with dev/prod data).
//
// Optional env vars:
//
//   PUCKDB_TEST_TEMPORAL_HOSTPORT  Existing Temporal server. If set,
//                                  the test dials it instead of
//                                  spinning up an in-process DevServer.
//                                  Default: spin up DevServer (downloads
//                                  the Temporal CLI on first run, ~50MB).
//
// Test skips cleanly when its required services aren't reachable —
// see startTemporalForTest and connectRedisForTest.

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/llm"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

// ============================================================================
// Service plumbing — Temporal + Redis
// ============================================================================

// startTemporalForTest returns a Temporal client + a cleanup func.
// Two modes:
//  1. PUCKDB_TEST_TEMPORAL_HOSTPORT set → dial that.
//  2. otherwise → spin up an in-process DevServer.
//
// DevServer downloads the Temporal CLI binary the first time it's
// invoked (cached afterward). Slow first run (~30s); subsequent
// runs ~3s.
func startTemporalForTest(t *testing.T) (client.Client, func()) {
	t.Helper()
	if hp := os.Getenv("PUCKDB_TEST_TEMPORAL_HOSTPORT"); hp != "" {
		c, err := client.Dial(client.Options{HostPort: hp})
		if err != nil {
			t.Skipf("PUCKDB_TEST_TEMPORAL_HOSTPORT=%q dial failed: %v", hp, err)
		}
		return c, func() { c.Close() }
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	srv, err := testsuite.StartDevServer(ctx, testsuite.DevServerOptions{})
	if err != nil {
		t.Skipf("DevServer start failed: %v (set PUCKDB_TEST_TEMPORAL_HOSTPORT to use external Temporal)", err)
	}
	return srv.Client(), func() { _ = srv.Stop() }
}

// connectRedisForTest opens a redis client against the configured
// host (default localhost:6379, DB 15). DB 15 chosen as a high
// index unlikely to collide with dev/prod keys.
func connectRedisForTest(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("PUCKDB_TEST_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	c := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Ping(ctx).Err(); err != nil {
		t.Skipf("Redis at %s unreachable: %v (set PUCKDB_TEST_REDIS_ADDR to override)", addr, err)
	}
	// Flush DB 15 so a previously-aborted run's progress reports
	// don't bleed into this run's tracker rehydration.
	if err := c.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("flush test redis db: %v", err)
	}
	return c
}

// ============================================================================
// Fixture seeding
// ============================================================================

// seedTeam inserts a season_teams row so games + stats joins resolve.
// Idempotent on (season, team_id).
func seedTeam(t *testing.T, ctx context.Context, pool *pgxpool.Pool, season int32, teamID int64, abbrev string) {
	t.Helper()
	// season_teams composite PK is (season, team_id). The test only
	// needs a name + abbrev for display purposes; everything else
	// defaults.
	_, err := pool.Exec(ctx, `
		INSERT INTO season_teams (season, team_id, full_name, abbrev, place_name, common_name, division_name, division_abbrev, conference_name, conference_abbrev, logo_url, dark_logo_url, franchise_id)
		VALUES ($1, $2, $3, $3, $3, $3, '', '', '', '', '', '', $2)
		ON CONFLICT DO NOTHING
	`, season, teamID, abbrev)
	require.NoErrorf(t, err, "seed season_team (%d, %d)", season, teamID)
}

// seedClubSkaterStats puts one row in club_skater_stats so
// LoadDraftCandidates' GetClubSkaterStatsBySeason returns a real
// candidate. Goals + assists drive the prior-season ranking.
func seedClubSkaterStats(t *testing.T, ctx context.Context, pool *pgxpool.Pool, season int32, playerID int64, teamID int64, goals, assists int) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO club_skater_stats (season, game_type, team_id, player_id, games_played, goals, assists, points, plus_minus, penalty_minutes, power_play_goals, shorthanded_goals, game_winning_goals, overtime_goals, shots, shooting_pctg, avg_toi_per_game, avg_shifts_per_game, faceoff_win_pctg)
		VALUES ($1, 'regular_season', $2, $3, 82, $4, $5, $4 + $5, 0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0)
		ON CONFLICT DO NOTHING
	`, season, teamID, playerID, goals, assists)
	require.NoErrorf(t, err, "seed club_skater_stats player %d", playerID)
}

// seedClubGoalieStats — goalie equivalent. Wins drive the ranking.
func seedClubGoalieStats(t *testing.T, ctx context.Context, pool *pgxpool.Pool, season int32, playerID int64, teamID int64, wins int) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO club_goalie_stats (season, game_type, team_id, player_id, games_played, games_started, wins, losses, overtime_losses, goals_against_average, save_percentage, shots_against, saves, goals_against, shutouts, goals, assists, points, penalty_minutes, toi_seconds)
		VALUES ($1, 'regular_season', $2, $3, 50, 50, $4, 20, 5, 2.5, 0.92, 1500, 1380, 120, 5, 0, 0, 0, 0, 180000)
		ON CONFLICT DO NOTHING
	`, season, teamID, playerID, wins)
	require.NoErrorf(t, err, "seed club_goalie_stats player %d", playerID)
}

// seedGame inserts one row in games. game_type=regular_season,
// game_state=FINAL — required filters for the day-loop scoring path.
func seedGame(t *testing.T, ctx context.Context, pool *pgxpool.Pool, gameID int64, season int32, gameDate string, homeID, awayID int64) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id, home_team_score, away_team_score, period_descriptor_number, venue, start_time_utc)
		VALUES ($1, $2, 'regular_season', $3::DATE, 'FINAL', $4, $5, 3, 2, 3, '', NOW())
		ON CONFLICT DO NOTHING
	`, gameID, season, gameDate, homeID, awayID)
	require.NoErrorf(t, err, "seed game %d", gameID)
}

// seedGameSkaterStats inserts a per-game stat row. CollectDayStats
// reads these to project onto the agent's roster.
func seedGameSkaterStats(t *testing.T, ctx context.Context, pool *pgxpool.Pool, gameID, playerID, teamID int64, goals, assists int) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position, goals, assists, points, plus_minus, shots_on_goal, toi_seconds, shifts, faceoff_winning_pctg, hits, blocked_shots, penalty_minutes, giveaways, takeaways, power_play_goals, power_play_points, game_winning_goals, ot_goals)
		VALUES ($1, $2, $3, true, 99, 'C', $4, $5, $4 + $5, 0, 3, 1200, 20, NULL, 1, 1, 0, 0, 0, 0, 0, 0, 0)
		ON CONFLICT DO NOTHING
	`, gameID, playerID, teamID, goals, assists)
	require.NoErrorf(t, err, "seed game_skater_stats game=%d player=%d", gameID, playerID)
}

// seedScenario seeds the full data set the workflow test needs:
//   - Two teams in the test season AND prior season (LoadDraftCandidates
//     reads prior-season stats; CollectDayStats reads test-season game
//     stats).
//   - Four players (two skaters per team) across both seasons.
//   - Prior-season club_skater_stats so the draft has candidates.
//   - Five games in the test season, one per day, with two
//     game_skater_stats rows per game (the two drafted players).
//
// Returns the slice of player IDs in scripted draft order. The mock
// LLM uses these to produce its scripted draft_player calls.
func seedScenario(t *testing.T, ctx context.Context, pool *pgxpool.Pool, testSeason int32) (playerIDs []int64) {
	t.Helper()
	priorSeason := testSeason - 10001

	// Seed seasons (the suite-level setup already inserted testSeason
	// when the smoke test ran; this is idempotent so it's fine).
	seedSeason(t, ctx, pool, testSeason)
	seedSeason(t, ctx, pool, priorSeason)

	const teamA, teamB int64 = 4001, 4002
	for _, season := range []int32{testSeason, priorSeason} {
		seedTeam(t, ctx, pool, season, teamA, "TMA")
		seedTeam(t, ctx, pool, season, teamB, "TMB")
	}

	// Two skaters per team — total 4 candidates. The mock LLM picks
	// the first two; the rest stay as free agents (proves the FA
	// pool query works for ManageRoster on day-1+).
	const p1, p2, p3, p4 int64 = 5001, 5002, 5003, 5004
	playerIDs = []int64{p1, p2, p3, p4}
	for _, id := range playerIDs {
		seedPlayer(t, ctx, pool, id, sqlcdb.PlayerPositionC)
	}

	// Prior-season club stats: pinned numbers so the ranking is
	// deterministic. P1 has the highest score (10+15=25), others
	// trail.
	seedClubSkaterStats(t, ctx, pool, priorSeason, p1, teamA, 10, 15) // 25
	seedClubSkaterStats(t, ctx, pool, priorSeason, p2, teamA, 8, 10)  // 18
	seedClubSkaterStats(t, ctx, pool, priorSeason, p3, teamB, 5, 5)   // 10
	seedClubSkaterStats(t, ctx, pool, priorSeason, p4, teamB, 3, 4)   // 7

	// Five games, one per day starting Oct 8 — TeamA vs TeamB.
	const startDate = "2024-10-08"
	gameIDs := []int64{6001, 6002, 6003, 6004, 6005}
	for i, gid := range gameIDs {
		date := offsetDate(startDate, i)
		seedGame(t, ctx, pool, gid, testSeason, date, teamA, teamB)
		// Both drafted players play every game. Goals/assists
		// vary so daily values are non-zero and distinct.
		seedGameSkaterStats(t, ctx, pool, gid, p1, teamA, 1, i%2)
		seedGameSkaterStats(t, ctx, pool, gid, p2, teamA, 0, 1)
	}

	return playerIDs
}

// offsetDate returns startDate + n days as a YYYY-MM-DD string.
// Avoids dragging time-arithmetic into the test body.
func offsetDate(startDate string, n int) string {
	t, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		panic(err)
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// ============================================================================
// Mock LLM — scripted responses for predictable workflow behavior
// ============================================================================

// scriptedIntegClient is the integration-test LLM client used by
// TestIntegrationFullDraftAnd5Day. It returns scripted tool-call
// responses for the draft phase, then "no tool calls" responses
// for every subsequent (daily) call — agents pass on every day,
// no mid-sim trades.
//
// Shared across every Agent the AgentFactory builds. The atomic
// counter sequences calls deterministically, so test outcomes
// don't depend on goroutine interleaving (Temporal can dispatch
// activities in parallel even for serially-numbered draft picks).
//
// Tests that need PER-AGENT scripting (e.g., agent A drops on day
// 3, agent B passes throughout) use perAgentMockClient instead —
// the global counter here can't differentiate between agents.
type scriptedIntegClient struct {
	draftPicks []int64 // player_ids to return in order
	called     atomic.Int32
}

func (c *scriptedIntegClient) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	idx := int(c.called.Add(1)) - 1
	// Draft phase: scripted picks.
	if idx < len(c.draftPicks) {
		pid := c.draftPicks[idx]
		return &llm.Response{
			Content: fmt.Sprintf("Picking player %d", pid),
			ToolCalls: []llm.ToolCall{{
				ID:   fmt.Sprintf("tc_%d", idx),
				Type: "function",
				Function: llm.ToolCallFunction{
					Name:      ToolDraftPlayer,
					Arguments: fmt.Sprintf(`{"player_id":%d}`, pid),
				},
			}},
			Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		}, nil
	}
	// Daily phase: no tool calls = pass.
	return &llm.Response{
		Content: "No moves today.",
		Usage:   &llm.Usage{PromptTokens: 200, CompletionTokens: 30, TotalTokens: 230},
	}, nil
}

// perAgentMockClient is the per-agent LLM mock for tests where the
// two agents need to behave differently on different days (drop on
// day 3, claim on day 4, etc).
//
// Each Agent the AgentFactory builds gets its OWN client wrapping
// its OWN script. The agent processing order is randomized per day
// (workflow.SideEffect-seeded shuffle), so a global call counter
// can't tell which agent is calling — but a per-agent counter
// always advances in sync with that agent's actual decisions.
//
// Script entries: each entry is one Complete-call response.
// Indices are calls made BY THIS AGENT, in workflow order:
//
//	[0] = first call (this agent's draft pick)
//	[1] = day 1 manage_roster
//	[2] = day 2 manage_roster
//	... etc.
//
// Running off the end of the script returns the default
// "no tool calls" pass response.
type perAgentMockClient struct {
	agentName string
	script    []*llm.Response
	called    atomic.Int32
}

func (c *perAgentMockClient) Complete(_ context.Context, _ *llm.Request) (*llm.Response, error) {
	idx := int(c.called.Add(1)) - 1
	if idx < len(c.script) && c.script[idx] != nil {
		return c.script[idx], nil
	}
	return passResponse(), nil
}

// passResponse is the canonical "no tool calls" reply — the workflow
// treats a response with empty ToolCalls as a pass.
func passResponse() *llm.Response {
	return &llm.Response{
		Content: "Pass.",
		Usage:   &llm.Usage{PromptTokens: 200, CompletionTokens: 10, TotalTokens: 210},
	}
}

// draftPickResponse builds a scripted draft_player tool-call response.
func draftPickResponse(playerID int64) *llm.Response {
	return &llm.Response{
		Content: fmt.Sprintf("Picking player %d", playerID),
		ToolCalls: []llm.ToolCall{{
			ID:   "tc_draft",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      ToolDraftPlayer,
				Arguments: fmt.Sprintf(`{"player_id":%d}`, playerID),
			},
		}},
		Usage: &llm.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
	}
}

// dropPlayerResponse builds a scripted drop_player tool-call response.
func dropPlayerResponse(playerID int64, reason string) *llm.Response {
	return &llm.Response{
		Content: reason,
		ToolCalls: []llm.ToolCall{{
			ID:   "tc_drop",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      ToolDropPlayer,
				Arguments: fmt.Sprintf(`{"player_id":%d}`, playerID),
			},
		}},
		Usage: &llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220},
	}
}

// claimPlayerResponse builds a scripted claim_player tool-call response.
func claimPlayerResponse(playerID int64, reason string) *llm.Response {
	return &llm.Response{
		Content: reason,
		ToolCalls: []llm.ToolCall{{
			ID:   "tc_claim",
			Type: "function",
			Function: llm.ToolCallFunction{
				Name:      ToolClaimPlayer,
				Arguments: fmt.Sprintf(`{"player_id":%d}`, playerID),
			},
		}},
		Usage: &llm.Usage{PromptTokens: 200, CompletionTokens: 20, TotalTokens: 220},
	}
}

// ============================================================================
// Pool setup — bypass the GraphQL resolver for direct DB control
// ============================================================================

// createIntegrationPool inserts a sim_pools row + 2 sim_agents rows
// directly. Mirrors what graph/simulation_helpers.go's
// createSimPoolImpl does, minus the workflow start (the test starts
// the workflow itself with explicit options).
//
// Uses tiny roster sizes (1 active slot, 1 BN, 0 IR) and 1 draft
// round so the draft completes in 2 picks total. waiverDays is
// per-test: drop-pickup tests pick whatever fits the day window.
//
// poolName lets each test use a distinct row so leftover state from
// a previously-aborted run can be filtered (the suite-level
// MigrateDown also wipes everything, but a per-test name keeps test
// runs distinguishable in logs).
func createIntegrationPool(t *testing.T, ctx context.Context, pool *pgxpool.Pool, season int32, waiverDays int, poolName string) (poolID int32, agent1ID, agent2ID int32) {
	t.Helper()
	q := sqlcdb.New(pool)
	cap, err := pgNumericFromFloat(200.0)
	require.NoError(t, err)
	row, err := q.InsertSimPool(ctx, sqlcdb.InsertSimPoolParams{
		Name:                 poolName,
		Season:               season,
		Status:               string(PoolStatusDraft),
		NumTeams:             2,
		WaiverDays:           int32(waiverDays),
		DraftRounds:          1,
		MaxLLMCostUsdPerPool: cap,
		Categories:           []string{"G", "A"},
		RosterC:              1,
		RosterBN:             1,
	})
	require.NoError(t, err)
	poolID = row.ID

	a1, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
		PoolID: poolID, DraftPosition: 1,
		Provider: "anthropic", Model: "claude-haiku-4-5", Strategy: "balanced",
	})
	require.NoError(t, err)
	a2, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
		PoolID: poolID, DraftPosition: 2,
		Provider: "anthropic", Model: "claude-haiku-4-5", Strategy: "aggressive",
	})
	require.NoError(t, err)
	return poolID, a1.ID, a2.ID
}

// ============================================================================
// Worker setup — registers all sim activities + workflow on a real
// Temporal worker. Returned cleanup stops the worker. The
// activitiesFor function lets the caller customize the
// AgentFactory per-test (single-shared client vs per-agent client).
// ============================================================================

func setupIntegrationWorker(t *testing.T, tc client.Client, pgPool *pgxpool.Pool, redisClient *redis.Client, agentFactory AgentFactory) func() {
	t.Helper()
	acts := &Activities{
		Queries:      sqlcdb.New(pgPool),
		Tx:           NewPgxTransactor(pgPool),
		Signaler:     NewTemporalSignaler(tc),
		AgentFactory: agentFactory,
	}
	progressActs := &shared.ProgressActivities{RedisClient: redisClient}

	w := worker.New(tc, "puckdb-tasks", worker.Options{})
	w.RegisterWorkflow(SimPoolWorkflow)
	w.RegisterActivity(acts.LoadPoolState)
	w.RegisterActivity(acts.LoadDraftCandidates)
	w.RegisterActivity(acts.RecordDraftOrder)
	w.RegisterActivity(acts.PickTeamName)
	w.RegisterActivity(acts.DraftPick)
	w.RegisterActivity(acts.ProcessWaivers)
	w.RegisterActivity(acts.BuildFreeAgentPool)
	w.RegisterActivity(acts.BuildManageRosterContext)
	w.RegisterActivity(acts.ManageRoster)
	w.RegisterActivity(acts.CollectDayStats)
	w.RegisterActivity(acts.UpdateStandings)
	w.RegisterActivity(acts.SetPoolStatus)
	w.RegisterActivity(acts.RecordDayDuration)
	w.RegisterActivity(progressActs.Save)
	w.RegisterActivity(progressActs.Load)
	w.RegisterActivity(progressActs.DeleteBatch)
	require.NoError(t, w.Start())
	return w.Stop
}

// runWorkflowToCompletion starts SimPoolWorkflow with the given
// pool and waits up to 60 seconds for it to finish. Fails the test
// on timeout — every integration scenario terminates within seconds
// once activities are mocked, so 60s is a generous upper bound.
func runWorkflowToCompletion(t *testing.T, ctx context.Context, tc client.Client, poolID int32) {
	t.Helper()
	wfID := simPoolWorkflowIDForPoolStr(poolID)
	run, err := tc.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: wfID, TaskQueue: "puckdb-tasks"},
		SimPoolWorkflow,
		SimPoolWorkflowInput{PoolID: poolID, AutoAdvance: true},
	)
	require.NoError(t, err)
	waitCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := run.Get(waitCtx, nil); err != nil {
		t.Fatalf("workflow %s run.Get: %v", wfID, err)
	}
}

// ============================================================================
// TestIntegrationFullDraftAnd5Day — the headline workflow test
// ============================================================================

func TestIntegrationFullDraftAnd5Day(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()

	ctx := context.Background()
	const testSeason int32 = 20242025

	playerIDs := seedScenario(t, ctx, pgPool, testSeason)
	poolID, _, _ := createIntegrationPool(t, ctx, pgPool, testSeason, 2, "integration_test_workflow")

	// Build the Activities with a mocked AgentFactory so the LLM
	// calls are deterministic. The script picks playerIDs[0] and
	// playerIDs[1] in order.
	mockClient := &scriptedIntegClient{draftPicks: []int64{playerIDs[0], playerIDs[1]}}
	stop := setupIntegrationWorker(t, tc, pgPool, redisClient,
		func(cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
			return &Agent{
				Config:       cfg,
				Client:       mockClient,
				systemPrompt: "test system",
				draftTools:   DraftTools(),
				dailyTools:   DailyTools(),
			}, nil
		})
	defer stop()

	// Limit the season range so the day loop terminates quickly:
	// override seasons.standings_end to day 5 of the simulation.
	endDate := offsetDate("2024-10-08", 4) // 5 days inclusive
	_, err := pgPool.Exec(ctx, `UPDATE seasons SET standings_start='2024-10-08', standings_end=$1 WHERE id=$2`, endDate, testSeason)
	require.NoError(t, err)

	runWorkflowToCompletion(t, ctx, tc, poolID)

	// Invariant assertions — read from the DB and check the
	// fundamental sums.
	assertTotalsEqualSumOfDailies(t, ctx, pgPool, poolID)
	assertDailyEqualsSumOfPerPlayer(t, ctx, pgPool, poolID)
	assertDraftedTwoPlayers(t, ctx, pgPool, poolID, playerIDs[:2])
	assertPoolCompleted(t, ctx, pgPool, poolID)
}

// ============================================================================
// TestIntegrationDropPickupHistoricalAttribution — agent A drops player
// X on day 3; verify days 1-2 attribution rows for that player still
// exist (the per-player attribution layer is the source of truth and
// must NOT be re-written / deleted on later drops).
// ============================================================================

func TestIntegrationDropPickupHistoricalAttribution(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()

	ctx := context.Background()
	const testSeason int32 = 20242025

	playerIDs := seedScenario(t, ctx, pgPool, testSeason)
	poolID, _, _ := createIntegrationPool(t, ctx, pgPool, testSeason, 2, "integration_test_droppickup")

	// Per-agent scripts. Agent processing order is randomized each
	// day, but each agent's OWN counter advances in workflow order:
	//   [0] draft pick
	//   [1] day 1 manage_roster (Oct 8)
	//   [2] day 2 manage_roster (Oct 9)
	//   [3] day 3 manage_roster (Oct 10) — Alpha drops player A
	//   [4] day 4 (Oct 11) — pass
	//   [5] day 5 (Oct 12) — pass
	alphaScript := []*llm.Response{
		draftPickResponse(playerIDs[0]), // [0] draft
		nil,                             // [1] day 1 pass
		nil,                             // [2] day 2 pass
		dropPlayerResponse(playerIDs[0], "Drop on day 3"), // [3] day 3 DROP
		nil, // [4] day 4 pass
		nil, // [5] day 5 pass
	}
	bravoScript := []*llm.Response{
		draftPickResponse(playerIDs[1]), // [0] draft
		// Days 1-5: nil entries fall through to passResponse.
	}

	stop := setupIntegrationWorker(t, tc, pgPool, redisClient,
		perAgentFactory(alphaScript, bravoScript))
	defer stop()

	endDate := offsetDate("2024-10-08", 4)
	_, err := pgPool.Exec(ctx, `UPDATE seasons SET standings_start='2024-10-08', standings_end=$1 WHERE id=$2`, endDate, testSeason)
	require.NoError(t, err)

	runWorkflowToCompletion(t, ctx, tc, poolID)

	// Find Alpha's agent_id (Alpha was drafted first per
	// createIntegrationPool's draft_position=1 ordering — but the
	// shuffled draft order means Alpha's *pick* could land in
	// either round-1 slot; what we know for certain is that
	// playerIDs[0] is on SOMEONE's roster history, and Alpha is
	// the agent whose script dropped them).
	alphaID := lookupAgentIDByName(t, ctx, pgPool, poolID, "Alpha")

	// Days 1 and 2 should have per-player attribution rows for the
	// dropped player under Alpha. Day 3 onward must NOT (player was
	// dropped on day 3 BEFORE that day's CollectDayStats ran).
	assertPlayerAttributionRowsForDays(t, ctx, pgPool, poolID, alphaID, playerIDs[0],
		[]string{"2024-10-08", "2024-10-09"})
	assertNoPlayerAttributionRowsForDays(t, ctx, pgPool, poolID, alphaID, playerIDs[0],
		[]string{"2024-10-10", "2024-10-11", "2024-10-12"})

	// SUM-invariants still hold post-drop — the totals = sum of
	// dailies relationship is preserved even when agent rosters
	// shrink mid-sim.
	assertTotalsEqualSumOfDailies(t, ctx, pgPool, poolID)
	assertDailyEqualsSumOfPerPlayer(t, ctx, pgPool, poolID)
}

// ============================================================================
// TestIntegrationWaiverClaimResolution — agent A drops on day 3,
// agent B claims on day 4, ProcessWaivers awards to B on day 5
// (waiver_days=1 fits the 5-day window).
// ============================================================================

func TestIntegrationWaiverClaimResolution(t *testing.T) {
	pgPool := requireIntegrationEnv(t)
	redisClient := connectRedisForTest(t)
	defer redisClient.Close()
	tc, tcCleanup := startTemporalForTest(t)
	defer tcCleanup()

	ctx := context.Background()
	const testSeason int32 = 20242025

	playerIDs := seedScenario(t, ctx, pgPool, testSeason)
	// waiverDays=1 so a claim filed on day 4 processes on day 5
	// (within the 5-day window).
	poolID, _, _ := createIntegrationPool(t, ctx, pgPool, testSeason, 1, "integration_test_waiver")

	// Agent A drafts player playerIDs[0], drops them on day 3.
	// Agent B drafts player playerIDs[1], drops them on day 2,
	// then claims playerIDs[0] on day 4.
	//
	// Why does B drop their own pick? B needs to make room before
	// claiming — roster size is 2 (1 active + 1 BN), and a successful
	// claim adds a player without a corresponding drop in this test
	// (the claim_player tool's optional drop_player_id arg is NOT
	// set). With 2 players already on the roster, the FUTURE-state
	// "roster full + 1" check in ValidateClaimPlayer would reject
	// the claim. Dropping first frees a slot.
	//
	// Bravo's day-2 drop sends playerIDs[1] to waivers. Alpha doesn't
	// claim it back, so it just expires — irrelevant to the test.
	alphaScript := []*llm.Response{
		draftPickResponse(playerIDs[0]), // [0] draft
		nil,                             // [1] day 1
		nil,                             // [2] day 2
		dropPlayerResponse(playerIDs[0], "Alpha drops X on day 3"), // [3] day 3 DROP
		nil, // [4] day 4
		nil, // [5] day 5
	}
	bravoScript := []*llm.Response{
		draftPickResponse(playerIDs[1]), // [0] draft
		nil,                             // [1] day 1
		dropPlayerResponse(playerIDs[1], "Bravo drops Y on day 2"), // [2] day 2 DROP (frees roster slot)
		nil, // [3] day 3 (Alpha drops X this turn)
		claimPlayerResponse(playerIDs[0], "Bravo claims X on day 4"), // [4] day 4 CLAIM
		nil, // [5] day 5
	}

	stop := setupIntegrationWorker(t, tc, pgPool, redisClient,
		perAgentFactory(alphaScript, bravoScript))
	defer stop()

	endDate := offsetDate("2024-10-08", 4)
	_, err := pgPool.Exec(ctx, `UPDATE seasons SET standings_start='2024-10-08', standings_end=$1 WHERE id=$2`, endDate, testSeason)
	require.NoError(t, err)

	runWorkflowToCompletion(t, ctx, tc, poolID)

	// Day 5: ProcessWaivers fires (start of the day loop). With
	// waiver_days=1, Bravo's day-4 claim has process_date = day 5
	// → resolves today. Bravo is the only claimant, wins.
	bravoID := lookupAgentIDByName(t, ctx, pgPool, poolID, "Bravo")
	assertPlayerOnRoster(t, ctx, pgPool, poolID, bravoID, playerIDs[0])

	// The claim row's status flipped to 'won'.
	assertWaiverClaimStatus(t, ctx, pgPool, poolID, bravoID, playerIDs[0], "won")
}

// ============================================================================
// Helpers used by the new tests
// ============================================================================

// perAgentFactory builds an AgentFactory that hands each agent its
// own scripted LLM client based on the agent's Name.
func perAgentFactory(alphaScript, bravoScript []*llm.Response) AgentFactory {
	scripts := map[string][]*llm.Response{
		"Alpha": alphaScript,
		"Bravo": bravoScript,
	}
	clients := map[string]*perAgentMockClient{}
	return func(cfg AgentConfig, _ map[llm.Provider]llm.ProviderConfig, _ int) (*Agent, error) {
		// One client per agent name, cached so each agent's call
		// counter persists across NewAgent invocations (the
		// AgentFactory may be called more than once if agentCache
		// were ever bypassed; today it's called exactly once per
		// (pool, agent) but defensive caching is cheap).
		c, ok := clients[cfg.Name]
		if !ok {
			c = &perAgentMockClient{
				agentName: cfg.Name,
				script:    scripts[cfg.Name],
			}
			clients[cfg.Name] = c
		}
		return &Agent{
			Config:       cfg,
			Client:       c,
			systemPrompt: "test system for " + cfg.Name,
			draftTools:   DraftTools(),
			dailyTools:   DailyTools(),
		}, nil
	}
}

// lookupAgentIDByName returns the sim_agents.id matching the given
// (pool, name). Tests use this to convert from the human-friendly
// agent name in the script to the int32 IDs the DB queries want.
func lookupAgentIDByName(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, name string) int32 {
	t.Helper()
	var id int32
	err := pgPool.QueryRow(ctx, `SELECT id FROM sim_agents WHERE pool_id=$1 AND name=$2`, poolID, name).Scan(&id)
	require.NoErrorf(t, err, "lookup agent %q in pool %d", name, poolID)
	return id
}

// assertPlayerAttributionRowsForDays asserts at least one
// sim_agent_daily_player_stats row exists for the (pool, agent,
// player) triple on EACH of the given dates.
func assertPlayerAttributionRowsForDays(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64, dates []string) {
	t.Helper()
	for _, d := range dates {
		var n int
		err := pgPool.QueryRow(ctx, `
			SELECT COUNT(*) FROM sim_agent_daily_player_stats
			WHERE pool_id=$1 AND agent_id=$2 AND player_id=$3 AND date=$4::DATE
		`, poolID, agentID, playerID, d).Scan(&n)
		require.NoError(t, err)
		assert.Greaterf(t, n, 0,
			"expected sim_agent_daily_player_stats rows for agent=%d player=%d date=%s — historical attribution must persist after a drop",
			agentID, playerID, d)
	}
}

// assertNoPlayerAttributionRowsForDays asserts NO
// sim_agent_daily_player_stats row exists for the (pool, agent,
// player) triple on ANY of the given dates.
func assertNoPlayerAttributionRowsForDays(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64, dates []string) {
	t.Helper()
	for _, d := range dates {
		var n int
		err := pgPool.QueryRow(ctx, `
			SELECT COUNT(*) FROM sim_agent_daily_player_stats
			WHERE pool_id=$1 AND agent_id=$2 AND player_id=$3 AND date=$4::DATE
		`, poolID, agentID, playerID, d).Scan(&n)
		require.NoError(t, err)
		assert.Equalf(t, 0, n,
			"expected NO sim_agent_daily_player_stats for agent=%d player=%d date=%s — player was dropped before that day's scoring",
			agentID, playerID, d)
	}
}

// assertPlayerOnRoster asserts a sim_rosters row exists for the
// (pool, agent, player) triple. Used by the waiver test after
// ProcessWaivers should have moved the claimed player.
func assertPlayerOnRoster(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64) {
	t.Helper()
	var n int
	err := pgPool.QueryRow(ctx, `
		SELECT COUNT(*) FROM sim_rosters
		WHERE pool_id=$1 AND agent_id=$2 AND player_id=$3
	`, poolID, agentID, playerID).Scan(&n)
	require.NoError(t, err)
	assert.Equalf(t, 1, n, "expected player %d on agent %d's roster", playerID, agentID)
}

// assertWaiverClaimStatus asserts the most-recent sim_waiver_claims
// row for the (pool, agent, player) triple has the given status
// ('won' / 'lost' / 'pending').
func assertWaiverClaimStatus(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID, agentID int32, playerID int64, expected string) {
	t.Helper()
	var status string
	err := pgPool.QueryRow(ctx, `
		SELECT status FROM sim_waiver_claims
		WHERE pool_id=$1 AND agent_id=$2 AND player_id=$3
		ORDER BY id DESC LIMIT 1
	`, poolID, agentID, playerID).Scan(&status)
	require.NoErrorf(t, err, "lookup waiver claim agent=%d player=%d", agentID, playerID)
	assert.Equal(t, expected, status)
}

// simPoolWorkflowIDForPoolStr returns "sim-pool-{id}" — duplicates
// the helper in graph/simulation_helpers.go but accessible from
// here without a package import.
func simPoolWorkflowIDForPoolStr(poolID int32) string {
	return "sim-pool-" + strconv.FormatInt(int64(poolID), 10)
}

// ============================================================================
// Invariant assertions
// ============================================================================

// assertTotalsEqualSumOfDailies verifies the contract:
//
//	sim_agent_totals.value = SUM(sim_agent_daily_stats.value)
//	  per (pool, agent, category)
//
// for every counting category (i.e., NOT GAA — GAA is computed from
// goalie_ga / goalie_toi_seconds components, not from value sums).
func assertTotalsEqualSumOfDailies(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) {
	t.Helper()
	rows, err := pgPool.Query(ctx, `
		SELECT
			t.agent_id,
			t.category,
			t.value::TEXT AS total_value,
			COALESCE(SUM(d.value), 0)::TEXT AS sum_of_dailies
		FROM sim_agent_totals t
		LEFT JOIN sim_agent_daily_stats d
			ON d.pool_id = t.pool_id AND d.agent_id = t.agent_id AND d.category = t.category
		WHERE t.pool_id = $1 AND t.category <> 'GAA'
		GROUP BY t.agent_id, t.category, t.value
	`, poolID)
	require.NoError(t, err)
	defer rows.Close()

	checked := 0
	for rows.Next() {
		var agentID int32
		var category string
		var totalValue, sumOfDailies string
		require.NoError(t, rows.Scan(&agentID, &category, &totalValue, &sumOfDailies))
		assert.Equalf(t, totalValue, sumOfDailies,
			"INVARIANT: sim_agent_totals.value (%s) != SUM(sim_agent_daily_stats.value) (%s) for agent=%d category=%s",
			totalValue, sumOfDailies, agentID, category)
		checked++
	}
	require.NoError(t, rows.Err())
	assert.Greater(t, checked, 0, "expected at least one (agent, category) row to verify")
}

// assertDailyEqualsSumOfPerPlayer verifies the contract:
//
//	sim_agent_daily_stats.value = SUM(sim_agent_daily_player_stats.value)
//	  per (pool, agent, date, category)
func assertDailyEqualsSumOfPerPlayer(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) {
	t.Helper()
	rows, err := pgPool.Query(ctx, `
		SELECT
			d.agent_id,
			d.date,
			d.category,
			d.value::TEXT AS daily_value,
			COALESCE(SUM(p.value), 0)::TEXT AS sum_of_per_player
		FROM sim_agent_daily_stats d
		LEFT JOIN sim_agent_daily_player_stats p
			ON p.pool_id = d.pool_id AND p.agent_id = d.agent_id
			AND p.date = d.date AND p.category = d.category
		WHERE d.pool_id = $1 AND d.category <> 'GAA'
		GROUP BY d.agent_id, d.date, d.category, d.value
	`, poolID)
	require.NoError(t, err)
	defer rows.Close()

	checked := 0
	for rows.Next() {
		var agentID int32
		var date pgtype.Date
		var category, dailyValue, sumOfPerPlayer string
		require.NoError(t, rows.Scan(&agentID, &date, &category, &dailyValue, &sumOfPerPlayer))
		assert.Equalf(t, dailyValue, sumOfPerPlayer,
			"INVARIANT: sim_agent_daily_stats.value (%s) != SUM(sim_agent_daily_player_stats.value) (%s) for agent=%d date=%s category=%s",
			dailyValue, sumOfPerPlayer, agentID, date.Time.Format("2006-01-02"), category)
		checked++
	}
	require.NoError(t, rows.Err())
	assert.Greater(t, checked, 0, "expected at least one (agent, date, category) row to verify")
}

// assertDraftedTwoPlayers checks that the draft phase actually wrote
// the two scripted picks to sim_rosters. Without this the
// SUM-invariants might trivially hold via empty tables.
func assertDraftedTwoPlayers(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32, expected []int64) {
	t.Helper()
	rows, err := pgPool.Query(ctx, `SELECT player_id FROM sim_rosters WHERE pool_id=$1 ORDER BY player_id`, poolID)
	require.NoError(t, err)
	defer rows.Close()
	var got []int64
	for rows.Next() {
		var pid int64
		require.NoError(t, rows.Scan(&pid))
		got = append(got, pid)
	}
	require.NoError(t, rows.Err())
	assert.ElementsMatch(t, expected, got, "drafted players")
}

// assertPoolCompleted verifies the workflow's Phase 3 wrote
// status='complete' via SetPoolStatusActivity. Catches a class of
// bugs where the day loop exits without going through Phase 3.
func assertPoolCompleted(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) {
	t.Helper()
	var status string
	err := pgPool.QueryRow(ctx, `SELECT status FROM sim_pools WHERE id = $1`, poolID).Scan(&status)
	require.NoError(t, err)
	assert.Equal(t, string(PoolStatusComplete), status)
}
