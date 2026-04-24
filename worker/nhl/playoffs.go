package nhl

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/config"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/activity"
	"golang.org/x/sync/errgroup"
)

// PlayoffTeamQuerier provides access to season team abbreviations for playoff fetching.
type PlayoffTeamQuerier interface {
	GetSeasonTeamAbbrevs(ctx context.Context, seasonID int32) ([]sqlcdb.GetSeasonTeamAbbrevsRow, error)
}

// PlayoffActivities holds dependencies for fetching and importing playoff game data.
type PlayoffActivities struct {
	Storage   store.Storage
	GobCache  *cache.GobCache
	NHLClient shared.NHLClient
	Teams     PlayoffTeamQuerier
	Queries   BoxscoreUpserter
}

// --- Fetch ---

// FetchPlayoffGamesInput contains the parameters for fetching playoff game data.
type FetchPlayoffGamesInput struct {
	Season int // start year (e.g., 2024 for the 2024-2025 season)
}

// FetchPlayoffGamesResult contains the results of fetching playoff game data.
type FetchPlayoffGamesResult struct {
	TeamsChecked int `json:"teamsChecked"`
	GamesFound   int `json:"gamesFound"`
	GamesFetched int `json:"gamesFetched"`
}

// playoffGame holds a deduplicated playoff game ID with its date for cache paths.
type playoffGame struct {
	ID   nhlapi.GameID
	Date time.Time
}

// FetchPlayoffGames fetches club-schedule-season for all teams in a season,
// extracts playoff game IDs, and downloads boxscore/pbp/shifts for each.
func (a *PlayoffActivities) FetchPlayoffGames(ctx context.Context, input FetchPlayoffGamesInput) (FetchPlayoffGamesResult, error) {
	logger := activity.GetLogger(ctx)
	result := FetchPlayoffGamesResult{}

	season := nhlapi.NewSeason(input.Season)
	teams, err := a.Teams.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return result, fmt.Errorf("get season teams: %w", err)
	}
	result.TeamsChecked = len(teams)

	// For the current season, invalidate cached club schedules so we pick up
	// newly finalized playoff games. Historical seasons keep their cache hits.
	invalidateSchedules := shared.IsCurrentSeason(input.Season)

	// Fetch club-schedule-season for each team and collect playoff game IDs
	seen := make(map[nhlapi.GameID]bool)
	var playoffGames []playoffGame

	for _, team := range teams {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("schedule:%s", team.Abbrev))

		res := resource.ClubScheduleSeason{Season: input.Season, TeamAbbrev: team.Abbrev}
		if invalidateSchedules {
			_ = a.Storage.Delete(ctx, res.Path())
			_ = a.GobCache.Delete(ctx, core.RedisKey(res))
		}
		schedule, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, res,
			func(ctx context.Context) (*nhlapi.TeamScheduleResponse, error) {
				return a.NHLClient.ClubScheduleSeason(ctx, team.Abbrev, season)
			})
		if errors.Is(err, nhlapi.ErrNotFound) {
			log.Debug().Str("team", team.Abbrev).Int("season", input.Season).Msg("Club schedule not found, skipping")
			continue
		}
		if err != nil {
			return result, fmt.Errorf("fetch club schedule for %s: %w", team.Abbrev, err)
		}

		for _, game := range schedule.Games {
			if game.GameType != nhlapi.GameTypePlayoffs {
				continue
			}
			if !game.GameState.IsFinal() {
				continue
			}
			if seen[game.ID] {
				continue
			}
			seen[game.ID] = true

			gameDate, err := parseGameDate(game)
			if err != nil {
				log.Warn().Str("gameID", game.ID.String()).Err(err).Msg("Skipping playoff game with unparseable date")
				continue
			}
			playoffGames = append(playoffGames, playoffGame{ID: game.ID, Date: gameDate})
		}
	}

	result.GamesFound = len(playoffGames)
	if len(playoffGames) == 0 {
		logger.Info("No playoff games found", "season", input.Season)
		return result, nil
	}

	// Download game data for each playoff game
	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(getGameDownloadConcurrency())

	for _, pg := range playoffGames {
		g.Go(func() error {
			activity.RecordHeartbeat(gctx, fmt.Sprintf("playoff:%s", pg.ID.String()))
			return fetchGameData(gctx, a.Storage, a.GobCache, a.NHLClient, pg.ID, pg.Date)
		})
	}
	if err := g.Wait(); err != nil {
		return result, err
	}

	result.GamesFetched = len(playoffGames)
	logger.Info("Fetched playoff games", "season", input.Season, "teams", len(teams), "games", len(playoffGames))
	return result, nil
}

