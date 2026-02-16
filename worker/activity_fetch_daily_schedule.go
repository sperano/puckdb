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

// BoxscoreDownloader downloads boxscore data for a game ID.
type BoxscoreDownloader func(id nhl.GameID) ([]byte, error)

// GameDataDownloaders holds all game data downloaders.
type GameDataDownloaders struct {
	Boxscore   BoxscoreDownloader
	PlayByPlay BoxscoreDownloader
	ShiftChart BoxscoreDownloader
}

func fetchDailyScheduleImpl(ctx context.Context, fs store.Store, client NHLClient, day time.Time, downloaders GameDataDownloaders) error {
	// Download schedule
	gameIDs, err := downloadSchedule(ctx, fs, client, day)
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
		if err := downloadBoxscoreToCache(ctx, fs, day, id, downloaders.Boxscore); err != nil {
			return fmt.Errorf("boxscore gameid %s: %w", id.String(), err)
		}
		if err := downloadPlayByPlayToCache(ctx, fs, day, id, downloaders.PlayByPlay); err != nil {
			return fmt.Errorf("play-by-play gameid %s: %w", id.String(), err)
		}
		if err := downloadShiftChartToCache(ctx, fs, day, id, downloaders.ShiftChart); err != nil {
			return fmt.Errorf("shift-chart gameid %s: %w", id.String(), err)
		}
	}
	return nil
}

// downloadSchedule downloads and parses the daily schedule.
func downloadSchedule(ctx context.Context, fs store.Store, client NHLClient, day time.Time) ([]nhl.GameID, error) {
	file := store.DailyScheduleFile{Date: day}
	if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	if fs.Exists(file) {
		log.Debug().Time("day", day).Msg("Daily schedule cache hit")
		metrics.IncDownload("DailySchedule", "hit")
	} else {
		log.Info().Time("day", day).Msg("Downloading daily schedule")

		start := time.Now()
		schedule, err := client.DailySchedule(ctx, nhl.FromDate(day))
		duration := time.Since(start)

		if err != nil {
			metrics.ObserveHTTP("nhl", http.MethodGet, 0, duration, 0)
			metrics.IncDownload("DailySchedule", "error")
			return nil, fmt.Errorf("download schedule: %w", err)
		}

		content, err := json.Marshal(schedule)
		if err != nil {
			metrics.IncDownload("DailySchedule", "error")
			return nil, fmt.Errorf("marshal schedule: %w", err)
		}

		metrics.ObserveHTTP("nhl", http.MethodGet, 200, duration, len(content))

		if err := fs.Write(file, content); err != nil {
			metrics.IncDownload("DailySchedule", "error")
			return nil, fmt.Errorf("save schedule: %w", err)
		}
		log.Info().Str("path", store.Path(file)).Msg("Saved schedule")
		metrics.IncDownload("DailySchedule", "miss")
	}

	// Parse schedule
	content, err := fs.Read(file)
	if err != nil {
		return nil, fmt.Errorf("read schedule: %w", err)
	}
	var schedule nhl.DailySchedule
	if err := json.Unmarshal(content, &schedule); err != nil {
		return nil, fmt.Errorf("parse schedule: %w", err)
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

// downloadBoxscoreToCache downloads a single boxscore to cache.
func downloadBoxscoreToCache(ctx context.Context, fs store.Store, day time.Time, id nhl.GameID, downloadBoxscore BoxscoreDownloader) error {
	file := store.BoxscoreFile{Date: day, GameID: id}
	if fs.Exists(file) {
		log.Debug().Str("gameid", id.String()).Msg("Boxscore already cached")
		metrics.IncDownload("Boxscore", "hit")
		return nil
	}

	if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
		metrics.IncDownload("Boxscore", "error")
		return fmt.Errorf("mkdir: %w", err)
	}

	content, err := downloadBoxscore(id)
	if err != nil {
		metrics.IncDownload("Boxscore", "error")
		return fmt.Errorf("download: %w", err)
	}

	if err := fs.Write(file, content); err != nil {
		metrics.IncDownload("Boxscore", "error")
		return fmt.Errorf("save: %w", err)
	}
	log.Info().Str("gameid", id.String()).Str("path", store.Path(file)).Msg("Saved boxscore")
	metrics.IncDownload("Boxscore", "miss")
	return nil
}

// downloadPlayByPlayToCache downloads play-by-play data to cache.
func downloadPlayByPlayToCache(ctx context.Context, fs store.Store, day time.Time, id nhl.GameID, download BoxscoreDownloader) error {
	file := store.PlayByPlayFile{Date: day, GameID: id}
	if fs.Exists(file) {
		log.Debug().Str("gameid", id.String()).Msg("PlayByPlay already cached")
		metrics.IncDownload("PlayByPlay", "hit")
		return nil
	}

	if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
		metrics.IncDownload("PlayByPlay", "error")
		return fmt.Errorf("mkdir: %w", err)
	}

	content, err := download(id)
	if err != nil {
		metrics.IncDownload("PlayByPlay", "error")
		return fmt.Errorf("download: %w", err)
	}

	if err := fs.Write(file, content); err != nil {
		metrics.IncDownload("PlayByPlay", "error")
		return fmt.Errorf("save: %w", err)
	}
	log.Info().Str("gameid", id.String()).Str("path", store.Path(file)).Msg("Saved play-by-play")
	metrics.IncDownload("PlayByPlay", "miss")
	return nil
}

// downloadShiftChartToCache downloads shift chart data to cache.
func downloadShiftChartToCache(ctx context.Context, fs store.Store, day time.Time, id nhl.GameID, download BoxscoreDownloader) error {
	file := store.ShiftChartFile{Date: day, GameID: id}
	if fs.Exists(file) {
		log.Debug().Str("gameid", id.String()).Msg("ShiftChart already cached")
		metrics.IncDownload("ShiftChart", "hit")
		return nil
	}

	if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
		metrics.IncDownload("ShiftChart", "error")
		return fmt.Errorf("mkdir: %w", err)
	}

	content, err := download(id)
	if err != nil {
		metrics.IncDownload("ShiftChart", "error")
		return fmt.Errorf("download: %w", err)
	}

	if err := fs.Write(file, content); err != nil {
		metrics.IncDownload("ShiftChart", "error")
		return fmt.Errorf("save: %w", err)
	}
	log.Info().Str("gameid", id.String()).Str("path", store.Path(file)).Msg("Saved shift chart")
	metrics.IncDownload("ShiftChart", "miss")
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
