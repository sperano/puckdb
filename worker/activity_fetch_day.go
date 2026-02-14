package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/metrics"
)

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

	log.Debug().
		Time("day", input.Day).
		Int("startYear", input.StartYear).
		Int("numTeams", len(input.TeamIDs)).
		Msg("FetchDayActivity started")

	// Fetch daily schedule (boxscores)
	if err := FetchDailyScheduleActivity(ctx, input.Day); err != nil {
		return err
	}

	// Fetch Yahoo rosters and summaries for each team (if any configured)
	for _, team := range input.TeamIDs {
		if err := FetchRosterForTeamOnDayActivity(ctx, input.StartYear, team.LeagueID, team.TeamID, input.Day); err != nil {
			return err
		}
		if err := FetchTeamSummaryForTeamOnDayActivity(ctx, input.StartYear, team.LeagueID, team.TeamID, input.Day); err != nil {
			return err
		}
	}

	return nil
}
