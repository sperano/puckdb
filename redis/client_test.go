package redis

import (
	"context"
	"fmt"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"testing"
	"time"
)

type clientMock struct {
	mock.Mock
}

func (cm *clientMock) Del(ctx context.Context, keys ...string) *redis.IntCmd {
	a := []any{ctx}
	for _, k := range keys {
		a = append(a, k)
	}
	args := cm.Called(a...)
	obj := args.Get(0)
	return obj.(*redis.IntCmd)
}

func (cm *clientMock) Get(ctx context.Context, key string) *redis.StringCmd {
	return nil
}

func (cm *clientMock) Keys(ctx context.Context, pattern string) *redis.StringSliceCmd {
	args := cm.Called(ctx, pattern)
	obj := args.Get(0)
	return obj.(*redis.StringSliceCmd)
}

func (cm *clientMock) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.StatusCmd {
	return nil
}

func (cm *clientMock) Close() error {
	return nil
}

func (cm *clientMock) SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd {
	return nil
}

func (cm *clientMock) Eval(ctx context.Context, script string, keys []string, args ...interface{}) *redis.Cmd {
	return nil
}

func (cm *clientMock) EvalSha(ctx context.Context, sha1 string, keys []string, args ...interface{}) *redis.Cmd {
	return nil
}

func (cm *clientMock) ScriptExists(ctx context.Context, scripts ...string) *redis.BoolSliceCmd {
	return nil
}
func (cm *clientMock) ScriptLoad(ctx context.Context, script string) *redis.StringCmd {
	return nil
}

func (cm *clientMock) TTL(ctx context.Context, key string) *redis.DurationCmd {
	return nil
}

func TestClearCache(t *testing.T) {
	t.Parallel()
	client := &clientMock{}
	ctx := context.TODO()
	cmd1 := &redis.StringSliceCmd{}
	cmd1.SetVal([]string{"yfh-key1", "yfh-key2"})
	client.On("Keys", ctx, mock.Anything).Return(cmd1)
	client.On("Del", ctx, mock.Anything, mock.Anything).Return(&redis.IntCmd{})

	err := ClearCache(ctx, client)
	assert.Nil(t, err)
	client.AssertCalled(t, "Keys", ctx, "yfh*")
	client.AssertCalled(t, "Del", ctx, "yfh-key1", "yfh-key2")
}

func TestClearCache_ErrKeys(t *testing.T) {
	t.Parallel()
	client := &clientMock{}
	ctx := context.TODO()
	cmd1 := &redis.StringSliceCmd{}
	cmd1.SetErr(fmt.Errorf("foo"))
	client.On("Keys", ctx, mock.Anything).Return(cmd1)
	err := ClearCache(ctx, client)
	assert.Equal(t, "error clearing cache: foo", err.Error())
	client.AssertCalled(t, "Keys", ctx, "yfh*")
}
