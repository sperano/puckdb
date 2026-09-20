//go:build integration || livellm

package simulation

// Shared harness + fixture-seeding helpers for the two integration
// test layers:
//
//   - //go:build integration  — hermetic tests with mocked LLM clients
//     (integration_test.go, integration_workflow_test.go)
//   - //go:build livellm      — operator-run live-model smoke against a
//     real Ollama host (integration_live_test.go)
//
// This file builds under EITHER tag so both layers share one TestMain,
// one seeding vocabulary, and one worker-bootstrap path.
//
// Required environment variables:
//
//   PUCKDB_TEST_PG_URL   Postgres connection URL for the integration
//                        test database. The schema is migrated up at
//                        TestMain start and migrated down at exit, so
//                        any sim_* + core tables in this DB are
//                        destroyed. Use a dedicated test DB.
//                        Example: postgres://puckdb:foo@localhost:5432/puckdb_integration_test?sslmode=disable
//
// Optional environment variables:
//
//   PUCKDB_TEST_REDIS_ADDR         Redis host:port for ProgressActivities
//                                  state (default localhost:6379, DB 15).
//
//   PUCKDB_TEST_REDIS_PASSWORD     Redis AUTH password. The project's
//                                  docker-compose redis runs with
//                                  --requirepass redis; a plain
//                                  `docker run redis` needs none.
//
//   PUCKDB_TEST_TEMPORAL_HOSTPORT  Existing Temporal server. If set,
//                                  tests dial it instead of spinning up
//                                  an in-process DevServer (which
//                                  downloads the Temporal CLI on first
//                                  run, ~50MB).

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/llm"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/worker/shared"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/worker"
)

// envTestPGURL is the env var the harness reads for the integration
// test Postgres URL. Tests skip when unset rather than failing — a
// missing env var means the developer didn't opt into integration
// runs, not that the suite is broken.
const envTestPGURL = "PUCKDB_TEST_PG_URL"

// integrationEnv is the per-process state TestMain sets up: a
// migrated DB, a pgx pool, and the URL (kept for the migrate-down
// at exit).
var integrationEnv struct {
	pool  *pgxpool.Pool
	dbURL string
}

// TestMain brings the schema up before the suite runs and tears it
// down at exit. Skips entirely when PUCKDB_TEST_PG_URL is unset;
// individual tests then short-circuit via integrationEnv.pool == nil.
//
// "Tear down" means migrating every migration back down to zero —
// the test DB ends each run empty. Slower than per-test TRUNCATE but
// it guarantees migration-down paths are exercised too.
func TestMain(m *testing.M) {
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		// Unset → skip the integration suite. Tests will short-
		// circuit via t.Skip when integrationEnv.pool is nil.
		os.Exit(m.Run())
	}
	integrationEnv.dbURL = dbURL

	if err := database.MigrateDown(dbURL); err != nil {
		// Down-then-up: any leftover schema from a previously-aborted
		// run gets cleared first. Errors here are non-fatal — a
		// genuinely empty DB will warn but still run up cleanly.
		fmt.Fprintf(os.Stderr, "warn: pre-test MigrateDown: %v\n", err)
	}
	if err := database.MigrateUp(dbURL); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: MigrateUp(%q): %v\n", dbURL, err)
		os.Exit(1)
	}

	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fatal: pgxpool.New(%q): %v\n", dbURL, err)
		os.Exit(1)
	}
	integrationEnv.pool = pool

	code := m.Run()

	pool.Close()
	if err := database.MigrateDown(dbURL); err != nil {
		fmt.Fprintf(os.Stderr, "warn: post-test MigrateDown: %v\n", err)
	}
	os.Exit(code)
}

// requireIntegrationEnv is the per-test guard — skips the test when
// the suite-level setup didn't run. Returns the prepared pgx pool
// the test should use.
func requireIntegrationEnv(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if integrationEnv.pool == nil {
		t.Skipf("set %s to run integration tests (e.g. postgres://puckdb:foo@localhost:5432/puckdb_integration_test?sslmode=disable)", envTestPGURL)
	}
	return integrationEnv.pool
}

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
// PUCKDB_TEST_REDIS_PASSWORD is optional — the project's
// docker-compose redis runs with --requirepass redis, so runs against
// the dev stack need it set.
func connectRedisForTest(t *testing.T) *redis.Client {
	t.Helper()
	addr := os.Getenv("PUCKDB_TEST_REDIS_ADDR")
	if addr == "" {
		addr = "localhost:6379"
	}
	c := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: os.Getenv("PUCKDB_TEST_REDIS_PASSWORD"),
		DB:       15,
	})
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

