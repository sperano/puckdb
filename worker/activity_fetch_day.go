package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/metrics"
)

// dayFetcher encapsulates the sub-activities called by FetchDayActivity.
type dayFetcher interface {
	FetchDailySchedule(ctx context.Context, day time.Time) error
	FetchRoster(ctx context.Context, startYear int, leagueID, teamID int, day time.Time) error
	FetchTeamSummary(ctx context.Context, startYear int, leagueID, teamID int, day time.Time) error
}

// realDayFetcher calls the actual activity functions.
type realDayFetcher struct{}

func (realDayFetcher) FetchDailySchedule(ctx context.Context, day time.Time) error {
	return FetchDailyScheduleActivity(ctx, day)
}

func (realDayFetcher) FetchRoster(ctx context.Context, startYear int, leagueID, teamID int, day time.Time) error {
	return FetchRosterForTeamOnDayActivity(ctx, startYear, leagueID, teamID, day)
}

func (realDayFetcher) FetchTeamSummary(ctx context.Context, startYear int, leagueID, teamID int, day time.Time) error {
	return FetchTeamSummaryForTeamOnDayActivity(ctx, startYear, leagueID, teamID, day)
}

// FetchDayActivity fetches all data for a single day.
// This is the activity version of FetchDayWorkflow, used by FetchSeasonWorkflow
// for better performance (avoiding child workflow overhead).
//
// TeamIDs should be pre-computed by the parent workflow from the Yahoo seasons config.
// If TeamIDs is empty, only NHL data (daily schedule/boxscores) is fetched.
func FetchDayActivity(ctx context.Context, input *FetchDayInput) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("FetchDayActivity", time.Since(start))
	}()

	return fetchDayImpl(ctx, realDayFetcher{}, input)
}

func fetchDayImpl(ctx context.Context, fetcher dayFetcher, input *FetchDayInput) error {
	log.Debug().
		Time("day", input.Day).
		Int("startYear", input.StartYear).
		Int("numTeams", len(input.TeamIDs)).
		Msg("FetchDayActivity started")

	// Fetch daily schedule (boxscores)
	if err := fetcher.FetchDailySchedule(ctx, input.Day); err != nil {
		return err
	}

	// Fetch Yahoo rosters and summaries for each team (if any configured)
	for _, team := range input.TeamIDs {
		if err := fetcher.FetchRoster(ctx, input.StartYear, team.LeagueID, team.TeamID, input.Day); err != nil {
			return err
		}
		if err := fetcher.FetchTeamSummary(ctx, input.StartYear, team.LeagueID, team.TeamID, input.Day); err != nil {
			return err
		}
	}

	return nil
}
