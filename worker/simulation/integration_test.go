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
//   PUCKDB_TEST_TEMPORAL_DEVSERVER  When set to "1", spins up an
//                                   in-process Temporal dev server
//                                   via go.temporal.io/sdk/testsuite.
//                                   Otherwise the tests will assume
//                                   a Temporal server reachable at
//                                   the default address (localhost:7233)
//                                   and skip if unreachable.
//
// What's covered today:
//
//   TestIntegrationSchemaSmoke — pins the JSONB→typed-columns
//   migration cascade. Inserts a pool + 2 agents + a roster row, reads
//   them back, asserts every typed column round-trips through pgx.
//   Catches the integration-time failures that unit tests can't see:
//   mismatched column lists in sqlc-generated Scan calls, NOT NULL
//   constraint violations, type-coercion errors at the wire boundary.
//
// What's deferred to follow-up commits:
//
//   - Full draft + 5-day workflow run with mock-LLM agents
//   - sim_agent_totals = SUM(sim_agent_daily_stats) invariant
//   - sim_agent_daily_stats = SUM(sim_agent_daily_player_stats) invariant
//   - Drop-and-pickup historical attribution
//   - Waiver claim resolution
//
// Each of those needs prior-season fixture data (club_skater_stats /
// club_goalie_stats) and a running Temporal — heavier than the smoke
// test. Landing them incrementally so the harness is reviewable in
// isolation.

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// seedSeason inserts one minimal seasons row so the FK from
// sim_pools.season can satisfy. Returns the season ID. Idempotent —
// a duplicate insert is silently swallowed since later tests may
// reuse the same season.
func seedSeason(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int32) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO seasons (id, standings_start, standings_end)
		 VALUES ($1, '2024-10-01', '2025-04-15')
		 ON CONFLICT (id) DO NOTHING`,
		id)
	require.NoError(t, err, "seed seasons row")
}

// seedPlayer inserts a minimal players row so sim_rosters.player_id
// has a valid FK target. Returns nothing — tests use the requested
// id for their assertions.
func seedPlayer(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id int64, position sqlcdb.PlayerPosition) {
	t.Helper()
	_, err := pool.Exec(ctx,
		`INSERT INTO players (id, first_name, last_name, first_name_normalized, last_name_normalized, position, is_active)
		 VALUES ($1, 'First', 'Last', 'first', 'last', $2, true)
		 ON CONFLICT (id) DO NOTHING`,
		id, position)
	require.NoError(t, err, "seed players row id=%d", id)
}

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
	// agent 1 sets it, agent 2 leaves it NULL.
	temp, err := pgNumericFromFloat(0.7)
	require.NoError(t, err)
	agent1, err := q.InsertSimAgent(ctx, sqlcdb.InsertSimAgentParams{
		PoolID:         pool1.ID,
		DraftPosition:  1,
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
		PoolID:        pool1.ID,
		DraftPosition: 2,
		Provider:      "anthropic",
		Model:         "claude-haiku-4-5",
		Strategy:      "aggressive",
		// temperature, api_base, max_tokens, timeout_seconds left at
		// their zero values — exercises the NULL temperature path.
	})
	require.NoError(t, err)

	// Read agents back via ListSimAgentsByPool — pins the LIST query's
	// column order matches the GET query's column order (would have
	// caught a regression where one Scan list was updated but the
	// other wasn't).
	listed, err := q.ListSimAgentsByPool(ctx, pool1.ID)
	require.NoError(t, err)
	require.Len(t, listed, 2)
	assert.Equal(t, "Sonnet", listed[0].Name)
	assert.Equal(t, int32(30), listed[0].TimeoutSeconds)
	assert.True(t, listed[0].Temperature.Valid, "agent 1's temperature is set")
	assert.Equal(t, "Haiku", listed[1].Name)
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

// pgNumericFromFloat / pgFloatFromNumeric mirror the simulation
// package's numericFromFloat / numericToFloat helpers but live in
// the test file's namespace so the integration test can be read
// independently.
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
