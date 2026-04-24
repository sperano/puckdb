package yahoo

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
	"github.com/sperano/puckdb/worker/shared"
	"go.temporal.io/sdk/activity"
)

// maxMatchupWeeks is the upper bound for matchup week iteration.
// NHL fantasy seasons are typically 23-26 weeks.
const maxMatchupWeeks = 30

// FetchActivities holds dependencies for Yahoo league and team fetching.
type FetchActivities struct {
	Storage          store.Storage
	Download         shared.Downloader
	GobCache         *cache.GobCache
	PublicDownloader HTTPDownloader
}

// FetchLeague downloads a Yahoo fantasy league file for the given season and league.
// Uses Redis → FileSystem cache; skips download if already cached.
func (a *FetchActivities) FetchLeague(ctx context.Context, season int, leagueID int) error {
	defer metrics.TrackActivityDuration("FetchLeague")()
	logger := activity.GetLogger(ctx)

	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, season)
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
	content, err := a.Download(ctx, res.URL())
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
		metrics.IncDownload(core.League, metrics.ResultError)
		return fmt.Errorf("download league %d/%d: %w", season, leagueID, err)
	}
	metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

	// Save raw XML to filesystem
	if err := a.Storage.Write(ctx, res.Path(), content); err != nil {
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
	SleepAfterYahooDownload()
	return nil
}

// FetchYahooPlayerBatch fetches a range of Yahoo player pages (from cache or network).
// Processes players from startID to endID (inclusive).
// Each player uses two targeted stat calls (Exists) to check cache status,
// which is faster on JuiceFS than listing the full 34k-entry missing directory per batch.
func (a *FetchActivities) FetchYahooPlayerBatch(ctx context.Context, startID, endID store.YahooPlayerID) (shared.FetchStats, error) {
	var result shared.FetchStats
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
func (a *FetchActivities) FetchTeams(ctx context.Context, input FetchTeamsInput) error {
	defer metrics.TrackActivityDuration("FetchTeams")()
	logger := activity.GetLogger(ctx)

	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.StartSeason)
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
		content, err := a.Download(ctx, res.URL())
		duration := time.Since(start)
		if err != nil {
			metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
			metrics.IncDownload(core.Team, metrics.ResultError)
			return fmt.Errorf("download team %d/%d/%d: %w", input.StartSeason, team.LeagueID, team.TeamID, err)
		}
		metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

		// Save raw XML to filesystem
		if err := a.Storage.Write(ctx, res.Path(), content); err != nil {
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
		SleepAfterYahooDownload()
	}

	return nil
}

// FetchYahooLeagueData fetches transactions, draft results, and matchups for a single league.
// Each data type is cached independently; already-cached items are skipped.
func (a *FetchActivities) FetchYahooLeagueData(ctx context.Context, input FetchYahooLeagueDataInput) error {
	logger := activity.GetLogger(ctx)

	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.Season)
	if err != nil {
		return err
	}

	if err := a.fetchYahooResource(ctx, resource.Transactions{
		Season: input.Season, LeagueID: input.LeagueID, GameKey: gameKey,
	}); err != nil {
		return fmt.Errorf("fetch transactions %d/%d: %w", input.Season, input.LeagueID, err)
	}
	activity.RecordHeartbeat(ctx, "transactions")

	if err := a.fetchYahooResource(ctx, resource.DraftResults{
		Season: input.Season, LeagueID: input.LeagueID, GameKey: gameKey,
	}); err != nil {
		return fmt.Errorf("fetch draft results %d/%d: %w", input.Season, input.LeagueID, err)
	}
	activity.RecordHeartbeat(ctx, "draftresults")

	// Fetch matchups week-by-week until we get an empty response or error
	for week := 1; week <= maxMatchupWeeks; week++ {
		activity.RecordHeartbeat(ctx, fmt.Sprintf("matchups:week%d", week))
		res := resource.Matchups{
			Season: input.Season, LeagueID: input.LeagueID, Week: week, GameKey: gameKey,
		}

		// Skip if already cached
		if a.Storage.Exists(ctx, res.Path()) {
			continue
		}

		start := time.Now()
		content, err := a.Download(ctx, res.URL())
		duration := time.Since(start)
		if err != nil {
			// An error fetching a future week likely means we've exhausted available weeks
			metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
			logger.Info("Stopped fetching matchups at week", "week", week, "error", err)
			break
		}
		metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

		if err := a.Storage.Write(ctx, res.Path(), content); err != nil {
			return fmt.Errorf("save matchups week %d: %w", week, err)
		}

		parsed, err := res.Parse(content)
		if err != nil {
			return fmt.Errorf("parse matchups week %d: %w", week, err)
		}
		if err := cache.Set(a.GobCache, ctx, core.RedisKey(res), parsed); err != nil {
			return fmt.Errorf("cache matchups week %d: %w", week, err)
		}

		SleepAfterYahooDownload()
	}

	logger.Info("Fetched Yahoo league data", "season", input.Season, "leagueID", input.LeagueID)
	return nil
}

// fetchYahooResource fetches a single Yahoo resource with cache-or-download logic.
func (a *FetchActivities) fetchYahooResource(ctx context.Context, res interface {
	Path() string
	URL() string
	Type() core.FileType
	Parse([]byte) (*store.FantasyContent, error)
}) error {
	// Check cache
	if a.Storage.Exists(ctx, res.Path()) {
		return nil
	}

	start := time.Now()
	content, err := a.Download(ctx, res.URL())
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
		return err
	}
	metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

	if err := a.Storage.Write(ctx, res.Path(), content); err != nil {
		return err
	}

	parsed, err := res.Parse(content)
	if err != nil {
		return err
	}
	if err := cache.Set(a.GobCache, ctx, core.RedisKey(res), parsed); err != nil {
		return err
	}

	SleepAfterYahooDownload()
	return nil
}
