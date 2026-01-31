package worker

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
	"github.com/sperano/puckdb/metrics"
)

// DownloadDayActivity downloads all data for a single day.
// This is the activity version of DownloadDayWorkflow, used by DownloadSeasonWorkflow
// for better performance (avoiding child workflow overhead).
//
// TeamIDs should be pre-computed by the parent workflow from the Yahoo seasons config.
// If TeamIDs is empty, only NHL data (daily schedule/boxscores) is downloaded.
func DownloadDayActivity(ctx context.Context, input *DownloadDayInput) error {
	start := time.Now()
	defer func() {
		metrics.ObserveActivityDuration("DownloadDayActivity", time.Since(start))
	}()

	log.Debug().
		Time("day", input.Day).
		Int("startYear", input.StartYear).
		Int("numTeams", len(input.TeamIDs)).
		Msg("DownloadDayActivity started")

	// Download daily schedule (boxscores)
	if err := DownloadDailySchedule(ctx, input.Day); err != nil {
		return err
	}

	// Download Yahoo rosters and summaries for each team (if any configured)
	for _, team := range input.TeamIDs {
		if err := DownloadRosterForTeamOnDay(ctx, input.StartYear, team.LeagueID, team.TeamID, input.Day); err != nil {
			return err
		}
		if err := DownloadTeamSummaryForTeamOnDay(ctx, input.StartYear, team.LeagueID, team.TeamID, input.Day); err != nil {
			return err
		}
	}

	return nil
}
