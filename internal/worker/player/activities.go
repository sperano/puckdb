package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/go-redis/redis/v8"
	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/sqlcdb"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
)

// PlayerCareerUpserter is the interface for player career database operations.
type PlayerCareerUpserter interface {
	UpsertPlayerAwardBatch(ctx context.Context, arg []sqlcdb.UpsertPlayerAwardBatchParams) *sqlcdb.UpsertPlayerAwardBatchBatchResults
	UpsertPlayerSeasonTotalBatch(ctx context.Context, arg []sqlcdb.UpsertPlayerSeasonTotalBatchParams) *sqlcdb.UpsertPlayerSeasonTotalBatchBatchResults
	UpsertInternationalSeasonTeam(ctx context.Context, arg sqlcdb.UpsertInternationalSeasonTeamParams) error
}

// Activities groups player-related activities with their dependencies.
type Activities struct {
	Storage       store.Storage
	NHLClient     shared.NHLClient
	RedisClient   *redis.Client
	GobCache      *cache.GobCache
	Queries       PlayerUpserter
	CareerQueries PlayerCareerUpserter
}

// --- Player landing activities ---

// FetchPlayerLandingsBatch fetches player landing pages for a batch of players.
func (a *Activities) FetchPlayerLandingsBatch(
	ctx context.Context,
	players []store.BoxscorePlayer,
) (shared.FetchStats, error) {
	result := shared.FetchStats{}

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
			metrics.IncDownload(core.PlayerLanding, metrics.ResultHit)
		case playerLandingDownloaded:
			result.Downloaded++
			metrics.IncDownload(core.PlayerLanding, metrics.ResultMiss)
		case playerLandingMissing:
			result.Missing++
			metrics.IncDownload(core.PlayerLanding, metrics.ResultMissing)
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
func (a *Activities) ensurePlayerLandingCached(
	ctx context.Context,
	playerID nhl.PlayerID,
	boxscorePlayer store.BoxscorePlayer,
) (playerLandingStatus, error) {
	missingRes := resource.MissingPlayerLanding{PlayerID: playerID}
	landingRes := resource.PlayerLanding{PlayerID: playerID}

	if a.Storage.Exists(ctx, missingRes.Path()) {
		log.Debug().Str("player_id", playerID.String()).Msg("Player landing already marked as missing")
		return playerLandingMissing, nil
	}

	if a.Storage.Exists(ctx, landingRes.Path()) {
		log.Debug().Str("player_id", playerID.String()).Msg("Player landing already cached")
		return playerLandingCached, nil
	}

	landing, err := a.NHLClient.PlayerLanding(ctx, playerID)
	if err != nil {
		if errors.Is(err, nhl.ErrNotFound) {
			missingInfo := store.MissingPlayerLandingData{
				FirstName: boxscorePlayer.FirstName,
				LastName:  boxscorePlayer.LastName,
				Position:  boxscorePlayer.Position,
			}
			if saveErr := resource.WriteParsed(ctx, a.Storage, missingRes, &missingInfo); saveErr != nil {
				log.Warn().Err(saveErr).Str("player_id", playerID.String()).Msg("Failed to save missing player landing")
			} else {
				log.Info().Str("player_id", playerID.String()).Msg("Saved player as missing (404)")
			}
			return playerLandingMissing, nil
		}
		return 0, err
	}
	if err := resource.WriteParsed(ctx, a.Storage, landingRes, landing); err != nil {
		return 0, fmt.Errorf("write player %s landing to cache: %w", playerID.String(), err)
	}
	return playerLandingDownloaded, nil
}

// --- Boxscore player loading activities ---

// LoadSeasonBoxscorePlayers reads the cached boxscore player list from Redis
// for a single season.
func (a *Activities) LoadSeasonBoxscorePlayers(ctx context.Context, seasonStartYear int) ([]store.BoxscorePlayer, error) {
	season := nhl.NewSeason(seasonStartYear)
	players, err := cache.LoadBoxscorePlayers(ctx, a.RedisClient, season)
	if err != nil {
		return nil, fmt.Errorf("load boxscore players for %d: %w", season.ID(), err)
	}
	return players, nil
}

// LoadAllBoxscorePlayers loads the consolidated player set from Redis.
func (a *Activities) LoadAllBoxscorePlayers(ctx context.Context) ([]store.BoxscorePlayer, error) {
	return cache.LoadAllBoxscorePlayers(ctx, a.RedisClient)
}

// CountPlayersForAllSeasons returns the player count for each season from Redis.
// Keys are season IDs (e.g. 20242025), values are player counts.
func (a *Activities) CountPlayersForAllSeasons(ctx context.Context, seasonStartYears []int) (map[int]int, error) {
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
func CountPlayersForSeason(ctx context.Context, client *redis.Client, season nhl.Season) (int, error) {
	players, err := cache.LoadBoxscorePlayers(ctx, client, season)
	if err != nil {
		return 0, fmt.Errorf("load boxscore players for %d: %w", season.ID(), err)
	}
	return len(players), nil
}

// --- Player game log activities ---

// DownloadPlayerGameLogsInput specifies which player game logs to download.
type DownloadPlayerGameLogsInput struct {
	PlayerIDs   []int64 // NHL player IDs
	StartSeason int     // Season start year (e.g., 2024 for 2024-2025 season)
	GameTypes   []int   // Game types to download (2=regular season, 3=playoffs)
	// RefreshCurrent overwrites cached files for this season. The workflow
	// decides which seasons get it (explicit season range, or the latest
	// season) — see FetchPlayerLogsWorkflow; activities must not gate it on
	// IsCurrentSeason, whose July-1 rollover froze just-ended seasons.
	RefreshCurrent bool
	TotalPlayers   int // Total players across all batches (for Redis progress reporting)
}

// DownloadPlayerGameLogsResult contains download statistics.
type DownloadPlayerGameLogsResult struct {
	Players    int // Number of players processed in this batch
	Downloaded int
	CacheHits  int
	Errors     []string
}

// DownloadPlayerGameLogsBatch downloads player game logs for a batch of players.
func (a *Activities) DownloadPlayerGameLogsBatch(ctx context.Context, input DownloadPlayerGameLogsInput) (*DownloadPlayerGameLogsResult, error) {
	defer metrics.TrackActivityDuration("DownloadPlayerGameLogsBatch")()

	result := &DownloadPlayerGameLogsResult{
		Players: len(input.PlayerIDs),
	}

	if len(input.PlayerIDs) == 0 {
		return result, nil
	}

	gameTypes := input.GameTypes
	if len(gameTypes) == 0 {
		gameTypes = []int{nhl.GameTypeRegularSeason.Int()}
	}

	season := nhl.NewSeason(input.StartSeason)

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

			opts := gameLogDownloadOptions{refresh: input.RefreshCurrent}
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
			}
		}
	}

	log.Info().
		Int("players", len(input.PlayerIDs)).
		Int("startSeason", input.StartSeason).
		Int("downloaded", result.Downloaded).
		Int("cacheHits", result.CacheHits).
		Int("errors", len(result.Errors)).
		Msg("Download player game logs batch complete")

	return result, nil
}

