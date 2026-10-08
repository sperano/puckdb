package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bsm/redislock"
	"github.com/sperano/puckdb/internal/config"
)

// TokenRefreshLockTTL bounds how long one process may hold a user's refresh
// lock, and therefore how long others wait for it. A refresh is a single
// round-trip to Yahoo's token endpoint, so this is generous; it mostly caps
// the damage of a holder that dies before releasing.
const TokenRefreshLockTTL = 30 * time.Second

// tokenRefreshLockRetry is how often a waiter re-tries to obtain the lock.
const tokenRefreshLockRetry = 100 * time.Millisecond

// tokenRefreshLockReleaseTimeout bounds the release round-trip, which runs on
// its own context: the holder's context is often already cancelled by the time
// it releases (a request deadline fired mid-refresh), and that is precisely
// when other waiters benefit most from an early release.
const tokenRefreshLockReleaseTimeout = 2 * time.Second

// ErrTokenRefreshBusy is returned when another process kept the refresh lock
// for the whole TokenRefreshLockTTL.
var ErrTokenRefreshBusy = errors.New("another process is refreshing the token")

func getRedisKeyForTokenRefreshLock(user string) string {
	return fmt.Sprintf(config.RedisKeyYahooTokenRefreshLockFmt, user)
}

// LockTokenRefresh serializes OAuth2 token refreshes for one user across
// processes. Concurrent workers each build their own Yahoo client; without
// this, they would all present the same refresh token to Yahoo at once and, if
// Yahoo rotated it, all but the first would fail and the stored record could
// end up with a refresh token that is no longer valid. Waiters block until the
// holder releases or their own context ends; TokenRefreshLockTTL bounds the
// wait only when the context has no deadline. On return they should re-read
// the stored token, which the holder has usually refreshed for them.
//
// The returned release func is safe to call once and never panics.
func LockTokenRefresh(ctx context.Context, redisClient redislock.RedisClient, user string) (release func(), err error) {
	key := getRedisKeyForTokenRefreshLock(user)
	release, err = acquireRedisLock(ctx, redisClient, redisLockConfig{
		key:             key,
		ttl:             TokenRefreshLockTTL,
		retry:           tokenRefreshLockRetry,
		releaseTimeout:  tokenRefreshLockReleaseTimeout,
		busyErr:         ErrTokenRefreshBusy,
		releaseLogField: "user",
		releaseLogValue: user,
		releaseLogMsg:   "Failed to release token refresh lock",
	})
	if err != nil {
		return nil, fmt.Errorf("lock token refresh for %s: %w", user, err)
	}
	return release, nil
}
