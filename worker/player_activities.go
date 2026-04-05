package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// PlayerCareerUpserter is the interface for player career database operations.
type PlayerCareerUpserter interface {
	UpsertPlayerAwardBatch(ctx context.Context, arg []sqlcdb.UpsertPlayerAwardBatchParams) *sqlcdb.UpsertPlayerAwardBatchBatchResults
	UpsertPlayerSeasonTotalBatch(ctx context.Context, arg []sqlcdb.UpsertPlayerSeasonTotalBatchParams) *sqlcdb.UpsertPlayerSeasonTotalBatchBatchResults
}

// PlayerActivities groups player-related activities with their dependencies.
type PlayerActivities struct {
	Storage      store.Storage
	NHLClient    NHLClient
	RedisClient  cache.Client
	GobCache     *cache.GobCache
	Queries      PlayerUpserter
	CareerQueries PlayerCareerUpserter
}

// --- Player landing activities ---

// FetchPlayerLandingsBatch fetches player landing pages for a batch of players.
func (a *PlayerActivities) FetchPlayerLandingsBatch(
	ctx context.Context,
	players []store.BoxscorePlayer,
) (FetchStats, error) {
	result := FetchStats{}

	for _, p := range players {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		activity.RecordHeartbeat(ctx, p.ID)
		playerID := nhl.PlayerID(p.ID)
		status, err := a.ensurePlayerLandingCached(ctx, playerID, p)
		if err != nil {
			log.Error().Err(err).Int64("player_id", p.ID).Msg("Failed to download player landing")
			return result, err
		}

		switch status {
		case playerLandingCached:
			result.CacheHits++
			metrics.LegacyIncDownload("PlayerLanding", metrics.ResultHit)
		case playerLandingDownloaded:
			result.Downloaded++
			metrics.LegacyIncDownload("PlayerLanding", metrics.ResultMiss)
		case playerLandingMissing:
			result.Missing++
			metrics.LegacyIncDownload("PlayerLanding", metrics.ResultMissing)
		}
	}

	log.Debug().
		Int("downloaded", result.Downloaded).
		Int("cache_hits", result.CacheHits).
		Int("missing", result.Missing).
		Int("batch_size", len(players)).
		Msg("Player landing batch complete")

	return result, nil
}

type playerLandingStatus int

const (
	playerLandingDownloaded playerLandingStatus = iota
	playerLandingCached
	playerLandingMissing
)

// ensurePlayerLandingCached downloads player landing data if not already cached.
// Returns a status indicating whether data was downloaded, already cached, or missing (404).
func (a *PlayerActivities) ensurePlayerLandingCached(
	ctx context.Context,
	playerID nhl.PlayerID,
	boxscorePlayer store.BoxscorePlayer,
) (playerLandingStatus, error) {
	missingRes := resource.MissingPlayerLanding{PlayerID: playerID}
	landingRes := resource.PlayerLanding{PlayerID: playerID}

	// Check if already marked as missing (most common case for 404s)
	if a.Storage.Exists(missingRes.Path()) {
		log.Debug().Str("player_id", playerID.String()).Msg("Player landing already marked as missing")
		return playerLandingMissing, nil
	}

	// Check if landing page is already cached
	if a.Storage.Exists(landingRes.Path()) {
		log.Debug().Str("player_id", playerID.String()).Msg("Player landing already cached")
		return playerLandingCached, nil
	}

	// Fetch from API
	landing, err := a.NHLClient.PlayerLanding(ctx, playerID)
	if err != nil {
		// Check if this is a 404 error
		if errors.Is(err, nhl.ErrNotFound) {
			// Cache the 404 with boxscore player data
			missingInfo := store.MissingPlayerLandingData{
				FirstName: boxscorePlayer.FirstName,
				LastName:  boxscorePlayer.LastName,
				Position:  boxscorePlayer.Position,
			}
			if saveErr := resource.WriteParsed(a.Storage, missingRes, &missingInfo); saveErr != nil {
				log.Warn().Err(saveErr).Str("player_id", playerID.String()).Msg("Failed to save missing player landing")
			} else {
				log.Info().Str("player_id", playerID.String()).Msg("Saved player as missing (404)")
			}
			return playerLandingMissing, nil
		}
		return 0, err
	}
	// Save successful response to cache
	if err := resource.WriteParsed(a.Storage, landingRes, landing); err != nil {
		return 0, fmt.Errorf("write player %s landing to cache: %w", playerID.String(), err)
	}
	return playerLandingDownloaded, nil
}

