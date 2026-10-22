package yahoo

import (
	"context"
	"fmt"
	"time"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/metrics"
	"github.com/sperano/puckdb/internal/resource"
	"github.com/sperano/puckdb/internal/store"
	"go.temporal.io/sdk/activity"
)

// FetchYahooLeagueStandingsResult reports one standings capture.
type FetchYahooLeagueStandingsResult struct {
	// Date is the capture day (YYYY-MM-DD) the snapshot is filed under;
	// empty when nothing was captured.
	Date string `json:"date,omitempty"`
	// Unavailable reports a preseason 400: Yahoo serves no standings yet.
	Unavailable bool `json:"unavailable,omitempty"`
	// SkipReason says why the capture day was not in the league's season,
	// in which case no request was made.
	SkipReason string `json:"skipReason,omitempty"`
}

// FetchYahooLeagueStandings captures the league's current standings under
// today's date (resource.StandingsCaptureDate): Yahoo only serves current
// standings, so a snapshot is only ever made for the day it is taken, and
// the first capture of a day is kept (later runs read the cached file). The
// capture is skipped, without a request, when the capture day is outside
// the league's season (before its start date or past its end date plus the
// grace day) or belongs to another NHL season. A preseason 400 is reported
// as unavailable; any other failure is returned so Temporal retries it.
func (a *FetchActivities) FetchYahooLeagueStandings(ctx context.Context, input FetchYahooLeagueDataInput) (FetchYahooLeagueStandingsResult, error) {
	defer metrics.TrackActivityDuration("FetchYahooLeagueStandings")()
	return a.fetchYahooLeagueStandings(ctx, input, time.Now())
}

func (a *FetchActivities) fetchYahooLeagueStandings(ctx context.Context, input FetchYahooLeagueDataInput, now time.Time) (FetchYahooLeagueStandingsResult, error) {
	logger := activity.GetLogger(ctx)
	date := resource.StandingsCaptureDate(now)
	league, _, err := readLeagueSettings(ctx, a.Storage, resource.League{Season: input.Season, LeagueID: input.LeagueID})
	if err != nil {
		return FetchYahooLeagueStandingsResult{}, err
	}
	if reason := standingsCaptureSkip(input.Season, league, date); reason != "" {
		logger.Info("Standings capture skipped", "season", input.Season, "leagueID", input.LeagueID,
			"date", date.Format(config.DateFormat), "reason", reason)
		return FetchYahooLeagueStandingsResult{SkipReason: reason}, nil
	}
	gameKey, err := GetGameKeyForSeason(ctx, a.Storage, a.GobCache, a.Download, input.Season)
	if err != nil {
		return FetchYahooLeagueStandingsResult{}, err
	}
	res := resource.LeagueStandings{Season: input.Season, LeagueID: input.LeagueID, Date: date, GameKey: gameKey}
	_, origin, err := a.fetcher().Fetch(ctx, res)
	if err != nil {
		if isPreseasonResourceUnavailable(err) {
			return FetchYahooLeagueStandingsResult{Unavailable: true}, nil
		}
		return FetchYahooLeagueStandingsResult{}, fmt.Errorf("fetch standings %d/%d for %s: %w",
			input.Season, input.LeagueID, date.Format(config.DateFormat), err)
	}
	logFetched(logger, origin, "Standings", "season", input.Season, "leagueID", input.LeagueID,
		"date", date.Format(config.DateFormat))
	return FetchYahooLeagueStandingsResult{Date: date.Format(config.DateFormat)}, nil
}

// standingsCaptureSkip says why no snapshot is taken on date for the league,
// or "" when one is: the date must fall in the league's season (its start
// date through its end date plus the grace day) and in NHL season season,
// so the snapshot's path and its date agree.
func standingsCaptureSkip(season int, league store.League, date time.Time) string {
	if resource.DeduceSeason(date) != season {
		return fmt.Sprintf("capture day %s is not in season %d", date.Format(config.DateFormat), season)
	}
	if !leagueRegularSeasonStarted(league, date) {
		return fmt.Sprintf("league season starts %s", league.StartDate)
	}
	if leagueSeasonEnded(league, date) {
		return fmt.Sprintf("league season ended %s", league.EndDate)
	}
	return ""
}
