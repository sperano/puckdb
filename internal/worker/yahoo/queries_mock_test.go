package yahoo

import (
	"context"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/stretchr/testify/mock"

	pgx "github.com/jackc/pgx/v5"
)

// mockBatchResults implements pgx.BatchResults for testing.
// It returns execErr for every Exec call and is closed on demand.
type mockBatchResults struct {
	execErr error
}

func (m *mockBatchResults) Exec() (pgconn.CommandTag, error) {
	return pgconn.NewCommandTag("INSERT 0 1"), m.execErr
}

func (m *mockBatchResults) Query() (pgx.Rows, error) { return nil, nil }
func (m *mockBatchResults) QueryRow() pgx.Row        { return nil }
func (m *mockBatchResults) Close() error             { return nil }

// MockQueries is a testify mock implementing the Queries interface.
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

// Compile-time check: MockQueries must satisfy Queries.
var _ Queries = (*MockQueries)(nil)
