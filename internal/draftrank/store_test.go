package draftrank_test

// PostgreSQL-backed tests of snapshot storage, refresh attempts and the
// refresh pipeline's failure states. They skip unless PUCKDB_TEST_PG_URL
// names a test database (see CLAUDE.md "Database-backed tests").

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sperano/puckdb/internal/draftrank"
	"github.com/sperano/puckdb/internal/fixtures/draftfixtures"
	"github.com/sperano/puckdb/internal/fixtures/pgtest"
	"github.com/sperano/puckdb/internal/newsadjust"
	"github.com/sperano/puckdb/internal/projection"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testKeep  = 2
	testRunID = "run-1"
)

// openDraftTestDB opens the test database with no draft ranking, news
// adjustment, projection or league rule rows.
func openDraftTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	return pgtest.Open(t, "draft_ranking_refreshes", "draft_ranking_snapshots", "news_adjustment_overrides",
		"news_adjustment_runs", "projection_snapshots", "yahoo_league_rule_snapshots", "yahoo_league_players")
}

// storedFixtureInput stores the fixture's baseline projection and news
// adjustment so a snapshot built from it can reference them.
func storedFixtureInput(t *testing.T, pool *pgxpool.Pool) draftrank.BuildInput {
	t.Helper()
	ctx := context.Background()
	input := fixtureInput(t)
	baselineID, err := projection.StoreSnapshotWith(ctx, sqlcdb.New(pool), input.Baseline)
	require.NoError(t, err)
	runID, err := newsadjust.NewPGStore(pool).SaveRun(ctx, baselineID, *input.Adjustment)
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
	require.NoError(t, newsadjust.NewPGStore(pool).CreateOverride(ctx, newsadjust.Override{
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

// TestPGStore_ResolvesEveryLeagueReferenceForm resolves the imported fixture
// league through the shared rule-snapshot query: a full key in any season, a
// numeric ID only in its season, and a missing league as ErrUnknownLeague.
func TestPGStore_ResolvesEveryLeagueReferenceForm(t *testing.T) {
	pool := openDraftTestDB(t)
	seedLeague(t, pool, seed{rules: true})
	store := draftrank.NewPGStore(pool)
	ctx := context.Background()

	season, leagueID, err := store.FindLeagueKey(ctx, draftfixtures.LeagueKey)
	require.NoError(t, err)
	assert.Equal(t, draftfixtures.Season, season)
	assert.Equal(t, draftfixtures.LeagueID, leagueID)
	_, _, err = store.FindLeagueKey(ctx, "465.l.1002")
	assert.ErrorIs(t, err, draftrank.ErrUnknownLeague)

	svc := draftrank.NewService(store, nil, draftrank.ServiceOptions{})
	want := draftrank.LeagueRef{Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID, LeagueKey: draftfixtures.LeagueKey}
	for name, ref := range map[string]draftrank.LeagueRef{
		"full key":               {LeagueKey: draftfixtures.LeagueKey},
		"full key, other season": {LeagueKey: draftfixtures.LeagueKey, Season: draftfixtures.Season + 1},
		"numeric ID with season": {Season: draftfixtures.Season, LeagueID: draftfixtures.LeagueID},
	} {
		got, err := svc.ResolveLeague(ctx, ref)
		require.NoError(t, err, name)
		assert.Equal(t, want, got, name)
	}
	for name, ref := range map[string]draftrank.LeagueRef{
		"missing full key":         {LeagueKey: "465.l.1002"},
		"numeric ID, other season": {Season: draftfixtures.Season + 1, LeagueID: draftfixtures.LeagueID},
		"missing numeric ID":       {Season: draftfixtures.Season, LeagueID: draftfixtures.OtherLeague},
		"numeric ID, no season":    {LeagueID: draftfixtures.LeagueID},
	} {
		_, err := svc.ResolveLeague(ctx, ref)
		assert.ErrorIs(t, err, draftrank.ErrUnknownLeague, name)
	}
}
