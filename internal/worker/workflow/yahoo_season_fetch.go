package workflow

import (
	"fmt"

	"github.com/sperano/puckdb/internal/config"
	"github.com/sperano/puckdb/internal/worker/yahoo"
	"go.temporal.io/sdk/workflow"
)

// fetchYahooSeasonMetadata downloads Yahoo league settings, team pages, and
// league-level data (transactions, draft results, matchups) for the season,
// one activity per Yahoo download so each league's bar advances per
// download. Every league's settings come first, then every team, so a bad
// league or team ID fails before the long downloads start. Returns the
// optional resources Yahoo has not published yet.
func fetchYahooSeasonMetadata(ctx workflow.Context, startYear int, leagues []config.League,
	progress yahooSeasonProgress) ([]string, error) {
	endWeeks, unavailable, err := fetchYahooLeagues(ctx, startYear, leagues, progress)
	if err != nil {
		return unavailable, err
	}
	if err := fetchYahooTeams(ctx, startYear, leagues, progress); err != nil {
		return unavailable, err
	}
	for i, league := range leagues {
		if league.UsesTemporaryMetadata() {
			continue
		}
		bar := yahooLeagueBar{progress: progress, index: i}
		missing, err := fetchYahooLeagueData(ctx, startYear, league.LeagueID, endWeeks[i], bar)
		if err != nil {
			return unavailable, fmt.Errorf("fetch yahoo league data %d: %w", league.LeagueID, err)
		}
		for _, resourceName := range missing {
			unavailable = append(unavailable, fmt.Sprintf("league %d %s", league.LeagueID, resourceName))
		}
	}
	return unavailable, nil
}

// fetchYahooLeagues downloads every league's settings (or a stand-in league's
// source settings) and returns each league's last matchup week (0 when
// unknown), indexed like leagues. A known last week replaces the matchup
// estimate in the league's bar.
func fetchYahooLeagues(ctx workflow.Context, startYear int, leagues []config.League,
	progress yahooSeasonProgress) ([]int, []string, error) {
	var act *yahoo.FetchActivities
	var unavailable []string
	endWeeks := make([]int, len(leagues))
	for i, league := range leagues {
		if league.UsesTemporaryMetadata() {
			if err := fetchStandInLeagueSource(ctx, league); err != nil {
				return nil, unavailable, err
			}
			unavailable = append(unavailable, standInLeagueNote(league))
			progress.advance(ctx, i)
			continue
		}
		var result yahoo.FetchLeagueResult
		if err := workflow.ExecuteActivity(ctx, act.FetchLeague, startYear, league.LeagueID).Get(ctx, &result); err != nil {
			return nil, unavailable, fmt.Errorf("fetch yahoo league %d: %w", league.LeagueID, err)
		}
		progress.advance(ctx, i)
		endWeeks[i] = result.EndWeek
		progress.resize(ctx, i, yahooMatchupCalls(result.EndWeek)-yahooMatchupCalls(0))
	}
	return endWeeks, unavailable, nil
}

// fetchYahooTeams downloads each configured team page in its own activity.
func fetchYahooTeams(ctx workflow.Context, startYear int, leagues []config.League, progress yahooSeasonProgress) error {
	var act *yahoo.FetchActivities
	for i, league := range leagues {
		if league.UsesTemporaryMetadata() {
			continue
		}
		for _, teamID := range league.TeamIDs {
			input := yahoo.FetchTeamsInput{
				StartSeason: startYear,
				Teams:       []yahoo.TeamInfo{{LeagueID: league.LeagueID, TeamID: teamID}},
			}
			if err := workflow.ExecuteActivity(ctx, act.FetchTeams, input).Get(ctx, nil); err != nil {
				return fmt.Errorf("fetch yahoo teams: %w", err)
			}
			progress.advance(ctx, i)
		}
	}
	return nil
}

