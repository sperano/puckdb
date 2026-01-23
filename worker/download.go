package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/auth"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	puckhttp "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/redis"
)

const nhlAPITimeout = 30 * time.Second

var (
	gameKeyCache   = make(map[int]int) // season year -> game key
	gameKeyCacheMu sync.RWMutex
)

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
	return puckhttp.DownloadYahoo(ctx, redisClient, url)
}

// GetGameKeyForSeason fetches the Yahoo Fantasy game key for a given NHL season.
// Results are cached in memory since game keys never change.
func GetGameKeyForSeason(season int) (int, error) {
	gameKeyCacheMu.RLock()
	if key, ok := gameKeyCache[season]; ok {
		gameKeyCacheMu.RUnlock()
		return key, nil
	}
	gameKeyCacheMu.RUnlock()

	url := puckhttp.YahooFantasyGameBySeasonURL(season)
	content, err := DownloadFromYahoo(url)
	if err != nil {
		return 0, fmt.Errorf("fetch game key for season %d: %w", season, err)
	}

	fantasy, err := cache.ParseXML(content)
	if err != nil {
		return 0, fmt.Errorf("parse game key response for season %d: %w", season, err)
	}

	var gameKey int
	if len(fantasy.Games) > 0 {
		gameKey = fantasy.Games[0].Key
	} else if fantasy.Game.Key != 0 {
		gameKey = fantasy.Game.Key
	} else {
		return 0, fmt.Errorf("no game key found for season %d", season)
	}

	gameKeyCacheMu.Lock()
	gameKeyCache[season] = gameKey
	gameKeyCacheMu.Unlock()

	log.Info().Int("season", season).Int("game_key", gameKey).Msg("Fetched Yahoo game key")
	return gameKey, nil
}

func DownloadBoxscore(gameid nhl.GameID) ([]byte, error) {
	log.Info().Str("gameid", gameid.String()).Msg("Downloading boxscore NHL API")
	client := newNHLClient()

	start := time.Now()
	boxscore, err := client.Boxscore(context.Background(), gameid)
	duration := time.Since(start)

	if err != nil {
		metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
		return nil, err
	}

	data, err := json.Marshal(boxscore)
	if err != nil {
		return nil, fmt.Errorf("gameid %s: %w", gameid, err)
	}

	metrics.ObserveHTTP("nhl", http.MethodGet, 200, duration, len(data))
	return data, nil
}
