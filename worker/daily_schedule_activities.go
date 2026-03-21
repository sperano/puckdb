package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"github.com/spf13/viper"
	"go.temporal.io/sdk/activity"
	"golang.org/x/sync/errgroup"
)

// DailyScheduleActivities holds dependencies for daily schedule fetching.
type DailyScheduleActivities struct {
	Storage     store.Storage
	NHLClient   NHLClient
	GobCache    *cache.GobCache
	RedisClient cache.Client // for progress tracking (nil-safe)
	Download    Downloader   // for Yahoo roster/summary downloads (nil if no Yahoo)
}

type FetchDailyScheduleResult struct {
	Origin core.DataOrigin
}

// FetchDailySchedule downloads the daily schedule and all game data for a given day.
func (a *DailyScheduleActivities) FetchDailySchedule(ctx context.Context, day time.Time) (FetchDailyScheduleResult, error) {
	logger := activity.GetLogger(ctx)

	schedule, origin, err := FetchOrCache(ctx, a.Storage, a.GobCache, resource.DailySchedule{Date: day},
		func(ctx context.Context) (*nhl.DailySchedule, error) {
			return a.NHLClient.DailySchedule(ctx, nhl.FromDate(day))
		},
	)
	if err != nil {
		return FetchDailyScheduleResult{}, fmt.Errorf("download schedule: %w", err)
	}

	// Cache hit - game data was already downloaded on the original miss
	if origin != core.OriginRemoteNHLAPI {
		logger.Debug("DailySchedule loaded from cache", "day", day, "origin", origin.String())
		return FetchDailyScheduleResult{Origin: origin}, nil
	}

	log.Info().Str("day", day.Format("2006-01-02")).Msg("Daily schedule fetched from API")

	// Filter and download game data (boxscore, play-by-play, shift chart, game story)
	filtered := filterRegularSeasonGames(schedule.Games)
	g, _ := errgroup.WithContext(ctx)
	g.SetLimit(getGameDownloadConcurrency())

	for _, id := range filtered {
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			if _, _, err := FetchOrCache(ctx, a.Storage, a.GobCache, resource.Boxscore{Date: day, GameID: id},
				func(ctx context.Context) (*nhl.Boxscore, error) {
					return a.NHLClient.Boxscore(ctx, id)
				},
			); err != nil {
				return fmt.Errorf("boxscore gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			if _, _, err := FetchOrCache(ctx, a.Storage, a.GobCache, resource.PlayByPlay{Date: day, GameID: id},
				func(ctx context.Context) (*nhl.PlayByPlay, error) {
					return a.NHLClient.PlayByPlay(ctx, id)
				},
			); err != nil {
				return fmt.Errorf("play-by-play gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			if _, _, err := FetchOrCache(ctx, a.Storage, a.GobCache, resource.ShiftChart{Date: day, GameID: id},
				func(ctx context.Context) (*nhl.ShiftChart, error) {
					return a.NHLClient.ShiftChart(ctx, id)
				},
			); err != nil {
				return fmt.Errorf("shift-chart gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			if _, _, err := FetchOrCache(ctx, a.Storage, a.GobCache, resource.GameStory{Date: day, GameID: id},
				func(ctx context.Context) (*nhl.GameStory, error) {
					return a.NHLClient.GameStory(ctx, id)
				},
			); err != nil {
				return fmt.Errorf("game-story gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(ctx, nil)
			if _, _, err := FetchOrCache(ctx, a.Storage, a.GobCache, resource.SeasonSeries{Date: day, GameID: id},
				func(ctx context.Context) (*nhl.SeasonSeriesMatchup, error) {
					return a.NHLClient.SeasonSeries(ctx, id)
				},
			); err != nil {
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
