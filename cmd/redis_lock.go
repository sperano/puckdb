package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/bsm/redislock"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/cache"
)

// withRedisLock acquires a distributed lock and executes fn, releasing on return.
// Returns nil without calling fn if lock is not obtained (another process holds it).
func withRedisLock(ctx context.Context, lockName string, timeout time.Duration, fn func() error) error {
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	locker := redislock.New(redisClient)
	lock, err := locker.Obtain(ctx, lockName, timeout, nil)
	if errors.Is(err, redislock.ErrNotObtained) {
		log.Warn().Str("lock", lockName).Msg("Lock not obtained, another process is running")
		return nil
	}
	if err != nil {
		return fmt.Errorf("acquire lock %s: %w", lockName, err)
	}
	defer func() {
		if err := lock.Release(ctx); err != nil {
			log.Error().Err(err).Str("lock", lockName).Msg("Failed to release lock")
		}
	}()

	return fn()
}