// gameLogDownloadStatus indicates the result of a download attempt.
type gameLogDownloadStatus int

const (
	gameLogDownloaded gameLogDownloadStatus = iota
	gameLogCached
)

// gameLogDownloadOptions controls download behavior.
type gameLogDownloadOptions struct {
	refresh bool // True if cached files for this season should be overwritten
}

// downloadPlayerGameLogToCache downloads a single player game log to cache.
func (a *Activities) downloadPlayerGameLogToCache(ctx context.Context, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType, opts gameLogDownloadOptions) (gameLogDownloadStatus, error) {
	gameTypeID := gameType.Int()
	gameLogRes := resource.PlayerGameLog{PlayerID: playerID, Season: season, GameType: gameTypeID}
	fileExists := a.Storage.Exists(ctx, gameLogRes.Path())

	if fileExists {
		if !opts.refresh {
			log.Debug().
				Str("playerID", playerID.String()).
				Str("season", season.String()).
				Str("gameType", gameType.String()).
				Msg("PlayerGameLog already cached")
			metrics.IncDownload(core.PlayerGameLog, metrics.ResultHit)
			return gameLogCached, nil
		}

		log.Debug().
			Str("playerID", playerID.String()).
			Str("season", season.String()).
			Str("gameType", gameType.String()).
			Msg("Refreshing cached player log")
	}

	gameLog, err := a.NHLClient.PlayerGameLog(ctx, playerID, season, gameType)
	if err != nil {
		metrics.IncDownload(core.PlayerGameLog, metrics.ResultError)
		return gameLogDownloaded, fmt.Errorf("download: %w", err)
	}

	content, err := json.Marshal(gameLog)
	if err != nil {
		metrics.IncDownload(core.PlayerGameLog, metrics.ResultError)
		return gameLogDownloaded, fmt.Errorf("marshal player %s season %s: %w", playerID, season, err)
	}

	if err := a.Storage.Write(ctx, gameLogRes.Path(), content); err != nil {
		metrics.IncDownload(core.PlayerGameLog, metrics.ResultError)
		return gameLogDownloaded, fmt.Errorf("save: %w", err)
	}

	log.Info().
		Str("playerID", playerID.String()).
		Str("season", season.String()).
		Str("gameType", gameType.String()).
		Str("path", gameLogRes.Path()).
		Msg("Saved player game log")
	metrics.IncDownload(core.PlayerGameLog, metrics.ResultMiss)

	return gameLogDownloaded, nil
}
