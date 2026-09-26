package draftrank_test

// PostgreSQL-backed tests of snapshot storage, refresh attempts and the
// refresh pipeline's failure states. They skip unless PUCKDB_TEST_PG_URL
// names a test database (see CLAUDE.md "Database-backed tests").

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/database"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	envTestPGURL     = "PUCKDB_TEST_PG_URL"
	testDBNameMarker = "test"
	testKeep         = 2
	testRunID        = "run-1"
)

var pgMigrateOnce struct {
	sync.Once
	err error
}

func openDraftTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv(envTestPGURL)
	if dbURL == "" {
		t.Skipf("set %s to run the PostgreSQL draft ranking tests", envTestPGURL)
	}
	require.Contains(t, dbURL, testDBNameMarker,
		"%s must name a dedicated test database (URL containing %q)", envTestPGURL, testDBNameMarker)
	pgMigrateOnce.Do(func() { pgMigrateOnce.err = database.MigrateUp(dbURL) })
	require.NoError(t, pgMigrateOnce.err, "migrate test database")
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	require.NoError(t, err)
	t.Cleanup(pool.Close)
	_, err = pool.Exec(ctx, `TRUNCATE draft_ranking_refreshes, draft_ranking_snapshots, news_adjustment_overrides,
		news_adjustment_runs, projection_snapshots, yahoo_league_rule_snapshots, yahoo_league_players CASCADE`)
	require.NoError(t, err)
	return pool
}

// storedFixtureInput stores the fixture's baseline projection and news
// adjustment so a snapshot built from it can reference them.
func storedFixtureInput(t *testing.T, pool *pgxpool.Pool) draftrank.BuildInput {
	t.Helper()
	ctx := context.Background()
	input := fixtureInput(t)
	baselineID, err := projection.StoreSnapshotWith(ctx, sqlcdb.New(pool), input.Baseline)
	require.NoError(t, err)
	runID, err := newsadjust.NewRepository(pool).SaveRun(ctx, baselineID, *input.Adjustment)
	require.NoError(t, err)
	input.BaselineID, input.AdjustmentRunID = baselineID, runID
	return input
}

func TestPGStore_SnapshotRoundTripAndPruning(t *testing.T) {
	pool := openDraftTestDB(t)
	store := draftrank.NewPGStore(pool)
	ctx := context.Background()
	input := storedFixtureInput(t, pool)

	var ids []uuid.UUID
	var last draftrank.Snapshot
	for i := range testKeep + 1 {
		input.AsOf = draftfixtures.AsOf.Add(time.Duration(i) * time.Hour)
		snapshot, err := draftrank.Build(input)
		require.NoError(t, err)
		id, err := store.SaveSnapshot(ctx, snapshot, testKeep)
		require.NoError(t, err)
		ids, last = append(ids, id), snapshot
	}
	latest, err := store.LatestSnapshotID(ctx, draftfixtures.Season, draftfixtures.LeagueID)
	require.NoError(t, err)
	assert.Equal(t, ids[len(ids)-1], latest)
	_, err = store.LoadSnapshot(ctx, ids[0])
	assert.ErrorIs(t, err, draftrank.ErrSnapshotNotFound, "the oldest snapshot beyond keep was pruned")

	loaded, err := store.LoadSnapshot(ctx, latest)
	require.NoError(t, err)
	assert.Equal(t, last.Identity, loaded.Identity)
	assert.Equal(t, last.Meta, loaded.Meta)
	assert.Equal(t, last.Players, loaded.Players, "players round-trip with exact values")
	assert.Equal(t, last.AsOf, loaded.AsOf)
	assert.False(t, loaded.CreatedAt.IsZero())
}

func TestPGStore_RefreshAttempts(t *testing.T) {
	store := draftrank.NewPGStore(openDraftTestDB(t))
	ctx := context.Background()
	started := draftfixtures.AsOf

	id, err := store.StartRefresh(ctx, testRunID, draftfixtures.Season, draftfixtures.LeagueID, started)
	require.NoError(t, err)
	require.NoError(t, store.FinishRefresh(ctx, id, draftrank.Refresh{
		State: draftrank.RefreshFailed, Code: draftrank.IssueMissingPool, Error: "no players", FinishedAt: started.Add(time.Minute),
	}, draftfixtures.LeagueKey))
	latest, err := store.LatestRefresh(ctx, draftfixtures.Season, draftfixtures.LeagueID)
	require.NoError(t, err)
	assert.Equal(t, draftrank.RefreshFailed, latest.State)
	assert.Equal(t, draftrank.IssueMissingPool, latest.Code)

	retried, err := store.StartRefresh(ctx, testRunID, draftfixtures.Season, draftfixtures.LeagueID, started.Add(time.Hour))
	require.NoError(t, err)
	assert.Equal(t, id, retried, "a retried activity of the same run reuses its attempt")
	canceled, err := store.CancelRefreshes(ctx, testRunID, started.Add(2*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, canceled)
	latest, err = store.LatestRefresh(ctx, draftfixtures.Season, draftfixtures.LeagueID)
	require.NoError(t, err)
	assert.Equal(t, draftrank.RefreshCanceled, latest.State)

	none, err := store.LatestRefresh(ctx, draftfixtures.Season, draftfixtures.OtherLeague)
	require.NoError(t, err)
	assert.Nil(t, none)
}

func TestPGStore_OverrideChangesSinceSnapshot(t *testing.T) {
	pool := openDraftTestDB(t)
	store := draftrank.NewPGStore(pool)
	ctx := context.Background()
	since := draftfixtures.AsOf
	require.NoError(t, newsadjust.NewRepository(pool).CreateOverride(ctx, newsadjust.Override{
		ID: "o1", PlayerKey: draftfixtures.TopCenter, Kind: newsadjust.OverrideMissedGames, Value: 5,
		Reason: "appeal", CreatedAt: since.Add(time.Hour),
	}))
	changes, err := store.OverrideChanges(ctx, draftfixtures.LeagueKey, since, since.Add(2*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, changes, "an all-league override counts for every league")
	changes, err = store.OverrideChanges(ctx, draftfixtures.LeagueKey, since.Add(90*time.Minute), since.Add(2*time.Hour))
	require.NoError(t, err)
	assert.Zero(t, changes)
}
