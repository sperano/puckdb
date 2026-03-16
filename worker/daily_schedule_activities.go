package worker

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/activity"
	"golang.org/x/sync/errgroup"
)

// File type constants for metrics labels.

const (
	fileTypeBoxscore     = "BoxscoreFile"
	fileTypePlayByPlay   = "PlayByPlayFile"
	fileTypeShiftChart   = "ShiftChartFile"
	fileTypeGameStory    = "GameStoryFile"
	fileTypeSeasonSeries = "SeasonSeriesFile"
)

// BoxscoreDownloader downloads boxscore data for a game ID.
type BoxscoreDownloader func(id nhl.GameID) ([]byte, error)

// GameDataDownloaders holds all game data downloaders.
type GameDataDownloaders struct {
	Boxscore     BoxscoreDownloader
	PlayByPlay   BoxscoreDownloader
	ShiftChart   BoxscoreDownloader
	GameStory    BoxscoreDownloader
	SeasonSeries BoxscoreDownloader
}

// DefaultGameDataDownloaders returns GameDataDownloaders wired to the real NHL API download functions.
func DefaultGameDataDownloaders() GameDataDownloaders {
	return GameDataDownloaders{
		Boxscore:     DownloadBoxscore,
		PlayByPlay:   DownloadPlayByPlay,
		ShiftChart:   DownloadShiftChart,
		GameStory:    DownloadGameStory,
		SeasonSeries: DownloadSeasonSeries,
	}
}

// DailyScheduleActivities holds dependencies for daily schedule fetching.
type DailyScheduleActivities struct {
	Storage       store.Storage
	NHLClient     NHLClient
	GobCache      *cache.GobCache
	GameDownloads GameDataDownloaders
	RedisClient   cache.Client // for progress tracking (nil-safe)
	Download      Downloader   // for Yahoo roster/summary downloads (nil if no Yahoo)
}

type FetchDailyScheduleResult struct {
	Origin core.DataOrigin
}

