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
	PlayerIDs      []int64 // NHL player IDs
	StartYear      int     // Season start year (e.g., 2024 for 2024-2025 season)
	GameTypes      []int   // Game types to download (2=regular season, 3=playoffs)
	RefreshCurrent bool    // If true, overwrite files for the current season
}

// DownloadPlayerGameLogsResult contains download statistics.
type DownloadPlayerGameLogsResult struct {
	Players    int // Number of players processed in this batch
	Downloaded int
	CacheHits  int
	Skipped    int // Files skipped for current season (not refreshing)
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
	result := &DownloadPlayerGameLogsResult{
		Players: len(input.PlayerIDs),
	}

	if len(input.PlayerIDs) == 0 {
		return result, nil
	}

	// Default to regular season if no game types specified
	gameTypes := input.GameTypes
	if len(gameTypes) == 0 {
		gameTypes = []int{nhl.GameTypeRegularSeason.ToInt()}
	}

	season := nhl.NewSeason(input.StartYear)
	isCurrent := isCurrentSeason(input.StartYear)

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

			opts := downloadOptions{
				isCurrent:      isCurrent,
				refreshCurrent: input.RefreshCurrent,
			}
			status, err := downloadPlayerGameLogToCache(ctx, fs, pid, season, gameType, opts)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("player %d season %s %s: %v", playerID, season, gameType, err))
				continue
			}

			switch status {
			case downloadStatusDownloaded:
				result.Downloaded++
			case downloadStatusCached:
				result.CacheHits++
			case downloadStatusSkipped:
				result.Skipped++
			}
		}
	}

	log.Info().
		Int("players", len(input.PlayerIDs)).
		Int("startYear", input.StartYear).
		Int("downloaded", result.Downloaded).
		Int("cacheHits", result.CacheHits).
		Int("skipped", result.Skipped).
		Int("errors", len(result.Errors)).
		Msg("Download player game logs complete")

	return result, nil
}

// downloadStatus indicates the result of a download attempt.
type downloadStatus int

const (
	downloadStatusDownloaded downloadStatus = iota
	downloadStatusCached
	downloadStatusSkipped
)

// downloadOptions controls download behavior.
type downloadOptions struct {
	isCurrent      bool // True if this is the current season
	refreshCurrent bool // True if current season files should be overwritten
}

// downloadPlayerGameLogToCache downloads a single player game log to cache.
// Returns the download status indicating whether it was downloaded, cached, or skipped.
func downloadPlayerGameLogToCache(ctx context.Context, fs store.Store, playerID nhl.PlayerID, season nhl.Season, gameType nhl.GameType, opts downloadOptions) (downloadStatus, error) {
	file := store.PlayerGameLogFile{
		PlayerID: playerID,
		Season:   seasonToInt(season),
		GameType: gameType.ToInt(),
	}

	fileExists := fs.Exists(file)

	if fileExists {
		// Current season with refresh disabled: skip with message
		if opts.isCurrent && !opts.refreshCurrent {
			log.Debug().
				Str("playerID", playerID.String()).
				Str("season", season.String()).
				Str("gameType", gameType.String()).
				Msg("Current season player log exists, not refreshing (use --refresh-current-player-logs to overwrite)")
			metrics.IncDownload("PlayerGameLog", "skip")
			return downloadStatusSkipped, nil
		}

		// Historical season: use cache
		if !opts.isCurrent {
			log.Debug().
				Str("playerID", playerID.String()).
				Str("season", season.String()).
				Str("gameType", gameType.String()).
				Msg("PlayerGameLog already cached")
			metrics.IncDownload("PlayerGameLog", "hit")
			return downloadStatusCached, nil
		}

		// Current season with refresh enabled: fall through to download
		log.Debug().
			Str("playerID", playerID.String()).
			Str("season", season.String()).
			Str("gameType", gameType.String()).
			Msg("Refreshing current season player log")
	}

	if err := fs.MkdirAll(file.Dir(), 0755); err != nil {
		metrics.IncDownload("PlayerGameLog", "error")
		return downloadStatusDownloaded, fmt.Errorf("mkdir: %w", err)
	}

	content, err := DownloadPlayerGameLog(playerID, season, gameType)
	if err != nil {
		metrics.IncDownload("PlayerGameLog", "error")
		return downloadStatusDownloaded, fmt.Errorf("download: %w", err)
	}

	if err := fs.Write(file, content); err != nil {
		metrics.IncDownload("PlayerGameLog", "error")
		return downloadStatusDownloaded, fmt.Errorf("save: %w", err)
	}

	log.Info().
		Str("playerID", playerID.String()).
		Str("season", season.String()).
		Str("gameType", gameType.String()).
		Str("path", store.Path(file)).
		Msg("Saved player game log")
	metrics.IncDownload("PlayerGameLog", "miss")

	return downloadStatusDownloaded, nil
}

// seasonToInt converts a Season to the integer format used in file names (e.g., 20242025).
func seasonToInt(s nhl.Season) int {
	return s.StartYear()*10000 + s.EndYear()
}

// isCurrentSeason returns true if the given start year represents the current NHL season.
// NHL seasons run from October to June, so the current season's start year is:
// - The current year if we're in Oct-Dec
// - The previous year if we're in Jan-June
func isCurrentSeason(startYear int) bool {
	now := time.Now()
	currentYear := now.Year()
	month := now.Month()

	// If we're in Jan-June, the current season started the previous year
	// If we're in Oct-Dec, the current season started this year
	// July-Sep is off-season, consider it the upcoming season's start year
	var currentSeasonStartYear int
	if month >= time.October {
		currentSeasonStartYear = currentYear
	} else {
		currentSeasonStartYear = currentYear - 1
	}

	return startYear == currentSeasonStartYear
}
