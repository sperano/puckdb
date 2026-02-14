package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/metrics"
)

// FetchDailyScheduleActivity fetches the NHL schedule for a day and all boxscores.
func FetchDailyScheduleActivity(ctx context.Context, day time.Time) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("FetchDailyScheduleActivity", time.Since(start))
	}()

	fs := store.NewStore()

	// Download schedule
	gameIDs, err := downloadSchedule(ctx, fs, day)
	if err != nil {
		return err
	}

	// Filter and download boxscores
	filtered := filterRegularSeasonGames(gameIDs)
	for _, id := range filtered {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := downloadBoxscoreToCache(ctx, fs, day, id); err != nil {
			log.Warn().Err(err).Str("gameid", id.String()).Msg("Skipping boxscore")
			continue
		}
	}
	return nil
}

// downloadSchedule downloads and parses the daily schedule.
func downloadSchedule(ctx context.Context, fs store.Store, day time.Time) ([]nhl.GameID, error) {
	file := store.DailyScheduleFile{Date: day}
	if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
		return nil, fmt.Errorf("mkdir: %w", err)
	}

	if fs.Exists(file) {
		log.Debug().Time("day", day).Msg("Daily schedule cache hit")
		metrics.IncDownload("DailySchedule", "hit")
	} else {
		log.Info().Time("day", day).Msg("Downloading daily schedule")
		client := newNHLClient()

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
func downloadBoxscoreToCache(ctx context.Context, fs store.Store, day time.Time, id nhl.GameID) error {
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

	content, err := DownloadBoxscore(id)
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

// filterRegularSeasonGames filters out preseason games.
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
