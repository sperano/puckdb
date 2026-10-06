package nhl

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
	nhlapi "github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
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

// --- Season teams ---

// ListSeasonTeams returns the team abbreviations for a season. Parent workflows
// use it to size per-team playoff progress bars; season child workflows use it
// to drive one fetch/import activity per team.
func (a *PlayoffActivities) ListSeasonTeams(ctx context.Context, startYear int) ([]string, error) {
	season := nhlapi.NewSeason(startYear)
	teams, err := a.Teams.GetSeasonTeamAbbrevs(ctx, int32(season.ID()))
	if err != nil {
		return nil, fmt.Errorf("get season teams: %w", err)
	}
	abbrevs := make([]string, len(teams))
	for i, t := range teams {
		abbrevs[i] = t.Abbrev
	}
	return abbrevs, nil
}

// --- Fetch ---

// FetchTeamPlayoffGamesInput contains the parameters for fetching one team's playoff games.
type FetchTeamPlayoffGamesInput struct {
	Season     int    // start year (e.g., 2024 for the 2024-2025 season)
	TeamAbbrev string // team whose club schedule (and home playoff games) to fetch
}

// FetchTeamPlayoffGamesResult contains the results of fetching one team's playoff games.
type FetchTeamPlayoffGamesResult struct {
	GamesFound   int `json:"gamesFound"`
	GamesFetched int `json:"gamesFetched"`
}

// playoffGame holds a playoff game ID with its date for cache paths.
type playoffGame struct {
	ID   nhlapi.GameID
	Date time.Time
}

// FetchTeamPlayoffGames fetches one team's club-schedule-season, extracts the
// team's final HOME playoff games, and downloads boxscore/pbp/shifts for each.
// Home games only: every playoff game has exactly one home team among the
// season's teams, so running this activity once per team covers each playoff
// game exactly once with no cross-activity dedup.
func (a *PlayoffActivities) FetchTeamPlayoffGames(ctx context.Context, input FetchTeamPlayoffGamesInput) (FetchTeamPlayoffGamesResult, error) {
	logger := activity.GetLogger(ctx)
	result := FetchTeamPlayoffGamesResult{}

	season := nhlapi.NewSeason(input.Season)
	res := resource.ClubScheduleSeason{Season: input.Season, TeamAbbrev: input.TeamAbbrev}

	// A cached schedule that still lists non-final games is incomplete by
	// definition — those games will change state, and playoff games are
	// appended as rounds get scheduled. Refetch it regardless of the calendar;
	// completed schedules (all games FINAL/OFF) stay cached forever. A
	// calendar-based gate (IsCurrentSeason) is wrong here: nhl.Current() rolls
	// over on July 1, which froze mid-playoff caches fetched in May once the
	// next sync ran in the offseason.
	if cached, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res); err == nil && scheduleIncomplete(cached) {
		log.Debug().Str("team", input.TeamAbbrev).Int("season", input.Season).
			Msg("Cached club schedule has non-final games, refetching")
		_ = resource.Delete(ctx, a.Storage, res)
		_ = a.GobCache.Delete(ctx, core.RedisKey(res))
	}

	activity.RecordHeartbeat(ctx, fmt.Sprintf("schedule:%s", input.TeamAbbrev))
	schedule, _, err := shared.FetchOrCache(ctx, a.Storage, a.GobCache, res,
		func(ctx context.Context) (*nhlapi.TeamScheduleResponse, error) {
			return a.NHLClient.ClubScheduleSeason(ctx, input.TeamAbbrev, season)
		})
	if errors.Is(err, nhlapi.ErrNotFound) {
		log.Debug().Str("team", input.TeamAbbrev).Int("season", input.Season).Msg("Club schedule not found, skipping")
		return result, nil
	}
	if err != nil {
		return result, fmt.Errorf("fetch club schedule for %s: %w", input.TeamAbbrev, err)
	}

	games := homePlayoffGames(schedule, input.TeamAbbrev)
	result.GamesFound = len(games)

	for _, pg := range games {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("playoff:%s", pg.ID.String()))
		if err := fetchGameData(ctx, a.Storage, a.GobCache, a.NHLClient, pg.ID, pg.Date); err != nil {
			return result, err
		}
		result.GamesFetched++
	}

	logger.Debug("Fetched team playoff games", "season", input.Season, "team", input.TeamAbbrev, "games", len(games))
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

// ImportTeamPlayoffGamesInput contains the parameters for importing one team's playoff games.
type ImportTeamPlayoffGamesInput struct {
	Season     int    // start year (e.g., 2024 for the 2024-2025 season)
	TeamAbbrev string // team whose home playoff games to import
}

