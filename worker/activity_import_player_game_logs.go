package worker

import (
	"bytes"
	"context"
	"encoding/gob"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/database"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/sqlcdb"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

const (
	// GameLogCacheTTL is the expiration time for game log data in Redis.
	// 1 hour is reasonable since this only needs to survive the import workflow.
	GameLogCacheTTL = 1 * time.Hour

	// gameLogCachePrefix is the Redis key prefix for cached game logs.
	gameLogCachePrefix = "puckdb:import:gamelog:"
)

// ImportPlayerGameLogsForDateInput specifies which date's player game logs to import.
type ImportPlayerGameLogsForDateInput struct {
	Season int       // Season ID (e.g., 20232024)
	Date   time.Time // Date to import
}

// ImportPlayerGameLogsForDateResult contains import statistics.
type ImportPlayerGameLogsForDateResult struct {
	PlayersProcessed int
	GamesUpdated     int
	CacheHits        int
	CacheMisses      int
	Errors           []string
}

// ImportPlayerGameLogsForDateActivity imports player game log stats (PPP, GWG, OT goals)
// for games played on a specific date. It reads boxscores to identify players,
// then extracts the relevant entries from their cached game log files.
// Uses Redis to cache parsed game logs to avoid repeated file reads.
func ImportPlayerGameLogsForDateActivity(ctx context.Context, input ImportPlayerGameLogsForDateInput) (*ImportPlayerGameLogsForDateResult, error) {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("ImportPlayerGameLogsForDateActivity", time.Since(start))
	}()

	logger := activity.GetLogger(ctx)
	fs := store.NewStore()
	redisClient := cache.NewClient()
	defer redisClient.Close()

	pool, err := database.OpenPGXPool(ctx)
	if err != nil {
		return nil, fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	queries := sqlcdb.New(pool)

	result, err := importPlayerGameLogsForDateImpl(ctx, fs, redisClient, queries, input)
	if err != nil {
		return result, err
	}

	logger.Debug("Imported player game logs for date",
		"date", input.Date.Format("2006-01-02"),
		"players", result.PlayersProcessed,
		"updated", result.GamesUpdated,
		"cacheHits", result.CacheHits,
		"cacheMisses", result.CacheMisses,
		"errors", len(result.Errors))

	return result, nil
}

// PlayerGameLogUpdater is the interface for database operations needed by game log import.
type PlayerGameLogUpdater interface {
	UpdateSkaterGameLogStats(ctx context.Context, arg sqlcdb.UpdateSkaterGameLogStatsParams) error
}

func importPlayerGameLogsForDateImpl(
	ctx context.Context,
	fs store.Store,
	redisClient cache.Client,
	queries PlayerGameLogUpdater,
	input ImportPlayerGameLogsForDateInput,
) (*ImportPlayerGameLogsForDateResult, error) {
	result := &ImportPlayerGameLogsForDateResult{}

	// Read daily schedule to get game IDs
	scheduleFile := store.DailyScheduleFile{Date: input.Date}
	if !fs.Exists(scheduleFile) {
		return result, nil // No games on this date
	}

	scheduleData, err := fs.Read(scheduleFile)
	if err != nil {
		return result, fmt.Errorf("read schedule: %w", err)
	}

	var schedule nhl.DailySchedule
	if err := json.Unmarshal(scheduleData, &schedule); err != nil {
		return result, fmt.Errorf("parse schedule: %w", err)
	}

	if len(schedule.Games) == 0 {
		return result, nil
	}

	// Build set of game IDs for this date
	gameIDs := make(map[int64]bool)
	for _, game := range schedule.Games {
		gameIDs[int64(game.ID)] = true
	}

	// Extract player IDs from boxscores
	playerIDs := extractPlayerIDsFromBoxscores(fs, schedule.Games)
	if len(playerIDs) == 0 {
		return result, nil
	}

	// Process each player's game log
	gameLogCache := newGameLogCache(redisClient, input.Season)

	for playerID := range playerIDs {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}

		updated, cacheHit, errs := processPlayerGameLogForDate(ctx, fs, gameLogCache, queries, playerID, input.Season, gameIDs)
		result.PlayersProcessed++
		result.GamesUpdated += updated
		if cacheHit {
			result.CacheHits++
		} else {
			result.CacheMisses++
		}
		result.Errors = append(result.Errors, errs...)
	}

	return result, nil
}