// fetchGameData downloads boxscore, play-by-play, shift chart, game story, and season series
// for a single game via FetchOrCache.
func fetchGameData(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, client shared.NHLClient, gameID nhlapi.GameID, date time.Time) error {
	if _, _, err := shared.FetchOrCache(ctx, storage, gobCache, resource.Boxscore{Date: date, GameID: gameID},
		func(ctx context.Context) (*nhlapi.Boxscore, error) {
			return client.Boxscore(ctx, gameID)
		}); err != nil {
		return fmt.Errorf("boxscore %s: %w", gameID, err)
	}
	if _, _, err := shared.FetchOrCache(ctx, storage, gobCache, resource.PlayByPlay{Date: date, GameID: gameID},
		func(ctx context.Context) (*nhlapi.PlayByPlay, error) {
			return client.PlayByPlay(ctx, gameID)
		}); err != nil {
		return fmt.Errorf("play-by-play %s: %w", gameID, err)
	}
	if _, _, err := shared.FetchOrCache(ctx, storage, gobCache, resource.ShiftChart{Date: date, GameID: gameID},
		func(ctx context.Context) (*nhlapi.ShiftChart, error) {
			return client.ShiftChart(ctx, gameID)
		}); err != nil {
		return fmt.Errorf("shift-chart %s: %w", gameID, err)
	}
	if _, _, err := shared.FetchOrCache(ctx, storage, gobCache, resource.GameStory{Date: date, GameID: gameID},
		func(ctx context.Context) (*nhlapi.GameStory, error) {
			return client.GameStory(ctx, gameID)
		}); err != nil {
		return fmt.Errorf("game-story %s: %w", gameID, err)
	}
	if _, _, err := shared.FetchOrCache(ctx, storage, gobCache, resource.SeasonSeries{Date: date, GameID: gameID},
		func(ctx context.Context) (*nhlapi.SeasonSeriesMatchup, error) {
			return client.SeasonSeries(ctx, gameID)
		}); err != nil {
		// The NHL API returns persistent 500s for right-rail on COVID bubble game IDs
		// (2019-2020 qualifying round and some round 2 series) due to the non-standard
		// playoff format. Skip server errors for that season only.
		covidBubbleSeason := nhlapi.NewSeason(2019)
		season, _ := gameID.Season()
		if errors.Is(err, nhlapi.ErrServerError) && season == covidBubbleSeason {
			log.Error().Err(err).Str("gameID", gameID.String()).
				Msg("Season series unavailable due to COVID bubble format, skipping")
		} else {
			return fmt.Errorf("season-series %s: %w", gameID, err)
		}
	}
	return nil
}

// --- Import ---

// ImportPlayoffGamesInput contains the parameters for importing playoff games.
type ImportPlayoffGamesInput struct {
	Season int // start year (e.g., 2024 for the 2024-2025 season)
}

// ImportPlayoffGamesResult contains the results of importing playoff games.
type ImportPlayoffGamesResult struct {
	GamesImported   int               `json:"gamesImported"`
	SkatersImported int               `json:"skatersImported"`
	GoaliesImported int               `json:"goaliesImported"`
	Origins         core.OriginCounts `json:"origins"`
}

