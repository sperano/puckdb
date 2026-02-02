package worker

import (
	"context"
	"os"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/stretchr/testify/mock"
)

// MockFileSystem implements cache.FileSystem for testing.
type MockFileSystem struct {
	mock.Mock
	files map[string][]byte
}

func NewMockFileSystem() *MockFileSystem {
	return &MockFileSystem{
		files: make(map[string][]byte),
	}
}

func (m *MockFileSystem) Read(file cache.File) ([]byte, error) {
	args := m.Called(file)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockFileSystem) Write(file cache.File, content []byte) error {
	args := m.Called(file, content)
	return args.Error(0)
}

func (m *MockFileSystem) Exists(file cache.File) bool {
	args := m.Called(file)
	return args.Bool(0)
}

func (m *MockFileSystem) MkdirAll(dir string, perm os.FileMode) error {
	args := m.Called(dir, perm)
	return args.Error(0)
}

func (m *MockFileSystem) Remove(file cache.File) error {
	args := m.Called(file)
	return args.Error(0)
}

func (m *MockFileSystem) FullPath(file cache.File) string {
	args := m.Called(file)
	return args.String(0)
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

// MockFile implements cache.File for testing.
type MockFile struct {
	DirVal  string
	NameVal string
	ExtVal  string
}

func (f MockFile) Dir() string  { return f.DirVal }
func (f MockFile) Name() string { return f.NameVal }
func (f MockFile) Ext() string  { return f.ExtVal }