// --- Boxscore player loading activities ---

// LoadSeasonBoxscorePlayers reads the cached boxscore player list from Redis
// for a single season.
func (a *PlayerActivities) LoadSeasonBoxscorePlayers(ctx context.Context, seasonStartYear int) ([]store.BoxscorePlayer, error) {
	season := nhl.NewSeason(seasonStartYear)
	players, err := cache.LoadBoxscorePlayers(ctx, a.RedisClient, season)
	if err != nil {
		return nil, fmt.Errorf("load boxscore players for %d: %w", season.ID(), err)
	}
	return players, nil
}

// LoadAllBoxscorePlayers loads the consolidated player set from Redis.
func (a *PlayerActivities) LoadAllBoxscorePlayers(ctx context.Context) ([]store.BoxscorePlayer, error) {
	return cache.LoadAllBoxscorePlayers(ctx, a.RedisClient)
}

// CountPlayersForAllSeasons returns the player count for each season from Redis.
// Keys are season IDs (e.g. 20242025), values are player counts.
func (a *PlayerActivities) CountPlayersForAllSeasons(ctx context.Context, seasonStartYears []int) (map[int]int, error) {
	counts := make(map[int]int, len(seasonStartYears))
	for _, startYear := range seasonStartYears {
		season := nhl.NewSeason(startYear)
		players, err := cache.LoadBoxscorePlayers(ctx, a.RedisClient, season)
		if err != nil {
			return nil, fmt.Errorf("load boxscore players for %d: %w", season.ID(), err)
		}
		counts[startYear] = len(players)
	}
	return counts, nil
}

// CountPlayersForSeason reads the cached boxscore player list from Redis
// and returns the count.
func CountPlayersForSeason(ctx context.Context, client cache.Client, season nhl.Season) (int, error) {
	players, err := cache.LoadBoxscorePlayers(ctx, client, season)
	if err != nil {
		return 0, fmt.Errorf("load boxscore players for %d: %w", season.ID(), err)
	}
	return len(players), nil
}

// --- Player game log activities ---

// DownloadPlayerGameLogsInput specifies which player game logs to download.
type DownloadPlayerGameLogsInput struct {
	PlayerIDs      []int64 // NHL player IDs
	StartSeason    int     // Season start year (e.g., 2024 for 2024-2025 season)
	GameTypes      []int   // Game types to download (2=regular season, 3=playoffs)
	RefreshCurrent bool    // If true, overwrite files for the current season
	TotalPlayers   int     // Total players across all batches (for Redis progress reporting)
}

// DownloadPlayerGameLogsResult contains download statistics.
type DownloadPlayerGameLogsResult struct {
	Players    int // Number of players processed in this batch
	Downloaded int
	CacheHits  int
	Skipped    int // Files skipped for current season (not refreshing)
	Errors     []string
}

// DownloadPlayerGameLogsBatch downloads player game logs for a batch of players.
func (a *PlayerActivities) DownloadPlayerGameLogsBatch(ctx context.Context, input DownloadPlayerGameLogsInput) (*DownloadPlayerGameLogsResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadPlayerGameLogsBatch", time.Since(start))
	}()

	result := &DownloadPlayerGameLogsResult{
		Players: len(input.PlayerIDs),
	}

	if len(input.PlayerIDs) == 0 {
		return result, nil
	}

	// Default to regular season if no game types specified
	gameTypes := input.GameTypes
	if len(gameTypes) == 0 {
		gameTypes = []int{nhl.GameTypeRegularSeason.Int()}
	}

	season := nhl.NewSeason(input.StartSeason)
	isCurrent := isCurrentSeason(input.StartSeason)

	for _, playerID := range input.PlayerIDs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		activity.RecordHeartbeat(ctx, playerID)
		pid := nhl.PlayerID(playerID)

		for _, gt := range gameTypes {
			select {
			case <-ctx.Done():
				return result, ctx.Err()
			default:
			}

			gameType, err := nhl.GameTypeFromInt(gt)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("player %d: invalid game type %d", playerID, gt))
				continue
			}

			opts := gameLogDownloadOptions{
				isCurrent:      isCurrent,
				refreshCurrent: input.RefreshCurrent,
			}
			status, err := a.downloadPlayerGameLogToCache(ctx, pid, season, gameType, opts)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("player %d season %s %s: %v", playerID, season, gameType, err))
				continue
			}

			switch status {
			case gameLogDownloaded:
				result.Downloaded++
			case gameLogCached:
				result.CacheHits++
			case gameLogSkipped:
				result.Skipped++
			}
		}
	}

	log.Info().
		Int("players", len(input.PlayerIDs)).
		Int("startSeason", input.StartSeason).
		Int("downloaded", result.Downloaded).
		Int("cacheHits", result.CacheHits).
		Int("skipped", result.Skipped).
		Int("errors", len(result.Errors)).
		Msg("Download player game logs batch complete")

	return result, nil
}

