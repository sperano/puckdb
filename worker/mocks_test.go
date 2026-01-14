package worker

import (
	"context"
	"os"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/yfh/cache"
	"github.com/sperano/yfh/sqlcdb"
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

func (m *MockFileSystem) New(ft cache.FileType, args ...any) cache.File {
	callArgs := m.Called(ft, args)
	return callArgs.Get(0).(cache.File)
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

// MockRedisClient implements redis.Client for testing.
type MockRedisClient struct {
	mock.Mock
}

func (m *MockRedisClient) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	args := make([]any, 0, len(keys)+1)
	args = append(args, ctx)
	for _, k := range keys {
		args = append(args, k)
	}
	result := m.Called(args...)
	if result.Get(0) == nil {
		return nil
	}
	return result.Get(0).(*redis.IntCmd)
}

func (m *MockRedisClient) Get(ctx context.Context, key string) *redis.StringCmd {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.StringCmd)
}

func (m *MockRedisClient) Keys(ctx context.Context, pattern string) *redis.StringSliceCmd {
	args := m.Called(ctx, pattern)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.StringSliceCmd)
}

func (m *MockRedisClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	args := m.Called(ctx, key, value, expiration)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.StatusCmd)
}

func (m *MockRedisClient) TTL(ctx context.Context, key string) *redis.DurationCmd {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.DurationCmd)
}

func (m *MockRedisClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// redislock.RedisClient interface methods
func (m *MockRedisClient) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	args := m.Called(ctx, key, value, expiration)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.BoolCmd)
}

func (m *MockRedisClient) Eval(ctx context.Context, script string, keys []string, scriptArgs ...interface{}) *redis.Cmd {
	args := m.Called(ctx, script, keys, scriptArgs)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.Cmd)
}

func (m *MockRedisClient) EvalSha(ctx context.Context, sha1 string, keys []string, scriptArgs ...interface{}) *redis.Cmd {
	args := m.Called(ctx, sha1, keys, scriptArgs)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.Cmd)
}

func (m *MockRedisClient) ScriptExists(ctx context.Context, scripts ...string) *redis.BoolSliceCmd {
	args := make([]any, 0, len(scripts)+1)
	args = append(args, ctx)
	for _, s := range scripts {
		args = append(args, s)
	}
	result := m.Called(args...)
	if result.Get(0) == nil {
		return nil
	}
	return result.Get(0).(*redis.BoolSliceCmd)
}

func (m *MockRedisClient) ScriptLoad(ctx context.Context, script string) *redis.StringCmd {
	args := m.Called(ctx, script)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.StringCmd)
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