// setupIntegrationWorker registers all sim activities + the workflow
// on a real Temporal worker with a mock AgentFactory. Returned
// cleanup stops the worker.
func setupIntegrationWorker(t *testing.T, tc client.Client, pgPool *pgxpool.Pool, redisClient *redis.Client, agentFactory AgentFactory) func() {
	t.Helper()
	return setupWorkerWithActivities(t, tc, pgPool, redisClient, agentFactory, nil)
}

// setupLiveIntegrationWorker is the livellm variant: no factory
// override — the production defaultAgentFactory builds real Agents
// from the given provider configs (NewAgent → real HTTP clients).
func setupLiveIntegrationWorker(t *testing.T, tc client.Client, pgPool *pgxpool.Pool, redisClient *redis.Client, providers map[llm.Provider]llm.ProviderConfig) func() {
	t.Helper()
	return setupWorkerWithActivities(t, tc, pgPool, redisClient, nil, providers)
}

func setupWorkerWithActivities(t *testing.T, tc client.Client, pgPool *pgxpool.Pool, redisClient *redis.Client, agentFactory AgentFactory, providers map[llm.Provider]llm.ProviderConfig) func() {
	t.Helper()
	acts := &Activities{
		Queries:         sqlcdb.New(pgPool),
		Tx:              NewPgxTransactor(pgPool),
		Signaler:        NewTemporalSignaler(tc),
		AgentFactory:    agentFactory,
		ProviderConfigs: providers,
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

// runWorkflowToCompletion starts SimPoolWorkflow with the given pool
// and waits up to timeout for it to finish. Fails the test on timeout.
//
// A fresh start carries only the PoolID: SimDate/DayCount are unset
// (the workflow loads the season start itself); the day loop runs to
// the season end (or PoolConfig.MaxSeasonDays / StopAfter boundary).
func runWorkflowToCompletion(t *testing.T, ctx context.Context, tc client.Client, poolID int32, timeout time.Duration) {
	t.Helper()
	wfID := simPoolWorkflowIDForPoolStr(poolID)
	run, err := tc.ExecuteWorkflow(ctx,
		client.StartWorkflowOptions{ID: wfID, TaskQueue: "puckdb-tasks"},
		SimPoolWorkflow,
		SimPoolWorkflowInput{PoolID: poolID},
	)
	require.NoError(t, err)
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if err := run.Get(waitCtx, nil); err != nil {
		t.Fatalf("workflow %s run.Get: %v", wfID, err)
	}
}

// simPoolWorkflowIDForPoolStr returns "sim-pool-{id}" — duplicates
// the helper in graph/simulation_helpers.go but accessible from
// here without a package import.
func simPoolWorkflowIDForPoolStr(poolID int32) string {
	return "sim-pool-" + strconv.FormatInt(int64(poolID), 10)
}

// ============================================================================
// Row-level seed primitives
// ============================================================================

// seedSeason inserts one minimal seasons row so the FK from
// sim_pools.season can satisfy. Idempotent — a duplicate insert is
// silently swallowed since later tests may reuse the same season.
// The dates are placeholder bounds; tests narrow the simulated
// window per pool via sim_pools.start_date / end_date.
func seedSeason(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int32) {
	t.Helper()
	seedSeasonWithBounds(t, ctx, pool, id, "2024-10-01", "2025-04-15")
}

// seedSeasonWithBounds is seedSeason with explicit standings bounds —
// used when a test's game window lies outside the default placeholder
// range (pool start_date/end_date must fall within the season bounds).
func seedSeasonWithBounds(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int32, start, end string) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO seasons (id, standings_start, standings_end)
		 VALUES ($1, $2::DATE, $3::DATE)
		 ON CONFLICT (id) DO NOTHING`,
		id, start, end)
	require.NoError(t, err, "seed seasons row")
}

// mustPgDate converts a YYYY-MM-DD string to a Valid pgtype.Date. Panics
// on bad input — test fixture dates are literals.
func mustPgDate(s string) pgtype.Date {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return pgtype.Date{Time: t, Valid: true}
}

// seedPlayer inserts a minimal players row so sim_rosters.player_id
// has a valid FK target.
func seedPlayer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64, position sqlcdb.PlayerPosition) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO players (id, first_name, last_name, first_name_normalized, last_name_normalized, position, is_active)
		 VALUES ($1, 'First', $2, 'first', $2, $3, true)
		 ON CONFLICT (id) DO NOTHING`,
		id, fmt.Sprintf("Player%d", id), position)
	require.NoError(t, err, "seed players row id=%d", id)
}

