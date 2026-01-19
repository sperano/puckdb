package redis

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func TestClearCache(t *testing.T) {
	t.Parallel()
	client := &MockClient{}
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
	client := &MockClient{}
	ctx := context.TODO()
	cmd1 := &redis.StringSliceCmd{}
	cmd1.SetErr(fmt.Errorf("foo"))
	client.On("Keys", ctx, mock.Anything).Return(cmd1)
	err := ClearCache(ctx, client)
	assert.Equal(t, "error clearing cache: foo", err.Error())
	client.AssertCalled(t, "Keys", ctx, "yfh*")
}
