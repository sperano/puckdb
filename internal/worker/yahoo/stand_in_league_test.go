package yahoo

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/testsuite"
)

const (
	standInTestSeason   = 2024
	standInTestLeagueID = 1001
	// leagueWithPositionsXML describes season 2023 league 12345.
	standInTestSourceSeason   = 2023
	standInTestSourceLeagueID = 12345
	standInTestDraftTime      = 1727737200
	standInTestPositions      = 2
	standInTestCategories     = 2
)

func standInTestInput() ImportYahooStandInLeagueInput {
	return ImportYahooStandInLeagueInput{
		Season:   standInTestSeason,
		LeagueID: standInTestLeagueID,
		Source:   config.LeagueMetadataSource{Season: standInTestSourceSeason, LeagueID: standInTestSourceLeagueID},
	}
}

func standInTestActivities(t *testing.T, q *MockQueries, cacheSource bool) *ImportActivities {
	t.Helper()
	mem := store.NewMemStorage()
	if cacheSource {
		res := resource.League{Season: standInTestSourceSeason, LeagueID: standInTestSourceLeagueID}
		require.NoError(t, mem.Write(context.Background(), res.Path(), []byte(leagueWithPositionsXML)))
	}
	return &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}
}

func runStandInImport(t *testing.T, a *ImportActivities, input ImportYahooStandInLeagueInput) (ImportYahooLeagueResult, error) {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.ImportYahooStandInLeague)
	val, err := env.ExecuteActivity(a.ImportYahooStandInLeague, input)
	var result ImportYahooLeagueResult
	if err == nil {
		require.NoError(t, val.Get(&result))
	}
	return result, err
}

func expectSettingsUpserts(q *MockQueries, leagueID int32) {
	q.On("UpsertYahooLeagueRosterPositionBatch", mock.Anything,
		mock.MatchedBy(func(p []sqlcdb.UpsertYahooLeagueRosterPositionBatchParams) bool {
			return len(p) == standInTestPositions && p[0].LeagueID == leagueID
		})).
		Return(sqlcdb.NewUpsertYahooLeagueRosterPositionBatchBatchResults(&mockBatchResults{}, standInTestPositions))
	q.On("UpsertYahooLeagueStatCategoryBatch", mock.Anything,
		mock.MatchedBy(func(p []sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) bool {
			return len(p) == standInTestCategories && p[0].LeagueID == leagueID
		})).
		Return(sqlcdb.NewUpsertYahooLeagueStatCategoryBatchBatchResults(&mockBatchResults{}, standInTestCategories))
	q.On("UpsertYahooLeagueRuleSnapshot", mock.Anything,
		mock.MatchedBy(func(p sqlcdb.UpsertYahooLeagueRuleSnapshotParams) bool { return p.LeagueID == leagueID })).
		Return(sqlcdb.UpsertYahooLeagueRuleSnapshotRow{ID: 1, Inserted: true}, nil)
}

func isNonRetryable(err error) bool {
	var appErr *temporal.ApplicationError
	return errors.As(err, &appErr) && appErr.NonRetryable()
}

func TestStandInLeague_RelabelsSourceAndClearsSeasonSpecificValues(t *testing.T) {
	source := store.League{
		ID: standInTestSourceLeagueID, Key: "453.l.12345", Name: "Crapettes",
		URL: "https://hockey.fantasysports.yahoo.com/hockey/12345", LogoURL: "https://example.com/logo.png",
		DraftStatus: "postdraft", NumTeams: 12, LeagueUpdateTimestamp: standInTestDraftTime,
		ScoringType: "roto", LeagueType: "private", StartDate: "2023-10-10", EndDate: "2024-04-18",
		GameCode: "nhl", Season: standInTestSourceSeason,
		Settings: store.Settings{
			DraftType: "live", DraftTime: standInTestDraftTime, TradeEndDate: "2024-03-08",
			PersistentURL: "https://example.com/league", WaiverRule: "all",
		},
	}

	got := standInLeague(source, standInTestInput())

	assert.Equal(t, standInTestLeagueID, got.ID)
	assert.Equal(t, "temporary-stand-in.l.1001", got.Key)
	assert.Equal(t, "Crapettes [TEMPORARY: 2023 settings of league 12345]", got.Name)
	assert.Equal(t, standInTestSeason, got.Season)
	assert.Equal(t, "roto", got.ScoringType)
	assert.Equal(t, 12, got.NumTeams)
	assert.Equal(t, "https://example.com/logo.png", got.LogoURL)
	assert.Equal(t, "live", got.Settings.DraftType)
	assert.Equal(t, "all", got.Settings.WaiverRule)
	assert.Empty(t, got.URL)
	assert.Empty(t, got.DraftStatus)
	assert.Empty(t, got.StartDate)
	assert.Empty(t, got.EndDate)
	assert.Zero(t, got.LeagueUpdateTimestamp)
	assert.Zero(t, got.Settings.DraftTime)
	assert.Empty(t, got.Settings.TradeEndDate)
	assert.Empty(t, got.Settings.PersistentURL)
	assert.True(t, isStandInLeagueKey(got.Key))
	assert.False(t, isStandInLeagueKey(source.Key))
}

