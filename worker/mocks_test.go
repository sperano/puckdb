package worker

import (
	"context"
	"os"
	"time"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/stretchr/testify/mock"
)

// MockFileSystem implements store.FileSystem for testing.
type MockFileSystem struct {
	mock.Mock
	files map[string][]byte
}

func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		files: make(map[string][]byte),
	}
}

func (m *MockFileSystem) Read(file store.File) ([]byte, error) {
	args := m.Called(file)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockFileSystem) Write(file store.File, content []byte) error {
	args := m.Called(file, content)
	return args.Error(0)
}

func (m *MockFileSystem) Exists(file store.File) bool {
	args := m.Called(file)
	return args.Bool(0)
}

func (m *MockFileSystem) MkdirAll(dir string, perm os.FileMode) error {
	args := m.Called(dir, perm)
	return args.Error(0)
}

func (m *MockFileSystem) Remove(file store.File) error {
	args := m.Called(file)
	return args.Error(0)
}

func (m *MockFileSystem) FullPath(file store.File) string {
	args := m.Called(file)
	return args.String(0)
}

func (m *MockFileSystem) ListFiles(sample store.File, parser store.FilenameParser) ([]store.File, error) {
	args := m.Called(sample, parser)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]store.File), args.Error(1)
}

// MockNHLClient implements NHLClient for testing.
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

// MockHTTPDownloader implements HTTPDownloader for testing.
type MockHTTPDownloader struct {
	mock.Mock
}

func (m *MockHTTPDownloader) Download(url string) ([]byte, error) {
	args := m.Called(url)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

// MockFile implements store.File for testing.
type MockFile struct {
	DirVal  string
	NameVal string
	ExtVal  string
}

func (f MockFile) Dir() string  { return f.DirVal }
func (f MockFile) Name() string { return f.NameVal }
func (f MockFile) Ext() string  { return f.ExtVal }

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

// MockDayFetcher implements dayFetcher for testing.
type MockDayFetcher struct {
	mock.Mock
}

func (m *MockDayFetcher) FetchDailySchedule(ctx context.Context, day time.Time) error {
	args := m.Called(ctx, day)
	return args.Error(0)
}

func (m *MockDayFetcher) FetchRoster(ctx context.Context, startYear int, leagueID, teamID int, day time.Time) error {
	args := m.Called(ctx, startYear, leagueID, teamID, day)
	return args.Error(0)
}

func (m *MockDayFetcher) FetchTeamSummary(ctx context.Context, startYear int, leagueID, teamID int, day time.Time) error {
	args := m.Called(ctx, startYear, leagueID, teamID, day)
	return args.Error(0)
}
