package nhl

import (
	"context"

	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/mock"
)

// MockNHLClient implements shared.NHLClient for testing.
type MockNHLClient struct {
	mock.Mock
}

func (m *MockNHLClient) PlayerLanding(ctx context.Context, playerID nhlapi.PlayerID) (*nhlapi.PlayerLanding, error) {
	args := m.Called(ctx, playerID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.PlayerLanding), args.Error(1)
}

func (m *MockNHLClient) Boxscore(ctx context.Context, gameID nhlapi.GameID) (*nhlapi.Boxscore, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.Boxscore), args.Error(1)
}

func (m *MockNHLClient) PlayByPlay(ctx context.Context, gameID nhlapi.GameID) (*nhlapi.PlayByPlay, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.PlayByPlay), args.Error(1)
}

func (m *MockNHLClient) ShiftChart(ctx context.Context, gameID nhlapi.GameID) (*nhlapi.ShiftChart, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.ShiftChart), args.Error(1)
}

func (m *MockNHLClient) GameStory(ctx context.Context, gameID nhlapi.GameID) (*nhlapi.GameStory, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.GameStory), args.Error(1)
}

func (m *MockNHLClient) SeasonSeries(ctx context.Context, gameID nhlapi.GameID) (*nhlapi.SeasonSeriesMatchup, error) {
	args := m.Called(ctx, gameID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.SeasonSeriesMatchup), args.Error(1)
}

func (m *MockNHLClient) PlayerGameLog(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.PlayerGameLog, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.PlayerGameLog), args.Error(1)
}

func (m *MockNHLClient) DailySchedule(ctx context.Context, date nhlapi.GameDate) (*nhlapi.DailySchedule, error) {
	args := m.Called(ctx, date)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.DailySchedule), args.Error(1)
}

func (m *MockNHLClient) SeasonStandingManifest(ctx context.Context) ([]nhlapi.SeasonInfo, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhlapi.SeasonInfo), args.Error(1)
}

func (m *MockNHLClient) LeagueStandingsForSeason(ctx context.Context, season nhlapi.Season) ([]nhlapi.Standing, error) {
	args := m.Called(ctx, season)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhlapi.Standing), args.Error(1)
}

func (m *MockNHLClient) Franchises(ctx context.Context) ([]nhlapi.Franchise, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhlapi.Franchise), args.Error(1)
}

func (m *MockNHLClient) SearchPlayer(ctx context.Context, query string, limit *int) ([]nhlapi.PlayerSearchResult, error) {
	args := m.Called(ctx, query, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhlapi.PlayerSearchResult), args.Error(1)
}

func (m *MockNHLClient) LeagueStandingsForDate(ctx context.Context, date nhlapi.GameDate) ([]nhlapi.Standing, error) {
	args := m.Called(ctx, date)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]nhlapi.Standing), args.Error(1)
}

func (m *MockNHLClient) RosterSeason(ctx context.Context, teamAbbr string, season nhlapi.Season) (*nhlapi.Roster, error) {
	args := m.Called(ctx, teamAbbr, season)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.Roster), args.Error(1)
}

func (m *MockNHLClient) ClubStats(ctx context.Context, teamAbbr string, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.ClubStats, error) {
	args := m.Called(ctx, teamAbbr, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.ClubStats), args.Error(1)
}

func (m *MockNHLClient) ClubScheduleSeason(ctx context.Context, teamAbbr string, season nhlapi.Season) (*nhlapi.TeamScheduleResponse, error) {
	args := m.Called(ctx, teamAbbr, season)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.TeamScheduleResponse), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterDetail(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeSkaterDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeSkaterDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterSpeedDetail(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeSkaterSpeedDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeSkaterSpeedDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterDistanceDetail(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeSkaterDistanceDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeSkaterDistanceDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterShotSpeedDetail(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeSkaterShotSpeedDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeSkaterShotSpeedDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterShotLocationDetail(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeSkaterShotLocationDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeSkaterShotLocationDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterZoneTime(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeSkaterZoneTimeDetail, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeSkaterZoneTimeDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterComparison(ctx context.Context, playerID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeSkaterComparison, error) {
	args := m.Called(ctx, playerID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeSkaterComparison), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieDetail(ctx context.Context, goalieID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeGoalieDetail, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeGoalieDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalie5v5Detail(ctx context.Context, goalieID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeGoalie5v5Detail, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeGoalie5v5Detail), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieShotLocationDetail(ctx context.Context, goalieID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeGoalieShotLocationDetail, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeGoalieShotLocationDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieSavePctgDetail(ctx context.Context, goalieID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeGoalieSavePctgDetail, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeGoalieSavePctgDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieComparison(ctx context.Context, goalieID nhlapi.PlayerID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeGoalieComparison, error) {
	args := m.Called(ctx, goalieID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeGoalieComparison), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamDetail(ctx context.Context, teamID nhlapi.TeamID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeTeamDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeTeamDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamSpeedDetail(ctx context.Context, teamID nhlapi.TeamID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeTeamSpeedDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeTeamSpeedDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamDistanceDetail(ctx context.Context, teamID nhlapi.TeamID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeTeamDistanceDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeTeamDistanceDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamShotSpeedDetail(ctx context.Context, teamID nhlapi.TeamID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeTeamShotSpeedDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeTeamShotSpeedDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamShotLocationDetail(ctx context.Context, teamID nhlapi.TeamID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeTeamShotLocationDetail, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeTeamShotLocationDetail), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamZoneTimeDetails(ctx context.Context, teamID nhlapi.TeamID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeTeamZoneTimeDetails, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeTeamZoneTimeDetails), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamComparison(ctx context.Context, teamID nhlapi.TeamID, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeTeamComparison, error) {
	args := m.Called(ctx, teamID, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeTeamComparison), args.Error(1)
}

func (m *MockNHLClient) EdgeSkaterLanding(ctx context.Context, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeSkaterLanding, error) {
	args := m.Called(ctx, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeSkaterLanding), args.Error(1)
}

func (m *MockNHLClient) EdgeGoalieLanding(ctx context.Context, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeGoalieLanding, error) {
	args := m.Called(ctx, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeGoalieLanding), args.Error(1)
}

func (m *MockNHLClient) EdgeTeamLanding(ctx context.Context, season nhlapi.Season, gameType nhlapi.GameType) (*nhlapi.EdgeTeamLanding, error) {
	args := m.Called(ctx, season, gameType)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*nhlapi.EdgeTeamLanding), args.Error(1)
}


// MockSeasonsUpserter implements seasonsUpserter for testing.
type MockSeasonsUpserter struct {
	mock.Mock
}

func (m *MockSeasonsUpserter) UpsertSeason(ctx context.Context, arg sqlcdb.UpsertSeasonParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}

// MockSeasonTeamsUpserter implements seasonTeamsUpserter for testing.
type MockSeasonTeamsUpserter struct {
	mock.Mock
}

func (m *MockSeasonTeamsUpserter) UpsertSeasonTeam(ctx context.Context, arg sqlcdb.UpsertSeasonTeamParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}

// MockFranchiseUpserter implements franchiseUpserter for testing.
type MockFranchiseUpserter struct {
	mock.Mock
}

func (m *MockFranchiseUpserter) UpsertFranchise(ctx context.Context, arg sqlcdb.UpsertFranchiseParams) error {
	args := m.Called(ctx, arg)
	return args.Error(0)
}