// seedTeam inserts a season_teams row so games + stats joins resolve.
// Idempotent on (season, team_id). franchise_id is a real FK to
// franchises so it stays NULL (no franchises seeded).
func seedTeam(t *testing.T, ctx context.Context, pool *pgxpool.Pool, season int32, teamID int64, abbrev string) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO season_teams (season, team_id, full_name, abbrev, division_name, division_abbrev)
		VALUES ($1, $2, $3, $3, '', '')
		ON CONFLICT DO NOTHING
	`, season, teamID, abbrev)
	require.NoErrorf(t, err, "seed season_team (%d, %d)", season, teamID)
}

// seedClubSkaterStats puts one row in club_skater_stats so
// LoadDraftCandidates' GetClubSkaterStatsBySeason returns a real
// candidate. Goals + assists drive the prior-season ranking; the sum
// must be > 0 — loadSkaterCandidates skips zero-score players.
func seedClubSkaterStats(t *testing.T, ctx context.Context, pool *pgxpool.Pool, season int32, playerID int64, teamID int64, goals, assists int) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO club_skater_stats (season, game_type, team_id, player_id, games_played, goals, assists, points, plus_minus, penalty_minutes, power_play_goals, shorthanded_goals, game_winning_goals, overtime_goals, shots, shooting_pctg, avg_toi_per_game, avg_shifts_per_game, faceoff_win_pctg)
		VALUES ($1, 'regular_season', $2, $3, 82, $4, $5, $4::INT + $5::INT, 0, 10, 0, 0, 0, 0, 0, 0, 0, 0, 0)
		ON CONFLICT DO NOTHING
	`, season, teamID, playerID, goals, assists)
	require.NoErrorf(t, err, "seed club_skater_stats player %d", playerID)
}

// seedClubGoalieStats — goalie equivalent. Wins drive the ranking and
// must be > 0 (zero-score goalies are skipped as draft candidates).
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
		INSERT INTO games (id, season, game_type, game_date, game_state, home_team_id, away_team_id, home_team_score, away_team_score, period_number, venue, start_time_utc)
		VALUES ($1, $2, 'regular_season', $3::DATE, 'FINAL', $4, $5, 3, 2, 3, '', NOW())
		ON CONFLICT DO NOTHING
	`, gameID, season, gameDate, homeID, awayID)
	require.NoErrorf(t, err, "seed game %d", gameID)
}

// skaterLine is one skater's stat line for one game, covering every
// column the six skater scoring categories read (G, A, +/-, PIM,
// PPP, SOG — see summarizeSkater).
type skaterLine struct {
	Goals     int
	Assists   int
	PlusMinus int
	PIM       int
	PPP       int
	SOG       int
}

// goalieLine is one goalie's stat line for one game, covering the
// three goalie categories (W, GA, GAA components — see
// summarizeGoalie).
type goalieLine struct {
	Win          bool
	GoalsAgainst int
	Saves        int
	TOISeconds   int
}

// seedGameSkaterStatsLine inserts a per-game skater stat row from a
// full skaterLine. CollectDayStats reads these to project onto the
// agent's roster.
func seedGameSkaterStatsLine(t *testing.T, ctx context.Context, pool *pgxpool.Pool, gameID, playerID, teamID int64, position sqlcdb.PlayerPosition, line skaterLine) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO game_skater_stats (game_id, player_id, team_id, is_home, sweater_number, position, goals, assists, points, plus_minus, shots_on_goal, toi_seconds, shifts, faceoff_winning_pctg, hits, blocked_shots, penalty_minutes, giveaways, takeaways, power_play_goals, power_play_points, game_winning_goals, ot_goals)
		VALUES ($1, $2, $3, true, 99, $4, $5, $6, $5::SMALLINT + $6::SMALLINT, $7, $8, 1200, 20, NULL, 1, 1, $9, 0, 0, 0, $10, 0, 0)
		ON CONFLICT DO NOTHING
	`, gameID, playerID, teamID, position, line.Goals, line.Assists, line.PlusMinus, line.SOG, line.PIM, line.PPP)
	require.NoErrorf(t, err, "seed game_skater_stats game=%d player=%d", gameID, playerID)
}

