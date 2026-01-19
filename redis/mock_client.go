package redis

import (
	"context"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/mock"
)

// MockClient implements Client for testing.
type MockClient struct {
	mock.Mock
}

func (m *MockClient) Del(ctx context.Context, keys ...string) *redis.IntCmd {
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

func (m *MockClient) Get(ctx context.Context, key string) *redis.StringCmd {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.StringCmd)
}

func (m *MockClient) Keys(ctx context.Context, pattern string) *redis.StringSliceCmd {
	args := m.Called(ctx, pattern)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.StringSliceCmd)
}

func (m *MockClient) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	args := m.Called(ctx, key, value, expiration)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.StatusCmd)
}

func (m *MockClient) TTL(ctx context.Context, key string) *redis.DurationCmd {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.DurationCmd)
}

func (m *MockClient) Close() error {
	args := m.Called()
	return args.Error(0)
}

// redislock.RedisClient interface methods

func (m *MockClient) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	args := m.Called(ctx, key, value, expiration)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.BoolCmd)
}

func (m *MockClient) Eval(ctx context.Context, script string, keys []string, scriptArgs ...interface{}) *redis.Cmd {
	args := m.Called(ctx, script, keys, scriptArgs)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.Cmd)
}

func (m *MockClient) EvalSha(ctx context.Context, sha1 string, keys []string, scriptArgs ...interface{}) *redis.Cmd {
	args := m.Called(ctx, sha1, keys, scriptArgs)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.Cmd)
}

func (m *MockClient) ScriptExists(ctx context.Context, scripts ...string) *redis.BoolSliceCmd {
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

func (m *MockClient) ScriptLoad(ctx context.Context, script string) *redis.StringCmd {
	args := m.Called(ctx, script)
	if args.Get(0) == nil {
		return nil
	}
	return args.Get(0).(*redis.StringCmd)
}
