package worker

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/sperano/puckdb/cache"
	"github.com/sperano/puckdb/core"
	"github.com/sperano/puckdb/metrics"
	"github.com/sperano/puckdb/resource"
	"github.com/sperano/puckdb/store"
	"go.temporal.io/sdk/activity"
)

// TeamInfo identifies a team within a league for Yahoo downloads.
type TeamInfo struct {
	LeagueID int
	TeamID   int
}

// FetchTeamsInput contains parameters for fetching multiple teams in a single activity.
type FetchTeamsInput struct {
	StartSeason int // Season start year (e.g., 2023 for 2023-2024 season)
	Teams       []TeamInfo
}


// YahooActivities holds dependencies for Yahoo league and team fetching.
type YahooActivities struct {
	Storage          store.Storage
	Download         Downloader
	GobCache         *cache.GobCache
	PublicDownloader HTTPDownloader
}

// FetchLeague downloads a Yahoo fantasy league file for the given season and league.
// Uses Redis → FileSystem cache; skips download if already cached.
func (a *YahooActivities) FetchLeague(ctx context.Context, season int, leagueID int) error {
	defer metrics.TrackActivityDuration("FetchLeague")()
	logger := activity.GetLogger(ctx)

	gameKey, err := GetGameKeyForSeason(season)
	if err != nil {
		return err
	}

	res := resource.League{Season: season, LeagueID: leagueID, GameKey: gameKey}

	// Check Redis → FileSystem cache
	_, _, err = cache.ReadParsedCached(ctx, a.Storage, a.GobCache, res)
	if err == nil {
		logger.Debug("League loaded from cache", "season", season, "leagueID", leagueID)
		metrics.IncDownload(core.League, metrics.ResultHit)
		return nil
	}

	// Cache miss — download from Yahoo
	start := time.Now()
	content, err := a.Download(res.URL())
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
		metrics.IncDownload(core.League, metrics.ResultError)
		return fmt.Errorf("download league %d/%d: %w", season, leagueID, err)
	}
	metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

	// Save raw XML to filesystem
	if err := a.Storage.Write(res.Path(), content); err != nil {
		return fmt.Errorf("save league %d/%d: %w", season, leagueID, err)
	}

	// Parse and populate Redis cache
	parsed, err := res.Parse(content)
	if err != nil {
		return err
	}
	if err := cache.Set(a.GobCache, ctx, core.RedisKey(res), parsed); err != nil {
		return fmt.Errorf("gob cache set league: %w", err)
	}

	logger.Info("League downloaded", "season", season, "leagueID", leagueID)
	metrics.IncDownload(core.League, metrics.ResultMiss)
	sleepAfterYahooDownload()
	return nil
}

// FetchYahooPlayerBatch fetches a range of Yahoo player pages (from cache or network).
// Processes players from startID to endID (inclusive).
// Each player uses two targeted stat calls (Exists) to check cache status,
// which is faster on JuiceFS than listing the full 34k-entry missing directory per batch.
func (a *YahooActivities) FetchYahooPlayerBatch(ctx context.Context, startID, endID store.YahooPlayerID) (FetchStats, error) {
	var result FetchStats
	logger := activity.GetLogger(ctx)
	logger.Debug("FetchYahooPlayerBatch started", "startID", startID, "endID", endID)

	for playerID := startID; playerID <= endID; playerID++ {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		activity.RecordHeartbeat(ctx, playerID)

		status, err := fetchYahooPlayerImpl(ctx, a.Storage, a.PublicDownloader, playerID)
		if err != nil {
			return result, err
		}
		switch status {
		case fetchStatusDownloaded:
			result.Downloaded++
		case fetchStatusMissing:
			result.Missing++
		case fetchStatusCached:
			result.CacheHits++
		}
	}
	return result, nil
}

// FetchTeams downloads Yahoo fantasy team pages for multiple teams.
// This batches what would otherwise be N separate activity calls into one.
// Uses Redis → FileSystem cache per team; skips download for cached teams.
func (a *YahooActivities) FetchTeams(ctx context.Context, input FetchTeamsInput) error {
	defer metrics.TrackActivityDuration("FetchTeams")()
	logger := activity.GetLogger(ctx)

	gameKey, err := GetGameKeyForSeason(input.StartSeason)
	if err != nil {
		return err
	}

	logger.Debug("FetchTeams started",
		"season", input.StartSeason,
		"gameKey", gameKey,
		"numTeams", len(input.Teams))

	for _, team := range input.Teams {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		res := resource.Team{
			Season:   input.StartSeason,
			LeagueID: team.LeagueID,
			TeamID:   team.TeamID,
			GameKey:  gameKey,
		}

		// Check Redis → FileSystem cache
		_, _, err := cache.ReadParsedCached(ctx, a.Storage, a.GobCache, res)
		if err == nil {
			logger.Debug("Team loaded from cache", "leagueID", team.LeagueID, "teamID", team.TeamID)
			metrics.IncDownload(core.Team, metrics.ResultHit)
			continue
		}

		// Cache miss — download from Yahoo
		start := time.Now()
		content, err := a.Download(res.URL())
		duration := time.Since(start)
		if err != nil {
			metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
			metrics.IncDownload(core.Team, metrics.ResultError)
			return fmt.Errorf("download team %d/%d/%d: %w", input.StartSeason, team.LeagueID, team.TeamID, err)
		}
		metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

		// Save raw XML to filesystem
		if err := a.Storage.Write(res.Path(), content); err != nil {
			return fmt.Errorf("save team %d/%d/%d: %w", input.StartSeason, team.LeagueID, team.TeamID, err)
		}

		// Parse and populate Redis cache
		parsed, err := res.Parse(content)
		if err != nil {
			return err
		}
		if err := cache.Set(a.GobCache, ctx, core.RedisKey(res), parsed); err != nil {
			return fmt.Errorf("gob cache set team: %w", err)
		}

		logger.Info("Team downloaded", "leagueID", team.LeagueID, "teamID", team.TeamID)
		metrics.IncDownload(core.Team, metrics.ResultMiss)
		sleepAfterYahooDownload()
	}

	return nil
}
