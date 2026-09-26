package newsadjust

// PostgreSQL-backed tests of override history and adjustment runs: the real
// migrations and queries, and a replay that rebuilds a stored run from its
// stored inputs. They skip unless PUCKDB_TEST_PG_URL names a test database
// (see CLAUDE.md "Database-backed tests").

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envTestPGURL     = "PUCKDB_TEST_PG_URL"
	testDBNameMarker = "test"
)

var pgMigrateOnce struct {
	sync.Once
	err error
}

func openAdjustmentTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL news adjustment tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBNameMarker,
		"%s must name a dedicated test database (URL containing %q)", envTestPGURL, testDBNameMarker)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err, "migrate test database")

	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `TRUNCATE news_adjustment_overrides, news_adjustment_runs, projection_snapshots CASCADE`)
	require.NoError(t, err)
	return pool
}

func TestRepository_OverrideHistory(t *testing.T) {
	repo := NewRepository(openAdjustmentTestDB(t))
	ctx := context.Background()
	override := testOverride(NewOverrideID(), OverrideMissedGames, 10)
	override.LeagueKey, override.ExpiresAt = testLeague1001, testOverrideAt.Add(30*24*time.Hour)
	require.NoError(t, repo.CreateOverride(ctx, override))

	resetAt := testOverrideAt.Add(48 * time.Hour)
	require.NoError(t, repo.ResetOverride(ctx, override.ID, resetAt, "suspension confirmed long"))
	assert.ErrorIs(t, repo.ResetOverride(ctx, override.ID, resetAt, "again"), ErrOverrideNotResettable)

	stored, err := repo.ListOverrides(ctx)
	require.NoError(t, err)
	require.Len(t, stored, 1)
	expected := override
	expected.ResetAt, expected.ResetReason = resetAt, "suspension confirmed long"
	assert.Equal(t, expected, stored[0], "a reset keeps the original values")
}

func TestRepository_SaveRunIsIdempotentAndReplays(t *testing.T) {
	pool := openAdjustmentTestDB(t)
	repo := NewRepository(pool)
	ctx := context.Background()
	baseline := testBaseline(testGoalie(testGoalieKey, testGoalieStarts), testSkater(testSkaterKey))
	baselineID, err := projection.StoreSnapshotWith(ctx, sqlcdb.New(pool), baseline)
	require.NoError(t, err)

	override := testOverride(NewOverrideID(), OverrideMissedGames, 20)
	override.Scenario = ScenarioConservative
	require.NoError(t, repo.CreateOverride(ctx, override))
	overrides, err := repo.ListOverrides(ctx)
	require.NoError(t, err)
	newTeam := int64(9)
	trade := roleEvent("trade", testSkaterKey, EventTrade, RoleChange{TeamID: &newTeam, PowerPlay: DirectionUp})
	trade.IncidentID = 5
	req := testRequest(baseline, testEvent("susp", testGoalieKey, EventSuspension, Duration{Kind: DurationIndefinite}), trade)
	req.Overrides, req.CoverageWarnings = overrides, []string{"rotowire-nhl stale"}
	result := mustApply(t, req)

	runID, err := repo.SaveRun(ctx, baselineID, result)
	require.NoError(t, err)
	again, err := repo.SaveRun(ctx, baselineID, result)
	require.NoError(t, err)
	assert.Equal(t, runID, again, "saving the same adjustment again reuses its run")

	require.NoError(t, repo.ResetOverride(ctx, override.ID, testDraftAt.Add(time.Hour), "no longer needed"))
	replayed, err := repo.Replay(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, result.ID, replayed.ID)
	assert.Equal(t, result.Players, replayed.Players, "explanations are reconstructed from stored versions")

	loaded, err := repo.LoadRun(ctx, runID)
	require.NoError(t, err)
	assert.Equal(t, result.Players, loaded.Result.Players)
	assert.Len(t, loaded.Result.Decisions, len(result.Decisions))
	assert.Len(t, loaded.ScenarioSnapshotIDs, len(Scenarios))
}
