package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/http"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/workflow"
	"gorm.io/gorm"
)

// gamePipelineConfig configures the pipeline for Yahoo game data
var gamePipelineConfig = PipelineConfig[cache.GameLink, *cache.Game]{
	FileType: cache.GameFileType,
	IDName:   func(link cache.GameLink) string { return link.Name() },
	Downloader: func(link cache.GameLink) ([]byte, error) {
		return http.DownloadPublic(http.YahooGameURL(link))
	},
	Parser:          cache.ParseGameHTML,
	Importer:        importGameToDB,
	ContinueOnError: true, // Yahoo game pages may return 404 for some games
}

// boxscorePipelineConfig configures the pipeline for NHL boxscore data
var boxscorePipelineConfig = PipelineConfig[nhl.GameID, nhl.Boxscore]{
	FileType: cache.BoxscoreFileType,
	IDName:   func(id nhl.GameID) string { return id.String() },
	Downloader: func(id nhl.GameID) ([]byte, error) {
		return DownloadBoxscore(id)
	},
	Parser:          parseBoxscore,
	Importer:        importBoxscoreToDB,
	ContinueOnError: true, // NHL API may rate limit or have transient failures
}

// DailyDataConfig configures downloading and parsing daily data files
type DailyDataConfig[T any] struct {
	FileType cache.FileType
	LogMsg   string
	Download func(ctx context.Context, day time.Time) ([]byte, error)
	Parse    func(fs cache.FileSystem, file cache.File) ([]T, error)
}

// downloadDailyData downloads and parses daily data using the provided config.
// Uses the production file system.
func downloadDailyData[T any](ctx context.Context, day time.Time, cfg DailyDataConfig[T]) ([]T, error) {
	fs := cache.NewSimpleCache()
	return downloadDailyDataImpl(ctx, fs, day, cfg)
}

// downloadDailyDataImpl is the testable implementation.
func downloadDailyDataImpl[T any](ctx context.Context, fs cache.FileSystem, day time.Time, cfg DailyDataConfig[T]) ([]T, error) {
	log.Info().Time("day", day).Msg(cfg.LogMsg)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}

	file := fs.New(cfg.FileType, day)
	if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
		return nil, err
	}

	if !fs.Exists(file) {
		content, err := cfg.Download(ctx, day)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", cache.Path(file), err)
		}
		if err = saveToCache(fs, file, content); err != nil {
			return nil, fmt.Errorf("%s: %w", cache.Path(file), err)
		}
	}

	log.Info().Str("file", cache.Path(file)).Msg("Parsing")
	result, err := cfg.Parse(fs, file)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cache.Path(file), err)
	}
	return result, nil
}

// importGameToDB imports a parsed game to the database
func importGameToDB(db *gorm.DB, day time.Time, link cache.GameLink, game *cache.Game) error {
	if game.IsPostponed() {
		log.Info().Time("day", day).Str("link", link.Name()).Msg("Skipping, game is postponed")
		return nil
	}
	if viper.GetBool(config.FlagSkipPreseason) && game.IsPreseason() {
		log.Info().Time("day", day).Str("link", link.Name()).Msg("Skipping, game is preseason")
		return nil
	}
	log.Info().Time("day", day).Str("link", link.Name()).Msg("Importing game")
	gameModel, players, playersStats, err := game.Models(day)
	if err != nil {
		return fmt.Errorf("game %s: %w", link.Name(), err)
	}
	if err := gameModel.Ensure(db); err != nil {
		return fmt.Errorf("game %s ensure: %w", link.Name(), err)
	}
	if len(players) == 0 {
		return fmt.Errorf("no players found in game %s", link.Name())
	}
	log.Info().Str("game", link.Name()).Msg("Importing players in database")
	if err := players.Ensure(db); err != nil {
		return fmt.Errorf("players ensure: %w", err)
	}
	if len(playersStats) == 0 {
		return fmt.Errorf("no players stats found in game %s", link.Name())
	}
	log.Info().Str("game", link.Name()).Msg("Importing players stats in database")
	if err := playersStats.Ensure(db); err != nil {
		return err
	}
	return nil
}

// parseBoxscore parses a boxscore file (placeholder - to be implemented)
func parseBoxscore(fs cache.FileSystem, file cache.File) (nhl.Boxscore, error) {
	// TODO: implement boxscore parsing when NHL API integration is complete
	return nhl.Boxscore{}, nil
}

// parseDailySchedule parses a daily schedule JSON file from cache.
// Only returns completed games (FINAL, OFF) to avoid boxscore errors
// for games that haven't finished yet (missing/incomplete data).
func parseDailySchedule(fs cache.FileSystem, file cache.File) ([]nhl.GameID, error) {
	content, err := fs.Read(file)
	if err != nil {
		return nil, err
	}
	var schedule nhl.DailySchedule
	if err := json.Unmarshal(content, &schedule); err != nil {
		return nil, err
	}
	ids := make([]nhl.GameID, 0, len(schedule.Games))
	for _, g := range schedule.Games {
		if !g.GameState.IsFinal() {
			log.Debug().
				Str("gameid", g.ID.String()).
				Str("state", g.GameState.String()).
				Msg("Skipping incomplete game")
			continue
		}
		ids = append(ids, g.ID)
	}
	return ids, nil
}