// extractPlayerIDsFromBoxscores reads boxscores and returns unique player IDs.
func extractPlayerIDsFromBoxscores(fs store.Store, games []nhl.ScheduleGame) map[int64]bool {
	playerIDs := make(map[int64]bool)

	for _, game := range games {
		boxscoreFile := store.BoxscoreFile{GameID: game.ID}
		if !fs.Exists(boxscoreFile) {
			continue
		}

		data, err := fs.Read(boxscoreFile)
		if err != nil {
			continue
		}

		var boxscore nhl.Boxscore
		if err := json.Unmarshal(data, &boxscore); err != nil {
			continue
		}

		// Extract skater IDs (forwards and defense)
		for _, player := range boxscore.PlayerByGameStats.HomeTeam.Forwards {
			playerIDs[int64(player.PlayerID)] = true
		}
		for _, player := range boxscore.PlayerByGameStats.HomeTeam.Defense {
			playerIDs[int64(player.PlayerID)] = true
		}
		for _, player := range boxscore.PlayerByGameStats.AwayTeam.Forwards {
			playerIDs[int64(player.PlayerID)] = true
		}
		for _, player := range boxscore.PlayerByGameStats.AwayTeam.Defense {
			playerIDs[int64(player.PlayerID)] = true
		}
	}

	return playerIDs
}

// gameLogCache provides Redis-backed caching for player game logs.
type gameLogCache struct {
	client cache.Client
	key    string // Redis hash key for this season
}

func newGameLogCache(client cache.Client, season int) *gameLogCache {
	return &gameLogCache{
		client: client,
		key:    fmt.Sprintf("%s%d", gameLogCachePrefix, season),
	}
}

// cachedGameLog is the serializable struct for Redis storage.
type cachedGameLog struct {
	Entries []nhl.GameLog
}

// get retrieves a player's game log entries from cache.
func (c *gameLogCache) get(ctx context.Context, playerID int64, gameType int) ([]nhl.GameLog, bool) {
	field := fmt.Sprintf("%d:%d", playerID, gameType)
	data, err := c.client.HGet(ctx, c.key, field).Bytes()
	if err != nil {
		return nil, false
	}

	var cached cachedGameLog
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&cached); err != nil {
		return nil, false
	}

	return cached.Entries, true
}

// set stores a player's game log entries in cache.
func (c *gameLogCache) set(ctx context.Context, playerID int64, gameType int, entries []nhl.GameLog) {
	field := fmt.Sprintf("%d:%d", playerID, gameType)

	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(cachedGameLog{Entries: entries}); err != nil {
		return
	}

	pipe := c.client.Pipeline()
	pipe.HSet(ctx, c.key, field, buf.Bytes())
	pipe.Expire(ctx, c.key, GameLogCacheTTL)
	_, _ = pipe.Exec(ctx)
}

// processPlayerGameLogForDate reads a player's game log and updates stats for matching games.
func processPlayerGameLogForDate(
	ctx context.Context,
	fs store.Store,
	gameLogCache *gameLogCache,
	queries PlayerGameLogUpdater,
	playerID int64,
	season int,
	gameIDs map[int64]bool,
) (updated int, cacheHit bool, errors []string) {
	pid := nhl.PlayerID(playerID)

	// Try both regular season and playoffs
	gameTypes := []int{nhl.GameTypeRegularSeason.ToInt(), nhl.GameTypePlayoffs.ToInt()}

	for _, gameType := range gameTypes {
		// Try cache first
		entries, hit := gameLogCache.get(ctx, playerID, gameType)
		if hit {
			cacheHit = true
		} else {
			// Load from file
			file := store.PlayerGameLogFile{
				PlayerID: pid,
				Season:   season,
				GameType: gameType,
			}

			if !fs.Exists(file) {
				continue
			}

			content, err := fs.Read(file)
			if err != nil {
				errors = append(errors, fmt.Sprintf("player %d: read error: %v", playerID, err))
				continue
			}

			var gameLog nhl.PlayerGameLog
			if err := json.Unmarshal(content, &gameLog); err != nil {
				errors = append(errors, fmt.Sprintf("player %d: parse error: %v", playerID, err))
				continue
			}

			entries = gameLog.GameLog

			// Cache for future days
			gameLogCache.set(ctx, playerID, gameType, entries)
		}

		// Process only entries matching today's games
		for _, entry := range entries {
			gid := int64(entry.GameID)
			if !gameIDs[gid] {
				continue
			}

			var gwg, otg int16
			if entry.GameWinningGoals != nil {
				gwg = int16(*entry.GameWinningGoals)
			}
			if entry.OTGoals != nil {
				otg = int16(*entry.OTGoals)
			}

			params := sqlcdb.UpdateSkaterGameLogStatsParams{
				GameID:           gid,
				PlayerID:         playerID,
				PowerPlayPoints:  int16(entry.PowerPlayPoints),
				GameWinningGoals: gwg,
				OtGoals:          otg,
			}

			if err := queries.UpdateSkaterGameLogStats(ctx, params); err != nil {
				log.Debug().Err(err).Int64("player", playerID).Int64("game", gid).Msg("Failed to update game log stats")
				continue
			}
			updated++
		}
	}

	return updated, cacheHit, errors
}