// ImportTeamPlayoffGamesResult contains the results of importing one team's playoff games.
type ImportTeamPlayoffGamesResult struct {
	GamesImported   int               `json:"gamesImported"`
	SkatersImported int               `json:"skatersImported"`
	GoaliesImported int               `json:"goaliesImported"`
	Origins         core.OriginCounts `json:"origins"`
}

// ImportTeamPlayoffGames reads one team's cached club-schedule-season file,
// extracts the team's final HOME playoff games (same partition as
// FetchTeamPlayoffGames), and imports each game's boxscore data to the database.
// A missing schedule file mirrors the fetch-side ErrNotFound skip: the fetch
// activity writes no file when the NHL API has no schedule for that team.
func (a *PlayoffActivities) ImportTeamPlayoffGames(ctx context.Context, input ImportTeamPlayoffGamesInput) (ImportTeamPlayoffGamesResult, error) {
	logger := activity.GetLogger(ctx)
	result := ImportTeamPlayoffGamesResult{Origins: core.OriginCounts{}}

	res := resource.ClubScheduleSeason{Season: input.Season, TeamAbbrev: input.TeamAbbrev}
	if !resource.Exists(ctx, a.Storage, res) {
		log.Warn().Str("team", input.TeamAbbrev).Int("season", input.Season).
			Msg("Club schedule not cached, skipping playoff import for team")
		return result, nil
	}

	schedule, _, err := a.GobCache.ReadParsedCached(ctx, a.Storage, res)
	if err != nil {
		return result, fmt.Errorf("read club schedule for %s: %w", input.TeamAbbrev, err)
	}

	for _, pg := range homePlayoffGames(schedule, input.TeamAbbrev) {
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

	logger.Debug("Imported team playoff games",
		"season", input.Season,
		"team", input.TeamAbbrev,
		"games", result.GamesImported)

	return result, nil
}

// --- Helpers ---

// collectPlayoffGames reads cached club-schedule-season files for all teams
// and returns a deduplicated list of final playoff games. Used by
// extractPlayoffPlayers, which needs the season-wide game list in one pass.
func collectPlayoffGames(ctx context.Context, storage store.Storage, gobCache *cache.GobCache, season int) ([]playoffGame, error) {
	seen := make(map[nhlapi.GameID]bool)
	var games []playoffGame

	scheduleDir := fmt.Sprintf("seasons/%d/club-schedule", season)
	files, err := store.WithFileType(storage, core.ClubScheduleSeasonResource).List(ctx, scheduleDir, "json")
	if err != nil {
		// FetchTeamPlayoffGames is expected to have populated this directory
		// before import runs. A missing directory means the fetch step never ran
		// for this season or wrote to a different mount — both pathologies the
		// operator should see, not silent zero-game imports.
		return nil, fmt.Errorf("list club schedules for season %d: %w", season, err)
	}

	for _, filename := range files {
		abbrev := extractTeamAbbrev(filename)
		if abbrev == "" {
			continue
		}

		res := resource.ClubScheduleSeason{Season: season, TeamAbbrev: abbrev}
		schedule, _, err := gobCache.ReadParsedCached(ctx, storage, res)
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
	abbrev, ok := strings.CutPrefix(filename, "club-schedule-")
	if !ok || abbrev == "" {
		return ""
	}
	return abbrev
}

// scheduleIncomplete reports whether a club schedule still lists games that
// haven't reached a final state (FUT/PRE/LIVE/postponed/...). Such a schedule
// will change on the NHL side and must not be served from cache indefinitely.
func scheduleIncomplete(schedule *nhlapi.TeamScheduleResponse) bool {
	for _, game := range schedule.Games {
		if !game.GameState.IsFinal() {
			return true
		}
	}
	return false
}

// homePlayoffGames extracts a team's final playoff HOME games from its schedule.
// Restricting to home games partitions the season's playoff games across teams:
// each game is returned by exactly one team's schedule.
func homePlayoffGames(schedule *nhlapi.TeamScheduleResponse, teamAbbrev string) []playoffGame {
	var games []playoffGame
	for _, game := range schedule.Games {
		if game.GameType != nhlapi.GameTypePlayoffs {
			continue
		}
		if !game.GameState.IsFinal() {
			continue
		}
		if game.HomeTeam.Abbrev != teamAbbrev {
			continue
		}
		gameDate, err := parseGameDate(game)
		if err != nil {
			log.Warn().Str("gameID", game.ID.String()).Err(err).Msg("Skipping playoff game with unparseable date")
			continue
		}
		games = append(games, playoffGame{ID: game.ID, Date: gameDate})
	}
	return games
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
