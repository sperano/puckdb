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
	"github.com/sperano/puckdb/internal/worker/yahoo"
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
	// GameKeys keeps the Yahoo game keys loaded so far; nil fetches the key
	// on every call.
	GameKeys *yahoo.GameKeys
}

// FetchDailyScheduleResult contains the result of fetching a daily schedule.
type FetchDailyScheduleResult struct {
	Origin core.DataOrigin
}

// FetchDailySchedule downloads the daily schedule and the cached files of
// every final, non-preseason game on it. A cached schedule still listing such
// a game that is not final yet (fetched before the game ended) is downloaded
// again, so the game's files are fetched once it is final. The game files are
// ensured even when the schedule was cached: an attempt that wrote the
// schedule and then failed a game download leaves that game's files missing,
// and its retry downloads them. With a cached schedule only missing game files
// are downloaded, so a fully cached day costs one stat per file.
func (a *DailyScheduleActivities) FetchDailySchedule(ctx context.Context, day time.Time) (FetchDailyScheduleResult, error) {
	logger := activity.GetLogger(ctx)

	schedule, origin, err := a.fetchSchedule(ctx, day)
	if err != nil {
		return FetchDailyScheduleResult{}, fmt.Errorf("download schedule: %w", err)
	}
	cached := origin != core.OriginRemoteNHLAPI
	if cached {
		logger.Debug("DailySchedule loaded from cache", "day", day, "origin", origin.String())
	} else {
		logger.Info("Daily schedule fetched from API", "day", day.Format(time.DateOnly))
	}

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(getGameDownloadConcurrency())
	for _, id := range filterFinalNonPreseasonGames(schedule.Games) {
		for _, file := range a.gameFiles(day, id) {
			g.Go(func() error {
				activity.RecordHeartbeat(gctx, nil)
				if err := file.ensure(gctx, cached); err != nil {
					return fmt.Errorf("%s gameid %s: %w", file.name, id.String(), err)
				}
				return nil
			})
		}
	}
	if err := g.Wait(); err != nil {
		return FetchDailyScheduleResult{}, err
	}
	return FetchDailyScheduleResult{Origin: origin}, nil
}

// fetchSchedule returns day's schedule from the cache, or from the API when
// it is not cached or the cached copy has a game that is not final yet. A
// failed re-download of such a cached copy falls back to it, unless ctx is
// done.
func (a *DailyScheduleActivities) fetchSchedule(ctx context.Context, day time.Time) (*nhlapi.DailySchedule, core.DataOrigin, error) {
	res := resource.DailySchedule{Date: day}
	download := func(ctx context.Context) (*nhlapi.DailySchedule, error) {
		return a.NHLClient.DailySchedule(ctx, nhlapi.FromDate(day))
	}
	schedule, origin, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, res, download)
	if err != nil || origin == core.OriginRemoteNHLAPI || !hasPendingGame(schedule.Games) {
		return schedule, origin, err
	}
	logger := activity.GetLogger(ctx)
	logger.Debug("Cached daily schedule has games not final yet, downloading it again", "day", day.Format(time.DateOnly))
	fresh, freshOrigin, err := shared.FetchAndCache(ctx, a.Storage, a.GobCache, res, download)
	if err == nil {
		return fresh, freshOrigin, nil
	}
	if ctx.Err() != nil {
		return nil, core.OriginUnknown, err
	}
	// The cached copy still lists every game that was final when it was
	// written; their files are ensured, and the pending games are left to
	// the next fetch, which retries the re-download.
	logger.Warn("Re-downloading a pending daily schedule failed, using the cached copy", "day", day.Format(time.DateOnly), "error", err)
	return schedule, origin, nil
}

// hasPendingGame reports whether games has a valid, non-preseason game that
// is not final yet, whose files FetchDailySchedule would skip. A postponed
// game keeps its original date's schedule pending, so that day's schedule is
// downloaded on every fetch.
func hasPendingGame(games []nhlapi.ScheduleGame) bool {
	for _, g := range games {
		if !g.GameState.IsFinal() && isTrackedGame(g.ID) {
			return true
		}
	}
	return false
}

// gameFile is one of the cached files FetchDailySchedule keeps for a final game.
type gameFile struct {
	name string
	// ensure downloads the file; with onlyMissing it does so only when the
	// file does not exist, without reading it.
	ensure func(ctx context.Context, onlyMissing bool) error
}

// gameFiles lists the cached files of game id on day.
func (a *DailyScheduleActivities) gameFiles(day time.Time, id nhlapi.GameID) []gameFile {
	c := a.NHLClient
	return []gameFile{
		{"boxscore", func(ctx context.Context, onlyMissing bool) error {
			return ensureGameFile(ctx, a, resource.Boxscore{Date: day, GameID: id}, onlyMissing,
				func(ctx context.Context) (*nhlapi.Boxscore, error) { return c.Boxscore(ctx, id) })
		}},
		{"play-by-play", func(ctx context.Context, onlyMissing bool) error {
			return ensureGameFile(ctx, a, resource.PlayByPlay{Date: day, GameID: id}, onlyMissing,
				func(ctx context.Context) (*nhlapi.PlayByPlay, error) { return c.PlayByPlay(ctx, id) })
		}},
		{"shift-chart", func(ctx context.Context, onlyMissing bool) error {
			return ensureGameFile(ctx, a, resource.ShiftChart{Date: day, GameID: id}, onlyMissing,
				func(ctx context.Context) (*nhlapi.ShiftChart, error) { return c.ShiftChart(ctx, id) })
		}},
		{"game-story", func(ctx context.Context, onlyMissing bool) error {
			return ensureGameFile(ctx, a, resource.GameStory{Date: day, GameID: id}, onlyMissing,
				func(ctx context.Context) (*nhlapi.GameStory, error) { return c.GameStory(ctx, id) })
		}},
		{"season-series", func(ctx context.Context, onlyMissing bool) error {
			return ensureGameFile(ctx, a, resource.SeasonSeries{Date: day, GameID: id}, onlyMissing,
				func(ctx context.Context) (*nhlapi.SeasonSeriesMatchup, error) { return c.SeasonSeries(ctx, id) })
		}},
	}
}

// ensureGameFile caches r through FetchOrCache, which also downloads a cached
// file that does not parse; with onlyMissing, an existing file is kept unread.
func ensureGameFile[T any](ctx context.Context, a *DailyScheduleActivities, r core.ReadWritable[T], onlyMissing bool, fetch func(context.Context) (T, error)) error {
	if onlyMissing && resource.Exists(ctx, a.Storage, r) {
		return nil
	}
	_, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, r, fetch)
	return err
}

func getGameDownloadConcurrency() int {
	return shared.ViperIntOrDefault(config.FlagGameDownloadConcurrency, config.DefaultGameDownloadConcurrency)
}

// shouldSkipGame returns true if the game should be skipped during processing.
func shouldSkipGame(game nhlapi.ScheduleGame) bool {
	return !game.GameState.IsFinal() || !isTrackedGame(game.ID)
}

// isTrackedGame reports whether id is a valid, non-preseason game ID.
func isTrackedGame(id nhlapi.GameID) bool {
	gameType, err := id.GameType()
	if err != nil {
		log.Warn().Str("game_id", id.String()).Err(err).Msg("Skipping game with invalid ID")
		return false
	}
	return nhlapi.GameType(gameType) != nhlapi.GameTypePreseason
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
