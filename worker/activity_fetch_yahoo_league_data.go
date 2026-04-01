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

// FetchYahooLeagueDataInput contains parameters for fetching league-level Yahoo data.
type FetchYahooLeagueDataInput struct {
	Season   int
	LeagueID int
}

// maxMatchupWeeks is the upper bound for matchup week iteration.
// NHL fantasy seasons are typically 23-26 weeks.
const maxMatchupWeeks = 30

// FetchYahooLeagueData fetches transactions, draft results, and matchups for a single league.
// Each data type is cached independently; already-cached items are skipped.
func (a *YahooActivities) FetchYahooLeagueData(ctx context.Context, input FetchYahooLeagueDataInput) error {
	logger := activity.GetLogger(ctx)

	gameKey, err := getGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.Season)
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
		if a.Storage.Exists(res.Path()) {
			continue
		}

		start := time.Now()
		content, err := a.Download(res.URL())
		duration := time.Since(start)
		if err != nil {
			// An error fetching a future week likely means we've exhausted available weeks
			metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
			logger.Info("Stopped fetching matchups at week", "week", week, "error", err)
			break
		}
		metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

		if err := a.Storage.Write(res.Path(), content); err != nil {
			return fmt.Errorf("save matchups week %d: %w", week, err)
		}

		parsed, err := res.Parse(content)
		if err != nil {
			return fmt.Errorf("parse matchups week %d: %w", week, err)
		}
		if err := cache.Set(a.GobCache, ctx, core.RedisKey(res), parsed); err != nil {
			return fmt.Errorf("cache matchups week %d: %w", week, err)
		}

		sleepAfterYahooDownload()
	}

	logger.Info("Fetched Yahoo league data", "season", input.Season, "leagueID", input.LeagueID)
	return nil
}

// fetchYahooResource fetches a single Yahoo resource with cache-or-download logic.
func (a *YahooActivities) fetchYahooResource(ctx context.Context, res interface {
	Path() string
	URL() string
	Type() core.FileType
	Parse([]byte) (*store.FantasyContent, error)
}) error {
	// Check cache
	if a.Storage.Exists(res.Path()) {
		return nil
	}

	start := time.Now()
	content, err := a.Download(res.URL())
	duration := time.Since(start)
	if err != nil {
		metrics.ObserveHTTP("yahoo", http.MethodGet, 0, duration, 0)
		return err
	}
	metrics.ObserveHTTP("yahoo", http.MethodGet, http.StatusOK, duration, len(content))

	if err := a.Storage.Write(res.Path(), content); err != nil {
		return err
	}

	parsed, err := res.Parse(content)
	if err != nil {
		return err
	}
	if err := cache.Set(a.GobCache, ctx, core.RedisKey(res), parsed); err != nil {
		return err
	}

	sleepAfterYahooDownload()
	return nil
}
