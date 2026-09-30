package cache

import (
	"context"
	"syscall"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/require"
)

const (
	testReadyRetries = 3
	testReadyDelay   = time.Millisecond
)

// scriptedPinger answers each Ping with the next scripted error (nil = PONG)
// and counts the calls; errs must not be empty (the last one repeats).
type scriptedPinger struct {
	errs  []error
	calls int
}

func (p *scriptedPinger) Ping(ctx context.Context) *redis.StatusCmd {
	err := p.errs[min(p.calls, len(p.errs)-1)]
	p.calls++
	if err != nil {
		return redis.NewStatusResult("", err)
	}
	return redis.NewStatusResult("PONG", nil)
}

func TestWaitReady_ReachableRedis(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	require.NoError(t, WaitReady(context.Background(), client, testReadyRetries, testReadyDelay))
}

// A pod whose IP is not yet allowed is refused for a moment, then admitted.
func TestWaitReady_RetriesUntilConnectionAccepted(t *testing.T) {
	t.Parallel()
	p := &scriptedPinger{errs: []error{syscall.ECONNREFUSED, syscall.ECONNREFUSED, nil}}

	require.NoError(t, WaitReady(context.Background(), p, testReadyRetries, testReadyDelay))
	require.Equal(t, 3, p.calls)
}

func TestWaitReady_GivesUpAfterRetries(t *testing.T) {
	t.Parallel()
	p := &scriptedPinger{errs: []error{syscall.ECONNREFUSED}}

	err := WaitReady(context.Background(), p, testReadyRetries, testReadyDelay)
	require.ErrorIs(t, err, syscall.ECONNREFUSED)
	require.ErrorContains(t, err, "after 4 attempts")
	require.Equal(t, testReadyRetries+1, p.calls)
}

// A server reply means Redis is reachable; retrying would only delay the error.
func TestWaitReady_ServerReplyIsNotRetried(t *testing.T) {
	t.Parallel()
	server := miniredis.RunT(t)
	server.RequireAuth("secret")
	client := redis.NewClient(&redis.Options{Addr: server.Addr(), MaxRetries: -1})
	t.Cleanup(func() { _ = client.Close() })

	start := time.Now()
	err := WaitReady(context.Background(), client, testReadyRetries, time.Hour)
	require.ErrorContains(t, err, "NOAUTH")
	require.ErrorContains(t, err, "redis replied")
	require.Less(t, time.Since(start), time.Minute)
}

func TestWaitReady_StopsWhenContextEnds(t *testing.T) {
	t.Parallel()
	p := &scriptedPinger{errs: []error{syscall.ECONNREFUSED}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := WaitReady(ctx, p, testReadyRetries, time.Hour)
	require.ErrorIs(t, err, context.Canceled)
	require.ErrorIs(t, err, syscall.ECONNREFUSED)
	require.Equal(t, 1, p.calls)
}
