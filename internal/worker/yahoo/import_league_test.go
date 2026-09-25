package yahoo

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/go-redis/redismock/v8"
	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/draft"
	"github.com/sperano/puckdb/internal/fixtures/yahoofixtures"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
)

// ────────────────────────────────────────────────────────────────────────────
// checkLeagueIdentity
// ────────────────────────────────────────────────────────────────────────────

const (
	identityTestLeagueID    = 77777
	identityTestSeason      = 2026
	identityTestOtherSeason = 2025
)

func TestCheckLeagueIdentity(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		stored      sqlcdb.YahooLeague
		found       bool
		wantErr     bool
		wantMessage []string
	}{
		{
			name:  "not_found",
			found: false,
		},
		{
			name:   "same_season",
			stored: sqlcdb.YahooLeague{Season: identityTestSeason, LeagueKey: "465.l.77777"},
			found:  true,
		},
		{
			name:        "other_season_collides",
			stored:      sqlcdb.YahooLeague{Season: identityTestOtherSeason, LeagueKey: "453.l.77777"},
			found:       true,
			wantErr:     true,
			wantMessage: []string{"season 2026", "season 2025", "77777"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := checkLeagueIdentity(tt.stored, tt.found, identityTestLeagueID, identityTestSeason)
			if !tt.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, isNonRetryable(err))
			for _, want := range tt.wantMessage {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

// ────────────────────────────────────────────────────────────────────────────
// ImportYahooLeague reads storage directly, bypassing the Redis gob cache
// ────────────────────────────────────────────────────────────────────────────

func runImportLeagueActivity(t *testing.T, a *ImportActivities, season, leagueID int) (ImportYahooLeagueResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.ImportYahooLeague)
	val, err := env.ExecuteActivity(a.ImportYahooLeague, ImportYahooLeagueInput{Season: season, LeagueID: leagueID})
	var result ImportYahooLeagueResult
	if err == nil {
		require.NoError(t, val.Get(&result))
	}
	return result, err
}

// TestImportYahooLeague_ReadsStorageDirectly proves ImportYahooLeague never
// consults the Redis gob cache for the league settings file: it sets up a
// Redis expectation that would only be satisfied if the code read the
// league through the cache-or-download Fetcher, then asserts that
// expectation was never met. The imported snapshot's FetchedAt must come
// from the file's own modification time, set here to a value distinct from
// "now" so a fallback to time.Now() would be caught.
func TestImportYahooLeague_ReadsStorageDirectlyNotRedisCache(t *testing.T) {
	mem := store.NewMemStorage()
	leagueRes := resource.League{Season: yahoofixtures.PointsSeason, LeagueID: yahoofixtures.PointsLeagueID, GameKey: yahoofixtures.PointsGameKey}
	fetchedAt := time.Date(2026, 9, 20, 3, 0, 0, 0, time.UTC)
	mem.SetFileWithTime(leagueRes.Path(), yahoofixtures.Read(yahoofixtures.PointsLeague), fetchedAt)

	redisClient, mockRedis := redismock.NewClientMock()
	// This expectation is only consumed if the import path ever falls back to
	// the Redis-backed gob cache for the league settings; it never runs.
	mockRedis.ExpectGet(core.RedisKey(leagueRes)).SetErr(redis.Nil)

	q := &MockQueries{}
	q.On("GetYahooLeague", mock.Anything, int32(yahoofixtures.PointsLeagueID)).Return(sqlcdb.YahooLeague{}, pgx.ErrNoRows)
	q.On("UpsertYahooLeague", mock.Anything, mock.Anything).Return(nil)
	q.On("UpsertYahooLeagueRosterPositionBatch", mock.Anything, mock.Anything).
		Return(sqlcdb.NewUpsertYahooLeagueRosterPositionBatchBatchResults(&mockBatchResults{}, 10))

	var statParams []sqlcdb.UpsertYahooLeagueStatCategoryBatchParams
	q.On("UpsertYahooLeagueStatCategoryBatch", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			statParams = args.Get(1).([]sqlcdb.UpsertYahooLeagueStatCategoryBatchParams)
		}).
		Return(sqlcdb.NewUpsertYahooLeagueStatCategoryBatchBatchResults(&mockBatchResults{}, 11))

	var snapshotParams sqlcdb.UpsertYahooLeagueRuleSnapshotParams
	q.On("UpsertYahooLeagueRuleSnapshot", mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			snapshotParams = args.Get(1).(sqlcdb.UpsertYahooLeagueRuleSnapshotParams)
		}).
		Return(sqlcdb.UpsertYahooLeagueRuleSnapshotRow{ID: 1, Inserted: true}, nil)

	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(redisClient), Queries: q}
	_, err := runImportLeagueActivity(t, a, yahoofixtures.PointsSeason, yahoofixtures.PointsLeagueID)
	require.NoError(t, err)

	// The Redis expectation above was never consumed: the league settings
	// were read straight from storage.
	assert.Error(t, mockRedis.ExpectationsWereMet(), "ImportYahooLeague must not read the league through the Redis gob cache")

	require.True(t, snapshotParams.FetchedAt.Valid)
	assert.True(t, fetchedAt.Equal(snapshotParams.FetchedAt.Time), "snapshot FetchedAt must be the file's modification time")
	assert.Equal(t, string(draft.SourceYahooAPI), snapshotParams.Source)
	assert.Equal(t, "465.l.77777", snapshotParams.LeagueKey)
	assert.True(t, snapshotParams.GameKey.Valid)
	assert.Equal(t, int32(yahoofixtures.PointsGameKey), snapshotParams.GameKey.Int32)
	assert.NotEmpty(t, snapshotParams.RulesHash)
	assert.NotEmpty(t, snapshotParams.Rules)
	assert.True(t, json.Valid(snapshotParams.Rules), "rules snapshot must be valid JSON")

	weighted := findStatParam(t, statParams, 1) // Goals: stat modifier value 3
	require.True(t, weighted.Value.Valid)
	assert.InDelta(t, 3.0, weighted.Value.Float32, 0.0001)

	ga := findStatParam(t, statParams, 22) // Goals Against: sort_order 0
	require.True(t, ga.SortOrder.Valid)
	assert.Equal(t, int16(sortOrderLowerIsBetter), ga.SortOrder.Int16)

	svPct := findStatParam(t, statParams, 26) // Save Percentage: display-only
	assert.True(t, svPct.IsOnlyDisplayStat)
	assert.False(t, svPct.Value.Valid, "a display-only stat has no points weight")
}

