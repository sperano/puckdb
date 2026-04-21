package player

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/worker/shared"
	"github.com/stretchr/testify/mock"
)

// MockNHLClient implements shared.NHLClient for testing.
type MockNHLClient struct {
	mock.Mock
}

func (m *MockNHLClient) PlayerLanding(ctx context.Context, playerID nhl.PlayerID) (*nhl.PlayerLanding, error) {
	args := m.Called(ctx, playerID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.PlayerLanding), args.Error(1)
}

func (m *MockNHLClient) Boxscore(ctx context.Context, gameID nhl.GameID) (*nhl.Boxscore, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.Boxscore), args.Error(1)
}

func (m *MockNHLClient) PlayByPlay(ctx context.Context, gameID nhl.GameID) (*nhl.PlayByPlay, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.PlayByPlay), args.Error(1)
}

func (m *MockNHLClient) ShiftChart(ctx context.Context, gameID nhl.GameID) (*nhl.ShiftChart, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.ShiftChart), args.Error(1)
}

func (m *MockNHLClient) GameStory(ctx context.Context, gameID nhl.GameID) (*nhl.GameStory, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.GameStory), args.Error(1)
}

func (m *MockNHLClient) SeasonSeries(ctx context.Context, gameID nhl.GameID) (*nhl.SeasonSeriesMatchup, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.SeasonSeriesMatchup), args.Error(1)
}

func (m *MockNHLClient) PlayerGameLog(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.PlayerGameLog, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.PlayerGameLog), args.Error(1)
}

func (m *MockNHLClient) DailySchedule(ctx context.Context, date nhl.GameDate) (*nhl.DailySchedule, error) {
	args := m.Called(ctx, date)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.DailySchedule), args.Error(1)
}

func (m *MockNHLClient) SeasonStandingManifest(ctx context.Context) ([]nhl.SeasonInfo, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhl.SeasonInfo), args.Error(1)
}

func (m *MockNHLClient) LeagueStandingsForSeason(ctx context.Context, season nhl.Season) ([]nhl.Standing, error) {
	args := m.Called(ctx, season)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhl.Standing), args.Error(1)
}

func (m *MockNHLClient) Franchises(ctx context.Context) ([]nhl.Franchise, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhl.Franchise), args.Error(1)
}

func (m *MockNHLClient) SearchPlayer(ctx context.Context, query string, limit *int) ([]nhl.PlayerSearchResult, error) {
	args := m.Called(ctx, query, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhl.PlayerSearchResult), args.Error(1)
}

func (m *MockNHLClient) LeagueStandingsForDate(ctx context.Context, date nhl.GameDate) ([]nhl.Standing, error) {
	args := m.Called(ctx, date)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhl.Standing), args.Error(1)
}

func (m *MockNHLClient) RosterSeason(ctx context.Context, teamAbbr string, season nhl.Season) (*nhl.Roster, error) {
	args := m.Called(ctx, teamAbbr, season)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.Roster), args.Error(1)
}

func (m *MockNHLClient) ClubStats(ctx context.Context, teamAbbr string, season nhl.Season, gameType nhl.GameType) (*nhl.ClubStats, error) {
	args := m.Called(ctx, teamAbbr, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.ClubStats), args.Error(1)
}

