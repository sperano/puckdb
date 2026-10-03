package yahoo

import (
	"context"
	"fmt"

	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"go.temporal.io/sdk/activity"
)

// MaxMatchupWeeks is the upper bound for matchup week iteration.
// NHL fantasy seasons are typically 23-26 weeks.
const MaxMatchupWeeks = 30

// FetchYahooLeagueResourceResult reports whether an optional league resource
// (transactions, draft results) is not published yet: Yahoo answers 400 for
// it before the season starts.
type FetchYahooLeagueResourceResult struct {
	Unavailable bool `json:"unavailable,omitempty"`
}

// FetchYahooMatchupWeekInput names one matchup week of one league.
type FetchYahooMatchupWeekInput struct {
	Season   int `json:"season"`
	LeagueID int `json:"leagueId"`
	Week     int `json:"week"`
}

// FetchYahooMatchupWeekResult reports whether Yahoo rejected the week as
// outside the matchup series, which ends the week loop.
type FetchYahooMatchupWeekResult struct {
	EndOfSeries bool `json:"endOfSeries,omitempty"`
}

// FetchYahooTransactions fetches a league's transactions (cache first). A
// preseason 400 is reported as unavailable; any other failure is returned so
// Temporal retries it.
func (a *FetchActivities) FetchYahooTransactions(ctx context.Context, input FetchYahooLeagueDataInput) (FetchYahooLeagueResourceResult, error) {
	defer metrics.TrackActivityDuration("FetchYahooTransactions")()
	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.Season)
	if err != nil {
		return FetchYahooLeagueResourceResult{}, err
	}
	res := resource.Transactions{Season: input.Season, LeagueID: input.LeagueID, GameKey: gameKey}
	return a.fetchOptionalLeagueResource(ctx, res, input, "transactions", "Transactions")
}

// FetchYahooDraftResults fetches a league's draft results (cache first). A
// preseason 400 is reported as unavailable; any other failure is returned so
// Temporal retries it.
func (a *FetchActivities) FetchYahooDraftResults(ctx context.Context, input FetchYahooLeagueDataInput) (FetchYahooLeagueResourceResult, error) {
	defer metrics.TrackActivityDuration("FetchYahooDraftResults")()
	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.Season)
	if err != nil {
		return FetchYahooLeagueResourceResult{}, err
	}
	res := resource.DraftResults{Season: input.Season, LeagueID: input.LeagueID, GameKey: gameKey}
	return a.fetchOptionalLeagueResource(ctx, res, input, "draft results", "Draft results")
}

// fetchOptionalLeagueResource fetches a resource Yahoo may not have published
// yet. name appears in errors, logName in the fetch log line.
func (a *FetchActivities) fetchOptionalLeagueResource(ctx context.Context, res Resource,
	input FetchYahooLeagueDataInput, name, logName string) (FetchYahooLeagueResourceResult, error) {
	_, origin, err := a.fetcher().Fetch(ctx, res)
	if err != nil {
		if !isPreseasonResourceUnavailable(err) {
			return FetchYahooLeagueResourceResult{}, fmt.Errorf("fetch %s %d/%d: %w", name, input.Season, input.LeagueID, err)
		}
		return FetchYahooLeagueResourceResult{Unavailable: true}, nil
	}
	logFetched(activity.GetLogger(ctx), origin, logName, "season", input.Season, "leagueID", input.LeagueID)
	return FetchYahooLeagueResourceResult{}, nil
}

// FetchYahooMatchupWeek fetches one matchup week (cache first). A week Yahoo
// rejects (400) ends the series and is reported, not returned as an error;
// every other failure, 404 included, is returned so Temporal retries it.
func (a *FetchActivities) FetchYahooMatchupWeek(ctx context.Context, input FetchYahooMatchupWeekInput) (FetchYahooMatchupWeekResult, error) {
	defer metrics.TrackActivityDuration("FetchYahooMatchupWeek")()
	logger := activity.GetLogger(ctx)
	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.Season)
	if err != nil {
		return FetchYahooMatchupWeekResult{}, err
	}
	res := resource.Matchups{Season: input.Season, LeagueID: input.LeagueID, Week: input.Week, GameKey: gameKey}
	_, origin, err := a.fetcher().Fetch(ctx, res)
	if isEndOfMatchupWeeks(err) {
		logger.Info("Stopped fetching matchups at week", "week", input.Week, "error", err)
		return FetchYahooMatchupWeekResult{EndOfSeries: true}, nil
	}
	if err != nil {
		return FetchYahooMatchupWeekResult{}, fmt.Errorf("fetch matchups %d/%d week %d: %w",
			input.Season, input.LeagueID, input.Week, err)
	}
	logFetched(logger, origin, "Matchups", "season", input.Season, "leagueID", input.LeagueID, "week", input.Week)
	return FetchYahooMatchupWeekResult{}, nil
}