func TestImportYahooStandInLeague_ImportsSourceUnderTargetLeague(t *testing.T) {
	q := &MockQueries{}
	q.On("GetYahooLeague", mock.Anything, int32(standInTestLeagueID)).Return(sqlcdb.YahooLeague{}, pgx.ErrNoRows)
	q.On("UpsertYahooLeague", mock.Anything, mock.MatchedBy(func(p sqlcdb.UpsertYahooLeagueParams) bool {
		return p.ID == standInTestLeagueID && p.Season == standInTestSeason &&
			p.LeagueKey == "temporary-stand-in.l.1001" &&
			p.Name == "Test League [TEMPORARY: 2023 settings of league 12345]" &&
			p.ScoringType == "headpoint" && !p.StartDate.Valid && !p.EndDate.Valid && !p.DraftTime.Valid
	})).Return(nil)
	expectSettingsUpserts(q, standInTestLeagueID)

	result, err := runStandInImport(t, standInTestActivities(t, q, true), standInTestInput())

	require.NoError(t, err)
	assert.Equal(t, ImportYahooLeagueResult{
		RosterPositions: standInTestPositions, StatCategories: standInTestCategories, NewRulesVersion: true,
	}, result)
	q.AssertExpectations(t)
	q.AssertCalled(t, "UpsertYahooLeagueRuleSnapshot", mock.Anything,
		mock.MatchedBy(func(p sqlcdb.UpsertYahooLeagueRuleSnapshotParams) bool {
			return p.Source == "temporary_stand_in" && p.Season == standInTestSeason &&
				p.LeagueKey == "temporary-stand-in.l.1001" && !p.GameKey.Valid &&
				p.SourceSeason == standInTestSourceSeason && p.SourceLeagueKey == "423.l.12345"
		}))
}

func TestImportYahooStandInLeague_ReplacesEarlierStandIn(t *testing.T) {
	q := &MockQueries{}
	q.On("GetYahooLeague", mock.Anything, int32(standInTestLeagueID)).
		Return(sqlcdb.YahooLeague{LeagueKey: standInLeagueKey(standInTestLeagueID), Season: standInTestSeason}, nil)
	q.On("DeleteYahooLeagueRosterPositions", mock.Anything, int32(standInTestLeagueID)).Return(nil).Once()
	q.On("DeleteYahooLeagueStatCategories", mock.Anything, int32(standInTestLeagueID)).Return(nil).Once()
	q.On("UpsertYahooLeague", mock.Anything, mock.Anything).Return(nil)
	expectSettingsUpserts(q, standInTestLeagueID)

	_, err := runStandInImport(t, standInTestActivities(t, q, true), standInTestInput())

	require.NoError(t, err)
	q.AssertExpectations(t)
}

func TestImportYahooStandInLeague_RefusesToOverwriteRealSettings(t *testing.T) {
	q := &MockQueries{}
	q.On("GetYahooLeague", mock.Anything, int32(standInTestLeagueID)).
		Return(sqlcdb.YahooLeague{LeagueKey: "465.l.1001", Season: standInTestSeason}, nil)

	_, err := runStandInImport(t, standInTestActivities(t, q, true), standInTestInput())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "already has real Yahoo settings")
	assert.True(t, isNonRetryable(err))
	q.AssertNotCalled(t, "UpsertYahooLeague", mock.Anything, mock.Anything)
	q.AssertNotCalled(t, "DeleteYahooLeagueStatCategories", mock.Anything, mock.Anything)
}

