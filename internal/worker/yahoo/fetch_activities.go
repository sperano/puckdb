package yahoo

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/sperano/puckdb/internal/cache"
	"github.com/sperano/puckdb/internal/core"
	"github.com/sperano/puckdb/internal/httpx"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/log"
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

// fetcher builds the cache-or-download primitive from the activity's dependencies.
func (a *FetchActivities) fetcher() Fetcher {
	return Fetcher{Storage: a.Storage, GobCache: a.GobCache, Download: a.Download}
}

// logFetched logs a Fetch outcome: downloads at Info, cache hits at Debug.
func logFetched(logger log.Logger, origin core.DataOrigin, what string, keyvals ...any) {
	if origin == core.OriginRemoteYahooAPI {
		logger.Info(what+" downloaded", keyvals...)
		return
	}
	logger.Debug(what+" loaded from cache", keyvals...)
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
	_, origin, err := a.fetcher().Fetch(ctx, res)
	if err != nil {
		return fmt.Errorf("fetch league %d/%d: %w", season, leagueID, err)
	}
	logFetched(logger, origin, "League", "season", season, "leagueID", leagueID)
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

	fetcher := a.fetcher()
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
		_, origin, err := fetcher.Fetch(ctx, res)
		if err != nil {
			return fmt.Errorf("fetch team %d/%d/%d: %w", input.StartSeason, team.LeagueID, team.TeamID, err)
		}
		logFetched(logger, origin, "Team", "leagueID", team.LeagueID, "teamID", team.TeamID)
	}

	return nil
}

// FetchYahooLeagueData fetches transactions, draft results, and matchups for a single league.
// Each data type is cached independently; already-cached items are skipped.
func (a *FetchActivities) FetchYahooLeagueData(ctx context.Context, input FetchYahooLeagueDataInput) (FetchYahooLeagueDataResult, error) {
	logger := activity.GetLogger(ctx)
	result := FetchYahooLeagueDataResult{}

	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.Season)
	if err != nil {
		return result, err
	}

	fetcher := a.fetcher()

	txRes := resource.Transactions{Season: input.Season, LeagueID: input.LeagueID, GameKey: gameKey}
	_, origin, err := fetcher.Fetch(ctx, txRes)
	if err != nil {
		if !isPreseasonResourceUnavailable(err) {
			return result, fmt.Errorf("fetch transactions %d/%d: %w", input.Season, input.LeagueID, err)
		}
		result.UnavailableResources = append(result.UnavailableResources, "transactions")
	} else {
		logFetched(logger, origin, "Transactions", "season", input.Season, "leagueID", input.LeagueID)
	}
	activity.RecordHeartbeat(ctx, "transactions")

	drRes := resource.DraftResults{Season: input.Season, LeagueID: input.LeagueID, GameKey: gameKey}
	_, origin, err = fetcher.Fetch(ctx, drRes)
	if err != nil {
		if !isPreseasonResourceUnavailable(err) {
			return result, fmt.Errorf("fetch draft results %d/%d: %w", input.Season, input.LeagueID, err)
		}
		result.UnavailableResources = append(result.UnavailableResources, "draft results")
	} else {
		logFetched(logger, origin, "Draft results", "season", input.Season, "leagueID", input.LeagueID)
	}
	activity.RecordHeartbeat(ctx, "draftresults")

	matchupsUnavailable, err := a.fetchMatchups(ctx, fetcher, input, gameKey)
	if err != nil {
		return result, err
	}
	if matchupsUnavailable {
		result.UnavailableResources = append(result.UnavailableResources, "matchups")
	}

	logger.Info("Fetched Yahoo league data", "season", input.Season, "leagueID", input.LeagueID)
	return result, nil
}

// fetchMatchups fetches matchups week-by-week until Yahoo rejects a week (end
// of the series). Any other failure is returned so Temporal retries it.
func (a *FetchActivities) fetchMatchups(ctx context.Context, fetcher Fetcher, input FetchYahooLeagueDataInput, gameKey int) (bool, error) {
	logger := activity.GetLogger(ctx)
	for week := 1; week <= maxMatchupWeeks; week++ {
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		default:
		}
		activity.RecordHeartbeat(ctx, fmt.Sprintf("matchups:week%d", week))
		res := resource.Matchups{
			Season: input.Season, LeagueID: input.LeagueID, Week: week, GameKey: gameKey,
		}
		_, origin, err := fetcher.Fetch(ctx, res)
		if isEndOfMatchupWeeks(err) {
			logger.Info("Stopped fetching matchups at week", "week", week, "error", err)
			return week == 1, nil
		}
		if err != nil {
			return false, fmt.Errorf("fetch matchups %d/%d week %d: %w", input.Season, input.LeagueID, week, err)
		}
		logFetched(logger, origin, "Matchups", "season", input.Season, "leagueID", input.LeagueID, "week", week)
	}
	return false, nil
}

func isPreseasonResourceUnavailable(err error) bool {
	var httpErr *httpx.HTTPError
	if !errors.Is(err, ErrDownload) || !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusBadRequest
}

// isEndOfMatchupWeeks reports whether Yahoo definitively rejected the week as
// outside the matchup series. Yahoo 404 responses are intermittent and remain
// retryable, as do transport, throttling, server, storage, parse, and cache failures.
func isEndOfMatchupWeeks(err error) bool {
	var httpErr *httpx.HTTPError
	if !errors.Is(err, ErrDownload) || !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusBadRequest
}
