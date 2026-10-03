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
	"go.temporal.io/sdk/temporal"
)

const (
	// teamNotFoundErrorType tags FetchTeams failures where Yahoo answered 400 for
	// a team key, meaning the team is not in the league (bad team_ids config).
	teamNotFoundErrorType = "YahooTeamNotFound"
	// leagueNotFoundErrorType tags FetchLeague failures where Yahoo answered 400
	// for a league key, meaning the league is not valid (bad league config).
	leagueNotFoundErrorType = "YahooLeagueNotFound"
)

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

// FetchLeagueResult reports what the workflow needs from the league file to
// size its progress: the last matchup week (0 when Yahoo omits it).
type FetchLeagueResult struct {
	EndWeek int `json:"endWeek,omitempty"`
}

// FetchLeague downloads a Yahoo fantasy league file for the given season and league.
// Uses Redis → FileSystem cache; skips download if already cached.
func (a *FetchActivities) FetchLeague(ctx context.Context, season int, leagueID int) (FetchLeagueResult, error) {
	defer metrics.TrackActivityDuration("FetchLeague")()
	logger := activity.GetLogger(ctx)

	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, season)
	if err != nil {
		return FetchLeagueResult{}, err
	}

	res := resource.League{Season: season, LeagueID: leagueID, GameKey: gameKey}
	content, origin, err := a.fetcher().Fetch(ctx, res)
	if err != nil {
		if isYahooBadRequest(err) {
			return FetchLeagueResult{}, temporal.NewNonRetryableApplicationError(
				fmt.Sprintf("season %d league %d was rejected by Yahoo (400): the league does not exist or "+
					"is not accessible; check the league IDs in the Yahoo seasons config (or the season's game key)", season, leagueID),
				leagueNotFoundErrorType, err)
		}
		return FetchLeagueResult{}, fmt.Errorf("fetch league %d/%d: %w", season, leagueID, err)
	}
	logFetched(logger, origin, "League", "season", season, "leagueID", leagueID)
	return FetchLeagueResult{EndWeek: content.League.EndWeek}, nil
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

// FetchTeams downloads Yahoo fantasy team pages for the given teams. The
// season sync workflow calls it with one team at a time so each download
// advances its progress; it still accepts a batch.
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
		activity.RecordHeartbeat(ctx, team.TeamID)

		res := resource.Team{
			Season:   input.StartSeason,
			LeagueID: team.LeagueID,
			TeamID:   team.TeamID,
			GameKey:  gameKey,
		}
		_, origin, err := fetcher.Fetch(ctx, res)
		if err != nil {
			if isYahooBadRequest(err) {
				return temporal.NewNonRetryableApplicationError(
					fmt.Sprintf("season %d league %d team %d was rejected by Yahoo (400): the team is not in "+
						"the Yahoo league; check team_ids in the Yahoo seasons config (or the season's game key)",
						input.StartSeason, team.LeagueID, team.TeamID),
					teamNotFoundErrorType, err)
			}
			return fmt.Errorf("fetch team %d/%d/%d: %w", input.StartSeason, team.LeagueID, team.TeamID, err)
		}
		logFetched(logger, origin, "Team", "leagueID", team.LeagueID, "teamID", team.TeamID)
	}

	return nil
}

// isYahooBadRequest reports whether err is a Yahoo API 400 surfaced by Fetcher
// (wrapped in ErrDownload). Yahoo uses 400 for keys that definitively do not
// exist; 404 is intermittent and every other failure stays retryable.
func isYahooBadRequest(err error) bool {
	var httpErr *httpx.HTTPError
	if !errors.Is(err, ErrDownload) || !errors.As(err, &httpErr) {
		return false
	}
	return httpErr.StatusCode == http.StatusBadRequest
}

// isPreseasonResourceUnavailable reports whether Yahoo rejected a resource that
// does not exist yet before the season starts.
func isPreseasonResourceUnavailable(err error) bool {
	return isYahooBadRequest(err)
}

// isEndOfMatchupWeeks reports whether Yahoo definitively rejected the week as
// outside the matchup series. Yahoo 404 responses are intermittent and remain
// retryable, as do transport, throttling, server, storage, parse, and cache failures.
func isEndOfMatchupWeeks(err error) bool {
	return isYahooBadRequest(err)
}