func TestImportYahooStandInLeague_MissingSourceCache(t *testing.T) {
	q := &MockQueries{}

	_, err := runStandInImport(t, standInTestActivities(t, q, false), standInTestInput())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stand-in source league cache is missing for season 2023 league 12345")
	q.AssertNotCalled(t, "UpsertYahooLeague", mock.Anything, mock.Anything)
}

func TestImportYahooStandInLeague_SourceMustBeEarlierSeason(t *testing.T) {
	q := &MockQueries{}
	input := standInTestInput()
	input.Source.Season = input.Season

	_, err := runStandInImport(t, standInTestActivities(t, q, true), input)

	require.Error(t, err)
	assert.True(t, isNonRetryable(err))
	q.AssertNotCalled(t, "GetYahooLeague", mock.Anything, mock.Anything)
}

func TestImportYahooStandInLeague_LookupError(t *testing.T) {
	q := &MockQueries{}
	q.On("GetYahooLeague", mock.Anything, int32(standInTestLeagueID)).Return(sqlcdb.YahooLeague{}, assert.AnError)

	_, err := runStandInImport(t, standInTestActivities(t, q, true), standInTestInput())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "get league 1001")
	q.AssertNotCalled(t, "UpsertYahooLeague", mock.Anything, mock.Anything)
}

// Once Yahoo serves the league again, the real import must drop the stand-in's
// positions and categories so ones the real league no longer uses disappear.
func TestImportYahooLeague_ClearsStandInBeforeRealImport(t *testing.T) {
	mem := store.NewMemStorage()
	res := resource.League{Season: standInTestSourceSeason, LeagueID: standInTestSourceLeagueID}
	require.NoError(t, mem.Write(context.Background(), res.Path(), []byte(leagueWithPositionsXML)))
	q := &MockQueries{}
	q.On("GetYahooLeague", mock.Anything, int32(standInTestSourceLeagueID)).
		Return(sqlcdb.YahooLeague{
			ID: standInTestSourceLeagueID, LeagueKey: standInLeagueKey(standInTestSourceLeagueID), Season: standInTestSourceSeason,
		}, nil)
	q.On("DeleteYahooLeagueRosterPositions", mock.Anything, int32(standInTestSourceLeagueID)).Return(nil).Once()
	q.On("DeleteYahooLeagueStatCategories", mock.Anything, int32(standInTestSourceLeagueID)).Return(nil).Once()
	q.On("UpsertYahooLeague", mock.Anything, mock.MatchedBy(func(p sqlcdb.UpsertYahooLeagueParams) bool {
		return p.LeagueKey == "423.l.12345"
	})).Return(nil)
	expectSettingsUpserts(q, standInTestSourceLeagueID)
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.ImportYahooLeague)
	_, err := env.ExecuteActivity(a.ImportYahooLeague,
		ImportYahooLeagueInput{Season: standInTestSourceSeason, LeagueID: standInTestSourceLeagueID})

	require.NoError(t, err)
	q.AssertExpectations(t)
}

func TestImportYahooLeague_KeepsRealSettingsRows(t *testing.T) {
	mem := store.NewMemStorage()
	res := resource.League{Season: standInTestSourceSeason, LeagueID: standInTestSourceLeagueID}
	require.NoError(t, mem.Write(context.Background(), res.Path(), []byte(leagueWithPositionsXML)))
	q := &MockQueries{}
	q.On("GetYahooLeague", mock.Anything, int32(standInTestSourceLeagueID)).
		Return(sqlcdb.YahooLeague{LeagueKey: "423.l.12345", Season: standInTestSourceSeason}, nil)
	q.On("UpsertYahooLeague", mock.Anything, mock.Anything).Return(nil)
	expectSettingsUpserts(q, standInTestSourceLeagueID)
	a := &ImportActivities{Storage: mem, GobCache: cache.NewGobCache(nil), Queries: q}

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestActivityEnvironment()
	env.RegisterActivity(a.ImportYahooLeague)
	_, err := env.ExecuteActivity(a.ImportYahooLeague,
		ImportYahooLeagueInput{Season: standInTestSourceSeason, LeagueID: standInTestSourceLeagueID})

	require.NoError(t, err)
	q.AssertNotCalled(t, "DeleteYahooLeagueRosterPositions", mock.Anything, mock.Anything)
	q.AssertNotCalled(t, "DeleteYahooLeagueStatCategories", mock.Anything, mock.Anything)
}