// gameLogDownloadStatus indicates the result of a download attempt.
type gameLogDownloadStatus int

const (
	gameLogDownloaded gameLogDownloadStatus = iota
	gameLogCached
	gameLogSkipped
)

// gameLogDownloadOptions controls download behavior.
type gameLogDownloadOptions struct {
	isCurrent      bool // True if this is the current season
	refreshCurrent bool // True if current season files should be overwritten
}

// downloadPlayerGameLogToCache downloads a single player game log to cache.
func (a *PlayerActivities) downloadPlayerGameLogToCache(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType, opts gameLogDownloadOptions) (gameLogDownloadStatus, error) {
	gameTypeID := gameType.Int()
	gameLogRes := resource.PlayerGameLog{PlayerID: playerID, Season: season, GameType: gameTypeID}
	fileExists := a.Storage.Exists(gameLogRes.Path())

	if fileExists {
		// Current season with refresh disabled: skip
		if opts.isCurrent && !opts.refreshCurrent {
			log.Debug().
				Str("playerID", playerID.String()).
				Str("season", season.String()).
				Str("gameType", gameType.String()).
				Msg("Current season player log exists, not refreshing")
			metrics.LegacyIncDownload("PlayerGameLog", metrics.ResultSkip)
			return gameLogSkipped, nil
		}

		// Historical season: use cache
		if !opts.isCurrent {
			log.Debug().
				Str("playerID", playerID.String()).
				Str("season", season.String()).
				Str("gameType", gameType.String()).
				Msg("PlayerGameLog already cached")
			metrics.LegacyIncDownload("PlayerGameLog", metrics.ResultHit)
			return gameLogCached, nil
		}

		// Current season with refresh enabled: fall through to download
		log.Debug().
			Str("playerID", playerID.String()).
			Str("season", season.String()).
			Str("gameType", gameType.String()).
			Msg("Refreshing current season player log")
	}

	// Fetch from NHL API and marshal to JSON
	gameLog, err := a.NHLClient.PlayerGameLog(ctx, playerID, season, gameType)
	if err != nil {
		metrics.LegacyIncDownload("PlayerGameLog", metrics.ResultError)
		return gameLogDownloaded, fmt.Errorf("download: %w", err)
	}

	content, err := json.Marshal(gameLog)
	if err != nil {
		metrics.LegacyIncDownload("PlayerGameLog", metrics.ResultError)
		return gameLogDownloaded, fmt.Errorf("marshal player %s season %s: %w", playerID, season, err)
	}

	if err := a.Storage.Write(gameLogRes.Path(), content); err != nil {
		metrics.LegacyIncDownload("PlayerGameLog", metrics.ResultError)
		return gameLogDownloaded, fmt.Errorf("save: %w", err)
	}

	log.Info().
		Str("playerID", playerID.String()).
		Str("season", season.String()).
		Str("gameType", gameType.String()).
		Str("path", gameLogRes.Path()).
		Msg("Saved player game log")
	metrics.LegacyIncDownload("PlayerGameLog", metrics.ResultMiss)

	return gameLogDownloaded, nil
}

// isCurrentSeason returns true if the given start year represents the current NHL season.
// NHL seasons run from October to June, so the current season's start year is:
// - The current year if we're in Oct-Dec
// - The previous year if we're in Jan-June
func isCurrentSeason(startYear int) bool {
	now := time.Now()
	currentYear := now.Year()
	month := now.Month()

	var currentSeasonStartYear int
	if month >= time.October {
		currentSeasonStartYear = currentYear
	} else {
		currentSeasonStartYear = currentYear - 1
	}

	return startYear == currentSeasonStartYear
}
