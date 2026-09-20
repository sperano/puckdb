package nhl

import (
	"context"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
	"golang.org/x/sync/errgroup"
)

// DailyScheduleActivities holds dependencies for daily schedule fetching.
type DailyScheduleActivities struct {
	Storage     store.Storage
	NHLClient   shared.NHLClient
	GobCache    *cache.GobCache
	RedisClient *redis.Client // for progress tracking (nil-safe)
	Download    shared.Downloader
}

// FetchDailyScheduleResult contains the result of fetching a daily schedule.
type FetchDailyScheduleResult struct {
	Origin core.DataOrigin
}

// FetchDailySchedule downloads the daily schedule and all game data for a given day.
func (a *DailyScheduleActivities) FetchDailySchedule(ctx context.Context, day time.Time) (FetchDailyScheduleResult, error) {
	logger := activity.GetLogger(ctx)

	schedule, origin, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, resource.DailySchedule{Date: day},
		func(ctx context.Context) (*nhlapi.DailySchedule, error) {
			return a.NHLClient.DailySchedule(ctx, nhlapi.FromDate(day))
		},
	)
	if err != nil {
		return FetchDailyScheduleResult{}, fmt.Errorf("download schedule: %w", err)
	}

	if origin != core.OriginRemoteNHLAPI {
		logger.Debug("DailySchedule loaded from cache", "day", day, "origin", origin.String())
		return FetchDailyScheduleResult{Origin: origin}, nil
	}

	log.Info().Str("day", day.Format("2006-01-02")).Msg("Daily schedule fetched from API")

	filtered := filterFinalNonPreseasonGames(schedule.Games)
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(getGameDownloadConcurrency())

	for _, id := range filtered {
		g.Go(func() error {
			activity.RecordHeartbeat(gctx, nil)
			if _, _, err := shared.FetchOrCache(gctx, a.Storage, a.GobCache, resource.Boxscore{Date: day, GameID: id},
				func(ctx context.Context) (*nhlapi.Boxscore, error) {
					return a.NHLClient.Boxscore(ctx, id)
				},
			); err != nil {
				return fmt.Errorf("boxscore gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(gctx, nil)
			if _, _, err := shared.FetchOrCache(gctx, a.Storage, a.GobCache, resource.PlayByPlay{Date: day, GameID: id},
				func(ctx context.Context) (*nhlapi.PlayByPlay, error) {
					return a.NHLClient.PlayByPlay(ctx, id)
				},
			); err != nil {
				return fmt.Errorf("play-by-play gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(gctx, nil)
			if _, _, err := shared.FetchOrCache(gctx, a.Storage, a.GobCache, resource.ShiftChart{Date: day, GameID: id},
				func(ctx context.Context) (*nhlapi.ShiftChart, error) {
					return a.NHLClient.ShiftChart(ctx, id)
				},
			); err != nil {
				return fmt.Errorf("shift-chart gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(gctx, nil)
			if _, _, err := shared.FetchOrCache(gctx, a.Storage, a.GobCache, resource.GameStory{Date: day, GameID: id},
				func(ctx context.Context) (*nhlapi.GameStory, error) {
					return a.NHLClient.GameStory(ctx, id)
				},
			); err != nil {
				return fmt.Errorf("game-story gameid %s: %w", id.String(), err)
			}
			return nil
		})
		g.Go(func() error {
			activity.RecordHeartbeat(gctx, nil)
			if _, _, err := shared.FetchOrCache(gctx, a.Storage, a.GobCache, resource.SeasonSeries{Date: day, GameID: id},
				func(ctx context.Context) (*nhlapi.SeasonSeriesMatchup, error) {
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
	return shared.ViperIntOrDefault(config.FlagGameDownloadConcurrency, config.DefaultGameDownloadConcurrency)
}

// shouldSkipGame returns true if the game should be skipped during processing.
func shouldSkipGame(game nhlapi.ScheduleGame) bool {
	if !game.GameState.IsFinal() {
		return true
	}
	gameType, err := game.ID.GameType()
	if err != nil {
		log.Warn().Str("game.ID", game.ID.String()).Err(err).Msg("Skipping game with invalid ID")
		return true
	}
	return nhlapi.GameType(gameType) == nhlapi.GameTypePreseason
}

// filterFinalNonPreseasonGames filters out preseason games and invalid game IDs.
func filterFinalNonPreseasonGames(games []nhlapi.ScheduleGame) []nhlapi.GameID {
	result := make([]nhlapi.GameID, 0, len(games))
	for _, g := range games {
		if shouldSkipGame(g) {
			continue
		}
		result = append(result, g.ID)
	}
	return result
}
