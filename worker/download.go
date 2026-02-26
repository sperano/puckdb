package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
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
	PlayByPlay(ctx context.Context, gameID nhl.GameID) (*nhl.PlayByPlay, error)
	ShiftChart(ctx context.Context, gameID nhl.GameID) (*nhl.ShiftChart, error)
	GameStory(ctx context.Context, gameID nhl.GameID) (*nhl.GameStory, error)
	PlayerGameLog(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (*nhl.PlayerGameLog, error)
	DailySchedule(ctx context.Context, date nhl.GameDate) (*nhl.DailySchedule, error)
	SeasonStandingManifest(ctx context.Context) ([]nhl.SeasonInfo, error)
	LeagueStandingsForSeason(ctx context.Context, season nhl.Season) ([]nhl.Standing, error)
	Franchises(ctx context.Context) ([]nhl.Franchise, error)
	SearchPlayer(ctx context.Context, query string, limit *int) ([]nhl.PlayerSearchResult, error)
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

	repos := store.NewDefaultRepos()
	gameKey, err := store.GetGameKey(repos, season, DownloadFromYahoo, sleepAfterYahooDownload)
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

// GameDataDownloader downloads game-related data (boxscore, play-by-play, shift chart).
type GameDataDownloader func(id nhl.GameID) ([]byte, error)

func DownloadPlayByPlay(gameid nhl.GameID) ([]byte, error) {
	log.Debug().Str("gameid", gameid.String()).Msg("Downloading play-by-play NHL API")
	client := newNHLClient()

	start := time.Now()
	pbp, err := client.PlayByPlay(context.Background(), gameid)
	duration := time.Since(start)

	if err != nil {
		metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
		return nil, err
	}

	data, err := json.Marshal(pbp)
	if err != nil {
		return nil, fmt.Errorf("gameid %s: %w", gameid, err)
	}

	metrics.ObserveHTTP("nhl", http.MethodGet, 200, duration, len(data))
	return data, nil
}

func DownloadShiftChart(gameid nhl.GameID) ([]byte, error) {
	log.Debug().Str("gameid", gameid.String()).Msg("Downloading shift chart NHL API")
	client := newNHLClient()

	start := time.Now()
	shifts, err := client.ShiftChart(context.Background(), gameid)
	duration := time.Since(start)

	if err != nil {
		metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
		return nil, err
	}

	data, err := json.Marshal(shifts)
	if err != nil {
		return nil, fmt.Errorf("gameid %s: %w", gameid, err)
	}

	metrics.ObserveHTTP("nhl", http.MethodGet, 200, duration, len(data))
	return data, nil
}

func DownloadGameStory(gameid nhl.GameID) ([]byte, error) {
	log.Debug().Str("gameid", gameid.String()).Msg("Downloading game story NHL API")
	client := newNHLClient()

	start := time.Now()
	story, err := client.GameStory(context.Background(), gameid)
	duration := time.Since(start)

	if err != nil {
		metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
		return nil, err
	}

	data, err := json.Marshal(story)
	if err != nil {
		return nil, fmt.Errorf("gameid %s: %w", gameid, err)
	}

	metrics.ObserveHTTP("nhl", http.MethodGet, 200, duration, len(data))
	return data, nil
}

// DownloadPlayerGameLog downloads a player's game log for a specific season and game type.
func DownloadPlayerGameLog(playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) ([]byte, error) {
	log.Debug().
		Str("playerID", playerID.String()).
		Str("season", season.String()).
		Str("gameType", gameType.String()).
		Msg("Downloading player game log NHL API")
	client := newNHLClient()

	start := time.Now()
	gameLog, err := client.PlayerGameLog(context.Background(), playerID, season, gameType)
	duration := time.Since(start)

	if err != nil {
		metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
		return nil, err
	}

	data, err := json.Marshal(gameLog)
	if err != nil {
		return nil, fmt.Errorf("player %s season %s: %w", playerID, season, err)
	}

	metrics.ObserveHTTP("nhl", http.MethodGet, 200, duration, len(data))
	return data, nil
}

// Downloader fetches content from a URL.
type Downloader func(url string) ([]byte, error)

// doDownloadImpl is the testable implementation.
// fileType is used for metrics labels.
func doDownloadImpl(ctx context.Context, storage store.Storage, path string, url string, download Downloader, fileType string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if storage.Exists(path) {
		log.Debug().Str("path", path).Str("type", fileType).Msg("Cached")
		metrics.IncDownload(fileType, "hit")
		return nil
	}
	content, err := download(url)
	if err != nil {
		metrics.IncDownload(fileType, "error")
		return fmt.Errorf("%s: %w", url, err)
	}
	if err := storage.Write(path, content); err != nil {
		metrics.IncDownload(fileType, "error")
		return fmt.Errorf("%s: %w", path, err)
	}
	log.Info().Str("path", path).Str("type", fileType).Msg("Saved")
	metrics.IncDownload(fileType, "miss")
	sleepAfterYahooDownload()
	return nil
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
