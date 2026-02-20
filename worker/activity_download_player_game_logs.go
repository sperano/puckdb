package worker

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/store"
)

// DownloadPlayerGameLogsInput specifies which player game logs to download.
type DownloadPlayerGameLogsInput struct {
	PlayerIDs []int64 // NHL player IDs
	StartYear int     // Season start year (e.g., 2024 for 2024-2025 season)
	GameTypes []int   // Game types to download (2=regular season, 3=playoffs)
}

// DownloadPlayerGameLogsResult contains download statistics.
type DownloadPlayerGameLogsResult struct {
	Downloaded int
	CacheHits  int
	Errors     []string
}

// DownloadPlayerGameLogsActivity downloads player game logs for specified players and season.
func DownloadPlayerGameLogsActivity(ctx context.Context, input DownloadPlayerGameLogsInput) (*DownloadPlayerGameLogsResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadPlayerGameLogsActivity", time.Since(start))
	}()

	fs := store.NewStore()
	return downloadPlayerGameLogsImpl(ctx, fs, input)
}

func downloadPlayerGameLogsImpl(ctx context.Context, fs store.Store, input DownloadPlayerGameLogsInput) (*DownloadPlayerGameLogsResult, error) {
	result := &DownloadPlayerGameLogsResult{}

	if len(input.PlayerIDs) == 0 {
		return result, nil
	}

	// Default to regular season if no game types specified
	gameTypes := input.GameTypes
	if len(gameTypes) == 0 {
		gameTypes = []int{nhl.GameTypeRegularSeason.ToInt()}
	}

	season := nhl.NewSeason(input.StartYear)

	for _, playerID := range input.PlayerIDs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

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

			downloaded, err := downloadPlayerGameLogToCache(ctx, fs, pid, season, gameType)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("player %d season %s %s: %v", playerID, season, gameType, err))
				continue
			}

			if downloaded {
				result.Downloaded++
			} else {
				result.CacheHits++
			}
		}
	}

	log.Info().
		Int("players", len(input.PlayerIDs)).
		Int("startYear", input.StartYear).
		Int("downloaded", result.Downloaded).
		Int("cacheHits", result.CacheHits).
		Int("errors", len(result.Errors)).
		Msg("Download player game logs complete")

	return result, nil
}

// downloadPlayerGameLogToCache downloads a single player game log to cache.
// Returns true if downloaded, false if already cached.
func downloadPlayerGameLogToCache(ctx context.Context, fs store.Store, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType) (bool, error) {
	file := store.PlayerGameLogFile{
		PlayerID: playerID,
		Season:   seasonToInt(season),
		GameType: gameType.ToInt(),
	}

	if fs.Exists(file) {
		log.Debug().
			Str("playerID", playerID.String()).
			Str("season", season.String()).
			Str("gameType", gameType.String()).
			Msg("PlayerGameLog already cached")
		metrics.IncDownload("PlayerGameLog", "hit")
		return false, nil
	}

	if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
		metrics.IncDownload("PlayerGameLog", "error")
		return false, fmt.Errorf("mkdir: %w", err)
	}

	content, err := DownloadPlayerGameLog(playerID, season, gameType)
	if err != nil {
		metrics.IncDownload("PlayerGameLog", "error")
		return false, fmt.Errorf("download: %w", err)
	}

	if err := fs.Write(file, content); err != nil {
		metrics.IncDownload("PlayerGameLog", "error")
		return false, fmt.Errorf("save: %w", err)
	}

	log.Info().
		Str("playerID", playerID.String()).
		Str("season", season.String()).
		Str("gameType", gameType.String()).
		Str("path", store.Path(file)).
		Msg("Saved player game log")
	metrics.IncDownload("PlayerGameLog", "miss")

	return true, nil
}

// seasonToInt converts a Season to the integer format used in file names (e.g., 20242025).
func seasonToInt(s nhl.Season) int {
	return s.StartYear()*10000 + s.EndYear()
}
