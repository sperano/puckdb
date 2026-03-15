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
	"github.com/sperano/puckdb/resource"
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

// NewNHLClient creates an NHL API client with the configured timeout.
func NewNHLClient() *nhl.Client {
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
// TODO: use 3 layers cache
func GetGameKeyForSeason(season int) (int, error) {
	// Check memory cache first
	gameKeyCacheMu.RLock()
	if key, ok := gameKeyCache[season]; ok {
		gameKeyCacheMu.RUnlock()
		return key, nil
	}
	gameKeyCacheMu.RUnlock()

	gameKey, err := getGameKeyImpl(context.Background(), store.NewDefaultStorage(), nil, season, DownloadFromYahoo, sleepAfterYahooDownload)
	if err != nil {
		return 0, err
	}

	// Update memory cache
	gameKeyCacheMu.Lock()
	gameKeyCache[season] = gameKey
	gameKeyCacheMu.Unlock()

	return gameKey, nil
}

// getGameKeyImpl fetches the game key, using cache or downloading if needed.
func getGameKeyImpl(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, season int, fetcher func(string) ([]byte, error), postDownload func()) (int, error) {
	gameKeyRes := resource.GameKey{Season: season}

	if storage.Exists(gameKeyRes.Path()) {
		log.Debug().Int("season", season).Msg("Game key file found")
		fantasy, _, err := cache.ReadParsedCached(ctx, storage, gobCache, gameKeyRes)
		if err != nil {
			return 0, fmt.Errorf("read stored game key for season %d: %w", season, err)
		}
		return extractGameKey(fantasy, season)
	}

	log.Info().Int("season", season).Msg("Downloading game key from Yahoo")
	content, err := fetcher(gameKeyRes.URL())
	if err != nil {
		return 0, fmt.Errorf("fetch game key for season %d: %w", season, err)
	}

	if err := storage.Write(gameKeyRes.Path(), content); err != nil {
		return 0, fmt.Errorf("save game key for season %d: %w", season, err)
	}
	log.Info().Int("season", season).Str("path", gameKeyRes.Path()).Msg("Saved game key")
	if postDownload != nil {
		postDownload()
	}

	fantasy, err := gameKeyRes.Parse(content)
	if err != nil {
		return 0, fmt.Errorf("parse game key response for season %d: %w", season, err)
	}

	return extractGameKey(fantasy, season)
}

// extractGameKey extracts the game key from parsed fantasy content.
func extractGameKey(fantasy *store.FantasyContent, season int) (int, error) {
	var gameKey int
	if len(fantasy.Games) > 0 {
		gameKey = fantasy.Games[0].Key
	} else if fantasy.Game.Key != 0 {
		gameKey = fantasy.Game.Key
	} else {
		return 0, fmt.Errorf("no game key found for season %d", season)
	}

	log.Info().Int("season", season).Int("game_key", gameKey).Msg("Loaded Yahoo game key")
	return gameKey, nil
}

func DownloadBoxscore(gameid nhl.GameID) ([]byte, error) {
	log.Info().Str("gameid", gameid.String()).Msg("Downloading boxscore NHL API")
	client := NewNHLClient()

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
	client := NewNHLClient()

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
	client := NewNHLClient()

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
	client := NewNHLClient()

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

// Downloader fetches content from a URL.
type Downloader func(url string) ([]byte, error)

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
