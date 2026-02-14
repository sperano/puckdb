package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"reflect"
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
	"github.com/spf13/viper"
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
// Results are cached in memory and on disk since game keys never change.
func GetGameKeyForSeason(season int) (int, error) {
	// Check memory cache first
	gameKeyCacheMu.RLock()
	if key, ok := gameKeyCache[season]; ok {
		gameKeyCacheMu.RUnlock()
		return key, nil
	}
	gameKeyCacheMu.RUnlock()

	fs := cache.NewSimpleCache()
	gameKey, err := cache.GetGameKey(fs, season, DownloadFromYahoo, sleepAfterYahooDownload)
	if err != nil {
		return 0, err
	}

	// Update memory cache
	gameKeyCacheMu.Lock()
	gameKeyCache[season] = gameKey
	gameKeyCacheMu.Unlock()

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

// doDownloadImpl is the testable implementation.
func doDownloadImpl(ctx context.Context, fs cache.FileSystem, file cache.File, url string) error {
	fileType := reflect.TypeOf(file).Name()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
				metrics.IncDownload(fileType, "error")
				return fmt.Errorf("%s: %w", file.Dir(), err)
			}
			if fs.Exists(file) {
				log.Debug().Str("file", cache.Path(file)).Msg("Already downloaded")
				metrics.IncDownload(fileType, "hit")
				return nil
			}
			content, err := DownloadFromYahoo(url)
			if err != nil {
				metrics.IncDownload(fileType, "error")
				return fmt.Errorf("%s: %w", url, err)
			}
			if err := fs.Write(file, content); err != nil {
				metrics.IncDownload(fileType, "error")
				return fmt.Errorf("%s: %w", cache.Path(file), err)
			}
			log.Info().Str("file", cache.Path(file)).Msg("Downloaded")
			metrics.IncDownload(fileType, "miss")
			sleepAfterYahooDownload()
			return nil
		}
	}
}

// sleepAfterYahooDownload sleeps for a random duration between min and max after a Yahoo API download.
func sleepAfterYahooDownload() {
	minSeconds := viper.GetInt(config.FlagYahooDownloadSleepMin)
	maxSeconds := viper.GetInt(config.FlagYahooDownloadSleepMax)
	if maxSeconds <= 0 {
		return
	}
	if minSeconds < 0 {
		minSeconds = 0
	}
	if minSeconds >= maxSeconds {
		minSeconds = maxSeconds
	}
	sleepSeconds := minSeconds + rand.Intn(maxSeconds-minSeconds+1)
	if sleepSeconds > 0 {
		log.Debug().Int("seconds", sleepSeconds).Msg("Sleeping after Yahoo download")
		time.Sleep(time.Duration(sleepSeconds) * time.Second)
	}
}