// seedGameSkaterStats is the goals/assists-only shorthand the 2-agent
// scenario tests use; other stats get fixed plausible values.
func seedGameSkaterStats(t *testing.T, ctx context.Context, pool *pgxpool.Pool, gameID, playerID, teamID int64, goals, assists int) {
	t.Helper()
	seedGameSkaterStatsLine(t, ctx, pool, gameID, playerID, teamID, sqlcdb.PlayerPositionC, skaterLine{
		Goals:   goals,
		Assists: assists,
		SOG:     3,
	})
}

// seedGameGoalieStats inserts a per-game goalie stat row. decision is
// 'W' for a win, 'L' otherwise (summarizeGoalie counts only exact 'W'
// rows toward the W category).
func seedGameGoalieStats(t *testing.T, ctx context.Context, pool *pgxpool.Pool, gameID, playerID, teamID int64, line goalieLine) {
	t.Helper()
	decision := "L"
	if line.Win {
		decision = "W"
	}
	_, err := pool.Exec(ctx, `
		INSERT INTO game_goalie_stats (game_id, player_id, team_id, is_home, sweater_number, decision, starter, shots_against, saves, save_pctg, goals_against, toi_seconds)
		VALUES ($1, $2, $3, true, 30, $4, true, $5::INT + $6::INT, $5, NULL, $6, $7)
		ON CONFLICT DO NOTHING
	`, gameID, playerID, teamID, decision, line.Saves, line.GoalsAgainst, line.TOISeconds)
	require.NoErrorf(t, err, "seed game_goalie_stats game=%d player=%d", gameID, playerID)
}

// offsetDate returns startDate + n days as a YYYY-MM-DD string.
// Avoids dragging time-arithmetic into test bodies.
func offsetDate(startDate string, n int) string {
	t, err := time.Parse("2006-01-02", startDate)
	if err != nil {
		panic(err)
	}
	return t.AddDate(0, 0, n).Format("2006-01-02")
}

// ============================================================================
// seedSmallSeason — parameterized N-team / D-day scenario builder
// ============================================================================

// ID bases keep seedSmallSeason's rows clear of the hand-rolled
// constants in the 2-agent scenario tests (teams 4001-4002, players
// 5001-5004, games 6001-6005) — the suite shares one DB.
const (
	smallSeasonTeamIDBase   int64 = 4100
	smallSeasonPlayerIDBase int64 = 7000
	smallSeasonGameIDBase   int64 = 8000
)

// smallSeasonConfig parameterizes seedSmallSeason. All counts must be
// positive; GamesPerDay*2 must not exceed NumTeams (a team plays at
// most one game per day).
//
// IDOffset shifts every generated team/player/game ID. Tests sharing
// the suite DB MUST use distinct offsets (inserts are ON CONFLICT DO
// NOTHING, so an ID collision silently keeps the OTHER test's row).
type smallSeasonConfig struct {
	Season         int32
	StartDate      string // YYYY-MM-DD — first day with games
	Days           int    // calendar days with games
	GamesPerDay    int
	NumTeams       int
	SkatersPerTeam int // positions cycle C, LW, RW, D
	GoaliesPerTeam int
	Seed           int64 // deterministic rand for stat lines
	IDOffset       int64
}

// seededPlayer identifies one synthetic player.
type seededPlayer struct {
	ID       int64
	TeamID   int64
	Position sqlcdb.PlayerPosition
}

// smallSeasonData is seedSmallSeason's return value: every ID it
// created plus the exact per-date stat lines it wrote, so tests can
// recompute expected attribution Go-side without re-querying the
// seeded tables.
//
// SkaterLines / GoalieLines are keyed date (YYYY-MM-DD) → player ID.
// A player appears under a date iff their team played that day.
type smallSeasonData struct {
	Config      smallSeasonConfig
	TeamIDs     []int64
	Skaters     []seededPlayer
	Goalies     []seededPlayer
	GameIDs     []int64
	SkaterLines map[string]map[int64]skaterLine
	GoalieLines map[string]map[int64]goalieLine
}

