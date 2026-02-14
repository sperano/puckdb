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
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	puckhttp "github.com/sperano/puckdb/http"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
)

const nhlAPITimeout = 30 * time.Second

var (
	gameKeyCache   = make(map[int]int) // season year -> game key
	gameKeyCacheMu sync.RWMutex
)

// NHLClient defines the NHL API methods used by worker activities.
// The real implementation is nhl.Client; tests can provide mock implementations.
type NHLClient interface {
	PlayerLanding(ctx context.Context, playerID nhl.PlayerID) (*nhl.PlayerLanding, error)
	Boxscore(ctx context.Context, gameID nhl.GameID) (*nhl.Boxscore, error)
	DailySchedule(ctx context.Context, date nhl.GameDate) (*nhl.DailySchedule, error)
	SeasonStandingManifest(ctx context.Context) ([]nhl.SeasonInfo, error)
	LeagueStandingsForSeason(ctx context.Context, season nhl.Season) ([]nhl.Standing, error)
	Franchises(ctx context.Context) ([]nhl.Franchise, error)
}

// Compile-time check that nhl.Client implements NHLClient
var _ NHLClient = (*nhl.Client)(nil)

// newNHLClient creates an NHL API client with the configured timeout.
func newNHLClient() *nhl.Client {
	cfg := nhl.NewClientConfig(nhl.WithConfigTimeout(nhlAPITimeout))
	return nhl.NewClientWithConfig(cfg)
}

func DownloadFromYahoo(url string) ([]byte, error) {
	redisClient := cache.NewClient()
	defer func() { _ = redisClient.Close() }()

	return downloadFromYahooImpl(redisClient, url)
}

// downloadFromYahooImpl is the testable implementation.
func downloadFromYahooImpl(redisClient cache.Client, url string) ([]byte, error) {
	ctx := context.WithValue(context.Background(), config.CtxUser, config.DefaultUser)
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

	fs := store.NewStore()
	gameKey, err := store.GetGameKey(fs, season, DownloadFromYahoo, sleepAfterYahooDownload)
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
func doDownloadImpl(ctx context.Context, fs store.Store, file store.File, url string) error {
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
				log.Debug().Str("file", store.Path(file)).Msg("Already downloaded")
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
				return fmt.Errorf("%s: %w", store.Path(file), err)
			}
			log.Info().Str("file", store.Path(file)).Msg("Downloaded")
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