// importBoxscoreToDB imports a parsed boxscore to the database (placeholder - to be implemented)
func importBoxscoreToDB(db *gorm.DB, day time.Time, id nhl.GameID, boxscore nhl.Boxscore) error {
	// TODO: implement boxscore import when NHL API integration is complete
	log.Warn().Str("id", id.String()).Msg("Boxscore import not yet implemented")
	return nil
}

// gamesListConfig configures downloading Yahoo games list
var gamesListConfig = DailyDataConfig[cache.GameLink]{
	FileType: cache.GamesListFileType,
	LogMsg:   "Checking games for the day",
	Download: func(ctx context.Context, day time.Time) ([]byte, error) {
		return http.DownloadPublic(http.YahooGamesListURL(day))
	},
	Parse: cache.ParseGamesList,
}

// dailyScheduleConfig configures downloading NHL daily schedule
var dailyScheduleConfig = DailyDataConfig[nhl.GameID]{
	FileType: cache.DailyScheduleFileType,
	LogMsg:   "Checking daily schedule for the day",
	Download: func(ctx context.Context, day time.Time) ([]byte, error) {
		client := newNHLClient()
		schedule, err := client.DailySchedule(ctx, nhl.FromDate(day))
		if err != nil {
			return nil, err
		}
		return json.Marshal(schedule)
	},
	Parse: parseDailySchedule,
}

func downloadGameDay(ctx context.Context, day time.Time) ([]cache.GameLink, error) {
	return downloadDailyData(ctx, day, gamesListConfig)
}

func downloadDailySchedule(ctx context.Context, day time.Time) ([]nhl.GameID, error) {
	return downloadDailyData(ctx, day, dailyScheduleConfig)
}

var globalDB *gorm.DB

var mu sync.Mutex

func getDB() (*gorm.DB, error) {
	mu.Lock()
	defer mu.Unlock()
	if globalDB == nil {
		db, err := database.OpenGorm()
		if err != nil {
			return nil, err
		}
		globalDB = db
	}
	return globalDB, nil
}

func DownloadDailySchedule(ctx context.Context, day time.Time) error {
	gameIDs, err := downloadDailySchedule(ctx, day)
	if err != nil {
		return err
	}
	// Filter out preseason games - they often have missing data (e.g., empty periodType)
	// that causes marshal errors
	filtered := filterRegularSeasonGames(gameIDs)
	return RunDownloadPipeline(ctx, boxscorePipelineConfig, day, filtered)
}

// filterRegularSeasonGames filters out preseason games from the list.
// Preseason games often have missing/invalid data that causes parsing errors.
func filterRegularSeasonGames(gameIDs []nhl.GameID) []nhl.GameID {
	result := make([]nhl.GameID, 0, len(gameIDs))
	for _, id := range gameIDs {
		gameType, err := id.GameType()
		if err != nil {
			log.Warn().Str("gameid", id.String()).Err(err).Msg("Skipping game with invalid ID")
			continue
		}
		if nhl.GameType(gameType) == nhl.GameTypePreseason {
			log.Debug().Str("gameid", id.String()).Msg("Skipping preseason game")
			continue
		}
		result = append(result, id)
	}
	return result
}

func DownloadGameDay(ctx context.Context, day time.Time) error {
	gamelinks, err := downloadGameDay(ctx, day)
	if err != nil {
		return err
	}
	return RunDownloadPipeline(ctx, gamePipelineConfig, day, gamelinks)
}

func ImportGameDay(ctx context.Context, day time.Time) error {
	gamelinks, err := downloadGameDay(ctx, day)
	if err != nil {
		return err
	}
	return RunImportPipeline(ctx, gamePipelineConfig, day, gamelinks)
}

func ImportGamesForDayWorkflow(ctx workflow.Context, day time.Time) error {
	ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
	log.Info().Time("day", day).Msg("Starting import games for day workflow")
	future := workflow.ExecuteActivity(ctx, ImportGameDay, day)
	if err := future.Get(ctx, nil); err != nil {
		return err
	}
	log.Info().Time("day", day).Msg("Import games for day workflow completed")
	return nil
}

/*
// TODO: merge into generic function with ImportGamesForSeasonWorkflow
func DownloadGamesForSeasonWorkflow(ctx workflow.Context, season config.Season) error {
	log.Info().Int("season", season.StartYear()).Msg("Downloading games for season")
	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err != nil {
		return err
	}
	// TODO we know the size, why 0?
	futures := make([]workflow.Future, 0)
	for _, day := range dateRange {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, DownloadGameDay, day)
		futures = append(futures, future)
		future = workflow.ExecuteActivity(ctx, DownloadDailySchedule, day)
		futures = append(futures, future)
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}

func ImportGamesForSeasonWorkflow(ctx workflow.Context, season config.Season) error {
	log.Info().Int("season", season.StartYear()).Msg("Importing games for season")
	dateRange, err := date.DateRangeToToday(season.Start, season.End, time.Now())
	if err != nil {
		return err
	}
	// TODO we know the size, why 0?
	futures := make([]workflow.Future, 0)
	for _, day := range dateRange {
		ctx = workflow.WithActivityOptions(ctx, defaultActivityOptions())
		future := workflow.ExecuteActivity(ctx, ImportGameDay, day)
		futures = append(futures, future)
	}
	for _, f := range futures {
		if err := f.Get(ctx, nil); err != nil {
			return err
		}
	}
	return nil
}
*/