// ImportPlayoffGames reads cached club-schedule-season files, extracts playoff game IDs,
// and imports each game's boxscore data to the database.
func (a *PlayoffActivities) ImportPlayoffGames(ctx context.Context, input ImportPlayoffGamesInput) (ImportPlayoffGamesResult, error) {
	logger := activity.GetLogger(ctx)
	result := ImportPlayoffGamesResult{Origins: core.OriginCounts{}}

	playoffGames, err := collectPlayoffGames(ctx, a.Storage, a.GobCache, input.Season)
	if err != nil {
		return result, err
	}

	if len(playoffGames) == 0 {
		logger.Info("No playoff games to import", "season", input.Season)
		return result, nil
	}

	for _, pg := range playoffGames {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("import-playoff:%s", pg.ID.String()))

		gameResult, err := importSingleGame(ctx, a.Storage, a.GobCache, a.Queries, pg.ID, pg.Date, input.Season)
		if err != nil {
			return result, fmt.Errorf("import playoff game %s: %w", pg.ID, err)
		}
		result.Origins.Add(gameResult.Origins)
		result.GamesImported++
		result.SkatersImported += gameResult.SkatersImported
		result.GoaliesImported += gameResult.GoaliesImported
	}

	logger.Info("Imported playoff games",
		"season", input.Season,
		"games", result.GamesImported,
		"skaters", result.SkatersImported,
		"goalies", result.GoaliesImported)

	return result, nil
}

// --- Helpers ---

// collectPlayoffGames reads cached club-schedule-season files for all teams
// and returns a deduplicated list of final playoff games.
func collectPlayoffGames(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, season int) ([]playoffGame, error) {
	seen := make(map[nhlapi.GameID]bool)
	var games []playoffGame

	scheduleDir := fmt.Sprintf("seasons/%d/club-schedule", season)
	files, err := storage.List(ctx, scheduleDir, "json")
	if err != nil {
		// FetchPlayoffGames is expected to have populated this directory before
		// import runs. A missing directory means the fetch step never ran for
		// this season or wrote to a different mount — both pathologies the
		// operator should see, not silent zero-game imports.
		return nil, fmt.Errorf("list club schedules for season %d: %w", season, err)
	}

	for _, filename := range files {
		abbrev := extractTeamAbbrev(filename)
		if abbrev == "" {
			continue
		}

		res := resource.ClubScheduleSeason{Season: season, TeamAbbrev: abbrev}
		schedule, _, err := cache.ReadParsedCached(ctx, storage, gobCache, res)
		if err != nil {
			return nil, fmt.Errorf("read club schedule for %s: %w", abbrev, err)
		}

		for _, game := range schedule.Games {
			if game.GameType != nhlapi.GameTypePlayoffs {
				continue
			}
			if !game.GameState.IsFinal() {
				continue
			}
			if seen[game.ID] {
				continue
			}
			seen[game.ID] = true

			gameDate, err := parseGameDate(game)
			if err != nil {
				continue
			}
			games = append(games, playoffGame{ID: game.ID, Date: gameDate})
		}
	}

	return games, nil
}

// extractTeamAbbrev extracts the team abbreviation from a club-schedule filename.
// storage.List strips the extension, so input is "club-schedule-TOR" (no .json).
func extractTeamAbbrev(filename string) string {
	const prefix = "club-schedule-"
	if len(filename) <= len(prefix) {
		return ""
	}
	if filename[:len(prefix)] != prefix {
		return ""
	}
	return filename[len(prefix):]
}

// parseGameDate extracts a time.Time from a ScheduleGame's GameDate or StartTimeUTC.
func parseGameDate(game nhlapi.ScheduleGame) (time.Time, error) {
	if game.GameDate != nil && *game.GameDate != "" {
		t, err := time.Parse(config.DateFormat, *game.GameDate)
		if err == nil {
			return t, nil
		}
	}
	if game.StartTimeUTC != "" {
		t, err := time.Parse(time.RFC3339, game.StartTimeUTC)
		if err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("no parseable date for game %s", game.ID)
}
