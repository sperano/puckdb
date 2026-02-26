package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

// File type constants for metrics labels.
const (
	fileTypeBoxscore   = "BoxscoreFile"
	fileTypePlayByPlay = "PlayByPlayFile"
	fileTypeShiftChart = "ShiftChartFile"
	fileTypeGameStory  = "GameStoryFile"
)

// BoxscoreDownloader downloads boxscore data for a game ID.
type BoxscoreDownloader func(id nhl.GameID) ([]byte, error)

// GameDataDownloaders holds all game data downloaders.
type GameDataDownloaders struct {
	Boxscore   BoxscoreDownloader
	PlayByPlay BoxscoreDownloader
	ShiftChart BoxscoreDownloader
	GameStory  BoxscoreDownloader
}

func fetchDailyScheduleImpl(ctx context.Context, repos *store.Repos, client NHLClient, day time.Time, downloaders GameDataDownloaders) error {
	// Download schedule
	gameIDs, err := downloadSchedule(ctx, repos, client, day)
	if err != nil {
		return err
	}

	// Filter and download game data (boxscore, play-by-play, shift chart)
	filtered := filterRegularSeasonGames(gameIDs)
	for _, id := range filtered {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := downloadGameDataToCache(repos.Storage, store.BoxscorePath(day, id), downloaders.Boxscore, id, fileTypeBoxscore); err != nil {
			return fmt.Errorf("boxscore gameid %s: %w", id.String(), err)
		}
		if err := downloadGameDataToCache(repos.Storage, store.PlayByPlayPath(day, id), downloaders.PlayByPlay, id, fileTypePlayByPlay); err != nil {
			return fmt.Errorf("play-by-play gameid %s: %w", id.String(), err)
		}
		if err := downloadGameDataToCache(repos.Storage, store.ShiftChartPath(day, id), downloaders.ShiftChart, id, fileTypeShiftChart); err != nil {
			return fmt.Errorf("shift-chart gameid %s: %w", id.String(), err)
		}
		if err := downloadGameDataToCache(repos.Storage, store.GameStoryPath(day, id), downloaders.GameStory, id, fileTypeGameStory); err != nil {
			return fmt.Errorf("game-story gameid %s: %w", id.String(), err)
		}
	}
	return nil
}

// downloadSchedule downloads and parses the daily schedule.
func downloadSchedule(ctx context.Context, repos *store.Repos, client NHLClient, day time.Time) ([]nhl.GameID, error) {
	path := store.DailySchedulePath(day)

	if repos.Schedule.Exists(day) {
		log.Debug().Time("day", day).Msg("Daily schedule cache hit")
		metrics.IncDownload("DailyScheduleFile", "hit")
	} else {
		log.Info().Time("day", day).Msg("Downloading daily schedule")

		start := time.Now()
		schedule, err := client.DailySchedule(ctx, nhl.FromDate(day))
		duration := time.Since(start)

		if err != nil {
			metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
			metrics.IncDownload("DailyScheduleFile", "error")
			return nil, fmt.Errorf("download schedule: %w", err)
		}

		content, err := json.Marshal(schedule)
		if err != nil {
			metrics.IncDownload("DailyScheduleFile", "error")
			return nil, fmt.Errorf("marshal schedule: %w", err)
		}

		metrics.ObserveHTTP("nhl", http.MethodGet, 200, duration, len(content))

		if err := repos.Schedule.Save(day, content); err != nil {
			metrics.IncDownload("DailyScheduleFile", "error")
			return nil, fmt.Errorf("save schedule: %w", err)
		}
		log.Info().Str("path", path).Msg("Saved schedule")
		metrics.IncDownload("DailyScheduleFile", "miss")
	}

	// Parse schedule using typed repo method
	schedule, err := repos.Schedule.Get(day)
	if err != nil {
		return nil, fmt.Errorf("read schedule: %w", err)
	}

	// Only return completed games
	ids := make([]nhl.GameID, 0, len(schedule.Games))
	for _, g := range schedule.Games {
		if !g.GameState.IsFinal() {
			log.Debug().Str("gameid", g.ID.String()).Str("state", g.GameState.String()).Msg("Skipping incomplete game")
			continue
		}
		ids = append(ids, g.ID)
	}
	return ids, nil
}

// downloadGameDataToCache downloads game data to cache with metrics tracking.
// This is a generic helper that handles boxscores, play-by-play, shift charts, and game stories.
func downloadGameDataToCache(storage store.Storage, path string, download BoxscoreDownloader, id nhl.GameID, fileType string) error {
	if storage.Exists(path) {
		log.Debug().Str("gameid", id.String()).Str("type", fileType).Msg("Already cached")
		metrics.IncDownload(fileType, "hit")
		return nil
	}

	content, err := download(id)
	if err != nil {
		metrics.IncDownload(fileType, "error")
		return fmt.Errorf("download: %w", err)
	}

	if err := storage.Write(path, content); err != nil {
		metrics.IncDownload(fileType, "error")
		return fmt.Errorf("save: %w", err)
	}
	log.Info().Str("gameid", id.String()).Str("path", path).Str("type", fileType).Msg("Saved")
	metrics.IncDownload(fileType, "miss")
	return nil
}

// shouldSkipGame returns true if the game should be skipped during processing.
// This includes preseason games and games with invalid IDs.
func shouldSkipGame(id nhl.GameID) bool {
	gameType, err := id.GameType()
	if err != nil {
		log.Warn().Str("gameID", id.String()).Err(err).Msg("Skipping game with invalid ID")
		return true
	}
	return nhl.GameType(gameType) == nhl.GameTypePreseason
}

// filterRegularSeasonGames filters out preseason games and invalid game IDs.
func filterRegularSeasonGames(gameIDs []nhl.GameID) []nhl.GameID {
	result := make([]nhl.GameID, 0, len(gameIDs))
	for _, id := range gameIDs {
		if shouldSkipGame(id) {
			continue
		}
		result = append(result, id)
	}
	return result
}