func (m *MockNHLClient) ClubScheduleSeason(ctx context.Context, teamAbbr string, season nhl.Season) (*nhl.TeamScheduleResponse, error) {
	args := m.Called(ctx, teamAbbr, season)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.TeamScheduleResponse), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeSkaterDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterSpeedDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterSpeedDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeSkaterSpeedDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterDistanceDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterDistanceDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeSkaterDistanceDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterShotSpeedDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterShotSpeedDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeSkaterShotSpeedDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterShotLocationDetail(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterShotLocationDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeSkaterShotLocationDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterZoneTime(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterZoneTimeDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeSkaterZoneTimeDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterComparison(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterComparison, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeSkaterComparison), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieDetail(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieDetail, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeGoalieDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalie5v5Detail(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalie5v5Detail, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeGoalie5v5Detail), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieShotLocationDetail(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieShotLocationDetail, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeGoalieShotLocationDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieSavePctgDetail(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieSavePctgDetail, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeGoalieSavePctgDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieComparison(ctx context.Context, goalieID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieComparison, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeGoalieComparison), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeTeamDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamSpeedDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamSpeedDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeTeamSpeedDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamDistanceDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamDistanceDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeTeamDistanceDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamShotSpeedDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamShotSpeedDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeTeamShotSpeedDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamShotLocationDetail(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamShotLocationDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeTeamShotLocationDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamZoneTimeDetails(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamZoneTimeDetails, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeTeamZoneTimeDetails), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamComparison(ctx context.Context, teamID nhl.TeamID, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamComparison, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeTeamComparison), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterLanding(ctx context.Context, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeSkaterLanding, error) {
	args := m.Called(ctx, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeSkaterLanding), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieLanding(ctx context.Context, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeGoalieLanding, error) {
	args := m.Called(ctx, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeGoalieLanding), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamLanding(ctx context.Context, season nhl.Season, gameType nhl.GameType) (*nhl.EdgeTeamLanding, error) {
	args := m.Called(ctx, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhl.EdgeTeamLanding), args.Error(1)
}


// MockPlayerUpserter implements PlayerUpserter for testing.
type MockPlayerUpserter struct {
	mock.Mock
	UpsertedPlayers []sqlcdb.UpsertPlayerParams
}

func NewMockPlayerUpserter() *MockPlayerUpserter {
	return &MockPlayerUpserter{
		UpsertedPlayers: make([]sqlcdb.UpsertPlayerParams, 0),
	}
}

func (m *MockPlayerUpserter) UpsertPlayer(ctx context.Context, arg sqlcdb.UpsertPlayerParams) error {
	m.UpsertedPlayers = append(m.UpsertedPlayers, arg)
	args := m.Called(ctx, arg)
	return args.Error(0)
}

func (m *MockPlayerUpserter) ClearConflictingYahooID(ctx context.Context, arg sqlcdb.ClearConflictingYahooIDParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}

// MockPlayerCareerUpserter implements PlayerCareerUpserter for testing.
type MockPlayerCareerUpserter struct {
	mock.Mock
}

func (m *MockPlayerCareerUpserter) UpsertPlayerAwardBatch(ctx context.Context, arg []sqlcdb.UpsertPlayerAwardBatchParams) *sqlcdb.UpsertPlayerAwardBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertPlayerAwardBatchBatchResults)
}

func (m *MockPlayerCareerUpserter) UpsertPlayerSeasonTotalBatch(ctx context.Context, arg []sqlcdb.UpsertPlayerSeasonTotalBatchParams) *sqlcdb.UpsertPlayerSeasonTotalBatchBatchResults {
	args := m.Called(ctx, arg)
	return args.Get(0).(*sqlcdb.UpsertPlayerSeasonTotalBatchBatchResults)
}

// Ensure shared import is used.
var _ shared.NHLClient = (*MockNHLClient)(nil)

// anyArgs is a redismock matcher that accepts any arguments.
func anyArgs(expected, actual []interface{}) error { return nil }

// mockBatchResults implements pgx.BatchResults for testing.
type mockBatchResults struct {
	execErr error
	count   int
	current int
}

func (m *mockBatchResults) Exec() (pgconn.CommandTag, error) {
	m.current++
	return pgconn.NewCommandTag("INSERT 0 1"), m.execErr
}

func (m *mockBatchResults) Query() (pgx.Rows, error) { return nil, nil }
func (m *mockBatchResults) QueryRow() pgx.Row        { return nil }
func (m *mockBatchResults) Close() error              { return nil }
