package workflow

import (
	"fmt"

	"github.com/sperano/nhl-api-go/nhl"
	"github.com/sperano/puckdb/internal/core"
	worknhl "github.com/sperano/puckdb/internal/worker/nhl"
	"github.com/sperano/puckdb/internal/worker/shared"
	"go.temporal.io/sdk/workflow"
)

// Group indices for FetchNHLSeasonWorkflow progress.
const (
	GroupFetchSeasonData    = groupSeasonDays
	GroupFetchSeasonPlayoff = groupSeasonPlayoff
)

// NewFetchSeasonProgressReport creates the progress structure for a single season.
// The playoff bar starts with Total 0; it is sized to the season's team count
// (one unit per club-schedule fetch) once ListSeasonTeams has run.
func NewFetchSeasonProgressReport(ctx workflow.Context, season nhl.SeasonInfo) *shared.ProgressReport {
	days := shared.CountDaysInSeason(ctx, season)
	return &shared.ProgressReport{
		Total: days,
		Groups: []shared.ProgressGroup{
			{Header: fmt.Sprintf("Fetching %s...", season.Label()), Bars: []shared.ProgressBar{{Total: days}}},
			{Header: "Fetching playoff games...", Bars: []shared.ProgressBar{{Total: 0}}},
		},
	}
}

// FetchNHLSeasonWorkflow fetches day-level data for a single season: boxscores,
// game stories, shifts, and per-day Yahoo team data for any configured Yahoo
// leagues. Yahoo league/team metadata itself, and league-level data
// (transactions, draft results, matchups), are fetched independently by
// FetchYahooSeasonWorkflow, which the parent season-sync workflow runs at the
// same time (see seasonSyncGroups); the day loop does not need its files
// (FetchDay resolves the game key itself).
// The result counts one origin per unit of progress: each day's daily
// schedule and each team's club schedule.
// Each season runs in its own child workflow to isolate history: a typical
// season (~270 days, 32 clubs) records roughly 1,250-1,950 history events,
// well under the 50K limit. Every activity costs 3 events; the day pool's
// batched progress costs about 27 MarkerRecorded events instead of about 270,
// while every other progress save costs one. Every workflow task costs up to
// 3 events (measured on the dev server; see the typed-worker-pool API plan).
func FetchNHLSeasonWorkflow(ctx workflow.Context, season nhl.SeasonInfo) (core.OriginCounts, error) {
	return runSeasonDays(ctx, season, fetchSeasonDaysMode())
}

func fetchSeasonDaysMode() seasonDaysMode {
	var sa *worknhl.SeasonsActivities
	var pa *worknhl.PlayoffActivities
	return seasonDaysMode{
		sync:           seasonSyncFetch,
		startDaysFirst: true,
		report:         NewFetchSeasonProgressReport,
		season:         seasonActivities{rosters: sa.FetchSeasonRosters, clubStats: sa.FetchClubStats},
		planDays:       planFetchDays,
		playoff: playoffMode{
			activity: pa.FetchTeamPlayoffGames,
			input: func(startYear int, team string) any {
				return worknhl.FetchTeamPlayoffGamesInput{Season: startYear, TeamAbbrev: team}
			},
			decode: decodeFetchPlayoffs,
			message: func(totals playoffTotals, season nhl.SeasonInfo, elapsed string) string {
				return fmt.Sprintf("Fetched %d playoff games for %s in %s.", totals.games, season.Label(), elapsed)
			},
		},
	}
}

// planFetchDays schedules FetchDay per day, with the longer timeout of
// rate-limited Yahoo downloads.
func planFetchDays(ctx workflow.Context, plan seasonDaysPlan, result *seasonDaysResult) dayLoop {
	dayCtx := workflow.WithActivityOptions(ctx, shared.YahooDownloadActivityOptions())
	var dsa *worknhl.DailyScheduleActivities
	return dayLoop{
		start: func(_ workflow.Context, i int) workflow.Future {
			return workflow.ExecuteActivity(dayCtx, dsa.FetchDay, worknhl.FetchDayInput{
				Day:         plan.day(i),
				StartSeason: plan.season.ID.StartYear(),
				TeamIDs:     plan.teamIDs,
				DayIndex:    i,
				TotalDays:   plan.numDays,
			})
		},
		handle: shared.AggregateInto[core.OriginCounts](result.origins),
	}
}

func decodeFetchPlayoffs(ctx workflow.Context, f workflow.Future) (playoffTotals, error) {
	var res worknhl.FetchTeamPlayoffGamesResult
	if err := f.Get(ctx, &res); err != nil {
		return playoffTotals{}, err
	}
	return playoffTotals{origins: res.Origins, games: res.GamesFetched}, nil
}

// WorkflowIDFetchNHLSeason identifies an NHL-only season child workflow.
func WorkflowIDFetchNHLSeason(startYear int) string {
	return fmt.Sprintf("fetch-nhl-season-%d", startYear)
}