// PlayersByTeam returns the skaters + goalies rostered on teamID.
func (d *smallSeasonData) PlayersByTeam(teamID int64) []seededPlayer {
	var out []seededPlayer
	for _, p := range d.Skaters {
		if p.TeamID == teamID {
			out = append(out, p)
		}
	}
	for _, p := range d.Goalies {
		if p.TeamID == teamID {
			out = append(out, p)
		}
	}
	return out
}

// seedSmallSeason builds a complete synthetic small season:
//
//   - seasons rows for cfg.Season AND its prior season (the draft
//     reads prior-season club stats; scoring reads cfg.Season games)
//   - NumTeams season_teams rows in both seasons
//   - SkatersPerTeam skaters (positions cycling C/LW/RW/D) and
//     GoaliesPerTeam goalies per team
//   - prior-season club_skater_stats / club_goalie_stats for EVERY
//     player, with strictly positive, strictly decreasing scores so
//     the draft ranking is total and deterministic
//   - Days × GamesPerDay FINAL regular-season games starting at
//     StartDate, teams rotating so matchups vary; every skater on
//     both teams gets a deterministic (cfg.Seed) stat line, and one
//     goalie per team (rotating by day) gets a goalie line — home
//     goalie records the W, matching seedGame's 3-2 home score
//
// Draft-starvation guard: the caller states how many players a full
// draft consumes (draftRounds × numAgents); this function fails the
// test up front if the seeded candidate pool is smaller. Compute it
// from the pool config, never hardcode (plan: "the seed helper must
// compute the candidate count from the config").
func seedSmallSeason(t *testing.T, ctx context.Context, pool *pgxpool.Pool, cfg smallSeasonConfig, draftDemand int) *smallSeasonData {
	t.Helper()

	require.Positive(t, cfg.Days, "smallSeasonConfig.Days")
	require.Positive(t, cfg.GamesPerDay, "smallSeasonConfig.GamesPerDay")
	require.Positive(t, cfg.SkatersPerTeam, "smallSeasonConfig.SkatersPerTeam")
	require.Positive(t, cfg.GoaliesPerTeam, "smallSeasonConfig.GoaliesPerTeam")
	require.LessOrEqual(t, cfg.GamesPerDay*2, cfg.NumTeams,
		"GamesPerDay*2 must be <= NumTeams (a team plays at most one game per day)")

	totalPlayers := cfg.NumTeams * (cfg.SkatersPerTeam + cfg.GoaliesPerTeam)
	require.GreaterOrEqual(t, totalPlayers, draftDemand,
		"seeded candidate pool (%d players) smaller than the draft demand (%d) — the draft would starve",
		totalPlayers, draftDemand)

	priorSeason := cfg.Season - 10001
	// The test season's standings bounds cover a generous year from
	// the configured start so per-pool start_date/end_date windows
	// always validate as in-range. The prior season only feeds the
	// draft ranking — placeholder bounds suffice.
	seedSeasonWithBounds(t, ctx, pool, cfg.Season, cfg.StartDate, offsetDate(cfg.StartDate, 364))
	seedSeason(t, ctx, pool, priorSeason)

	data := &smallSeasonData{
		Config:      cfg,
		SkaterLines: map[string]map[int64]skaterLine{},
		GoalieLines: map[string]map[int64]goalieLine{},
	}

	// Teams — in both seasons (club stats FK on (season, team_id)).
	for i := 0; i < cfg.NumTeams; i++ {
		teamID := smallSeasonTeamIDBase + cfg.IDOffset + int64(i)
		abbrev := fmt.Sprintf("T%02d", i)
		seedTeam(t, ctx, pool, cfg.Season, teamID, abbrev)
		seedTeam(t, ctx, pool, priorSeason, teamID, abbrev)
		data.TeamIDs = append(data.TeamIDs, teamID)
	}

	// Players + prior-season club stats. Scores are strictly positive
	// (zero-score candidates are dropped by loadSkaterCandidates /
	// loadGoalieCandidates) and strictly decreasing by player index so
	// the pre-draft ranking has no ties.
	skaterPositions := []sqlcdb.PlayerPosition{
		sqlcdb.PlayerPositionC,
		sqlcdb.PlayerPositionLW,
		sqlcdb.PlayerPositionRW,
		sqlcdb.PlayerPositionD,
	}
	nextPlayerID := smallSeasonPlayerIDBase + cfg.IDOffset
	nSkaters := cfg.NumTeams * cfg.SkatersPerTeam
	for i := 0; i < cfg.NumTeams; i++ {
		teamID := data.TeamIDs[i]
		for j := 0; j < cfg.SkatersPerTeam; j++ {
			pos := skaterPositions[j%len(skaterPositions)]
			p := seededPlayer{ID: nextPlayerID, TeamID: teamID, Position: pos}
			nextPlayerID++
			seedPlayer(t, ctx, pool, p.ID, p.Position)
			// Rank score: strictly decreasing with global skater index.
			rank := len(data.Skaters)
			seedClubSkaterStats(t, ctx, pool, priorSeason, p.ID, teamID, nSkaters-rank+10, nSkaters-rank+5)
			data.Skaters = append(data.Skaters, p)
		}
		for j := 0; j < cfg.GoaliesPerTeam; j++ {
			p := seededPlayer{ID: nextPlayerID, TeamID: teamID, Position: sqlcdb.PlayerPositionG}
			nextPlayerID++
			seedPlayer(t, ctx, pool, p.ID, p.Position)
			rank := len(data.Goalies)
			seedClubGoalieStats(t, ctx, pool, priorSeason, p.ID, teamID, cfg.NumTeams*cfg.GoaliesPerTeam-rank+5)
			data.Goalies = append(data.Goalies, p)
		}
	}

	// Index players by team for the per-game stat-line loop.
	skatersByTeam := map[int64][]seededPlayer{}
	for _, p := range data.Skaters {
		skatersByTeam[p.TeamID] = append(skatersByTeam[p.TeamID], p)
	}
	goaliesByTeam := map[int64][]seededPlayer{}
	for _, p := range data.Goalies {
		goaliesByTeam[p.TeamID] = append(goaliesByTeam[p.TeamID], p)
	}

	// Games + stat lines. Deterministic rand: same cfg.Seed → same
	// lines → tests can be re-run byte-identically.
	rng := rand.New(rand.NewSource(cfg.Seed))
	nextGameID := smallSeasonGameIDBase + cfg.IDOffset
	for d := 0; d < cfg.Days; d++ {
		date := offsetDate(cfg.StartDate, d)
		data.SkaterLines[date] = map[int64]skaterLine{}
		data.GoalieLines[date] = map[int64]goalieLine{}
		for g := 0; g < cfg.GamesPerDay; g++ {
			// Rotate pairings by day so matchups vary; (2g+d, 2g+1+d)
			// mod NumTeams are always distinct and no team appears in
			// two games on one day (guaranteed by GamesPerDay*2 <=
			// NumTeams).
			home := data.TeamIDs[(2*g+d)%cfg.NumTeams]
			away := data.TeamIDs[(2*g+1+d)%cfg.NumTeams]
			gameID := nextGameID
			nextGameID++
			seedGame(t, ctx, pool, gameID, cfg.Season, date, home, away)
			data.GameIDs = append(data.GameIDs, gameID)

			for _, teamID := range []int64{home, away} {
				for _, p := range skatersByTeam[teamID] {
					line := skaterLine{
						Goals:     rng.Intn(3),
						Assists:   rng.Intn(3),
						PlusMinus: rng.Intn(5) - 2,
						PIM:       2 * rng.Intn(2),
						PPP:       rng.Intn(2),
						SOG:       1 + rng.Intn(5),
					}
					seedGameSkaterStatsLine(t, ctx, pool, gameID, p.ID, teamID, p.Position, line)
					data.SkaterLines[date][p.ID] = line
				}
				// One goalie per team plays, rotating by day so every
				// seeded goalie eventually accrues stats.
				goalies := goaliesByTeam[teamID]
				goalie := goalies[d%len(goalies)]
				line := goalieLine{
					Win:          teamID == home, // home wins 3-2 per seedGame
					GoalsAgainst: rng.Intn(4),
					Saves:        20 + rng.Intn(15),
					TOISeconds:   3600,
				}
				seedGameGoalieStats(t, ctx, pool, gameID, goalie.ID, teamID, line)
				data.GoalieLines[date][goalie.ID] = line
			}
		}
	}

	return data
}