// FetchDailySchedule downloads the daily schedule and all game data for a given day.
func (a *DailyScheduleActivities) FetchDailySchedule(ctx context.Context, day time.Time) (FetchDailyScheduleResult, error) {
	scheduleRes := resource.DailySchedule{Date: day}
	logger := activity.GetLogger(ctx)

	// Check Redis → FileSystem cache
	_, origin, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, scheduleRes)
	if err == nil {
		logger.Debug("DailySchedule loaded from cache", "day", day, "origin", origin.String())
		metrics.IncDownload(core.DailySchedule, metrics.ResultHit)
		return FetchDailyScheduleResult{Origin: origin}, nil
	}
	// Cache miss - fetch from API
	start := time.Now()
	schedule, err := a.NHLClient.DailySchedule(ctx, nhl.FromDate(day))
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
		metrics.IncDownload(core.DailySchedule, metrics.ResultError)
		return FetchDailyScheduleResult{}, fmt.Errorf("download schedule: %w", err)
	}
	// Save to FileSystem
	metrics.ObserveHTTP("nhl", http.MethodGet, 200, duration, 0)
	metrics.IncDownload(core.DailySchedule, metrics.ResultMiss)
	if err := resource.WriteParsed(a.Storage, scheduleRes, schedule); err != nil {
		metrics.IncDownload(core.DailySchedule, metrics.ResultError)
		return FetchDailyScheduleResult{}, fmt.Errorf("failed to write daily schedule (%v) to cache: %w", day, err)
	}
	// Populate Redis gob cache
	if err := cache.Set(a.GobCache, ctx, core.RedisKey(scheduleRes), schedule); err != nil {
		return FetchDailyScheduleResult{}, fmt.Errorf("gob cache set schedule: %w", err)
	}
	log.Info().Str("path", scheduleRes.Path()).Msg("Saved schedule")
	// Filter and download game data (boxscore, play-by-play, shift chart, game story)
	filtered := filterRegularSeasonGames(schedule.Games)
	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(getGameDownloadConcurrency())

	for _, id := range filtered {
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			boxscoreRes := resource.Boxscore{Date: day, GameID: id}
			if err := downloadGameDataToCache(a.Storage, boxscoreRes, a.GameDownloads.Boxscore, id, fileTypeBoxscore); err != nil {
				return fmt.Errorf("boxscore gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			playByPlayRes := resource.PlayByPlay{Date: day, GameID: id}
			if err := downloadGameDataToCache(a.Storage, playByPlayRes, a.GameDownloads.PlayByPlay, id, fileTypePlayByPlay); err != nil {
				return fmt.Errorf("play-by-play gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			shiftChartRes := resource.ShiftChart{Date: day, GameID: id}
			if err := downloadGameDataToCache(a.Storage, shiftChartRes, a.GameDownloads.ShiftChart, id, fileTypeShiftChart); err != nil {
				return fmt.Errorf("shift-chart gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			gameStoryRes := resource.GameStory{Date: day, GameID: id}
			if err := downloadGameDataToCache(a.Storage, gameStoryRes, a.GameDownloads.GameStory, id, fileTypeGameStory); err != nil {
				return fmt.Errorf("game-story gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			seasonSeriesRes := resource.SeasonSeries{Date: day, GameID: id}
			if err := downloadGameDataToCache(a.Storage, seasonSeriesRes, a.GameDownloads.SeasonSeries, id, fileTypeSeasonSeries); err != nil {
				return fmt.Errorf("season-series gameid %s: %w", id.String(), err)
			}
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return FetchDailyScheduleResult{}, err
	}
	return FetchDailyScheduleResult{Origin: core.OriginRemoteNHLAPI}, nil
}

// downloadGameDataToCache downloads game data to cache with metrics tracking.
// This is a generic helper that handles boxscores, play-by-play, shift charts, and game stories.
func downloadGameDataToCache(storage store.Storage, res core.Resource, download BoxscoreDownloader, id nhl.GameID, fileType string) error {
	if storage.Exists(res.Path()) {
		log.Debug().Str("gameid", id.String()).Str("type", fileType).Msg("Already cached")
		metrics.LegacyIncDownload(fileType, metrics.ResultHit)
		return nil
	}
	content, err := download(id)
	if err != nil {
		metrics.LegacyIncDownload(fileType, metrics.ResultError)
		return fmt.Errorf("download: %w", err)
	}
	if err := storage.Write(res.Path(), content); err != nil {
		metrics.LegacyIncDownload(fileType, metrics.ResultError)
		return fmt.Errorf("save: %w", err)
	}
	log.Info().Str("gameid", id.String()).Str("path", res.Path()).Str("type", fileType).Msg("Saved")
	metrics.LegacyIncDownload(fileType, metrics.ResultMiss)
	return nil
}

func getGameDownloadConcurrency() int {
	concurrency := viper.GetInt(config.FlagGameDownloadConcurrency)
	if concurrency <= 0 {
		return config.DefaultGameDownloadConcurrency
	}
	return concurrency
}

// shouldSkipGame returns true if the game should be skipped during processing.
// This includes preseason games and games with invalid IDs.
func shouldSkipGame(game nhl.ScheduleGame) bool {
	if !game.GameState.IsFinal() {
		return true
	}
	gameType, err := game.ID.GameType()
	if err != nil {
		log.Warn().Str("game.ID", game.ID.String()).Err(err).Msg("Skipping game with invalid ID")
		return true
	}
	return nhl.GameType(gameType) == nhl.GameTypePreseason
}

// filterRegularSeasonGames filters out preseason games and invalid game IDs.
func filterRegularSeasonGames(games []nhl.ScheduleGame) []nhl.GameID {
	result := make([]nhl.GameID, 0, len(games))
	for _, g := range games {
		if shouldSkipGame(g) {
			continue
		}
		result = append(result, g.ID)
	}
	return result
}
