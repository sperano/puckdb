package cache

import (
	"context"
	"errors"
	"time"

	"github.com/bsm/redislock"
	"github.com/rs/zerolog/log"
)

type redisLockConfig struct {
	key             string
	ttl             time.Duration
	retry           time.Duration
	releaseTimeout  time.Duration
	busyErr         error
	releaseLogField string
	releaseLogValue string
	releaseLogMsg   string
}

func acquireRedisLock(ctx context.Context, client redislock.RedisClient, config redisLockConfig) (func(), error) {
	options := &redislock.Options{
		RetryStrategy: redislock.LinearBackoff(config.retry),
	}
	lock, err := redislock.New(client).Obtain(ctx, config.key, config.ttl, options)
	if errors.Is(err, redislock.ErrNotObtained) {
		return nil, config.busyErr
	}
	if err != nil {
		return nil, err
	}
	return func() {
		releaseRedisLock(ctx, lock, config)
	}, nil
}

func releaseRedisLock(ctx context.Context, lock *redislock.Lock, config redisLockConfig) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), config.releaseTimeout)
	defer cancel()
	if err := lock.Release(releaseCtx); err != nil {
		log.Warn().Err(err).
			Str(config.releaseLogField, config.releaseLogValue).
			Msg(config.releaseLogMsg)
	}
}
