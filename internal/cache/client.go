package cache

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/internal/config"
)

// Options are the Redis connection settings. The command layer reads them
// from its flags; library code and tests pass them explicitly.
type Options struct {
	Addr     string
	Password string
	DB       int
}

// DefaultOptions returns the settings the redis flags default to.
func DefaultOptions() Options {
	return Options{Addr: config.DefaultRedisURL, DB: config.DefaultRedisDB}
}

// NewClient creates a new Redis client with the given settings.
// Returns the concrete go-redis client. Consumers that need a narrower
// surface should declare their own interface (the "accept interfaces,
// return concretes" idiom) — see worker/shared/progress.go for an example.
func NewClient(o Options) *redis.Client {
	log.Debug().Str("url", o.Addr).Int("db", o.DB).Msg("Initializing Redis")
	return redis.NewClient(&redis.Options{
		Addr:     o.Addr,
		Password: o.Password,
		DB:       o.DB,
	})
}

// pinger is the part of a Redis client WaitReady needs.
type pinger interface {
	Ping(ctx context.Context) *redis.StatusCmd
}

// WaitReady pings Redis until it answers, retrying a failed connection up to
// retries times (retries+1 attempts in all) with delay between attempts. A
// reply from the server (such as NOAUTH) is returned at once: Redis is
// reachable and retrying will not change the answer. Short-lived pods need
// this because a new pod's first connections can be refused until its IP is
// allowed by the NetworkPolicy.
func WaitReady(ctx context.Context, client pinger, retries int, delay time.Duration) error {
	for attempt := 0; ; attempt++ {
		err := client.Ping(ctx).Err()
		if err == nil {
			return nil
		}
		if _, isReply := errors.AsType[redis.Error](err); isReply {
			return fmt.Errorf("redis replied: %w", err)
		}
		if attempt == retries {
			return fmt.Errorf("redis not reachable after %d attempts: %w", attempt+1, err)
		}
		log.Warn().Err(err).Int("attempt", attempt+1).Dur("retry_in", delay).Msg("Redis not reachable, retrying")
		select {
		case <-ctx.Done():
			return fmt.Errorf("redis not reachable: %w", errors.Join(err, ctx.Err()))
		case <-time.After(delay):
		}
	}
}

// FlushDB clears all keys from the current Redis database.
func FlushDB(ctx context.Context, client *redis.Client) error {
	return client.FlushDB(ctx).Err()
}