func findStatParam(t *testing.T, params []sqlcdb.UpsertYahooLeagueStatCategoryBatchParams, statID int32) sqlcdb.UpsertYahooLeagueStatCategoryBatchParams {
	t.Helper()
	for _, p := range params {
		if p.StatID == statID {
			return p
		}
	}
	t.Fatalf("no captured stat category param for stat %d", statID)
	return sqlcdb.UpsertYahooLeagueStatCategoryBatchParams{}
}

// ────────────────────────────────────────────────────────────────────────────
// statCategoryParams (unit)
// ────────────────────────────────────────────────────────────────────────────

func TestStatCategoryParams(t *testing.T) {
	t.Parallel()
	const leagueID = 77777
	weight := 2.5

	tests := []struct {
		name     string
		category draft.StatCategory
		check    func(t *testing.T, p sqlcdb.UpsertYahooLeagueStatCategoryBatchParams)
	}{
		{
			name:     "direction_unknown_sort_order_invalid",
			category: draft.StatCategory{StatID: 1, Direction: draft.DirectionUnknown},
			check: func(t *testing.T, p sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) {
				assert.False(t, p.SortOrder.Valid)
			},
		},
		{
			name:     "multiple_position_types_position_type_empty",
			category: draft.StatCategory{StatID: 2, PositionTypes: []string{"P", "G"}},
			check: func(t *testing.T, p sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) {
				assert.Empty(t, p.PositionType)
			},
		},
		{
			name:     "single_position_type_is_set",
			category: draft.StatCategory{StatID: 3, PositionTypes: []string{"P"}},
			check: func(t *testing.T, p sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) {
				assert.Equal(t, "P", p.PositionType)
			},
		},
		{
			name:     "weight_sets_value",
			category: draft.StatCategory{StatID: 4, Weight: &weight},
			check: func(t *testing.T, p sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) {
				require.True(t, p.Value.Valid)
				assert.InDelta(t, weight, p.Value.Float32, 0.0001)
			},
		},
		{
			name:     "no_weight_leaves_value_invalid",
			category: draft.StatCategory{StatID: 5},
			check: func(t *testing.T, p sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) {
				assert.False(t, p.Value.Valid)
			},
		},
		{
			name:     "higher_is_better_sort_order",
			category: draft.StatCategory{StatID: 6, Direction: draft.HigherIsBetter},
			check: func(t *testing.T, p sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) {
				require.True(t, p.SortOrder.Valid)
				assert.Equal(t, int16(sortOrderHigherIsBetter), p.SortOrder.Int16)
			},
		},
		{
			name:     "lower_is_better_sort_order",
			category: draft.StatCategory{StatID: 7, Direction: draft.LowerIsBetter},
			check: func(t *testing.T, p sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) {
				require.True(t, p.SortOrder.Valid)
				assert.Equal(t, int16(sortOrderLowerIsBetter), p.SortOrder.Int16)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			p := statCategoryParams(leagueID, tt.category)
			assert.Equal(t, int32(leagueID), p.LeagueID)
			assert.Equal(t, int32(tt.category.StatID), p.StatID)
			tt.check(t, p)
		})
	}
}
