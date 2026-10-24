package yahoo

import (
	"context"

	"github.com/sperano/puckdb/internal/fixtures/pgxfixtures"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/mock"
)

type mockBatchResults = pgxfixtures.BatchResults

// MockQueries is a testify mock implementing the YahooImportQueries interface.
type MockQueries struct {
	mock.Mock
}

func (m *MockQueries) UpsertYahooLeague(ctx context.Context, arg sqlcdb.UpsertYahooLeagueParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) UpsertYahooLeagueRosterPositionBatch(ctx context.Context, arg []sqlcdb.UpsertYahooLeagueRosterPositionBatchParams) *sqlcdb.UpsertYahooLeagueRosterPositionBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooLeagueRosterPositionBatchBatchResults)
}

func (m *MockQueries) UpsertYahooLeagueStatCategoryBatch(ctx context.Context, arg []sqlcdb.UpsertYahooLeagueStatCategoryBatchParams) *sqlcdb.UpsertYahooLeagueStatCategoryBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooLeagueStatCategoryBatchBatchResults)
}

func (m *MockQueries) GetYahooLeague(ctx context.Context, id int32) (sqlcdb.YahooLeague, error) {
	args := m.Called(ctx, id)
	return args.Get(0).(sqlcdb.YahooLeague), args.Error(1)
}

func (m *MockQueries) DeleteYahooLeagueRosterPositions(ctx context.Context, leagueID int32) error {
	return m.Called(ctx, leagueID).Error(0)
}

func (m *MockQueries) DeleteYahooLeagueStatCategories(ctx context.Context, leagueID int32) error {
	return m.Called(ctx, leagueID).Error(0)
}

func (m *MockQueries) UpsertYahooTeamBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamBatchParams) *sqlcdb.UpsertYahooTeamBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooTeamBatchBatchResults)
}

func (m *MockQueries) UpsertYahooTeamManagerBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamManagerBatchParams) *sqlcdb.UpsertYahooTeamManagerBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooTeamManagerBatchBatchResults)
}

func (m *MockQueries) UpsertYahooTeamSummaryBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamSummaryBatchParams) *sqlcdb.UpsertYahooTeamSummaryBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooTeamSummaryBatchBatchResults)
}

func (m *MockQueries) ReplaceYahooTeamSummaryStatsBatch(ctx context.Context, arg []sqlcdb.ReplaceYahooTeamSummaryStatsBatchParams) *sqlcdb.ReplaceYahooTeamSummaryStatsBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.ReplaceYahooTeamSummaryStatsBatchBatchResults)
}

func (m *MockQueries) UpsertYahooTeamStandingBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamStandingBatchParams) *sqlcdb.UpsertYahooTeamStandingBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooTeamStandingBatchBatchResults)
}

func (m *MockQueries) ClearYahooTeamStandingsExcept(ctx context.Context, arg sqlcdb.ClearYahooTeamStandingsExceptParams) error {
	return m.Called(ctx, arg).Error(0)
}

func (m *MockQueries) UpsertYahooTeamRosterBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTeamRosterBatchParams) *sqlcdb.UpsertYahooTeamRosterBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooTeamRosterBatchBatchResults)
}

func (m *MockQueries) UpsertYahooTransactionBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTransactionBatchParams) *sqlcdb.UpsertYahooTransactionBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooTransactionBatchBatchResults)
}

func (m *MockQueries) UpsertYahooTransactionPlayerBatch(ctx context.Context, arg []sqlcdb.UpsertYahooTransactionPlayerBatchParams) *sqlcdb.UpsertYahooTransactionPlayerBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooTransactionPlayerBatchBatchResults)
}

func (m *MockQueries) UpsertYahooDraftResultBatch(ctx context.Context, arg []sqlcdb.UpsertYahooDraftResultBatchParams) *sqlcdb.UpsertYahooDraftResultBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooDraftResultBatchBatchResults)
}

func (m *MockQueries) UpsertYahooMatchupBatch(ctx context.Context, arg []sqlcdb.UpsertYahooMatchupBatchParams) *sqlcdb.UpsertYahooMatchupBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooMatchupBatchBatchResults)
}

func (m *MockQueries) UpsertYahooLeagueRuleSnapshot(ctx context.Context, arg sqlcdb.UpsertYahooLeagueRuleSnapshotParams) (sqlcdb.UpsertYahooLeagueRuleSnapshotRow, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(sqlcdb.UpsertYahooLeagueRuleSnapshotRow), args.Error(1)
}

func (m *MockQueries) UpsertYahooLeaguePlayerBatch(ctx context.Context, arg []sqlcdb.UpsertYahooLeaguePlayerBatchParams) *sqlcdb.UpsertYahooLeaguePlayerBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertYahooLeaguePlayerBatchBatchResults)
}

func (m *MockQueries) DeleteStaleYahooLeaguePlayers(ctx context.Context, arg sqlcdb.DeleteStaleYahooLeaguePlayersParams) (int64, error) {
	args := m.Called(ctx, arg)
	return args.Get(0).(int64), args.Error(1)
}

// Compile-time check: MockQueries must satisfy YahooImportQueries.
var _ YahooImportQueries = (*MockQueries)(nil)