// fetchYahooLeagueData downloads a league's transactions, draft results,
// today's standings snapshot and matchup weeks, returning the resources
// Yahoo has not published yet.
func fetchYahooLeagueData(ctx workflow.Context, startYear, leagueID, endWeek int,
	bar yahooLeagueBar) ([]string, error) {
	var act *yahoo.FetchActivities
	input := yahoo.FetchYahooLeagueDataInput{Season: startYear, LeagueID: leagueID}
	resources := []struct {
		name     string
		activity any
	}{
		{"transactions", act.FetchYahooTransactions},
		{"draft results", act.FetchYahooDraftResults},
	}
	var unavailable []string
	for _, res := range resources {
		var result yahoo.FetchYahooLeagueResourceResult
		if err := workflow.ExecuteActivity(ctx, res.activity, input).Get(ctx, &result); err != nil {
			return unavailable, err
		}
		bar.advance(ctx)
		if result.Unavailable {
			unavailable = append(unavailable, res.name)
		}
	}
	noStandings, err := fetchYahooLeagueStandings(ctx, input, bar)
	if err != nil {
		return unavailable, err
	}
	if noStandings {
		unavailable = append(unavailable, "standings")
	}
	matchups := bar.estimate(yahooMatchupCalls(endWeek))
	noMatchups, err := fetchYahooMatchups(ctx, startYear, leagueID, matchups)
	if noMatchups {
		unavailable = append(unavailable, "matchups")
	}
	return unavailable, err
}

// fetchYahooMatchups fetches matchup weeks one activity at a time until Yahoo
// rejects a week (the end of the series) or the loop bound. It reports the
// matchups as unavailable when Yahoo rejects week 1 (preseason). On failure
// the estimate is left as is, so the league's bar stays incomplete.
func fetchYahooMatchups(ctx workflow.Context, startYear, leagueID int, weeks *yahooEstimate) (bool, error) {
	var act *yahoo.FetchActivities
	for week := 1; week <= yahoo.MaxMatchupWeeks; week++ {
		weeks.beforeCall(ctx)
		input := yahoo.FetchYahooMatchupWeekInput{Season: startYear, LeagueID: leagueID, Week: week}
		var result yahoo.FetchYahooMatchupWeekResult
		if err := workflow.ExecuteActivity(ctx, act.FetchYahooMatchupWeek, input).Get(ctx, &result); err != nil {
			return false, err
		}
		weeks.afterCall(ctx)
		if result.EndOfSeries {
			weeks.finish(ctx)
			return week == 1, nil
		}
	}
	weeks.finish(ctx)
	return false, nil
}

// The daily standings capture is gated by a GetVersion change: histories
// recorded before it carry no marker for it and replay without the capture,
// with a league bar sized without it (yahooLeagueUnitsFor).
const (
	// yahooLeagueStandingsChangeID is the change's GetVersion ID.
	yahooLeagueStandingsChangeID = "yahoo-league-standings"
	// yahooLeagueStandingsVersion is the version that runs the capture.
	yahooLeagueStandingsVersion = 1
)

// yahooFetchCapturesStandings reports whether this execution captures the
// day's standings. The first call records the version in a new execution's
// history; every later call, wherever it is, returns the same answer.
func yahooFetchCapturesStandings(ctx workflow.Context) bool {
	version := workflow.GetVersion(ctx, yahooLeagueStandingsChangeID, workflow.DefaultVersion,
		yahooLeagueStandingsVersion)
	return version != workflow.DefaultVersion
}

// fetchYahooLeagueStandings captures the league's current standings under
// today's date (the activity skips days outside the league's season) and
// reports whether Yahoo serves no standings yet.
func fetchYahooLeagueStandings(ctx workflow.Context, input yahoo.FetchYahooLeagueDataInput,
	bar yahooLeagueBar) (bool, error) {
	if !yahooFetchCapturesStandings(ctx) {
		return false, nil
	}
	var act *yahoo.FetchActivities
	var result yahoo.FetchYahooLeagueStandingsResult
	if err := workflow.ExecuteActivity(ctx, act.FetchYahooLeagueStandings, input).Get(ctx, &result); err != nil {
		return false, err
	}
	bar.advance(ctx)
	return result.Unavailable, nil
}
