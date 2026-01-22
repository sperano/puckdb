package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/auth"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/redis"
)

const nhlAPITimeout = 30 * time.Second

// newNHLClient creates an NHL API client with the configured timeout.
func newNHLClient() *nhl.Client {
	cfg := nhl.NewClientConfig(nhl.WithConfigTimeout(nhlAPITimeout))
	return nhl.NewClientWithConfig(cfg)
}

func DownloadFromYahoo(url string) ([]byte, error) {
	redisClient := redis.NewClient()
	defer func() { _ = redisClient.Close() }()

	return downloadFromYahooImpl(redisClient, url)
}

// downloadFromYahooImpl is the testable implementation.
func downloadFromYahooImpl(redisClient redis.Client, url string) ([]byte, error) {
	ctx := context.WithValue(context.Background(), auth.CtxUser, config.DefaultUser)
	return http.DownloadYahoo(ctx, redisClient, url)
}

func DownloadBoxscore(gameid nhl.GameID) ([]byte, error) {
	log.Info().Str("gameid", gameid.String()).Msg("Downloading boxscore NHL API")
	client := newNHLClient()

	start := time.Now()
	boxscore, err := client.Boxscore(context.Background(), gameid)
	duration := time.Since(start)

	if err != nil {
		metrics.ObserveHTTP("nhl", 0, duration, 0)
		return nil, err
	}

	data, err := json.Marshal(boxscore)
	if err != nil {
		return nil, fmt.Errorf("gameid %s: %w", gameid, err)
	}

	metrics.ObserveHTTP("nhl", 200, duration, len(data))
	return data, nil
}