// seedWaiverPriority inserts one sim_waiver_priority row. NOTE:
// production code never populates this table (InsertSimWaiverPriority
// has no callers — the migration comment says "initialized as reverse
// draft order" but nothing does it), so ProcessWaivers ranks every
// claimant at missingWaiverPriority and falls back to claim-ID order.
// Tests seed explicit priorities so the priority-resolution path is
// exercised deterministically.
func seedWaiverPriority(t *testing.T, ctx context.Context, pool *pgxpool.Pool, poolID, agentID int32, priority int32) {
	t.Helper()
	_, err := pool.Exec(ctx, `
		INSERT INTO sim_waiver_priority (pool_id, agent_id, priority)
		VALUES ($1, $2, $3)
		ON CONFLICT (pool_id, agent_id) DO UPDATE SET priority = EXCLUDED.priority
	`, poolID, agentID, priority)
	require.NoErrorf(t, err, "seed waiver priority agent=%d", agentID)
}

// assertPoolCompleted verifies the workflow's Phase 3 wrote
// status='complete' via SetPoolStatusActivity. Catches a class of
// bugs where the day loop exits without going through Phase 3.
func assertPoolCompleted(t *testing.T, ctx context.Context, pgPool *pgxpool.Pool, poolID int32) {
	t.Helper()
	var status string
	err := pgPool.QueryRow(ctx, `SELECT status FROM sim_pools WHERE id = $1`, poolID).Scan(&status)
	require.NoError(t, err)
	require.Equal(t, string(PoolStatusComplete), status)
}

// dumpTurnTelemetry logs every non-accepted tool call and every
// errored turn for the pool — the first thing to read when a scripted
// scenario's end state doesn't match. Call it on failure paths.
func dumpTurnTelemetry(t *testing.T, ctx context.Context, pool *pgxpool.Pool, poolID int32) {
	t.Helper()
	rows, err := pool.Query(ctx, `
		SELECT tn.agent_id, tn.phase, COALESCE(tn.sim_date::TEXT, ''), tn.status,
		       COALESCE(tn.error_detail, ''), COALESCE(tc.tool_name, ''),
		       COALESCE(tc.outcome, ''), COALESCE(tc.failure_reason, ''),
		       COALESCE(tc.arguments_raw, ''), COALESCE(tc.result, '')
		FROM sim_agent_turns tn
		LEFT JOIN sim_agent_tool_calls tc ON tc.turn_id = tn.id
		WHERE tn.pool_id = $1
		  AND (tn.status <> 'ok' OR (tc.outcome IS NOT NULL AND tc.outcome <> 'accepted'))
		ORDER BY tn.sim_date NULLS FIRST, tn.agent_id
	`, poolID)
	if err != nil {
		t.Logf("dumpTurnTelemetry query failed: %v", err)
		return
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var agentID int32
		var phase, date, status, errDetail, toolName, outcome, failureReason, argsRaw, result string
		if err := rows.Scan(&agentID, &phase, &date, &status, &errDetail,
			&toolName, &outcome, &failureReason, &argsRaw, &result); err != nil {
			t.Logf("dumpTurnTelemetry scan failed: %v", err)
			return
		}
		t.Logf("turn agent=%d phase=%s date=%s status=%s err=%q | tool=%s outcome=%s failure=%q args=%s result=%q",
			agentID, phase, date, status, errDetail, toolName, outcome, failureReason, argsRaw, result)
		n++
	}
	if n == 0 {
		t.Logf("dumpTurnTelemetry: no non-accepted tool calls / errored turns for pool %d", poolID)
	}
}

// ============================================================================
// Numeric conversion helpers
// ============================================================================

// pgNumericFromFloat / pgFloatFromNumeric mirror the simulation
// package's numericFromFloat / numericToFloat helpers but live in
// the test namespace so integration tests can be read independently.
func pgNumericFromFloat(f float64) (pgtype.Numeric, error) {
	var n pgtype.Numeric
	if err := n.Scan(formatFloat(f)); err != nil {
		return pgtype.Numeric{}, err
	}
	return n, nil
}

func pgFloatFromNumeric(n pgtype.Numeric) (float64, error) {
	if !n.Valid {
		return 0, nil
	}
	f, err := n.Float64Value()
	if err != nil {
		return 0, err
	}
	return f.Float64, nil
}

// formatFloat formats f as a fixed-precision string suitable for
// pgtype.Numeric.Scan.
func formatFloat(f float64) string {
	return fmt.Sprintf("%.6f", f)
}
